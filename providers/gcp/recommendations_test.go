package gcp

import (
	"context"
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

	// This will fail without credentials, but we're testing the structure
	_, err := adapter.GetRecommendationsForService(ctx, common.ServiceCompute)
	assert.Error(t, err) // Expected to fail without credentials/API access
}

func TestRecommendationsClientAdapter_GetAllRecommendations(t *testing.T) {
	ctx := context.Background()
	adapter := &RecommendationsClientAdapter{
		ctx:       ctx,
		projectID: "test-project",
	}

	// This will fail without credentials, but we're testing the structure
	_, err := adapter.GetAllRecommendations(ctx)
	assert.Error(t, err) // Expected to fail without credentials/API access
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

	_, err := adapter.GetRecommendations(ctx, common.RecommendationParams{})
	require.Error(t, err, "expected context.Canceled to propagate from GetRecommendations")
	assert.ErrorIs(t, err, context.Canceled,
		"GetRecommendations must propagate the parent ctx error")
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
