package purchase

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/common"
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
	if tokErr := validateApprovalToken(execution, token); tokErr != nil {
		return tokErr
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
		// Token rotation (best-effort): mint a fresh revocation token after a
		// successful approve. The old approval token must not double as a
		// revocation token: (a) it was consumed to authorize this approve action
		// and (b) clearing it (old behavior) caused the post-execution email to
		// embed an already-invalidated token, making every "Revoke" click return
		// 403 (issue #291 wave-2 adversarial review finding).
		//
		// mintRevocationToken fetches the freshly-finalized row (so we don't
		// stomp completed_at or other fields), generates a new random token with
		// a 24-hour expiry matching the revocation window, and persists it.
		// The handler re-fetches the execution after this call to obtain the
		// fresh token for the email.
		//
		// Security: re-approval with the old token is still blocked by the status
		// check in TransitionExecutionStatus (the row is now "completed", not
		// "pending"/"notified"), so token rotation is NOT required to prevent
		// approve-replay. The new token is scoped to revoke-only via the status
		// check in checkRevokableStatus ("completed" required).
		if rotateErr := m.mintRevocationToken(ctx, executionID); rotateErr != nil {
			logging.Warnf("purchase[%s]: ApproveExecution: revocation token mint failed (best-effort): %v", executionID, rotateErr)
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

// enforceFourEyesPolicy is the UNIVERSAL 4-eyes approval gate (issue #1005).
// It is the single choke point ApproveAndExecute runs before mutating any
// execution state, so every approve/execute entry point inherits the policy
// regardless of which caller reaches ApproveAndExecute:
//
//   - approvePurchaseViaSession (session-authed dashboard approve)
//   - directExecutePurchase (execute_mode="direct" -- issue #289)
//   - ApproveExecution (email-token deep-link approve AND the SQS async
//     approve worker, both of which funnel into ApproveAndExecute)
//
// Adversarial review of PR #1500 found that requireDifferentApprover in
// internal/api was wired into only 2 of these 4 entry points, leaving
// execute_mode="direct" (HIGH: the requester always equals the creator on
// this path, so this is unconditional self-authorization) and the SQS
// approve worker (MEDIUM: token + actor_email verification never compared
// the actor against the creator) able to bypass dual control entirely. This
// method closes both gaps by running at the one place all four paths share.
//
// Identity comparison has two tiers, checked in order of precision:
//
//  1. actorUserID (== transitionedBy, the actor's own UUID): populated by the
//     two session-based callers (approvePurchaseViaSession, directExecutePurchase)
//     via validUUIDPtrOrNil(&session.UserID). When present it is compared
//     DIRECTLY against execution.CreatedByUserID -- both are UUIDs from the
//     same `users` table, so this is authoritative identity, not a proxy.
//     This tier is what closes a gap an independent adversarial review found
//     in an earlier version of this fix: a per-user API key session
//     (Session.UserAPIKeyID != "") carries a real user UUID but an EMPTY
//     Session.Email, unlike a normal bearer-token session. An email-only
//     comparison would fall back to a non-email placeholder for that empty
//     Email and could never match the creator's real email, silently
//     allowing self-direct-execute. Comparing UUIDs first sidesteps the
//     email domain entirely for these two callers and cannot be fooled by an
//     empty or substituted email string.
//  2. actorEmail: used only when actorUserID is nil -- the token
//     (approveViaToken) and SQS (handleApproveMessage) callers into
//     ApproveExecution, which always pass transitionedBy=nil (see
//     ApproveExecution's own doc comment), so the RBAC/contact-email-
//     verified actor identity from authorizeApprovalAction /
//     verifyAsyncApprovalActor is the only signal available there.
//     execution.CreatedByUserID is a UUID; internal/purchase cannot resolve
//     it through internal/auth directly (internal/auth already imports
//     internal/config, so the reverse import would cycle), so
//     GetUserEmailByID resolves the creator's email via a minimal,
//     auth-type-free query on the shared config store instead.
//
// Fails CLOSED whenever mode is on and any of the following holds: the
// execution has no recorded creator (legacy row); neither actorUserID nor a
// non-empty actorEmail identify the acting party; or the resolved identity
// (UUID or email) matches the creator's.
func (m *Manager) enforceFourEyesPolicy(ctx context.Context, executionID, actorEmail string, actorUserID *string) error {
	cfg, err := m.config.GetGlobalConfig(ctx)
	if err != nil {
		return fmt.Errorf("4-eyes policy check: failed to load global config: %w", err)
	}
	if cfg == nil || !cfg.RequireDifferentApprover {
		return nil
	}

	execution, err := m.loadExecutionForFourEyes(ctx, executionID)
	if err != nil || execution == nil {
		return err
	}
	return m.checkDifferentApprover(ctx, executionID, execution, actorEmail, actorUserID)
}

// loadExecutionForFourEyes fetches the execution enforceFourEyesPolicy needs
// to compare identities against. Returns (nil, nil) when the executionID
// does not resolve to a row -- there is nothing to gate, and the caller's
// subsequent TransitionExecutionStatus surfaces the standard not-found/
// cannot-transition error for a bogus or already-terminal executionID.
// Extracted from enforceFourEyesPolicy to keep it under the gocyclo
// threshold.
func (m *Manager) loadExecutionForFourEyes(ctx context.Context, executionID string) (*config.PurchaseExecution, error) {
	execution, err := m.config.GetExecutionByID(ctx, executionID)
	if errors.Is(err, config.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("4-eyes policy check: failed to load execution: %w", err)
	}
	return execution, nil
}

// checkDifferentApprover runs the actual identity comparison once mode is
// confirmed on and the execution is loaded. Extracted from
// enforceFourEyesPolicy to keep it under the gocyclo threshold.
func (m *Manager) checkDifferentApprover(ctx context.Context, executionID string, execution *config.PurchaseExecution, actorEmail string, actorUserID *string) error {
	if execution.CreatedByUserID == nil {
		logging.Warnf("purchase[%s]: 4-eyes mode on; NULL creator (legacy row), denying", executionID)
		return fmt.Errorf("approval declined: this execution predates the dual-control feature and has no recorded creator; an admin must disable 4-eyes mode to approve")
	}

	// Tier 1: authoritative UUID comparison when the caller identified the
	// actor's own user row (session-based callers). See enforceFourEyesPolicy's
	// doc comment for why this must run before any email-based fallback.
	if actorUserID != nil {
		if *actorUserID == *execution.CreatedByUserID {
			logging.Warnf("purchase[%s]: 4-eyes mode on; creator %s attempted self-approval (actor UUID match), denied",
				executionID, *execution.CreatedByUserID)
			return fmt.Errorf("approval declined: 4-eyes mode requires a different approver than the requester")
		}
		return nil
	}

	// Tier 2: no actor UUID available (token/SQS callers) -- fall back to
	// resolving and comparing emails.
	actorEmail = strings.TrimSpace(actorEmail)
	if actorEmail == "" {
		logging.Warnf("purchase[%s]: 4-eyes mode on; no actor identity available, denying (fail-closed)", executionID)
		return fmt.Errorf("4-eyes approval mode is enabled but no approver identity could be determined; sign in or supply a verified actor before approving")
	}

	creatorEmail, err := m.config.GetUserEmailByID(ctx, *execution.CreatedByUserID)
	if err != nil {
		return fmt.Errorf("4-eyes policy check: failed to resolve creator identity: %w", err)
	}
	creatorEmail = strings.TrimSpace(creatorEmail)
	if creatorEmail == "" {
		logging.Warnf("purchase[%s]: 4-eyes mode on; creator account %s not found, denying (fail-closed)",
			executionID, *execution.CreatedByUserID)
		return fmt.Errorf("4-eyes approval mode is enabled but the creator's account could not be resolved; an admin must investigate before approving")
	}

	if strings.EqualFold(creatorEmail, actorEmail) {
		logging.Warnf("purchase[%s]: 4-eyes mode on; creator %s attempted self-approval via actor %q, denied",
			executionID, *execution.CreatedByUserID, maskActor(actorEmail))
		return fmt.Errorf("approval declined: 4-eyes mode requires a different approver than the requester")
	}
	return nil
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

	// Universal 4-eyes gate (issue #1005 / PR #1500 adversarial review): runs
	// before any state mutation so every caller -- session approve, direct
	// execute, token approve, SQS approve -- is covered by one policy check.
	// transitionedBy doubles as the actor's own UUID for this check when the
	// caller has one (session-based callers); see enforceFourEyesPolicy's doc
	// comment for the full rationale.
	if err := m.enforceFourEyesPolicy(ctx, executionID, actor, transitionedBy); err != nil {
		logging.Warnf("purchase[%s]: ApproveAndExecute denied by 4-eyes policy: %v", executionID, err)
		return err
	}

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
	// Token clear (best-effort): a canceled execution has no valid follow-up
	// action, so the approval token is cleared to prevent replay. Unlike the
	// approve path (which mints a fresh revocation token), cancel produces no
	// usable successor action, so an empty token is correct here.
	if rotateErr := m.clearApprovalToken(ctx, executionID); rotateErr != nil {
		logging.Warnf("purchase[%s]: CancelExecution: token clear failed (best-effort): %v", executionID, rotateErr)
	}
	return nil
}

// mintRevocationToken generates a fresh cryptographically-secure revocation
// token, stamps it onto the execution with a revocationTokenWindow expiry,
// and persists the update. Called after a successful token-authed approve so
// the post-execution email carries a valid revoke-capable token rather than
// the now-consumed approval token.
//
// The handler re-fetches the execution after ApproveExecution returns to
// pick up the fresh token for embedding in the email.
//
// Best-effort: if the write fails the approve has already landed and this
// returns an error the caller only logs. The stored token is then unchanged
// (still the consumed approval token), so the handler's re-fetch surfaces that
// token and the email carries it -- it still validates for revoke because it
// was never rotated (degraded to the old reuse-the-approval-token behavior on
// this rare path, but functional and safe). The distinct failure mode where the
// handler's re-fetch itself errors is handled in approveViaToken by blanking
// ApprovalToken so the Revoke panel is suppressed rather than emailing a token
// whose validity is unknown.
func (m *Manager) mintRevocationToken(ctx context.Context, executionID string) error {
	exec, err := m.config.GetExecutionByID(ctx, executionID)
	if err != nil {
		return fmt.Errorf("mintRevocationToken: failed to fetch execution: %w", err)
	}
	if exec == nil {
		return fmt.Errorf("mintRevocationToken: execution not found: %s", executionID)
	}
	tok, err := common.GenerateApprovalToken()
	if err != nil {
		return fmt.Errorf("mintRevocationToken: failed to generate token: %w", err)
	}
	expiry := time.Now().Add(config.RevocationWindow)
	exec.ApprovalToken = tok
	exec.ApprovalTokenExpiresAt = &expiry
	return m.config.SavePurchaseExecution(ctx, exec)
}

// clearApprovalToken clears the ApprovalToken on the execution after a
// successful cancel. A canceled execution has no valid follow-up action, so
// clearing the token prevents replay for any purpose. Unlike the approve path
// (which mints a fresh revocation token for the revoke-window email link),
// cancel produces no usable successor action.
//
// Best-effort: if the follow-up save fails the cancel has already landed, so
// we only warn rather than surfacing an error to the caller.
func (m *Manager) clearApprovalToken(ctx context.Context, executionID string) error {
	exec, err := m.config.GetExecutionByID(ctx, executionID)
	if err != nil {
		return fmt.Errorf("clearApprovalToken: failed to fetch execution: %w", err)
	}
	if exec == nil {
		return fmt.Errorf("clearApprovalToken: execution not found: %s", executionID)
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

	// Only pre-purchase rows (pending/notified/scheduled) are cancelable --
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
