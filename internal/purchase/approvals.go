package purchase

import (
	"context"
	"crypto/subtle"
	"fmt"
	"time"

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

	// Enforce token TTL (issue #397). Legacy rows that pre-date migration
	// 000051 have ApprovalTokenExpiresAt == nil and are passed through
	// for backward compatibility; all new rows carry a non-nil deadline.
	if execution.ApprovalTokenExpiresAt != nil && time.Now().After(*execution.ApprovalTokenExpiresAt) {
		return fmt.Errorf("approval token has expired")
	}

	// Preflight guard (issue #609): reject non-AWS orphan executions before
	// the cloud SDK is reached. See OrphanExecutionError for the full rationale.
	if err := OrphanExecutionError(execution); err != nil {
		return err
	}

	return m.ApproveAndExecute(ctx, executionID, actor)
}

// OrphanExecutionError returns a descriptive error when the execution has no
// resolvable CloudAccountID and the provider is explicitly non-AWS (issue #609).
// AWS executions with a nil CloudAccountID are passed through because the
// ambient-host-account fallback from PR #607/#604 handles them. Empty
// provider is treated as AWS (legacy rows pre-dating multi-cloud support).
//
// An execution is NOT considered orphan if any individual recommendation
// carries its own non-nil CloudAccountID — multi-rec fan-out executions can
// have a nil execution-level field while individual recs still name the
// target account.
//
// Exported so the HTTP layer (internal/api) can call the same guard without
// duplicating the predicate. Extracted from ApproveExecution to keep that
// function below the gocyclo threshold — same pattern as loadCancelableExecution.
func OrphanExecutionError(execution *config.PurchaseExecution) error {
	if execution.CloudAccountID != nil {
		return nil
	}
	if len(execution.Recommendations) == 0 {
		return nil
	}
	provider := execution.Recommendations[0].Provider
	if provider == "" || provider == "aws" {
		return nil
	}
	// Any rec-level CloudAccountID means at least one recommendation has a
	// concrete target account — the execution is not fully orphaned.
	for i := range execution.Recommendations {
		if execution.Recommendations[i].CloudAccountID != nil {
			return nil
		}
	}
	return fmt.Errorf("execution %s references an account that no longer exists (provider: %s): cancel this purchase — it cannot execute",
		execution.ExecutionID, provider)
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

	// Enforce token TTL (issue #397). Same backward-compat nil-guard as
	// ApproveExecution: legacy rows without ApprovalTokenExpiresAt pass through.
	if execution.ApprovalTokenExpiresAt != nil && time.Now().After(*execution.ApprovalTokenExpiresAt) {
		return nil, fmt.Errorf("approval token has expired")
	}

	// Only pending/notified rows are cancelable — mirrors the session path
	// in cancelPurchaseViaSession (issue #645). The previous predicate
	// rejected only completed/cancelled, which let an email-link holder
	// cancel an approved/running/paused/failed/expired execution that the
	// dashboard user cannot. Restricting to the pre-purchase states is also
	// the in-flight guard: approved/running rows are mid-execution (the AWS
	// commitment is being or has been created), so cancelling them would
	// leave the DB and the cloud out of sync.
	if execution.Status != "pending" && execution.Status != "notified" {
		return nil, fmt.Errorf("execution cannot be cancelled, current status: %s", execution.Status)
	}
	return execution, nil
}
