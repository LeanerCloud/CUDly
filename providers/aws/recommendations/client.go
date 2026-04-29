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

// defaultDiscoveryTerms enumerates the term lengths the discovery flow
// fetches per service. Cost Explorer's GetReservationPurchaseRecommendation
// requires `TermInYears` on each request and returns recs for that single
// term — there's no "give me both" mode. Issue #188 traced the
// "AWS recs only ever show Term = 3 Years" symptom to this loop having
// previously been a single hardcoded "3yr" entry, so 1yr recs never
// reached the scheduler regardless of how unique their downstream IDs
// were after PR #189.
var defaultDiscoveryTerms = []string{"1yr", "3yr"}

// defaultDiscoveryPaymentOptions enumerates the payment options the
// discovery flow fetches per (service, term). Cost Explorer's
// GetReservationPurchaseRecommendation requires a single `PaymentOption`
// per request and returns recs only for that option; the prior single
// hardcoded "partial-upfront" entry meant the Recommendations page
// could never offer the user a choice between all-upfront / partial /
// no-upfront variants. The recordID encoding (scheduler.go) and the
// recommendations natural-key index (migration 000042) both already
// include payment_option, so the three variants land as distinct DB
// rows and render as distinct UI rows for free.
var defaultDiscoveryPaymentOptions = []string{"all-upfront", "partial-upfront", "no-upfront"}

// GetRecommendationsForService fetches recommendations for a specific
// service across the full Cartesian product of defaultDiscoveryTerms ×
// defaultDiscoveryPaymentOptions (currently 2 × 3 = 6 Cost Explorer
// calls per service). Each call returns the recs for that single
// (term, payment) cell and the parser tags them with params.Term /
// params.PaymentOption so the resulting slice contains every combo
// for the user to choose from in the UI.
//
// A per-call Cost Explorer error is tolerated and skipped so a single
// throttle on one (term, payment) combo doesn't suppress the others;
// only an error where every combo fails is propagated. This mirrors
// the "continue on per-service error" tolerance in GetAllRecommendations.
func (c *Client) GetRecommendationsForService(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error) {
	allRecs := make([]common.Recommendation, 0)
	var lastErr error
	successCount := 0
	attempts := 0
	for _, term := range defaultDiscoveryTerms {
		for _, payment := range defaultDiscoveryPaymentOptions {
			attempts++
			params := common.RecommendationParams{
				Service:        service,
				PaymentOption:  payment,
				Term:           term,
				LookbackPeriod: "7d",
				Region:         "",
			}
			recs, err := c.GetRecommendations(ctx, params)
			if err != nil {
				// A canceled / deadline-exceeded ctx is NOT a per-combo
				// failure to be tolerated — every subsequent combo
				// would just hit the same dead context and waste time
				// while we accumulate "failures" that hide the real
				// reason. Short-circuit so the caller sees the ctx
				// error verbatim. Per-combo errors (throttle, 5xx)
				// keep the existing skip-and-continue tolerance.
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				lastErr = err
				continue
			}
			successCount++
			allRecs = append(allRecs, recs...)
		}
	}
	if successCount == 0 && attempts > 0 && lastErr != nil {
		return nil, fmt.Errorf("all (term, payment) variants failed for service %s: %w", service, lastErr)
	}
	return allRecs, nil
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
			// Same rationale as in GetRecommendationsForService: a
			// canceled / deadline-exceeded ctx is not a recoverable
			// per-service error — short-circuit instead of marching
			// through the remaining services and silently swallowing
			// the cancellation.
			if ctx.Err() != nil {
				return allRecommendations, ctx.Err()
			}
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
