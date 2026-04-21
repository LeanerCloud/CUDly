// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
)

// Recommendations handlers
func (h *Handler) getRecommendations(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (*RecommendationsResponse, error) {
	// Require view:recommendations permission
	session, err := h.requirePermission(ctx, req, "view", "recommendations")
	if err != nil {
		return nil, err
	}

	// Validate input parameters to prevent injection attacks
	if err := validateProvider(params["provider"]); err != nil {
		return nil, err
	}
	if err := validateServiceName(params["service"]); err != nil {
		return nil, err
	}
	if err := validateRegion(params["region"]); err != nil {
		return nil, err
	}

	accountIDs, err := parseAccountIDs(params["account_ids"])
	if err != nil {
		return nil, NewClientError(400, err.Error())
	}

	// Translate query string → DB-level filter. ListRecommendations
	// pushes these into the WHERE clause so the cache does the pruning.
	filter := config.RecommendationFilter{
		Provider:   params["provider"],
		Service:    params["service"],
		Region:     params["region"],
		AccountIDs: accountIDs,
	}

	recommendations, err := h.scheduler.ListRecommendations(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to get recommendations: %w", err)
	}

	// Filter by allowed accounts if the user has restricted access
	recommendations, err = h.filterRecommendationsByAllowedAccounts(ctx, session, recommendations)
	if err != nil {
		return nil, err
	}

	return buildRecommendationsResponse(recommendations), nil
}

// filterRecommendationsByAllowedAccounts filters recommendations to only include
// those belonging to accounts the user is allowed to access. Returns the
// unmodified slice when the user has unrestricted access (empty allowed list).
func (h *Handler) filterRecommendationsByAllowedAccounts(ctx context.Context, session *Session, recs []config.RecommendationRecord) ([]config.RecommendationRecord, error) {
	allowedAccounts, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if auth.IsUnrestrictedAccess(allowedAccounts) {
		return recs, nil
	}

	// Resolve account names so the allowed list can match on either ID or
	// display name. Recommendations only carry CloudAccountID, so the name
	// lookup needs a one-time fetch of the account list.
	accounts, err := h.config.ListCloudAccounts(ctx, config.CloudAccountFilter{})
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts for filter: %w", err)
	}
	nameByID := make(map[string]string, len(accounts))
	for _, a := range accounts {
		nameByID[a.ID] = a.Name
	}

	filtered := recs[:0]
	for _, rec := range recs {
		if rec.CloudAccountID == nil {
			continue
		}
		id := *rec.CloudAccountID
		if auth.MatchesAccount(allowedAccounts, id, nameByID[id]) {
			filtered = append(filtered, rec)
		}
	}
	return filtered, nil
}

// buildRecommendationsResponse calculates summary statistics and collects
// unique regions from a set of recommendations.
func buildRecommendationsResponse(recommendations []config.RecommendationRecord) *RecommendationsResponse {
	regionSet := make(map[string]struct{})
	var totalSavings, totalUpfront float64
	for _, rec := range recommendations {
		totalSavings += rec.Savings
		totalUpfront += rec.UpfrontCost
		if rec.Region != "" {
			regionSet[rec.Region] = struct{}{}
		}
	}

	regions := make([]string, 0, len(regionSet))
	for r := range regionSet {
		regions = append(regions, r)
	}

	var avgPayback float64
	if totalSavings > 0 {
		avgPayback = totalUpfront / totalSavings
	}

	return &RecommendationsResponse{
		Recommendations: recommendations,
		Summary: RecommendationsSummary{
			TotalCount:          len(recommendations),
			TotalMonthlySavings: totalSavings,
			TotalUpfrontCost:    totalUpfront,
			AvgPaybackMonths:    avgPayback,
		},
		Regions: regions,
	}
}

// getRecommendationsFreshness returns the cache-freshness state (last
// successful collection timestamp + most recent collection error) so the
// frontend can render a "Data from <N> min ago" indicator and surface a
// warning banner when the last collect was partial or failed.
//
// Gated by view:recommendations permission inside the handler. The route
// itself inherits AuthAdmin (the router default) so the permission check
// is currently defense-in-depth, matching the pattern used by other
// scoped handlers in this package.
func (h *Handler) getRecommendationsFreshness(ctx context.Context, req *events.LambdaFunctionURLRequest) (*config.RecommendationsFreshness, error) {
	if _, err := h.requirePermission(ctx, req, "view", "recommendations"); err != nil {
		return nil, err
	}
	freshness, err := h.config.GetRecommendationsFreshness(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get recommendations freshness: %w", err)
	}
	return freshness, nil
}
