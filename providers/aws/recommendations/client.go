// Package recommendations provides AWS Cost Explorer recommendations client
package recommendations

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"golang.org/x/sync/errgroup"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
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
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
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

// GetAllRecommendations fetches recommendations for all supported services.
//
// All five service calls run concurrently under errgroup. Each goroutine
// captures its own error in a closure-scoped variable and returns nil to the
// group so a single per-service failure does not cancel its siblings (matching
// the previous loop's `continue`-on-error tolerance). Results are merged in
// the canonical order EC2 → RDS → ElastiCache → OpenSearch → Redshift after
// all goroutines finish so order-sensitive consumers stay stable.
//
// Behaviour change vs the previous sequential loop: per-service errors are
// now logged at WARN via mergeServiceResults — the previous loop swallowed
// them silently with a bare `continue`, leaving operators no signal when a
// single service was misbehaving. Mirrors the Azure parallelisation in
// providers/azure/recommendations.go (closes #258, commit b10326c5).
func (c *Client) GetAllRecommendations(ctx context.Context) ([]common.Recommendation, error) {
	var (
		ec2Recs, rdsRecs, cacheRecs, osRecs, redshiftRecs []common.Recommendation
		ec2Err, rdsErr, cacheErr, osErr, redshiftErr      error
	)

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		ec2Recs, ec2Err = c.GetRecommendationsForService(gctx, common.ServiceEC2)
		return nil
	})
	g.Go(func() error {
		rdsRecs, rdsErr = c.GetRecommendationsForService(gctx, common.ServiceRDS)
		return nil
	})
	g.Go(func() error {
		cacheRecs, cacheErr = c.GetRecommendationsForService(gctx, common.ServiceElastiCache)
		return nil
	})
	g.Go(func() error {
		osRecs, osErr = c.GetRecommendationsForService(gctx, common.ServiceOpenSearch)
		return nil
	})
	g.Go(func() error {
		redshiftRecs, redshiftErr = c.GetRecommendationsForService(gctx, common.ServiceRedshift)
		return nil
	})

	// Wait for all goroutines. g.Wait() always returns nil because every
	// goroutine returns nil — errors are captured per-service above. After
	// Wait, propagate ctx cancellation so callers can distinguish "all five
	// services completed (with possibly per-service errors)" from "the
	// parent ctx was canceled mid-fan-out".
	_ = g.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return mergeServiceResults(
		serviceResult{name: "EC2", recs: ec2Recs, err: ec2Err},
		serviceResult{name: "RDS", recs: rdsRecs, err: rdsErr},
		serviceResult{name: "ElastiCache", recs: cacheRecs, err: cacheErr},
		serviceResult{name: "OpenSearch", recs: osRecs, err: osErr},
		serviceResult{name: "Redshift", recs: redshiftRecs, err: redshiftErr},
	), nil
}

// serviceResult bundles a per-service collection outcome for the deterministic
// merge in mergeServiceResults. Extracted into a helper so GetAllRecommendations
// stays under the gocyclo gate (.golangci.yml min-complexity: 15) after the
// post-Wait ctx.Err() block was added.
type serviceResult struct {
	name string
	recs []common.Recommendation
	err  error
}

// mergeServiceResults logs per-service errors at WARN and appends successful
// results in the order the slice is passed — callers must preserve the
// canonical EC2 → RDS → ElastiCache → OpenSearch → Redshift order so that
// order-sensitive consumers stay stable.
func mergeServiceResults(results ...serviceResult) []common.Recommendation {
	total := 0
	for _, r := range results {
		total += len(r.recs)
	}
	out := make([]common.Recommendation, 0, total)
	for _, r := range results {
		if r.err != nil {
			logging.Warnf("AWS %s recommendations: %v", r.name, r.err)
			continue
		}
		out = append(out, r.recs...)
	}
	return out
}
