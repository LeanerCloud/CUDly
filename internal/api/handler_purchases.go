// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/email"
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

func (h *Handler) getPlannedPurchases(ctx context.Context, req *events.LambdaFunctionURLRequest) (*PlannedPurchasesResponse, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	executions, err := h.config.GetPendingExecutions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending executions: %w", err)
	}

	plans, err := h.config.ListPurchasePlans(ctx)
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

	// Atomically transition to cancelled
	if _, err := h.config.TransitionExecutionStatus(ctx, executionID, []string{"pending", "paused"}, "cancelled"); err != nil {
		return nil, NewClientError(409, fmt.Sprintf("execution %s cannot be cancelled: %v", executionID, err))
	}

	return &StatusResponse{Status: "cancelled"}, nil
}

// Purchase action handlers
func (h *Handler) approvePurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, execID, token string) (any, error) {
	if err := validateUUID(execID); err != nil {
		return nil, err
	}
	if token == "" {
		return nil, NewClientError(400, "approval token is required")
	}

	execution, err := h.config.GetExecutionByID(ctx, execID)
	if err != nil {
		return nil, fmt.Errorf("failed to get execution: %w", err)
	}
	if execution == nil {
		return nil, NewClientError(404, "execution not found")
	}
	actor, err := h.authorizeApprovalAction(ctx, req, execution)
	if err != nil {
		return nil, err
	}

	if err := h.purchase.ApproveExecution(ctx, execID, token, actor); err != nil {
		return nil, err
	}

	return map[string]string{"status": "approved"}, nil
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

	// Two-mode dispatch (issue #46):
	//   * token != ""  → legacy email-link flow. Token possession proves
	//     intent; the handler still requires the session email (when one
	//     exists) to be on the authorised-approver list, then delegates
	//     to the purchase service which validates the token itself.
	//   * token == ""  → session-authed dashboard Cancel button. The
	//     session must carry a permission allowing the cancel:
	//       - cancel-any:purchases (admin) → any pending execution; or
	//       - cancel-own:purchases (default user) AND the execution's
	//         created_by_user_id matches the session UserID.
	//     Legacy rows with NULL created_by_user_id are reachable only
	//     via cancel-any (admin) or via the email token already in the
	//     inbox — they do not become orphaned by this change.
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
// is in a cancellable state (pending|notified), atomically transitions the
// row to "cancelled", and stamps the cancelling user's email onto
// CancelledBy. The History UI's annotateCancelled() helper renders that
// into "cancelled by <email>" at read time — see handler_history.go.
func (h *Handler) cancelPurchaseViaSession(ctx context.Context, req *events.LambdaFunctionURLRequest, execution *config.PurchaseExecution) (any, error) {
	session, err := h.requireSession(ctx, req)
	if err != nil {
		return nil, err
	}

	if execution.Status != "pending" && execution.Status != "notified" {
		return nil, NewClientError(409, fmt.Sprintf("execution %s cannot be cancelled (status=%s)", execution.ExecutionID, execution.Status))
	}

	if err := h.authorizeSessionCancel(ctx, session, execution); err != nil {
		return nil, err
	}

	updated, err := h.config.TransitionExecutionStatus(ctx, execution.ExecutionID, []string{"pending", "notified"}, "cancelled")
	if err != nil {
		return nil, NewClientError(409, fmt.Sprintf("execution %s cannot be cancelled: %v", execution.ExecutionID, err))
	}

	// Persist who cancelled the row so the History view can show
	// "cancelled by <email>" instead of falling back to the notification
	// inbox. Best-effort — a write failure does not undo the status
	// transition; the row is already cancelled.
	if session.Email != "" {
		actor := session.Email
		updated.CancelledBy = &actor
		if err := h.config.SavePurchaseExecution(ctx, updated); err != nil {
			logging.Warnf("cancelPurchaseViaSession: failed to persist cancelled_by for %s: %v", execution.ExecutionID, err)
		}
	}

	return map[string]string{"status": "cancelled"}, nil
}

// authorizeSessionCancel returns nil when the session is permitted to cancel
// the given execution under the cancel-any / cancel-own RBAC rules added in
// issue #46. Returns a 403 ClientError otherwise.
func (h *Handler) authorizeSessionCancel(ctx context.Context, session *Session, execution *config.PurchaseExecution) error {
	if session.Role == "admin" {
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

// tryResolveActorEmail returns the email of the session-authenticated user
// who made the request, or "" when the request carries no valid session.
// Best-effort: the approve/cancel routes are AuthPublic (token-only), so
// we don't require a session; we just capture it when present so the
// auth-gated deep-link flow (frontend /purchases/{action}/:id → login
// → session-authed call with ?token=…) can record per-user attribution
// without changing the token-only fallback path used by message workers.
func (h *Handler) tryResolveActorEmail(ctx context.Context, req *events.LambdaFunctionURLRequest) string {
	if req == nil || h.auth == nil {
		return ""
	}
	bearer := h.extractBearerToken(req)
	if bearer == "" {
		return ""
	}
	session, err := h.auth.ValidateSession(ctx, bearer)
	if err != nil || session == nil {
		return ""
	}
	return session.Email
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
	// capacity_percent is audit-only but we still bound it: a request
	// with 0 / absent → default 100; anything outside [1, 100] is a
	// client bug worth surfacing rather than silently clamping.
	if execReq.CapacityPercent == 0 {
		execReq.CapacityPercent = 100
	}
	if execReq.CapacityPercent < 1 || execReq.CapacityPercent > 100 {
		return ExecutePurchaseRequest{}, nil, NewClientError(400, fmt.Sprintf("capacity_percent must be between 1 and 100, got %d", execReq.CapacityPercent))
	}
	// Scope: reject the whole request if any recommendation targets an
	// account outside the session's allowed_accounts. Safer than silently
	// dropping the out-of-scope ones — the user explicitly chose those
	// recommendations; a partial execution would misrepresent intent.
	if err := h.validatePurchaseRecommendationScope(ctx, session, execReq.Recommendations); err != nil {
		return ExecutePurchaseRequest{}, nil, err
	}
	return execReq, session, nil
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
func (h *Handler) executePurchase(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	execReq, session, err := h.validateExecutePurchaseRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	totalUpfront, totalSavings, err := validateAndTotalRecommendations(execReq.Recommendations)
	if err != nil {
		return nil, err
	}

	executionID := uuid.New().String()
	execution := &config.PurchaseExecution{
		ExecutionID:      executionID,
		Status:           "pending",
		ScheduledDate:    time.Now(),
		Recommendations:  execReq.Recommendations,
		TotalUpfrontCost: totalUpfront,
		EstimatedSavings: totalSavings,
		ApprovalToken:    uuid.New().String(),
		Source:           common.PurchaseSourceWeb,
		CapacityPercent:  execReq.CapacityPercent,
		CreatedByUserID:  resolveCreatorUserID(session),
	}

	// Load the grace-period config once before entering the tx so a
	// GetGlobalConfig failure fails the whole request cleanly rather
	// than leaving a half-committed tx state. Errors here don't block
	// the purchase — we just default the grace period per-provider.
	var gracePeriodCfg *config.GlobalConfig
	if g, err := h.config.GetGlobalConfig(ctx); err == nil {
		gracePeriodCfg = g
	}

	suppressions := buildSuppressions(execReq.Recommendations, executionID, gracePeriodCfg, time.Now())
	if err := h.config.WithTx(ctx, func(tx pgx.Tx) error {
		if err := h.config.SavePurchaseExecutionTx(ctx, tx, execution); err != nil {
			return err
		}
		for i := range suppressions {
			if err := h.config.CreateSuppressionTx(ctx, tx, &suppressions[i]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to save execution: %w", err)
	}

	// Send approval email synchronously so the response can surface the
	// actual outcome. The DB write above is the source of truth — email is
	// best-effort and never blocks the response body; the returned
	// email_sent / email_reason fields let the UI tell the user whether they
	// should wait for an inbox or cancel/retry manually.
	emailSent, emailReason := h.sendPurchaseApprovalEmail(ctx, req, execution, execReq.Recommendations, totalUpfront, totalSavings)
	status := h.finalizePurchaseStatus(ctx, execution, emailSent, emailReason)

	message := "Purchase execution created and pending approval"
	if !emailSent {
		message = "Purchase execution created but approval email could not be sent — see email_reason"
	}
	resp := map[string]any{
		"execution_id":         executionID,
		"status":               status,
		"recommendation_count": len(execReq.Recommendations),
		"total_upfront_cost":   totalUpfront,
		"estimated_savings":    totalSavings,
		"email_sent":           emailSent,
		"message":              message,
	}
	if emailReason != "" {
		resp["email_reason"] = emailReason
	}
	return resp, nil
}

// sendPurchaseApprovalEmail sends an approval-request email for a newly created
// execution and returns a structured outcome:
//   - (true, "") on successful send
//   - (false, "<reason>") on any preflight gate or send error
//
// Errors are also logged at Errorf level so they show up in CloudWatch, but
// the reason string is what the API response surfaces to the UI.
func (h *Handler) sendPurchaseApprovalEmail(ctx context.Context, req *events.LambdaFunctionURLRequest, execution *config.PurchaseExecution, recs []config.RecommendationRecord, totalUpfront, totalSavings float64) (bool, string) {
	if h.emailNotifier == nil {
		return false, "email notifier not configured for this deployment"
	}
	globalCfg, err := h.config.GetGlobalConfig(ctx)
	if err != nil {
		logging.Errorf("Failed to load global config for approval email: %v", err)
		return false, fmt.Sprintf("failed to load settings: %v", err)
	}
	globalNotify := ""
	if globalCfg.NotificationEmail != nil {
		globalNotify = *globalCfg.NotificationEmail
	}
	to, cc, approvers, err := h.resolveApprovalRecipients(ctx, recs, globalNotify)
	if err != nil {
		logging.Errorf("Failed to resolve approval recipients: %v", err)
		return false, fmt.Sprintf("failed to resolve recipients: %v", err)
	}
	if to == "" {
		return false, "no notification email set in Settings → General and no account contact emails configured"
	}
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
	data := email.NotificationData{
		DashboardURL:        h.resolveDashboardURL(req),
		ApprovalToken:       execution.ApprovalToken,
		ExecutionID:         execution.ExecutionID,
		TotalSavings:        totalSavings,
		TotalUpfrontCost:    totalUpfront,
		Recommendations:     summaries,
		RecipientEmail:      to,
		CCEmails:            cc,
		AuthorizedApprovers: approvers,
	}
	if err := h.emailNotifier.SendPurchaseApprovalRequest(ctx, data); err != nil {
		logging.Errorf("Failed to send purchase approval email: %v", err)
		switch {
		case errors.Is(err, email.ErrNoRecipient):
			return false, "no notification email set in Settings → General"
		case errors.Is(err, email.ErrNoFromEmail):
			return false, "FROM_EMAIL not configured for this deployment"
		default:
			return false, fmt.Sprintf("send failed: %v", err)
		}
	}
	return true, ""
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
// Fallbacks:
//   - When none of the rec'd accounts carry a contact_email, the global
//     notification email takes the To slot and is itself the sole
//     authorised approver (legacy single-recipient behaviour).
//   - When neither is configured, returns ("", nil, nil, nil); the caller
//     surfaces a user-facing error.
func (h *Handler) resolveApprovalRecipients(ctx context.Context, recs []config.RecommendationRecord, globalNotify string) (to string, cc []string, approvers []string, err error) {
	contactEmails, err := h.gatherAccountContactEmails(ctx, recs)
	if err != nil {
		return "", nil, nil, err
	}

	globalNotify = strings.TrimSpace(globalNotify)

	if len(contactEmails) == 0 {
		// No per-account contacts — fall back to the global notification
		// email as the sole addressee + sole approver.
		if globalNotify == "" {
			return "", nil, nil, nil
		}
		return globalNotify, nil, []string{globalNotify}, nil
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
// contribution to the authorised-approver set.
func (h *Handler) gatherAccountContactEmails(ctx context.Context, recs []config.RecommendationRecord) ([]string, error) {
	orderedIDs := uniqueAccountIDsFromRecs(recs)
	if len(orderedIDs) == 0 {
		return nil, nil
	}
	emailsSeen := map[string]bool{}
	var out []string
	for _, id := range orderedIDs {
		addr := h.lookupContactEmail(ctx, id)
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

// lookupContactEmail resolves the account's ContactEmail, or returns "" if
// the account can't be found or doesn't have one. A single missing account
// should not poison the whole email — errors are logged and swallowed.
func (h *Handler) lookupContactEmail(ctx context.Context, id string) string {
	acct, err := h.config.GetCloudAccount(ctx, id)
	if err != nil {
		logging.Warnf("resolveApprovalRecipients: GetCloudAccount(%s) failed: %v", id, err)
		return ""
	}
	if acct == nil {
		return ""
	}
	return strings.TrimSpace(acct.ContactEmail)
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
		// No authorised approvers at all — happens when the account has
		// no contact email AND Settings → General has no notification
		// email. Reject; an operator has to set at least one before the
		// flow works.
		return "", NewClientError(403, "no authorised approver configured for this execution (set the account's contact email or the global notification email)")
	}
	actorLower := strings.ToLower(strings.TrimSpace(actor))
	for _, addr := range approvers {
		if strings.ToLower(strings.TrimSpace(addr)) == actorLower {
			return actor, nil
		}
	}
	return "", NewClientError(403, "your session email is not the authorised approver for this purchase")
}
