// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
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

	// Build query params from request parameters
	queryParams := scheduler.RecommendationQueryParams{
		Provider:   params["provider"],
		Service:    params["service"],
		Region:     params["region"],
		AccountIDs: accountIDs,
	}

	// Fetch recommendations from scheduler (which fetches from cloud providers)
	recommendations, err := h.scheduler.GetRecommendations(ctx, queryParams)
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
	if len(allowedAccounts) == 0 {
		return recs, nil
	}

	allowed := make(map[string]struct{}, len(allowedAccounts))
	for _, id := range allowedAccounts {
		allowed[id] = struct{}{}
	}
	filtered := recs[:0]
	for _, rec := range recs {
		if rec.CloudAccountID != nil {
			if _, ok := allowed[*rec.CloudAccountID]; ok {
				filtered = append(filtered, rec)
			}
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
