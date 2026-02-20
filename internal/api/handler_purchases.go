// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
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

func (h *Handler) runPlannedPurchase(ctx context.Context, req *events.LambdaFunctionURLRequest, executionID string) (interface{}, error) {
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

	return map[string]interface{}{
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
func (h *Handler) approvePurchase(ctx context.Context, execID, token string) (interface{}, error) {
	if err := h.purchase.ApproveExecution(ctx, execID, token); err != nil {
		return nil, err
	}

	return map[string]string{"status": "approved"}, nil
}

func (h *Handler) cancelPurchase(ctx context.Context, execID, token string) (interface{}, error) {
	if err := h.purchase.CancelExecution(ctx, execID, token); err != nil {
		return nil, err
	}

	return map[string]string{"status": "cancelled"}, nil
}
