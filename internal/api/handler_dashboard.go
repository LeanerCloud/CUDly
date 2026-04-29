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
// Recs without a CloudAccountID, recs whose triple has no entry in the
// map, and recs with a recorded coverage of zero (treated as "no
// scaling configured") all count at full weight — this matches the
// pre-#196 behaviour for un-configured accounts.
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

// scaledSavings returns rec.Savings * min(coverage, 100) / 100 when a
// non-zero coverage entry exists for the rec's (account, provider, service)
// triple. Otherwise returns rec.Savings unchanged.
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
}

// resolveCoverageByAccountKey returns a map of AccountConfigKey -> resolved
// coverage% for every (account, provider, service) triple represented in
// recs. Lookup errors degrade gracefully to a nil map (no scaling applied
// → un-overridden behaviour).
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
		out[k] = cfg.Coverage
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

func (h *Handler) getUpcomingPurchases(ctx context.Context, req *events.LambdaFunctionURLRequest) (*UpcomingPurchaseResponse, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	plans, err := h.config.ListPurchasePlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get purchase plans: %w", err)
	}

	allowed, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("failed to get allowed accounts: %w", err)
	}

	var upcoming []UpcomingPurchase
	for _, plan := range plans {
		include, err := h.includeUpcomingPlan(ctx, plan, allowed)
		if err != nil {
			return nil, err
		}
		if !include {
			continue
		}
		upcoming = append(upcoming, upcomingFromPlan(plan))
	}

	return &UpcomingPurchaseResponse{Purchases: upcoming}, nil
}

// includeUpcomingPlan returns true when the plan should appear in the
// upcoming-purchases list for the session's allowed_accounts. Skips disabled
// plans and plans with no next execution date. Admin/unrestricted sessions
// pass through without the per-plan account lookup.
//
// Plans with no account assignments are hidden from scoped users — we can't
// attribute them to a specific account, so the safe default is "don't leak".
func (h *Handler) includeUpcomingPlan(ctx context.Context, plan config.PurchasePlan, allowed []string) (bool, error) {
	if !plan.Enabled || plan.NextExecutionDate == nil {
		return false, nil
	}
	if auth.IsUnrestrictedAccess(allowed) {
		return true, nil
	}
	return h.planIntersectsAllowed(ctx, plan.ID, allowed)
}

// upcomingFromPlan projects a PurchasePlan onto the UpcomingPurchase response
// type. Uses the first service as representative — the response shape doesn't
// support multi-service plans.
func upcomingFromPlan(plan config.PurchasePlan) UpcomingPurchase {
	var provider, service string
	for _, svcCfg := range plan.Services {
		provider = svcCfg.Provider
		service = svcCfg.Service
		break
	}
	return UpcomingPurchase{
		ExecutionID:      plan.ID,
		PlanName:         plan.Name,
		ScheduledDate:    plan.NextExecutionDate.Format("2006-01-02"),
		Provider:         provider,
		Service:          service,
		StepNumber:       plan.RampSchedule.CurrentStep + 1,
		TotalSteps:       plan.RampSchedule.TotalSteps,
		EstimatedSavings: 0, // Would need to calculate from recommendations
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
		// Check if purchase is still active (within term)
		termYears := time.Duration(p.Term) * 365 * 24 * time.Hour
		expiryTime := p.Timestamp.Add(termYears)

		if currentTime.After(expiryTime) {
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
