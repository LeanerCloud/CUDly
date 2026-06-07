// Package gcp provides GCP recommendations client
package gcp

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/concurrency"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/providers/gcp/services/cloudsql"
	"github.com/LeanerCloud/CUDly/providers/gcp/services/cloudstorage"
	"github.com/LeanerCloud/CUDly/providers/gcp/services/computeengine"
	"github.com/LeanerCloud/CUDly/providers/gcp/services/memorystore"
)

// defaultGCPRegionConcurrency caps the parallel per-region goroutines inside
// a single GetRecommendations call. The GCP Recommender API is project-scoped
// and per-region calls share the project's quota, so the cap is intentionally
// modest. Override at runtime via CUDLY_GCP_REGION_PARALLELISM.
const defaultGCPRegionConcurrency = 10

// gcpRegionConcurrency reads the CUDLY_GCP_REGION_PARALLELISM env var and
// returns its positive-integer value, falling back to
// defaultGCPRegionConcurrency on unset / invalid / non-positive values. The
// helper is local to the gcp package because the providers/gcp module is a
// separate Go module from internal/execution and cannot import its
// ConcurrencyFromEnv counterpart directly.
func gcpRegionConcurrency() int {
	if v := os.Getenv("CUDLY_GCP_REGION_PARALLELISM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultGCPRegionConcurrency
}

// regionResult bundles per-service recommendation slices returned for a single
// GCP region. The merge in GetRecommendations walks regions in sorted order
// and appends compute, sql, cache, storage per region so output is
// deterministic independent of goroutine completion order.
//
// All four GCP service clients (computeengine, cloudsql, memorystore,
// cloudstorage) implement GetRecommendations and are fanned out concurrently
// when shouldIncludeService permits. Note that cache and storage purchase paths
// are advisory-only (no-op PurchaseCommitment); their recommendations are still
// surfaced so operators can see spend-optimisation signals.
type regionResult struct {
	compute []common.Recommendation
	sql     []common.Recommendation
	cache   []common.Recommendation
	storage []common.Recommendation
}

// RecommendationsClientAdapter aggregates GCP CUD and commitment recommendations across all services
type RecommendationsClientAdapter struct {
	ctx        context.Context
	projectID  string
	clientOpts []option.ClientOption
}

// GetRecommendations retrieves all GCP commitment recommendations across all
// services and regions.
//
// Two-level concurrent fan-out:
//   - Outer: errgroup over regions, capped at gcpRegionConcurrency()
//     (CUDLY_GCP_REGION_PARALLELISM, default 10) to stay polite to the
//     project-scoped Recommender API quota.
//   - Inner: within each region's goroutine, the four service calls
//     (compute, cloud-sql, memorystore, cloudstorage) run as concurrent
//     goroutines under a per-region sub-errgroup, so the per-region cost is
//     max(service latencies) rather than their sum.
//
// Behaviour change vs the previous nested for-loops: per-(region, service)
// errors that were previously silently swallowed (`if err == nil { ... }`
// shape) are now logged at WARN with region+service identifiers so
// misconfigured projects are diagnosable. Errors do NOT cancel siblings —
// each goroutine returns nil to its errgroup, matching the previous
// silent-skip-on-err semantics.
//
// After the outer Wait(), ctx.Err() is checked and propagated so a canceled
// parent ctx surfaces as context.Canceled / context.DeadlineExceeded rather
// than being swallowed by the per-region error-isolation goroutines.
//
// Mirrors the Azure parallelisation in
// providers/azure/recommendations.go (closes #258, commit b10326c5) and the
// AWS service-loop parallelisation (closes #266).
func (r *RecommendationsClientAdapter) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	// Get list of regions to check
	regions, err := r.getRegions(ctx)
	if err != nil {
		// Permission errors (403 / missing compute.regions.list) mean the
		// service account lacks Compute Viewer on this project. Log at Warn
		// so it doesn't spam as ERROR in Lambda — the application-layer auth
		// still works; only GCP recommendations for this account are skipped.
		// See issue #247.
		if isPermissionError(err) {
			logging.Warnf("GCP account %s: skipping recommendations — insufficient Compute permission to list regions (grant roles/compute.viewer): %v", r.projectID, err)
			return []common.Recommendation{}, nil
		}
		return nil, fmt.Errorf("failed to get regions: %w", err)
	}

	// Collect per-region results into a map keyed by region name. The merge
	// step walks regions in sorted order so the output is deterministic
	// independent of goroutine completion order — keeps snapshot tests
	// stable.
	var (
		mu      sync.Mutex
		results = make(map[string]regionResult, len(regions))
	)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(gcpRegionConcurrency())

	for _, region := range regions {
		region := region // capture per-iteration
		g.Go(func() error {
			res := r.collectRegion(gctx, params, region)
			mu.Lock()
			results[region] = res
			mu.Unlock()
			return nil // error isolation: per-region failures don't cancel siblings
		})
	}

	// Wait for all region goroutines. g.Wait() always returns nil because
	// every goroutine returns nil — errors are logged inside collectRegion.
	// After Wait, propagate ctx cancellation so callers can distinguish
	// "all regions completed (with possibly per-region errors)" from "the
	// parent ctx was canceled mid-fan-out".
	_ = g.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Deterministic merge: walk regions in sorted order, append compute, sql,
	// cache, storage per region. Output is stable regardless of GCP API
	// region-list ordering or goroutine completion order.
	sortedRegions := make([]string, 0, len(results))
	for region := range results {
		sortedRegions = append(sortedRegions, region)
	}
	sort.Strings(sortedRegions)

	allRecommendations := make([]common.Recommendation, 0)
	for _, region := range sortedRegions {
		res := results[region]
		allRecommendations = append(allRecommendations, res.compute...)
		allRecommendations = append(allRecommendations, res.sql...)
		allRecommendations = append(allRecommendations, res.cache...)
		allRecommendations = append(allRecommendations, res.storage...)
	}
	return allRecommendations, nil
}

// collectRegion fetches recommendations for all four GCP services
// (Compute Engine, Cloud SQL, Memorystore, Cloud Storage) for a single region
// concurrently. Per-service errors are logged at WARN with the region+service
// tag and never propagate — the previous silent-skip-on-err shape is preserved
// (so a misconfigured project doesn't error out the whole recommendations
// refresh) but errors are now observable in logs. Extracted from
// GetRecommendations to keep that function under the gocyclo gate
// (.golangci.yml min-complexity: 15) after the post-Wait ctx.Err() block was
// added.
//
// Note: memorystore and cloudstorage PurchaseCommitment paths are advisory-only
// (no programmatic purchase API exists for either); their recommendations are
// surfaced so operators can see spend-optimisation signals (H-2 fix).
func (r *RecommendationsClientAdapter) collectRegion(ctx context.Context, params common.RecommendationParams, region string) regionResult {
	var (
		computeRecs, sqlRecs, cacheRecs, storageRecs []common.Recommendation
		computeErr, sqlErr, cacheErr, storageErr     error
	)

	g, gctx := errgroup.WithContext(ctx)

	// Per-(region, service) goroutines are leaves — they issue the actual
	// Recommender API call. Acquire bounds aggregate concurrent IO across
	// the whole recommendations-collection fan-out tree at the shared
	// semaphore's cap (CUDLY_MAX_PARALLELISM, default 20); Release returns
	// the slot. Without this bound the per-region fan-out (cap 10) x
	// per-service sub-fan-out (4) x accounts x providers can produce
	// hundreds of concurrent gRPC clients that exhaust Lambda memory. If
	// no semaphore is on ctx (CLI tools, unit tests), Acquire/Release are
	// no-ops. See pkg/concurrency.
	if shouldIncludeService(params, common.ServiceCompute) {
		g.Go(func() error {
			if err := concurrency.Acquire(gctx); err != nil {
				computeErr = err
				return nil
			}
			defer concurrency.Release(gctx)
			client, err := computeengine.NewClient(gctx, r.projectID, region, r.clientOpts...)
			if err != nil {
				computeErr = err
				return nil
			}
			computeRecs, computeErr = client.GetRecommendations(gctx, params)
			return nil
		})
	}
	if shouldIncludeService(params, common.ServiceRelationalDB) {
		g.Go(func() error {
			if err := concurrency.Acquire(gctx); err != nil {
				sqlErr = err
				return nil
			}
			defer concurrency.Release(gctx)
			client, err := cloudsql.NewClient(gctx, r.projectID, region, r.clientOpts...)
			if err != nil {
				sqlErr = err
				return nil
			}
			sqlRecs, sqlErr = client.GetRecommendations(gctx, params)
			return nil
		})
	}
	if shouldIncludeService(params, common.ServiceCache) {
		g.Go(func() error {
			if err := concurrency.Acquire(gctx); err != nil {
				cacheErr = err
				return nil
			}
			defer concurrency.Release(gctx)
			client, err := memorystore.NewClient(gctx, r.projectID, region, r.clientOpts...)
			if err != nil {
				cacheErr = err
				return nil
			}
			cacheRecs, cacheErr = client.GetRecommendations(gctx, params)
			return nil
		})
	}
	if shouldIncludeService(params, common.ServiceStorage) {
		g.Go(func() error {
			if err := concurrency.Acquire(gctx); err != nil {
				storageErr = err
				return nil
			}
			defer concurrency.Release(gctx)
			client, err := cloudstorage.NewClient(gctx, r.projectID, region, r.clientOpts...)
			if err != nil {
				storageErr = err
				return nil
			}
			storageRecs, storageErr = client.GetRecommendations(gctx, params)
			return nil
		})
	}
	_ = g.Wait()

	if computeErr != nil {
		logging.Warnf("GCP %s compute recommendations: %v", region, computeErr)
	}
	if sqlErr != nil {
		logging.Warnf("GCP %s cloudsql recommendations: %v", region, sqlErr)
	}
	if cacheErr != nil {
		logging.Warnf("GCP %s memorystore recommendations: %v", region, cacheErr)
	}
	if storageErr != nil {
		logging.Warnf("GCP %s cloudstorage recommendations: %v", region, storageErr)
	}

	return regionResult{compute: computeRecs, sql: sqlRecs, cache: cacheRecs, storage: storageRecs}
}

// GetRecommendationsForService retrieves GCP commitment recommendations for a specific service
func (r *RecommendationsClientAdapter) GetRecommendationsForService(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error) {
	params := common.RecommendationParams{
		Service: service,
	}
	return r.GetRecommendations(ctx, params)
}

// GetAllRecommendations retrieves all GCP commitment recommendations across all services
func (r *RecommendationsClientAdapter) GetAllRecommendations(ctx context.Context) ([]common.Recommendation, error) {
	params := common.RecommendationParams{}
	return r.GetRecommendations(ctx, params)
}

// getRegions retrieves available GCP regions for the project
func (r *RecommendationsClientAdapter) getRegions(ctx context.Context) ([]string, error) {
	// Create a temporary provider to get regions. The local variable is named
	// p (not provider) to avoid shadowing the imported provider package (10-N3).
	p := NewProviderWithProject(ctx, r.projectID, r.clientOpts...)

	regions, err := p.GetRegions(ctx)
	if err != nil {
		return nil, err
	}

	regionNames := make([]string, 0, len(regions))
	for _, region := range regions {
		regionNames = append(regionNames, region.ID)
	}

	return regionNames, nil
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

// isPermissionError returns true when err is a GCP 403 "Required '...' permission"
// error. These errors arise when the service account is missing an IAM role
// (e.g. roles/compute.viewer for compute.regions.list). Callers can then log
// at Warn rather than propagating an error that would be recorded as ERROR by
// the Lambda handler — see issue #247.
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	var gapiErr *googleapi.Error
	if ok := errorAs(err, &gapiErr); ok && gapiErr.Code == 403 {
		return true
	}
	// Fall back to string inspection for wrapped or non-googleapi 403s.
	msg := err.Error()
	return strings.Contains(msg, "403") && strings.Contains(msg, "permission")
}

// errorAs is a thin shim around errors.As so it can be stubbed in tests
// without importing errors at the call site.
var errorAs = func(err error, target interface{}) bool {
	switch t := target.(type) {
	case **googleapi.Error:
		var gErr *googleapi.Error
		if !isGoogleAPIError(err, &gErr) {
			return false
		}
		*t = gErr
		return true
	}
	return false
}

func isGoogleAPIError(err error, out **googleapi.Error) bool {
	if gErr, ok := err.(*googleapi.Error); ok {
		*out = gErr
		return true
	}
	// Unwrap one level for fmt.Errorf("%w", ...) wrapping.
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok {
		return isGoogleAPIError(u.Unwrap(), out)
	}
	return false
}
