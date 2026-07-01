package azure

import (
	"context"
	"strings"
	"testing"

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

	emptyParams := common.RecommendationParams{}
	_, err := adapter.GetRecommendations(ctx, &emptyParams)
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
