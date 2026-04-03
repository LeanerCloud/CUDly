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

	// Parse account_ids (comma-separated). RecommendationQueryParams does not yet support
	// account-level filtering; the parameter is accepted here so the frontend receives a clean
	// response rather than an error. Per-account filtering will be wired in a future step.
	if _, err := parseAccountIDs(params["account_ids"]); err != nil {
		return nil, NewClientError(400, err.Error())
	}

	// Build query params from request parameters
	queryParams := scheduler.RecommendationQueryParams{
		Provider: params["provider"],
		Service:  params["service"],
		Region:   params["region"],
	}

	// Fetch recommendations from scheduler (which fetches from cloud providers)
	recommendations, err := h.scheduler.GetRecommendations(ctx, queryParams)
	if err != nil {
		return nil, fmt.Errorf("failed to get recommendations: %w", err)
	}

	// Calculate summary
	var totalSavings float64
	for _, rec := range recommendations {
		totalSavings += rec.Savings
	}

	return &RecommendationsResponse{
		Recommendations: recommendations,
		TotalSavings:    totalSavings,
		Count:           len(recommendations),
	}, nil
}
