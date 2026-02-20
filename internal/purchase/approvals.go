package purchase

import (
	"context"
	"fmt"

	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// ApproveExecution approves a pending execution
func (m *Manager) ApproveExecution(ctx context.Context, executionID, token string) error {
	logging.Infof("Approving execution: %s", executionID)

	// Get the execution
	execution, err := m.config.GetExecutionByID(ctx, executionID)
	if err != nil {
		return fmt.Errorf("failed to get execution: %w", err)
	}
	if execution == nil {
		return fmt.Errorf("execution not found: %s", executionID)
	}

	// Validate token
	if execution.ApprovalToken != token {
		return fmt.Errorf("invalid approval token")
	}

	// Check status
	if execution.Status != "pending" && execution.Status != "notified" {
		return fmt.Errorf("execution cannot be approved, current status: %s", execution.Status)
	}

	// Update status
	execution.Status = "approved"
	if err := m.config.SavePurchaseExecution(ctx, execution); err != nil {
		return fmt.Errorf("failed to save execution: %w", err)
	}

	logging.Infof("Execution %s approved", executionID)
	return nil
}

// CancelExecution cancels a pending execution
func (m *Manager) CancelExecution(ctx context.Context, executionID, token string) error {
	logging.Infof("Cancelling execution: %s", executionID)

	// Get the execution
	execution, err := m.config.GetExecutionByID(ctx, executionID)
	if err != nil {
		return fmt.Errorf("failed to get execution: %w", err)
	}
	if execution == nil {
		return fmt.Errorf("execution not found: %s", executionID)
	}

	// Validate token
	if execution.ApprovalToken != token {
		return fmt.Errorf("invalid approval token")
	}

	// Check status
	if execution.Status == "completed" || execution.Status == "cancelled" {
		return fmt.Errorf("execution cannot be cancelled, current status: %s", execution.Status)
	}

	// Update status
	execution.Status = "cancelled"
	if err := m.config.SavePurchaseExecution(ctx, execution); err != nil {
		return fmt.Errorf("failed to save execution: %w", err)
	}

	logging.Infof("Execution %s cancelled", executionID)
	return nil
}
