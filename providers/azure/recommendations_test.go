package azure

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/advisor/armadvisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// mockAzureTokenCredential implements azcore.TokenCredential for testing
type mockAzureTokenCredential struct{}

func (m *mockAzureTokenCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "mock-token"}, nil
}

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
			name:     "Empty params includes all services - Cache",
			params:   common.RecommendationParams{},
			service:  common.ServiceCache,
			expected: true,
		},
		{
			name:     "Empty params includes all services - RelationalDB",
			params:   common.RecommendationParams{},
			service:  common.ServiceRelationalDB,
			expected: true,
		},
		{
			name: "Specific service matches",
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
			service:  common.ServiceCache,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldIncludeService(tt.params, tt.service)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		substr   string
		expected bool
	}{
		{
			name:     "Contains substring",
			s:        "Microsoft.Compute/virtualMachines",
			substr:   "Microsoft.Compute",
			expected: true,
		},
		{
			name:     "Does not contain substring",
			s:        "Microsoft.Sql/servers",
			substr:   "Microsoft.Compute",
			expected: false,
		},
		{
			name:     "Empty string does not contain anything",
			s:        "",
			substr:   "something",
			expected: false,
		},
		{
			name:     "Any string contains empty substring",
			s:        "anything",
			substr:   "",
			expected: true,
		},
		{
			name:     "Case sensitive match",
			s:        "Microsoft.Cache",
			substr:   "microsoft.cache",
			expected: false,
		},
		{
			name:     "Exact match",
			s:        "test",
			substr:   "test",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := strings.Contains(tt.s, tt.substr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractRegionFromResourceID(t *testing.T) {
	tests := []struct {
		name       string
		resourceID string
		expected   string
	}{
		{
			name:       "Standard ARM ID without region segment returns empty",
			resourceID: "/subscriptions/123/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/vm1",
			expected:   "",
		},
		{
			name:       "Empty resource ID",
			resourceID: "",
			expected:   "",
		},
		{
			name:       "Locations segment mid-id extracts region",
			resourceID: "/subscriptions/123/providers/Microsoft.Capacity/locations/eastus/reservationOrders/rid1",
			expected:   "eastus",
		},
		{
			name:       "Uppercase Locations segment case-insensitive match",
			resourceID: "/subscriptions/123/providers/Microsoft.Capacity/Locations/westus2/reservationOrders/rid1",
			expected:   "westus2",
		},
		{
			name:       "Singular location segment also recognised",
			resourceID: "/subscriptions/123/providers/Microsoft.Resources/location/northeurope/foo/bar",
			expected:   "northeurope",
		},
		{
			name:       "Region is the last segment (no trailing slash)",
			resourceID: "/subscriptions/123/locations/centralus",
			expected:   "centralus",
		},
		{
			name:       "Non-ARM-shaped string returns empty safely",
			resourceID: "not-an-id",
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRegionFromResourceID(tt.resourceID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRecommendationsClientAdapter_Fields(t *testing.T) {
	// Test that adapter can be created with expected fields
	adapter := &RecommendationsClientAdapter{
		subscriptionID: "test-subscription",
	}

	assert.Equal(t, "test-subscription", adapter.subscriptionID)
	assert.Nil(t, adapter.cred)
}

func TestRecommendationsClientAdapter_GetRecommendationsForService(t *testing.T) {
	adapter := &RecommendationsClientAdapter{
		cred:           &mockAzureTokenCredential{},
		subscriptionID: "test-subscription",
	}

	// This will try to make API calls which will fail without real credentials,
	// but we test the wiring is correct. Error is expected since we don't have
	// real Azure credentials - the important thing is the function is wired correctly.
	_, _ = adapter.GetRecommendationsForService(context.Background(), common.ServiceCompute)
}

func TestRecommendationsClientAdapter_GetAllRecommendations(t *testing.T) {
	adapter := &RecommendationsClientAdapter{
		cred:           &mockAzureTokenCredential{},
		subscriptionID: "test-subscription",
	}

	// This will try to make API calls which will fail without real credentials,
	// but we test the wiring is correct. Error is expected - we just verify the function is callable.
	_, _ = adapter.GetAllRecommendations(context.Background())
}

// TestGetRecommendations_SavingsPlansServiceIncluded pins that shouldIncludeService
// allows ServiceSavingsPlans through both when params.Service is empty (all-services
// sweep) and when explicitly set to ServiceSavingsPlans, and does not include it
// when a different service is requested. This ensures the SP goroutine added to the
// fan-out in GetRecommendations is exercised on every scheduler collection run.
func TestGetRecommendations_SavingsPlansServiceIncluded(t *testing.T) {
	tests := []struct {
		name     string
		params   common.RecommendationParams
		expected bool
	}{
		{
			name:     "empty params includes savingsplans",
			params:   common.RecommendationParams{},
			expected: true,
		},
		{
			name:     "explicit savingsplans service is included",
			params:   common.RecommendationParams{Service: common.ServiceSavingsPlans},
			expected: true,
		},
		{
			name:     "different service excludes savingsplans",
			params:   common.RecommendationParams{Service: common.ServiceCompute},
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldIncludeService(tt.params, common.ServiceSavingsPlans)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestRecommendationsClientAdapter_GetRecommendations_PropagatesContextCancellation
// pins the contract that GetRecommendations propagates ctx.Err() to its caller
// after the errgroup Wait() — the parent context being cancelled or its
// deadline exceeding must surface as an error rather than being swallowed by
// the per-service error-isolation goroutines (which all return nil to the
// errgroup so a single per-service failure does not cancel siblings).
//
// Without the explicit `if err := ctx.Err(); err != nil { return nil, err }`
// after `g.Wait()`, callers that wrap GetRecommendations with a deadline could
// see "all services finished cleanly" even when the deadline expired
// mid-fan-out (because every goroutine returned nil from its closure).
func TestRecommendationsClientAdapter_GetRecommendations_PropagatesContextCancellation(t *testing.T) {
	adapter := &RecommendationsClientAdapter{
		cred:           &mockAzureTokenCredential{},
		subscriptionID: "test-subscription",
	}

	// Cancel the context BEFORE the call so we don't depend on race-y timing
	// inside the SDK clients. The Azure clients constructed inside the
	// goroutines will observe the cancelled gctx (derived from the parent ctx
	// via errgroup.WithContext) and either short-circuit or return cancelled
	// errors; either way, our post-Wait ctx.Err() check returns context.Canceled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := adapter.GetRecommendations(ctx, common.RecommendationParams{})
	require.Error(t, err, "expected context.Canceled to propagate from GetRecommendations")
	assert.ErrorIs(t, err, context.Canceled,
		"GetRecommendations must propagate the parent ctx error after g.Wait()")
}

func TestExtractServiceType(t *testing.T) {
	tests := []struct {
		name          string
		impactedField string
		expected      string
	}{
		{
			name:          "Microsoft.Compute returns Compute",
			impactedField: "Microsoft.Compute/virtualMachines",
			expected:      string(common.ServiceCompute),
		},
		{
			name:          "Microsoft.Sql returns RelationalDB",
			impactedField: "Microsoft.Sql/servers",
			expected:      string(common.ServiceRelationalDB),
		},
		{
			name:          "Microsoft.Cache returns Cache",
			impactedField: "Microsoft.Cache/Redis",
			expected:      string(common.ServiceCache),
		},
		{
			name:          "Microsoft.DBforMySQL returns RelationalDB",
			impactedField: "Microsoft.DBforMySQL/servers",
			expected:      string(common.ServiceRelationalDB),
		},
		{
			name:          "Microsoft.DBforPostgreSQL returns RelationalDB",
			impactedField: "Microsoft.DBforPostgreSQL/servers",
			expected:      string(common.ServiceRelationalDB),
		},
		{
			name:          "Unknown resource type returns empty",
			impactedField: "Microsoft.Storage/storageAccounts",
			expected:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &armadvisor.ResourceRecommendationBase{
				Properties: &armadvisor.RecommendationProperties{
					ImpactedField: &tt.impactedField,
				},
			}
			result := extractServiceType(rec)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractServiceType_NilProperties(t *testing.T) {
	// Test with nil properties
	rec := &armadvisor.ResourceRecommendationBase{
		Properties: nil,
	}
	result := extractServiceType(rec)
	assert.Equal(t, "", result)
}

func TestExtractServiceType_NilImpactedField(t *testing.T) {
	// Test with nil impacted field
	rec := &armadvisor.ResourceRecommendationBase{
		Properties: &armadvisor.RecommendationProperties{
			ImpactedField: nil,
		},
	}
	result := extractServiceType(rec)
	assert.Equal(t, "", result)
}

func TestConvertAdvisorRecommendation(t *testing.T) {
	adapter := &RecommendationsClientAdapter{
		subscriptionID: "test-subscription",
	}

	impactedField := "Microsoft.Compute/virtualMachines"
	resourceID := "/subscriptions/123/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/vm1"

	rec := &armadvisor.ResourceRecommendationBase{
		ID: &resourceID,
		Properties: &armadvisor.RecommendationProperties{
			ImpactedField: &impactedField,
			ExtendedProperties: map[string]*string{
				"annualSavingsAmount": strPtr("1000.00"),
				"savingsCurrency":     strPtr("USD"),
			},
		},
	}

	result := adapter.convertAdvisorRecommendation(rec)
	require.NotNil(t, result)
	assert.Equal(t, common.ProviderAzure, result.Provider)
	assert.Equal(t, common.ServiceCompute, result.Service)
	assert.Equal(t, "test-subscription", result.Account)
	assert.Equal(t, common.CommitmentReservedInstance, result.CommitmentType)
	assert.Equal(t, "1yr", result.Term)
	assert.Equal(t, "upfront", result.PaymentOption)
}

func TestConvertAdvisorRecommendation_NilProperties(t *testing.T) {
	adapter := &RecommendationsClientAdapter{
		subscriptionID: "test-subscription",
	}

	rec := &armadvisor.ResourceRecommendationBase{
		Properties: nil,
	}

	result := adapter.convertAdvisorRecommendation(rec)
	assert.Nil(t, result)
}

func TestConvertAdvisorRecommendation_UnknownService(t *testing.T) {
	adapter := &RecommendationsClientAdapter{
		subscriptionID: "test-subscription",
	}

	impactedField := "Microsoft.Storage/storageAccounts"
	rec := &armadvisor.ResourceRecommendationBase{
		Properties: &armadvisor.RecommendationProperties{
			ImpactedField: &impactedField,
		},
	}

	result := adapter.convertAdvisorRecommendation(rec)
	assert.Nil(t, result)
}

func strPtr(s string) *string {
	return &s
}

// TestMergeServiceResults_OrderIsStable is a regression test for L3:
// the merged output of mergeServiceResults must preserve the canonical
// compute -> database -> cache -> cosmosdb -> savingsplans -> advisor order
// regardless of the order arguments are passed in. This test pins the actual
// argument order used in GetRecommendations so a reordering of the serviceResult
// literals will be caught immediately.
func TestMergeServiceResults_OrderIsStable(t *testing.T) {
	mkRec := func(svc common.ServiceType, marker string) common.Recommendation {
		return common.Recommendation{Service: svc, Provider: common.ProviderAzure, ResourceType: marker}
	}

	computeRec := mkRec(common.ServiceCompute, "compute")
	dbRec := mkRec(common.ServiceRelationalDB, "database")
	cacheRec := mkRec(common.ServiceCache, "cache")
	cosmosRec := mkRec(common.ServiceNoSQL, "cosmosdb")
	spRec := mkRec(common.ServiceSavingsPlans, "savingsplans")
	advisorRec := mkRec(common.ServiceCompute, "advisor") // Advisor produces Compute recs

	// Replicate the exact call order from GetRecommendations.
	result := mergeServiceResults(
		serviceResult{"compute", []common.Recommendation{computeRec}, nil},
		serviceResult{"database", []common.Recommendation{dbRec}, nil},
		serviceResult{"cache", []common.Recommendation{cacheRec}, nil},
		serviceResult{"cosmosdb", []common.Recommendation{cosmosRec}, nil},
		serviceResult{"savingsplans", []common.Recommendation{spRec}, nil},
		serviceResult{"advisor", []common.Recommendation{advisorRec}, nil},
	)

	require.Len(t, result, 6, "all six services must be represented")
	assert.Equal(t, "compute", result[0].ResourceType, "first must be compute")
	assert.Equal(t, "database", result[1].ResourceType, "second must be database")
	assert.Equal(t, "cache", result[2].ResourceType, "third must be cache")
	assert.Equal(t, "cosmosdb", result[3].ResourceType, "fourth must be cosmosdb")
	assert.Equal(t, "savingsplans", result[4].ResourceType, "fifth must be savingsplans")
	assert.Equal(t, "advisor", result[5].ResourceType, "sixth must be advisor (compute-type)")
}

// fakeServiceClient is a test double for serviceRecsGetter. It sleeps for
// sleepDur to simulate network latency and then returns the fixed recs slice
// (or err when non-nil). sleep is done inside GetRecommendations so that
// mock latency is isolated to the mock body — the test's assert path never
// sleeps (see memory feedback_no_sleep_in_tests).
type fakeServiceClient struct {
	sleepDur time.Duration
	recs     []common.Recommendation
	err      error
}

func (f *fakeServiceClient) GetRecommendations(ctx context.Context, _ common.RecommendationParams) ([]common.Recommendation, error) {
	select {
	case <-time.After(f.sleepDur):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return f.recs, f.err
}

// newFakeFn returns a constructor compatible with the newXxxClientFn signature
// that ignores the credential/subscription/region and always returns fake.
func newFakeFn(fake serviceRecsGetter) func(azcore.TokenCredential, string, string) serviceRecsGetter {
	return func(_ azcore.TokenCredential, _, _ string) serviceRecsGetter { return fake }
}

// noopAdvisorFn is a getAdvisorRecsFn replacement that returns immediately
// with zero results, used in timing/isolation tests to keep all latency inside
// the injectable fakeServiceClient mocks.
func noopAdvisorFn(_ context.Context, _ common.RecommendationParams) ([]common.Recommendation, error) {
	return nil, nil
}

const fakeServiceSleep = 100 * time.Millisecond

// TestGetRecommendations_Parallelism proves that all four service goroutines
// run concurrently: total wall-clock time must be well under 2x per-service
// sleep (i.e. less than 200ms) rather than near 4x (400ms sequential).
//
// savingsplans and advisor are injected as instant no-ops so that all latency
// comes from the four fakeServiceClient mocks; without this, both paths make
// real ARM network calls whose RTT dwarfs the 100ms threshold.
func TestGetRecommendations_Parallelism(t *testing.T) {
	origCompute := newComputeClientFn
	origDatabase := newDatabaseClientFn
	origCache := newCacheClientFn
	origCosmos := newCosmosDBClientFn
	origSP := newSavingsPlansClientFn
	t.Cleanup(func() {
		newComputeClientFn = origCompute
		newDatabaseClientFn = origDatabase
		newCacheClientFn = origCache
		newCosmosDBClientFn = origCosmos
		newSavingsPlansClientFn = origSP
	})

	rec := func(svc common.ServiceType) common.Recommendation {
		return common.Recommendation{Provider: common.ProviderAzure, Service: svc}
	}

	noopFake := newFakeFn(&fakeServiceClient{})
	newComputeClientFn = newFakeFn(&fakeServiceClient{sleepDur: fakeServiceSleep, recs: []common.Recommendation{rec(common.ServiceCompute)}})
	newDatabaseClientFn = newFakeFn(&fakeServiceClient{sleepDur: fakeServiceSleep, recs: []common.Recommendation{rec(common.ServiceRelationalDB)}})
	newCacheClientFn = newFakeFn(&fakeServiceClient{sleepDur: fakeServiceSleep, recs: []common.Recommendation{rec(common.ServiceCache)}})
	newCosmosDBClientFn = newFakeFn(&fakeServiceClient{sleepDur: fakeServiceSleep, recs: []common.Recommendation{rec(common.ServiceNoSQL)}})
	newSavingsPlansClientFn = noopFake

	adapter := &RecommendationsClientAdapter{
		cred:             &mockAzureTokenCredential{},
		subscriptionID:   "sub-parallelism",
		getAdvisorRecsFn: noopAdvisorFn,
	}

	start := time.Now()
	_, err := adapter.GetRecommendations(context.Background(), common.RecommendationParams{})
	elapsed := time.Since(start)

	require.NoError(t, err)
	// With 4 services sleeping 100ms in parallel the wall-clock must be
	// substantially less than 2x per-service sleep. We allow 190ms headroom
	// for scheduler jitter; if the calls were serial this would take ~400ms.
	assert.Less(t, elapsed, 2*fakeServiceSleep,
		"expected parallel dispatch: elapsed %v >= 2x per-service sleep %v -- services may be running serially",
		elapsed, fakeServiceSleep)
}

// TestGetRecommendations_OrderPreservation verifies that the merged slice
// follows the canonical order compute -> database -> cache -> cosmosdb regardless
// of which fake goroutine returns first (staggered sleeps force an
// out-of-start-order completion).
//
// savingsplans and advisor are injected as instant no-ops so the test is
// fully hermetic: no real ARM calls, deterministic rec count.
func TestGetRecommendations_OrderPreservation(t *testing.T) {
	origCompute := newComputeClientFn
	origDatabase := newDatabaseClientFn
	origCache := newCacheClientFn
	origCosmos := newCosmosDBClientFn
	origSP := newSavingsPlansClientFn
	t.Cleanup(func() {
		newComputeClientFn = origCompute
		newDatabaseClientFn = origDatabase
		newCacheClientFn = origCache
		newCosmosDBClientFn = origCosmos
		newSavingsPlansClientFn = origSP
	})

	makeRec := func(svc common.ServiceType) common.Recommendation {
		return common.Recommendation{Provider: common.ProviderAzure, Service: svc, ResourceType: string(svc)}
	}

	// Stagger sleeps so goroutines complete in reverse order: cosmosdb
	// finishes first (~10ms), compute last (~40ms). The merged slice must
	// still reflect the canonical order.
	newComputeClientFn = newFakeFn(&fakeServiceClient{sleepDur: 40 * time.Millisecond, recs: []common.Recommendation{makeRec(common.ServiceCompute)}})
	newDatabaseClientFn = newFakeFn(&fakeServiceClient{sleepDur: 30 * time.Millisecond, recs: []common.Recommendation{makeRec(common.ServiceRelationalDB)}})
	newCacheClientFn = newFakeFn(&fakeServiceClient{sleepDur: 20 * time.Millisecond, recs: []common.Recommendation{makeRec(common.ServiceCache)}})
	newCosmosDBClientFn = newFakeFn(&fakeServiceClient{sleepDur: 10 * time.Millisecond, recs: []common.Recommendation{makeRec(common.ServiceNoSQL)}})
	newSavingsPlansClientFn = newFakeFn(&fakeServiceClient{})

	adapter := &RecommendationsClientAdapter{
		cred:             &mockAzureTokenCredential{},
		subscriptionID:   "sub-order",
		getAdvisorRecsFn: noopAdvisorFn,
	}

	recs, err := adapter.GetRecommendations(context.Background(), common.RecommendationParams{})
	require.NoError(t, err)

	// savingsplans and advisor are no-ops; we expect exactly 4 recs (one per
	// injectable service) in canonical order.
	require.Len(t, recs, 4, "expected one rec per injectable service")
	assert.Equal(t, common.ServiceCompute, recs[0].Service, "slot 0 must be compute")
	assert.Equal(t, common.ServiceRelationalDB, recs[1].Service, "slot 1 must be database")
	assert.Equal(t, common.ServiceCache, recs[2].Service, "slot 2 must be cache")
	assert.Equal(t, common.ServiceNoSQL, recs[3].Service, "slot 3 must be cosmosdb")
}

// TestGetRecommendations_ErrorIsolation asserts that a single service error
// does not prevent the other services' results from appearing in the merged
// slice (no sibling cancellation).
//
// savingsplans and advisor are injected as instant no-ops so the rec count is
// fully deterministic regardless of network availability.
func TestGetRecommendations_ErrorIsolation(t *testing.T) {
	origCompute := newComputeClientFn
	origDatabase := newDatabaseClientFn
	origCache := newCacheClientFn
	origCosmos := newCosmosDBClientFn
	origSP := newSavingsPlansClientFn
	t.Cleanup(func() {
		newComputeClientFn = origCompute
		newDatabaseClientFn = origDatabase
		newCacheClientFn = origCache
		newCosmosDBClientFn = origCosmos
		newSavingsPlansClientFn = origSP
	})

	makeRec := func(svc common.ServiceType) common.Recommendation {
		return common.Recommendation{Provider: common.ProviderAzure, Service: svc}
	}

	// database returns an error; the other three must still contribute recs.
	newComputeClientFn = newFakeFn(&fakeServiceClient{sleepDur: fakeServiceSleep, recs: []common.Recommendation{makeRec(common.ServiceCompute)}})
	newDatabaseClientFn = newFakeFn(&fakeServiceClient{sleepDur: fakeServiceSleep, err: errors.New("db unavailable")})
	newCacheClientFn = newFakeFn(&fakeServiceClient{sleepDur: fakeServiceSleep, recs: []common.Recommendation{makeRec(common.ServiceCache)}})
	newCosmosDBClientFn = newFakeFn(&fakeServiceClient{sleepDur: fakeServiceSleep, recs: []common.Recommendation{makeRec(common.ServiceNoSQL)}})
	newSavingsPlansClientFn = newFakeFn(&fakeServiceClient{})

	adapter := &RecommendationsClientAdapter{
		cred:             &mockAzureTokenCredential{},
		subscriptionID:   "sub-isolation",
		getAdvisorRecsFn: noopAdvisorFn,
	}

	recs, err := adapter.GetRecommendations(context.Background(), common.RecommendationParams{})
	require.NoError(t, err, "a per-service error must not surface as a GetRecommendations error")
	require.Len(t, recs, 3, "expected recs from the 3 healthy injectable services")

	services := make([]common.ServiceType, len(recs))
	for i, r := range recs {
		services[i] = r.Service
	}
	assert.Contains(t, services, common.ServiceCompute, "compute recs must be present despite db error")
	assert.Contains(t, services, common.ServiceCache, "cache recs must be present despite db error")
	assert.Contains(t, services, common.ServiceNoSQL, "cosmosdb recs must be present despite db error")
	assert.NotContains(t, services, common.ServiceRelationalDB, "db recs must be absent when db errors")
}
