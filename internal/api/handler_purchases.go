// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
	"github.com/google/uuid"
)

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
func (h *Handler) approvePurchase(ctx context.Context, execID, token string) (any, error) {
	if err := validateUUID(execID); err != nil {
		return nil, err
	}
	if token == "" {
		return nil, NewClientError(400, "approval token is required")
	}

	if err := h.purchase.ApproveExecution(ctx, execID, token); err != nil {
		return nil, err
	}

	return map[string]string{"status": "approved"}, nil
}

func (h *Handler) cancelPurchase(ctx context.Context, execID, token string) (any, error) {
	if err := validateUUID(execID); err != nil {
		return nil, err
	}
	if token == "" {
		return nil, NewClientError(400, "cancellation token is required")
	}

	if err := h.purchase.CancelExecution(ctx, execID, token); err != nil {
		return nil, err
	}

	return map[string]string{"status": "cancelled"}, nil
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
	_ = session

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
	}

	if err := h.config.SavePurchaseExecution(ctx, execution); err != nil {
		return nil, fmt.Errorf("failed to save execution: %w", err)
	}

	// Send approval email synchronously so the response can surface the
	// actual outcome. The DB write above is the source of truth — email is
	// best-effort and never blocks the response body; the returned
	// email_sent / email_reason fields let the UI tell the user whether they
	// should wait for an inbox or cancel/retry manually.
	emailSent, emailReason := h.sendPurchaseApprovalEmail(ctx, execution, execReq.Recommendations, totalUpfront, totalSavings)
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
func (h *Handler) sendPurchaseApprovalEmail(ctx context.Context, execution *config.PurchaseExecution, recs []config.RecommendationRecord, totalUpfront, totalSavings float64) (bool, string) {
	if h.emailNotifier == nil {
		return false, "email notifier not configured for this deployment"
	}
	globalCfg, err := h.config.GetGlobalConfig(ctx)
	if err != nil {
		logging.Errorf("Failed to load global config for approval email: %v", err)
		return false, fmt.Sprintf("failed to load settings: %v", err)
	}
	if globalCfg.NotificationEmail == nil || *globalCfg.NotificationEmail == "" {
		return false, "no notification email set in Settings → General"
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
		DashboardURL:     h.dashboardURL,
		ApprovalToken:    execution.ApprovalToken,
		ExecutionID:      execution.ExecutionID,
		TotalSavings:     totalSavings,
		TotalUpfrontCost: totalUpfront,
		Recommendations:  summaries,
		RecipientEmail:   *globalCfg.NotificationEmail,
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
