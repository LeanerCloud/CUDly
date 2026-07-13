package purchase

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/execution"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	azureprovider "github.com/LeanerCloud/CUDly/providers/azure"
	gcpprovider "github.com/LeanerCloud/CUDly/providers/gcp"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/google/uuid"
)

// executePurchase performs the actual purchase.
// When the plan has associated cloud accounts and a credential store is configured,
// it fans out execution in parallel — one goroutine per account, each with its own
// PurchaseExecution record tagged with cloud_account_id.
// If no accounts are configured or no credential store is available, it falls back
// to single-account execution using ambient credentials.
// executePurchase runs the purchase for a single execution. When the plan has
// associated cloud accounts it fans out via executeMultiAccount (which saves its
// own per-account records); otherwise it runs the single-account path. The root
// row is always finalized+saved by the caller regardless of which path ran.
func (m *Manager) executePurchase(ctx context.Context, exec *config.PurchaseExecution) (err error) {
	logging.Infof("Executing purchase for plan %q, step %d", exec.PlanID, exec.StepNumber)

	// Direct-execute purchases (Opportunities "Purchase" button) arrive
	// with no associated plan. PlanID is empty and the Postgres UUID
	// column rejects "" with SQLSTATE 22P02, so skip the plan/accounts
	// fetch entirely and synthesize a placeholder plan whose Name is the
	// only field downstream history/notification code reads. By
	// definition direct-execute purchases target a single account, so
	// fall straight through to the legacy single-account path.
	var plan *config.PurchasePlan
	if exec.PlanID == "" {
		plan = &config.PurchasePlan{Name: "Direct purchase"}
	} else {
		plan, err = m.config.GetPurchasePlan(ctx, exec.PlanID)
		if err != nil {
			return fmt.Errorf("failed to get plan: %w", err)
		}
		if plan == nil {
			return fmt.Errorf("plan not found: %s", exec.PlanID)
		}

		// Fan out across plan accounts when accounts are configured.
		if exec.CloudAccountID == nil {
			accounts, err := m.config.GetPlanAccounts(ctx, exec.PlanID)
			if err != nil {
				return fmt.Errorf("failed to load plan accounts for plan %s: %w", exec.PlanID, err)
			}
			if len(accounts) > 0 {
				return m.executeMultiAccount(ctx, exec, plan, accounts)
			}
		}
	}

	// Single-account (legacy) path.
	return m.executeSingleAccount(ctx, exec, plan)
}

// executeSingleAccount runs the legacy single-account purchase path: resolve
// per-account credentials + the target account id, execute the selected recs,
// then notify and classify the outcome. Returns nil on full success, a
// *partialPurchaseError when some recs committed and others failed (#642), or a
// plain error when nothing committed. Split out of executePurchase to keep that
// function under the gocyclo budget.
func (m *Manager) executeSingleAccount(ctx context.Context, exec *config.PurchaseExecution, plan *config.PurchasePlan) error {
	provCfg, targetAccountID, err := m.resolveSingleAccountProvider(ctx, exec)
	if err != nil {
		return err
	}
	// #646: stamp the resolved target account on history, not the ambient
	// AWS host account. resolveSingleAccountProvider returns the target's
	// ExternalID (the provider-appropriate account identifier for AWS /
	// Azure / GCP). Only fall back to the ambient AWS STS identity when no
	// target account could be identified (truly-ambient AWS single-account
	// execution where credentials are inherited from the host).
	accountID := targetAccountID
	if accountID == "" {
		// Fall back to the ambient AWS STS identity for logging/tagging.
		// getAWSAccountID returns ("", error) on failure so the caller
		// can substitute a sentinel rather than baking "unknown" into the
		// purchase history record (05-L2).
		if id, err := m.getAWSAccountID(ctx); err != nil {
			logging.Warnf("purchase[%s]: could not resolve ambient AWS account ID: %v; proceeding with empty account ID",
				exec.ExecutionID, err)
		} else {
			accountID = id
		}
	}

	totalSavings, totalUpfront, purchaseErrors := m.processPurchaseRecommendations(ctx, exec, plan, accountID, provCfg)

	if len(purchaseErrors) > 0 && !anyRecPurchased(exec.Recommendations) {
		// Nothing committed — a clean total failure.
		return fmt.Errorf("some purchases failed: %v", purchaseErrors)
	}

	// At least one rec committed (full or partial success): the confirmation
	// must go out for the recs that purchased. buildPurchaseConfirmationData
	// only lists Purchased recs, so a partial run notifies on exactly those.
	if notifyErr := m.sendPurchaseNotification(ctx, exec, plan, totalSavings, totalUpfront); notifyErr != nil {
		logging.Errorf("Failed to send confirmation: %v", notifyErr)
	}

	if len(purchaseErrors) > 0 {
		// #642: partial success. Real commitments were written to
		// purchase_history with Purchased=true; never mark the row "failed"
		// (it would invite a re-approve that double-buys the purchased recs).
		// Return a sentinel so finalizeExecution records "partially_completed".
		return &partialPurchaseError{errors: purchaseErrors}
	}
	return nil
}

// anyRecPurchased reports whether at least one recommendation in the slice
// was marked Purchased=true by the aggregator (a real commitment was created
// on the cloud provider). It is the gate that distinguishes a partial success
// (#642 — some recs committed, some failed) from a total failure (nothing
// committed, e.g. credential resolution failed before any rec ran).
func anyRecPurchased(recs []config.RecommendationRecord) bool {
	for i := range recs {
		if recs[i].Purchased {
			return true
		}
	}
	return false
}

// multiAccountPartialError is the sentinel returned by executeMultiAccount when
// at least one account committed a real purchase while one or more others
// failed (issue #1014). It is the multi-account analog of partialPurchaseError:
// the executor entry points must NOT treat this as a flat failure — the
// per-account rows already own their authoritative status (partially_completed /
// completed / failed) and real commitments exist, so an SQS/cron caller must ACK
// the message (not redeliver) to avoid re-running the fan-out and double-buying
// the accounts that already succeeded (which #1012's stable key would otherwise
// dedupe, but the contract should not depend on that second line of defense).
type multiAccountPartialError struct {
	errors    []string
	committed int
}

func (e *multiAccountPartialError) Error() string {
	return fmt.Sprintf("multi-account execution: %d account(s) committed, %d failed: %s",
		e.committed, len(e.errors), strings.Join(e.errors, "; "))
}

// errAllAccountsFailed is returned by executeMultiAccount when no account
// committed any purchase — a flat failure that callers may redeliver/retry.
var errAllAccountsFailed = errors.New("multi-account execution: all accounts failed")

// executeMultiAccount fans out executePurchase across all plan accounts in parallel.
// Each account gets its own PurchaseExecution record tagged with cloud_account_id.
//
// Returns nil when every account fully succeeded; a *multiAccountPartialError
// when at least one account committed a purchase and at least one failed (so
// callers ACK rather than redeliver, issue #1014); and errAllAccountsFailed
// (wrapping the per-account errors) when no account committed anything.
func (m *Manager) executeMultiAccount(ctx context.Context, baseExec *config.PurchaseExecution, plan *config.PurchasePlan, accounts []config.CloudAccount) error {
	results := execution.RunForAccountsWithConcurrency(ctx, accounts, func(ctx context.Context, account config.CloudAccount) (bool, error) {
		return m.executeForAccount(ctx, baseExec, plan, account)
	}, getMaxAccountParallelism())

	committed := 0
	var errs []string
	for _, r := range results {
		if r.Value {
			// At least one rec committed for this account (full or partial).
			committed++
		}
		if r.Err != nil {
			errs = append(errs, fmt.Sprintf("account %s: %v", r.AccountID, r.Err))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	if committed > 0 {
		// Some accounts committed real purchases; the per-account rows already
		// recorded the truth. Surface a partial sentinel so the entry points
		// ack and never mislabel the run as a flat failure (issue #1014).
		return &multiAccountPartialError{committed: committed, errors: errs}
	}
	return fmt.Errorf("%w: %s", errAllAccountsFailed, strings.Join(errs, "; "))
}

// saveExecutionStatusBestEffort saves the execution record and logs any error.
// Used in error paths where we need to persist the failure status but cannot
// propagate the save error (the original error is already being returned).
func (m *Manager) saveExecutionStatusBestEffort(ctx context.Context, exec *config.PurchaseExecution) {
	if err := m.config.SavePurchaseExecution(ctx, exec); err != nil {
		logging.Warnf("execution: failed to persist error status for account %v: %v", exec.CloudAccountID, err)
	}
}

// executeForAccount runs a single plan execution for one cloud account.
// It creates a new PurchaseExecution record tagged with cloud_account_id, resolves
// per-account credentials, executes purchases, and saves the result.
//
// Returns committed=true when at least one rec committed a real purchase for
// this account (full or partial success), so executeMultiAccount can tell a
// partial run (some accounts bought) apart from a flat failure and pick the
// right sentinel (issue #1014). The returned error is non-nil whenever any rec
// failed, but the authoritative per-account row has already been saved with the
// correct status (partially_completed / failed) before it surfaces.
func (m *Manager) executeForAccount(ctx context.Context, baseExec *config.PurchaseExecution, plan *config.PurchasePlan, account config.CloudAccount) (committed bool, err error) { //nolint:gocritic // hugeParam: by-value per calling convention
	// Create a per-account copy of the execution record with an independent
	// Recommendations slice so concurrent goroutines don't race on writes.
	acctID := account.ID
	acctExec := *baseExec
	// ExecutionID is the per-account row identity (still a fresh UUID), but the
	// per-rec provider idempotency token must NOT derive from it: a re-drive of
	// the root execution would mint new UUIDs and lose the provider dedupe,
	// double-buying every account (issue #1012 / H1). Seed a STABLE per-account
	// idempotency key from the root's lineage key + the account ID so a re-drive
	// reproduces the identical token per account. The account ID is the cloud
	// account's stable config ID, not the (also stable) ExternalID — either is
	// durable, but account.ID is the value that uniquely keys the fan-out unit.
	acctExec.ExecutionID = uuid.New().String()
	acctExec.IdempotencyKey = idempotencyLineageKey(baseExec) + ":" + account.ID
	acctExec.CloudAccountID = &acctID
	recs := make([]config.RecommendationRecord, len(baseExec.Recommendations))
	copy(recs, baseExec.Recommendations)
	acctExec.Recommendations = recs

	provCfg, err := m.resolveAccountProvider(ctx, account)
	if err != nil {
		acctExec.Status = "failed"
		acctExec.Error = err.Error()
		m.saveExecutionStatusBestEffort(ctx, &acctExec)
		return false, fmt.Errorf("credential resolution failed for account %s: %w", account.ID, err)
	}

	accountID := account.ExternalID
	totalSavings, totalUpfront, purchaseErrors := m.processPurchaseRecommendations(ctx, &acctExec, plan, accountID, provCfg)

	// #642: a per-account run can be a partial success — some recs committed
	// to purchase_history (Purchased=true) while others failed. Marking the
	// row "failed" in that case is wrong (it hides the real commitments and
	// invites a re-approve that double-buys). Record "partially_completed"
	// instead so the row reflects reality, and still send the confirmation
	// for the recs that did purchase.
	partial := len(purchaseErrors) > 0 && anyRecPurchased(acctExec.Recommendations)
	switch {
	case partial:
		now := time.Now()
		acctExec.Status = "partially_completed"
		// Append so any audit-gap note already stamped by
		// aggregatePurchaseOutcomes (issue #621) survives alongside the
		// per-rec failure list.
		acctExec.Error = appendErrNote(acctExec.Error, strings.Join(purchaseErrors, "; "))
		acctExec.CompletedAt = &now
	case len(purchaseErrors) > 0:
		acctExec.Status = "failed"
		acctExec.Error = appendErrNote(acctExec.Error, strings.Join(purchaseErrors, "; "))
	default:
		now := time.Now()
		acctExec.Status = "completed"
		acctExec.CompletedAt = &now
	}

	// committed is the gate executeMultiAccount uses to distinguish a partial
	// run from a flat failure (issue #1014): true when any rec purchased.
	committed = anyRecPurchased(acctExec.Recommendations)

	if saveErr := m.config.SavePurchaseExecution(ctx, &acctExec); saveErr != nil {
		return committed, fmt.Errorf("AUDIT LOSS: failed to save execution record for account %s: %w", account.ID, saveErr)
	}

	// Send the confirmation whenever at least one rec committed (full or
	// partial success). buildPurchaseConfirmationData only lists Purchased
	// recs, so a partial run notifies on exactly the recs that fired.
	if len(purchaseErrors) == 0 || partial {
		if err := m.sendPurchaseNotification(ctx, &acctExec, plan, totalSavings, totalUpfront); err != nil {
			logging.Errorf("Failed to send confirmation for account %s: %v", account.ID, err)
		}
	}

	// Surface the per-rec failures to the multi-account aggregator so the
	// overall run reflects that not everything succeeded, even though the
	// authoritative per-account row was already saved as partially_completed
	// (not failed) and the confirmation already went out.
	if len(purchaseErrors) > 0 {
		return committed, fmt.Errorf("some purchases failed: %v", purchaseErrors)
	}
	return committed, nil
}

// resolveSingleAccountProvider derives per-account credentials for the
// single-account execution path. Returns (nil, "", nil) when no account can be
// identified (ambient credentials are used in that case), or when no recs are
// selected (approval-only flows where credentials are not needed).
//
// The second return value is the resolved target account's ExternalID — the
// provider-appropriate account identifier (AWS account number, Azure
// subscription, GCP project) that the caller stamps onto purchase_history
// (#646). It is "" when no target account could be identified, signaling the
// caller to fall back to the ambient AWS STS identity.
//
// The account is taken from exec.CloudAccountID when set (plan-with-single-
// account executions), or derived from the shared cloud_account_id on the
// recommendations when all selected recs agree on exactly one account (direct-
// execute purchases where PlanID is empty and exec.CloudAccountID is nil).
//
// A non-nil error means a target account was identified but could not be
// resolved (lookup failed, the account does not exist, or credentials could
// not be derived); the caller must NOT fall back to ambient credentials on
// error, as that would purchase/stamp against the wrong account (#646).
func (m *Manager) resolveSingleAccountProvider(ctx context.Context, exec *config.PurchaseExecution) (*provider.ProviderConfig, string, error) {
	// Skip credential resolution when nothing is selected. Approval-only
	// flows may carry an account ID on the recs solely for the contact-email
	// gate; attempting resolution there would fail on accounts with no
	// provider field set and add a pointless DB round-trip.
	if len(selectedIndices(exec.Recommendations)) == 0 {
		return nil, "", nil
	}

	cloudAccountID := exec.CloudAccountID
	if cloudAccountID == nil {
		cloudAccountID = singleCloudAccountIDFromRecs(exec.Recommendations)
	}
	if cloudAccountID == nil {
		return nil, "", nil
	}

	account, err := m.config.GetCloudAccount(ctx, *cloudAccountID)
	if err != nil {
		return nil, "", fmt.Errorf("credential resolution failed for account %s: %w", *cloudAccountID, err)
	}
	if account == nil {
		// A target account was identified but does not exist in config.
		// Returning ("") here would let the caller fall back to ambient AWS
		// credentials and purchase/stamp against the wrong account (#646), so
		// surface an error instead; the contract above forbids ambient
		// fallback once a target account ID is known.
		return nil, "", fmt.Errorf("credential resolution failed for account %s: account not found", *cloudAccountID)
	}
	provCfg, err := m.resolveAccountProvider(ctx, *account)
	if err != nil {
		return nil, "", fmt.Errorf("credential resolution failed for account %s: %w", *cloudAccountID, err)
	}
	return provCfg, account.ExternalID, nil
}

// partialPurchaseError is the sentinel returned by the single-account
// execution path when at least one rec purchased and at least one failed
// (#642). finalizeExecution detects it via errors.As and records the row as
// "partially_completed" rather than "failed", so the real commitments stay
// visible and a re-approve can't double-buy them. The successful recs'
// confirmation email has already been sent by the time this error surfaces.
type partialPurchaseError struct {
	errors []string
}

func (e *partialPurchaseError) Error() string {
	return "some purchases failed (partial success): " + strings.Join(e.errors, "; ")
}

// resolveAccountProvider returns a *ProviderConfig with a pre-authenticated provider
// for the given account. Returns an error if credential resolution fails -- callers
// must NOT fall back to ambient credentials on error.
func (m *Manager) resolveAccountProvider(ctx context.Context, account config.CloudAccount) (*provider.ProviderConfig, error) { //nolint:gocritic // hugeParam: by-value per calling convention
	t0 := time.Now()
	logging.Infof("purchase[resolveAccountProvider]: resolving credentials for provider=%s account=%s",
		account.Provider, account.ID)
	var cfg *provider.ProviderConfig
	var err error
	switch account.Provider {
	case "aws":
		cfg, err = m.resolveAWSProvider(ctx, account)
	case "azure":
		cfg, err = m.resolveAzureProvider(ctx, account)
	case "gcp":
		cfg, err = m.resolveGCPProvider(ctx, account)
	default:
		return nil, fmt.Errorf("credentials: unknown cloud provider %q for account %s", account.Provider, account.ID)
	}
	if err != nil {
		logging.Errorf("purchase[resolveAccountProvider]: credential resolution failed for provider=%s account=%s after %s: %v",
			account.Provider, account.ID, time.Since(t0), err)
		return nil, err
	}
	logging.Infof("purchase[resolveAccountProvider]: credentials resolved for provider=%s account=%s in %s",
		account.Provider, account.ID, time.Since(t0))
	return cfg, nil
}

func (m *Manager) resolveAWSProvider(ctx context.Context, account config.CloudAccount) (*provider.ProviderConfig, error) { //nolint:gocritic // hugeParam: by-value per calling convention
	t0 := time.Now()
	logging.Infof("purchase[resolveAWSProvider]: resolving AWS credentials for account=%s authMode=%s",
		account.ID, account.AWSAuthMode)
	if account.AWSAuthMode != "access_keys" && m.assumeRoleSTS == nil {
		return nil, fmt.Errorf("credentials: STS client not configured for non-access_keys mode (account %s)", account.ID)
	}
	awsCreds, err := credentials.ResolveAWSCredentialProviderWithOpts(ctx, &account, m.credStore, m.assumeRoleSTS,
		credentials.AWSResolveOptions{AmbientProvider: m.ambientAWSCreds})
	if err != nil {
		logging.Errorf("purchase[resolveAWSProvider]: failed for account=%s after %s: %v",
			account.ID, time.Since(t0), err)
		return nil, fmt.Errorf("credentials: resolve AWS for account %s (%s): %w", account.ID, account.Name, err)
	}
	logging.Infof("purchase[resolveAWSProvider]: AWS credentials resolved for account=%s in %s",
		account.ID, time.Since(t0))
	return &provider.ProviderConfig{Name: "aws", AWSCredentialsProvider: awsCreds}, nil
}

func (m *Manager) resolveAzureProvider(ctx context.Context, account config.CloudAccount) (*provider.ProviderConfig, error) { //nolint:gocritic // hugeParam: by-value per calling convention
	t0 := time.Now()
	logging.Infof("purchase[resolveAzureProvider]: resolving Azure credentials for account=%s authMode=%s",
		account.ID, account.AzureAuthMode)
	if account.AzureAuthMode != "managed_identity" && m.credStore == nil {
		return nil, fmt.Errorf("credentials: credential store required for non-managed_identity Azure account %s", account.ID)
	}
	azCred, err := credentials.ResolveAzureTokenCredentialWithOpts(ctx, &account, m.credStore, credentials.AzureResolveOptions{
		Signer:    m.oidcSigner,
		IssuerURL: m.oidcIssuerURL,
	})
	if err != nil {
		logging.Errorf("purchase[resolveAzureProvider]: token credential resolution failed for account=%s after %s: %v",
			account.ID, time.Since(t0), err)
		return nil, fmt.Errorf("credentials: resolve Azure for account %s (%s): %w", account.ID, account.Name, err)
	}
	azProv, err := azureprovider.NewAzureProvider(&provider.ProviderConfig{Profile: account.AzureSubscriptionID})
	if err != nil {
		logging.Errorf("purchase[resolveAzureProvider]: Azure provider construction failed for account=%s after %s: %v",
			account.ID, time.Since(t0), err)
		return nil, fmt.Errorf("credentials: create Azure provider for account %s (%s): %w", account.ID, account.Name, err)
	}
	azProv.SetCredential(azCred)
	logging.Infof("purchase[resolveAzureProvider]: Azure credentials resolved for account=%s in %s",
		account.ID, time.Since(t0))
	return &provider.ProviderConfig{ProviderOverride: azProv}, nil
}

func (m *Manager) resolveGCPProvider(ctx context.Context, account config.CloudAccount) (*provider.ProviderConfig, error) { //nolint:gocritic // hugeParam: by-value per calling convention
	t0 := time.Now()
	logging.Infof("purchase[resolveGCPProvider]: resolving GCP credentials for account=%s authMode=%s",
		account.ID, account.GCPAuthMode)
	if account.GCPAuthMode != "application_default" && m.credStore == nil {
		return nil, fmt.Errorf("credentials: credential store required for non-ADC GCP account %s", account.ID)
	}
	gcpTS, err := credentials.ResolveGCPTokenSourceWithOpts(ctx, &account, m.credStore, credentials.GCPResolveOptions{
		Signer:    m.oidcSigner,
		IssuerURL: m.oidcIssuerURL,
	})
	if err != nil {
		logging.Errorf("purchase[resolveGCPProvider]: token source resolution failed for account=%s after %s: %v",
			account.ID, time.Since(t0), err)
		return nil, fmt.Errorf("credentials: resolve GCP for account %s (%s): %w", account.ID, account.Name, err)
	}
	if gcpTS == nil {
		// ADC mode: no explicit token source -- use ambient credentials.
		logging.Infof("purchase[resolveGCPProvider]: GCP ADC mode for account=%s (ambient credentials) in %s",
			account.ID, time.Since(t0))
		return nil, nil
	}
	gcpProv := gcpprovider.NewProviderWithCredentials(ctx, account.GCPProjectID, gcpTS)
	logging.Infof("purchase[resolveGCPProvider]: GCP credentials resolved for account=%s in %s",
		account.ID, time.Since(t0))
	return &provider.ProviderConfig{ProviderOverride: gcpProv}, nil
}

// getMaxAccountParallelism is a thin alias over the shared
// execution.ConcurrencyFromEnv so the purchase manager and the scheduler
// both honor the same CUDLY_MAX_ACCOUNT_PARALLELISM override.
func getMaxAccountParallelism() int {
	return execution.ConcurrencyFromEnv()
}

// recPurchaseOutcome carries the result of a single fan-out unit so the
// aggregator can write back into exec.Recommendations and call
// savePurchaseHistory from a single goroutine (no concurrent map / slice
// mutation). The index field is the position in exec.Recommendations.
type recPurchaseOutcome struct {
	purchase common.PurchaseResult
	err      error
	index    int
}

func (m *Manager) processPurchaseRecommendations(ctx context.Context, exec *config.PurchaseExecution, plan *config.PurchasePlan, accountID string, provCfg *provider.ProviderConfig) (float64, float64, []string) { //nolint:gocritic // unnamedResult: return names would conflict with body locals
	// ExecutionID is carried into PurchaseOptions so executeSinglePurchase
	// can tag every per-rec log line with the owning exec UUID. Without
	// this, CloudWatch filtering by exec ID returns zero hits and a stuck
	// or failed execution (e.g. issue #667's context-deadline timeout)
	// can't be correlated to the cloud SDK call that produced it.
	opts := common.PurchaseOptions{
		Source:      m.normalizePurchaseSource(exec),
		ExecutionID: exec.ExecutionID,
	}

	// Build the list of selected indices once so the fan-out closure only
	// has to look up rec[i] (no second pass over the full slice).
	selected := selectedIndices(exec.Recommendations)
	if len(selected) == 0 {
		logging.Infof("purchase[%s]: no selected recommendations, nothing to execute (account=%s plan=%q)",
			exec.ExecutionID, accountID, plan.Name)
		return 0, 0, nil
	}
	logging.Infof("purchase[%s]: dispatching %d recommendation(s) for account=%s plan=%q",
		exec.ExecutionID, len(selected), accountID, plan.Name)

	// Each rec runs in its own goroutine so a multi-rec execution that
	// spans providers (AWS RI + Azure reservation + GCP CUD) or services
	// (EC2 + RDS + ElastiCache + OpenSearch within AWS) makes its cloud
	// API calls in parallel rather than blocking on the slowest one.
	// Provider client construction inside executeSinglePurchase is
	// independent per call, so cross-provider parallelism is safe.
	// Concurrency is capped at getMaxAccountParallelism so the same
	// operator-level CUDLY_MAX_ACCOUNT_PARALLELISM knob covers both the
	// outer account fan-out and this inner rec fan-out.
	results := execution.FanOutWithConcurrency(ctx, indexKeys(selected),
		func(ctx context.Context, key string) (recPurchaseOutcome, error) {
			// indexKeys() builds these keys from selectedIndices(), so parse +
			// bounds errors are not expected at runtime. Surface them as
			// closure-level errors anyway so the aggregator can correlate
			// them via execution.Result.Err and never silently dereference a
			// zero-valued outcome at index 0.
			i, parseErr := strconv.Atoi(key)
			if parseErr != nil {
				return recPurchaseOutcome{}, fmt.Errorf("invalid fan-out key %q: %w", key, parseErr)
			}
			if i < 0 || i >= len(exec.Recommendations) {
				return recPurchaseOutcome{}, fmt.Errorf("fan-out index %d out of range for %d recommendations", i, len(exec.Recommendations))
			}
			rec := exec.Recommendations[i]
			logging.Infof("Purchasing: %dx %s in %s (%s/%s)", rec.Count, rec.ResourceType, rec.Region, rec.Provider, rec.Service)
			// Derive a deterministic per-rec idempotency token from the
			// execution's STABLE lineage key (not its mutable ExecutionID)
			// and this rec's index so a re-drive of a stranded execution
			// reuses the identical token and the commitment is never created
			// twice. The lineage key survives Retry and multi-account fan-out
			// (issue #1012), where the ExecutionID is regenerated; deriving
			// from the ExecutionID directly defeated the provider guard on
			// exactly those re-drive paths (#636/#639). opts is a value, so
			// this per-rec copy is safe under the parallel fan-out (no shared
			// mutation across goroutines).
			recOpts := opts
			recOpts.IdempotencyToken = common.DeriveIdempotencyToken(idempotencyLineageKey(exec), i)
			purchaseResult, err := m.executeSinglePurchase(ctx, rec, provCfg, recOpts)
			return recPurchaseOutcome{index: i, purchase: purchaseResult, err: err}, nil
		}, getMaxAccountParallelism())

	// aggregatePurchaseOutcomes is intentionally single-threaded: the fan-out
	// results are collected above, and the aggregator walks them serially so
	// there are no concurrent writes to totals, exec.Recommendations, or
	// purchaseErrors (05-N2). Do NOT move the aggregation inside the FanOut
	// closure or run it concurrently with the fan-out.
	return m.aggregatePurchaseOutcomes(ctx, exec, plan, accountID, results)
}

// aggregatePurchaseOutcomes walks the fan-out results serially and writes
// each rec's outcome back to exec.Recommendations + savePurchaseHistory.
// Extracted so processPurchaseRecommendations stays under gocyclo:10 and
// so the aggregation logic is single-threaded — no concurrent writes to
// totals, purchaseErrors, or exec.Recommendations[i] regardless of how
// many recs ran in parallel.
func (m *Manager) aggregatePurchaseOutcomes(ctx context.Context, exec *config.PurchaseExecution, plan *config.PurchasePlan, accountID string, results []execution.Result[recPurchaseOutcome]) (float64, float64, []string) { //nolint:gocritic // unnamedResult: return names would conflict with body locals
	var totalSavings, totalUpfront float64
	var purchaseErrors []string
	for _, r := range results {
		// Closure-level error (parse / bounds / framework). r.Value is the
		// zero recPurchaseOutcome here — its index is 0 and would mis-target
		// exec.Recommendations[0] if blindly trusted. Record it as a
		// generic execution failure and skip the per-rec writes.
		if r.Err != nil {
			logging.Errorf("Internal fan-out failure during purchase execution: %v", r.Err)
			purchaseErrors = append(purchaseErrors, fmt.Sprintf("internal fan-out error: %v", r.Err))
			continue
		}
		v := r.Value
		i := v.index
		// Defense-in-depth: even with the closure's bounds check, never
		// index past exec.Recommendations here (a future refactor that
		// mutates the slice between fan-out and aggregation would corrupt
		// this otherwise).
		if i < 0 || i >= len(exec.Recommendations) {
			logging.Errorf("Aggregator received out-of-range index %d (len=%d)", i, len(exec.Recommendations))
			purchaseErrors = append(purchaseErrors, fmt.Sprintf("aggregator index %d out of range", i))
			continue
		}
		rec := exec.Recommendations[i]
		if v.err != nil {
			logging.Errorf("Failed to purchase %s: %v", rec.ResourceType, v.err)
			exec.Recommendations[i].Error = v.err.Error()
			purchaseErrors = append(purchaseErrors, fmt.Sprintf("%s: %v", rec.ResourceType, v.err))
			continue
		}
		exec.Recommendations[i].Purchased = true
		exec.Recommendations[i].PurchaseID = v.purchase.CommitmentID
		totalSavings += rec.Savings
		totalUpfront += rec.UpfrontCost
		if histErr := m.savePurchaseHistory(ctx, exec, plan, rec, v.purchase, accountID); histErr != nil {
			// The purchase SUCCEEDED but its purchase_history row failed to
			// persist. Do NOT add this to purchaseErrors — that would flip the
			// execution to "failed" and tempt the user to re-approve a purchase
			// that already fired (the exact double-spend trap issue #621 warns
			// about). Instead stamp an audit-gap marker on the execution so it
			// stays "completed" but visible in the History view despite the
			// missing purchase_history row.
			recordHistoryAuditGap(exec, v.purchase.CommitmentID, histErr)
		}
	}
	return totalSavings, totalUpfront, purchaseErrors
}

// recordHistoryAuditGap stamps exec.Error with a note that a successful
// purchase's history record could not be saved (issue #621). The execution
// keeps a successful status; the marker is what makes the row visible in the
// History view (which synthesizes completed executions that carry an Error).
// Appends rather than overwrites so multiple failed history writes within one
// execution are all recorded.
// historyAuditGapPrefix is the structured prefix stamped on exec.Error by
// recordHistoryAuditGap. Using a fixed prefix makes these entries queryable
// in CloudWatch Insights / log aggregators without grepping free-form text
// (05-L1). The prefix is intentionally stable; changing it invalidates any
// existing dashboards or alerts that pattern-match on it.
const historyAuditGapPrefix = "history_write_failed"

func recordHistoryAuditGap(exec *config.PurchaseExecution, commitmentID string, histErr error) {
	// histErr is a raw persistence error: log it server-side for diagnosis but
	// keep it out of exec.Error, which is surfaced to clients via the history
	// status_description. Only a generic, user-safe note reaches the UI.
	logging.Errorf("history audit gap for commitment %s: %v", commitmentID, histErr)
	// Structured marker: historyAuditGapPrefix + ":" + commitmentID so log
	// queries can filter on "history_write_failed" and operators can correlate
	// the commitment ID to the cloud provider's record without parsing prose.
	note := fmt.Sprintf("%s: commitment %s purchased but its history record failed to save",
		historyAuditGapPrefix, commitmentID)
	exec.Error = appendErrNote(exec.Error, note)
}

// idempotencyLineageKey returns the stable anchor the per-rec provider
// idempotency token is derived from (issue #1012). It prefers the durable
// IdempotencyKey column, which is generated once at first creation and copied
// verbatim onto every Retry successor and combined with the account ID for each
// multi-account fan-out row — so a strand-and-re-drive reproduces the same
// token and the provider dedupes the purchase. It falls back to ExecutionID
// only for legacy rows persisted before migration 000066 (IdempotencyKey == "");
// for a single un-retried execution that fallback is identical to the pre-fix
// behavior, and such legacy rows never gain a retry successor that could
// diverge (the retry handler seeds the successor's key from the predecessor's
// ExecutionID in that case, preserving the match).
func idempotencyLineageKey(exec *config.PurchaseExecution) string {
	if exec.IdempotencyKey != "" {
		return exec.IdempotencyKey
	}
	return exec.ExecutionID
}

// appendErrNote joins note onto an existing error string with "; ",
// returning note alone when existing is empty. Shared by the audit-gap and
// partial-failure paths so a successful-rec audit gap (#621) and a per-rec
// failure list (#642) within the same execution are both preserved on
// exec.Error rather than one clobbering the other.
func appendErrNote(existing, note string) string {
	if existing == "" {
		return note
	}
	if note == "" {
		return existing
	}
	return existing + "; " + note
}

// normalizePurchaseSource canonicalizes exec.Source for downstream tag
// stamping. Defense-in-depth: NormalizeSource rejects anything outside
// the allowed whitelist; an unexpected value (DB tampering, future
// code path) is dropped to "" rather than fed onto a cloud commitment
// where it would be expensive to retract.
//
// On invalid source the purchase PROCEEDS UNTAGGED (05-L3 decision):
// failing the rec over a tag-only field would abort a successful cloud
// purchase, which is a worse outcome than a missing tag. Input
// validation at the API write boundary (exec.Source on save) is the
// correct gate; this fallback is last-resort defense-in-depth.
func (m *Manager) normalizePurchaseSource(exec *config.PurchaseExecution) string {
	source := exec.Source
	if source == "" {
		return ""
	}
	normalized, err := common.NormalizeSource(source)
	if err != nil {
		logging.Warnf("Invalid purchase source %q on execution %s: %v, proceeding untagged", source, exec.ExecutionID, err)
		return ""
	}
	return normalized
}

// selectedIndices returns the positions of recs with Selected=true,
// preserving slice order so the aggregator's pass over the fan-out
// results writes back to exec.Recommendations deterministically.
func selectedIndices(recs []config.RecommendationRecord) []int {
	out := make([]int, 0, len(recs))
	for i, rec := range recs { //nolint:gocritic // rangeValCopy: acceptable value copy
		if rec.Selected {
			out = append(out, i)
		}
	}
	return out
}

// indexKeys formats a slice of indices as string keys for FanOut.
func indexKeys(idx []int) []string {
	out := make([]string, len(idx))
	for i, n := range idx {
		out[i] = strconv.Itoa(n)
	}
	return out
}

// singleCloudAccountIDFromRecs returns the shared cloud_account_id when
// all recommendations in the slice agree on exactly one non-empty account ID.
// Returns nil when the slice is empty, all IDs are absent, or more than one
// distinct ID is present (the caller falls back to ambient credentials in the
// first two cases and to fan-out in the last).
func singleCloudAccountIDFromRecs(recs []config.RecommendationRecord) *string {
	var found *string
	for i := range recs {
		id := recs[i].CloudAccountID
		if id == nil || *id == "" {
			continue
		}
		if found == nil {
			found = id
		} else if *found != *id {
			// More than one distinct account — not the single-account path.
			return nil
		}
	}
	return found
}

// savePurchaseHistory persists one purchase_history row for a successful
// commitment. It returns the store error (rather than only logging it) so the
// caller can record an audit gap on the execution: a swallowed failure here
// used to leave the execution silently "completed" with no purchase_history
// row, making the purchase invisible in the History view (issue #621).
func (m *Manager) savePurchaseHistory(ctx context.Context, exec *config.PurchaseExecution, plan *config.PurchasePlan, rec config.RecommendationRecord, result common.PurchaseResult, accountID string) error { //nolint:gocritic // hugeParam: by-value per calling convention
	purchasedAt := time.Now()
	historyRecord := &config.PurchaseHistoryRecord{
		AccountID:        accountID,
		PurchaseID:       result.CommitmentID,
		Timestamp:        purchasedAt,
		Provider:         rec.Provider,
		Service:          rec.Service,
		Region:           rec.Region,
		ResourceType:     rec.ResourceType,
		Count:            rec.Count,
		Term:             rec.Term,
		Payment:          rec.Payment,
		UpfrontCost:      result.Cost,
		MonthlyCost:      rec.MonthlyCost,
		EstimatedSavings: rec.Savings,
		PlanID:           exec.PlanID,
		PlanName:         plan.Name,
		RampStep:         exec.StepNumber,
		CloudAccountID:   exec.CloudAccountID,
		Source:           exec.Source,
		// Stamp the in-app free-cancel window so the History UI can offer the
		// Revoke button (issue #290). Azure-only in Phase 1; nil for AWS/GCP.
		RevocationWindowClosesAt: config.RevocationWindowClosesAtFor(rec.Provider, purchasedAt),
	}
	if err := m.config.SavePurchaseHistory(ctx, historyRecord); err != nil {
		logging.Errorf("Failed to save history: %v", err)
		return err
	}
	return nil
}

func (m *Manager) sendPurchaseNotification(ctx context.Context, exec *config.PurchaseExecution, plan *config.PurchasePlan, totalSavings, totalUpfront float64) error {
	data := m.buildPurchaseConfirmationData(exec, plan, totalSavings, totalUpfront)
	return m.email.SendPurchaseConfirmation(ctx, data)
}

func (m *Manager) buildPurchaseConfirmationData(exec *config.PurchaseExecution, plan *config.PurchasePlan, totalSavings, totalUpfront float64) email.NotificationData {
	dashboardBase := strings.TrimRight(m.dashboardURL, "/")
	data := email.NotificationData{
		DashboardURL:     dashboardBase,
		TotalSavings:     totalSavings,
		TotalUpfrontCost: totalUpfront,
		PlanName:         plan.Name,
	}
	if dashboardBase != "" {
		data.ArcheraEducationURL = dashboardBase + "/archera-insurance"
	}

	for _, rec := range exec.Recommendations { //nolint:gocritic // rangeValCopy: acceptable value copy
		if rec.Purchased {
			data.Recommendations = append(data.Recommendations, email.RecommendationSummary{
				Service:        rec.Service,
				ResourceType:   rec.ResourceType,
				Engine:         rec.Engine,
				Region:         rec.Region,
				Count:          rec.Count,
				MonthlySavings: rec.Savings,
			})
		}
	}

	return data
}

// logRecCtxErr emits a diagnostic log line when a per-recommendation context has
// been canceled or timed out. It distinguishes DeadlineExceeded (the 30s per-rec
// budget fired) from Canceled (a parent context stopped the execution) so that
// CloudWatch filters can tell the two apart without parsing error strings.
// It is a no-op when recCtxErr is nil.
func logRecCtxErr(executionID, recTuple string, elapsed time.Duration, recCtxErr error) {
	if recCtxErr == nil {
		return
	}
	if errors.Is(recCtxErr, context.DeadlineExceeded) {
		logging.Errorf("purchase[%s]: per-rec deadline exceeded for %s (elapsed=%s)",
			executionID, recTuple, elapsed)
	} else if errors.Is(recCtxErr, context.Canceled) {
		logging.Errorf("purchase[%s]: recommendation context canceled for %s (elapsed=%s)",
			executionID, recTuple, elapsed)
	}
}

// executeSinglePurchase executes a single purchase using the appropriate provider.
// provCfg carries optional per-account credentials; pass nil to use ambient credentials.
// opts carries execution-level metadata (the source surface) that providers stamp
// onto the commitment they create.
func (m *Manager) executeSinglePurchase(ctx context.Context, rec config.RecommendationRecord, provCfg *provider.ProviderConfig, opts common.PurchaseOptions) (common.PurchaseResult, error) { //nolint:gocritic // hugeParam: by-value per calling convention
	// Per-purchase Info logs tagged with the owning execution ID so a
	// CloudWatch filter on the execution UUID surfaces every step of the
	// purchase attempt -- provider construction, service-client lookup,
	// details decode, the cloud SDK call, and the result. Issue #667
	// surfaced that the prior flow had ZERO observable signal between the
	// approve handler and the final aggregated error toast; a timed-out
	// SDK call left no trace in CloudWatch beyond the wrapped error.
	logging.Infof("purchase[%s]: starting rec %s/%s/%s/%s (count=%d term=%dyr payment=%s)",
		opts.ExecutionID, rec.Provider, rec.Service, rec.Region, rec.ResourceType, rec.Count, rec.Term, rec.Payment)

	// Cap this individual rec at 30s so one hung rec (SDK retry storm, STS
	// hang, pagination hang) cannot exhaust the Lambda budget and starve the
	// other recs running in parallel in the same execution. 30s matches the
	// purchasecfg hard limits (2 retries * 15s HTTP timeout) from issue #683.
	// The parent ctx governs the overall Lambda budget; this per-rec deadline
	// is always the shorter of the two.
	recTuple := fmt.Sprintf("%s/%s/%s/%s", rec.Provider, rec.Service, rec.Region, rec.ResourceType)
	recCtx, recCancel := context.WithTimeout(ctx, 30*time.Second)
	defer recCancel()

	// Create the provider
	t0 := time.Now()
	cloudProvider, err := m.providerFactory.CreateAndValidateProvider(recCtx, rec.Provider, provCfg)
	if err != nil {
		logging.Errorf("purchase[%s]: provider construction failed for %s after %s: %v",
			opts.ExecutionID, rec.Provider, time.Since(t0), err)
		return common.PurchaseResult{}, fmt.Errorf("failed to create %s provider: %w", rec.Provider, err)
	}
	logging.Infof("purchase[%s]: provider %s constructed in %s", opts.ExecutionID, rec.Provider, time.Since(t0))

	// Map service string to ServiceType
	serviceType := m.mapServiceType(rec.Service)

	// Get the service client for this region
	t0 = time.Now()
	serviceClient, err := cloudProvider.GetServiceClient(recCtx, serviceType, rec.Region)
	if err != nil {
		logging.Errorf("purchase[%s]: service client lookup failed for %s/%s in %s after %s: %v",
			opts.ExecutionID, rec.Provider, serviceType, rec.Region, time.Since(t0), err)
		return common.PurchaseResult{}, fmt.Errorf("failed to get service client: %w", err)
	}
	logging.Infof("purchase[%s]: service client %s/%s/%s ready in %s",
		opts.ExecutionID, rec.Provider, serviceType, rec.Region, time.Since(t0))

	// Build the recommendation in common format
	recommendation := common.Recommendation{
		Provider:       common.ProviderType(rec.Provider),
		Service:        serviceType,
		Region:         rec.Region,
		ResourceType:   rec.ResourceType,
		Count:          rec.Count,
		Term:           fmt.Sprintf("%dyr", rec.Term),
		PaymentOption:  rec.Payment,
		CommitmentCost: rec.UpfrontCost,
	}

	// Reconstruct the typed *Details pointer from the persisted JSON
	// payload (issue #453). The scheduler stores the full
	// common.ServiceDetails at collection time; this is the read-back
	// step that yields a *ComputeDetails / *DatabaseDetails /
	// *CacheDetails / *SavingsPlanDetails (etc.) keyed on rec.Service
	// so the cloud service client's findOfferingID type-assertion
	// succeeds with the full per-rec details (Platform, Tenancy,
	// Scope, AZConfig, HourlyCommitment, …), not just an Engine string.
	//
	// Legacy rows persisted before #453 carry an empty rec.Details —
	// DecodeServiceDetailsFor returns a zero-valued typed pointer for
	// those, and the cloud client's buildOfferingFilters substitutes
	// defaults (Platform=Linux/UNIX, Tenancy=default, etc.). Engine is
	// preserved on the record column too, so we use it as a last-resort
	// fallback for DB/Cache services to avoid silently mis-purchasing a
	// legacy non-default-engine rec as the default engine.
	details, detailsErr := common.DecodeServiceDetailsFor(rec.Service, rec.Details)
	if detailsErr != nil {
		logging.Errorf("purchase[%s]: decode service details failed for %s rec %s: %v",
			opts.ExecutionID, rec.Service, rec.ResourceType, detailsErr)
		return common.PurchaseResult{}, fmt.Errorf("decode service details for %s rec %s: %w", rec.Service, rec.ResourceType, detailsErr)
	}
	if details != nil {
		applyEngineFallback(details, rec.Engine)
		recommendation.Details = details
	}
	// One log line capturing the details shape that's about to drive
	// findOfferingID. detailsAbsent=true is the legacy-row fallback
	// (issue #453) and a strong indicator that filters will fall back
	// to defaults — surface it so a "no offerings found" failure is
	// linkable to a stale rec rather than a transient AWS issue.
	logging.Infof("purchase[%s]: details ready for %s/%s (detailsAbsent=%v engine=%q idempotencyToken=%q)",
		opts.ExecutionID, rec.Provider, rec.Service, len(rec.Details) == 0, rec.Engine, opts.IdempotencyToken)

	// Execute the purchase. This is the call that hits the cloud SDK
	// (and ultimately the AWS / Azure / GCP API). Time it explicitly so
	// CloudWatch shows whether a failure came from the SDK call itself
	// (which the AWS SDK retries up to 3× × 30s by default) versus
	// something earlier in the flow — issue #667.
	tCall := time.Now()
	result, err := serviceClient.PurchaseCommitment(recCtx, recommendation, opts)
	elapsed := time.Since(tCall)
	if err != nil {
		logRecCtxErr(opts.ExecutionID, recTuple, elapsed, recCtx.Err())
		logging.Errorf("purchase[%s]: %s/%s/%s/%s PurchaseCommitment failed after %s: %v",
			opts.ExecutionID, rec.Provider, rec.Service, rec.Region, rec.ResourceType, elapsed, err)
		return result, fmt.Errorf("purchase failed: %w", err)
	}

	if !result.Success {
		if result.Error != nil {
			logging.Errorf("purchase[%s]: %s/%s/%s/%s PurchaseCommitment returned Success=false after %s: %v",
				opts.ExecutionID, rec.Provider, rec.Service, rec.Region, rec.ResourceType, elapsed, result.Error)
			return result, result.Error
		}
		logging.Errorf("purchase[%s]: %s/%s/%s/%s PurchaseCommitment returned Success=false after %s (no error detail)",
			opts.ExecutionID, rec.Provider, rec.Service, rec.Region, rec.ResourceType, elapsed)
		return result, fmt.Errorf("purchase was not successful")
	}

	logging.Infof("purchase[%s]: %s/%s/%s/%s PurchaseCommitment succeeded in %s (commitmentID=%s, cost=%.2f)",
		opts.ExecutionID, rec.Provider, rec.Service, rec.Region, rec.ResourceType, elapsed, result.CommitmentID, result.Cost)
	return result, nil
}

// mapServiceType maps a service string to common.ServiceType. Both the
// canonical hyphenated slugs (compute, relational-db, cache, search,
// data-warehouse) and the legacy AWS-only slugs (ec2, rds, elasticache,
// opensearch, redshift, memorydb) are recognized; everything else passes
// through verbatim. Savings Plans slugs are normalised by mapSavingsPlansSlug.
func (m *Manager) mapServiceType(service string) common.ServiceType {
	if svc, ok := mapSavingsPlansSlug(service); ok {
		return svc
	}
	if svc, ok := mapServiceSlug(service); ok {
		return svc
	}
	return common.ServiceType(service)
}

// mapServiceSlug returns the common.ServiceType for a canonical or legacy
// service slug.
//
// Canonical slugs (compute, relational-db, cache, search, data-warehouse)
// map to the canonical ServiceType constants. Legacy AWS-only slugs (ec2,
// rds, elasticache, opensearch, redshift, memorydb) map to the AWS-legacy
// constants for backward compat with rec rows persisted before the
// canonical normalisation.
//
// The split matters because Azure and GCP providers' GetServiceClient
// switches accept canonical-only; passing the AWS-legacy constants for a
// canonical rec falls through to default and returns "unsupported
// service: <legacy>". AWS provider accepts both legacy and canonical
// forms (see providers/aws/provider.go), so the legacy slugs below are
// still safe for AWS rec rows. Bug:
// https://github.com/LeanerCloud/CUDly/issues/626.
//
// Pulled out of mapServiceType to keep that function under the gocyclo
// budget (same pattern as mapSavingsPlansSlug).
func mapServiceSlug(service string) (common.ServiceType, bool) {
	slugs := map[string]common.ServiceType{
		// Canonical slugs.
		"compute":        common.ServiceCompute,
		"relational-db":  common.ServiceRelationalDB,
		"cache":          common.ServiceCache,
		"search":         common.ServiceSearch,
		"data-warehouse": common.ServiceDataWarehouse,
		// Legacy AWS-only slugs.
		"ec2":         common.ServiceEC2,
		"rds":         common.ServiceRDS,
		"elasticache": common.ServiceElastiCache,
		"opensearch":  common.ServiceOpenSearch,
		"redshift":    common.ServiceRedshift,
		"memorydb":    common.ServiceMemoryDB,
	}
	svc, ok := slugs[service]
	return svc, ok
}

// mapSavingsPlansSlug normalises both the canonical hyphenated SP slugs and
// the dash-free spellings the frontend has historically sent into the
// matching common.ServiceType. The map covers the legacy umbrella plus the
// four per-plan-type slugs in both spellings — pulled out of mapServiceType
// to keep that switch under the gocyclo budget.
//
// TODO(#85): once purchase_executions JSONB rows persisted before the
// "savingsplans" rename (~6-month retention window) have aged out, the
// "savings-plans"-spelled aliases below can be removed and only the
// dash-free spellings ("savingsplans", "savingsplans-compute", etc.)
// need be matched here. The umbrella rename happened in PR #94; the
// per-plan-type slugs were always dash-form on the wire so their
// "savingsplans-*" aliases are forward-compat for any future
// frontend-canonical normalisation.
func mapSavingsPlansSlug(service string) (common.ServiceType, bool) {
	slugs := map[string]common.ServiceType{
		"savings-plans":             common.ServiceSavingsPlans,
		"savingsplans":              common.ServiceSavingsPlans,
		"savings-plans-compute":     common.ServiceSavingsPlansCompute,
		"savingsplans-compute":      common.ServiceSavingsPlansCompute,
		"savings-plans-ec2instance": common.ServiceSavingsPlansEC2Instance,
		"savingsplans-ec2instance":  common.ServiceSavingsPlansEC2Instance,
		"savings-plans-sagemaker":   common.ServiceSavingsPlansSageMaker,
		"savingsplans-sagemaker":    common.ServiceSavingsPlansSageMaker,
		"savings-plans-database":    common.ServiceSavingsPlansDatabase,
		"savingsplans-database":     common.ServiceSavingsPlansDatabase,
	}
	svc, ok := slugs[service]
	return svc, ok
}

// updatePlanProgress advances the ramp schedule after a purchase.
// Direct-execute purchases (Opportunities flow) have no plan to advance --
// PlanID is empty and the Postgres UUID column would reject the query
// with SQLSTATE 22P02, so short-circuit cleanly before hitting the store.
// The actual increment is delegated to IncrementPlanCurrentStep which holds
// a SELECT FOR UPDATE lock for the duration, preventing the lost-update race
// when two Lambda invocations overlap on the same plan (issue #1071).
func (m *Manager) updatePlanProgress(ctx context.Context, planID string) error {
	if planID == "" {
		return nil
	}
	return m.config.IncrementPlanCurrentStep(ctx, planID)
}

// getAWSAccountID retrieves the current AWS account ID using STS.
// It returns ("", error) on all failure paths so the caller decides how to
// handle the absent ID (log a sentinel, skip tagging, etc.) rather than
// silently recording "unknown" in the purchase history row (05-L2).
func (m *Manager) getAWSAccountID(ctx context.Context) (string, error) {
	if m.stsClient == nil {
		return "", fmt.Errorf("STS client not configured")
	}

	result, err := m.stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("STS GetCallerIdentity: %w", err)
	}

	if result.Account == nil {
		return "", fmt.Errorf("STS returned nil Account")
	}
	return *result.Account, nil
}

// applyEngineFallback backfills the Engine field on DB / Cache Details
// from the persisted RecommendationRecord.Engine column when the decoded
// Details left Engine empty. Legacy rows (persisted before #453) carry
// an empty Details payload but a populated Engine column — without this
// backfill, a Postgres RDS rec would silently mis-purchase as whatever
// engine buildOfferingFilters defaults to. For non-DB/Cache Details
// types the call is a no-op. Pointer-typed Details get mutated in
// place; callers pass the pointer that's about to be assigned into
// recommendation.Details so the caller's variable sees the update.
func applyEngineFallback(details common.ServiceDetails, engine string) {
	if engine == "" {
		return
	}
	switch d := details.(type) {
	case *common.DatabaseDetails:
		if d.Engine == "" {
			d.Engine = engine
		}
	case *common.CacheDetails:
		if d.Engine == "" {
			d.Engine = engine
		}
	}
}
