package purchase

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
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
	t0 := time.Now()
	logging.Infof("purchase[%s]: ApproveExecution entry (auth=token actor=%q)", executionID, maskActor(actor))

	execution, err := m.config.GetExecutionByID(ctx, executionID)
	if errors.Is(err, config.ErrNotFound) {
		return fmt.Errorf("execution not found: %s", executionID)
	}
	if err != nil {
		return fmt.Errorf("failed to get execution: %w", err)
	}

	// Validate token and TTL (Finding #4 + issue #397).
	if err := validateApprovalToken(execution, token); err != nil {
		return err
	}

	// Preflight guard (issue #609): reject non-AWS orphan executions before
	// the cloud SDK is reached. See OrphanExecutionError for the full rationale.
	if checkErr := OrphanExecutionError(execution); checkErr != nil {
		logging.Errorf("purchase[%s]: ApproveExecution preflight rejected after %s: %v",
			executionID, time.Since(t0), checkErr)
		return checkErr
	}

	// Token/SQS path: no authenticated session UUID is available, so the
	// transition is recorded as system-initiated (transitioned_by = NULL).
	err = m.ApproveAndExecute(ctx, executionID, actor, nil)
	if err != nil {
		logging.Errorf("purchase[%s]: ApproveExecution (token path) failed after %s: %v",
			executionID, time.Since(t0), err)
	} else {
		logging.Infof("purchase[%s]: ApproveExecution (token path) completed in %s",
			executionID, time.Since(t0))
		// Token rotation (best-effort): clear the ApprovalToken after a
		// successful approve so a leaked token cannot be replayed for a
		// different action (e.g. revoke). We do this via a follow-up
		// SavePurchaseExecution on a freshly-fetched row so we don't
		// stomp a completed_at or other fields written by executeAndFinalize.
		// The accept-small-race comment: if a concurrent read sees the pre-
		// rotation row between this point and the save completing, it will
		// find an empty-string token and reject the request, which is the
		// desired security outcome even if the window is tiny.
		if rotateErr := m.rotateApprovalToken(ctx, executionID); rotateErr != nil {
			logging.Warnf("purchase[%s]: ApproveExecution: token rotation failed (best-effort): %v", executionID, rotateErr)
		}
	}
	return err
}

// maskActor masks an actor email/username for safe log emission.
// Full email addresses are PII; log only the domain part.
// Empty actor is logged as "<anon>" so the distinction between
// "no actor provided" and "actor was empty string" is visible.
func maskActor(actor string) string {
	if actor == "" {
		return "<anon>"
	}
	if at := len(actor) - 1; at >= 0 {
		for i, c := range actor {
			if c == '@' {
				return "***" + actor[i:]
			}
		}
	}
	// Not an email address -- log only the last 4 chars (e.g. a username).
	if len(actor) > 4 {
		return "..." + actor[len(actor)-4:]
	}
	return "****"
}

// validateApprovalToken checks that the execution carries a non-empty token,
// that the supplied token matches the stored one using constant-time comparison
// (Finding #4 -- prevents timing attacks), and that the token has not expired
// (issue #397). Legacy rows with a nil ApprovalTokenExpiresAt pass the TTL
// check for backward compatibility. Extracted from ApproveExecution to keep
// that function under the gocyclo threshold.
func validateApprovalToken(execution *config.PurchaseExecution, token string) error {
	if execution.ApprovalToken == "" || token == "" {
		return fmt.Errorf("invalid approval token")
	}
	storedHash := sha256.Sum256([]byte(execution.ApprovalToken))
	userHash := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(storedHash[:], userHash[:]) != 1 {
		return fmt.Errorf("invalid approval token")
	}
	if execution.ApprovalTokenExpiresAt != nil && time.Now().After(*execution.ApprovalTokenExpiresAt) {
		return fmt.Errorf("approval token has expired")
	}
	return nil
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
func (m *Manager) ApproveAndExecute(ctx context.Context, executionID, actor string, transitionedBy *string) error {
	t0 := time.Now()
	logging.Infof("purchase[%s]: ApproveAndExecute starting (actor=%q)", executionID, maskActor(actor))

	// transitionedBy carries the session user's UUID for human-initiated
	// approvals (stamped onto transitioned_by); it is nil for token/SQS/system
	// flows so transitioned_by = NULL on those hops. The human-readable actor
	// email is recorded separately onto approved_by (below).
	updated, err := m.config.TransitionExecutionStatus(ctx, executionID, []string{"pending", "notified"}, "approved", transitionedBy)
	if err != nil {
		logging.Errorf("purchase[%s]: ApproveAndExecute status transition failed after %s: %v",
			executionID, time.Since(t0), err)
		return fmt.Errorf("approve: %w", err)
	}
	logging.Infof("purchase[%s]: status transitioned to approved in %s", executionID, time.Since(t0))

	if actor != "" {
		a := actor
		updated.ApprovedBy = &a
		if saveErr := m.config.SavePurchaseExecution(ctx, updated); saveErr != nil {
			// Attribution is best-effort once the atomic flip has landed --
			// dropping ApprovedBy must not stop the purchase from firing.
			// Log loudly so the audit gap is visible.
			logging.Errorf("AUDIT GAP: failed to stamp approved_by on %s: %v", executionID, saveErr)
		}
	}

	logging.Infof("purchase[%s]: executing purchase synchronously", executionID)
	execErr := m.executeAndFinalize(ctx, updated)
	if execErr != nil {
		logging.Errorf("purchase[%s]: ApproveAndExecute failed after %s: %v", executionID, time.Since(t0), execErr)
	} else {
		logging.Infof("purchase[%s]: ApproveAndExecute completed in %s", executionID, time.Since(t0))
	}
	return execErr
}

// CancelExecution cancels a pending execution. actor carries the email of
// the session-authenticated user who clicked cancel; verified by the
// caller (HTTP path: authorizeApprovalAction; SQS path:
// verifyAsyncApprovalActor) before reaching here. Same empty-actor
// rationale as ApproveExecution.
//
// Concurrency: CancelExecutionAtomic uses a conditional UPDATE WHERE
// status IN ('pending','notified') so a concurrent approve that wins
// the race causes zero rows to be affected and the caller receives a
// clean error with the current status rather than silently overwriting
// the approved row. This is the token/email-link cancel analog of
// the atomic guard TransitionExecutionStatus provides for ApproveAndExecute.
func (m *Manager) CancelExecution(ctx context.Context, executionID, token, actor string) error {
	logging.Infof("Canceling execution: %s", executionID)

	if _, err := m.loadCancelableExecution(ctx, executionID, token); err != nil {
		return err
	}

	// Build the nullable canceled_by pointer — see ApproveExecution for
	// the nil-vs-empty-string rationale.
	var cancelledBy *string
	if actor != "" {
		a := actor
		cancelledBy = &a
	}

	// Atomic conditional UPDATE + suppression cleanup in one transaction.
	// CancelExecutionAtomic flips status only when status IN
	// ('pending','notified') so a concurrent approve that has already
	// transitioned the row causes zero rows affected and we surface a 409.
	var canceled bool
	var currentStatus string
	if err := m.config.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		canceled, currentStatus, err = m.config.CancelExecutionAtomic(ctx, tx, executionID, cancelledBy)
		if err != nil {
			return err
		}
		if !canceled {
			// Row already transitioned (concurrent approve/cancel won the
			// race). Return early without touching suppressions — the other
			// operation owns the execution state now.
			return nil
		}
		return m.config.DeleteSuppressionsByExecutionTx(ctx, tx, executionID)
	}); err != nil {
		return fmt.Errorf("failed to cancel execution: %w", err)
	}

	if !canceled {
		return fmt.Errorf("execution %s cannot be canceled: concurrent operation already transitioned it to %q", executionID, currentStatus)
	}

	logging.Infof("Execution %s canceled", executionID)
	// Token rotation (best-effort): same rationale as in ApproveExecution.
	if rotateErr := m.rotateApprovalToken(ctx, executionID); rotateErr != nil {
		logging.Warnf("purchase[%s]: CancelExecution: token rotation failed (best-effort): %v", executionID, rotateErr)
	}
	return nil
}

// rotateApprovalToken clears the ApprovalToken on the execution after a
// successful approve or cancel. This prevents a leaked token from being
// replayed for a different action (e.g. using an old approval token to
// trigger a revoke). The rotation is best-effort: if the follow-up save
// fails the operation (approve/cancel) has already landed, so we only
// warn rather than surfacing an error to the caller.
func (m *Manager) rotateApprovalToken(ctx context.Context, executionID string) error {
	exec, err := m.config.GetExecutionByID(ctx, executionID)
	if err != nil {
		return fmt.Errorf("rotateApprovalToken: failed to fetch execution: %w", err)
	}
	if exec == nil {
		return fmt.Errorf("rotateApprovalToken: execution not found: %s", executionID)
	}
	exec.ApprovalToken = ""
	exec.ApprovalTokenExpiresAt = nil
	return m.config.SavePurchaseExecution(ctx, exec)
}

// loadCancelableExecution fetches an execution, validates the approval
// token, and checks the status is cancelable. Extracted from
// CancelExecution to keep both functions below the gocyclo threshold.
func (m *Manager) loadCancelableExecution(ctx context.Context, executionID, token string) (*config.PurchaseExecution, error) {
	execution, err := m.config.GetExecutionByID(ctx, executionID)
	if errors.Is(err, config.ErrNotFound) {
		return nil, fmt.Errorf("execution not found: %s", executionID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get execution: %w", err)
	}
	if execution.ApprovalToken == "" || token == "" {
		return nil, fmt.Errorf("invalid approval token")
	}
	storedHash := sha256.Sum256([]byte(execution.ApprovalToken))
	userHash := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(storedHash[:], userHash[:]) != 1 {
		return nil, fmt.Errorf("invalid approval token")
	}

	// Enforce token TTL (issue #397). Same backward-compat nil-guard as
	// ApproveExecution: legacy rows without ApprovalTokenExpiresAt pass through.
	if execution.ApprovalTokenExpiresAt != nil && time.Now().After(*execution.ApprovalTokenExpiresAt) {
		return nil, fmt.Errorf("approval token has expired")
	}

	// Only pre-purchase rows (pending/notified/scheduled) are cancelable —
	// shares the single PurchaseExecution.IsCancelable predicate with the
	// session path in cancelPurchaseViaSession so the policy can never drift
	// between the two flows (issue #645). The "scheduled" state is also
	// cancelable because the cloud SDK has not been called yet (issue #291
	// wave-2). The previous predicate rejected only completed/canceled, which
	// let an email-link holder cancel an approved/running/paused/failed/expired
	// execution that the dashboard user cannot. Restricting to the pre-purchase
	// states is also the in-flight guard: approved/running rows are
	// mid-execution (the AWS commitment is being or has been created), so
	// canceling them would leave the DB and the cloud out of sync.
	if !execution.IsCancelable() {
		return nil, fmt.Errorf("execution cannot be canceled, current status: %s", execution.Status)
	}
	return execution, nil
}
