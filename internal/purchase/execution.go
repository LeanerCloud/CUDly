package purchase

import (
	"context"
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
// executePurchase runs the purchase for a single execution. Returns wasMultiAccount=true when
// fan-out was used (per-account records are already saved; caller should skip root record save).
func (m *Manager) executePurchase(ctx context.Context, exec *config.PurchaseExecution) (wasMultiAccount bool, err error) {
	logging.Infof("Executing purchase for plan %q, step %d", exec.PlanID, exec.StepNumber)

	// Direct-execute purchases (Opportunities "Purchase" button) arrive
	// with no associated plan. PlanID is empty and the Postgres UUID
	// column rejects "" with SQLSTATE 22P02, so skip the plan/accounts
	// fetch entirely and synthesise a placeholder plan whose Name is the
	// only field downstream history/notification code reads. By
	// definition direct-execute purchases target a single account, so
	// fall straight through to the legacy single-account path.
	var plan *config.PurchasePlan
	if exec.PlanID == "" {
		plan = &config.PurchasePlan{Name: "Direct purchase"}
	} else {
		plan, err = m.config.GetPurchasePlan(ctx, exec.PlanID)
		if err != nil {
			return false, fmt.Errorf("failed to get plan: %w", err)
		}
		if plan == nil {
			return false, fmt.Errorf("plan not found: %s", exec.PlanID)
		}

		// Fan out across plan accounts when accounts are configured.
		if exec.CloudAccountID == nil {
			accounts, err := m.config.GetPlanAccounts(ctx, exec.PlanID)
			if err != nil {
				return false, fmt.Errorf("failed to load plan accounts for plan %s: %w", exec.PlanID, err)
			}
			if len(accounts) > 0 {
				return true, m.executeMultiAccount(ctx, exec, plan, accounts)
			}
		}
	}

	// Single-account (legacy) path.
	accountID := m.getAWSAccountID(ctx)
	provCfg, err := m.resolveSingleAccountProvider(ctx, exec)
	if err != nil {
		return false, err
	}

	totalSavings, totalUpfront, purchaseErrors := m.processPurchaseRecommendations(ctx, exec, plan, accountID, provCfg)

	if len(purchaseErrors) > 0 {
		return false, fmt.Errorf("some purchases failed: %v", purchaseErrors)
	}

	if err := m.sendPurchaseNotification(ctx, exec, plan, totalSavings, totalUpfront); err != nil {
		logging.Errorf("Failed to send confirmation: %v", err)
	}

	return false, nil
}

// executeMultiAccount fans out executePurchase across all plan accounts in parallel.
// Each account gets its own PurchaseExecution record tagged with cloud_account_id.
func (m *Manager) executeMultiAccount(ctx context.Context, baseExec *config.PurchaseExecution, plan *config.PurchasePlan, accounts []config.CloudAccount) error {
	results := execution.RunForAccountsWithConcurrency(ctx, accounts, func(ctx context.Context, account config.CloudAccount) (struct{}, error) {
		return struct{}{}, m.executeForAccount(ctx, baseExec, plan, account)
	}, getMaxAccountParallelism())

	var errs []string
	for _, r := range results {
		if r.Err != nil {
			errs = append(errs, fmt.Sprintf("account %s: %v", r.AccountID, r.Err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("multi-account execution: %s", strings.Join(errs, "; "))
	}
	return nil
}

// executeForAccount runs a single plan execution for one cloud account.
// It creates a new PurchaseExecution record tagged with cloud_account_id, resolves
// per-account credentials, executes purchases, and saves the result.
func (m *Manager) executeForAccount(ctx context.Context, baseExec *config.PurchaseExecution, plan *config.PurchasePlan, account config.CloudAccount) error {
	// Create a per-account copy of the execution record with an independent
	// Recommendations slice so concurrent goroutines don't race on writes.
	acctID := account.ID
	acctExec := *baseExec
	acctExec.ExecutionID = uuid.New().String()
	acctExec.CloudAccountID = &acctID
	recs := make([]config.RecommendationRecord, len(baseExec.Recommendations))
	copy(recs, baseExec.Recommendations)
	acctExec.Recommendations = recs

	provCfg, err := m.resolveAccountProvider(ctx, account)
	if err != nil {
		acctExec.Status = "failed"
		acctExec.Error = err.Error()
		_ = m.config.SavePurchaseExecution(ctx, &acctExec)
		return fmt.Errorf("credential resolution failed for account %s: %w", account.ID, err)
	}

	accountID := account.ExternalID
	totalSavings, totalUpfront, purchaseErrors := m.processPurchaseRecommendations(ctx, &acctExec, plan, accountID, provCfg)

	if len(purchaseErrors) > 0 {
		acctExec.Status = "failed"
		acctExec.Error = strings.Join(purchaseErrors, "; ")
	} else {
		now := time.Now()
		acctExec.Status = "completed"
		acctExec.CompletedAt = &now
	}

	if err := m.config.SavePurchaseExecution(ctx, &acctExec); err != nil {
		return fmt.Errorf("AUDIT LOSS: failed to save execution record for account %s: %w", account.ID, err)
	}

	if len(purchaseErrors) > 0 {
		return fmt.Errorf("some purchases failed: %v", purchaseErrors)
	}

	if err := m.sendPurchaseNotification(ctx, &acctExec, plan, totalSavings, totalUpfront); err != nil {
		logging.Errorf("Failed to send confirmation for account %s: %v", account.ID, err)
	}
	return nil
}

// resolveSingleAccountProvider derives per-account credentials for the
// single-account execution path. Returns (nil, nil) when no account can be
// identified (ambient credentials are used in that case), or when no recs are
// selected (approval-only flows where credentials are not needed).
//
// The account is taken from exec.CloudAccountID when set (plan-with-single-
// account executions), or derived from the shared cloud_account_id on the
// recommendations when all selected recs agree on exactly one account (direct-
// execute purchases where PlanID is empty and exec.CloudAccountID is nil).
//
// A non-nil error means credentials were found but could not be resolved; the
// caller must NOT fall back to ambient credentials on error.
func (m *Manager) resolveSingleAccountProvider(ctx context.Context, exec *config.PurchaseExecution) (*provider.ProviderConfig, error) {
	// Skip credential resolution when nothing is selected. Approval-only
	// flows may carry an account ID on the recs solely for the contact-email
	// gate; attempting resolution there would fail on accounts with no
	// provider field set and add a pointless DB round-trip.
	if len(selectedIndices(exec.Recommendations)) == 0 {
		return nil, nil
	}

	cloudAccountID := exec.CloudAccountID
	if cloudAccountID == nil {
		cloudAccountID = singleCloudAccountIDFromRecs(exec.Recommendations)
	}
	if cloudAccountID == nil {
		return nil, nil
	}

	account, err := m.config.GetCloudAccount(ctx, *cloudAccountID)
	if err != nil {
		return nil, fmt.Errorf("credential resolution failed for account %s: %w", *cloudAccountID, err)
	}
	if account == nil {
		return nil, nil
	}
	provCfg, err := m.resolveAccountProvider(ctx, *account)
	if err != nil {
		return nil, fmt.Errorf("credential resolution failed for account %s: %w", *cloudAccountID, err)
	}
	return provCfg, nil
}

// resolveAccountProvider returns a *ProviderConfig with a pre-authenticated provider
// for the given account. Returns an error if credential resolution fails — callers
// must NOT fall back to ambient credentials on error.
func (m *Manager) resolveAccountProvider(ctx context.Context, account config.CloudAccount) (*provider.ProviderConfig, error) {
	switch account.Provider {
	case "aws":
		return m.resolveAWSProvider(ctx, account)
	case "azure":
		return m.resolveAzureProvider(ctx, account)
	case "gcp":
		return m.resolveGCPProvider(ctx, account)
	default:
		return nil, fmt.Errorf("credentials: unknown cloud provider %q for account %s", account.Provider, account.ID)
	}
}

func (m *Manager) resolveAWSProvider(ctx context.Context, account config.CloudAccount) (*provider.ProviderConfig, error) {
	if account.AWSAuthMode != "access_keys" && m.assumeRoleSTS == nil {
		return nil, fmt.Errorf("credentials: STS client not configured for non-access_keys mode (account %s)", account.ID)
	}
	awsCreds, err := credentials.ResolveAWSCredentialProviderWithOpts(ctx, &account, m.credStore, m.assumeRoleSTS,
		credentials.AWSResolveOptions{AmbientProvider: m.ambientAWSCreds})
	if err != nil {
		return nil, fmt.Errorf("credentials: resolve AWS for account %s (%s): %w", account.ID, account.Name, err)
	}
	return &provider.ProviderConfig{Name: "aws", AWSCredentialsProvider: awsCreds}, nil
}

func (m *Manager) resolveAzureProvider(ctx context.Context, account config.CloudAccount) (*provider.ProviderConfig, error) {
	if account.AzureAuthMode != "managed_identity" && m.credStore == nil {
		return nil, fmt.Errorf("credentials: credential store required for non-managed_identity Azure account %s", account.ID)
	}
	azCred, err := credentials.ResolveAzureTokenCredentialWithOpts(ctx, &account, m.credStore, credentials.AzureResolveOptions{
		Signer:    m.oidcSigner,
		IssuerURL: m.oidcIssuerURL,
	})
	if err != nil {
		return nil, fmt.Errorf("credentials: resolve Azure for account %s (%s): %w", account.ID, account.Name, err)
	}
	azProv, err := azureprovider.NewAzureProvider(&provider.ProviderConfig{Profile: account.AzureSubscriptionID})
	if err != nil {
		return nil, fmt.Errorf("credentials: create Azure provider for account %s (%s): %w", account.ID, account.Name, err)
	}
	azProv.SetCredential(azCred)
	return &provider.ProviderConfig{ProviderOverride: azProv}, nil
}

func (m *Manager) resolveGCPProvider(ctx context.Context, account config.CloudAccount) (*provider.ProviderConfig, error) {
	if account.GCPAuthMode != "application_default" && m.credStore == nil {
		return nil, fmt.Errorf("credentials: credential store required for non-ADC GCP account %s", account.ID)
	}
	gcpTS, err := credentials.ResolveGCPTokenSourceWithOpts(ctx, &account, m.credStore, credentials.GCPResolveOptions{
		Signer:    m.oidcSigner,
		IssuerURL: m.oidcIssuerURL,
	})
	if err != nil {
		return nil, fmt.Errorf("credentials: resolve GCP for account %s (%s): %w", account.ID, account.Name, err)
	}
	if gcpTS == nil {
		// ADC mode: no explicit token source — use ambient credentials
		return nil, nil
	}
	gcpProv := gcpprovider.NewProviderWithCredentials(ctx, account.GCPProjectID, gcpTS)
	return &provider.ProviderConfig{ProviderOverride: gcpProv}, nil
}

// getMaxAccountParallelism is a thin alias over the shared
// execution.ConcurrencyFromEnv so the purchase manager and the scheduler
// both honour the same CUDLY_MAX_ACCOUNT_PARALLELISM override.
func getMaxAccountParallelism() int {
	return execution.ConcurrencyFromEnv()
}

// recPurchaseOutcome carries the result of a single fan-out unit so the
// aggregator can write back into exec.Recommendations and call
// savePurchaseHistory from a single goroutine (no concurrent map / slice
// mutation). The index field is the position in exec.Recommendations.
type recPurchaseOutcome struct {
	index    int
	purchase common.PurchaseResult
	err      error
}

func (m *Manager) processPurchaseRecommendations(ctx context.Context, exec *config.PurchaseExecution, plan *config.PurchasePlan, accountID string, provCfg *provider.ProviderConfig) (float64, float64, []string) {
	opts := common.PurchaseOptions{Source: m.normalizePurchaseSource(exec)}

	// Build the list of selected indices once so the fan-out closure only
	// has to look up rec[i] (no second pass over the full slice).
	selected := selectedIndices(exec.Recommendations)
	if len(selected) == 0 {
		return 0, 0, nil
	}

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
			purchaseResult, err := m.executeSinglePurchase(ctx, rec, provCfg, opts)
			return recPurchaseOutcome{index: i, purchase: purchaseResult, err: err}, nil
		}, getMaxAccountParallelism())

	return m.aggregatePurchaseOutcomes(ctx, exec, plan, accountID, results)
}

// aggregatePurchaseOutcomes walks the fan-out results serially and writes
// each rec's outcome back to exec.Recommendations + savePurchaseHistory.
// Extracted so processPurchaseRecommendations stays under gocyclo:10 and
// so the aggregation logic is single-threaded — no concurrent writes to
// totals, purchaseErrors, or exec.Recommendations[i] regardless of how
// many recs ran in parallel.
func (m *Manager) aggregatePurchaseOutcomes(ctx context.Context, exec *config.PurchaseExecution, plan *config.PurchasePlan, accountID string, results []execution.Result[recPurchaseOutcome]) (float64, float64, []string) {
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
		// Defence-in-depth: even with the closure's bounds check, never
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
// History view (which synthesises completed executions that carry an Error).
// Appends rather than overwrites so multiple failed history writes within one
// execution are all recorded.
func recordHistoryAuditGap(exec *config.PurchaseExecution, commitmentID string, histErr error) {
	note := fmt.Sprintf("commitment %s purchased but its history record failed to save: %v", commitmentID, histErr)
	if exec.Error == "" {
		exec.Error = note
	} else {
		exec.Error += "; " + note
	}
}

// normalizePurchaseSource canonicalizes exec.Source for downstream tag
// stamping. Defence-in-depth: NormalizeSource rejects anything outside
// the allowed whitelist; an unexpected value (DB tampering, future
// code path) is dropped to "" rather than fed onto a cloud commitment
// where it would be expensive to retract.
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
	for i, rec := range recs {
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
func (m *Manager) savePurchaseHistory(ctx context.Context, exec *config.PurchaseExecution, plan *config.PurchasePlan, rec config.RecommendationRecord, result common.PurchaseResult, accountID string) error {
	historyRecord := &config.PurchaseHistoryRecord{
		AccountID:        accountID,
		PurchaseID:       result.CommitmentID,
		Timestamp:        time.Now(),
		Provider:         rec.Provider,
		Service:          rec.Service,
		Region:           rec.Region,
		ResourceType:     rec.ResourceType,
		Count:            rec.Count,
		Term:             rec.Term,
		Payment:          rec.Payment,
		UpfrontCost:      result.Cost,
		MonthlyCost:      derefFloat64(rec.MonthlyCost),
		EstimatedSavings: rec.Savings,
		PlanID:           exec.PlanID,
		PlanName:         plan.Name,
		RampStep:         exec.StepNumber,
		CloudAccountID:   exec.CloudAccountID,
		Source:           exec.Source,
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

	for _, rec := range exec.Recommendations {
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

// executeSinglePurchase executes a single purchase using the appropriate provider.
// provCfg carries optional per-account credentials; pass nil to use ambient credentials.
// opts carries execution-level metadata (the source surface) that providers stamp
// onto the commitment they create.
func (m *Manager) executeSinglePurchase(ctx context.Context, rec config.RecommendationRecord, provCfg *provider.ProviderConfig, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	// Create the provider
	cloudProvider, err := m.providerFactory.CreateAndValidateProvider(ctx, rec.Provider, provCfg)
	if err != nil {
		return common.PurchaseResult{}, fmt.Errorf("failed to create %s provider: %w", rec.Provider, err)
	}

	// Map service string to ServiceType
	serviceType := m.mapServiceType(rec.Service)

	// Get the service client for this region
	serviceClient, err := cloudProvider.GetServiceClient(ctx, serviceType, rec.Region)
	if err != nil {
		return common.PurchaseResult{}, fmt.Errorf("failed to get service client: %w", err)
	}

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
		return common.PurchaseResult{}, fmt.Errorf("decode service details for %s rec %s: %w", rec.Service, rec.ResourceType, detailsErr)
	}
	if details != nil {
		applyEngineFallback(details, rec.Engine)
		recommendation.Details = details
	}

	// Execute the purchase
	result, err := serviceClient.PurchaseCommitment(ctx, recommendation, opts)
	if err != nil {
		return result, fmt.Errorf("purchase failed: %w", err)
	}

	if !result.Success {
		if result.Error != nil {
			return result, result.Error
		}
		return result, fmt.Errorf("purchase was not successful")
	}

	logging.Infof("Successfully purchased %s: %s", rec.ResourceType, result.CommitmentID)
	return result, nil
}

// mapServiceType maps a service string to common.ServiceType.
//
// Returns the provider-neutral canonical types (ServiceCompute,
// ServiceRelationalDB, ServiceCache, ServiceSearch, ServiceDataWarehouse)
// rather than the legacy AWS-specific aliases (ServiceEC2, ServiceRDS,
// ServiceElastiCache, ServiceOpenSearch, ServiceRedshift). Both the AWS
// and Azure provider switches accept the canonical types; only the AWS
// switch accepts the legacy aliases (see providers/aws/provider.go +
// providers/azure/provider.go). Returning the AWS alias here caused
// every Azure VM purchase to fail at provider.GetServiceClient with
// "unsupported service: ec2" because rec.Service="compute" on an Azure
// rec was mapped to ServiceEC2 and the Azure provider's switch only
// accepts ServiceCompute.
//
// ServiceMemoryDB stays as-is because Azure has no MemoryDB equivalent.
func (m *Manager) mapServiceType(service string) common.ServiceType {
	if svc, ok := mapSavingsPlansSlug(service); ok {
		return svc
	}
	switch service {
	case "ec2", "compute":
		return common.ServiceCompute
	case "rds", "relational-db":
		return common.ServiceRelationalDB
	case "elasticache", "cache":
		return common.ServiceCache
	case "opensearch", "search":
		return common.ServiceSearch
	case "redshift", "data-warehouse":
		return common.ServiceDataWarehouse
	case "memorydb":
		return common.ServiceMemoryDB
	default:
		return common.ServiceType(service)
	}
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
// Direct-execute purchases (Opportunities flow) have no plan to advance —
// PlanID is empty and the Postgres UUID column would reject the query
// with SQLSTATE 22P02, so short-circuit cleanly before hitting the store.
func (m *Manager) updatePlanProgress(ctx context.Context, planID string) error {
	if planID == "" {
		return nil
	}
	plan, err := m.config.GetPurchasePlan(ctx, planID)
	if err != nil {
		return err
	}
	if plan == nil {
		return nil
	}

	// Advance ramp schedule only if not complete
	if !plan.RampSchedule.IsComplete() {
		plan.RampSchedule.CurrentStep++
	}

	// Calculate next execution date
	if !plan.RampSchedule.IsComplete() {
		nextDate := plan.RampSchedule.GetNextPurchaseDate()
		plan.NextExecutionDate = &nextDate
	} else {
		plan.NextExecutionDate = nil
	}

	now := time.Now()
	plan.LastExecutionDate = &now

	return m.config.UpdatePurchasePlan(ctx, plan)
}

// getAWSAccountID retrieves the current AWS account ID using STS
func (m *Manager) getAWSAccountID(ctx context.Context) string {
	if m.stsClient == nil {
		logging.Debug("STS client not configured, using 'unknown' as account ID")
		return "unknown"
	}

	result, err := m.stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		logging.Warnf("Failed to get AWS account ID: %v", err)
		return "unknown"
	}

	if result.Account != nil {
		return *result.Account
	}

	return "unknown"
}

// derefFloat64 safely dereferences a *float64, returning 0 for nil.
// Used when copying from RecommendationRecord.MonthlyCost (*float64)
// into PurchaseHistoryRecord.MonthlyCost (float64), where nil means
// "not provided" and maps to 0 in the history table.
func derefFloat64(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
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
