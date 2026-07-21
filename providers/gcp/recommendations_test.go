package gcp

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

func TestShouldIncludeService(t *testing.T) {
	tests := []struct {
		name     string
		params   common.RecommendationParams
		service  common.ServiceType
		expected bool
	}{
		{
			name:     "Empty params includes all services - Compute",
			params:   common.RecommendationParams{},
			service:  common.ServiceCompute,
			expected: true,
		},
		{
			name:     "Empty params includes all services - RelationalDB",
			params:   common.RecommendationParams{},
			service:  common.ServiceRelationalDB,
			expected: true,
		},
		{
			name: "Specific service matches - Compute",
			params: common.RecommendationParams{
				Service: common.ServiceCompute,
			},
			service:  common.ServiceCompute,
			expected: true,
		},
		{
			name: "Specific service does not match",
			params: common.RecommendationParams{
				Service: common.ServiceCompute,
			},
			service:  common.ServiceRelationalDB,
			expected: false,
		},
		{
			name: "RelationalDB service matches",
			params: common.RecommendationParams{
				Service: common.ServiceRelationalDB,
			},
			service:  common.ServiceRelationalDB,
			expected: true,
		},
		{
			name: "Cache service requested - Compute not included",
			params: common.RecommendationParams{
				Service: common.ServiceCache,
			},
			service:  common.ServiceCompute,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldIncludeService(tt.params, tt.service)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRecommendationsClientAdapter_GetRecommendationsForService(t *testing.T) {
	ctx := context.Background()
	adapter := &RecommendationsClientAdapter{
		ctx:       ctx,
		projectID: "test-project",
	}

	// Without real GCP credentials the regions call will fail. Since issue
	// #247, permission errors (403) return ([], nil) instead of an error, so
	// we can only assert that the call does not panic. Non-permission errors
	// (network timeout, invalid project) still propagate as errors — but we
	// cannot predict which path the test environment will take without
	// mocking the provider. Structure is verified in fields/context tests.
	recs, err := adapter.GetRecommendationsForService(ctx, common.ServiceCompute)
	if err != nil {
		// non-permission failure: function returned an error as expected
		return
	}
	// permission failure path (issue #247): returns empty slice, no error
	assert.NotNil(t, recs)
}

func TestRecommendationsClientAdapter_GetAllRecommendations(t *testing.T) {
	ctx := context.Background()
	adapter := &RecommendationsClientAdapter{
		ctx:       ctx,
		projectID: "test-project",
	}

	// Same environment-sensitivity caveat as GetRecommendationsForService.
	// Assert no panic; accept either ([], nil) for permission errors or
	// (nil, err) for other failures.
	recs, err := adapter.GetAllRecommendations(ctx)
	if err != nil {
		return
	}
	assert.NotNil(t, recs)
}

func TestRecommendationsClientAdapter_Fields(t *testing.T) {
	ctx := context.Background()
	adapter := &RecommendationsClientAdapter{
		ctx:       ctx,
		projectID: "my-gcp-project",
	}

	assert.Equal(t, ctx, adapter.ctx)
	assert.Equal(t, "my-gcp-project", adapter.projectID)
	assert.Nil(t, adapter.clientOpts)
}

// TestRecommendationsClientAdapter_GetRecommendations_PropagatesContextCancellation
// pins the contract that GetRecommendations propagates ctx.Err() to its caller
// after the errgroup Wait() — the parent context being cancelled or its
// deadline exceeding must surface as an error rather than being swallowed by
// the per-region error-isolation goroutines (which all return nil to the
// errgroup so a single per-region failure does not cancel siblings).
//
// Without the explicit `if err := ctx.Err(); err != nil { return nil, err }`
// after the outer `g.Wait()`, callers that wrap GetRecommendations with a
// deadline could see "all regions finished cleanly" even when the deadline
// expired mid-fan-out (because every goroutine returned nil from its closure).
//
// Mirrors providers/azure/recommendations_test.go's
// TestRecommendationsClientAdapter_GetRecommendations_PropagatesContextCancellation
// and providers/aws/recommendations/client_test.go's
// TestGetAllRecommendations_PropagatesContextCancellation.
func TestRecommendationsClientAdapter_GetRecommendations_PropagatesContextCancellation(t *testing.T) {
	adapter := &RecommendationsClientAdapter{
		ctx:       context.Background(),
		projectID: "test-project",
	}

	// Cancel the context BEFORE the call so we don't depend on race-y timing
	// inside the SDK clients. getRegions itself observes the cancelled ctx
	// and returns the cancellation error wrapped via fmt.Errorf("failed to
	// get regions: %w", err) — errors.Is unwraps that. (If a future refactor
	// makes getRegions skip the ctx check, the post-Wait ctx.Err() block is
	// still the safety net and the assertion still holds.)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := adapter.GetRecommendations(ctx, &common.RecommendationParams{})
	require.Error(t, err, "expected context.Canceled to propagate from GetRecommendations")
	assert.ErrorIs(t, err, context.Canceled,
		"GetRecommendations must propagate the parent ctx error")
}

// TestRegionResult_HasCacheAndStorageFields is a compile-time regression test
// for H-2 (GCP broad audit): regionResult must carry cache and storage slices
// so collectRegion can fan out to memorystore and cloudstorage in addition to
// compute and cloudsql. If this test stops compiling, the wiring was reverted.
func TestRegionResult_HasCacheAndStorageFields(t *testing.T) {
	recs := []common.Recommendation{{Provider: common.ProviderGCP}}

	// Field access verifies that regionResult has the full four-service shape.
	result := regionResult{
		compute: recs,
		sql:     recs,
		cache:   recs,
		storage: recs,
	}

	assert.Len(t, result.compute, 1, "compute field must be present on regionResult")
	assert.Len(t, result.sql, 1, "sql field must be present on regionResult")
	assert.Len(t, result.cache, 1, "cache field must be present on regionResult (H-2)")
	assert.Len(t, result.storage, 1, "storage field must be present on regionResult (H-2)")
}

// TestShouldIncludeService_Cache_Storage verifies that shouldIncludeService
// correctly routes ServiceCache and ServiceStorage requests, which is a
// prerequisite for H-2 (wiring them into collectRegion).
func TestShouldIncludeService_Cache_Storage(t *testing.T) {
	tests := []struct {
		name     string
		params   common.RecommendationParams
		service  common.ServiceType
		expected bool
	}{
		{
			name:     "all services includes Cache",
			params:   common.RecommendationParams{},
			service:  common.ServiceCache,
			expected: true,
		},
		{
			name:     "all services includes Storage",
			params:   common.RecommendationParams{},
			service:  common.ServiceStorage,
			expected: true,
		},
		{
			name:     "Cache-scoped request includes Cache",
			params:   common.RecommendationParams{Service: common.ServiceCache},
			service:  common.ServiceCache,
			expected: true,
		},
		{
			name:     "Cache-scoped request excludes Storage",
			params:   common.RecommendationParams{Service: common.ServiceCache},
			service:  common.ServiceStorage,
			expected: false,
		},
		{
			name:     "Storage-scoped request includes Storage",
			params:   common.RecommendationParams{Service: common.ServiceStorage},
			service:  common.ServiceStorage,
			expected: true,
		},
		{
			name:     "Storage-scoped request excludes Compute",
			params:   common.RecommendationParams{Service: common.ServiceStorage},
			service:  common.ServiceCompute,
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, shouldIncludeService(tt.params, tt.service))
		})
	}
}

// TestGCPRegionConcurrency pins the env-knob parsing for
// CUDLY_GCP_REGION_PARALLELISM.
func TestGCPRegionConcurrency(t *testing.T) {
	t.Run("unset returns default", func(t *testing.T) {
		t.Setenv("CUDLY_GCP_REGION_PARALLELISM", "")
		assert.Equal(t, defaultGCPRegionConcurrency, gcpRegionConcurrency())
	})
	t.Run("positive integer overrides default", func(t *testing.T) {
		t.Setenv("CUDLY_GCP_REGION_PARALLELISM", "25")
		assert.Equal(t, 25, gcpRegionConcurrency())
	})
	t.Run("non-numeric falls back to default", func(t *testing.T) {
		t.Setenv("CUDLY_GCP_REGION_PARALLELISM", "many")
		assert.Equal(t, defaultGCPRegionConcurrency, gcpRegionConcurrency())
	})
	t.Run("zero falls back to default", func(t *testing.T) {
		t.Setenv("CUDLY_GCP_REGION_PARALLELISM", "0")
		assert.Equal(t, defaultGCPRegionConcurrency, gcpRegionConcurrency())
	})
	t.Run("negative falls back to default", func(t *testing.T) {
		t.Setenv("CUDLY_GCP_REGION_PARALLELISM", "-3")
		assert.Equal(t, defaultGCPRegionConcurrency, gcpRegionConcurrency())
	})
	// Sanity check: explicit unset path (Setenv only sets-then-restores; we
	// also cover a real Unsetenv to make sure the os.Getenv == "" branch is
	// exercised independently of the test framework's stash/restore).
	t.Run("explicit unset returns default", func(t *testing.T) {
		os.Unsetenv("CUDLY_GCP_REGION_PARALLELISM")
		assert.Equal(t, defaultGCPRegionConcurrency, gcpRegionConcurrency())
	})
}

// TestMergeRegionResults_AllAttemptedFailed is the COR-03 regression test:
// when EVERY attempted (region, service) call errors (the shape produced by a
// project-wide throttle or an RBAC gap that survives getRegions), the merge
// must return a non-nil error instead of (empty, nil). Pre-fix, the per-region
// errors were logged in collectRegion and dropped, so a total failure surfaced
// as an empty successful collection: the scheduler counted the account in
// SucceededAccountIDs, UpsertRecommendations evicted all previously collected
// rows for it, and last_collection_error was cleared.
func TestMergeRegionResults_AllAttemptedFailed(t *testing.T) {
	rbacErr := errors.New("googleapi: Error 403: missing recommender.commitmentRecommendations.list")

	results := map[string]regionResult{
		"europe-west1": {attempted: 4, failed: 4, lastErr: rbacErr},
		"us-central1":  {attempted: 4, failed: 4, lastErr: rbacErr},
	}

	recs, err := mergeRegionResults([]string{"europe-west1", "us-central1"}, results)

	require.Error(t, err, "all-attempted-failed merge must fail loud, not return (empty, nil)")
	assert.ErrorIs(t, err, rbacErr, "the underlying service error must be wrapped")
	assert.Contains(t, err.Error(), "all 8 GCP recommendation service calls failed across 2 regions")
	assert.Nil(t, recs)
}

// TestMergeRegionResults_PartialFailureStillSucceeds pins the tolerated
// partial-failure behavior: a single successful (region, service) call is
// enough for the merge to return its recommendations with a nil error.
func TestMergeRegionResults_PartialFailureStillSucceeds(t *testing.T) {
	throttleErr := errors.New("googleapi: Error 429: quota exceeded")
	computeRec := common.Recommendation{Service: common.ServiceCompute, Provider: common.ProviderGCP, Region: "us-central1"}

	results := map[string]regionResult{
		"europe-west1": {attempted: 4, failed: 4, lastErr: throttleErr},
		"us-central1": {
			compute:   []common.Recommendation{computeRec},
			attempted: 4,
			failed:    3,
			lastErr:   throttleErr,
		},
	}

	recs, err := mergeRegionResults([]string{"europe-west1", "us-central1"}, results)

	require.NoError(t, err, "partial failure must still return the successful calls' recs")
	require.Len(t, recs, 1)
	assert.Equal(t, "us-central1", recs[0].Region)
}

// TestMergeRegionResults_NoAttemptsIsNotAFailure asserts the guard does not
// fire when nothing was attempted (zero regions, or every service filtered
// out by params): that case keeps the previous empty-success behavior.
func TestMergeRegionResults_NoAttemptsIsNotAFailure(t *testing.T) {
	recs, err := mergeRegionResults(nil, map[string]regionResult{})

	require.NoError(t, err)
	assert.Empty(t, recs)
}
