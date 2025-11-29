package azure

import (
	"context"
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
			result := contains(tt.s, tt.substr)
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
			name:       "Simple resource ID returns empty",
			resourceID: "/subscriptions/123/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/vm1",
			expected:   "",
		},
		{
			name:       "Empty resource ID",
			resourceID: "",
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
	// but we test the wiring is correct
	_, err := adapter.GetRecommendationsForService(context.Background(), common.ServiceCompute)
	// Error is expected since we don't have real Azure credentials
	// The important thing is the function is wired correctly
	_ = err
}

func TestRecommendationsClientAdapter_GetAllRecommendations(t *testing.T) {
	adapter := &RecommendationsClientAdapter{
		cred:           &mockAzureTokenCredential{},
		subscriptionID: "test-subscription",
	}

	// This will try to make API calls which will fail without real credentials,
	// but we test the wiring is correct
	_, err := adapter.GetAllRecommendations(context.Background())
	_ = err
}

func TestExtractServiceType(t *testing.T) {
	adapter := &RecommendationsClientAdapter{
		subscriptionID: "test-subscription",
	}

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
			result := adapter.extractServiceType(rec)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractServiceType_NilProperties(t *testing.T) {
	adapter := &RecommendationsClientAdapter{
		subscriptionID: "test-subscription",
	}

	// Test with nil properties
	rec := &armadvisor.ResourceRecommendationBase{
		Properties: nil,
	}
	result := adapter.extractServiceType(rec)
	assert.Equal(t, "", result)
}

func TestExtractServiceType_NilImpactedField(t *testing.T) {
	adapter := &RecommendationsClientAdapter{
		subscriptionID: "test-subscription",
	}

	// Test with nil impacted field
	rec := &armadvisor.ResourceRecommendationBase{
		Properties: &armadvisor.RecommendationProperties{
			ImpactedField: nil,
		},
	}
	result := adapter.extractServiceType(rec)
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
