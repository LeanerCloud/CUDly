// Package azure provides Azure recommendations client
package azure

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/advisor/armadvisor"
	"golang.org/x/sync/errgroup"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/concurrency"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	azrecs "github.com/LeanerCloud/CUDly/providers/azure/internal/recommendations"
	"github.com/LeanerCloud/CUDly/providers/azure/services/cache"
	"github.com/LeanerCloud/CUDly/providers/azure/services/compute"
	"github.com/LeanerCloud/CUDly/providers/azure/services/cosmosdb"
	"github.com/LeanerCloud/CUDly/providers/azure/services/database"
	"github.com/LeanerCloud/CUDly/providers/azure/services/savingsplans"
)

// RecommendationsClientAdapter aggregates Azure reservation recommendations across all services.
//
// Invariant: subscriptionID must be non-empty. Downstream converters use it as
// the Recommendation.Account field; an empty subscriptionID would silently
// produce Account="" recommendations that downstream consumers (account-scoped
// caches, UI filters, billing reports) can't route. The canonical construction
// path is NewRecommendationsClientAdapter; direct struct literals bypass the
// invariant check and should be confined to tests that deliberately exercise
// the unvalidated shape.
type RecommendationsClientAdapter struct {
	cred           azcore.TokenCredential
	subscriptionID string
}

// NewRecommendationsClientAdapter builds a RecommendationsClientAdapter with
// the subscriptionID-non-empty invariant enforced. Returns an error when
// subscriptionID is the empty string so the caller sees the mis-wiring at
// construction time rather than via confusing Account="" rows later.
func NewRecommendationsClientAdapter(cred azcore.TokenCredential, subscriptionID string) (*RecommendationsClientAdapter, error) {
	if subscriptionID == "" {
		return nil, fmt.Errorf("azure recommendations: subscriptionID is required")
	}
	return &RecommendationsClientAdapter{
		cred:           cred,
		subscriptionID: subscriptionID,
	}, nil
}

// GetRecommendations retrieves all Azure reservation recommendations across services.
//
// The Azure Consumption Reservation Recommendations API is subscription-scoped:
// the response covers every region in one call. Iterating regions and calling each
// service per region (the previous behaviour) produced ~60× duplicate results,
// hammered the rate limit, and meant downstream consumers had to deduplicate.
// We now call each service client exactly once. Region is intentionally left
// blank on the client — converters must populate Region from the response data
// (see known_issues/10_azure_provider.md CRITICAL "Recommendation converters
// ignore the API response entirely" for the matching converter work).
//
// All six service calls run concurrently under errgroup. Each goroutine captures
// its own error and returns nil to the group so that a single service failure
// does not cancel sibling calls. Results are appended in a deterministic order
// (compute → database → cache → cosmosdb → savingsplans → advisor) after all goroutines finish.
func (r *RecommendationsClientAdapter) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	var (
		computeRecs, dbRecs, cacheRecs, cosmosRecs, advisorRecs, spRecs []common.Recommendation
		computeErr, dbErr, cacheErr, cosmosErr, advisorErr, spErr       error
	)

	g, gctx := errgroup.WithContext(ctx)

	// Each per-service goroutine is a leaf — it issues the actual ARM /
	// pricing-API call. The shared semaphore on gctx (set by the scheduler)
	// bounds aggregate concurrent IO across the whole recommendations-
	// collection fan-out tree at CUDLY_MAX_PARALLELISM (default 20). If no
	// semaphore is attached (CLI tools, unit tests), Acquire/Release are
	// no-ops. See pkg/concurrency.
	//
	// goService extracts the Acquire/Release boilerplate so each per-
	// service block stays a single g.Go call — keeps GetRecommendations
	// under the project's gocyclo gate (the per-service `if Acquire {…}`
	// branches counted toward this function's cyclomatic complexity
	// before extraction).
	goService := func(errOut *error, fn func()) {
		g.Go(func() error {
			if err := concurrency.Acquire(gctx); err != nil {
				*errOut = err
				return nil
			}
			defer concurrency.Release(gctx)
			fn()
			return nil // error isolation: never propagate to errgroup
		})
	}

	// Record which services the params filter lets through so the merge can
	// distinguish "skipped by filter" from "attempted and succeeded with zero
	// recommendations" when applying the all-attempted-failed guard.
	includeCompute := shouldIncludeService(params, common.ServiceCompute)
	includeDB := shouldIncludeService(params, common.ServiceRelationalDB)
	includeCache := shouldIncludeService(params, common.ServiceCache)
	includeCosmos := shouldIncludeService(params, common.ServiceNoSQL)
	includeSP := shouldIncludeService(params, common.ServiceSavingsPlans)

	// Compute (VM) recommendations — subscription-wide.
	if includeCompute {
		goService(&computeErr, func() {
			computeClient := compute.NewClient(r.cred, r.subscriptionID, "")
			computeRecs, computeErr = computeClient.GetRecommendations(gctx, params)
		})
	}

	// Database (SQL) recommendations — subscription-wide.
	if includeDB {
		goService(&dbErr, func() {
			dbClient := database.NewClient(r.cred, r.subscriptionID, "")
			dbRecs, dbErr = dbClient.GetRecommendations(gctx, params)
		})
	}

	// Cache (Redis) recommendations — subscription-wide.
	if includeCache {
		goService(&cacheErr, func() {
			cacheClient := cache.NewClient(r.cred, r.subscriptionID, "")
			cacheRecs, cacheErr = cacheClient.GetRecommendations(gctx, params)
		})
	}

	// CosmosDB (NoSQL) recommendations — subscription-wide.
	if includeCosmos {
		goService(&cosmosErr, func() {
			cosmosClient := cosmosdb.NewClient(r.cred, r.subscriptionID, "")
			cosmosRecs, cosmosErr = cosmosClient.GetRecommendations(gctx, params)
		})
	}

	// Savings Plans — Azure has no stable public API for SP purchase
	// recommendations (Benefits Recommendations API is still in preview).
	// The call returns an empty slice so the service appears in the fan-out
	// and will start returning data once the API stabilizes without requiring
	// a scheduler change.
	if includeSP {
		goService(&spErr, func() {
			spClient := savingsplans.NewClient(r.cred, r.subscriptionID, "")
			spRecs, spErr = spClient.GetRecommendations(gctx, params)
		})
	}

	// Azure Advisor adds cross-cutting cost recommendations independent of the
	// per-service Reservation API. Failures here are non-fatal — the per-service
	// results above are still useful on their own.
	goService(&advisorErr, func() {
		advisorRecs, advisorErr = r.getAdvisorRecommendations(gctx, params)
	})

	// Wait for all goroutines. g.Wait() always returns nil because every
	// goroutine returns nil — errors are captured in per-service variables
	// above. After Wait, propagate ctx cancellation so callers can distinguish
	// "all five services completed (with possibly per-service errors)" from
	// "the parent ctx was canceled mid-fan-out". Without this check the
	// CHECK could swallow a deadline exceeded that the caller expected to
	// see.
	_ = g.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return mergeServiceResults(serviceResult{"compute", computeRecs, computeErr, includeCompute},
		serviceResult{"database", dbRecs, dbErr, includeDB},
		serviceResult{"cache", cacheRecs, cacheErr, includeCache},
		serviceResult{"cosmosdb", cosmosRecs, cosmosErr, includeCosmos},
		// The savingsplans client is a stub that unconditionally returns
		// ([], nil) until the Benefits Recommendations API stabilizes (see
		// services/savingsplans Client.GetRecommendations). Counting its
		// built-in success as an attempted service would keep the
		// all-attempted-failed guard from ever firing on a total provider
		// failure (expired credential, subscription-wide throttle), so it is
		// excluded from the guard until it makes real API calls.
		serviceResult{"savingsplans", spRecs, spErr, false},
		// The Advisor client is excluded from the all-attempted-failed guard
		// for the same reason: getAdvisorRecommendations swallows pagination
		// errors (the auth failure from an expired credential surfaces as a
		// 401 during pager.NextPage, not during client construction) and
		// always returns (recs, nil). Counting its unconditional success as
		// an attempted service would keep the guard from firing on a total
		// credential failure -- the same hazard the savingsplans exclusion
		// prevents. When getAdvisorRecommendations is changed to propagate
		// hard errors, flip this flag back to true.
		serviceResult{"advisor", advisorRecs, advisorErr, false})
}

// serviceResult bundles a per-service collection outcome for the deterministic
// merge in mergeServiceResults. Extracted into a helper so GetRecommendations
// stays under the cyclomatic-complexity gate after the post-Wait ctx.Err()
// propagation was added.
type serviceResult struct {
	name string
	recs []common.Recommendation
	err  error
	// attempted records whether the service call was actually launched
	// (i.e. not skipped by the params service filter). Skipped services
	// carry nil recs and nil err, which is indistinguishable from a
	// successful zero-recommendation call, so the all-attempted-failed
	// guard below needs this flag to avoid counting skips as successes.
	attempted bool
}

// mergeServiceResults logs per-service errors (matches the previous sequential
// behaviour where each error was logged inline via logging.Warnf) and appends
// successful results in the order the slice is passed — callers must preserve
// the canonical compute → database → cache → cosmosdb → savingsplans → advisor
// order so that order-sensitive consumers remain stable. The advisor entry's
// error is logged via logging.Errorf to match the pre-parallelisation severity.
//
// Partial failure is tolerated: as long as at least one attempted service
// succeeded, the successful services' recommendations are returned with a nil
// error. But when EVERY attempted service errored (e.g. an expired federated
// credential or a subscription-wide throttle), the merge returns a wrapped
// error instead of an empty-but-nil-error result, porting the AWS 08-H4 guard
// from providers/aws/recommendations/client.go. Returning (recs, nil) on a
// total failure makes a broken run indistinguishable from "no savings
// available": the scheduler would count the account as succeeded, evict its
// previously collected rows, and clear last_collection_error (COR-03).
func mergeServiceResults(results ...serviceResult) ([]common.Recommendation, error) {
	total := 0
	attempted := 0
	failures := 0
	var lastErr error
	for _, r := range results {
		total += len(r.recs)
		if !r.attempted {
			continue
		}
		attempted++
		if r.err != nil {
			failures++
			lastErr = r.err
		}
	}
	out := make([]common.Recommendation, 0, total)
	for _, r := range results {
		if r.err != nil {
			if r.name == "advisor" {
				logging.Errorf("Failed to get Azure Advisor recommendations: %v", r.err)
			} else {
				logging.Warnf("Azure %s recommendations: %v", r.name, r.err)
			}
			continue
		}
		out = append(out, r.recs...)
	}
	if failures > 0 && failures == attempted {
		return nil, fmt.Errorf("all %d Azure recommendation services failed: %w", failures, lastErr)
	}
	return out, nil
}

// GetRecommendationsForService retrieves Azure reservation recommendations for a specific service
func (r *RecommendationsClientAdapter) GetRecommendationsForService(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error) {
	params := common.RecommendationParams{
		Service: service,
	}
	return r.GetRecommendations(ctx, params)
}

// GetAllRecommendations retrieves all Azure reservation recommendations across all services
func (r *RecommendationsClientAdapter) GetAllRecommendations(ctx context.Context) ([]common.Recommendation, error) {
	params := common.RecommendationParams{}
	return r.GetRecommendations(ctx, params)
}

// getAdvisorRecommendations retrieves cost optimization recommendations from Azure Advisor
func (r *RecommendationsClientAdapter) getAdvisorRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	client, err := armadvisor.NewRecommendationsClient(r.subscriptionID, r.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create advisor client: %w", err)
	}

	recommendations := make([]common.Recommendation, 0)

	// Filter for cost recommendations
	filter := "Category eq 'Cost'"
	pager := client.NewListPager(&armadvisor.RecommendationsClientListOptions{
		Filter: &filter,
	})

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			// Advisor partial-OK is intentional: per-service reservation results
			// (compute, database, etc.) remain useful on their own when Advisor
			// pagination fails mid-stream. Warnf with the accumulated count so
			// operators can assess the completeness of the Advisor slice.
			logging.Warnf("Azure Advisor pagination error after %d recommendations (partial results returned): %v", len(recommendations), err)
			break
		}
		recommendations = r.appendAdvisorPageRecs(params, page, recommendations)
	}

	return recommendations, nil
}

// appendAdvisorPageRecs converts one Advisor pager page into common recommendations
// and appends them to the provided slice. Pulled out of getAdvisorRecommendations
// to keep that function under the cyclomatic limit.
func (r *RecommendationsClientAdapter) appendAdvisorPageRecs(
	params common.RecommendationParams,
	page armadvisor.RecommendationsClientListResponse,
	recommendations []common.Recommendation,
) []common.Recommendation {
	for _, advisorRec := range page.Value {
		if advisorRec.Properties == nil {
			continue
		}
		// Convert Azure Advisor recommendation to our common format
		rec := r.convertAdvisorRecommendation(advisorRec)
		if rec != nil && shouldIncludeService(params, rec.Service) {
			// Preserve Advisor-provided EstimatedSavings when both OnDemandCost
			// and CommitmentCost are unset (zero), since ExpandPaymentVariants
			// would otherwise overwrite it with zero (OnDemandCost - CommitmentCost).
			advisorSavings := rec.EstimatedSavings
			variants := azrecs.ExpandPaymentVariants(*rec)
			if rec.OnDemandCost == 0 && rec.CommitmentCost == 0 && advisorSavings != 0 {
				for i := range variants {
					variants[i].EstimatedSavings = advisorSavings
				}
			}
			recommendations = append(recommendations, variants...)
		}
	}
	return recommendations
}

// resolveAdvisorRegion picks the region for an Advisor recommendation.
// ExtendedProperties["region"|"location"] is authoritative when present;
// the resource-ID parser is a fallback for the rare case where the ID
// embeds /locations/{region}/. Pulled out of convertAdvisorRecommendation
// to keep that function under the cyclomatic limit.
func resolveAdvisorRegion(advisorRec *armadvisor.ResourceRecommendationBase) string {
	if ext := advisorRec.Properties.ExtendedProperties; ext != nil {
		for _, key := range []string{"region", "location"} {
			if v, ok := ext[key]; ok && v != nil && *v != "" {
				return *v
			}
		}
	}
	if advisorRec.ID != nil {
		return extractRegionFromResourceID(*advisorRec.ID)
	}
	return ""
}

// convertAdvisorRecommendation converts an Azure Advisor recommendation to common format
func (r *RecommendationsClientAdapter) convertAdvisorRecommendation(advisorRec *armadvisor.ResourceRecommendationBase) *common.Recommendation {
	if advisorRec.Properties == nil {
		return nil
	}

	service := extractServiceType(advisorRec)
	if service == "" {
		return nil
	}

	rec := &common.Recommendation{
		Provider:       common.ProviderAzure,
		Service:        common.ServiceType(service),
		Account:        r.subscriptionID,
		CommitmentType: common.CommitmentReservedInstance,
		Term:           "1yr",
		PaymentOption:  "upfront",
	}

	rec.Region = resolveAdvisorRegion(advisorRec)

	populateFromExtendedProperties(rec, advisorRec.Properties.ExtendedProperties)
	return rec
}

// populateFromExtendedProperties fills savings, SKU, term, and count from
// the Advisor recommendation's ExtendedProperties map.
func populateFromExtendedProperties(rec *common.Recommendation, ext map[string]*string) {
	if ext == nil {
		return
	}
	rec.EstimatedSavings = extFloat(ext, "annualSavingsAmount") / 12
	rec.ResourceType = extString(ext, "sku", rec.ResourceType)
	rec.Term = extString(ext, "term", rec.Term)
	rec.Count = extInt(ext, "qty", rec.Count)
}

func extString(m map[string]*string, key, fallback string) string {
	if v, ok := m[key]; ok && v != nil && *v != "" {
		return *v
	}
	return fallback
}

func extFloat(m map[string]*string, key string) float64 {
	if v, ok := m[key]; ok && v != nil {
		if f, err := strconv.ParseFloat(*v, 64); err == nil {
			return f
		}
	}
	return 0
}

func extInt(m map[string]*string, key string, fallback int) int {
	if v, ok := m[key]; ok && v != nil {
		if n, err := strconv.Atoi(*v); err == nil {
			return n
		}
	}
	return fallback
}

// extractServiceType determines the service type from an Advisor recommendation.
// First checks ImpactedField (resource-scoped), then falls back to
// ExtendedProperties (subscription-scoped reservations/savings plans).
func extractServiceType(rec *armadvisor.ResourceRecommendationBase) string {
	if rec.Properties == nil {
		return ""
	}
	if svc := serviceFromImpactedField(rec.Properties.ImpactedField); svc != "" {
		return svc
	}
	return serviceFromExtendedProperties(rec.Properties.ExtendedProperties)
}

// serviceFromImpactedField maps Azure resource namespace to service type.
func serviceFromImpactedField(field *string) string {
	if field == nil {
		return ""
	}
	f := *field
	switch {
	case strings.Contains(f, "Microsoft.Compute"):
		return string(common.ServiceCompute)
	case strings.Contains(f, "Microsoft.Sql"):
		return string(common.ServiceRelationalDB)
	case strings.Contains(f, "Microsoft.Cache"):
		return string(common.ServiceCache)
	case strings.Contains(f, "Microsoft.DBforMySQL"), strings.Contains(f, "Microsoft.DBforPostgreSQL"):
		return string(common.ServiceRelationalDB)
	}
	return ""
}

// serviceFromExtendedProperties resolves service type for subscription-scoped
// recommendations where ImpactedField is "Microsoft.Subscriptions/subscriptions".
func serviceFromExtendedProperties(ext map[string]*string) string {
	if ext == nil {
		return ""
	}
	if rrt, ok := ext["reservedResourceType"]; ok && rrt != nil {
		switch strings.ToLower(*rrt) {
		case "virtualmachines":
			return string(common.ServiceCompute)
		case "sqldatabases":
			return string(common.ServiceRelationalDB)
		case "rediscache":
			return string(common.ServiceCache)
		}
	}
	if subcat, ok := ext["recommendationSubCategory"]; ok && subcat != nil {
		if strings.EqualFold(*subcat, "SavingsPlan") {
			return string(common.ServiceCompute)
		}
	}
	return ""
}

// extractRegionFromResourceID extracts the region from an Azure resource ID.
//
// Standard ARM resource IDs follow the shape
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/{ns}/{type}/{name}
//
// and do NOT embed the region — so this helper is a best-effort fallback
// only. Callers that have a better source (Advisor recommendation's
// Properties.ExtendedProperties["region"] / "location", or a sibling
// Location field) must use that first. This helper exists for the rare
// Advisor recommendation whose ID happens to carry a /locations/{region}/
// segment (some reservation-scope resource IDs do).
//
// Returns "" when the ID has no recognisable region segment.
func extractRegionFromResourceID(resourceID string) string {
	// Case-insensitive scan for /locations/{region}/ — Azure is inconsistent
	// between `locations`, `Locations`, `location`.
	lower := strings.ToLower(resourceID)
	for _, marker := range []string{"/locations/", "/location/"} {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		rest := resourceID[idx+len(marker):]
		// The next / ends the region segment.
		if end := strings.IndexByte(rest, '/'); end >= 0 {
			return rest[:end]
		}
		// No trailing / — the region is the last segment.
		return rest
	}
	return ""
}

// shouldIncludeService checks if a service should be included based on params
func shouldIncludeService(params common.RecommendationParams, service common.ServiceType) bool {
	// If no service specified in params, include all
	if params.Service == "" {
		return true
	}

	// Check if this is the requested service
	return params.Service == service
}
