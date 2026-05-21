// Package purchase handles the purchase workflow including approvals and execution.
package purchase

import (
	"context"
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
	if execErr != nil {
		exec.Status = "failed"
		exec.Error = execErr.Error()
	} else {
		completedAt := time.Now()
		exec.Status = "completed"
		exec.CompletedAt = &completedAt
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
			if execErr == nil {
				execErr = fmt.Errorf("audit loss: %w", err)
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

// RecoverStrandedApprovals finds executions stuck in the "approved" status past
// staleApprovedThreshold and drives them into a terminal "failed" state so an
// approved row can never sit permanently stranded with no owner (issue #632).
//
// It deliberately does NOT re-run the purchase: there is no idempotency token on
// commitment creation (EC2 PurchaseReservedInstancesOffering sets no ClientToken;
// CreateSavingsPlan has none), so an automatic re-drive of a row that was
// interrupted *after* AWS created the commitment but *before* the row persisted
// would double-purchase. Failing the row makes it visible in the History view
// (which surfaces failed rows) and Retry-able by an operator who has confirmed
// the AWS-side state, instead of requiring a manual DB edit.
//
// The transition is atomic: TransitionExecutionStatus only flips rows still in
// "approved", so if the original run finally completes between the stale SELECT
// and this UPDATE, the transition is a no-op and the genuine "completed" status
// is preserved.
func (m *Manager) RecoverStrandedApprovals(ctx context.Context) (int, error) {
	stranded, err := m.config.GetStaleApprovedExecutions(ctx, staleApprovedThreshold)
	if err != nil {
		return 0, fmt.Errorf("failed to list stranded approved executions: %w", err)
	}

	recovered := 0
	for i := range stranded {
		exec := &stranded[i]
		logging.Errorf("Recovering stranded approved execution %s (approved but never finalized; failing it for visibility)", exec.ExecutionID)

		updated, txErr := m.config.TransitionExecutionStatus(ctx, exec.ExecutionID, []string{"approved"}, "failed")
		if txErr != nil {
			// Distinguish benign races (row already left the "approved"
			// state — concurrent sweep handled it, or the original run
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
				continue
			}
			return recovered, fmt.Errorf("failed to transition stranded execution %s to failed: %w", exec.ExecutionID, txErr)
		}

		updated.Error = "execution was approved but its purchase run was interrupted before completing and never finalized; failed by the recovery sweep so it is not silently stuck (issue #632). Verify on the cloud provider that no commitment was created, then Retry."
		if saveErr := m.config.SavePurchaseExecution(ctx, updated); saveErr != nil {
			// The atomic flip to "failed" already landed via TransitionExecutionStatus;
			// only the explanatory error string failed to persist. Log loudly but
			// still count the recovery — the row is no longer stranded in "approved".
			logging.Errorf("AUDIT GAP: failed to stamp recovery error on %s: %v", exec.ExecutionID, saveErr)
		}
		recovered++
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
