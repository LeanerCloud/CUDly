package cloudsql

import (
	"context"
	"errors"
	"testing"

	"cloud.google.com/go/recommender/apiv1/recommenderpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/api/sqladmin/v1"
	"google.golang.org/genproto/googleapis/type/money"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// MockSQLAdminService mocks the SQLAdminService interface
type MockSQLAdminService struct {
	instances *sqladmin.InstancesListResponse
	tiers     *sqladmin.TiersListResponse
	operation *sqladmin.Operation
	err       error
}

func (m *MockSQLAdminService) ListInstances(projectID string) (*sqladmin.InstancesListResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.instances, nil
}

func (m *MockSQLAdminService) InsertInstance(projectID string, instance *sqladmin.DatabaseInstance) (*sqladmin.Operation, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.operation, nil
}

func (m *MockSQLAdminService) ListTiers(projectID string) (*sqladmin.TiersListResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.tiers, nil
}

// MockBillingService mocks the BillingService interface
type MockBillingService struct {
	skus *cloudbilling.ListSkusResponse
	err  error
}

func (m *MockBillingService) ListSKUs(serviceID string) (*cloudbilling.ListSkusResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.skus, nil
}

// MockRecommenderIterator mocks the RecommenderIterator interface
type MockRecommenderIterator struct {
	recommendations []*recommenderpb.Recommendation
	index           int
	err             error
}

func (m *MockRecommenderIterator) Next() (*recommenderpb.Recommendation, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.index >= len(m.recommendations) {
		return nil, iterator.Done
	}
	rec := m.recommendations[m.index]
	m.index++
	return rec, nil
}

// MockRecommenderClient mocks the RecommenderClient interface
type MockRecommenderClient struct {
	iterator RecommenderIterator
	closed   bool
}

func (m *MockRecommenderClient) ListRecommendations(ctx context.Context, req *recommenderpb.ListRecommendationsRequest) RecommenderIterator {
	return m.iterator
}

func (m *MockRecommenderClient) Close() error {
	m.closed = true
	return nil
}

func TestNewClient(t *testing.T) {
	ctx := context.Background()
	client, err := NewClient(ctx, "test-project", "us-central1")

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Equal(t, "test-project", client.projectID)
	assert.Equal(t, "us-central1", client.region)
}

func TestCloudSQLClient_GetServiceType(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "project", "region")
	assert.Equal(t, common.ServiceRelationalDB, client.GetServiceType())
}

func TestCloudSQLClient_GetRegion(t *testing.T) {
	tests := []struct {
		name     string
		region   string
		expected string
	}{
		{
			name:     "US Central 1",
			region:   "us-central1",
			expected: "us-central1",
		},
		{
			name:     "Europe West 1",
			region:   "europe-west1",
			expected: "europe-west1",
		},
		{
			name:     "Asia East 1",
			region:   "asia-east1",
			expected: "asia-east1",
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := NewClient(ctx, "project", tt.region)
			assert.Equal(t, tt.expected, client.GetRegion())
		})
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		name     string
		slice    []string
		str      string
		expected bool
	}{
		{
			name:     "String found in slice",
			slice:    []string{"us-central1", "us-east1", "us-west1"},
			str:      "us-central1",
			expected: true,
		},
		{
			name:     "String not found in slice",
			slice:    []string{"us-central1", "us-east1", "us-west1"},
			str:      "europe-west1",
			expected: false,
		},
		{
			name:     "Case insensitive match",
			slice:    []string{"US-CENTRAL1", "US-EAST1"},
			str:      "us-central1",
			expected: true,
		},
		{
			name:     "Empty slice",
			slice:    []string{},
			str:      "any",
			expected: false,
		},
		{
			name:     "Empty string search",
			slice:    []string{"us-central1"},
			str:      "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := contains(tt.slice, tt.str)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSkuMatchesTier(t *testing.T) {
	tests := []struct {
		name     string
		sku      *cloudbilling.Sku
		tier     string
		region   string
		expected bool
	}{
		{
			name: "SKU matches tier and region",
			sku: &cloudbilling.Sku{
				Description:    "db-n1-standard-1 Cloud SQL",
				ServiceRegions: []string{"us-central1"},
			},
			tier:     "db-n1-standard-1",
			region:   "us-central1",
			expected: true,
		},
		{
			name: "SKU matches tier but not region",
			sku: &cloudbilling.Sku{
				Description:    "db-n1-standard-1 Cloud SQL",
				ServiceRegions: []string{"us-east1"},
			},
			tier:     "db-n1-standard-1",
			region:   "us-central1",
			expected: false,
		},
		{
			name: "SKU does not match tier",
			sku: &cloudbilling.Sku{
				Description:    "db-n1-highmem-2 Cloud SQL",
				ServiceRegions: []string{"us-central1"},
			},
			tier:     "db-n1-standard-1",
			region:   "us-central1",
			expected: false,
		},
		{
			name: "SKU with nil service regions matches any region",
			sku: &cloudbilling.Sku{
				Description:    "db-n1-standard-1 Cloud SQL",
				ServiceRegions: nil,
			},
			tier:     "db-n1-standard-1",
			region:   "us-central1",
			expected: true,
		},
		{
			name: "Case insensitive tier match",
			sku: &cloudbilling.Sku{
				Description:    "DB-N1-Standard-1 Cloud SQL",
				ServiceRegions: []string{"us-central1"},
			},
			tier:     "db-n1-standard-1",
			region:   "us-central1",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := skuMatchesTier(tt.sku, tt.tier, tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSQLPricingStructure(t *testing.T) {
	pricing := SQLPricing{
		HourlyRate:        0.05,
		CommitmentPrice:   438.0,
		OnDemandPrice:     876.0,
		Currency:          "USD",
		SavingsPercentage: 50.0,
	}

	assert.Equal(t, 0.05, pricing.HourlyRate)
	assert.Equal(t, 438.0, pricing.CommitmentPrice)
	assert.Equal(t, 876.0, pricing.OnDemandPrice)
	assert.Equal(t, "USD", pricing.Currency)
	assert.Equal(t, 50.0, pricing.SavingsPercentage)
}

func TestCloudSQLClient_ValidateOffering_NoCredentials(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	rec := common.Recommendation{
		ResourceType: "db-n1-standard-1",
	}

	// Will fail without credentials
	err := client.ValidateOffering(ctx, rec)
	assert.Error(t, err)
}

func TestCloudSQLClient_GetExistingCommitments_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockSQLAdminService{
		instances: &sqladmin.InstancesListResponse{
			Items: []*sqladmin.DatabaseInstance{
				{
					Name:            "instance-1",
					Region:          "us-central1",
					State:           "RUNNABLE",
					DatabaseVersion: "MYSQL_8_0",
					Settings: &sqladmin.Settings{
						Tier:        "db-n1-standard-1",
						PricingPlan: "PACKAGE",
					},
				},
				{
					Name:            "instance-2",
					Region:          "us-central1",
					State:           "RUNNABLE",
					DatabaseVersion: "POSTGRES_14",
					Settings: &sqladmin.Settings{
						Tier:        "db-n1-standard-2",
						PricingPlan: "PER_USE", // Not a commitment
					},
				},
			},
		},
	}
	client.SetSQLAdminService(mockService)

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	require.Len(t, commitments, 1)
	assert.Equal(t, "instance-1", commitments[0].CommitmentID)
	assert.Equal(t, "db-n1-standard-1", commitments[0].ResourceType)
	assert.Equal(t, common.ServiceRelationalDB, commitments[0].Service)
}

func TestCloudSQLClient_GetExistingCommitments_Error(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockSQLAdminService{
		err: errors.New("API error"),
	}
	client.SetSQLAdminService(mockService)

	_, err := client.GetExistingCommitments(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list SQL instances")
}

func TestCloudSQLClient_GetExistingCommitments_Empty(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockSQLAdminService{
		instances: &sqladmin.InstancesListResponse{
			Items: []*sqladmin.DatabaseInstance{},
		},
	}
	client.SetSQLAdminService(mockService)

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestCloudSQLClient_GetValidResourceTypes_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockSQLAdminService{
		tiers: &sqladmin.TiersListResponse{
			Items: []*sqladmin.Tier{
				{Tier: "db-n1-standard-1", Region: []string{"us-central1", "us-east1"}},
				{Tier: "db-n1-standard-2", Region: []string{"us-central1"}},
				{Tier: "db-n1-standard-4", Region: []string{"us-east1"}}, // Different region
				{Tier: "db-n1-highmem-2", Region: []string{}},            // All regions
			},
		},
	}
	client.SetSQLAdminService(mockService)

	tiers, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	assert.Contains(t, tiers, "db-n1-standard-1")
	assert.Contains(t, tiers, "db-n1-standard-2")
	assert.Contains(t, tiers, "db-n1-highmem-2")
	assert.NotContains(t, tiers, "db-n1-standard-4")
}

func TestCloudSQLClient_GetValidResourceTypes_Error(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockSQLAdminService{
		err: errors.New("API error"),
	}
	client.SetSQLAdminService(mockService)

	_, err := client.GetValidResourceTypes(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list SQL tiers")
}

func TestCloudSQLClient_GetValidResourceTypes_NoTiers(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockSQLAdminService{
		tiers: &sqladmin.TiersListResponse{
			Items: []*sqladmin.Tier{
				{Tier: "db-n1-standard-4", Region: []string{"us-east1"}}, // Different region only
			},
		},
	}
	client.SetSQLAdminService(mockService)

	_, err := client.GetValidResourceTypes(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no Cloud SQL tiers found")
}

func TestCloudSQLClient_ValidateOffering_Valid(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockSQLAdminService{
		tiers: &sqladmin.TiersListResponse{
			Items: []*sqladmin.Tier{
				{Tier: "db-n1-standard-1", Region: []string{"us-central1"}},
			},
		},
	}
	client.SetSQLAdminService(mockService)

	rec := common.Recommendation{
		ResourceType: "db-n1-standard-1",
	}

	err := client.ValidateOffering(ctx, rec)
	assert.NoError(t, err)
}

func TestCloudSQLClient_ValidateOffering_Invalid(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockSQLAdminService{
		tiers: &sqladmin.TiersListResponse{
			Items: []*sqladmin.Tier{
				{Tier: "db-n1-standard-1", Region: []string{"us-central1"}},
			},
		},
	}
	client.SetSQLAdminService(mockService)

	rec := common.Recommendation{
		ResourceType: "invalid-tier",
	}

	err := client.ValidateOffering(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Cloud SQL tier")
}

func TestCloudSQLClient_PurchaseCommitment_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockSQLAdminService{
		operation: &sqladmin.Operation{
			Status: "DONE",
		},
	}
	client.SetSQLAdminService(mockService)

	rec := common.Recommendation{
		ResourceType:   "db-n1-standard-1",
		CommitmentCost: 1000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.NotEmpty(t, result.CommitmentID)
	assert.Equal(t, 1000.0, result.Cost)
}

func TestCloudSQLClient_PurchaseCommitment_InProgress(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockSQLAdminService{
		operation: &sqladmin.Operation{
			Status: "RUNNING",
		},
	}
	client.SetSQLAdminService(mockService)

	rec := common.Recommendation{
		ResourceType: "db-n1-standard-1",
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "instance creation in progress")
}

func TestCloudSQLClient_PurchaseCommitment_Error(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockSQLAdminService{
		err: errors.New("API error"),
	}
	client.SetSQLAdminService(mockService)

	rec := common.Recommendation{
		ResourceType: "db-n1-standard-1",
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to create SQL instance")
}

func TestCloudSQLClient_GetOfferingDetails_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: []*cloudbilling.Sku{
				{
					Description:    "db-n1-standard-1 Cloud SQL",
					ServiceRegions: []string{"us-central1"},
					PricingInfo: []*cloudbilling.PricingInfo{
						{
							PricingExpression: &cloudbilling.PricingExpression{
								TieredRates: []*cloudbilling.TierRate{
									{
										UnitPrice: &cloudbilling.Money{
											Units:        0,
											Nanos:        50000000, // 0.05 per hour
											CurrencyCode: "USD",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	client.SetBillingService(mockService)

	rec := common.Recommendation{
		ResourceType:  "db-n1-standard-1",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	assert.Equal(t, "db-n1-standard-1", details.ResourceType)
	assert.Equal(t, "1yr", details.Term)
	assert.Equal(t, "USD", details.Currency)
	assert.Greater(t, details.TotalCost, float64(0))
}

func TestCloudSQLClient_GetOfferingDetails_3Year(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: []*cloudbilling.Sku{
				{
					Description:    "db-n1-standard-1 Cloud SQL",
					ServiceRegions: []string{"us-central1"},
					PricingInfo: []*cloudbilling.PricingInfo{
						{
							PricingExpression: &cloudbilling.PricingExpression{
								TieredRates: []*cloudbilling.TierRate{
									{
										UnitPrice: &cloudbilling.Money{
											Units:        0,
											Nanos:        50000000,
											CurrencyCode: "USD",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	client.SetBillingService(mockService)

	rec := common.Recommendation{
		ResourceType:  "db-n1-standard-1",
		Term:          "3yr",
		PaymentOption: "monthly",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	assert.Equal(t, "3yr", details.Term)
	assert.Equal(t, "monthly", details.PaymentOption)
	assert.Equal(t, float64(0), details.UpfrontCost)
	assert.Greater(t, details.RecurringCost, float64(0))
}

func TestCloudSQLClient_GetOfferingDetails_NoPricing(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: []*cloudbilling.Sku{}, // No matching SKUs
		},
	}
	client.SetBillingService(mockService)

	rec := common.Recommendation{
		ResourceType: "db-n1-standard-1",
	}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no pricing found")
}

func TestCloudSQLClient_GetOfferingDetails_APIError(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockBillingService{
		err: errors.New("API error"),
	}
	client.SetBillingService(mockService)

	rec := common.Recommendation{
		ResourceType: "db-n1-standard-1",
	}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list SKUs")
}

func TestCloudSQLClient_GetRecommendations_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockIterator := &MockRecommenderIterator{
		recommendations: []*recommenderpb.Recommendation{
			{
				Name: "recommendation-1",
				PrimaryImpact: &recommenderpb.Impact{
					Category: recommenderpb.Impact_COST,
					Projection: &recommenderpb.Impact_CostProjection{
						CostProjection: &recommenderpb.CostProjection{
							Cost: &money.Money{
								Units:        -100,
								Nanos:        0,
								CurrencyCode: "USD",
							},
						},
					},
				},
				Content: &recommenderpb.RecommendationContent{
					OperationGroups: []*recommenderpb.OperationGroup{
						{
							Operations: []*recommenderpb.Operation{
								{
									Resource: "projects/test/instances/my-instance",
								},
							},
						},
					},
				},
			},
		},
	}

	mockClient := &MockRecommenderClient{
		iterator: mockIterator,
	}
	client.SetRecommenderClient(mockClient)

	recommendations, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	require.NoError(t, err)
	assert.Len(t, recommendations, 1)
	assert.Equal(t, common.ProviderGCP, recommendations[0].Provider)
	assert.Equal(t, common.ServiceRelationalDB, recommendations[0].Service)
	assert.True(t, mockClient.closed)
}

func TestCloudSQLClient_GetRecommendations_IteratorError(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockIterator := &MockRecommenderIterator{
		err: errors.New("injected iterator failure"),
	}
	mockClient := &MockRecommenderClient{iterator: mockIterator}
	client.SetRecommenderClient(mockClient)

	recs, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cloudsql: iterate recommendations")
	assert.Nil(t, recs, "partial data must not leak on iterator failure")
	assert.True(t, mockClient.closed, "client must still be closed on the error path")
}

func TestCloudSQLClient_GetRecommendations_Empty(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockIterator := &MockRecommenderIterator{
		recommendations: []*recommenderpb.Recommendation{},
	}

	mockClient := &MockRecommenderClient{
		iterator: mockIterator,
	}
	client.SetRecommenderClient(mockClient)

	recommendations, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	require.NoError(t, err)
	assert.Empty(t, recommendations)
}

func TestCloudSQLClient_SetterMethods(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// Test SetSQLAdminService
	mockSQL := &MockSQLAdminService{}
	client.SetSQLAdminService(mockSQL)
	assert.Equal(t, mockSQL, client.sqlAdminService)

	// Test SetBillingService
	mockBilling := &MockBillingService{}
	client.SetBillingService(mockBilling)
	assert.Equal(t, mockBilling, client.billingService)

	// Test SetRecommenderClient
	mockRec := &MockRecommenderClient{}
	client.SetRecommenderClient(mockRec)
	assert.Equal(t, mockRec, client.recommenderClient)
}

func TestCloudSQLClient_ConvertGCPRecommendation(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	gcpRec := &recommenderpb.Recommendation{
		Name: "test-rec",
		PrimaryImpact: &recommenderpb.Impact{
			Category: recommenderpb.Impact_COST,
			Projection: &recommenderpb.Impact_CostProjection{
				CostProjection: &recommenderpb.CostProjection{
					Cost: &money.Money{
						Units:        -50,
						Nanos:        -500000000, // -0.5
						CurrencyCode: "USD",
					},
				},
			},
		},
		Content: &recommenderpb.RecommendationContent{
			OperationGroups: []*recommenderpb.OperationGroup{
				{
					Operations: []*recommenderpb.Operation{
						{
							Resource: "projects/test/instances/sql-instance",
						},
					},
				},
			},
		},
	}

	rec := client.convertGCPRecommendation(ctx, gcpRec)
	require.NotNil(t, rec)
	assert.Equal(t, common.ProviderGCP, rec.Provider)
	assert.Equal(t, common.ServiceRelationalDB, rec.Service)
	assert.Equal(t, "test-project", rec.Account)
	assert.Equal(t, "us-central1", rec.Region)
	assert.Equal(t, "sql-instance", rec.ResourceType)
	assert.Equal(t, 50.5, rec.EstimatedSavings)
}

func TestCloudSQLClient_ConvertGCPRecommendation_NilContent(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	gcpRec := &recommenderpb.Recommendation{
		Name:    "test-rec",
		Content: nil,
	}

	rec := client.convertGCPRecommendation(ctx, gcpRec)
	require.NotNil(t, rec)
	assert.Equal(t, common.ProviderGCP, rec.Provider)
}
