// Package recommendations provides AWS Cost Explorer recommendations client
package recommendations

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"golang.org/x/sync/errgroup"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/concurrency"
	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// maxRecommendationPages caps the number of pages fetched per Cost Explorer
// GetReservationPurchaseRecommendation or GetSavingsPlansPurchaseRecommendation
// call. 20 pages x ~100 items/page = ~2000 items, enough headroom for any
// payer org we have seen. Exceeding the cap returns a diagnostic error (issue #692).
const maxRecommendationPages = 20

// CostExplorerAPI defines the interface for Cost Explorer operations
type CostExplorerAPI interface {
	GetReservationPurchaseRecommendation(ctx context.Context, params *costexplorer.GetReservationPurchaseRecommendationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationPurchaseRecommendationOutput, error)
	GetSavingsPlansPurchaseRecommendation(ctx context.Context, params *costexplorer.GetSavingsPlansPurchaseRecommendationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansPurchaseRecommendationOutput, error)
	GetReservationUtilization(ctx context.Context, params *costexplorer.GetReservationUtilizationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationUtilizationOutput, error)
	GetReservationCoverage(ctx context.Context, params *costexplorer.GetReservationCoverageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationCoverageOutput, error)
	GetSavingsPlansCoverage(ctx context.Context, params *costexplorer.GetSavingsPlansCoverageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansCoverageOutput, error)
	GetSavingsPlansUtilization(ctx context.Context, params *costexplorer.GetSavingsPlansUtilizationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetSavingsPlansUtilizationOutput, error)
	// GetCostAndUsage fetches cost and usage data for arbitrary time periods
	// and granularities. Added for the daily on-demand series adapter that
	// powers GetUsageBaseline (L2).
	GetCostAndUsage(ctx context.Context, params *costexplorer.GetCostAndUsageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error)
}

// Client wraps the AWS Cost Explorer client for RI recommendations
type Client struct {
	costExplorerClient CostExplorerAPI
	region             string
	rateLimiter        *RateLimiter

	// ec2API is the EC2 client used to build the DescribeInstanceTypes paginator.
	// Populated by NewClient from aws.Config; nil when created via NewClientWithAPI.
	ec2API DescribeInstanceTypesAPI

	// instanceTypePagerFactory creates a new InstanceTypePager on demand.
	// Set by NewClient to wrap ec2API; overridable via SetInstanceTypePagerFactory
	// for hermetic tests. When nil, instanceTypeLookup returns (0,0).
	instanceTypePagerFactory func() InstanceTypePager

	// skuCatalog caches the per-instance-type vCPU/memory catalogue, fetched
	// lazily once per Client lifetime via sync.Once (one DescribeInstanceTypes
	// fan-out per scheduler tick).
	skuCatalog skuCatalog

	// recLookbackPeriod is forwarded to GetReservationPurchaseRecommendation
	// as LookbackPeriodInDays. Defaults to "7d" when empty.
	recLookbackPeriod string
}

// NewClient creates a new recommendations client
func NewClient(cfg aws.Config) *Client {
	// Force Cost Explorer to use us-east-1 with explicit endpoint
	ceConfig := cfg.Copy()
	ceConfig.Region = "us-east-1"
	ceConfig.BaseEndpoint = aws.String("https://ce.us-east-1.amazonaws.com")

	ec2Client := awsec2.NewFromConfig(cfg)
	return &Client{
		costExplorerClient: costexplorer.NewFromConfig(ceConfig),
		region:             cfg.Region,
		rateLimiter:        NewRateLimiter(),
		ec2API:             ec2Client,
		// Factory wraps the EC2 client so the paginator is created lazily
		// on the first EC2 recommendation parse (not at construction time).
		instanceTypePagerFactory: func() InstanceTypePager {
			return awsec2.NewDescribeInstanceTypesPaginator(ec2Client, &awsec2.DescribeInstanceTypesInput{})
		},
	}
}

// NewClientWithAPI creates a new recommendations client with a custom Cost Explorer API (for testing)
func NewClientWithAPI(api CostExplorerAPI, region string) *Client {
	return &Client{
		costExplorerClient: api,
		region:             region,
		rateLimiter:        NewRateLimiter(),
		// ec2API left nil: instanceTypeLookup falls back to VCPU=0/MemoryGB=0
		// unless the caller sets instanceTypePagerFactory.
	}
}

// SetInstanceTypePagerFactory injects a pager factory for the instance-type
// SKU catalogue. Must be called before the first GetRecommendations call.
// Intended for tests that need to verify the one-fetch-per-lifetime invariant
// without hitting AWS.
func (c *Client) SetInstanceTypePagerFactory(f func() InstanceTypePager) {
	c.instanceTypePagerFactory = f
}

// instanceTypeLookup returns the cached SKU entry for instanceType.
// On the first call the catalogue is built by calling the pager factory.
// ok=false when no factory is configured, the catalogue fetch failed, or
// the instance type was not in the catalogue -- the caller falls back to
// VCPU=0/MemoryGB=0 (graceful-degradation contract from Azure PR #810).
func (c *Client) instanceTypeLookup(ctx context.Context, instanceType string) (instanceTypeSKUEntry, bool) {
	if c.instanceTypePagerFactory == nil {
		return instanceTypeSKUEntry{}, false
	}
	return c.skuCatalog.lookup(ctx, instanceType, c.instanceTypePagerFactory)
}

// SetRecLookbackPeriod configures the LookbackPeriodInDays used by
// GetRecommendationsForService. Valid values: "7d", "30d", "60d".
// An empty or unrecognised value falls back to "7d" at call time.
func (c *Client) SetRecLookbackPeriod(period string) {
	c.recLookbackPeriod = period
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

	allRecs, err := c.fetchRIAllPages(ctx, input, params.Service)
	if err != nil {
		return nil, err
	}

	return c.parseRecommendations(ctx, allRecs, params)
}

// fetchRIAllPages paginates over all pages of RI recommendations for a single
// (service, term, payment) combination. ctx.Err() is checked at the top of
// each iteration so cancellation is terminal (per feedback_ctx_cancel_terminal.md,
// issue #692).
func (c *Client) fetchRIAllPages(
	ctx context.Context,
	input *costexplorer.GetReservationPurchaseRecommendationInput,
	service common.ServiceType,
) ([]types.ReservationPurchaseRecommendation, error) {
	var allRecs []types.ReservationPurchaseRecommendation
	var nextPageToken *string

	for pageIdx := 0; ; pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pageIdx >= maxRecommendationPages {
			return nil, fmt.Errorf(
				"pagination cap reached after %d pages for RI %s (issue #692)",
				maxRecommendationPages, service,
			)
		}
		input.NextPageToken = nextPageToken

		result, err := c.fetchRIPageWithRetry(ctx, input)
		if err != nil {
			return nil, err
		}

		allRecs = append(allRecs, result.Recommendations...)

		if result.NextPageToken == nil || aws.ToString(result.NextPageToken) == "" {
			break
		}
		nextPageToken = result.NextPageToken
	}

	return allRecs, nil
}

// fetchRIPageWithRetry executes a single GetReservationPurchaseRecommendation
// call with rate-limiter exponential back-off. Extracted so the pagination loop
// in fetchRIAllPages stays below the gocyclo cap.
func (c *Client) fetchRIPageWithRetry(
	ctx context.Context,
	input *costexplorer.GetReservationPurchaseRecommendationInput,
) (*costexplorer.GetReservationPurchaseRecommendationOutput, error) {
	c.rateLimiter.Reset()
	var result *costexplorer.GetReservationPurchaseRecommendationOutput
	var err error

	for {
		if waitErr := c.rateLimiter.Wait(ctx); waitErr != nil {
			return nil, fmt.Errorf("rate limiter wait failed: %w", waitErr)
		}

		if acqErr := concurrency.Acquire(ctx); acqErr != nil {
			return nil, fmt.Errorf("concurrency acquire failed: %w", acqErr)
		}
		result, err = c.costExplorerClient.GetReservationPurchaseRecommendation(ctx, input)
		concurrency.Release(ctx)
		if !c.rateLimiter.ShouldRetry(err) {
			break
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get RI recommendations after %d retries: %w", c.rateLimiter.GetRetryCount(), err)
	}

	return result, nil
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
			lookback := c.recLookbackPeriod
			if lookback == "" {
				lookback = "7d"
			}
			params := common.RecommendationParams{
				Service:        service,
				PaymentOption:  payment,
				Term:           term,
				LookbackPeriod: lookback,
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
	// Enrich each rec with 7-day daily coverage history so the frontend
	// can render a per-row sparkline (closes #239 Part 1). SavingsPlans
	// are skipped inside AttachDailyUsageHistory (no per-SKU CE coverage
	// breakdown available). Errors are logged and skipped per-tuple so a
	// single CE failure doesn't suppress the rest of the collection.
	if len(allRecs) > 0 {
		c.AttachDailyUsageHistory(ctx, allRecs)
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
		spRecs                                            []common.Recommendation
		spErr                                             error
	)

	g, gctx := errgroup.WithContext(ctx)

	// Per-service goroutines launch the inner (term, payment) sweep for each
	// AWS service. They do NOT acquire the shared semaphore at this level —
	// each service sweep makes 6 inner Cost Explorer calls (2 terms × 3
	// payment options) plus retries with rate-limiter backoff, so holding a
	// permit across the whole sweep would tie up a slot during waits when
	// no request is actually in flight. The Acquire/Release boundary lives
	// inside GetRecommendations (around the individual CE SDK call), which
	// frees slots during backoffs and gives the cap its full effective
	// throughput. The SP goroutine expands further to 24 calls (2 terms × 3
	// payment options × 4 plan types) internally via planTypesForParams.
	// See pkg/concurrency.
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
	g.Go(func() error {
		spRecs, spErr = c.GetRecommendationsForService(gctx, common.ServiceSavingsPlans)
		return nil
	})

	// Wait for all goroutines. g.Wait() always returns nil because every
	// goroutine returns nil — errors are captured per-service above. After
	// Wait, propagate ctx cancellation so callers can distinguish "all six
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
		serviceResult{name: "SavingsPlans", recs: spRecs, err: spErr},
	)
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
// canonical EC2 → RDS → ElastiCache → OpenSearch → Redshift → SavingsPlans
// order so that order-sensitive consumers stay stable.
//
// Partial failure is tolerated: as long as at least one service succeeded, the
// successful services' recommendations are returned with a nil error and the
// failures are logged at WARN. But when EVERY service errored (e.g. a sustained
// Cost Explorer throttle that exhausts each service's per-combo retries), the
// merge returns a wrapped error instead of an empty-but-nil-error result
// (08-H4). Returning (recs, nil) on a total failure makes a throttled run
// indistinguishable from "no savings available", which an operator can misread
// as "nothing to buy": the same hazard the per-service all-combos-failed guard
// in GetRecommendationsForService prevents one level down.
func mergeServiceResults(results ...serviceResult) ([]common.Recommendation, error) {
	total := 0
	failures := 0
	var lastErr error
	for _, r := range results {
		total += len(r.recs)
		if r.err != nil {
			failures++
			lastErr = r.err
		}
	}
	out := make([]common.Recommendation, 0, total)
	for _, r := range results {
		if r.err != nil {
			logging.Warnf("AWS %s recommendations: %v", r.name, r.err)
			continue
		}
		out = append(out, r.recs...)
	}
	if failures == len(results) && failures > 0 {
		return nil, fmt.Errorf("all %d AWS recommendation services failed: %w", failures, lastErr)
	}
	return out, nil
}
