// Package purchase handles the purchase workflow including approvals and execution.
package purchase

import (
	"context"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// STSClient interface for AWS STS operations
type STSClient interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// ManagerConfig holds configuration for the purchase manager
type ManagerConfig struct {
	ConfigStore               config.StoreInterface
	EmailSender               email.SenderInterface
	STSClient                 STSClient
	AssumeRoleSTS             credentials.STSClient // used for cross-account role assumption
	CredentialStore           credentials.CredentialStore
	ProviderFactory           provider.FactoryInterface
	NotificationDaysBefore    int
	DefaultTerm               int
	DefaultPaymentOption      string
	DefaultCoverage           float64
	DefaultRampSchedule       string
	AzureCredentialsSecretARN string
	GCPCredentialsSecretARN   string
	DashboardURL              string
}

// Manager handles purchase workflow
type Manager struct {
	config          config.StoreInterface
	email           email.SenderInterface
	stsClient       STSClient
	assumeRoleSTS   credentials.STSClient
	credStore       credentials.CredentialStore
	providerFactory provider.FactoryInterface
	notifyDays      int
	defaults        PurchaseDefaults
	dashboardURL    string
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
	Processed int      `json:"processed"`
	Executed  int      `json:"executed"`
	Failed    int      `json:"failed"`
	Errors    []string `json:"errors,omitempty"`
}

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
		credStore:       cfg.CredentialStore,
		providerFactory: factory,
		notifyDays:      cfg.NotificationDaysBefore,
		defaults: PurchaseDefaults{
			Term:         cfg.DefaultTerm,
			Payment:      cfg.DefaultPaymentOption,
			Coverage:     cfg.DefaultCoverage,
			RampSchedule: cfg.DefaultRampSchedule,
		},
		dashboardURL: cfg.DashboardURL,
	}
}

// ProcessScheduledPurchases checks for and executes scheduled purchases
func (m *Manager) ProcessScheduledPurchases(ctx context.Context) (*ProcessResult, error) {
	logging.Info("Processing scheduled purchases...")

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

		// Execute the purchase
		if err := m.executePurchase(ctx, &exec); err != nil {
			logging.Errorf("Failed to execute purchase %s: %v", exec.ExecutionID, err)
			exec.Status = "failed"
			exec.Error = err.Error()
			failed++
			errors = append(errors, fmt.Sprintf("%s: %v", exec.ExecutionID, err))
		} else {
			exec.Status = "completed"
			completedAt := time.Now()
			exec.CompletedAt = &completedAt
			executed++
		}

		// Update execution record
		if err := m.config.SavePurchaseExecution(ctx, &exec); err != nil {
			logging.Errorf("Failed to save execution status: %v", err)
		}

		// Update plan's ramp schedule if applicable
		if err := m.updatePlanProgress(ctx, exec.PlanID); err != nil {
			logging.Errorf("Failed to update plan progress: %v", err)
		}
	}

	return &ProcessResult{
		Processed: processed,
		Executed:  executed,
		Failed:    failed,
		Errors:    errors,
	}, nil
}
