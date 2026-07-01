// Package apihttp provides the HTTP API handlers for the CUDly dashboard.
package apihttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Plans handlers
func (h *Handler) listPlans(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (*PlansResponse, error) {
	// Require view:plans permission
	if _, err := h.requirePermission(ctx, req, "view", "plans"); err != nil {
		return nil, err
	}

	// parseAccountIDs validates and splits the comma-separated account_ids
	// query param. Returns nil (no filter) when absent or empty.
	accountIDs, err := parseAccountIDs(params["account_ids"])
	if err != nil {
		return nil, NewClientError(400, err.Error())
	}

	filter := config.PurchasePlanFilter{AccountIDs: accountIDs}
	plans, err := h.config.ListPurchasePlans(ctx, filter)
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

// mapCreatePlanStorageError converts a storage-layer error from createPlan into
// the appropriate HTTP ClientError. If err is ErrNotFound it returns a 404
// with notFoundMsg; otherwise it logs the supplied format string at ERROR level
// and returns a 500 with genericMsg.
func mapCreatePlanStorageError(err error, notFoundMsg, genericMsg, logFmt string, logArgs ...any) error {
	if errors.Is(err, config.ErrNotFound) {
		return NewClientError(http.StatusNotFound, notFoundMsg)
	}
	logging.Errorf(logFmt, logArgs...)
	return NewClientError(http.StatusInternalServerError, genericMsg)
}

func (h *Handler) createPlan(ctx context.Context, httpReq *events.LambdaFunctionURLRequest) (any, error) {
	// Require create:plans permission
	if _, err := h.requirePermission(ctx, httpReq, "create", "plans"); err != nil {
		return nil, err
	}

	var req PlanRequest
	if err := json.Unmarshal([]byte(httpReq.Body), &req); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// target_accounts is required: a plan must be tied to at least one
	// cloud_account row. The historical "leave blank to mean all accounts of
	// this provider" behaviour created "universal plans" (rows in
	// purchase_plans with no matching plan_accounts row) that were hard to
	// scope, hard to filter, and hard to govern. Reject early with a clear
	// 400 so the frontend can surface the error before any DB write.
	if err := validateTargetAccounts(req.TargetAccounts); err != nil {
		return nil, err
	}

	plan := req.toPurchasePlan()

	// Validate the plan
	if err := plan.Validate(); err != nil {
		return nil, NewClientError(400, fmt.Sprintf("validation error: %s", err))
	}

	if err := h.config.CreatePurchasePlan(ctx, plan); err != nil {
		return nil, mapCreatePlanStorageError(err,
			"plan not found", "failed to create plan",
			"createPlan: CreatePurchasePlan failed (provider=%s service=%s accounts=%d): %v",
			req.Provider, req.Service, len(req.TargetAccounts), err)
	}

	// Provider-match validation + plan_accounts insert. SetPlanAccounts is
	// transactional internally, but the plan-row insert above is not part of
	// that tx. If either step here fails we roll the plan row back so the
	// invariant "every purchase_plans row has at least one plan_accounts
	// row" holds end-to-end.
	rollbackPlan := func() {
		if delErr := h.config.DeletePurchasePlan(ctx, plan.ID); delErr != nil {
			logging.Warnf("createPlan rollback: failed to delete partial plan %s: %v (manual cleanup may be required)", plan.ID, delErr)
		}
	}

	if err := h.validatePlanAccountProviders(ctx, plan.ID, req.TargetAccounts); err != nil {
		rollbackPlan()
		return nil, err
	}

	if err := h.config.SetPlanAccounts(ctx, plan.ID, req.TargetAccounts); err != nil {
		rollbackPlan()
		return nil, mapCreatePlanStorageError(err,
			"account not found", "failed to assign accounts to plan",
			"createPlan: SetPlanAccounts failed (plan=%s accounts=%d): %v",
			plan.ID, len(req.TargetAccounts), err)
	}

	return plan, nil
}

// validateTargetAccounts rejects a missing/empty target_accounts payload and
// rejects entries that are not valid UUIDs. Mirrors the validation the
// dedicated PUT /plans/:id/accounts endpoint already performs (see
// setPlanAccounts in handler_accounts.go) so a request that gets past
// createPlan would also get past that endpoint.
func validateTargetAccounts(ids []string) error {
	if len(ids) == 0 {
		return NewClientError(400, "target_accounts is required: a plan must be tied to at least one account")
	}
	for _, aid := range ids {
		if err := validateUUID(aid); err != nil {
			return NewClientError(400, fmt.Sprintf("invalid target_account %q: must be a valid UUID", aid))
		}
	}
	return nil
}

func (h *Handler) getPlan(ctx context.Context, req *events.LambdaFunctionURLRequest, planID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(planID); err != nil {
		return nil, err
	}

	session, err := h.requirePermission(ctx, req, "view", "plans")
	if err != nil {
		return nil, err
	}

	if err := h.requirePlanAccess(ctx, session, planID); err != nil {
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

	session, err := h.requirePermission(ctx, httpReq, "update", "plans")
	if err != nil {
		return nil, err
	}

	if err := h.requirePlanAccess(ctx, session, planID); err != nil {
		return nil, err
	}

	var req PlanRequest
	if err := json.Unmarshal([]byte(httpReq.Body), &req); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Fetch existing plan to preserve data not sent in request. Do NOT wrap
	// err with the raw plan UUID: a non-ClientError propagates to the router's
	// logging.Errorf("API error: %v"), leaking the plan UUID and the raw DB
	// error string (issue #965, same shape as the createPlan-side fix).
	existingPlan, err := h.config.GetPurchasePlan(ctx, planID)
	if err != nil {
		return nil, mapCreatePlanStorageError(err,
			"plan not found", "failed to load plan",
			"updatePlan: GetPurchasePlan failed")
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

	session, err := h.requirePermission(ctx, req, "delete", "plans")
	if err != nil {
		return nil, err
	}

	if err := h.requirePlanAccess(ctx, session, planID); err != nil {
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

	session, err := h.requirePermission(ctx, httpReq, "create", "plans")
	if err != nil {
		return nil, err
	}

	if err := h.requirePlanAccess(ctx, session, planID); err != nil {
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

	// Atomic write: per-row execution inserts and the plan's
	// next_execution_date bump commit together, or roll back together.
	// The previous implementation called SavePurchaseExecution outside
	// a transaction, so a mid-loop failure (e.g. network blip on row 4
	// of 5) left rows 1-3 persisted and updatePlanNextExecutionDate
	// either skipped (orphaned rows) or partially-applied (stale plan
	// pointer). A retry would then duplicate rows 1-3. WithTx makes
	// both classes of corruption impossible — the caller can safely
	// retry on transient errors knowing nothing was committed.
	//
	// Issue #950: stamp the session user onto each new execution's
	// created_by_user_id so the per-row creator-scope ownership gate
	// (authorizeExecutionManagement) recognises the actor who scheduled
	// the purchases as their owner. Without this the rows ship NULL and
	// are unreachable for pause / resume / run / delete by anyone except
	// admins / update-any holders, including the user who just clicked
	// "Create planned purchases" for their own plan. Admin-API-key and
	// non-UUID sessions resolve to nil, matching the executePurchase /
	// retry paths and the migration-000041 fail-closed policy.
	creator := resolveCreatorUserID(session)
	created := 0
	if err := h.config.WithTx(ctx, func(tx pgx.Tx) error {
		n, txErr := h.createPurchaseExecutionsTx(ctx, tx, plan, planID, req.Count, startDate, creator)
		if txErr != nil {
			return txErr
		}
		if planErr := h.updatePlanNextExecutionDateTx(ctx, tx, plan, startDate); planErr != nil {
			return planErr
		}
		created = n
		return nil
	}); err != nil {
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

// getPlanForPurchaseCreation retrieves and validates the purchase plan.
//
// Errors are routed through mapCreatePlanStorageError so a DB failure never
// leaks the raw error string through the router's
// logging.Errorf("API error: %v") path (issue #965, same shape as the
// validatePlanAccountProviders fix). The "plan not found" case (nil, nil) is
// returned as a 404 ClientError; planID is the caller-supplied value, so
// echoing it back in the 404 body is acceptable.
func (h *Handler) getPlanForPurchaseCreation(ctx context.Context, planID string) (*config.PurchasePlan, error) {
	plan, err := h.config.GetPurchasePlan(ctx, planID)
	if err != nil {
		return nil, mapCreatePlanStorageError(err,
			"plan not found", "failed to load plan",
			"getPlanForPurchaseCreation: GetPurchasePlan failed")
	}
	if plan == nil {
		return nil, NewClientError(404, fmt.Sprintf("plan not found: %s", planID))
	}
	return plan, nil
}

// createPurchaseExecutionsTx creates the per-row purchase executions
// inside the caller's transaction. A mid-loop failure rolls the whole
// transaction back, so the caller never sees orphaned rows on retry.
// Returns the number of rows that would have been committed had the
// loop completed — used for the user-visible response on success;
// undefined (and unused) on error since the rollback voids them all.
//
// creator carries the session user's UUID (or nil for the admin-API-key /
// non-UUID-session paths) and is stamped onto every inserted row's
// created_by_user_id so the issue-#950 ownership gate downstream can
// recognise the actor as the rightful manager. A nil value mirrors the
// migration-000041 fail-closed semantics: legacy / unattributed rows are
// reachable only by admin / update-any holders.
func (h *Handler) createPurchaseExecutionsTx(ctx context.Context, tx pgx.Tx, plan *config.PurchasePlan, planID string, count int, startDate time.Time, creator *string) (int, error) {
	intervalDays := plan.RampSchedule.StepIntervalDays
	if intervalDays == 0 {
		intervalDays = 7 // Default to weekly if not set
	}

	created := 0
	for i := 0; i < count; i++ {
		scheduledDate := startDate.AddDate(0, 0, i*intervalDays)

		approvalToken, err := common.GenerateApprovalToken()
		if err != nil {
			return created, fmt.Errorf("failed to generate approval token (row %d/%d): %w", created+1, count, err)
		}
		execution := &config.PurchaseExecution{
			PlanID:          planID,
			ExecutionID:     uuid.New().String(),
			Status:          "pending",
			StepNumber:      plan.RampSchedule.CurrentStep + i + 1,
			ScheduledDate:   scheduledDate,
			ApprovalToken:   approvalToken,
			CreatedByUserID: creator,
		}

		if err := h.config.SavePurchaseExecutionTx(ctx, tx, execution); err != nil {
			return created, fmt.Errorf("failed to save execution (row %d/%d): %w", created+1, count, err)
		}
		created++
	}

	return created, nil
}

// updatePlanNextExecutionDateTx bumps the plan's next_execution_date
// inside the caller's transaction so it commits atomically with the
// SavePurchaseExecutionTx writes from createPurchaseExecutionsTx.
func (h *Handler) updatePlanNextExecutionDateTx(ctx context.Context, tx pgx.Tx, plan *config.PurchasePlan, startDate time.Time) error {
	if plan.NextExecutionDate == nil || plan.NextExecutionDate.After(startDate) {
		plan.NextExecutionDate = &startDate
		plan.UpdatedAt = time.Now()
		if err := h.config.UpdatePurchasePlanTx(ctx, tx, plan); err != nil {
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

// applyPatchFields applies validated partial-update fields to a plan.
func applyPatchFields(plan *config.PurchasePlan, req PatchPlanRequest) error {
	if req.Name != nil {
		if len(*req.Name) == 0 {
			return NewClientError(400, "plan name cannot be empty")
		}
		if len(*req.Name) > 255 {
			return NewClientError(400, "plan name too long (max 255 characters)")
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
			return NewClientError(400, "notification_days_before must be between 0 and 30")
		}
		plan.NotificationDaysBefore = *req.NotificationDaysBefore
	}
	return nil
}

// patchPlan handles partial updates to a plan (PATCH method)
func (h *Handler) patchPlan(ctx context.Context, httpReq *events.LambdaFunctionURLRequest, planID string) (any, error) {
	if err := validateUUID(planID); err != nil {
		return nil, err
	}

	session, err := h.requirePermission(ctx, httpReq, "update", "plans")
	if err != nil {
		return nil, err
	}

	if err := h.requirePlanAccess(ctx, session, planID); err != nil {
		return nil, err
	}

	var req PatchPlanRequest
	if err := json.Unmarshal([]byte(httpReq.Body), &req); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Do NOT wrap err with the raw plan UUID: a non-ClientError propagates to
	// the router's logging.Errorf("API error: %v"), leaking the plan UUID and
	// the raw DB error string (issue #965, same shape as the createPlan-side
	// fix).
	plan, err := h.config.GetPurchasePlan(ctx, planID)
	if err != nil {
		return nil, mapCreatePlanStorageError(err,
			"plan not found", "failed to load plan",
			"patchPlan: GetPurchasePlan failed")
	}
	if plan == nil {
		return nil, NewClientError(404, fmt.Sprintf("plan not found: %s", planID))
	}

	if err := applyPatchFields(plan, req); err != nil {
		return nil, err
	}

	plan.UpdatedAt = time.Now()

	if err := plan.Validate(); err != nil {
		return nil, NewClientError(400, fmt.Sprintf("validation error: %s", err))
	}

	if err := h.config.UpdatePurchasePlan(ctx, plan); err != nil {
		return nil, err
	}

	return plan, nil
}
