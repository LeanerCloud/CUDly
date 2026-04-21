package computeengine

import (
	"context"
	"errors"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/recommender/apiv1/recommenderpb"
	gax "github.com/googleapis/gax-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/genproto/googleapis/type/money"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// MockCommitmentsService mocks the CommitmentsService interface
type MockCommitmentsService struct {
	commitments []*computepb.Commitment
	operation   *MockOperation
	listErr     error
	insertErr   error
	index       int
}

func (m *MockCommitmentsService) List(ctx context.Context, req *computepb.ListRegionCommitmentsRequest) CommitmentsIterator {
	return &MockCommitmentsIterator{commitments: m.commitments, err: m.listErr}
}

func (m *MockCommitmentsService) Insert(ctx context.Context, req *computepb.InsertRegionCommitmentRequest) (CommitmentsOperation, error) {
	if m.insertErr != nil {
		return nil, m.insertErr
	}
	return m.operation, nil
}

func (m *MockCommitmentsService) Close() error {
	return nil
}

// MockCommitmentsIterator mocks the CommitmentsIterator interface
type MockCommitmentsIterator struct {
	commitments []*computepb.Commitment
	index       int
	err         error
}

func (m *MockCommitmentsIterator) Next() (*computepb.Commitment, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.index >= len(m.commitments) {
		return nil, iterator.Done
	}
	c := m.commitments[m.index]
	m.index++
	return c, nil
}

// MockOperation mocks the CommitmentsOperation interface
type MockOperation struct {
	err error
}

func (m *MockOperation) Wait(ctx context.Context, opts ...gax.CallOption) error {
	return m.err
}

// MockMachineTypesService mocks the MachineTypesService interface
type MockMachineTypesService struct {
	machineTypes []*computepb.MachineType
	err          error
}

func (m *MockMachineTypesService) List(ctx context.Context, req *computepb.ListMachineTypesRequest) MachineTypesIterator {
	return &MockMachineTypesIterator{machineTypes: m.machineTypes, err: m.err}
}

func (m *MockMachineTypesService) Close() error {
	return nil
}

// MockMachineTypesIterator mocks the MachineTypesIterator interface
type MockMachineTypesIterator struct {
	machineTypes []*computepb.MachineType
	index        int
	err          error
}

func (m *MockMachineTypesIterator) Next() (*computepb.MachineType, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.index >= len(m.machineTypes) {
		return nil, iterator.Done
	}
	mt := m.machineTypes[m.index]
	m.index++
	return mt, nil
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

func TestComputeEngineClient_GetServiceType(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "project", "region")
	assert.Equal(t, common.ServiceCompute, client.GetServiceType())
}

func TestComputeEngineClient_GetRegion(t *testing.T) {
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
			name:     "Asia Northeast 1",
			region:   "asia-northeast1",
			expected: "asia-northeast1",
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

func TestSkuMatchesMachineType(t *testing.T) {
	tests := []struct {
		name        string
		sku         *cloudbilling.Sku
		machineType string
		region      string
		expected    bool
	}{
		{
			name: "SKU matches machine type and region",
			sku: &cloudbilling.Sku{
				Description:    "n1-standard-1 VM running in Americas",
				ServiceRegions: []string{"us-central1"},
			},
			machineType: "n1-standard-1",
			region:      "us-central1",
			expected:    true,
		},
		{
			name: "SKU matches machine type but not region",
			sku: &cloudbilling.Sku{
				Description:    "n1-standard-1 VM running in Europe",
				ServiceRegions: []string{"europe-west1"},
			},
			machineType: "n1-standard-1",
			region:      "us-central1",
			expected:    false,
		},
		{
			name: "SKU does not match machine type",
			sku: &cloudbilling.Sku{
				Description:    "n2-highmem-4 VM running in Americas",
				ServiceRegions: []string{"us-central1"},
			},
			machineType: "n1-standard-1",
			region:      "us-central1",
			expected:    false,
		},
		{
			name: "SKU with nil service regions matches any region",
			sku: &cloudbilling.Sku{
				Description:    "n1-standard-1 VM",
				ServiceRegions: nil,
			},
			machineType: "n1-standard-1",
			region:      "us-central1",
			expected:    true,
		},
		{
			name: "Case insensitive machine type match",
			sku: &cloudbilling.Sku{
				Description:    "N1-STANDARD-1 VM running in Americas",
				ServiceRegions: []string{"us-central1"},
			},
			machineType: "n1-standard-1",
			region:      "us-central1",
			expected:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := skuMatchesMachineType(tt.sku, tt.machineType, tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestComputePricingStructure(t *testing.T) {
	pricing := ComputePricing{
		HourlyRate:        0.10,
		CommitmentPrice:   876.0,
		OnDemandPrice:     1752.0,
		Currency:          "USD",
		SavingsPercentage: 50.0,
	}

	assert.Equal(t, 0.10, pricing.HourlyRate)
	assert.Equal(t, 876.0, pricing.CommitmentPrice)
	assert.Equal(t, 1752.0, pricing.OnDemandPrice)
	assert.Equal(t, "USD", pricing.Currency)
	assert.Equal(t, 50.0, pricing.SavingsPercentage)
}

func TestStringPtr(t *testing.T) {
	s := "test"
	ptr := stringPtr(s)
	require.NotNil(t, ptr)
	assert.Equal(t, "test", *ptr)
}

func TestInt64Ptr(t *testing.T) {
	i := int64(42)
	ptr := int64Ptr(i)
	require.NotNil(t, ptr)
	assert.Equal(t, int64(42), *ptr)
}

func TestComputeEngineClient_ValidateOffering_NoCredentials(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	rec := common.Recommendation{
		ResourceType: "n1-standard-1",
	}

	// Will fail without credentials
	err := client.ValidateOffering(ctx, rec)
	assert.Error(t, err)
}

func TestComputeEngineClient_GetExistingCommitments_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	name := "commitment-1"
	status := "ACTIVE"
	commitmentType := "GENERAL_PURPOSE"
	resourceType := "n1-standard-1"

	mockService := &MockCommitmentsService{
		commitments: []*computepb.Commitment{
			{
				Name:   &name,
				Status: &status,
				Type:   &commitmentType,
				Resources: []*computepb.ResourceCommitment{
					{Type: &resourceType},
				},
			},
		},
		operation: &MockOperation{},
	}
	client.SetCommitmentsService(mockService)

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	require.Len(t, commitments, 1)
	assert.Equal(t, "commitment-1", commitments[0].CommitmentID)
	assert.Equal(t, "active", commitments[0].State)
	assert.Equal(t, "n1-standard-1", commitments[0].ResourceType)
}

func TestComputeEngineClient_GetExistingCommitments_Error(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockCommitmentsService{
		listErr: errors.New("API error"),
	}
	client.SetCommitmentsService(mockService)

	_, err := client.GetExistingCommitments(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list commitments")
}

func TestComputeEngineClient_GetExistingCommitments_NilName(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockCommitmentsService{
		commitments: []*computepb.Commitment{
			{Name: nil}, // Should be skipped
		},
	}
	client.SetCommitmentsService(mockService)

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestComputeEngineClient_GetValidResourceTypes_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	name1, name2 := "n1-standard-1", "n1-standard-2"
	mockService := &MockMachineTypesService{
		machineTypes: []*computepb.MachineType{
			{Name: &name1},
			{Name: &name2},
		},
	}
	client.SetMachineTypesService(mockService)

	types, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	assert.Len(t, types, 2)
	assert.Contains(t, types, "n1-standard-1")
	assert.Contains(t, types, "n1-standard-2")
}

func TestComputeEngineClient_GetValidResourceTypes_Error(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockMachineTypesService{
		err: errors.New("API error"),
	}
	client.SetMachineTypesService(mockService)

	_, err := client.GetValidResourceTypes(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list machine types")
}

func TestComputeEngineClient_GetValidResourceTypes_Empty(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockMachineTypesService{
		machineTypes: []*computepb.MachineType{},
	}
	client.SetMachineTypesService(mockService)

	_, err := client.GetValidResourceTypes(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no machine types found")
}

func TestComputeEngineClient_ValidateOffering_Valid(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	name := "n1-standard-1"
	mockService := &MockMachineTypesService{
		machineTypes: []*computepb.MachineType{
			{Name: &name},
		},
	}
	client.SetMachineTypesService(mockService)

	rec := common.Recommendation{ResourceType: "n1-standard-1"}
	err := client.ValidateOffering(ctx, rec)
	assert.NoError(t, err)
}

func TestComputeEngineClient_ValidateOffering_Invalid(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	name := "n1-standard-1"
	mockService := &MockMachineTypesService{
		machineTypes: []*computepb.MachineType{
			{Name: &name},
		},
	}
	client.SetMachineTypesService(mockService)

	rec := common.Recommendation{ResourceType: "invalid-type"}
	err := client.ValidateOffering(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid GCP machine type")
}

func TestComputeEngineClient_PurchaseCommitment_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockCommitmentsService{
		operation: &MockOperation{err: nil},
	}
	client.SetCommitmentsService(mockService)

	rec := common.Recommendation{
		ResourceType:   "n1-standard-1",
		Term:           "1yr",
		CommitmentCost: 1000.0,
		Count:          5,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.NotEmpty(t, result.CommitmentID)
	assert.Equal(t, 1000.0, result.Cost)
}

func TestComputeEngineClient_PurchaseCommitment_3Year(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockCommitmentsService{
		operation: &MockOperation{err: nil},
	}
	client.SetCommitmentsService(mockService)

	rec := common.Recommendation{
		ResourceType: "n1-standard-1",
		Term:         "3yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestComputeEngineClient_PurchaseCommitment_InsertError(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockCommitmentsService{
		insertErr: errors.New("API error"),
	}
	client.SetCommitmentsService(mockService)

	rec := common.Recommendation{ResourceType: "n1-standard-1"}

	result, err := client.PurchaseCommitment(ctx, rec)
	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to create commitment")
}

func TestComputeEngineClient_PurchaseCommitment_WaitError(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockCommitmentsService{
		operation: &MockOperation{err: errors.New("operation failed")},
	}
	client.SetCommitmentsService(mockService)

	rec := common.Recommendation{ResourceType: "n1-standard-1"}

	result, err := client.PurchaseCommitment(ctx, rec)
	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "commitment creation failed")
}

func TestComputeEngineClient_GetOfferingDetails_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: []*cloudbilling.Sku{
				{
					Description:    "n1-standard-1 VM running in Americas",
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
		ResourceType:  "n1-standard-1",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	assert.Equal(t, "n1-standard-1", details.ResourceType)
	assert.Equal(t, "1yr", details.Term)
	assert.Equal(t, "USD", details.Currency)
	assert.Greater(t, details.TotalCost, float64(0))
}

func TestComputeEngineClient_GetOfferingDetails_NoPricing(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: []*cloudbilling.Sku{},
		},
	}
	client.SetBillingService(mockService)

	rec := common.Recommendation{ResourceType: "n1-standard-1"}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no on-demand pricing found")
}

func TestComputeEngineClient_GetRecommendations_WithMock(t *testing.T) {
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
								{Resource: "projects/test/machineTypes/n1-standard-1"},
							},
						},
					},
				},
			},
		},
	}

	mockClient := &MockRecommenderClient{iterator: mockIterator}
	client.SetRecommenderClient(mockClient)

	recommendations, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	require.NoError(t, err)
	assert.Len(t, recommendations, 1)
	assert.Equal(t, common.ProviderGCP, recommendations[0].Provider)
	assert.Equal(t, common.ServiceCompute, recommendations[0].Service)
	assert.True(t, mockClient.closed)
}

// TestComputeEngineClient_GetRecommendations_IteratorError pins the
// "iterator error propagates, no silent swallow" contract that commit
// f75aa6cf4 introduced. Memorystore's test (TestMemorystoreClient_
// GetRecommendations_WithMockClient) already covers this shape; adding
// the same coverage for computeengine closes the test-parity gap flagged
// in known_issues/11_gcp_provider.md.
func TestComputeEngineClient_GetRecommendations_IteratorError(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockIterator := &MockRecommenderIterator{
		err: errors.New("injected iterator failure"),
	}
	mockClient := &MockRecommenderClient{iterator: mockIterator}
	client.SetRecommenderClient(mockClient)

	recs, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "computeengine: iterate recommendations")
	assert.Nil(t, recs, "partial data must not leak on iterator failure")
	assert.True(t, mockClient.closed, "client must still be closed on the error path")
}

func TestComputeEngineClient_GetRecommendations_Empty(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockIterator := &MockRecommenderIterator{
		recommendations: []*recommenderpb.Recommendation{},
	}

	mockClient := &MockRecommenderClient{iterator: mockIterator}
	client.SetRecommenderClient(mockClient)

	recommendations, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	require.NoError(t, err)
	assert.Empty(t, recommendations)
}

func TestComputeEngineClient_SetterMethods(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// Test SetCommitmentsService
	mockCommit := &MockCommitmentsService{}
	client.SetCommitmentsService(mockCommit)
	assert.Equal(t, mockCommit, client.commitmentsService)

	// Test SetMachineTypesService
	mockMT := &MockMachineTypesService{}
	client.SetMachineTypesService(mockMT)
	assert.Equal(t, mockMT, client.machineTypesService)

	// Test SetBillingService
	mockBilling := &MockBillingService{}
	client.SetBillingService(mockBilling)
	assert.Equal(t, mockBilling, client.billingService)

	// Test SetRecommenderClient
	mockRec := &MockRecommenderClient{}
	client.SetRecommenderClient(mockRec)
	assert.Equal(t, mockRec, client.recommenderClient)
}

func TestComputeEngineClient_ConvertGCPRecommendation(t *testing.T) {
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
						Nanos:        -500000000,
						CurrencyCode: "USD",
					},
				},
			},
		},
		Content: &recommenderpb.RecommendationContent{
			OperationGroups: []*recommenderpb.OperationGroup{
				{
					Operations: []*recommenderpb.Operation{
						{Resource: "projects/test/machineTypes/n1-standard-4"},
					},
				},
			},
		},
	}

	rec := client.convertGCPRecommendation(ctx, gcpRec)
	require.NotNil(t, rec)
	assert.Equal(t, common.ProviderGCP, rec.Provider)
	assert.Equal(t, common.ServiceCompute, rec.Service)
	assert.Equal(t, "test-project", rec.Account)
	assert.Equal(t, "us-central1", rec.Region)
	assert.Equal(t, "n1-standard-4", rec.ResourceType)
	assert.Equal(t, 50.5, rec.EstimatedSavings)
}
