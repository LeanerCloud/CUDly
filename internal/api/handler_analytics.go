// Package api provides the HTTP API handlers for analytics endpoints.
package api

import (
	"context"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/analytics"
	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/aws/aws-lambda-go/events"
)

// TrendsResponse is the savings-snapshot time-series for the Trends view: a
// monthly series (coverage %, committed spend, usage, realized savings) plus
// by-provider and by-service breakdowns over the requested window. Backed by
// the savings_snapshots store / materialized views (issues #1023 / #1033),
// distinct from the purchase_history-backed /history/analytics path.
type TrendsResponse struct {
	Start    string                        `json:"start"`
	End      string                        `json:"end"`
	Months   int                           `json:"months"`
	Monthly  []analytics.MonthlySummary    `json:"monthly"`
	Provider []analytics.ProviderBreakdown `json:"by_provider"`
	Service  []analytics.ServiceBreakdown  `json:"by_service"`
}

// getAnalyticsTrends handles GET /api/analytics/trends. It returns the
// historical savings-snapshot series scoped to the caller's allowed_accounts.
func (h *Handler) getAnalyticsTrends(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	if h.analyticsSnapshots == nil {
		// Mirror getHistoryAnalytics: 503 = feature intentionally unavailable.
		return nil, NewClientError(503, "analytics snapshots not configured")
	}

	accountID := params["account_id"]
	// Enforce allowed_accounts scope BEFORE resolving filter ids so a scoped
	// user can never widen to "all" (empty filters mean all-accessible).
	if err := h.validateAnalyticsAccountScope(ctx, session, accountID); err != nil {
		return nil, err
	}

	start, end, err := parseDateRange(params["start"], params["end"])
	if err != nil {
		return nil, err
	}

	months := monthsBetween(start, end)

	// Resolve the requested account (top-bar chip UUID, or "" for all-accessible
	// for an unrestricted session) into the dual-column filter inputs so rows
	// carrying only the external account_id (cloud_account_id NULL) are matched.
	accountUUIDs, accountExternalIDsByProvider := h.resolveSingleAccountFilterIDs(ctx, accountID)

	monthly, err := h.analyticsSnapshots.QueryMonthlyTotals(ctx, accountUUIDs, accountExternalIDsByProvider, months)
	if err != nil {
		return nil, fmt.Errorf("failed to query monthly totals: %w", err)
	}
	byProvider, err := h.analyticsSnapshots.QueryByProvider(ctx, accountUUIDs, accountExternalIDsByProvider, start, end)
	if err != nil {
		return nil, fmt.Errorf("failed to query provider breakdown: %w", err)
	}
	provider := params["provider"]
	byService, err := h.analyticsSnapshots.QueryByService(ctx, accountUUIDs, accountExternalIDsByProvider, provider, start, end)
	if err != nil {
		return nil, fmt.Errorf("failed to query service breakdown: %w", err)
	}

	return &TrendsResponse{
		Start:    start.Format(time.RFC3339),
		End:      end.Format(time.RFC3339),
		Months:   months,
		Monthly:  monthly,
		Provider: byProvider,
		Service:  byService,
	}, nil
}

// monthsBetween returns the inclusive count of month-buckets spanned by
// [start, end], at least 1. Used to bound the monthly_savings_summary query.
func monthsBetween(start, end time.Time) int {
	months := (end.Year()-start.Year())*12 + int(end.Month()) - int(start.Month()) + 1
	if months < 1 {
		return 1
	}
	return months
}

// AnalyticsResponse represents the response for the analytics endpoint.
type AnalyticsResponse struct {
	Start      string             `json:"start"`
	End        string             `json:"end"`
	Interval   string             `json:"interval"`
	Summary    *HistorySummary    `json:"summary"`
	DataPoints []HistoryDataPoint `json:"data_points"`
}

// BreakdownResponse represents the response for the breakdown endpoint.
type BreakdownResponse struct {
	Dimension string                    `json:"dimension"`
	Start     string                    `json:"start"`
	End       string                    `json:"end"`
	Data      map[string]BreakdownValue `json:"data"`
}

// getHistoryAnalytics handles GET /history/analytics
func (h *Handler) getHistoryAnalytics(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	// Analytics aggregate across purchase history — gate on view:purchases and
	// scope by allowed_accounts.
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	// Analytics is Postgres-backed (api.PostgresAnalyticsClient) and wired
	// in server.Application.reinitializeAfterConnect, so analyticsClient
	// is non-nil whenever the DB is up. The guard stays as defence-in-depth
	// for test builds and misconfigured callers — the frontend treats 503
	// as "feature intentionally unavailable" and renders the corresponding
	// empty-state instead of a generic error.
	if h.analyticsClient == nil {
		return nil, NewClientError(503, "analytics not configured")
	}

	// Parse parameters
	accountID := params["account_id"]
	interval := params["interval"]
	if interval == "" {
		interval = "hourly"
	}

	// For scoped users we require account_id and validate it's in their scope.
	// We don't (yet) support analytics across a subset — the underlying
	// aggregate takes a single account_id. An unrestricted/admin session
	// can pass "" to mean "all accessible accounts".
	if err := h.validateAnalyticsAccountScope(ctx, session, accountID); err != nil {
		return nil, err
	}

	// Parse date range
	start, end, err := parseDateRange(params["start"], params["end"])
	if err != nil {
		return nil, err
	}

	// Resolve the requested account (a top-bar chip UUID, or "" for all) to the
	// dual-column filter inputs so rows that carry only the external account_id
	// (cloud_account_id NULL) are aggregated (issue #701/#498/#866).
	accountUUIDs, accountExternalIDsByProvider := h.resolveSingleAccountFilterIDs(ctx, accountID)

	// Aggregate history from the analytics client (Postgres-backed).
	dataPoints, summary, err := h.analyticsClient.QueryHistory(ctx, accountUUIDs, accountExternalIDsByProvider, start, end, interval)
	if err != nil {
		return nil, fmt.Errorf("failed to query analytics: %w", err)
	}

	return &AnalyticsResponse{
		Start:      start.Format(time.RFC3339),
		End:        end.Format(time.RFC3339),
		Interval:   interval,
		Summary:    summary,
		DataPoints: dataPoints,
	}, nil
}

// getHistoryBreakdown handles GET /history/breakdown
func (h *Handler) getHistoryBreakdown(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	if h.analyticsClient == nil {
		// See getHistoryAnalytics for the 503 rationale.
		return nil, NewClientError(503, "analytics not configured")
	}

	accountID := params["account_id"]
	dimension := params["dimension"]
	if dimension == "" {
		dimension = "service"
	}

	if err := h.validateAnalyticsAccountScope(ctx, session, accountID); err != nil {
		return nil, err
	}

	start, end, err := parseDateRange(params["start"], params["end"])
	if err != nil {
		return nil, err
	}

	// Resolve account UUID -> dual-column inputs; see getHistoryAnalytics.
	accountUUIDs, accountExternalIDsByProvider := h.resolveSingleAccountFilterIDs(ctx, accountID)

	// Fetch the breakdown from the analytics client (Postgres-backed).
	data, err := h.analyticsClient.QueryBreakdown(ctx, accountUUIDs, accountExternalIDsByProvider, start, end, dimension)
	if err != nil {
		return nil, fmt.Errorf("failed to query breakdown: %w", err)
	}

	return &BreakdownResponse{
		Dimension: dimension,
		Start:     start.Format(time.RFC3339),
		End:       end.Format(time.RFC3339),
		Data:      data,
	}, nil
}

// validateAnalyticsAccountScope rejects scoped users who request analytics
// without account_id or for an account outside their allowed_accounts list.
// Admin/unrestricted sessions pass through (account_id may be empty).
func (h *Handler) validateAnalyticsAccountScope(ctx context.Context, session *Session, accountID string) error {
	allowed, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if auth.IsUnrestrictedAccess(allowed) {
		return nil
	}
	if accountID == "" {
		return NewClientError(400, "account_id is required for scoped users")
	}
	nameByID := h.resolveAccountNamesByID(ctx)
	if !auth.MatchesAccount(allowed, accountID, nameByID[accountID]) {
		return errNotFound
	}
	return nil
}

// triggerAnalyticsCollection handles POST /analytics/collect (admin only)
// This can be used to manually trigger the hourly collection.
func (h *Handler) triggerAnalyticsCollection(ctx context.Context, req *events.LambdaFunctionURLRequest, _ map[string]string) (any, error) {
	// Admin-only operation.
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}
	if h.analyticsCollector == nil {
		return nil, fmt.Errorf("analytics collector not configured")
	}

	if err := h.analyticsCollector.Collect(ctx); err != nil {
		return nil, fmt.Errorf("collection failed: %w", err)
	}

	return map[string]string{
		"status":  "success",
		"message": "Analytics collection completed",
	}, nil
}

// parseDateRange parses start and end date strings with defaults.
func parseDateRange(startStr, endStr string) (time.Time, time.Time, error) {
	var start, end time.Time
	var err error

	// Default end to now
	if endStr == "" {
		end = time.Now().UTC()
	} else {
		end, err = time.Parse(time.RFC3339, endStr)
		if err != nil {
			// Try date-only format
			end, err = time.Parse("2006-01-02", endStr)
			if err != nil {
				return time.Time{}, time.Time{}, fmt.Errorf("invalid end date format")
			}
			// Set to end of day
			end = end.Add(24*time.Hour - time.Second)
		}
	}

	// Default start to 7 days ago
	if startStr == "" {
		start = end.AddDate(0, 0, -7)
	} else {
		start, err = time.Parse(time.RFC3339, startStr)
		if err != nil {
			// Try date-only format
			start, err = time.Parse("2006-01-02", startStr)
			if err != nil {
				return time.Time{}, time.Time{}, fmt.Errorf("invalid start date format")
			}
		}
	}

	// Validate range order.
	if start.After(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("start date must be before end date")
	}

	// Cap the range to at most 366 days to prevent full-table-scan DoS via
	// start=1970-01-01&end=2100-12-31 (issue #414).
	const maxRangeDays = 366
	if end.Sub(start) > maxRangeDays*24*time.Hour {
		return time.Time{}, time.Time{}, NewClientError(400,
			fmt.Sprintf("date range too large: maximum allowed range is %d days", maxRangeDays))
	}

	return start, end, nil
}
