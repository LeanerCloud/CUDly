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
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/scorer"
)

// MockCommitmentsService mocks the CommitmentsService interface
type MockCommitmentsService struct {
	commitments   []*computepb.Commitment
	operation     *MockOperation
	listErr       error
	insertErr     error
	index         int
	lastInsertReq *computepb.InsertRegionCommitmentRequest   // captured for assertions
	insertReqs    []*computepb.InsertRegionCommitmentRequest // every Insert call (re-drive assertions)
}

func (m *MockCommitmentsService) List(ctx context.Context, req *computepb.ListRegionCommitmentsRequest) CommitmentsIterator {
	return &MockCommitmentsIterator{commitments: m.commitments, err: m.listErr}
}

func (m *MockCommitmentsService) Insert(ctx context.Context, req *computepb.InsertRegionCommitmentRequest) (CommitmentsOperation, error) {
	m.lastInsertReq = req
	m.insertReqs = append(m.insertReqs, req)
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

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.NotEmpty(t, result.CommitmentID)
	assert.Equal(t, 1000.0, result.Cost)
}

func TestComputeEngineClient_PurchaseCommitment_EncodesSourceInDescription(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockCommitmentsService{operation: &MockOperation{err: nil}}
	client.SetCommitmentsService(mockService)

	rec := common.Recommendation{ResourceType: "n1-standard-1", Term: "1yr", Count: 1}

	_, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceWeb})
	require.NoError(t, err)
	require.NotNil(t, mockService.lastInsertReq)
	require.NotNil(t, mockService.lastInsertReq.CommitmentResource)
	desc := mockService.lastInsertReq.CommitmentResource.GetDescription()
	assert.Contains(t, desc, "["+common.PurchaseTagKey+"="+common.PurchaseSourceWeb+"]")
}

func TestComputeEngineClient_PurchaseCommitment_OmitsTagWhenSourceEmpty(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockCommitmentsService{operation: &MockOperation{err: nil}}
	client.SetCommitmentsService(mockService)

	rec := common.Recommendation{ResourceType: "n1-standard-1", Term: "1yr", Count: 1}

	_, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
	require.NoError(t, err)
	require.NotNil(t, mockService.lastInsertReq)
	desc := mockService.lastInsertReq.CommitmentResource.GetDescription()
	assert.NotContains(t, desc, common.PurchaseTagKey)
}

// TestComputeEngineClient_PurchaseCommitment_IdempotentReDrive is the issue #654
// regression: re-driving the same execution (identical IdempotencyToken) must
// not create a second CUD. We assert the second Insert carries the *same*
// deterministic RequestId (GCP's native server-side dedupe key) and the *same*
// commitment Name (the defense-in-depth ALREADY_EXISTS guard) as the first, so
// GCP treats the re-drive as a no-op rather than a double-purchase.
func TestComputeEngineClient_PurchaseCommitment_IdempotentReDrive(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockCommitmentsService{operation: &MockOperation{err: nil}}
	client.SetCommitmentsService(mockService)

	rec := common.Recommendation{ResourceType: "n1-standard-1", Term: "1yr", Count: 5}
	token := common.DeriveIdempotencyToken("exec-654", 0)
	opts := common.PurchaseOptions{Source: common.PurchaseSourceWeb, IdempotencyToken: token}

	r1, err := client.PurchaseCommitment(ctx, rec, opts)
	require.NoError(t, err)
	require.True(t, r1.Success)

	r2, err := client.PurchaseCommitment(ctx, rec, opts)
	require.NoError(t, err)
	require.True(t, r2.Success)

	require.Len(t, mockService.insertReqs, 2, "both calls reach Insert; GCP dedupes server-side on RequestId")

	first, second := mockService.insertReqs[0], mockService.insertReqs[1]

	// Native idempotency key: same token -> same non-empty valid UUID RequestId.
	wantGUID := common.IdempotencyGUID(token)
	require.NotEmpty(t, wantGUID, "token must yield a valid idempotency GUID")
	assert.Equal(t, wantGUID, first.GetRequestId(), "first RequestId must be the derived GUID")
	assert.Equal(t, first.GetRequestId(), second.GetRequestId(), "re-drive must reuse the same RequestId (no double-buy)")
	assert.NotEqual(t, "00000000-0000-0000-0000-000000000000", first.GetRequestId(), "zero UUID is rejected by GCP")

	// Defense in depth: same token -> same deterministic commitment name.
	require.NotNil(t, first.CommitmentResource)
	require.NotNil(t, second.CommitmentResource)
	assert.Equal(t, first.CommitmentResource.GetName(), second.CommitmentResource.GetName(),
		"re-drive must reuse the same commitment name (GCP rejects the duplicate with ALREADY_EXISTS)")
	assert.Equal(t, r1.CommitmentID, r2.CommitmentID, "re-drive must report the same commitment ID")
}

// TestComputeEngineClient_PurchaseCommitment_EmptyTokenNoRequestID confirms the
// CLI path (no owning execution, empty token) keeps its prior non-idempotent
// behaviour: no RequestId is set and the name is the timestamp-based fallback.
func TestComputeEngineClient_PurchaseCommitment_EmptyTokenNoRequestID(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockCommitmentsService{operation: &MockOperation{err: nil}}
	client.SetCommitmentsService(mockService)

	rec := common.Recommendation{ResourceType: "n1-standard-1", Term: "1yr", Count: 1}

	_, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
	require.NoError(t, err)
	require.NotNil(t, mockService.lastInsertReq)
	assert.Empty(t, mockService.lastInsertReq.GetRequestId(), "empty token must not set a RequestId")
	require.NotNil(t, mockService.lastInsertReq.CommitmentResource)
	assert.Contains(t, mockService.lastInsertReq.CommitmentResource.GetName(), "cud-",
		"empty token keeps the timestamp-based name")
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
		Count:        4, // must be > 0 after issue #1022 guard
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
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

	// Count must be > 0 so the guard passes and the insert error is exercised.
	rec := common.Recommendation{ResourceType: "n1-standard-1", Count: 2}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
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

	// Count must be > 0 so the guard passes and the wait error is exercised.
	rec := common.Recommendation{ResourceType: "n1-standard-1", Count: 2}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "commitment creation failed")
}

func TestComputeEngineClient_GetOfferingDetails_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// Both on-demand and commitment SKUs are required; without a commitment SKU
	// getComputePricing now returns an error (issue #1020 fix).
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
				{
					Description:    "n1-standard-1 commitment 1yr in Americas",
					ServiceRegions: []string{"us-central1"},
					PricingInfo: []*cloudbilling.PricingInfo{
						{
							PricingExpression: &cloudbilling.PricingExpression{
								TieredRates: []*cloudbilling.TierRate{
									{
										UnitPrice: &cloudbilling.Money{
											Units:        0,
											Nanos:        32000000,
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

	rec := client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{})
	require.NotNil(t, rec)
	assert.Equal(t, common.ProviderGCP, rec.Provider)
	assert.Equal(t, common.ServiceCompute, rec.Service)
	assert.Equal(t, "test-project", rec.Account)
	assert.Equal(t, "us-central1", rec.Region)
	assert.Equal(t, "n1-standard-4", rec.ResourceType)
	assert.Equal(t, 50.5, rec.EstimatedSavings)
}

// infiniteRecommenderIterator never signals iterator.Done, used to exercise
// the ctx-cancel guard and the maxRecsPages budget cap.
type infiniteRecommenderIterator struct{}

func (i *infiniteRecommenderIterator) Next() (*recommenderpb.Recommendation, error) {
	return &recommenderpb.Recommendation{}, nil
}

type infiniteRecommenderClient struct{}

func (c *infiniteRecommenderClient) ListRecommendations(_ context.Context, _ *recommenderpb.ListRecommendationsRequest) RecommenderIterator {
	return &infiniteRecommenderIterator{}
}

func (c *infiniteRecommenderClient) Close() error { return nil }

// TestComputeEngineClient_GetRecommendations_CtxCancelReturnsError asserts
// that a cancelled context is treated as a terminal stop and returns an error
// rather than silently producing a partial result set
// (feedback_ctx_cancel_terminal).
func TestComputeEngineClient_GetRecommendations_CtxCancelReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the call

	client, err := NewClient(context.Background(), "test-project", "us-central1")
	require.NoError(t, err)
	client.SetRecommenderClient(&infiniteRecommenderClient{})

	_, err = client.GetRecommendations(ctx, common.RecommendationParams{})
	require.Error(t, err, "cancelled context must surface an error, not a partial result set")
}

// TestComputeEngineClient_GetRecommendations_PageCapFires asserts that the
// iteration budget terminates an infinite iterator rather than looping forever.
func TestComputeEngineClient_GetRecommendations_PageCapFires(t *testing.T) {
	client, err := NewClient(context.Background(), "test-project", "us-central1")
	require.NoError(t, err)
	client.SetRecommenderClient(&infiniteRecommenderClient{})

	_, err = client.GetRecommendations(context.Background(), common.RecommendationParams{})
	require.Error(t, err, "page cap must surface an error when the iterator never terminates")
}

// realisticCUDRecommendation builds a realistic GCP Commitment Recommender payload
// for a 4-vCPU n1-standard-4 commitment in us-central1. The operation group has two ops:
//  1. A machine-type op whose resource path ends in the machine type (n1-standard-4) --
//     used by extractResourceTypeFromOperations to set rec.ResourceType.
//  2. A commitment resource op whose numeric Value is the VCPU count --
//     used by extractVCPUCountFromRecommendation to set rec.Count.
//
// This mirrors the GCP CUD Recommender format (issue #1022 C1).
func realisticCUDRecommendation() *recommenderpb.Recommendation {
	vcpuVal, _ := structpb.NewValue(4.0)
	return &recommenderpb.Recommendation{
		Name: "projects/test/locations/us-central1/recommenders/google.billing.CostInsight.commitmentRecommender/recommendations/rec-001",
		PrimaryImpact: &recommenderpb.Impact{
			Category: recommenderpb.Impact_COST,
			Projection: &recommenderpb.Impact_CostProjection{
				CostProjection: &recommenderpb.CostProjection{
					Cost: &money.Money{
						Units:        -200,
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
							// Machine type op: resource path ends in the machine type name,
							// so extractResourceTypeFromOperations sets rec.ResourceType = "n1-standard-4".
							Action:   "add",
							Resource: "projects/test/zones/us-central1-a/machineTypes/n1-standard-4",
						},
					},
				},
				{
					Operations: []*recommenderpb.Operation{
						{
							// Commitment resource op: carries the VCPU amount as a numeric value.
							// extractVCPUCountFromRecommendation reads this and sets rec.Count = 4.
							Action:       "add",
							ResourceType: "compute.googleapis.com/Commitment",
							Resource:     "//compute.googleapis.com/projects/test/regions/us-central1/commitments/cud-001",
							Path:         "/resources/0/amount",
							PathValue:    &recommenderpb.Operation_Value{Value: vcpuVal},
						},
					},
				},
			},
		},
	}
}

// mockBillingWithCommitment returns a MockBillingService that has both an
// on-demand SKU and a commitment SKU for n1-standard machines in us-central1.
func mockBillingWithCommitment() *MockBillingService {
	return &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: []*cloudbilling.Sku{
				{
					Description:    "n1-standard-4 VM running in Americas",
					ServiceRegions: []string{"us-central1"},
					PricingInfo: []*cloudbilling.PricingInfo{
						{
							PricingExpression: &cloudbilling.PricingExpression{
								TieredRates: []*cloudbilling.TierRate{
									{
										UnitPrice: &cloudbilling.Money{
											Units:        0,
											Nanos:        190000000,
											CurrencyCode: "USD",
										},
									},
								},
							},
						},
					},
				},
				{
					Description:    "n1-standard-4 commitment 1yr in Americas",
					ServiceRegions: []string{"us-central1"},
					PricingInfo: []*cloudbilling.PricingInfo{
						{
							PricingExpression: &cloudbilling.PricingExpression{
								TieredRates: []*cloudbilling.TierRate{
									{
										UnitPrice: &cloudbilling.Money{
											Units:        0,
											Nanos:        120000000,
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
}

// TestConverterToInsert_CountNonZero_VCPUAmountSet is the primary regression test
// for issue #1022 C1. It exercises the full converter->purchase path with a
// realistic Recommender payload and asserts:
//   - converter sets Count > 0 (extracted from the operation's numeric value)
//   - buildInsertRequest produces a non-zero VCPU Amount in the resulting commitment
//
// This test FAILS on the pre-fix code where convertGCPRecommendation never set Count.
func TestConverterToInsert_CountNonZero_VCPUAmountSet(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")
	client.SetBillingService(mockBillingWithCommitment())

	gcpRec := realisticCUDRecommendation()
	rec := client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{})
	require.NotNil(t, rec)

	// C1: Count must be > 0 so that buildInsertRequest produces non-zero VCPU Amount.
	require.Greater(t, rec.Count, 0, "convertGCPRecommendation must set Count > 0 from the Recommender payload (issue #1022 C1)")

	// Verify the insert request carries the correct VCPU count.
	insertReq, _, buildErr := client.buildInsertRequest(*rec, common.PurchaseOptions{})
	require.NoError(t, buildErr, "buildInsertRequest must not error when Count > 0")
	require.NotNil(t, insertReq)
	require.NotNil(t, insertReq.CommitmentResource)

	var vcpuAmount, memoryAmount int64
	for _, r := range insertReq.CommitmentResource.Resources {
		switch r.GetType() {
		case "VCPU":
			vcpuAmount = r.GetAmount()
		case "MEMORY_MB":
			memoryAmount = r.GetAmount()
		}
	}
	assert.Greater(t, vcpuAmount, int64(0), "VCPU Amount in the insert request must be > 0 (issue #1022 C1)")
	assert.Greater(t, memoryAmount, int64(0), "MEMORY_MB Amount in the insert request must be > 0 (issue #1022 C1)")
}

// TestConverterFillsPricingForScorer is the regression test for issue #1022 C2.
// It exercises the full converter path with a realistic Recommender payload and a
// billing mock that returns both on-demand and commitment SKUs, and then asserts
// that a scorer with MinSavingsPct set does NOT drop the GCP recommendation.
//
// This test FAILS on the pre-fix code where convertGCPRecommendation never
// called getComputePricing, leaving SavingsPercentage at 0.
func TestConverterFillsPricingForScorer(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")
	client.SetBillingService(mockBillingWithCommitment())

	gcpRec := realisticCUDRecommendation()
	rec := client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{})
	require.NotNil(t, rec)

	// C2: SavingsPercentage must be > 0 so a MinSavingsPct filter doesn't silently drop the rec.
	require.Greater(t, rec.SavingsPercentage, float64(0),
		"convertGCPRecommendation must set SavingsPercentage > 0 from billing pricing (issue #1022 C2)")
	assert.Greater(t, rec.CommitmentCost, float64(0), "CommitmentCost must be > 0")
	assert.Greater(t, rec.OnDemandCost, float64(0), "OnDemandCost must be > 0")

	// Apply a non-trivial MinSavingsPct filter and confirm the rec passes.
	result := scorer.Score([]common.Recommendation{*rec}, scorer.Config{MinSavingsPct: 5.0})
	require.Len(t, result.Passed, 1, "GCP recommendation must pass a MinSavingsPct=5 scorer filter after pricing is wired (issue #1022 C2)")
	assert.Empty(t, result.Filtered, "no GCP recommendations should be filtered by MinSavingsPct when pricing is set")
}

// TestBuildInsertRequest_RefusesZeroCount is the regression test for the
// issue #1022 guard in buildInsertRequest. A recommendation with Count == 0
// must return an error rather than sending a zero-vCPU commitment to GCP.
//
// This test FAILS on the pre-fix code (which had no such guard).
func TestBuildInsertRequest_RefusesZeroCount(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	rec := common.Recommendation{
		ResourceType: "n1-standard-4",
		Term:         "1yr",
		Count:        0, // zero -- should be refused
	}

	_, _, err := client.buildInsertRequest(rec, common.PurchaseOptions{})
	require.Error(t, err, "buildInsertRequest must refuse Count <= 0 (issue #1022 guard)")
	assert.Contains(t, err.Error(), "rec.Count must be > 0")

	// PurchaseCommitment must also surface the error and not call Insert.
	mockSvc := &MockCommitmentsService{operation: &MockOperation{}}
	client.SetCommitmentsService(mockSvc)
	result, purchaseErr := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
	require.Error(t, purchaseErr)
	assert.False(t, result.Success)
	assert.Empty(t, mockSvc.insertReqs, "Insert must not be called when Count <= 0")
}

// TestGetComputePricing_NoCommitmentSKUReturnsError is the regression test for
// issue #1020 (GCP). When the billing catalog has no commitment SKU, getComputePricing
// must return an error rather than fabricating a price from a hardcoded discount factor.
//
// This test FAILS on the pre-fix code where estimateComputeCommitmentPrice was called.
func TestGetComputePricing_NoCommitmentSKUReturnsError(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// Only on-demand SKU -- no commitment SKU.
	client.SetBillingService(&MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: []*cloudbilling.Sku{
				{
					Description:    "n1-standard-4 VM running in Americas",
					ServiceRegions: []string{"us-central1"},
					PricingInfo: []*cloudbilling.PricingInfo{
						{
							PricingExpression: &cloudbilling.PricingExpression{
								TieredRates: []*cloudbilling.TierRate{
									{
										UnitPrice: &cloudbilling.Money{
											Units:        0,
											Nanos:        190000000,
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
	})

	pricing, err := client.getComputePricing(ctx, "n1-standard-4", "us-central1", 1)
	require.Error(t, err, "getComputePricing must return an error when no commitment SKU exists (issue #1020)")
	assert.Contains(t, err.Error(), "no commitment pricing found")
	assert.Nil(t, pricing, "no pricing struct must be returned when commitment SKU is missing")
}
