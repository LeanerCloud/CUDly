// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// buildSuppressions converts the recs on an executePurchase request into
// purchase_suppressions rows, one per unique 6-tuple (account, provider,
// service, region, resource_type, engine). Counts for duplicate tuples
// within the same request are summed (defensive — the UI should collapse
// them client-side, but the backend doesn't trust that). Providers with
// a grace period of 0 (feature disabled) contribute no rows.
func buildSuppressions(recs []config.RecommendationRecord, executionID string, cfg *config.GlobalConfig, now time.Time) []config.PurchaseSuppression {
	// Aggregate count per 6-tuple before writing suppression rows so
	// the UNIQUE(execution_id, ...6-tuple) constraint can't fire from
	// a request that repeated the same tuple.
	type key struct {
		accountID, provider, service, region, resourceType, engine string
	}
	agg := map[key]int{}
	order := []key{}
	for _, rec := range recs {
		if rec.Count <= 0 {
			continue
		}
		accountID := ""
		if rec.CloudAccountID != nil {
			accountID = *rec.CloudAccountID
		}
		k := key{
			accountID:    accountID,
			provider:     rec.Provider,
			service:      rec.Service,
			region:       rec.Region,
			resourceType: rec.ResourceType,
			engine:       rec.Engine,
		}
		if _, seen := agg[k]; !seen {
			order = append(order, k)
		}
		agg[k] += rec.Count
	}

	out := make([]config.PurchaseSuppression, 0, len(order))
	for _, k := range order {
		graceDays := config.DefaultGracePeriodDays
		if cfg != nil {
			graceDays = cfg.GracePeriodFor(k.provider)
		}
		if graceDays <= 0 {
			continue // feature disabled for this provider
		}
		out = append(out, config.PurchaseSuppression{
			ExecutionID:     executionID,
			AccountID:       k.accountID,
			Provider:        k.provider,
			Service:         k.service,
			Region:          k.region,
			ResourceType:    k.resourceType,
			Engine:          k.engine,
			SuppressedCount: agg[k],
			ExpiresAt:       now.Add(time.Duration(graceDays) * 24 * time.Hour),
		})
	}
	return out
}

// plannedListStatuses are the execution statuses surfaced in the Scheduled
// (Planned) Purchases list. It deliberately includes "paused" so a paused
// execution stays VISIBLE with its badge instead of silently dropping out of
// the list. It does NOT use GetPendingExecutions, which the
// scheduler relies on to decide what to FIRE: paused rows must be listed but
// never fired, so the two concerns use different status sets.
var plannedListStatuses = []string{"pending", "notified", "paused"}

func (h *Handler) getPlannedPurchases(ctx context.Context, req *events.LambdaFunctionURLRequest) (*PlannedPurchasesResponse, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	// GetPlannedExecutions orders scheduled_date ASC at the DB level so the
	// soonest-first list isn't truncated when total rows exceed MaxListLimit.
	// (GetExecutionsByStatuses uses DESC + LIMIT for History; mixing them here
	// drops the genuinely-soonest rows, exactly the rows this list must show.
	// An in-memory re-sort cannot recover what LIMIT already discarded.)
	executions, err := h.config.GetPlannedExecutions(ctx, plannedListStatuses, config.MaxListLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to get planned executions: %w", err)
	}

	plans, err := h.config.ListPurchasePlans(ctx, config.PurchasePlanFilter{})
	if err != nil {
		return nil, fmt.Errorf("failed to get purchase plans: %w", err)
	}

	planMap := make(map[string]*config.PurchasePlan, len(plans))
	for i := range plans {
		planMap[plans[i].ID] = &plans[i]
	}

	// Cache per-plan access decisions — all executions for the same plan share
	// the same account scope. Avoids GetPlanAccounts round-trips per execution.
	allowedPlan := make(map[string]bool)

	var purchases []PlannedPurchase
	for _, exec := range executions {
		plan := planMap[exec.PlanID]
		if plan == nil {
			continue
		}
		ok, err := h.isPlanAllowedCached(ctx, session, exec.PlanID, allowedPlan)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		purchases = append(purchases, buildPlannedPurchase(plan, &exec))
	}

	return &PlannedPurchasesResponse{
		Purchases: purchases,
	}, nil
}

// isPlanAllowedCached resolves and memoises whether the session may see the given plan.
// NotFound errors are treated as "not allowed" (not an error) so missing plans don't
// surface as 500s, mirroring the previous inline behaviour.
func (h *Handler) isPlanAllowedCached(ctx context.Context, session *Session, planID string, cache map[string]bool) (bool, error) {
	if ok, cached := cache[planID]; cached {
		return ok, nil
	}
	planErr := h.requirePlanAccess(ctx, session, planID)
	if planErr != nil && !IsNotFoundError(planErr) {
		return false, planErr
	}
	ok := planErr == nil
	cache[planID] = ok
	return ok, nil
}

// buildPlannedPurchase converts a (plan, execution) pair into the API-facing PlannedPurchase.
// Provider/service/term/payment are taken from the first service entry, matching prior behaviour.
func buildPlannedPurchase(plan *config.PurchasePlan, exec *config.PurchaseExecution) PlannedPurchase {
	var provider, service, payment string
	var term int
	for _, svcCfg := range plan.Services {
		provider = svcCfg.Provider
		service = svcCfg.Service
		term = svcCfg.Term
		payment = svcCfg.Payment
		break
	}
	return PlannedPurchase{
		ID:               exec.ExecutionID,
		PlanID:           exec.PlanID,
		PlanName:         plan.Name,
		ScheduledDate:    exec.ScheduledDate.Format("2006-01-02"),
		Provider:         provider,
		Service:          service,
		ResourceType:     "Various",
		Region:           "Multiple",
		Count:            len(exec.Recommendations),
		Term:             term,
		Payment:          payment,
		EstimatedSavings: exec.EstimatedSavings,
		UpfrontCost:      exec.TotalUpfrontCost,
		Status:           exec.Status,
		StepNumber:       exec.StepNumber,
		TotalSteps:       plan.RampSchedule.TotalSteps,
	}
}

func (h *Handler) pausePlannedPurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, executionID string) (*StatusResponse, error) {
	if err := validateUUID(executionID); err != nil {
		return nil, err
	}

	session, err := h.requirePermission(ctx, req, "update", "purchases")
	if err != nil {
		return nil, err
	}
	if err := h.requireExecutionAccess(ctx, session, executionID); err != nil {
		return nil, err
	}

	// Atomically transition to paused
	if _, err := h.config.TransitionExecutionStatus(ctx, executionID, []string{"pending", "running"}, "paused"); err != nil {
		return nil, NewClientError(409, fmt.Sprintf("execution %s cannot be paused: %v", executionID, err))
	}

	return &StatusResponse{Status: "paused"}, nil
}

func (h *Handler) resumePlannedPurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, executionID string) (*StatusResponse, error) {
	if err := validateUUID(executionID); err != nil {
		return nil, err
	}

	session, err := h.requirePermission(ctx, req, "update", "purchases")
	if err != nil {
		return nil, err
	}
	if err := h.requireExecutionAccess(ctx, session, executionID); err != nil {
		return nil, err
	}

	// Atomically transition from paused back to pending
	if _, err := h.config.TransitionExecutionStatus(ctx, executionID, []string{"paused"}, "pending"); err != nil {
		return nil, NewClientError(409, fmt.Sprintf("execution %s cannot be resumed: %v", executionID, err))
	}

	return &StatusResponse{Status: "resumed"}, nil
}

func (h *Handler) runPlannedPurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, executionID string) (any, error) {
	if err := validateUUID(executionID); err != nil {
		return nil, err
	}

	session, err := h.requirePermission(ctx, req, "execute", "purchases")
	if err != nil {
		return nil, err
	}
	if err := h.requireExecutionAccess(ctx, session, executionID); err != nil {
		return nil, err
	}

	// Atomically transition to running — only one concurrent caller can succeed.
	// TransitionExecutionStatus handles not-found and wrong-status cases.
	if _, err := h.config.TransitionExecutionStatus(ctx, executionID, []string{"pending", "paused"}, "running"); err != nil {
		return nil, NewClientError(409, fmt.Sprintf("execution %s cannot be started: %v", executionID, err))
	}

	return map[string]any{
		"execution_id": executionID,
		"status":       "running",
		"message":      "Purchase execution initiated",
	}, nil
}

func (h *Handler) deletePlannedPurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, executionID string) (*StatusResponse, error) {
	if err := validateUUID(executionID); err != nil {
		return nil, err
	}

	session, err := h.requirePermission(ctx, req, "delete", "purchases")
	if err != nil {
		return nil, err
	}
	if err := h.requireExecutionAccess(ctx, session, executionID); err != nil {
		return nil, err
	}

	// Cancel the scheduled execution. The RETURNING clause gives us the
	// parent plan_id so we can disable the plan in the same handler call.
	//
	// Idempotency: if TransitionExecutionStatus returns
	// ErrExecutionNotInExpectedStatus the row is already in a terminal state
	// (most likely "cancelled" from a previous attempt). In that case we
	// fetch the execution to recover the PlanID and still attempt to disable
	// the plan, so a retry never leaves plan.enabled=true.
	cancelled, err := h.config.TransitionExecutionStatus(ctx, executionID, []string{"pending", "paused"}, "cancelled")
	if err != nil {
		if !errors.Is(err, config.ErrExecutionNotInExpectedStatus) {
			return nil, NewClientError(409, fmt.Sprintf("execution %s cannot be cancelled: %v", executionID, err))
		}
		// The cancel already landed (e.g. a prior request succeeded and was
		// retried). Recover the execution so we can still disable the plan.
		existing, getErr := h.config.GetExecutionByID(ctx, executionID)
		if getErr != nil {
			return nil, fmt.Errorf("disable plan: failed to get execution %s after conflict: %w", executionID, getErr)
		}
		if existing == nil {
			return nil, NewClientError(404, fmt.Sprintf("execution %s not found", executionID))
		}
		cancelled = existing
	}

	// Set the parent plan's enabled flag to false so the Plans page toggle
	// reflects the disable action immediately. Issue #774: previously the
	// execution was cancelled but plan.enabled was left true, causing
	// inconsistent state between the Scheduled Purchases and Plans views.
	if cancelled.PlanID != "" {
		if err := h.disablePlan(ctx, cancelled.PlanID); err != nil {
			return nil, err
		}
	}

	return &StatusResponse{Status: "cancelled"}, nil
}

// disablePlan fetches the plan identified by planID and sets Enabled=false if
// it is currently true. It is idempotent: calling it against an already-
// disabled plan is a no-op. Returns a 404 ClientError when the plan does not
// exist, and a 500-wrapped error for any other store failure.
func (h *Handler) disablePlan(ctx context.Context, planID string) error {
	plan, err := h.config.GetPurchasePlan(ctx, planID)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return NewClientError(404, fmt.Sprintf("disable plan: plan %s not found", planID))
		}
		return fmt.Errorf("disable plan: failed to fetch plan %s: %w", planID, err)
	}
	if plan.Enabled {
		plan.Enabled = false
		if err := h.config.UpdatePurchasePlan(ctx, plan); err != nil {
			return fmt.Errorf("disable plan: failed to update plan %s: %w", planID, err)
		}
	}
	return nil
}

// loadApproveExecution fetches an execution by ID, returns a 404 ClientError
// when not found, and then runs the issue-#609 orphan preflight check.
// Extracted from approvePurchase to keep that function below the gocyclo
// threshold — same pattern as loadCancelableExecution in the purchase package.
func (h *Handler) loadApproveExecution(ctx context.Context, execID string) (*config.PurchaseExecution, error) {
	execution, err := h.config.GetExecutionByID(ctx, execID)
	if err != nil {
		return nil, fmt.Errorf("failed to get execution: %w", err)
	}
	if execution == nil {
		return nil, NewClientError(404, "execution not found")
	}
	// Preflight (issue #609): reject non-AWS orphan executions before the
	// cloud SDK is reached. Delegates to the centralized predicate in the
	// purchase package so the logic is maintained in one place.
	if err := purchase.OrphanExecutionError(execution); err != nil {
		return nil, NewClientError(409, err.Error())
	}
	return execution, nil
}

// Purchase action handlers
func (h *Handler) approvePurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, execID, token string) (any, error) {
	if err := validateUUID(execID); err != nil {
		return nil, err
	}

	execution, err := h.loadApproveExecution(ctx, execID)
	if err != nil {
		return nil, err
	}

	// Three-mode dispatch — same shape as cancelPurchase (issue #46) and
	// retryPurchase (issue #47):
	//   1. Session present AND RBAC-authorized (admin / approve-any /
	//      approve-own match) → session-authed approve, regardless of
	//      whether a token is in the URL. Closes issue #286: an admin
	//      logged into the dashboard can now approve from the History
	//      view without round-tripping to the SES email.
	//   2. token != "" → legacy email-link flow without a qualifying
	//      session. authorizeApprovalAction enforces the per-account
	//      contact_email gate from PR #101; the purchase service
	//      validates the token itself before mutating state.
	//   3. token == "" → session-authed dashboard Approve button.
	//      approvePurchaseViaSession runs the approve-any /
	//      approve-own RBAC matrix and rejects sessions without it.
	if session := h.tryGetSession(ctx, req); session != nil {
		switch err := h.authorizeSessionApprove(ctx, session, execution); {
		case err == nil:
			return h.approvePurchaseViaSession(ctx, req, execution)
		case isPermissionDenied(err):
			// Explicit 403 → fall through to the token branch so the
			// contact_email gate gets a chance (a logged-in user without
			// approve-* may still be the per-account contact recipient).
		default:
			// Transient failure — propagate instead of silently widening.
			return nil, err
		}
	}

	if token != "" {
		actor, err := h.authorizeApprovalAction(ctx, req, execution)
		if err != nil {
			return nil, err
		}
		// ApproveExecution now runs the purchase synchronously inside the
		// same call (issue #372). When it returns nil the AWS API call
		// has already happened, so the response surfaces "completed"
		// instead of the transient "approved" the old no-op flow returned.
		if err := h.purchase.ApproveExecution(ctx, execID, token, actor); err != nil {
			return nil, err
		}
		return map[string]string{"status": "completed"}, nil
	}

	return h.approvePurchaseViaSession(ctx, req, execution)
}

// approvePurchaseViaSession is the session-authed branch of approvePurchase.
// Enforces the approve-any/approve-own RBAC matrix, then hands off to
// purchase.Manager.ApproveAndExecute which atomically flips the row to
// "approved" (stamping session.Email onto ApprovedBy) and runs the
// purchase synchronously. The synchronous-execute is what closes the gap
// from issue #372 — pre-fix, approval was a no-op beyond the status flip
// because no scheduler picked the "approved" row up for the Lambda
// deployment.
//
// Concurrency: ApproveAndExecute uses an atomic transition, so two
// in-flight Approve clicks on the same execution see exactly one win.
// Approvals across different executions run independently — each HTTP
// invocation drives its own executeAndFinalize, which already fans out
// per-account in parallel via executeMultiAccount.
func (h *Handler) approvePurchaseViaSession(ctx context.Context, req *events.LambdaFunctionURLRequest, execution *config.PurchaseExecution) (any, error) {
	t0 := time.Now()
	logging.Infof("purchase[%s]: approvePurchaseViaSession entry (auth=session)", execution.ExecutionID)

	// These endpoints are AuthPublic so the outer middleware skips CSRF.
	// Enforce it here for the session-authed sub-path: the session bearer
	// token is cookie-equivalent and must be CSRF-protected.
	if err := h.validateCSRF(ctx, req); err != nil {
		return nil, NewClientError(403, "CSRF validation failed")
	}

	session, err := h.requireSession(ctx, req)
	if err != nil {
		return nil, err
	}

	if execution.Status != "pending" && execution.Status != "notified" {
		return nil, NewClientError(409, fmt.Sprintf("execution %s cannot be approved (status=%s)", execution.ExecutionID, execution.Status))
	}

	if err := h.authorizeSessionApprove(ctx, session, execution); err != nil {
		return nil, err
	}

	if err := h.purchase.ApproveAndExecute(ctx, execution.ExecutionID, session.Email); err != nil {
		// ApproveAndExecute returns either a transition error (the row
		// drifted out of pending/notified between our check and the UPDATE
		// -- race with cancel/expire) or an execution error (AWS API failed,
		// status is now "failed" on disk). Both surface as 409 to the
		// caller; the History view shows the resulting row state.
		logging.Errorf("purchase[%s]: approvePurchaseViaSession failed after %s: %v",
			execution.ExecutionID, time.Since(t0), err)
		return nil, NewClientError(409, fmt.Sprintf("execution %s could not be approved: %v", execution.ExecutionID, err))
	}

	logging.Infof("purchase[%s]: approvePurchaseViaSession completed in %s (auth=session)",
		execution.ExecutionID, time.Since(t0))
	return map[string]string{"status": "completed"}, nil
}

// authorizeSessionApprove returns nil when the session is permitted to
// approve the given execution under the approve-any / approve-own RBAC
// rules added in issue #286. Returns a 403 ClientError otherwise.
// Mirror of authorizeSessionCancel.
func (h *Handler) authorizeSessionApprove(ctx context.Context, session *Session, execution *config.PurchaseExecution) error {
	// The stateless admin API key has full access and no user row to resolve
	// permissions from. Administrators-group users fall through and pass via
	// the approve-any HasPermissionAPI check below, since {admin, *} matches
	// any requested permission.
	if session.UserID == apiKeyAdminUserID {
		return nil
	}
	if h.auth == nil {
		return NewClientError(500, "authentication service not configured")
	}

	hasAny, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionApproveAny, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if hasAny {
		return nil
	}

	hasOwn, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionApproveOwn, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if !hasOwn {
		return NewClientError(403, "permission denied: requires approve-any or approve-own on purchases")
	}

	if execution.CreatedByUserID == nil || *execution.CreatedByUserID != session.UserID {
		return NewClientError(403, "permission denied: cannot approve another user's pending purchase")
	}
	return nil
}

// authorizeSessionExecuteDirect returns nil when the session is permitted to
// bypass the approval email and execute a purchase immediately under the
// execute-any / execute-own RBAC rules added in issue #289.
// Returns a 403 ClientError otherwise.
//
// creatorID is the creator of the execution being submitted (resolved via
// resolveCreatorUserID before this call; "" on non-human or legacy rows).
//
// Gate logic (mirrors authorizeSessionApprove / authorizeSessionCancel):
//   - stateless admin API key: always permitted (apiKeyAdminUserID sentinel).
//   - execute-any: permitted regardless of creator. Administrators-group users
//     pass here because {admin, *} matches ActionExecuteAny.
//   - execute-own: permitted only when creatorID == session.UserID and both
//     are non-empty (prevents an empty-string collision from granting access).
//   - no matching grant: 403 fail-closed; nil auth component is a 500 as
//     per feedback_fail_closed_middleware.md.
func (h *Handler) authorizeSessionExecuteDirect(ctx context.Context, session *Session, creatorID string) error {
	// Stateless admin API key: full access, no user row. Administrators-group
	// users pass via the execute-any HasPermissionAPI check below, since
	// {admin, *} matches any requested permission.
	if session.UserID == apiKeyAdminUserID {
		return nil
	}
	if h.auth == nil {
		return NewClientError(500, "authentication service not configured")
	}

	hasAny, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionExecuteAny, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if hasAny {
		return nil
	}

	hasOwn, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionExecuteOwn, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if !hasOwn {
		return NewClientError(403, "permission denied: requires execute-any or execute-own on purchases")
	}

	// execute-own: both IDs must be non-empty and must match (empty-string
	// collision would otherwise grant access to any null-creator row).
	if session.UserID == "" || creatorID == "" || creatorID != session.UserID {
		return NewClientError(403, "permission denied: execute-own requires you to be the creator of this purchase")
	}
	return nil
}

func (h *Handler) cancelPurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, execID, token string) (any, error) {
	if err := validateUUID(execID); err != nil {
		return nil, err
	}

	execution, err := h.config.GetExecutionByID(ctx, execID)
	if err != nil {
		return nil, fmt.Errorf("failed to get execution: %w", err)
	}
	if execution == nil {
		return nil, NewClientError(404, "execution not found")
	}

	// Three-mode dispatch:
	//   1. Session present AND RBAC-authorized (admin / cancel-any /
	//      cancel-own match) → session-authed cancel, regardless of
	//      whether a token is in the URL. Restores parity with the
	//      History page Cancel button: a user who can cancel from the
	//      dashboard should also be able to cancel from the email-link
	//      flow, since the same email-link flow already requires a
	//      logged-in session before reaching this endpoint (see
	//      frontend purchases-deeplink.ts). Without this branch, an
	//      admin clicking Cancel from a notification email about an
	//      ambient-credentials execution (CloudAccountID == nil → no
	//      per-account contact_email available) is locked out of an
	//      action they can perform from the dashboard. The token in
	//      the URL is informational at that point — the session has
	//      already authenticated the actor.
	//   2. token != "" → legacy email-link flow without a qualifying
	//      session. authorizeApprovalAction enforces the per-account
	//      contact_email gate from PR #101; the purchase service
	//      validates the token itself before mutating state. This
	//      preserves the security model for forwarded email / shared
	//      inbox / stolen link cases — a non-privileged session falls
	//      through to this branch.
	//   3. token == "" → session-authed dashboard Cancel button (issue
	//      #46). Same path as branch (1) but reached without a URL
	//      token. cancelPurchaseViaSession runs the cancel-any /
	//      cancel-own RBAC matrix and rejects sessions without it.
	if session := h.tryGetSession(ctx, req); session != nil {
		switch err := h.authorizeSessionCancel(ctx, session, execution); {
		case err == nil:
			// Session is RBAC-authorized → run the session-authed cancel.
			return h.cancelPurchaseViaSession(ctx, req, execution)
		case isPermissionDenied(err):
			// Explicit "permission denied" (403) → fall through to the
			// token branch so the contact_email gate still gets a chance
			// (a logged-in user without admin / cancel-* may still be
			// the per-account contact email recipient).
		default:
			// Transient failure (auth-service down, HasPermissionAPI
			// returning a wrapped error, h.auth==nil 500). Propagate
			// instead of silently widening to the contact_email gate —
			// a stale auth backend should not mask itself as a 403 about
			// missing contact emails. CR feedback on PR #216.
			return nil, err
		}
	}

	if token != "" {
		actor, err := h.authorizeApprovalAction(ctx, req, execution)
		if err != nil {
			return nil, err
		}
		if err := h.purchase.CancelExecution(ctx, execID, token, actor); err != nil {
			return nil, err
		}
		return map[string]string{"status": "cancelled"}, nil
	}

	return h.cancelPurchaseViaSession(ctx, req, execution)
}

// cancelPurchaseViaSession is the session-authed branch of cancelPurchase.
// Enforces the cancel-any/cancel-own RBAC matrix, validates the execution
// is in a cancellable state (pending|notified), atomically flips the row
// to "cancelled" AND drops its purchase_suppressions in the same
// transaction, and stamps session.Email onto CancelledBy. The History
// UI's annotateCancelled() helper renders CancelledBy as
// "cancelled by <email>" at read time — see handler_history.go.
//
// The atomic suppression cleanup mirrors purchase.Manager.CancelExecution
// on the email-token path: an executePurchase upfront writes
// purchase_suppressions to hide the just-bought capacity from the
// recommendations list during the grace window, and cancel must drop
// those rows in the same commit so a crash between the two writes can't
// leave the rec list hiding capacity the user already cancelled.
func (h *Handler) cancelPurchaseViaSession(ctx context.Context, req *events.LambdaFunctionURLRequest, execution *config.PurchaseExecution) (any, error) {
	// These endpoints are AuthPublic so the outer middleware skips CSRF.
	// Enforce it here for the session-authed sub-path: the session bearer
	// token is cookie-equivalent and must be CSRF-protected.
	if err := h.validateCSRF(ctx, req); err != nil {
		return nil, NewClientError(403, "CSRF validation failed")
	}

	session, err := h.requireSession(ctx, req)
	if err != nil {
		return nil, err
	}

	if !execution.IsCancelable() {
		return nil, NewClientError(409, fmt.Sprintf("execution %s cannot be cancelled (status=%s)", execution.ExecutionID, execution.Status))
	}

	if err := h.authorizeSessionCancel(ctx, session, execution); err != nil {
		return nil, err
	}

	// Atomically flip status from pending/notified to cancelled + clear
	// suppressions in one tx. CancelExecutionAtomic issues a conditional
	// UPDATE WHERE status IN ('pending','notified'), so a concurrent approve
	// that has already transitioned the row to 'approved' causes zero rows
	// to be affected and we return a 409 with the current status rather
	// than silently overwriting an approved purchase.
	var cancelledBy *string
	if session.Email != "" {
		e := session.Email
		cancelledBy = &e
	}
	var cancelled bool
	var currentStatus string
	if err := h.config.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		cancelled, currentStatus, err = h.config.CancelExecutionAtomic(ctx, tx, execution.ExecutionID, cancelledBy)
		if err != nil {
			return err
		}
		if !cancelled {
			return nil
		}
		return h.config.DeleteSuppressionsByExecutionTx(ctx, tx, execution.ExecutionID)
	}); err != nil {
		return nil, fmt.Errorf("cancel execution %s: %w", execution.ExecutionID, err)
	}
	if !cancelled {
		return nil, NewClientError(409, fmt.Sprintf("execution %s cannot be cancelled: a concurrent operation already transitioned it to %q", execution.ExecutionID, currentStatus))
	}

	return map[string]string{"status": "cancelled"}, nil
}

// authorizeSessionCancel returns nil when the session is permitted to cancel
// the given execution under the cancel-any / cancel-own RBAC rules added in
// issue #46. Returns a 403 ClientError otherwise.
func (h *Handler) authorizeSessionCancel(ctx context.Context, session *Session, execution *config.PurchaseExecution) error {
	// Stateless admin API key: full access, no user row. Administrators-group
	// users pass via the cancel-any HasPermissionAPI check below.
	if session.UserID == apiKeyAdminUserID {
		return nil
	}
	if h.auth == nil {
		return NewClientError(500, "authentication service not configured")
	}

	hasAny, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionCancelAny, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if hasAny {
		return nil
	}

	hasOwn, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionCancelOwn, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if !hasOwn {
		return NewClientError(403, "permission denied: requires cancel-any or cancel-own on purchases")
	}

	if execution.CreatedByUserID == nil || *execution.CreatedByUserID != session.UserID {
		return NewClientError(403, "permission denied: cannot cancel another user's pending purchase")
	}
	return nil
}

// requireSession validates the request's session token and returns the
// session, or a 401 ClientError when no/invalid session is present. Unlike
// requirePermission, it does NOT consult the admin-API-key shortcut — the
// session-authed cancel path needs an actual user UUID for the
// cancel-own check; falling through to the API-key admin role would let
// a key impersonate ownership we cannot verify.
func (h *Handler) requireSession(ctx context.Context, req *events.LambdaFunctionURLRequest) (*Session, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}
	token := h.extractBearerToken(req)
	if token == "" {
		return nil, NewClientError(401, "no authorization token provided")
	}
	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil || session == nil {
		return nil, NewClientError(401, "invalid session")
	}
	return session, nil
}

// retryThreshold is the number of attempts after which a retry is
// soft-blocked and requires `?force=true` to override (issue #47, Q2).
// Five was picked to be generous enough to absorb transient SES /
// recipient-mailbox blips (which usually clear within a couple of
// attempts) while still surfacing genuinely-stuck deployments before
// they accumulate dozens of dead retry rows.
const retryThreshold = 5

// persistentFailureHints maps known-persistent failure substrings to
// the operator-actionable hint surfaced on the History row in place of
// the Retry button (issue #47, Q3). The match is a case-INsensitive
// substring contains check (see resolveOpsHint) — wording variations
// like "ses sandbox" / "SES Sandbox" all hit. Keep needles short and
// distinctive so legitimate transient failures don't accidentally
// substring-match a persistent hint and lock the user out.
//
// Membership criteria: only failures that NO retry can possibly fix —
// they require an operator to change configuration or unblock an
// upstream resource. SES sandbox / unverified domain / missing
// FROM_EMAIL all fit; a transient SES throttle does NOT (the next
// retry might succeed). When in doubt, leave it out — false-negatives
// cost a wasted retry; false-positives lock the user out of an
// actionable button entirely.
var persistentFailureHints = map[string]string{
	"FROM_EMAIL not configured": "Set FROM_EMAIL tfvar then retry",
	"SES sandbox":               "Move SES out of sandbox or verify recipient, then retry",
	"SES domain not verified":   "Verify SES domain in AWS console, then retry",
	"IAM denied":                "Grant the deploy role missing IAM permission, then retry",
}

// resolveOpsHint returns a non-empty hint when the failure reason
// matches a known-persistent pattern, else empty. Match is case-
// insensitive substring contains so backend wording variations don't
// silently slip through ("ses sandbox" / "SES sandbox" / "SES Sandbox"
// all hit).
func resolveOpsHint(failureReason string) string {
	if failureReason == "" {
		return ""
	}
	lower := strings.ToLower(failureReason)
	for needle, hint := range persistentFailureHints {
		if strings.Contains(lower, strings.ToLower(needle)) {
			return hint
		}
	}
	return ""
}

// retryPurchase creates a new execution from a *failed* execution's
// stored Recommendations slice (issue #47). The original failed row
// keeps its `failed` status as a historical record and gains a
// retry_execution_id pointer to the successor; the new row inherits
// retry_attempt_n = predecessor.retry_attempt_n + 1.
//
// Permission gate (mirrors authorizeSessionCancel from #46):
//   - retry-any → may retry any failed row.
//   - retry-own AND failedExec.created_by_user_id == session.user_id
//     → may retry their own failed row.
//   - else → 403.
//
// State gate:
//   - failedExec.Status must be "failed" → 409 otherwise.
//   - failedExec.Error must NOT match the persistent-failure map →
//     409 with ops_hint when it does (Q3).
//   - failedExec.RetryAttemptN < retryThreshold OR ?force=true → soft
//     block at threshold (Q2).
//
// Atomicity:
//
//	A single tx writes (a) the new execution row and (b) the linkage
//	on the original failed row. Suppressions for the new execution are
//	created in the same tx. A crash between any two writes leaves
//	either everything or nothing — no orphaned successor without a
//	predecessor pointer, no dangling pointer to a non-existent row.
func (h *Handler) retryPurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, execID string) (any, error) {
	failedExec, session, err := h.loadAndValidateRetryRequest(ctx, req, execID)
	if err != nil {
		return nil, err
	}

	totalUpfront, totalSavings, err := validateAndTotalRecommendations(failedExec.Recommendations)
	if err != nil {
		return nil, err
	}

	newExecution, err := h.persistRetryExecution(ctx, failedExec, session, totalUpfront, totalSavings)
	if err != nil {
		return nil, err
	}

	// Send the approval email outside the tx — same pattern as
	// executePurchase. Email failures flip the new execution to
	// `failed` so the user sees the reason in History; the linkage on
	// the original row is unaffected (it points at the failed-again
	// successor, which is exactly the audit trail we want).
	emailSent, emailReason, recipient := h.sendPurchaseApprovalEmail(ctx, req, newExecution, failedExec.Recommendations, totalUpfront, totalSavings)
	status := h.finalizePurchaseStatus(ctx, newExecution, emailSent, emailReason)

	resp := map[string]any{
		"execution_id":         newExecution.ExecutionID,
		"original_execution":   failedExec.ExecutionID,
		"status":               status,
		"recommendation_count": len(failedExec.Recommendations),
		"total_upfront_cost":   totalUpfront,
		"estimated_savings":    totalSavings,
		"email_sent":           emailSent,
		"retry_attempt_n":      newExecution.RetryAttemptN,
	}
	if emailReason != "" {
		resp["email_reason"] = emailReason
	}
	if recipient != "" {
		resp["approval_recipient"] = recipient
	}
	return resp, nil
}

// loadAndValidateRetryRequest fetches the failed execution, the
// session, and runs every gate (status, already-retried, RBAC,
// persistent-failure, threshold) before returning a green-light pair.
// Extracted from retryPurchase to keep that function under the
// cyclomatic-complexity ceiling; the gates remain in the order
// documented on retryPurchase so the security boundary is the same.
func (h *Handler) loadAndValidateRetryRequest(ctx context.Context, req *events.LambdaFunctionURLRequest, execID string) (*config.PurchaseExecution, *Session, error) {
	if err := validateUUID(execID); err != nil {
		return nil, nil, err
	}

	failedExec, err := h.config.GetExecutionByID(ctx, execID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get execution: %w", err)
	}
	if failedExec == nil {
		return nil, nil, NewClientError(404, "execution not found")
	}

	session, err := h.requireSession(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	if failedExec.Status != "failed" {
		return nil, nil, NewClientError(409, fmt.Sprintf("execution %s cannot be retried (status=%s)", failedExec.ExecutionID, failedExec.Status))
	}

	// Authorize BEFORE the already-retried guard so an unauthorized
	// caller can't enumerate descendant execution IDs by probing
	// failed-row UUIDs (CR #168 review). The status check above is
	// allowed pre-RBAC because non-admins can already see the row's
	// status via the History endpoint they're entitled to read.
	if err := h.authorizeSessionRetry(ctx, session, failedExec); err != nil {
		return nil, nil, err
	}

	// Already-retried guard: a row with retry_execution_id set was
	// already retried into a successor. Retrying it AGAIN would
	// silently overwrite the linkage pointer and orphan the previous
	// chain, breaking History attribution. Surfaced only after RBAC
	// so the descendant ID isn't a cross-user info leak.
	if failedExec.RetryExecutionID != nil && *failedExec.RetryExecutionID != "" {
		return nil, nil, NewClientErrorWithDetails(409,
			fmt.Sprintf("execution %s was already retried; act on its descendant instead", failedExec.ExecutionID),
			map[string]any{"retry_execution_id": *failedExec.RetryExecutionID})
	}

	if err := checkRetryRateGates(failedExec, req); err != nil {
		return nil, nil, err
	}

	return failedExec, session, nil
}

// checkRetryRateGates runs the persistent-failure (Q3) and
// retry-attempt-threshold (Q2) gates and returns the appropriate
// 409 ClientError when either fires. Extracted from
// loadAndValidateRetryRequest to keep that function under the
// cyclomatic-complexity ceiling without flattening the gate sequence.
func checkRetryRateGates(failedExec *config.PurchaseExecution, req *events.LambdaFunctionURLRequest) error {
	// Persistent-failure block (Q3). Surfaces the ops_hint via the
	// API for stale-cache callers; the History UI already shows it
	// inline in place of the Retry button.
	if hint := resolveOpsHint(failedExec.Error); hint != "" {
		return NewClientErrorWithDetails(409,
			"this failure is operator-fixable; retrying without changing configuration will fail again",
			map[string]any{"ops_hint": hint, "failure_reason": failedExec.Error})
	}

	// Threshold soft-block (Q2). force=true (set by the frontend
	// confirm-with-warning) skips the block but still increments the
	// chain so the next retry is gated by the same threshold against
	// the new attempt count.
	force := strings.EqualFold(req.QueryStringParameters["force"], "true")
	if !force && failedExec.RetryAttemptN >= retryThreshold {
		return NewClientErrorWithDetails(409,
			fmt.Sprintf("execution %s has been retried %d times already; pass ?force=true to override", failedExec.ExecutionID, failedExec.RetryAttemptN),
			map[string]any{"retry_attempt_n": failedExec.RetryAttemptN, "threshold": retryThreshold})
	}

	return nil
}

// persistRetryExecution builds the successor PurchaseExecution and
// writes it + the predecessor linkage + suppressions in a single tx.
// Returns the populated newExecution so the caller can send the
// approval email and synthesize the response. Extracted from
// retryPurchase to keep that function under the cyclomatic-complexity
// ceiling; tx ordering and the slice-aliasing fix live here.
func (h *Handler) persistRetryExecution(ctx context.Context, failedExec *config.PurchaseExecution, session *Session, totalUpfront, totalSavings float64) (*config.PurchaseExecution, error) {
	// Defensive deep copy of the recommendations slice. Sharing the
	// backing array with failedExec.Recommendations would let the
	// downstream purchase pipeline (purchase.Manager.purchaseRecommendations
	// at internal/purchase/execution.go) mutate per-element fields
	// (.Error / .Purchased / .PurchaseID) on the *original failed row's*
	// in-memory representation as well — corrupting the historical
	// record any caller still holding the failedExec pointer would see.
	// The DB rows are isolated (each row JSON-marshals its own copy),
	// but in-process aliasing across a "historical" row and its
	// "successor" is a footgun we want to remove at the source.
	copiedRecs := append([]config.RecommendationRecord(nil), failedExec.Recommendations...)

	// Generate a crypto/rand-backed approval token for the retry execution
	// (issue #408). uuid.New().String() was used here previously, which only
	// provides 122 bits of entropy in a known format; common.GenerateApprovalToken
	// provides 256 bits of uniform randomness, matching every other creation site.
	approvalToken, err := common.GenerateApprovalToken()
	if err != nil {
		return nil, fmt.Errorf("generate approval token for retry: %w", err)
	}
	tokenExpiresAt := time.Now().Add(config.ApprovalTokenTTL)

	newExecutionID := uuid.New().String()
	// PlanID + StepNumber propagate from the predecessor so a retried
	// planned execution stays attributed to its plan + ramp step (CR
	// #168 review). For ad-hoc executions PlanID is "" and StepNumber
	// is 0, so propagation is a no-op for the non-plan case.
	newExecution := &config.PurchaseExecution{
		ExecutionID:            newExecutionID,
		PlanID:                 failedExec.PlanID,
		StepNumber:             failedExec.StepNumber,
		Status:                 "pending",
		ScheduledDate:          time.Now(),
		Recommendations:        copiedRecs,
		TotalUpfrontCost:       totalUpfront,
		EstimatedSavings:       totalSavings,
		ApprovalToken:          approvalToken,
		ApprovalTokenExpiresAt: &tokenExpiresAt,
		Source:                 common.PurchaseSourceWeb,
		CapacityPercent:        failedExec.CapacityPercent,
		CreatedByUserID:        resolveCreatorUserID(session),
		RetryAttemptN:          failedExec.RetryAttemptN + 1,
	}

	// Stamp the original failed row with the linkage. This is a
	// separate value copy so the caller's pointer to failedExec doesn't
	// accidentally pick up other status-mutating concerns: only
	// retry_execution_id changes here, the status stays `failed`.
	originalUpdated := *failedExec
	originalUpdated.RetryExecutionID = &newExecutionID

	var gracePeriodCfg *config.GlobalConfig
	if g, err := h.config.GetGlobalConfig(ctx); err == nil {
		gracePeriodCfg = g
	}
	suppressions := buildSuppressions(failedExec.Recommendations, newExecutionID, gracePeriodCfg, time.Now())

	// Three writes in one tx:
	//  1. INSERT the new execution row (the successor).
	//  2. UPSERT the original failed row to set retry_execution_id.
	//  3. INSERT suppression rows for the new execution.
	// Order matters: the FK on retry_execution_id requires the
	// successor to exist before the original can point at it.
	if err := h.config.WithTx(ctx, func(tx pgx.Tx) error {
		if err := h.config.SavePurchaseExecutionTx(ctx, tx, newExecution); err != nil {
			return err
		}
		if err := h.config.SavePurchaseExecutionTx(ctx, tx, &originalUpdated); err != nil {
			return err
		}
		for i := range suppressions {
			if err := h.config.CreateSuppressionTx(ctx, tx, &suppressions[i]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to save retry execution: %w", err)
	}

	return newExecution, nil
}

// authorizeSessionRetry is the retry-side mirror of
// authorizeSessionCancel. Returns nil when the session is permitted to
// retry the given failed execution under the retry-any / retry-own
// rules (issue #47); 403 ClientError otherwise. Admins short-circuit;
// non-admins must hold retry-any (allowed for any execution) or
// retry-own (allowed only when the execution's CreatedByUserID matches
// the session UserID — legacy NULL-creator rows are out of reach for
// non-admins, same as the cancel path).
func (h *Handler) authorizeSessionRetry(ctx context.Context, session *Session, execution *config.PurchaseExecution) error {
	// Stateless admin API key: full access, no user row. Administrators-group
	// users pass via the retry-any HasPermissionAPI check below.
	if session.UserID == apiKeyAdminUserID {
		return nil
	}
	if h.auth == nil {
		return NewClientError(500, "authentication service not configured")
	}

	hasAny, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionRetryAny, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if hasAny {
		return nil
	}

	hasOwn, err := h.auth.HasPermissionAPI(ctx, session.UserID, auth.ActionRetryOwn, auth.ResourcePurchases)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if !hasOwn {
		return NewClientError(403, "permission denied: requires retry-any or retry-own on purchases")
	}

	if execution.CreatedByUserID == nil || *execution.CreatedByUserID != session.UserID {
		return NewClientError(403, "permission denied: cannot retry another user's failed purchase")
	}
	return nil
}

// tryResolveActorEmail returns the email of the session-authenticated user
// who made the request, or "" when the request carries no valid session.
// Best-effort: the approve/cancel routes are AuthPublic (token-only), so
// we don't require a session; we just capture it when present so the
// auth-gated deep-link flow (frontend /purchases/{action}/:id → login
// → session-authed call with ?token=…) can record per-user attribution
// without changing the token-only fallback path used by message workers.
func (h *Handler) tryResolveActorEmail(ctx context.Context, req *events.LambdaFunctionURLRequest) string {
	if s := h.tryGetSession(ctx, req); s != nil {
		return s.Email
	}
	return ""
}

// isPermissionDenied reports whether err is *directly* a 403 ClientError
// (not merely something that wraps one). Used by the cancel-from-email
// session pre-check to distinguish a legitimate "your session lacks
// cancel-* permission" answer (fall through to the token branch's
// contact_email gate) from a transient auth-service failure (propagate so
// the caller sees the real cause). CR feedback on PR #216.
//
// Strict (un-wrapped) type assertion is deliberate: if a future caller
// wraps a 403 to add context (fmt.Errorf("permission check failed: %w",
// ...)), that wrapped error represents the *outer* failure mode (the
// wrapper's intent), not "this is still a 403". errors.As-style unwrapping
// would erase that distinction and silently route wrapped backend
// failures into the contact_email gate — exactly the misclassification
// the propagate-vs-fall-through split is meant to prevent.
func isPermissionDenied(err error) bool {
	ce, ok := err.(*clientError)
	return ok && ce.code == 403
}

// tryGetSession returns the validated session for the request, or nil when
// the request carries no Bearer token, the auth service isn't configured,
// or session validation fails. Mirrors tryResolveActorEmail's silent
// best-effort semantics so AuthPublic callers can opt into session-aware
// behaviour without forcing a 401 on tokenless flows.
func (h *Handler) tryGetSession(ctx context.Context, req *events.LambdaFunctionURLRequest) *Session {
	if req == nil || h.auth == nil {
		return nil
	}
	bearer := h.extractBearerToken(req)
	if bearer == "" {
		return nil
	}
	session, err := h.auth.ValidateSession(ctx, bearer)
	if err != nil || session == nil {
		return nil
	}
	return session
}

// buildPurchaseDetailsResponse builds the response map for a purchase execution.
func buildPurchaseDetailsResponse(execution *config.PurchaseExecution, planName string) map[string]any {
	response := map[string]any{
		"execution_id":       execution.ExecutionID,
		"plan_id":            execution.PlanID,
		"plan_name":          planName,
		"status":             execution.Status,
		"step_number":        execution.StepNumber,
		"scheduled_date":     execution.ScheduledDate.Format("2006-01-02"),
		"total_upfront_cost": execution.TotalUpfrontCost,
		"estimated_savings":  execution.EstimatedSavings,
		"recommendations":    execution.Recommendations,
	}

	if execution.NotificationSent != nil {
		response["notification_sent"] = execution.NotificationSent.Format("2006-01-02T15:04:05Z")
	}
	if execution.CompletedAt != nil {
		response["completed_at"] = execution.CompletedAt.Format("2006-01-02T15:04:05Z")
	}
	if execution.Error != "" {
		response["error"] = execution.Error
	}
	return response
}

// getPurchaseDetails returns details about a specific purchase execution
func (h *Handler) getPurchaseDetails(ctx context.Context, req *events.LambdaFunctionURLRequest, executionID string) (any, error) {
	if err := validateUUID(executionID); err != nil {
		return nil, err
	}

	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	execution, err := h.config.GetExecutionByID(ctx, executionID)
	if err != nil {
		return nil, fmt.Errorf("execution not found: %w", err)
	}
	if execution == nil {
		return nil, fmt.Errorf("execution not found: %s", executionID)
	}

	// Scope: reject if the execution's plan isn't accessible to the session.
	if err := h.requirePlanAccess(ctx, session, execution.PlanID); err != nil {
		return nil, err
	}

	var planName string
	if execution.PlanID != "" {
		plan, err := h.config.GetPurchasePlan(ctx, execution.PlanID)
		if err == nil && plan != nil {
			planName = plan.Name
		}
	}

	return buildPurchaseDetailsResponse(execution, planName), nil
}

// ExecutePurchaseRequest represents the request to execute purchases
type ExecutePurchaseRequest struct {
	Recommendations []config.RecommendationRecord `json:"recommendations"`
	// CapacityPercent is what fraction (1..100) of the originally-
	// recommended counts the user chose in the bulk Purchase flow.
	// Audit-only: the Recommendations slice already carries scaled
	// counts, so backend math ignores this field for purchase work.
	// 0 / absent defaults to 100 ("full capacity").
	CapacityPercent int `json:"capacity_percent,omitempty"`
	// ExecuteMode controls whether this request bypasses the approval
	// email and executes the purchase immediately. The only accepted
	// non-empty value is "direct"; any other value is treated as the
	// default approval-required flow. The handler re-checks the
	// execute-any/execute-own RBAC gate before honouring "direct",
	// even if the session already passed the execute:purchases gate in
	// validateExecutePurchaseRequest, so a client that sets this field
	// without the privilege receives a 403 rather than silent fallback.
	ExecuteMode string `json:"execute_mode,omitempty"`
}

// validateExecutePurchaseRequest handles the permission check, body parse,
// and recommendation-list bounds + scope checks. Extracted so executePurchase
// itself stays linear and under the gocyclo threshold.
func (h *Handler) validateExecutePurchaseRequest(ctx context.Context, req *events.LambdaFunctionURLRequest) (ExecutePurchaseRequest, *Session, error) {
	session, err := h.requirePermission(ctx, req, "execute", "purchases")
	if err != nil {
		return ExecutePurchaseRequest{}, nil, err
	}
	var execReq ExecutePurchaseRequest
	if err := json.Unmarshal([]byte(req.Body), &execReq); err != nil {
		return ExecutePurchaseRequest{}, nil, NewClientError(400, "invalid request body")
	}
	const maxRecommendations = 1000
	if len(execReq.Recommendations) == 0 {
		return ExecutePurchaseRequest{}, nil, NewClientError(400, "no recommendations provided")
	}
	if len(execReq.Recommendations) > maxRecommendations {
		return ExecutePurchaseRequest{}, nil, NewClientError(400, fmt.Sprintf("too many recommendations: %d (max %d)", len(execReq.Recommendations), maxRecommendations))
	}
	if err := normalizeCapacityPercent(&execReq); err != nil {
		return ExecutePurchaseRequest{}, nil, err
	}
	// Scope: reject the whole request if any recommendation targets an
	// account outside the session's allowed_accounts. Safer than silently
	// dropping the out-of-scope ones — the user explicitly chose those
	// recommendations; a partial execution would misrepresent intent.
	// Runs before per-rec content validation so an out-of-scope request is
	// rejected as 403 regardless of the rec's Term/Payment/Count contents.
	if err := h.validatePurchaseRecommendationScope(ctx, session, execReq.Recommendations); err != nil {
		return ExecutePurchaseRequest{}, nil, err
	}
	// Per-rec Provider/Service/Term/Payment/Count validation at the API
	// boundary so a malformed client-supplied rec (e.g. Term:7, Payment:"foo",
	// negative Count) is rejected here rather than reaching the cloud SDK at
	// execute time (#643). This is scoped to the web execute path only — the
	// retry path replays recs from an already-validated execution and must
	// not be re-gated by the same rules.
	if err := validateExecutePurchaseRecommendations(execReq.Recommendations); err != nil {
		return ExecutePurchaseRequest{}, nil, err
	}
	// Cross-check the audit-only capacity_percent against the scaled rec
	// counts so the persisted execution can't claim a capacity that
	// disagrees with what was actually purchased (#647). Skipped per-rec
	// when the rec carries no recommended_count.
	if err := validateCapacityConsistency(execReq.Recommendations, execReq.CapacityPercent); err != nil {
		return ExecutePurchaseRequest{}, nil, err
	}
	return execReq, session, nil
}

// normalizeCapacityPercent defaults an absent/zero capacity_percent to 100
// and rejects anything outside [1, 100]. capacity_percent is audit-only but
// still bounded: a value outside the range is a client bug worth surfacing
// rather than silently clamping. Extracted so validateExecutePurchaseRequest
// stays under the gocyclo threshold.
func normalizeCapacityPercent(execReq *ExecutePurchaseRequest) error {
	if execReq.CapacityPercent == 0 {
		execReq.CapacityPercent = 100
	}
	if execReq.CapacityPercent < 1 || execReq.CapacityPercent > 100 {
		return NewClientError(400, fmt.Sprintf("capacity_percent must be between 1 and 100, got %d", execReq.CapacityPercent))
	}
	return nil
}

// validateExecutePurchaseRecommendations runs the per-rec #643 boundary
// validation over every rec in a web execute request, returning the first
// failure. Extracted so validateExecutePurchaseRequest stays under the
// gocyclo threshold.
func validateExecutePurchaseRecommendations(recs []config.RecommendationRecord) error {
	for i := range recs {
		if err := validatePurchaseRecommendation(&recs[i], i); err != nil {
			return err
		}
	}
	return nil
}

// finalizePurchaseStatus flips an execution's stored status to "failed" if
// the approval email couldn't send, and returns the status string the API
// response should carry. Returns the original "pending" when email_sent is
// true or when the failed-state write itself fails (best-effort — the UI
// still gets email_sent=false and can point the user at History).
func (h *Handler) finalizePurchaseStatus(ctx context.Context, execution *config.PurchaseExecution, emailSent bool, emailReason string) string {
	if emailSent {
		return "pending"
	}
	execution.Status = "failed"
	execution.Error = emailReason
	if err := h.config.SavePurchaseExecution(ctx, execution); err != nil {
		logging.Errorf("failed to mark execution %s as failed after email send error: %v", execution.ExecutionID, err)
		return "pending"
	}
	return "failed"
}

// validateAndTotalRecommendations validates each recommendation and returns totals.
func validateAndTotalRecommendations(recs []config.RecommendationRecord) (upfront, savings float64, err error) {
	const maxAmount = 10_000_000 // $10M sanity cap
	for i, rec := range recs {
		if rec.UpfrontCost < 0 {
			return 0, 0, NewClientError(400, fmt.Sprintf("recommendation %d has negative upfront cost: %.2f", i, rec.UpfrontCost))
		}
		if rec.Savings < 0 {
			return 0, 0, NewClientError(400, fmt.Sprintf("recommendation %d has negative savings: %.2f", i, rec.Savings))
		}
		upfront += rec.UpfrontCost
		savings += rec.Savings
	}
	if upfront > maxAmount {
		return 0, 0, NewClientError(400, fmt.Sprintf("total upfront cost %.2f exceeds maximum allowed (%.2f)", upfront, float64(maxAmount)))
	}
	if savings > maxAmount {
		return 0, 0, NewClientError(400, fmt.Sprintf("total estimated savings %.2f exceeds maximum allowed (%.2f)", savings, float64(maxAmount)))
	}
	return upfront, savings, nil
}

// derefStringOrEmpty returns the string a pointer points to, or "" when nil.
// Used to convert *string creator IDs to the bare string needed for hashing
// without adding a branch to the caller.
func derefStringOrEmpty(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

// resolveCreatorUserID returns a pointer to the session user's UUID for
// stamping onto purchase_executions.created_by_user_id, or nil for
// non-user sessions whose UserID isn't a real UUID. This keeps the
// cancel-own RBAC check (issue #46) honest:
//   - Real human session → pointer to UUID; future cancel-own match works.
//   - Admin-API-key session → UserID is the literal "admin-api-key"
//     sentinel, which fails validateUUID; store NULL so the FK to users
//     stays valid and the row is reachable only via cancel-any/email-token.
//   - nil session (defensive — current callers gate on requirePermission
//     so this shouldn't happen) → NULL.
func resolveCreatorUserID(session *Session) *string {
	if session == nil {
		return nil
	}
	if validateUUID(session.UserID) != nil {
		return nil
	}
	uid := session.UserID
	return &uid
}

// executePurchase handles direct purchase execution from recommendations
// matchDuplicateInList scans a slice of pending executions for one that
// matches creatorID + idempotencyKey within the idempotency window.
// Returns the first match, or nil when there is no duplicate.
// Extracted from persistExecutionAndSuppressions to keep cyclomatic
// complexity under the project gocyclo threshold.
func matchDuplicateInList(pending []config.PurchaseExecution, creatorID, idempotencyKey string, now time.Time) *config.PurchaseExecution {
	cutoff := now.Add(-purchaseIdempotencyWindow)
	for i := range pending {
		ex := &pending[i]
		if ex.Source != common.PurchaseSourceWeb || ex.ScheduledDate.Before(cutoff) {
			continue
		}
		exCreator := ""
		if ex.CreatedByUserID != nil {
			exCreator = *ex.CreatedByUserID
		}
		if exCreator == creatorID && purchaseIdempotencyKey(exCreator, ex.Recommendations, ex.CapacityPercent) == idempotencyKey {
			return ex
		}
	}
	return nil
}

// persistExecutionAndSuppressions saves the execution + its suppression
// records in a single transaction. It also performs the duplicate-execution
// check inside the same transaction (using SELECT ... FOR UPDATE) so that the
// read and the insert are atomic — closing the TOCTOU race that the pre-tx
// duplicatePurchaseResponse call could not prevent (#643).
//
// Return values:
//   - (nil, nil)          — no duplicate found; execution was inserted.
//   - (existing, nil)     — duplicate found; execution was NOT inserted; caller
//     should collapse onto existing.
//   - (nil, err)          — store error; caller should surface it.
func (h *Handler) persistExecutionAndSuppressions(
	ctx context.Context,
	execution *config.PurchaseExecution,
	suppressions []config.PurchaseSuppression,
	creatorID, idempotencyKey string,
) (dup *config.PurchaseExecution, err error) {
	txErr := h.config.WithTx(ctx, func(tx pgx.Tx) error {
		// Duplicate check inside the tx (SELECT FOR UPDATE) — atomic with
		// the insert below.
		pending, err := h.config.GetPendingExecutionsTx(ctx, tx)
		if err != nil {
			return err
		}
		if dup = matchDuplicateInList(pending, creatorID, idempotencyKey, time.Now()); dup != nil {
			return nil // found duplicate — skip insert, commit tx (read-only)
		}

		if err := h.config.SavePurchaseExecutionTx(ctx, tx, execution); err != nil {
			return err
		}
		for i := range suppressions {
			if err := h.config.CreateSuppressionTx(ctx, tx, &suppressions[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if txErr != nil {
		return nil, fmt.Errorf("failed to save execution: %w", txErr)
	}
	return dup, nil
}

// purchaseIdempotencyWindow is how long a freshly-created pending execution
// "absorbs" an identical resubmission. A double-click or a retried network
// call within this window resolves to the original execution instead of
// minting a second approvable row (double-spend). Sized to comfortably cover
// a stuck request retry without masking a genuine intentional re-purchase.
const purchaseIdempotencyWindow = 2 * time.Minute

// purchaseIdempotencyKey derives a stable fingerprint of a submit so two
// identical submissions (same actor, same scaled rec set, same capacity)
// collapse to one execution. The recs are normalized and sorted so map/slice
// ordering can't change the hash. Account scope is implicit: each rec carries
// its CloudAccountID, so the same recs targeting a different account hash
// differently. Closes issue #644.
func purchaseIdempotencyKey(creatorID string, recs []config.RecommendationRecord, capacityPercent int) string {
	tuples := make([]string, 0, len(recs))
	for _, r := range recs {
		acct := ""
		if r.CloudAccountID != nil {
			acct = *r.CloudAccountID
		}
		tuples = append(tuples, fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d|%d|%s",
			strings.ToLower(strings.TrimSpace(r.Provider)),
			r.Service, r.Region, r.ResourceType, r.Engine, acct,
			r.Count, r.Term, strings.ToLower(strings.TrimSpace(r.Payment))))
	}
	sort.Strings(tuples)
	h := sha256.New()
	fmt.Fprintf(h, "%s\x1f%d\x1f%s", creatorID, capacityPercent, strings.Join(tuples, "\x1e"))
	return hex.EncodeToString(h.Sum(nil))
}

// findDuplicatePendingExecution returns an existing pending/notified execution
// whose idempotency fingerprint matches key and whose creation falls inside
// purchaseIdempotencyWindow of now, or (nil, nil) when there is no duplicate.
// It recomputes each candidate's key from its persisted recs + creator +
// capacity rather than relying on a stored column, so no schema change is
// needed. Restricted to web-sourced executions to avoid colliding with
// scheduler/CLI rows. A lookup error is non-fatal to the caller's decision
// (returned so the caller can log and proceed with a fresh execution).
func (h *Handler) findDuplicatePendingExecution(ctx context.Context, creatorID, key string, now time.Time) (*config.PurchaseExecution, error) {
	pending, err := h.config.GetPendingExecutions(ctx)
	if err != nil {
		return nil, err
	}
	cutoff := now.Add(-purchaseIdempotencyWindow)
	for i := range pending {
		ex := &pending[i]
		if ex.Source != common.PurchaseSourceWeb {
			continue
		}
		if ex.ScheduledDate.Before(cutoff) {
			continue
		}
		exCreator := ""
		if ex.CreatedByUserID != nil {
			exCreator = *ex.CreatedByUserID
		}
		if exCreator != creatorID {
			continue
		}
		if purchaseIdempotencyKey(exCreator, ex.Recommendations, ex.CapacityPercent) == key {
			return ex, nil
		}
	}
	return nil, nil
}

// duplicatePurchaseResponse returns a ready-to-send response body when this
// submit collapses onto an existing pending execution (#644), or nil when it
// is a genuinely new submit that should proceed to create a fresh execution.
// A lookup failure is logged and treated as "not a duplicate" so a transient
// store error never blocks a legitimate purchase. Extracted from executePurchase
// to keep that function under the gocyclo threshold.
func (h *Handler) duplicatePurchaseResponse(ctx context.Context, creator *string, recs []config.RecommendationRecord, capacityPercent int) map[string]any {
	creatorID := ""
	if creator != nil {
		creatorID = *creator
	}
	key := purchaseIdempotencyKey(creatorID, recs, capacityPercent)
	dup, err := h.findDuplicatePendingExecution(ctx, creatorID, key, time.Now())
	if err != nil {
		logging.Errorf("idempotency lookup failed, proceeding with new execution: %v", err)
		return nil
	}
	if dup == nil {
		return nil
	}
	logging.Infof("duplicate purchase submit collapsed to existing execution %s", dup.ExecutionID)
	return buildDuplicatePurchaseResponse(dup)
}

// buildDuplicatePurchaseResponse returns the executePurchase response body for
// a submit that collapsed onto an existing pending execution (#644). It points
// at the original row so the client lands on the same approvable execution
// instead of a freshly-minted duplicate. duplicate=true lets the UI explain
// why no new approval email was sent.
func buildDuplicatePurchaseResponse(ex *config.PurchaseExecution) map[string]any {
	return map[string]any{
		"execution_id":         ex.ExecutionID,
		"status":               ex.Status,
		"recommendation_count": len(ex.Recommendations),
		"total_upfront_cost":   ex.TotalUpfrontCost,
		"estimated_savings":    ex.EstimatedSavings,
		"email_sent":           ex.NotificationSent != nil,
		"duplicate":            true,
		"message":              "Duplicate submission collapsed onto the existing pending execution; no new approval request was created.",
	}
}

// newPendingExecution builds a fresh pending PurchaseExecution with a
// crypto/rand-backed approval token. Extracted from executePurchase to keep
// that function under the gocyclo threshold.
func newPendingExecution(req *ExecutePurchaseRequest, totalUpfront, totalSavings float64) (*config.PurchaseExecution, error) {
	approvalToken, err := common.GenerateApprovalToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate approval token: %w", err)
	}
	tokenExpiresAt := time.Now().Add(config.ApprovalTokenTTL)
	return &config.PurchaseExecution{
		ExecutionID:            uuid.New().String(),
		Status:                 "pending",
		ScheduledDate:          time.Now(),
		Recommendations:        req.Recommendations,
		TotalUpfrontCost:       totalUpfront,
		EstimatedSavings:       totalSavings,
		ApprovalToken:          approvalToken,
		ApprovalTokenExpiresAt: &tokenExpiresAt,
		Source:                 common.PurchaseSourceWeb,
		CapacityPercent:        req.CapacityPercent,
	}, nil
}

func (h *Handler) executePurchase(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	execReq, session, err := h.validateExecutePurchaseRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	totalUpfront, totalSavings, err := validateAndTotalRecommendations(execReq.Recommendations)
	if err != nil {
		return nil, err
	}

	// Submit-time idempotency (#644/#643): a double-click or retried POST with
	// an identical actor + scaled rec set + capacity within a short window must
	// resolve to the original pending execution rather than minting a second
	// approvable row (double-spend). The atomic guard lives inside
	// persistExecutionAndSuppressions (SELECT FOR UPDATE + INSERT in one tx)
	// which closes the TOCTOU race that a pre-tx read alone cannot prevent.
	creator := resolveCreatorUserID(session)
	creatorID := derefStringOrEmpty(creator)
	idempotencyKey := purchaseIdempotencyKey(creatorID, execReq.Recommendations, execReq.CapacityPercent)

	execution, err := newPendingExecution(&execReq, totalUpfront, totalSavings)
	if err != nil {
		return nil, err
	}
	// CreatedByUserID is set after construction so newPendingExecution stays
	// signature-stable. The cancel-own RBAC path in cancelPurchase relies on
	// this stamp to identify the creator on later cancellation; legacy rows
	// pre-dating issue #46 can carry NULL here, so always go through the
	// resolveCreatorUserID helper rather than dereferencing the session.
	execution.CreatedByUserID = creator
	executionID := execution.ExecutionID

	// Load the grace-period config once before entering the tx so a
	// GetGlobalConfig failure fails the whole request cleanly rather
	// than leaving a half-committed tx state. Errors here don't block
	// the purchase — we just default the grace period per-provider.
	var gracePeriodCfg *config.GlobalConfig
	if g, err := h.config.GetGlobalConfig(ctx); err == nil {
		gracePeriodCfg = g
	}

	suppressions := buildSuppressions(execReq.Recommendations, executionID, gracePeriodCfg, time.Now())
	dupExec, err := h.persistExecutionAndSuppressions(ctx, execution, suppressions, creatorID, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if dupExec != nil {
		logging.Infof("concurrent duplicate purchase submit collapsed to existing execution %s", dupExec.ExecutionID)
		return buildDuplicatePurchaseResponse(dupExec), nil
	}

	// Direct-execute path (issue #289): a session with execute-any or
	// execute-own on purchases can request execute_mode="direct" to bypass
	// the approval email and commit the purchase immediately.
	//
	// authorizeSessionExecuteDirect is a hard gate — any check failure
	// returns a 403 rather than falling through to the email path. This
	// prevents a client that sets execute_mode="direct" but only holds the
	// base execute:purchases verb from silently degrading to the email flow.
	if execReq.ExecuteMode == "direct" {
		if err := h.authorizeSessionExecuteDirect(ctx, session, creatorID); err != nil {
			return nil, err
		}
		return h.directExecutePurchase(ctx, execution, session)
	}

	// Send approval email synchronously so the response can surface the
	// actual outcome. The DB write above is the source of truth — email is
	// best-effort and never blocks the response body; the returned
	// email_sent / email_reason fields let the UI tell the user whether they
	// should wait for an inbox or cancel/retry manually.
	emailSent, emailReason, recipient := h.sendPurchaseApprovalEmail(ctx, req, execution, execReq.Recommendations, totalUpfront, totalSavings)
	status := h.finalizePurchaseStatus(ctx, execution, emailSent, emailReason)

	return buildApprovalPendingResponse(executionID, status, len(execReq.Recommendations), totalUpfront, totalSavings, emailSent, emailReason, recipient), nil
}

// buildApprovalPendingResponse assembles the JSON-serialisable response body
// for the approval-pending path of executePurchase. Extracted to keep
// executePurchase cyclomatic complexity within the project limit.
func buildApprovalPendingResponse(
	executionID string,
	status string,
	recCount int,
	totalUpfront, totalSavings float64,
	emailSent bool,
	emailReason, recipient string,
) map[string]any {
	message := "Purchase execution created and pending approval"
	if !emailSent {
		message = "Purchase execution created but approval email could not be sent - see email_reason"
	}
	resp := map[string]any{
		"execution_id":         executionID,
		"status":               status,
		"recommendation_count": recCount,
		"total_upfront_cost":   totalUpfront,
		"estimated_savings":    totalSavings,
		"email_sent":           emailSent,
		"message":              message,
	}
	if emailReason != "" {
		resp["email_reason"] = emailReason
	}
	if recipient != "" {
		resp["approval_recipient"] = recipient
	}
	return resp
}

// directExecutePurchase is the direct-execute branch of executePurchase
// (issue #289). It is called after the execution row has been persisted as
// "pending" and after authorizeSessionExecuteDirect has confirmed the session
// holds execute-any or execute-own on purchases.
//
// Steps:
//  1. Stamp the three audit fields (executed_by_user_id, executed_at,
//     pre_approval_skip_reason) onto the in-memory execution so
//     SavePurchaseExecution persists them in the next call inside
//     ApproveAndExecute.
//  2. Delegate to purchase.Manager.ApproveAndExecute, which atomically
//     transitions the row to "approved" and then runs the purchase
//     synchronously. ApproveAndExecute already stamps ApprovedBy; we pass
//     the session email as the actor so the approved_by column also records
//     who direct-executed.
//  3. Return a "completed" status to the caller.
//
// The audit fields are best-effort if ApproveAndExecute's SavePurchaseExecution
// races with our pre-call stamp -- but in practice ApproveAndExecute calls
// SavePurchaseExecution once after a successful TransitionExecutionStatus, at
// which point our pre-stamp is already on the row that was loaded by
// TransitionExecutionStatus. The critical audit invariant is that a non-nil
// executed_by_user_id always co-occurs with a non-nil pre_approval_skip_reason,
// and both are set atomically in the same SavePurchaseExecution call here.
func (h *Handler) directExecutePurchase(ctx context.Context, execution *config.PurchaseExecution, session *Session) (any, error) {
	t0 := time.Now()
	executionID := execution.ExecutionID
	logging.Infof("purchase[%s]: directExecutePurchase entry (auth=session)", executionID)

	// Stamp audit fields before the status transition so they are
	// present on the row the reaper / history query reads.
	if session.UserID != "" {
		uid := session.UserID
		execution.ExecutedByUserID = &uid
	}
	now := time.Now()
	execution.ExecutedAt = &now
	skipReason := "direct-execute permission"
	execution.PreApprovalSkipReason = &skipReason
	if err := h.config.SavePurchaseExecution(ctx, execution); err != nil {
		// Audit-gap: stamp failed but don't block the purchase. Log at
		// error level so a CloudWatch alarm can catch persistent failures.
		logging.Errorf("AUDIT GAP: failed to stamp direct-execute audit fields on %s: %v", executionID, err)
	}

	if err := h.purchase.ApproveAndExecute(ctx, executionID, session.Email); err != nil {
		logging.Errorf("purchase[%s]: directExecutePurchase failed after %s: %v",
			executionID, time.Since(t0), err)
		return nil, NewClientError(409, fmt.Sprintf("execution %s could not be direct-executed: %v", executionID, err))
	}

	logging.Infof("purchase[%s]: directExecutePurchase completed in %s", executionID, time.Since(t0))
	return map[string]any{
		"execution_id":         executionID,
		"status":               "completed",
		"recommendation_count": len(execution.Recommendations),
		"total_upfront_cost":   execution.TotalUpfrontCost,
		"estimated_savings":    execution.EstimatedSavings,
		"direct_execute":       true,
		"message":              "Purchase executed immediately (direct-execute permission).",
	}, nil
}

// archeraEducationURL returns dashboardBase + "/archera-insurance", or "" when
// dashboardBase is empty so email templates omit the block safely.
func archeraEducationURL(dashboardBase string) string {
	if dashboardBase == "" {
		return ""
	}
	return dashboardBase + "/archera-insurance"
}

// approvalResponseRecipient returns the email address to surface in the
// approval_recipient API response field (and therefore in the post-submit toast).
// It returns globalNotify when set (after trimming whitespace), matching the
// address the History handler shows for pending rows (resolvePendingApproverEmail
// also returns globalNotify first). Falls back to to (the per-account
// contact_email) when globalNotify is empty or whitespace-only.
// Extracted to keep sendPurchaseApprovalEmail under the cyclomatic-complexity ceiling.
func approvalResponseRecipient(globalNotify, to string) string {
	if trimmed := strings.TrimSpace(globalNotify); trimmed != "" {
		return trimmed
	}
	return to
}

// sendPurchaseApprovalEmail sends an approval-request email for a newly created
// execution and returns a structured outcome:
//   - (true, "", recipient) on successful send
//   - (false, "<reason>", "") on any preflight gate or send error
//   - (false, "<reason>", recipient) when send failed AFTER recipient resolution
//     (so the response can still surface who would have been notified)
//
// `recipient` is the address surfaced in the post-submit toast. It is the
// Admin notification email (Settings -> General) when configured, matching the
// value the History UI shows for pending rows via resolvePendingApproverEmail.
// When no notification email is set, it falls back to the per-account
// contact_email (the actual To address). This fixes issue #735 where the toast
// named the per-account contact_email instead of the Admin notification email,
// creating a discrepancy with the History "awaiting approval from X" display.
//
// Errors are also logged at Errorf level so they show up in CloudWatch, but
// the reason string is what the API response surfaces to the UI.
func (h *Handler) sendPurchaseApprovalEmail(ctx context.Context, req *events.LambdaFunctionURLRequest, execution *config.PurchaseExecution, recs []config.RecommendationRecord, totalUpfront, totalSavings float64) (bool, string, string) {
	if h.emailNotifier == nil {
		return false, "email notifier not configured for this deployment", ""
	}
	globalCfg, err := h.config.GetGlobalConfig(ctx)
	if err != nil {
		logging.Errorf("Failed to load global config for approval email: %v", err)
		return false, fmt.Sprintf("failed to load settings: %v", err), ""
	}
	globalNotify := ""
	if globalCfg.NotificationEmail != nil {
		globalNotify = *globalCfg.NotificationEmail
	}
	to, cc, approvers, err := h.resolveApprovalRecipients(ctx, recs, globalNotify)
	if err != nil {
		logging.Errorf("Failed to resolve approval recipients: %v", err)
		return false, fmt.Sprintf("failed to resolve recipients: %v", err), ""
	}
	if to == "" {
		return false, "no notification email set in Settings → General and no account contact emails configured", ""
	}
	// responseRecipient is the email address surfaced in the UI toast (approval_recipient
	// API field). It matches what the History handler shows for pending rows via
	// resolvePendingApproverEmail, which always returns globalNotify when set. Using
	// globalNotify here keeps both displays consistent. When globalNotify is empty,
	// fall back to to (the per-account contact_email). See issue #735.
	responseRecipient := approvalResponseRecipient(globalNotify, to)
	summaries := make([]email.RecommendationSummary, 0, len(recs))
	for _, rec := range recs {
		summaries = append(summaries, email.RecommendationSummary{
			Service:        rec.Service,
			ResourceType:   rec.ResourceType,
			Engine:         rec.Engine,
			Region:         rec.Region,
			Count:          rec.Count,
			MonthlySavings: rec.Savings,
		})
	}
	dashboardBase := h.resolveDashboardURL(req)
	data := email.NotificationData{
		DashboardURL:        dashboardBase,
		ApprovalToken:       execution.ApprovalToken,
		ExecutionID:         execution.ExecutionID,
		TotalSavings:        totalSavings,
		TotalUpfrontCost:    totalUpfront,
		Recommendations:     summaries,
		RecipientEmail:      to,
		CCEmails:            cc,
		AuthorizedApprovers: approvers,
	}
	data.ArcheraEducationURL = archeraEducationURL(dashboardBase)
	if err := h.emailNotifier.SendPurchaseApprovalRequest(ctx, data); err != nil {
		logging.Errorf("Failed to send purchase approval email: %v", err)
		switch {
		case errors.Is(err, email.ErrNoRecipient):
			return false, "no notification email set in Settings → General", ""
		case errors.Is(err, email.ErrNoFromEmail):
			return false, "FROM_EMAIL not configured for this deployment", responseRecipient
		default:
			return false, fmt.Sprintf("send failed: %v", err), responseRecipient
		}
	}
	return true, "", responseRecipient
}

// resolveDashboardURL returns the absolute base URL to embed in email
// approval/cancel links. Preference order matches the OIDC issuer helper's
// strategy for the same underlying problem (Lambda's Function URL can't be
// wired into its own env via Terraform without a cycle, so the canonical
// URL has to be discovered at request time):
//
//  1. h.dashboardURL — set from CUDLY_DASHBOARD_URL when the operator has
//     a custom domain (frontend_domain_names populated in Terraform).
//  2. The inbound request's trusted DomainName (`https://<lambda-url-host>`)
//     — the fallback for bare-Function-URL deployments so emails carry
//     clickable absolute links instead of relative paths.
//
// When both are empty the template renders relative links — still valid if
// the user clicks them from the dashboard itself, but broken in email
// clients. That case is a deployment misconfiguration (no domain, no
// Function URL) and deserves to surface.
func (h *Handler) resolveDashboardURL(req *events.LambdaFunctionURLRequest) string {
	if h.dashboardURL != "" {
		return strings.TrimRight(h.dashboardURL, "/")
	}
	if req != nil && req.RequestContext.DomainName != "" {
		return "https://" + req.RequestContext.DomainName
	}
	return ""
}

// resolveApprovalRecipients computes the To / Cc / authorised-approver sets
// for a purchase approval email based on the recommendations' account
// contact emails and the global Settings → General notification email.
//
// Intent: the account's contact_email is the party accountable for a
// purchase in that account; the global notification inbox is informed for
// visibility. So we direct the email at the contact email(s) as To, list
// any *other* contact emails plus the global notification email as Cc,
// and the approve/cancel token is only honoured for session holders whose
// email matches one of the authorised approvers (case-insensitive).
//
// **Authorisation policy** (post-security-hardening): the authorised-
// approver set is ALWAYS the per-account contact_email list, never the
// global notification email. The global notify mailbox is informed of the
// purchase via Cc but cannot itself approve. If no recommendation has a
// per-account contact_email, the approver set is empty and the caller
// must reject the approval with a clear error directing the operator to
// set a contact email on the account. This closes the loophole where a
// catch-all inbox could authorise spend on accounts it doesn't own.
//
// Returns ("", nil, nil, nil) when neither contact_email nor globalNotify
// is configured — the caller surfaces a user-facing error.
func (h *Handler) resolveApprovalRecipients(ctx context.Context, recs []config.RecommendationRecord, globalNotify string) (to string, cc []string, approvers []string, err error) {
	contactEmails, err := h.gatherAccountContactEmails(ctx, recs)
	if err != nil {
		return "", nil, nil, err
	}

	globalNotify = strings.TrimSpace(globalNotify)

	if len(contactEmails) == 0 {
		// No per-account contact_email anywhere — the global notify mailbox
		// can still receive the email (it's "To" so something gets sent),
		// but it is NOT in the approvers set. authorizeApprovalAction will
		// reject the approve/cancel because approvers is empty.
		if globalNotify == "" {
			return "", nil, nil, nil
		}
		return globalNotify, nil, nil, nil
	}

	to = contactEmails[0]
	approvers = append([]string(nil), contactEmails...)

	// Cc: remaining contact emails + global notification email, deduped
	// against the To address (the SES layer dedupes again as a belt-and-
	// braces guard, but keeping the list tidy here makes logs cleaner).
	seen := map[string]bool{strings.ToLower(to): true}
	appendIfNew := func(addr string) {
		norm := strings.ToLower(strings.TrimSpace(addr))
		if norm == "" || seen[norm] {
			return
		}
		seen[norm] = true
		cc = append(cc, addr)
	}
	for _, addr := range contactEmails[1:] {
		appendIfNew(addr)
	}
	appendIfNew(globalNotify)
	return to, cc, approvers, nil
}

// gatherAccountContactEmails returns the deduped (case-insensitive),
// insertion-ordered list of contact emails for the unique accounts
// referenced by recs. Accounts without a CloudAccountID or without a
// contact_email are silently skipped — they're not an error, just not a
// contribution to the authorised-approver set. A real DB error from
// lookupContactEmail is propagated so the caller surfaces it as a
// retriable failure instead of silently degrading to a globalNotify
// fallback (which would be wrong: a transient DB blip should not change
// who is allowed to approve).
func (h *Handler) gatherAccountContactEmails(ctx context.Context, recs []config.RecommendationRecord) ([]string, error) {
	orderedIDs := uniqueAccountIDsFromRecs(recs)
	if len(orderedIDs) == 0 {
		return nil, nil
	}
	emailsSeen := map[string]bool{}
	var out []string
	for _, id := range orderedIDs {
		addr, err := h.lookupContactEmail(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("lookup contact email for account %s: %w", id, err)
		}
		if addr == "" {
			continue
		}
		norm := strings.ToLower(addr)
		if emailsSeen[norm] {
			continue
		}
		emailsSeen[norm] = true
		out = append(out, addr)
	}
	return out, nil
}

// uniqueAccountIDsFromRecs returns the account IDs referenced by recs in
// insertion order, with duplicates and blanks stripped.
func uniqueAccountIDsFromRecs(recs []config.RecommendationRecord) []string {
	seen := map[string]bool{}
	var out []string
	for _, rec := range recs {
		if rec.CloudAccountID == nil || *rec.CloudAccountID == "" {
			continue
		}
		id := *rec.CloudAccountID
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// lookupContactEmail resolves the account's ContactEmail. The caller
// must distinguish three outcomes:
//
//   - real DB error → return ("", err). The caller propagates as a
//     retriable failure rather than silently treating the actor as
//     unauthorised or falling through to globalNotify; a transient
//     blip should not change who is allowed to approve.
//   - account-not-found (GetCloudAccount returns nil, nil per pgx
//     ErrNoRows handling in the postgres store) → return ("", nil).
//     Semantically equivalent to "no contact email configured".
//   - account found but ContactEmail is empty/whitespace →
//     return ("", nil). Same fall-through as above.
//
// Trimming happens here so the caller sees only normalised values.
func (h *Handler) lookupContactEmail(ctx context.Context, id string) (string, error) {
	acct, err := h.config.GetCloudAccount(ctx, id)
	if err != nil {
		logging.Warnf("lookupContactEmail: GetCloudAccount(%s) failed: %v", id, err)
		return "", err
	}
	if acct == nil {
		return "", nil
	}
	return strings.TrimSpace(acct.ContactEmail), nil
}

// authorizeApprovalAction returns the actor email to record on an
// approve/cancel action, after enforcing that the session-authenticated
// user's email is on the authorised-approver list for the given execution.
// Returns a 403 ClientError when the session is missing or the email
// doesn't match. The returned actor is stored as approved_by/cancelled_by
// on the execution.
//
// Rationale: the approve/cancel API routes are AuthPublic (token-only) for
// backwards compatibility with the email-link flow, but the web UI now
// redirects through login before calling this endpoint (see frontend
// /purchases/{action}/:id). Requiring the session's email to match the
// account's contact_email closes the gap where anyone holding the token
// (forwarded email, shared inbox, stolen link) could act on the purchase.
func (h *Handler) authorizeApprovalAction(ctx context.Context, req *events.LambdaFunctionURLRequest, execution *config.PurchaseExecution) (string, error) {
	actor := h.tryResolveActorEmail(ctx, req)
	if actor == "" {
		return "", NewClientError(401, "sign in with the account's contact email to approve or cancel this purchase")
	}

	globalNotify := ""
	if cfg, err := h.config.GetGlobalConfig(ctx); err == nil && cfg != nil && cfg.NotificationEmail != nil {
		globalNotify = *cfg.NotificationEmail
	}
	_, _, approvers, err := h.resolveApprovalRecipients(ctx, execution.Recommendations, globalNotify)
	if err != nil {
		return "", fmt.Errorf("failed to resolve approvers: %w", err)
	}
	if len(approvers) == 0 {
		// No per-account contact_email set on any of this execution's
		// recommendations. Per the authorisation policy in
		// resolveApprovalRecipients, the global notify mailbox is NOT a
		// valid approver — only per-account contact emails are. Direct
		// the operator to set the account's contact_email before approval
		// can proceed.
		return "", NewClientError(403, "no per-account contact email configured for this execution; set the cloud account's contact_email before approving")
	}
	actorLower := strings.ToLower(strings.TrimSpace(actor))
	for _, addr := range approvers {
		if strings.ToLower(strings.TrimSpace(addr)) == actorLower {
			return actor, nil
		}
	}
	return "", NewClientError(403, "your session email is not the authorised approver for this purchase")
}
