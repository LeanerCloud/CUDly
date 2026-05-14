// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
)

func (h *Handler) getDashboardSummary(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (*DashboardSummaryResponse, error) {
	// Dashboard aggregates recommendations + purchase history. Gate on
	// view:recommendations — the closest existing resource. No dedicated
	// "dashboard" resource in the permission model.
	session, err := h.requirePermission(ctx, req, "view", "recommendations")
	if err != nil {
		return nil, err
	}

	effectiveAccountID, err := resolveDashboardAccountID(params)
	if err != nil {
		return nil, err
	}

	recommendations, err := h.scheduler.ListRecommendations(ctx, config.RecommendationFilter{
		Provider: params["provider"],
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get recommendations: %w", err)
	}

	recommendations, err = h.filterDashboardRecommendations(ctx, session, recommendations)
	if err != nil {
		return nil, err
	}

	// Issue #196 — cap the headline "potential savings" by the per-account
	// coverage target. Without this scaling the dashboard always reports
	// the un-overridden total, which is misleading once a per-account
	// override has narrowed the user's intended commitment.
	//
	// The resolver is best-effort: a lookup error logs and falls through
	// to the un-scaled total, mirroring the over-show-vs-under-show
	// trade-off baked into Scheduler.applySuppressions / applyAccountOverrides.
	coverageByKey := h.resolveCoverageByAccountKey(ctx, recommendations)

	totalSavings, byService := summarizeRecommendationsWithCoverage(recommendations, coverageByKey)
	targetCoverage := h.resolveTargetCoverage(ctx)
	activeCommitments, committedMonthly, ytdSavings := h.calculateCommitmentMetrics(ctx, effectiveAccountID)

	return &DashboardSummaryResponse{
		PotentialMonthlySavings: totalSavings,
		TotalRecommendations:    len(recommendations),
		ActiveCommitments:       activeCommitments,
		CommittedMonthly:        committedMonthly,
		CurrentCoverage:         h.calculateCurrentCoverage(totalSavings, committedMonthly),
		TargetCoverage:          targetCoverage,
		YTDSavings:              ytdSavings,
		ByService:               byService,
	}, nil
}

// resolveDashboardAccountID chooses the single account_id passed into the
// (legacy, single-account) commitment metrics calculation:
//   - one ID in `account_ids` → use it
//   - multiple IDs → empty (no filter; logged as observability)
//   - none → fall back to the legacy singular `account_id` param
func resolveDashboardAccountID(params map[string]string) (string, error) {
	accountIDs, err := parseAccountIDs(params["account_ids"])
	if err != nil {
		return "", NewClientError(400, err.Error())
	}

	switch len(accountIDs) {
	case 1:
		return accountIDs[0], nil
	case 0:
		return params["account_id"], nil
	default:
		logging.Infof("dashboard: multi-account filter requested (%d accounts); returning unfiltered metrics until per-account breakdown is implemented", len(accountIDs))
		return "", nil
	}
}

// filterDashboardRecommendations applies the session's allowed_accounts filter
// to the recommendations list before aggregation so scoped users don't see
// cross-account totals. Admin/unrestricted sessions pass through unchanged.
func (h *Handler) filterDashboardRecommendations(ctx context.Context, session *Session, recs []config.RecommendationRecord) ([]config.RecommendationRecord, error) {
	allowed, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if auth.IsUnrestrictedAccess(allowed) {
		return recs, nil
	}

	nameByID := h.resolveAccountNamesByID(ctx)
	filtered := recs[:0]
	for _, rec := range recs {
		if rec.CloudAccountID == nil {
			continue
		}
		id := *rec.CloudAccountID
		if auth.MatchesAccount(allowed, id, nameByID[id]) {
			filtered = append(filtered, rec)
		}
	}
	return filtered, nil
}

// summarizeRecommendationsWithCoverage is the dashboard reducer that
// respects per-account, per-service coverage overrides. Each rec's
// contribution is scaled by min(coverage, 100) / 100 looked up in
// coverageByKey via config.AccountConfigKey(account, provider, service).
// Pass coverageByKey=nil to disable scaling (every rec counts fully).
//
// Recs without a CloudAccountID and recs whose triple has no entry in the
// map all count at full weight — this matches the pre-#196 behaviour for
// un-configured accounts. Zero-coverage configs are excluded from the map
// by resolveCoverageByAccountKey (issue #201) so they also fall through to
// full weight rather than silently zeroing the headline.
//
// Coverage > 100 is capped at 100 so a misconfigured override cannot
// inflate the headline "potential savings" beyond the raw rec total.
//
// Note: assumes recs are generated at 100% coverage. A follow-up on
// dashboard accuracy (see #196 references) will revisit if rec
// generation becomes coverage-aware.
func summarizeRecommendationsWithCoverage(
	recs []config.RecommendationRecord,
	coverageByKey map[string]float64,
) (float64, map[string]ServiceSavings) {
	var total float64
	byService := make(map[string]ServiceSavings)
	for _, rec := range recs {
		scaled := scaledSavings(rec, coverageByKey)
		total += scaled
		svc := byService[rec.Service]
		svc.PotentialSavings += scaled
		byService[rec.Service] = svc
	}
	return total, byService
}

// scaledSavings returns rec.Savings * min(max(coverage, 0), 100) / 100 when
// a coverage entry exists for the rec's (account, provider, service) triple.
// Otherwise returns rec.Savings unchanged.
func scaledSavings(rec config.RecommendationRecord, coverageByKey map[string]float64) float64 {
	if rec.CloudAccountID == nil || coverageByKey == nil {
		return rec.Savings
	}
	coverage, ok := coverageByKey[config.AccountConfigKey(*rec.CloudAccountID, rec.Provider, rec.Service)]
	if !ok {
		return rec.Savings
	}
	if coverage <= 0 {
		return 0
	}
	if coverage >= 100 {
		return rec.Savings
	}
	return rec.Savings * coverage / 100
}

// resolveCoverageByAccountKey returns a map of AccountConfigKey -> resolved
// coverage% for every (account, provider, service) triple represented in
// recs. Lookup errors degrade gracefully to a nil map (no scaling applied
// → un-overridden behaviour).
//
// Entries with a resolved coverage of zero are omitted from the map.
// ServiceConfig.Coverage is a float64 whose zero-value means "not configured",
// so including a zero entry would silently scale that account's savings to $0
// even though the operator never set an explicit coverage cap (issue #201).
// When an entry is absent, scaledSavings falls through to full savings,
// matching the pre-#196 behaviour for un-configured accounts.
func (h *Handler) resolveCoverageByAccountKey(ctx context.Context, recs []config.RecommendationRecord) map[string]float64 {
	if len(recs) == 0 {
		return nil
	}
	resolved, err := config.ResolveAccountConfigsForRecs(ctx, h.config, recs)
	if err != nil {
		logging.Errorf("dashboard: failed to resolve per-account configs for coverage cap; using un-scaled totals: %v", err)
		return nil
	}
	if len(resolved) == 0 {
		return nil
	}
	out := make(map[string]float64, len(resolved))
	for k, cfg := range resolved {
		if cfg.Coverage == 0 {
			// Zero is the float64 zero-value, meaning "not configured".
			// Omit the entry so scaledSavings returns full savings
			// rather than silently zeroing the dashboard headline.
			continue
		}
		out[k] = cfg.Coverage
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveTargetCoverage returns the configured default coverage or 80% when
// no global config is set or the configured value is zero.
func (h *Handler) resolveTargetCoverage(ctx context.Context) float64 {
	globalCfg, _ := h.config.GetGlobalConfig(ctx)
	if globalCfg != nil && globalCfg.DefaultCoverage > 0 {
		return globalCfg.DefaultCoverage
	}
	return 80.0
}

// getUpcomingPurchases enumerates pending purchase_executions joined to
// their parent PurchasePlan for display. Mirrors the data flow in
// getPlannedPurchases (handler_purchases.go) so the dashboard widget and
// the Plans page walk the same canonical "what's about to happen" set.
//
// The widget previously enumerated plans and synthesised one row per plan
// from plan.NextExecutionDate. That was wrong because action endpoints
// (DELETE /api/purchases/planned/{id}, pause, resume, run) all target
// purchase_executions.execution_id, not purchase_plans.id; the Cancel
// button could only fire correctly when the row in front of the operator
// IS a real execution. PR #207 worked around it by routing Cancel to
// api.deletePlan(planID), but that deleted the whole plan — too
// aggressive for "skip this scheduled run". This version emits actual
// execution rows so Cancel can target just the planned purchase via
// DELETE /api/purchases/planned/{id}.
func (h *Handler) getUpcomingPurchases(ctx context.Context, req *events.LambdaFunctionURLRequest) (*UpcomingPurchaseResponse, error) {
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

	// Per-plan access cache mirrors getPlannedPurchases — all executions
	// for the same plan share the same account scope, so we resolve once
	// per plan.
	allowedPlan := make(map[string]bool)

	var upcoming []UpcomingPurchase
	for i := range executions {
		exec := executions[i]
		plan := planMap[exec.PlanID]
		if plan == nil {
			// Orphaned execution (plan was deleted but cleanup missed it).
			// Hide rather than crash; cleanup is a separate concern.
			continue
		}
		ok, err := h.isPlanAllowedCached(ctx, session, exec.PlanID, allowedPlan)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		upcoming = append(upcoming, upcomingFromExecution(plan, &exec))
	}

	return &UpcomingPurchaseResponse{Purchases: upcoming}, nil
}

// upcomingFromExecution projects a (plan, execution) pair onto the
// UpcomingPurchase response. Uses the first service entry from the plan as
// representative — the response shape doesn't support multi-service plans.
// Step number comes from the execution row directly (already stamped by the
// scheduler at instance-create time).
func upcomingFromExecution(plan *config.PurchasePlan, exec *config.PurchaseExecution) UpcomingPurchase {
	var provider, service string
	for _, svcCfg := range plan.Services {
		provider = svcCfg.Provider
		service = svcCfg.Service
		break
	}
	return UpcomingPurchase{
		ExecutionID:      exec.ExecutionID,
		PlanID:           plan.ID,
		PlanName:         plan.Name,
		ScheduledDate:    exec.ScheduledDate.Format("2006-01-02"),
		Provider:         provider,
		Service:          service,
		StepNumber:       exec.StepNumber,
		TotalSteps:       plan.RampSchedule.TotalSteps,
		EstimatedSavings: exec.EstimatedSavings,
	}
}

// planIntersectsAllowed returns true when any of the plan's associated cloud
// accounts is in the allowed list (matched by ID or display name). Returns
// false when the plan has no account rows — scoped users don't get to see
// unattributed plans.
func (h *Handler) planIntersectsAllowed(ctx context.Context, planID string, allowed []string) (bool, error) {
	accounts, err := h.config.GetPlanAccounts(ctx, planID)
	if err != nil {
		return false, fmt.Errorf("failed to get plan accounts: %w", err)
	}
	for _, acct := range accounts {
		if auth.MatchesAccount(allowed, acct.ID, acct.Name) {
			return true, nil
		}
	}
	return false, nil
}

// getPublicInfo returns public information about the CUDly instance (no auth required)
// No rate limiting — this is hit by Terraform deployment checks and the frontend on every page load.
func (h *Handler) getPublicInfo(ctx context.Context, req *events.LambdaFunctionURLRequest) (*PublicInfoResponse, error) {
	// Check if admin exists
	adminExists := false
	if h.auth != nil {
		exists, err := h.auth.CheckAdminExists(ctx)
		if err == nil {
			adminExists = exists
		}
	}

	// Build the API key secret URL for the console
	var apiKeySecretURL string
	if h.secretsARN != "" {
		// Extract region from ARN: arn:aws:secretsmanager:region:account:secret:name
		parts := strings.Split(h.secretsARN, ":")
		if len(parts) >= 4 {
			region := parts[3]
			apiKeySecretURL = fmt.Sprintf("https://%s.console.aws.amazon.com/secretsmanager/secret?name=%s&region=%s",
				region, h.secretsARN, region)
		}
	}

	return &PublicInfoResponse{
		Version:         "1.0.0",
		AdminExists:     adminExists,
		APIKeySecretURL: apiKeySecretURL,
	}, nil
}

// commitmentExpiry returns the moment a purchase's commitment term ends.
// Computed from Timestamp + Term*365d. Extracted from
// calculateCommitmentMetrics so the inventory handler can surface the
// same date in its per-row response without re-deriving the math (and
// so a future tweak to "what counts as expired" lands in exactly one
// place). One year is approximated as 365 days — matches the original
// dashboard arithmetic verbatim; leap-year precision isn't material for
// a multi-year RI/SP/CUD term.
func commitmentExpiry(p config.PurchaseHistoryRecord) time.Time {
	termDuration := time.Duration(p.Term) * 365 * 24 * time.Hour
	return p.Timestamp.Add(termDuration)
}

// isActiveCommitment reports whether the purchase's term has not yet
// expired as of `now`. The boundary is strict (After): a commitment is
// active right up to the instant its term ends. Same predicate shared
// by the dashboard aggregate and the per-commitment inventory endpoint.
func isActiveCommitment(p config.PurchaseHistoryRecord, now time.Time) bool {
	return !now.After(commitmentExpiry(p))
}

// calculateCommitmentMetrics calculates active commitments and savings from purchase history
func (h *Handler) calculateCommitmentMetrics(ctx context.Context, accountID string) (activeCommitments int, committedMonthly, ytdSavings float64) {
	// Get purchase history (last 1000 purchases should be sufficient)
	purchases, err := h.config.GetPurchaseHistory(ctx, accountID, 1000)
	if err != nil {
		// Log error but don't fail the request
		return 0, 0, 0
	}

	// Get current time from context or use now
	currentTime := time.Now()
	yearStart := time.Date(currentTime.Year(), 1, 1, 0, 0, 0, 0, time.UTC)

	for _, p := range purchases {
		if !isActiveCommitment(p, currentTime) {
			continue // Skip expired commitments
		}

		// Count active commitments
		activeCommitments++

		// Add to committed monthly (EstimatedSavings is typically monthly)
		committedMonthly += p.EstimatedSavings

		// Calculate YTD savings (savings accumulated since year start)
		if p.Timestamp.Before(yearStart) {
			// Purchase made before this year, count full year so far
			monthsSinceYearStart := int(currentTime.Sub(yearStart).Hours() / (24 * 30))
			ytdSavings += p.EstimatedSavings * float64(monthsSinceYearStart)
		} else {
			// Purchase made this year, count from purchase date
			monthsSincePurchase := int(currentTime.Sub(p.Timestamp).Hours() / (24 * 30))
			ytdSavings += p.EstimatedSavings * float64(monthsSincePurchase)
		}
	}

	return activeCommitments, committedMonthly, ytdSavings
}

// calculateCurrentCoverage calculates the current coverage percentage
func (h *Handler) calculateCurrentCoverage(potentialSavings, committedMonthly float64) float64 {
	if potentialSavings == 0 {
		return 100.0 // No recommendations means 100% coverage
	}

	totalPossible := potentialSavings + committedMonthly
	if totalPossible == 0 {
		return 0
	}

	return (committedMonthly / totalPossible) * 100
}
