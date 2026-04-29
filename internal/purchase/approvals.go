package purchase

import (
	"context"
	"crypto/subtle"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/jackc/pgx/v5"
)

// ApproveExecution approves a pending execution. actor carries the email
// of the session-authenticated user who clicked approve (from the
// HTTP route gated by authorizeApprovalAction) or the actor_email
// carried by an SQS approve message after verifyAsyncApprovalActor has
// validated it against the per-account contact_email approver list.
// Both call paths now enforce the approver gate before reaching this
// function — actor is empty only on legacy in-process callers (tests,
// older code paths) and is recorded as nil so we don't claim "approved
// by nobody".
func (m *Manager) ApproveExecution(ctx context.Context, executionID, token, actor string) error {
	logging.Infof("Approving execution: %s", executionID)

	// Get the execution
	execution, err := m.config.GetExecutionByID(ctx, executionID)
	if err != nil {
		return fmt.Errorf("failed to get execution: %w", err)
	}
	if execution == nil {
		return fmt.Errorf("execution not found: %s", executionID)
	}

	// Validate token using constant-time comparison to prevent timing attacks
	if execution.ApprovalToken == "" || token == "" {
		return fmt.Errorf("invalid approval token")
	}
	if subtle.ConstantTimeCompare([]byte(execution.ApprovalToken), []byte(token)) != 1 {
		return fmt.Errorf("invalid approval token")
	}

	// Check status
	if execution.Status != "pending" && execution.Status != "notified" {
		return fmt.Errorf("execution cannot be approved, current status: %s", execution.Status)
	}

	// Update status + attribution (nil when actor is empty — the store
	// column is nullable TEXT and we don't want to record an empty string
	// as "approved by nobody").
	execution.Status = "approved"
	if actor != "" {
		a := actor
		execution.ApprovedBy = &a
	}
	if err := m.config.SavePurchaseExecution(ctx, execution); err != nil {
		return fmt.Errorf("failed to save execution: %w", err)
	}

	logging.Infof("Execution %s approved", executionID)
	return nil
}

// CancelExecution cancels a pending execution. actor carries the email of
// the session-authenticated user who clicked cancel; verified by the
// caller (HTTP path: authorizeApprovalAction; SQS path:
// verifyAsyncApprovalActor) before reaching here. Same empty-actor
// rationale as ApproveExecution.
func (m *Manager) CancelExecution(ctx context.Context, executionID, token, actor string) error {
	logging.Infof("Cancelling execution: %s", executionID)

	execution, err := m.loadCancelableExecution(ctx, executionID, token)
	if err != nil {
		return err
	}

	// Update status + attribution — see ApproveExecution for the empty-actor
	// nil-vs-empty-string rationale. Paired with DeleteSuppressionsByExecution
	// in the same transaction so the status flip and the un-suppression
	// commit atomically — a crash between the two would otherwise leave the
	// rec-list hiding capacity the user already cancelled.
	execution.Status = "cancelled"
	if actor != "" {
		a := actor
		execution.CancelledBy = &a
	}
	if err := m.config.WithTx(ctx, func(tx pgx.Tx) error {
		if err := m.config.SavePurchaseExecutionTx(ctx, tx, execution); err != nil {
			return err
		}
		return m.config.DeleteSuppressionsByExecutionTx(ctx, tx, executionID)
	}); err != nil {
		return fmt.Errorf("failed to save execution: %w", err)
	}

	logging.Infof("Execution %s cancelled", executionID)
	return nil
}

// loadCancelableExecution fetches an execution, validates the approval
// token, and checks the status is cancelable. Extracted from
// CancelExecution to keep both functions below the gocyclo threshold.
func (m *Manager) loadCancelableExecution(ctx context.Context, executionID, token string) (*config.PurchaseExecution, error) {
	execution, err := m.config.GetExecutionByID(ctx, executionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get execution: %w", err)
	}
	if execution == nil {
		return nil, fmt.Errorf("execution not found: %s", executionID)
	}
	if execution.ApprovalToken == "" || token == "" {
		return nil, fmt.Errorf("invalid approval token")
	}
	if subtle.ConstantTimeCompare([]byte(execution.ApprovalToken), []byte(token)) != 1 {
		return nil, fmt.Errorf("invalid approval token")
	}
	if execution.Status == "completed" || execution.Status == "cancelled" {
		return nil, fmt.Errorf("execution cannot be cancelled, current status: %s", execution.Status)
	}
	return execution, nil
}
