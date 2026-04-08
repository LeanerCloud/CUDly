// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/scheduler"
)

// Recommendations handlers
func (h *Handler) getRecommendations(ctx context.Context, params map[string]string) (*RecommendationsResponse, error) {
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

	// Calculate summary and collect unique regions
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
	}, nil
}
