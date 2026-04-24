// Package api provides the HTTP API handlers for analytics endpoints.
package api

import (
	"context"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/aws/aws-lambda-go/events"
)

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

	// Analytics requires an S3/Athena backend. When the deployment
	// hasn't wired one up, return 503 Service Unavailable with a
	// structured reason rather than a generic 500 — the frontend
	// treats 503 as "feature intentionally unavailable" and renders
	// the "Configure analytics (S3/Athena) to see the trend"
	// empty-state instead of a generic "savings trend failed" error.
	if h.analyticsClient == nil {
		return nil, NewClientError(503, "analytics not configured — S3/Athena backend required")
	}

	// Parse parameters
	accountID := params["account_id"]
	interval := params["interval"]
	if interval == "" {
		interval = "hourly"
	}

	// For scoped users we require account_id and validate it's in their scope.
	// We don't (yet) support analytics across a subset — the Athena query takes
	// a single account_id. An unrestricted/admin session can pass "" for all.
	if err := h.validateAnalyticsAccountScope(ctx, session, accountID); err != nil {
		return nil, err
	}

	// Parse date range
	start, end, err := parseDateRange(params["start"], params["end"])
	if err != nil {
		return nil, err
	}

	// Query Athena for historical data
	dataPoints, summary, err := h.analyticsClient.QueryHistory(ctx, accountID, start, end, interval)
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
		return nil, NewClientError(503, "analytics not configured — S3/Athena backend required")
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

	// Query Athena for breakdown data
	data, err := h.analyticsClient.QueryBreakdown(ctx, accountID, start, end, dimension)
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

	// Validate range
	if start.After(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("start date must be before end date")
	}

	return start, end, nil
}
