// Package purchase handles the purchase workflow including approvals and execution.
package purchase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/oidc"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// STSClient interface for AWS STS operations
type STSClient interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// ManagerConfig holds configuration for the purchase manager
type ManagerConfig struct {
	ConfigStore            config.StoreInterface
	EmailSender            email.SenderInterface
	STSClient              STSClient
	AssumeRoleSTS          credentials.STSClient // used for cross-account role assumption
	CredentialStore        credentials.CredentialStore
	ProviderFactory        provider.FactoryInterface
	NotificationDaysBefore int
	DefaultTerm            int
	DefaultPaymentOption   string
	DefaultCoverage        float64
	DefaultRampSchedule    string
	DashboardURL           string
	// AmbientAWSCreds is the host Lambda / EC2 instance credentials provider,
	// used when resolving a Self account (auth_mode=role_arn with empty role ARN).
	AmbientAWSCreds aws.CredentialsProvider
	// OIDCSigner and OIDCIssuerURL enable the secret-free Azure
	// federated credential path. When both are set, Azure accounts in
	// workload_identity_federation mode with no stored PEM are routed
	// through BuildAzureFederatedCredential. Optional — when unset,
	// the legacy cert-based path is used for backward compatibility.
	OIDCSigner    oidc.Signer
	OIDCIssuerURL string
}

// Manager handles purchase workflow
type Manager struct {
	config          config.StoreInterface
	email           email.SenderInterface
	stsClient       STSClient
	assumeRoleSTS   credentials.STSClient
	ambientAWSCreds aws.CredentialsProvider
	credStore       credentials.CredentialStore
	providerFactory provider.FactoryInterface
	notifyDays      int
	defaults        PurchaseDefaults
	dashboardURL    string
	oidcSigner      oidc.Signer
	oidcIssuerURL   string
}

// PurchaseDefaults holds default purchase settings
type PurchaseDefaults struct {
	Term         int
	Payment      string
	Coverage     float64
	RampSchedule string
}

// ProcessResult holds the result of processing scheduled purchases
type ProcessResult struct {
	Processed int `json:"processed"`
	Executed  int `json:"executed"`
	Failed    int `json:"failed"`
	// Recovered counts executions that were stuck in "approved" and were
	// re-driven into a terminal "failed" state by the recovery sweep
	// (issue #632).
	Recovered int      `json:"recovered,omitempty"`
	Errors    []string `json:"errors,omitempty"`
}

// staleApprovedThreshold is how long an execution may sit in the "approved"
// status before the recovery sweep treats it as stranded (issue #632). It must
// be comfortably larger than the longest possible synchronous purchase run so a
// legitimately in-flight execution is never failed out from under itself. The
// purchase Lambda timeout is 60s; 15min (matching the RI-exchange stale-sweep
// threshold in pkg/exchange) leaves a wide safety margin.
const staleApprovedThreshold = 15 * time.Minute

// NotificationResult holds the result of sending notifications
type NotificationResult struct {
	Notified int `json:"notified"`
}

// NewManager creates a new purchase manager
func NewManager(cfg ManagerConfig) *Manager {
	factory := cfg.ProviderFactory
	if factory == nil {
		factory = &provider.DefaultFactory{}
	}

	return &Manager{
		config:          cfg.ConfigStore,
		email:           cfg.EmailSender,
		stsClient:       cfg.STSClient,
		assumeRoleSTS:   cfg.AssumeRoleSTS,
		ambientAWSCreds: cfg.AmbientAWSCreds,
		credStore:       cfg.CredentialStore,
		providerFactory: factory,
		notifyDays:      cfg.NotificationDaysBefore,
		defaults: PurchaseDefaults{
			Term:         cfg.DefaultTerm,
			Payment:      cfg.DefaultPaymentOption,
			Coverage:     cfg.DefaultCoverage,
			RampSchedule: cfg.DefaultRampSchedule,
		},
		dashboardURL:  cfg.DashboardURL,
		oidcSigner:    cfg.OIDCSigner,
		oidcIssuerURL: cfg.OIDCIssuerURL,
	}
}

// finalizeExecution sets the status and completion time on an execution based on the error.
func (m *Manager) finalizeExecution(exec *config.PurchaseExecution, execErr error) {
	var partial *partialPurchaseError
	switch {
	case execErr == nil:
		completedAt := time.Now()
		exec.Status = "completed"
		exec.CompletedAt = &completedAt
	case errors.As(execErr, &partial):
		// #642: at least one rec committed a real purchase while others
		// failed. Never mark such a row "failed" — the commitments are real
		// and a re-approve would double-buy them. Record the partial outcome
		// and stamp CompletedAt so the row reads as terminal (the successful
		// recs are done), with the per-rec failures preserved in Error.
		// Append rather than overwrite so any audit-gap note already stamped
		// by aggregatePurchaseOutcomes (a successful rec whose history write
		// failed, issue #621) is not lost.
		completedAt := time.Now()
		exec.Status = "partially_completed"
		exec.Error = appendErrNote(exec.Error, execErr.Error())
		exec.CompletedAt = &completedAt
	default:
		exec.Status = "failed"
		exec.Error = execErr.Error()
	}
}

// executeAndFinalize runs a purchase and handles status updates, record saving, and progress.
func (m *Manager) executeAndFinalize(ctx context.Context, exec *config.PurchaseExecution) error {
	wasMultiAccount, execErr := m.executePurchase(ctx, exec)
	m.finalizeExecution(exec, execErr)
	if execErr != nil {
		logging.Errorf("Failed to execute purchase %s: %v", exec.ExecutionID, execErr)
	}
	if !wasMultiAccount {
		if err := m.config.SavePurchaseExecution(ctx, exec); err != nil {
			logging.Errorf("AUDIT LOSS: failed to save execution status: %v", err)
			// Wrap with ErrAuditLoss regardless of whether executePurchase itself
			// failed. When execErr != nil (provider/partial error), finalizeExecution
			// stamped a terminal status on the in-memory exec struct, but if
			// SavePurchaseExecution then failed the DB row is still in "running" --
			// exactly the stranded-row scenario ErrAuditLoss signals. Preserve the
			// original execErr as the innermost %w so errors.As/errors.Is can still
			// reach it from callers (e.g. claimAndRedrive checking ErrAuditLoss).
			if execErr != nil {
				execErr = fmt.Errorf("%w: terminal save failed (%v); original execution error: %w",
					config.ErrAuditLoss, err, execErr)
			} else {
				execErr = fmt.Errorf("%w: %w", config.ErrAuditLoss, err)
			}
		}
	}
	if execErr == nil {
		if err := m.updatePlanProgress(ctx, exec.PlanID); err != nil {
			logging.Errorf("Failed to update plan progress: %v", err)
		}
	}
	return execErr
}

// allRecsSafeToRedrive reports whether every recommendation in the execution
// can be safely re-driven without risking a double-purchase. A re-drive is safe
// when the underlying provider purchase API is idempotent under the
// DeriveIdempotencyToken(exec.ExecutionID, i) scheme used by execution.go.
//
// Safe providers / services (issue #639):
//   - AWS (all services): tag-guard or ClientToken deduplication (#636/#638).
//   - Azure reservations (compute, relational-db, cache, nosql, memorydb,
//     search, data-warehouse): DoIdempotentPurchaseTwoStep performs a
//     tag-based lookup before purchasing (#729 / #721).
//   - GCP compute (CUDs): server-side RequestId + deterministic name from
//     the token (#654).
//
// NOT safe - safe-fail path preserved:
//   - Azure savings-plans: the OrderAlias API uses time.Now().UnixNano() as
//     the alias name; there is no server-side idempotency key and no
//     tag-based lookup implemented yet. Re-driving would create a duplicate
//     savings plan.
//
// Empty provider ("") is treated as AWS (pre-multi-cloud legacy rows).
// An execution with no recommendations returns false so it falls through to the
// safe-fail path (nothing to re-drive anyway).
func allRecsSafeToRedrive(exec *config.PurchaseExecution) bool {
	if len(exec.Recommendations) == 0 {
		return false
	}
	for _, rec := range exec.Recommendations {
		if !recIsSafeToRedrive(rec) {
			return false
		}
	}
	return true
}

// recIsSafeToRedrive reports whether a single recommendation can be safely
// re-driven. Extracted from allRecsSafeToRedrive to keep that function under
// the gocyclo budget and to make per-rec exclusions explicit.
func recIsSafeToRedrive(rec config.RecommendationRecord) bool {
	switch rec.Provider {
	case "", "aws":
		// Empty provider is legacy AWS. All AWS services honour IdempotencyToken.
		return true
	case "azure":
		// Azure savings-plans uses a timestamp-based alias name and has no
		// server-side idempotency key, so a re-drive would create a duplicate.
		// All other Azure services use DoIdempotentPurchaseTwoStep (#729).
		return rec.Service != "savingsplans" && rec.Service != "savings-plans"
	case "gcp":
		// GCP compute CUDs use RequestId + deterministic name from the token (#654).
		return true
	default:
		// Unknown provider: refuse to re-drive rather than risk a double-buy.
		return false
	}
}

// claimAndRedrive atomically claims a stranded execution (by transitioning its
// status from "approved" to "running") and then re-drives it via
// executeAndFinalize. It is extracted from RecoverStrandedApprovals to keep that
// function's cyclomatic complexity within the gocyclo:10 limit.
//
// Returns (true, nil) when the claim was won and the re-drive completed (row is
// now in a terminal state). A re-drive error is not fatal -- executeAndFinalize
// already stamped a terminal status -- so it returns (false, nil) on drive
// failure too (the failed row is still visible in History).
// Returns (false, nil) when the CAS claim is lost to a concurrent sweep or the
// row vanishes mid-flight (both are benign races).
// Returns (false, err) only on a genuine DB error during the claim step.
func (m *Manager) claimAndRedrive(ctx context.Context, exec *config.PurchaseExecution) (bool, error) {
	// Atomically claim ownership before re-driving to prevent two concurrent
	// sweeps (or a late original completion) from both calling executeAndFinalize
	// on the same approved row. The CAS transitions "approved" -> "running";
	// only the winner proceeds.
	claimed, claimErr := m.config.TransitionExecutionStatus(ctx, exec.ExecutionID, []string{"approved"}, "running")
	if claimErr != nil {
		// ErrNotFound: row vanished between SELECT and CAS - benign race.
		// ErrExecutionNotInExpectedStatus: another sweep or the original run
		// already claimed/completed this row - also benign.
		if errors.Is(claimErr, config.ErrNotFound) || errors.Is(claimErr, config.ErrExecutionNotInExpectedStatus) {
			logging.Warnf("Skipping re-drive of %s (CAS claim lost to concurrent worker): %v", exec.ExecutionID, claimErr)
			return false, nil
		}
		return false, fmt.Errorf("failed to claim execution %s for re-drive: %w", exec.ExecutionID, claimErr)
	}
	// Update the local struct to reflect the committed DB state so
	// finalizeExecution starts from "running" rather than "approved".
	exec.Status = claimed.Status
	logging.Infof("Recovering stranded execution %s via idempotent re-drive (issue #639)", exec.ExecutionID)
	if driveErr := m.executeAndFinalize(ctx, exec); driveErr != nil {
		logging.Errorf("Re-drive of stranded execution %s failed: %v", exec.ExecutionID, driveErr)
		// Persistence failures (ErrAuditLoss) are non-benign: the row was CAS-ed
		// to "running" but SavePurchaseExecution failed, so no terminal status was
		// persisted. Propagate so the sweep surfaces the error rather than silently
		// dropping a row that is now stranded in "running".
		if errors.Is(driveErr, config.ErrAuditLoss) {
			return false, fmt.Errorf("persistence failure re-driving execution %s (row stranded in running): %w", exec.ExecutionID, driveErr)
		}
		// Benign provider/rec errors: finalizeExecution already stamped a terminal
		// status (failed/partially_completed) and SavePurchaseExecution succeeded.
		// The row is in a terminal state; log and continue without counting as
		// recovered.
		return false, nil
	}
	return true, nil
}

// safeFail atomically transitions a stranded execution to "failed" and stamps a
// recovery error on it. It is extracted from RecoverStrandedApprovals to keep
// that function's cyclomatic complexity within the gocyclo:10 limit.
//
// Returns (true, nil) when the row was successfully transitioned to "failed".
// Returns (false, nil) when TransitionExecutionStatus fails but the row has
// already left "approved" (benign race - the original run completed late;
// not counted as a recovery since no action was taken here).
// Returns (false, err) when a real store failure occurs.
func (m *Manager) safeFail(ctx context.Context, exec *config.PurchaseExecution) (bool, error) {
	logging.Errorf("Recovering stranded approved execution %s (approved but never finalized; failing it for visibility)", exec.ExecutionID)

	updated, txErr := m.config.TransitionExecutionStatus(ctx, exec.ExecutionID, []string{"approved"}, "failed")
	if txErr != nil {
		// ErrNotFound means the row vanished between the stale SELECT and
		// this CAS attempt (e.g. deleted by an operator or a concurrent
		// sweep already claimed and deleted it). That is a benign race-loss:
		// there is nothing left to fail, and the caller should not be
		// charged with an error.
		if errors.Is(txErr, config.ErrNotFound) {
			logging.Warnf("Skipping recovery of %s (row no longer exists, benign race-loss): %v", exec.ExecutionID, txErr)
			return false, nil
		}
		// ErrExecutionNotInExpectedStatus means the row exists but its
		// status has already moved out of "approved" (a concurrent sweep or
		// the original run won the CAS race). Treat identically to the
		// ErrNotFound case: nothing left to do here, no re-read needed.
		// This mirrors claimAndRedrive and reaper.go, which both treat this
		// sentinel as terminally benign.
		if errors.Is(txErr, config.ErrExecutionNotInExpectedStatus) {
			logging.Warnf("Skipping recovery of %s (row already left approved state, benign CAS race-loss): %v", exec.ExecutionID, txErr)
			return false, nil
		}
		// Distinguish benign races (row already left the "approved"
		// state - concurrent sweep handled it, or the original run
		// finished after the LIST snapshot) from real store
		// failures (DB unreachable, query syntax error). A real
		// store failure must fail the sweep so a transient DB
		// outage does not silently under-recover. We probe the
		// current row state via GetExecutionByID: a clean read
		// with Status != "approved" confirms the race; any other
		// outcome (read error, still-approved row) is a real
		// failure worth propagating.
		current, getErr := m.config.GetExecutionByID(ctx, exec.ExecutionID)
		if getErr == nil && current != nil && current.Status != "approved" {
			logging.Warnf("Skipping recovery of %s (already transitioned out of approved): %v", exec.ExecutionID, txErr)
			return false, nil
		}
		return false, fmt.Errorf("failed to transition stranded execution %s to failed: %w", exec.ExecutionID, txErr)
	}

	updated.Error = "execution was approved but its purchase run was interrupted before completing and never finalized; failed by the recovery sweep so it is not silently stuck (issue #632). Verify on the cloud provider that no commitment was created, then Retry."
	if saveErr := m.config.SavePurchaseExecution(ctx, updated); saveErr != nil {
		// The atomic flip to "failed" already landed via TransitionExecutionStatus;
		// only the explanatory error string failed to persist. Log loudly but
		// still count the recovery - the row is no longer stranded in "approved".
		logging.Errorf("AUDIT GAP: failed to stamp recovery error on %s: %v", exec.ExecutionID, saveErr)
	}
	return true, nil
}

// RecoverStrandedApprovals finds executions stuck in the "approved" status past
// staleApprovedThreshold and either re-drives them idempotently (executions
// where every rec is safe to re-drive and the row has a durable ExecutionID)
// or drives them into a terminal "failed" state (rows with unsafe recs or
// without a stable ExecutionID).
//
// Idempotent re-drive path (issue #639): all AWS, Azure reservations, and GCP
// compute service clients derive or look up a deterministic idempotency key
// from DeriveIdempotencyToken(exec.ExecutionID, i). Re-driving with the same
// ExecutionID produces the same token, so the cloud provider dedupes the second
// call and no double-purchase occurs. The row transitions directly to "completed"
// (or "failed"/"partially_completed" on a genuine error), bypassing the manual
// Retry step required by the old safe-fail path. See allRecsSafeToRedrive for
// which provider/service combinations are eligible.
//
// Safe-fail path: Azure savings-plans recs are excluded because the OrderAlias
// API uses a timestamp-based alias name with no idempotency key. Executions
// without a stable ExecutionID (legacy rows) also fall through because
// DeriveIdempotencyToken("", i) would produce the same token set for every
// such row. These fall through to the original behaviour: the row is atomically
// transitioned to "failed" so it surfaces in History and can be Retry-ed by
// an operator after confirming the cloud-side state.
//
// The transition in the safe-fail path is atomic: TransitionExecutionStatus only
// flips rows still in "approved", so if the original run finally completes between
// the stale SELECT and this UPDATE, the transition is a no-op and the genuine
// "completed" status is preserved.
func (m *Manager) RecoverStrandedApprovals(ctx context.Context) (int, error) {
	stranded, err := m.config.GetStaleApprovedExecutions(ctx, staleApprovedThreshold)
	if err != nil {
		return 0, fmt.Errorf("failed to list stranded approved executions: %w", err)
	}

	recovered := 0
	for i := range stranded {
		exec := &stranded[i]

		// Idempotent re-drive path (issue #639): all recs honour
		// opts.IdempotencyToken via DeriveIdempotencyToken(exec.ExecutionID, i),
		// so a second call with the same ExecutionID is a safe no-op on the
		// provider side. The ExecutionID must be non-empty to derive a unique
		// token; an empty ID would map every legacy row to the same token set.
		if allRecsSafeToRedrive(exec) && exec.ExecutionID != "" {
			counted, driveErr := m.claimAndRedrive(ctx, exec)
			if driveErr != nil {
				return recovered, driveErr
			}
			if counted {
				recovered++
			}
			continue
		}

		// Safe-fail path for mixed/Azure/GCP/legacy executions.
		counted, failErr := m.safeFail(ctx, exec)
		if failErr != nil {
			return recovered, failErr
		}
		if counted {
			recovered++
		}
	}

	return recovered, nil
}

// ProcessScheduledPurchases checks for and executes scheduled purchases
func (m *Manager) ProcessScheduledPurchases(ctx context.Context) (*ProcessResult, error) {
	logging.Info("Processing scheduled purchases...")

	// Recover any executions stranded in "approved" by an interrupted
	// synchronous run before processing fresh pending work (issue #632).
	recovered, err := m.RecoverStrandedApprovals(ctx)
	if err != nil {
		// A recovery failure must not block scheduled purchases — log and continue
		// with the pending-execution pass; the next tick retries the sweep.
		logging.Errorf("Failed to recover stranded approved executions: %v", err)
	}

	// Get all pending executions
	executions, err := m.config.GetPendingExecutions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending executions: %w", err)
	}

	now := time.Now()
	processed := 0
	executed := 0
	failed := 0
	var errors []string

	for _, exec := range executions {
		// Check if it's time to execute
		if exec.ScheduledDate.After(now) {
			logging.Debugf("Execution %s not yet due (scheduled for %s)", exec.ExecutionID, exec.ScheduledDate)
			continue
		}

		// Skip if cancelled or already completed
		if exec.Status == "cancelled" || exec.Status == "completed" {
			continue
		}

		processed++

		logging.Infof("Executing scheduled purchase: %s", exec.ExecutionID)

		// Execute the purchase and handle post-execution bookkeeping.
		if execErr := m.executeAndFinalize(ctx, &exec); execErr != nil {
			failed++
			errors = append(errors, fmt.Sprintf("%s: %v", exec.ExecutionID, execErr))
		} else {
			executed++
		}
	}

	return &ProcessResult{
		Processed: processed,
		Executed:  executed,
		Failed:    failed,
		Recovered: recovered,
		Errors:    errors,
	}, nil
}
