// Package recommendations provides AWS Cost Explorer recommendations client
package recommendations

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// CostExplorerAPI defines the interface for Cost Explorer operations
type CostExplorerAPI interface {
	GetReservationPurchaseRecommendation(ctx context.Context, params *costexplorer.GetReservationPurchaseRecommendationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationPurchaseRecommendationOutput, error)
	GetSavingsPlansPurchaseRecommendation(ctx context.Context, params *costexplorer.GetSavingsPlansPurchaseRecommendationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansPurchaseRecommendationOutput, error)
	GetReservationUtilization(ctx context.Context, params *costexplorer.GetReservationUtilizationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationUtilizationOutput, error)
}

// Client wraps the AWS Cost Explorer client for RI recommendations
type Client struct {
	costExplorerClient CostExplorerAPI
	region             string
	rateLimiter        *RateLimiter
}

// NewClient creates a new recommendations client
func NewClient(cfg aws.Config) *Client {
	// Force Cost Explorer to use us-east-1 with explicit endpoint
	ceConfig := cfg.Copy()
	ceConfig.Region = "us-east-1"
	ceConfig.BaseEndpoint = aws.String("https://ce.us-east-1.amazonaws.com")

	return &Client{
		costExplorerClient: costexplorer.NewFromConfig(ceConfig),
		region:             cfg.Region,
		rateLimiter:        NewRateLimiter(),
	}
}

// NewClientWithAPI creates a new recommendations client with a custom Cost Explorer API (for testing)
func NewClientWithAPI(api CostExplorerAPI, region string) *Client {
	return &Client{
		costExplorerClient: api,
		region:             region,
		rateLimiter:        NewRateLimiter(),
	}
}

// GetRecommendations fetches Reserved Instance recommendations for any service
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	// Handle Savings Plans separately — they use a different Cost Explorer API
	// (GetSavingsPlansPurchaseRecommendation, not GetReservationPurchaseRecommendation).
	// Match any SP slug — the legacy umbrella plus the four per-plan-type slugs —
	// via the IsSavingsPlan family predicate so the dispatch keeps working as
	// callers migrate.
	if common.IsSavingsPlan(params.Service) {
		return c.getSavingsPlansRecommendations(ctx, params)
	}

	input := &costexplorer.GetReservationPurchaseRecommendationInput{
		Service:              aws.String(getServiceStringForCostExplorer(params.Service)),
		PaymentOption:        convertPaymentOption(params.PaymentOption),
		TermInYears:          convertTermInYears(params.Term),
		LookbackPeriodInDays: convertLookbackPeriod(params.LookbackPeriod),
		AccountScope:         types.AccountScopeLinked,
	}

	// Implement rate limiting with exponential backoff
	var result *costexplorer.GetReservationPurchaseRecommendationOutput
	var err error

	c.rateLimiter.Reset()
	for {
		if waitErr := c.rateLimiter.Wait(ctx); waitErr != nil {
			return nil, fmt.Errorf("rate limiter wait failed: %w", waitErr)
		}

		result, err = c.costExplorerClient.GetReservationPurchaseRecommendation(ctx, input)
		if !c.rateLimiter.ShouldRetry(err) {
			break
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get RI recommendations after %d retries: %w", c.rateLimiter.GetRetryCount(), err)
	}

	return c.parseRecommendations(result.Recommendations, params)
}

// GetRecommendationsForService fetches recommendations for a specific service (for discovery)
func (c *Client) GetRecommendationsForService(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error) {
	params := common.RecommendationParams{
		Service:        service,
		PaymentOption:  "partial-upfront",
		Term:           "3yr",
		LookbackPeriod: "7d",
		Region:         "",
	}

	return c.GetRecommendations(ctx, params)
}

// GetAllRecommendations fetches recommendations for all supported services
func (c *Client) GetAllRecommendations(ctx context.Context) ([]common.Recommendation, error) {
	services := []common.ServiceType{
		common.ServiceEC2,
		common.ServiceRDS,
		common.ServiceElastiCache,
		common.ServiceOpenSearch,
		common.ServiceRedshift,
	}

	allRecommendations := make([]common.Recommendation, 0)

	for _, service := range services {
		recs, err := c.GetRecommendationsForService(ctx, service)
		if err != nil {
			continue
		}
		allRecommendations = append(allRecommendations, recs...)
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return allRecommendations, ctx.Err()
		}
	}

	return allRecommendations, nil
}
