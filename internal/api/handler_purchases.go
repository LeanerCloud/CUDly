// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/google/uuid"
)

func (h *Handler) getPlannedPurchases(ctx context.Context, req *events.LambdaFunctionURLRequest) (*PlannedPurchasesResponse, error) {
	// Require admin access for viewing planned purchases
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	// Get all pending executions (actual scheduled purchases)
	executions, err := h.config.GetPendingExecutions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending executions: %w", err)
	}

	// Get all purchase plans for metadata
	plans, err := h.config.ListPurchasePlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get purchase plans: %w", err)
	}

	// Build plan map for lookup
	planMap := make(map[string]*config.PurchasePlan)
	for i := range plans {
		planMap[plans[i].ID] = &plans[i]
	}

	var purchases []PlannedPurchase

	// Convert executions to planned purchases
	for _, exec := range executions {
		plan := planMap[exec.PlanID]
		if plan == nil {
			continue
		}

		// Get first service config for provider/service info
		var provider, service string
		var term int
		var payment string
		for _, svcCfg := range plan.Services {
			provider = svcCfg.Provider
			service = svcCfg.Service
			term = svcCfg.Term
			payment = svcCfg.Payment
			break
		}

		scheduledDate := exec.ScheduledDate.Format("2006-01-02")

		purchases = append(purchases, PlannedPurchase{
			ID:               exec.ExecutionID,
			PlanID:           exec.PlanID,
			PlanName:         plan.Name,
			ScheduledDate:    scheduledDate,
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
		})
	}

	return &PlannedPurchasesResponse{
		Purchases: purchases,
	}, nil
}

func (h *Handler) pausePlannedPurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, executionID string) (*StatusResponse, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(executionID); err != nil {
		return nil, err
	}

	// Require admin access for pausing planned purchases
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	// Get the execution and set status to paused
	execution, err := h.config.GetExecutionByID(ctx, executionID)
	if err != nil {
		return nil, fmt.Errorf("execution not found: %w", err)
	}
	if execution == nil {
		return nil, fmt.Errorf("execution not found: %s", executionID)
	}

	execution.Status = "paused"
	if err := h.config.SavePurchaseExecution(ctx, execution); err != nil {
		return nil, fmt.Errorf("failed to pause execution: %w", err)
	}

	return &StatusResponse{Status: "paused"}, nil
}

func (h *Handler) resumePlannedPurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, executionID string) (*StatusResponse, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(executionID); err != nil {
		return nil, err
	}

	// Require admin access for resuming planned purchases
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	// Get the execution and set status back to pending
	execution, err := h.config.GetExecutionByID(ctx, executionID)
	if err != nil {
		return nil, fmt.Errorf("execution not found: %w", err)
	}
	if execution == nil {
		return nil, fmt.Errorf("execution not found: %s", executionID)
	}

	execution.Status = "pending"
	if err := h.config.SavePurchaseExecution(ctx, execution); err != nil {
		return nil, fmt.Errorf("failed to resume execution: %w", err)
	}

	return &StatusResponse{Status: "resumed"}, nil
}

func (h *Handler) runPlannedPurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, executionID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(executionID); err != nil {
		return nil, err
	}

	// Require admin access for running planned purchases
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	// Get the execution
	execution, err := h.config.GetExecutionByID(ctx, executionID)
	if err != nil {
		return nil, fmt.Errorf("execution not found: %w", err)
	}
	if execution == nil {
		return nil, fmt.Errorf("execution not found: %s", executionID)
	}

	// Set status to running and trigger execution
	execution.Status = "running"
	if err := h.config.SavePurchaseExecution(ctx, execution); err != nil {
		return nil, fmt.Errorf("failed to update execution: %w", err)
	}

	return map[string]any{
		"execution_id": executionID,
		"status":       "running",
		"message":      "Purchase execution initiated",
	}, nil
}

func (h *Handler) deletePlannedPurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, executionID string) (*StatusResponse, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(executionID); err != nil {
		return nil, err
	}

	// Require admin access for deleting planned purchases
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	// Get the execution and set status to cancelled
	execution, err := h.config.GetExecutionByID(ctx, executionID)
	if err != nil {
		return nil, fmt.Errorf("execution not found: %w", err)
	}
	if execution == nil {
		return nil, fmt.Errorf("execution not found: %s", executionID)
	}

	execution.Status = "cancelled"
	if err := h.config.SavePurchaseExecution(ctx, execution); err != nil {
		return nil, fmt.Errorf("failed to cancel execution: %w", err)
	}

	return &StatusResponse{Status: "cancelled"}, nil
}

// Purchase action handlers
func (h *Handler) approvePurchase(ctx context.Context, execID, token string) (any, error) {
	if err := validateUUID(execID); err != nil {
		return nil, err
	}
	if token == "" {
		return nil, fmt.Errorf("approval token is required")
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
		return nil, fmt.Errorf("cancellation token is required")
	}

	if err := h.purchase.CancelExecution(ctx, execID, token); err != nil {
		return nil, err
	}

	return map[string]string{"status": "cancelled"}, nil
}

// getPurchaseDetails returns details about a specific purchase execution
func (h *Handler) getPurchaseDetails(ctx context.Context, req *events.LambdaFunctionURLRequest, executionID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(executionID); err != nil {
		return nil, err
	}

	// Require admin access for viewing purchase details
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	// Get the execution
	execution, err := h.config.GetExecutionByID(ctx, executionID)
	if err != nil {
		return nil, fmt.Errorf("execution not found: %w", err)
	}
	if execution == nil {
		return nil, fmt.Errorf("execution not found: %s", executionID)
	}

	// Get associated plan for additional details
	var planName string
	if execution.PlanID != "" {
		plan, err := h.config.GetPurchasePlan(ctx, execution.PlanID)
		if err == nil && plan != nil {
			planName = plan.Name
		}
	}

	// Build response matching frontend expectations
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

	return response, nil
}

// ExecutePurchaseRequest represents the request to execute purchases
type ExecutePurchaseRequest struct {
	Recommendations []config.RecommendationRecord `json:"recommendations"`
}

// executePurchase handles direct purchase execution from recommendations
func (h *Handler) executePurchase(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	// Require admin access for executing purchases
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	var execReq ExecutePurchaseRequest
	if err := json.Unmarshal([]byte(req.Body), &execReq); err != nil {
		return nil, fmt.Errorf("invalid request body: %w", err)
	}

	// Validate recommendations count to prevent DoS
	const maxRecommendations = 1000
	if len(execReq.Recommendations) == 0 {
		return nil, fmt.Errorf("no recommendations provided")
	}
	if len(execReq.Recommendations) > maxRecommendations {
		return nil, fmt.Errorf("too many recommendations: %d (max %d)", len(execReq.Recommendations), maxRecommendations)
	}

	// Create a new execution for these recommendations
	executionID := uuid.New().String()
	now := time.Now()

	// Calculate totals from recommendations with validation
	const maxAmount = 10_000_000 // $10M sanity cap
	var totalUpfront, totalSavings float64
	for i, rec := range execReq.Recommendations {
		if rec.UpfrontCost < 0 {
			return nil, fmt.Errorf("recommendation %d has negative upfront cost: %.2f", i, rec.UpfrontCost)
		}
		if rec.Savings < 0 {
			return nil, fmt.Errorf("recommendation %d has negative savings: %.2f", i, rec.Savings)
		}
		totalUpfront += rec.UpfrontCost
		totalSavings += rec.Savings
	}
	if totalUpfront > maxAmount {
		return nil, fmt.Errorf("total upfront cost %.2f exceeds maximum allowed (%.2f)", totalUpfront, float64(maxAmount))
	}
	if totalSavings > maxAmount {
		return nil, fmt.Errorf("total estimated savings %.2f exceeds maximum allowed (%.2f)", totalSavings, float64(maxAmount))
	}

	execution := &config.PurchaseExecution{
		ExecutionID:      executionID,
		Status:           "pending",
		ScheduledDate:    now,
		Recommendations:  execReq.Recommendations,
		TotalUpfrontCost: totalUpfront,
		EstimatedSavings: totalSavings,
		ApprovalToken:    uuid.New().String(),
	}

	if err := h.config.SavePurchaseExecution(ctx, execution); err != nil {
		return nil, fmt.Errorf("failed to save execution: %w", err)
	}

	return map[string]any{
		"execution_id":         executionID,
		"status":               "pending",
		"recommendation_count": len(execReq.Recommendations),
		"total_upfront_cost":   totalUpfront,
		"estimated_savings":    totalSavings,
		"message":              "Purchase execution created and pending approval",
	}, nil
}
