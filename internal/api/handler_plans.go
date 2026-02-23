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

// Plans handlers
func (h *Handler) listPlans(ctx context.Context, req *events.LambdaFunctionURLRequest) (*PlansResponse, error) {
	// Require admin access for viewing plans
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	plans, err := h.config.ListPurchasePlans(ctx)
	if err != nil {
		return nil, err
	}

	// Ensure all enabled plans have NextExecutionDate calculated
	now := time.Now()
	for i := range plans {
		if plans[i].Enabled && plans[i].NextExecutionDate == nil {
			plans[i].NextExecutionDate = calculateNextExecutionDate(&plans[i], now)
		}
	}

	return &PlansResponse{Plans: plans}, nil
}

// calculateNextExecutionDate calculates the next execution date for a plan
func calculateNextExecutionDate(plan *config.PurchasePlan, now time.Time) *time.Time {
	var nextDate time.Time
	if plan.RampSchedule.Type == "immediate" {
		// For immediate, schedule for tomorrow
		nextDate = now.AddDate(0, 0, 1)
	} else if plan.RampSchedule.StepIntervalDays > 0 {
		// For other schedules, use the step interval from now
		nextDate = now.AddDate(0, 0, plan.RampSchedule.StepIntervalDays)
	} else {
		// Default to tomorrow
		nextDate = now.AddDate(0, 0, 1)
	}
	return &nextDate
}

func (h *Handler) createPlan(ctx context.Context, httpReq *events.LambdaFunctionURLRequest) (any, error) {
	// Require admin access for creating plans
	if _, err := h.requireAdmin(ctx, httpReq); err != nil {
		return nil, err
	}

	var req PlanRequest
	if err := json.Unmarshal([]byte(httpReq.Body), &req); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	plan := req.toPurchasePlan()

	// Validate the plan
	if err := plan.Validate(); err != nil {
		return nil, NewClientError(400, fmt.Sprintf("validation error: %s", err))
	}

	if err := h.config.CreatePurchasePlan(ctx, plan); err != nil {
		return nil, err
	}

	return plan, nil
}

func (h *Handler) getPlan(ctx context.Context, req *events.LambdaFunctionURLRequest, planID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(planID); err != nil {
		return nil, err
	}

	// Require admin access for viewing plan details
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	plan, err := h.config.GetPurchasePlan(ctx, planID)
	if err != nil {
		return nil, err
	}

	// Ensure plan has NextExecutionDate calculated
	if plan.Enabled && plan.NextExecutionDate == nil {
		plan.NextExecutionDate = calculateNextExecutionDate(plan, time.Now())
	}

	return plan, nil
}

func (h *Handler) updatePlan(ctx context.Context, httpReq *events.LambdaFunctionURLRequest, planID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(planID); err != nil {
		return nil, err
	}

	// Require admin access for updating plans
	if _, err := h.requireAdmin(ctx, httpReq); err != nil {
		return nil, err
	}

	var req PlanRequest
	if err := json.Unmarshal([]byte(httpReq.Body), &req); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Fetch existing plan to preserve data not sent in request
	existingPlan, err := h.config.GetPurchasePlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("plan not found: %s", planID)
	}

	// Create new plan from request
	plan := req.toPurchasePlan()
	plan.ID = planID

	// Preserve timestamps from existing plan
	plan.CreatedAt = existingPlan.CreatedAt
	plan.UpdatedAt = time.Now()

	// If no services were created from request, preserve existing services
	if len(plan.Services) == 0 && len(existingPlan.Services) > 0 {
		plan.Services = existingPlan.Services
	}

	// Validate the plan
	if err := plan.Validate(); err != nil {
		return nil, NewClientError(400, fmt.Sprintf("validation error: %s", err))
	}

	if err := h.config.UpdatePurchasePlan(ctx, plan); err != nil {
		return nil, err
	}

	return plan, nil
}

func (h *Handler) deletePlan(ctx context.Context, req *events.LambdaFunctionURLRequest, planID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(planID); err != nil {
		return nil, err
	}

	// Require admin access for deleting plans
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	if err := h.config.DeletePurchasePlan(ctx, planID); err != nil {
		return nil, err
	}

	return map[string]string{"status": "deleted"}, nil
}

func (h *Handler) createPlannedPurchases(ctx context.Context, httpReq *events.LambdaFunctionURLRequest, planID string) (*CreatePlannedPurchasesResponse, error) {
	if err := validateUUID(planID); err != nil {
		return nil, err
	}

	if _, err := h.requireAdmin(ctx, httpReq); err != nil {
		return nil, err
	}

	req, startDate, err := h.parseCreatePurchasesRequest(httpReq.Body)
	if err != nil {
		return nil, err
	}

	plan, err := h.getPlanForPurchaseCreation(ctx, planID)
	if err != nil {
		return nil, err
	}

	created, err := h.createPurchaseExecutions(ctx, plan, planID, req.Count, startDate)
	if err != nil {
		return nil, err
	}

	if err := h.updatePlanNextExecutionDate(ctx, plan, startDate); err != nil {
		return nil, err
	}

	return &CreatePlannedPurchasesResponse{Created: created}, nil
}

// parseCreatePurchasesRequest parses and validates the create purchases request
func (h *Handler) parseCreatePurchasesRequest(body string) (*CreatePlannedPurchasesRequest, time.Time, error) {
	var req CreatePlannedPurchasesRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return nil, time.Time{}, NewClientError(400, "invalid request body")
	}

	if req.Count < 1 || req.Count > 52 {
		return nil, time.Time{}, NewClientError(400, "count must be between 1 and 52")
	}

	startDate, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		return nil, time.Time{}, NewClientError(400, "invalid start_date format, expected YYYY-MM-DD")
	}

	return &req, startDate, nil
}

// getPlanForPurchaseCreation retrieves and validates the purchase plan
func (h *Handler) getPlanForPurchaseCreation(ctx context.Context, planID string) (*config.PurchasePlan, error) {
	plan, err := h.config.GetPurchasePlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("failed to get plan: %w", err)
	}
	if plan == nil {
		return nil, fmt.Errorf("plan not found: %s", planID)
	}
	return plan, nil
}

// createPurchaseExecutions creates multiple purchase executions based on the plan's schedule
func (h *Handler) createPurchaseExecutions(ctx context.Context, plan *config.PurchasePlan, planID string, count int, startDate time.Time) (int, error) {
	intervalDays := plan.RampSchedule.StepIntervalDays
	if intervalDays == 0 {
		intervalDays = 7 // Default to weekly if not set
	}

	created := 0
	for i := 0; i < count; i++ {
		scheduledDate := startDate.AddDate(0, 0, i*intervalDays)

		execution := &config.PurchaseExecution{
			PlanID:        planID,
			ExecutionID:   uuid.New().String(),
			Status:        "pending",
			StepNumber:    plan.RampSchedule.CurrentStep + i + 1,
			ScheduledDate: scheduledDate,
			ApprovalToken: uuid.New().String(),
		}

		if err := h.config.SavePurchaseExecution(ctx, execution); err != nil {
			return 0, fmt.Errorf("failed to save execution: %w", err)
		}
		created++
	}

	return created, nil
}

// updatePlanNextExecutionDate updates the plan's next execution date if needed
func (h *Handler) updatePlanNextExecutionDate(ctx context.Context, plan *config.PurchasePlan, startDate time.Time) error {
	if plan.NextExecutionDate == nil || plan.NextExecutionDate.After(startDate) {
		plan.NextExecutionDate = &startDate
		plan.UpdatedAt = time.Now()
		if err := h.config.UpdatePurchasePlan(ctx, plan); err != nil {
			return fmt.Errorf("failed to update plan: %w", err)
		}
	}
	return nil
}

// PatchPlanRequest represents a partial update request for plans
type PatchPlanRequest struct {
	Name                   *string `json:"name,omitempty"`
	Enabled                *bool   `json:"enabled,omitempty"`
	AutoPurchase           *bool   `json:"auto_purchase,omitempty"`
	NotificationDaysBefore *int    `json:"notification_days_before,omitempty"`
}

// patchPlan handles partial updates to a plan (PATCH method)
func (h *Handler) patchPlan(ctx context.Context, httpReq *events.LambdaFunctionURLRequest, planID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(planID); err != nil {
		return nil, err
	}

	// Require admin access for patching plans
	if _, err := h.requireAdmin(ctx, httpReq); err != nil {
		return nil, err
	}

	var req PatchPlanRequest
	if err := json.Unmarshal([]byte(httpReq.Body), &req); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Fetch existing plan
	plan, err := h.config.GetPurchasePlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("plan not found: %s", planID)
	}
	if plan == nil {
		return nil, fmt.Errorf("plan not found: %s", planID)
	}

	// Apply only the fields that are present in the request, with validation
	if req.Name != nil {
		if len(*req.Name) == 0 {
			return nil, NewClientError(400, "plan name cannot be empty")
		}
		if len(*req.Name) > 255 {
			return nil, NewClientError(400, "plan name too long (max 255 characters)")
		}
		plan.Name = *req.Name
	}
	if req.Enabled != nil {
		plan.Enabled = *req.Enabled
	}
	if req.AutoPurchase != nil {
		plan.AutoPurchase = *req.AutoPurchase
	}
	if req.NotificationDaysBefore != nil {
		if *req.NotificationDaysBefore < 0 || *req.NotificationDaysBefore > 30 {
			return nil, NewClientError(400, "notification_days_before must be between 0 and 30")
		}
		plan.NotificationDaysBefore = *req.NotificationDaysBefore
	}

	// Update timestamp
	plan.UpdatedAt = time.Now()

	// Validate and save
	if err := plan.Validate(); err != nil {
		return nil, NewClientError(400, fmt.Sprintf("validation error: %s", err))
	}

	if err := h.config.UpdatePurchasePlan(ctx, plan); err != nil {
		return nil, err
	}

	return plan, nil
}
