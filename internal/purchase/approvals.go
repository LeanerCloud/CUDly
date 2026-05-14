package purchase

import (
	"context"
	"crypto/subtle"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/jackc/pgx/v5"
)

// ApproveExecution is the token-authenticated approve entry point used by
// the legacy email-link flow and the SQS approve worker. After validating
// the approval token it hands off to ApproveAndExecute, which performs the
// atomic status transition and runs the AWS purchase synchronously.
//
// actor carries the email of the operator who triggered the approval
// (session-authed click on the HTTP path; verified actor_email on the SQS
// path) so it can be stamped onto ApprovedBy. Empty actor is recorded as
// NULL — the column is nullable TEXT and we don't want to claim "approved
// by nobody".
func (m *Manager) ApproveExecution(ctx context.Context, executionID, token, actor string) error {
	logging.Infof("Approving execution: %s", executionID)

	execution, err := m.config.GetExecutionByID(ctx, executionID)
	if err != nil {
		return fmt.Errorf("failed to get execution: %w", err)
	}
	if execution == nil {
		return fmt.Errorf("execution not found: %s", executionID)
	}

	// Validate token using constant-time comparison to prevent timing attacks.
	if execution.ApprovalToken == "" || token == "" {
		return fmt.Errorf("invalid approval token")
	}
	if subtle.ConstantTimeCompare([]byte(execution.ApprovalToken), []byte(token)) != 1 {
		return fmt.Errorf("invalid approval token")
	}

	return m.ApproveAndExecute(ctx, executionID, actor)
}

// ApproveAndExecute atomically flips a pending/notified execution to
// "approved" (stamping ApprovedBy) and then runs the purchase
// synchronously, returning the final outcome. Callers MUST have already
// authorized the actor — this method does no RBAC or token validation; it
// is shared between:
//
//   - ApproveExecution (token path: token validated by the caller)
//   - approvePurchaseViaSession (session path: RBAC validated by the caller)
//
// Concurrency: TransitionExecutionStatus uses an atomic UPDATE WHERE status
// IN ('pending','notified'). Two callers racing to approve the same row
// will see exactly one win — the loser receives a clear "cannot
// transition" error. Cross-execution concurrency is unaffected: each
// approval drives its own executeAndFinalize, which already fans out
// per-account in parallel via executeMultiAccount.
func (m *Manager) ApproveAndExecute(ctx context.Context, executionID, actor string) error {
	updated, err := m.config.TransitionExecutionStatus(ctx, executionID, []string{"pending", "notified"}, "approved")
	if err != nil {
		return fmt.Errorf("approve: %w", err)
	}

	if actor != "" {
		a := actor
		updated.ApprovedBy = &a
		if saveErr := m.config.SavePurchaseExecution(ctx, updated); saveErr != nil {
			// Attribution is best-effort once the atomic flip has landed —
			// dropping ApprovedBy must not stop the purchase from firing.
			// Log loudly so the audit gap is visible.
			logging.Errorf("AUDIT GAP: failed to stamp approved_by on %s: %v", executionID, saveErr)
		}
	}

	logging.Infof("Execution %s approved, executing synchronously", executionID)
	return m.executeAndFinalize(ctx, updated)
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
