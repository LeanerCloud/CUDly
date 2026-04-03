// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/internal/scheduler"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
)

func (h *Handler) getDashboardSummary(ctx context.Context, params map[string]string) (*DashboardSummaryResponse, error) {
	provider := params["provider"]

	// Parse account_ids (comma-separated); fall back to singular account_id for backward compat.
	accountIDs := parseAccountIDs(params["account_ids"])

	// Resolve a single account_id for calculateCommitmentMetrics:
	// - one ID supplied → use it directly
	// - multiple IDs → pass empty to return all (multi-account filtering is a future step)
	// - none supplied → fall back to legacy singular param
	effectiveAccountID := params["account_id"]
	switch len(accountIDs) {
	case 1:
		effectiveAccountID = accountIDs[0]
	case 0:
		// keep legacy value
	default:
		logging.Infof("dashboard: multi-account filter requested (%d accounts); returning unfiltered metrics until per-account breakdown is implemented", len(accountIDs))
		effectiveAccountID = ""
	}

	// Get recommendations to calculate potential savings
	queryParams := scheduler.RecommendationQueryParams{
		Provider: provider,
	}
	recommendations, err := h.scheduler.GetRecommendations(ctx, queryParams)
	if err != nil {
		return nil, fmt.Errorf("failed to get recommendations: %w", err)
	}

	// Calculate totals
	var totalSavings float64
	byService := make(map[string]ServiceSavings)

	for _, rec := range recommendations {
		totalSavings += rec.Savings

		serviceKey := rec.Service
		svc := byService[serviceKey]
		svc.PotentialSavings += rec.Savings
		byService[serviceKey] = svc
	}

	// Get global config for target coverage
	globalCfg, _ := h.config.GetGlobalConfig(ctx)
	targetCoverage := 80.0
	if globalCfg != nil && globalCfg.DefaultCoverage > 0 {
		targetCoverage = globalCfg.DefaultCoverage
	}

	// Calculate metrics from purchase history
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

func (h *Handler) getUpcomingPurchases(ctx context.Context) (*UpcomingPurchaseResponse, error) {
	// Get scheduled purchases from plans
	plans, err := h.config.ListPurchasePlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get purchase plans: %w", err)
	}

	var upcoming []UpcomingPurchase
	for _, plan := range plans {
		if !plan.Enabled || plan.NextExecutionDate == nil {
			continue
		}

		// Get first service from the Services map as representative
		var provider, service string
		for _, svcCfg := range plan.Services {
			provider = svcCfg.Provider
			service = svcCfg.Service
			break
		}

		upcoming = append(upcoming, UpcomingPurchase{
			ExecutionID:      plan.ID,
			PlanName:         plan.Name,
			ScheduledDate:    plan.NextExecutionDate.Format("2006-01-02"),
			Provider:         provider,
			Service:          service,
			StepNumber:       plan.RampSchedule.CurrentStep + 1,
			TotalSteps:       plan.RampSchedule.TotalSteps,
			EstimatedSavings: 0, // Would need to calculate from recommendations
		})
	}

	return &UpcomingPurchaseResponse{
		Purchases: upcoming,
	}, nil
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
