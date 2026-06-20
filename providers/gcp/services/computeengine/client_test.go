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
	err := client.ValidateOffering(ctx, &rec)
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
	err := client.ValidateOffering(ctx, &rec)
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
	err := client.ValidateOffering(ctx, &rec)
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
		Details:        common.ComputeDetails{MemoryGB: 20.0}, // 5 vCPU * 4 GB
	}

	result, err := client.PurchaseCommitment(ctx, &rec, common.PurchaseOptions{})
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

	rec := common.Recommendation{
		ResourceType: "n1-standard-1",
		Term:         "1yr",
		Count:        1,
		Details:      common.ComputeDetails{MemoryGB: 4.0}, // 1 vCPU * 4 GB
	}

	_, err := client.PurchaseCommitment(ctx, &rec, common.PurchaseOptions{Source: common.PurchaseSourceWeb})
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

	rec := common.Recommendation{
		ResourceType: "n1-standard-1",
		Term:         "1yr",
		Count:        1,
		Details:      common.ComputeDetails{MemoryGB: 4.0},
	}

	_, err := client.PurchaseCommitment(ctx, &rec, common.PurchaseOptions{})
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

	rec := common.Recommendation{
		ResourceType: "n1-standard-1",
		Term:         "1yr",
		Count:        5,
		Details:      common.ComputeDetails{MemoryGB: 20.0}, // 5 vCPU * 4 GB
	}
	token := common.DeriveIdempotencyToken("exec-654", 0)
	opts := common.PurchaseOptions{Source: common.PurchaseSourceWeb, IdempotencyToken: token}

	r1, err := client.PurchaseCommitment(ctx, &rec, opts)
	require.NoError(t, err)
	require.True(t, r1.Success)

	r2, err := client.PurchaseCommitment(ctx, &rec, opts)
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

	rec := common.Recommendation{
		ResourceType: "n1-standard-1",
		Term:         "1yr",
		Count:        1,
		Details:      common.ComputeDetails{MemoryGB: 4.0},
	}

	_, err := client.PurchaseCommitment(ctx, &rec, common.PurchaseOptions{})
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
		Count:        4,                                     // must be > 0 after issue #1022 guard
		Details:      common.ComputeDetails{MemoryGB: 16.0}, // 4 vCPU * 4 GB
	}

	result, err := client.PurchaseCommitment(ctx, &rec, common.PurchaseOptions{})
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

	// Count must be > 0, Term must be valid, and Details.MemoryGB must be set so
	// all pre-insert guards pass and the insert error is exercised.
	rec := common.Recommendation{
		ResourceType: "n1-standard-1",
		Term:         "1yr",
		Count:        2,
		Details:      common.ComputeDetails{MemoryGB: 8.0},
	}

	result, err := client.PurchaseCommitment(ctx, &rec, common.PurchaseOptions{})
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

	// Count must be > 0, Term must be valid, and Details.MemoryGB must be set so
	// all pre-insert guards pass and the wait error is exercised.
	rec := common.Recommendation{
		ResourceType: "n1-standard-1",
		Term:         "1yr",
		Count:        2,
		Details:      common.ComputeDetails{MemoryGB: 8.0},
	}

	result, err := client.PurchaseCommitment(ctx, &rec, common.PurchaseOptions{})
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

	details, err := client.GetOfferingDetails(ctx, &rec)
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

	_, err := client.GetOfferingDetails(ctx, &rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no on-demand pricing found")
}

func TestComputeEngineClient_GetRecommendations_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockIterator := &MockRecommenderIterator{
		recommendations: []*recommenderpb.Recommendation{
			{
				Name:      "recommendation-1",
				StateInfo: &recommenderpb.RecommendationStateInfo{State: recommenderpb.RecommendationStateInfo_ACTIVE},
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

	recommendations, err := client.GetRecommendations(ctx, &common.RecommendationParams{})
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

	recs, err := client.GetRecommendations(ctx, &common.RecommendationParams{})
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

	recommendations, err := client.GetRecommendations(ctx, &common.RecommendationParams{})
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

	_, err = client.GetRecommendations(ctx, &common.RecommendationParams{})
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
// for a 4-vCPU n1-standard-4 commitment in us-central1. The operation groups have
// three ops:
//  1. A machine-type op whose resource path ends in the machine type (n1-standard-4) --
//     used by extractResourceTypeFromOperations to set rec.ResourceType.
//  2. A VCPU commitment resource op whose numeric Value is the VCPU count --
//     used by extractVCPUCountFromRecommendation to set rec.Count = 4.
//  3. A MEMORY commitment resource op whose numeric Value is 6144 MB --
//     used by extractMemoryMBFromRecommendation to set rec.Details.MemoryGB = 6.
//     Using 6144 MB (1536 MB/vCPU) intentionally tests a non-4096-ratio case to
//     confirm the value is read from the payload rather than computed from a ratio.
//
// This mirrors the GCP CUD Recommender format (issue #1022 C1 + memory fix).
func realisticCUDRecommendation() *recommenderpb.Recommendation {
	vcpuVal, _ := structpb.NewValue(4.0)
	memVal, _ := structpb.NewValue(6144.0) // 6144 MB = 6 GB; ratio is 1536 MB/vCPU, NOT 4096
	memTypeFilter, _ := structpb.NewValue("MEMORY")
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
							// VCPU op: carries the vCPU count as a numeric value.
							// extractVCPUCountFromRecommendation reads this and sets rec.Count = 4.
							Action:       "add",
							ResourceType: "compute.googleapis.com/Commitment",
							Resource:     "//compute.googleapis.com/projects/test/regions/us-central1/commitments/cud-001",
							Path:         "/resources/0/amount",
							PathValue:    &recommenderpb.Operation_Value{Value: vcpuVal},
						},
						{
							// MEMORY op: carries the memory amount in MB as a numeric value.
							// extractMemoryMBFromRecommendation reads this (path_filter type=MEMORY)
							// and sets rec.Details = ComputeDetails{MemoryGB: 6.0}.
							Action:       "add",
							ResourceType: "compute.googleapis.com/Commitment",
							Resource:     "//compute.googleapis.com/projects/test/regions/us-central1/commitments/cud-001",
							Path:         "/resources/1/amount",
							PathValue:    &recommenderpb.Operation_Value{Value: memVal},
							PathFilters: map[string]*structpb.Value{
								"/resources/1/type": memTypeFilter,
							},
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
//   - converter extracts the MEMORY amount from the payload (not a ratio)
//   - buildInsertRequest produces the exact VCPU and MEMORY Amounts
//
// The payload uses 6144 MB for 4 vCPUs (1536 MB/vCPU, NOT 4096). An old ratio-
// based implementation would produce 4*4096 = 16384 MB; the correct value is
// 6144 MB. This assertion fails on pre-fix ratio-based code.
func TestConverterToInsert_CountNonZero_VCPUAmountSet(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")
	client.SetBillingService(mockBillingWithCommitment())

	gcpRec := realisticCUDRecommendation() // 4 vCPU, 6144 MB (from payload)
	rec := client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{})
	require.NotNil(t, rec)

	// C1: Count must be > 0 so that buildInsertRequest produces non-zero VCPU Amount.
	require.Greater(t, rec.Count, 0, "convertGCPRecommendation must set Count > 0 from the Recommender payload (issue #1022 C1)")

	// Memory must be extracted from the payload and stored in Details.
	cd, ok := rec.Details.(common.ComputeDetails)
	require.True(t, ok, "convertGCPRecommendation must populate ComputeDetails (memory extracted from payload)")
	assert.Equal(t, 6.0, cd.MemoryGB,
		"MEMORY amount from payload is 6144 MB = 6 GB; ratio-based code would produce 16 GB (4*4096 MB)")

	// Verify the insert request carries the correct VCPU and MEMORY amounts.
	insertReq, _, buildErr := client.buildInsertRequest(*rec, common.PurchaseOptions{})
	require.NoError(t, buildErr, "buildInsertRequest must not error when Count > 0 and memory is present")
	require.NotNil(t, insertReq)
	require.NotNil(t, insertReq.CommitmentResource)

	// The resource Type strings MUST be valid GCP ResourceCommitment.Type enum
	// members (VCPU/MEMORY/LOCAL_SSD/ACCELERATOR). "MEMORY_MB" is NOT a valid
	// enum value and GCP rejects the commitments.insert, failing the purchase
	// (issue #1022). This loop asserts on the canonical "MEMORY" spelling.
	var vcpuAmount, memoryAmount int64
	var sawInvalidType string
	for _, r := range insertReq.CommitmentResource.Resources {
		switch r.GetType() {
		case "VCPU":
			vcpuAmount = r.GetAmount()
		case "MEMORY":
			memoryAmount = r.GetAmount()
		default:
			sawInvalidType = r.GetType()
		}
	}
	assert.Empty(t, sawInvalidType, "insert request must use only valid ResourceCommitment.Type enum members; got %q (issue #1022)", sawInvalidType)
	assert.Equal(t, int64(4), vcpuAmount, "VCPU Amount in the insert request must equal the extracted count")
	// Exact memory check: payload says 6144 MB; old ratio (4*4096=16384 MB) would fail.
	assert.Equal(t, int64(6144), memoryAmount,
		"MEMORY Amount must match the payload's 6144 MB (not ratio-derived 4*4096=16384 MB)")
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
	result, purchaseErr := client.PurchaseCommitment(ctx, &rec, common.PurchaseOptions{})
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

// TestConvertGCPRecommendation_EmptyParamsDefaultsToMonthly asserts that when
// RecommendationParams.PaymentOption is empty the converter defaults to "monthly".
// GCP CUDs have no upfront-payment option; defaulting to "upfront" caused incorrect
// purchase-body construction downstream.
//
// This test FAILS on the pre-fix code that defaulted to "upfront" (10-M5 / auditor finding).
func TestConvertGCPRecommendation_EmptyParamsDefaultsToMonthly(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	gcpRec := &recommenderpb.Recommendation{
		Name: "test-rec",
		PrimaryImpact: &recommenderpb.Impact{
			Category: recommenderpb.Impact_COST,
			Projection: &recommenderpb.Impact_CostProjection{
				CostProjection: &recommenderpb.CostProjection{
					Cost: &money.Money{Units: -100, CurrencyCode: "USD"},
				},
			},
		},
	}

	// Empty params: no caller-supplied PaymentOption.
	rec := client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{})
	require.NotNil(t, rec)
	assert.Equal(t, "monthly", rec.PaymentOption,
		"GCP CUDs have no upfront option; empty PaymentOption must default to \"monthly\" (10-M5)")
}

// TestConvertGCPRecommendation_ParamPaymentOptionRespected asserts that an
// explicit PaymentOption in RecommendationParams is forwarded to the recommendation.
func TestConvertGCPRecommendation_ParamPaymentOptionRespected(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	gcpRec := &recommenderpb.Recommendation{Name: "test-rec"}
	params := common.RecommendationParams{PaymentOption: "monthly"}

	rec := client.convertGCPRecommendation(ctx, gcpRec, params)
	require.NotNil(t, rec)
	assert.Equal(t, "monthly", rec.PaymentOption)
}

// TestGroupCommitments_UsesValidMemoryEnum is a regression test for issue #1022:
// GroupCommitments must emit the canonical GCP ResourceCommitment.Type enum
// member "MEMORY" (Amount in MB), not the invalid "MEMORY_MB" string that GCP
// rejects on commitments.insert. Fails on the pre-fix code that emitted
// "MEMORY_MB".
// The memory amount is read from ComputeDetails.MemoryGB (set by the converter);
// recommendations without Details are skipped.
func TestGroupCommitments_UsesValidMemoryEnum(t *testing.T) {
	// 4 vCPU, 24 GB memory (non-4096-ratio to confirm the value is read from
	// Details, not computed from the old ratio).
	const wantMemoryGB = 24.0
	const wantMemoryMB = int64(wantMemoryGB * 1024)

	recs := []common.Recommendation{
		{
			Provider: common.ProviderGCP,
			Service:  common.ServiceCompute,
			Account:  "test-project",
			Region:   "us-central1",
			Term:     "1yr",
			Count:    4,
			Details:  common.ComputeDetails{MemoryGB: wantMemoryGB},
		},
	}

	groups := GroupCommitments(recs)
	require.Len(t, groups, 1, "one project+region+term group expected")

	var sawVCPU, sawMemory bool
	for _, r := range groups[0].Resources {
		switch r.Type {
		case "VCPU":
			sawVCPU = true
			assert.Equal(t, int64(4), r.Amount, "VCPU amount must equal the summed Count")
		case "MEMORY":
			sawMemory = true
			assert.Equal(t, wantMemoryMB, r.Amount, "MEMORY amount must equal the payload-sourced MB (issue #1022)")
		default:
			t.Fatalf("invalid ResourceCommitment.Type %q (issue #1022): must be VCPU/MEMORY/LOCAL_SSD/ACCELERATOR", r.Type)
		}
	}
	assert.True(t, sawVCPU, "GroupCommitments must include a VCPU resource")
	assert.True(t, sawMemory, "GroupCommitments must include a MEMORY resource (issue #1022)")
}

// TestGroupCommitments_SkipsRecsWithoutMemory verifies that GroupCommitments skips
// recommendations that have no Details.MemoryGB (payload omitted memory) rather
// than producing a zero-memory commitment that GCP would reject.
func TestGroupCommitments_SkipsRecsWithoutMemory(t *testing.T) {
	recs := []common.Recommendation{
		{
			Provider: common.ProviderGCP,
			Service:  common.ServiceCompute,
			Account:  "test-project",
			Region:   "us-central1",
			Term:     "1yr",
			Count:    4,
			// No Details: memory is absent from the payload.
		},
	}

	groups := GroupCommitments(recs)
	assert.Empty(t, groups, "GroupCommitments must skip recs without payload memory, not produce a zero-memory commitment")
}

// TestConvertGCPRecommendation_NonMonthlyPaymentOptionForcedToMonthly is a
// regression test for the CR finding that non-monthly values (e.g. "upfront")
// are silently propagated to the recommendation, which is invalid for GCP CUDs
// that only support monthly billing. The converter must always emit "monthly".
//
// This test FAILS on the pre-fix code that passed params.PaymentOption through
// unchanged when it was non-empty.
func TestConvertGCPRecommendation_NonMonthlyPaymentOptionForcedToMonthly(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	gcpRec := &recommenderpb.Recommendation{Name: "test-rec"}

	for _, input := range []string{"upfront", "all-upfront", "UPFRONT", "partial-upfront"} {
		params := common.RecommendationParams{PaymentOption: input}
		rec := client.convertGCPRecommendation(ctx, gcpRec, params)
		require.NotNil(t, rec)
		assert.Equal(t, "monthly", rec.PaymentOption,
			"GCP CUDs are monthly-only; input %q must be forced to \"monthly\"", input)
	}
}

// TestIsMemoryAmountOp_MatchesBothSpellings asserts that the inbound Recommender
// memory-op detector skips both the canonical "MEMORY" and legacy "MEMORY_MB"
// path_filter spellings, so the VCPU extractor never mistakes the memory sibling
// for the vCPU amount (issue #1022).
func TestIsMemoryAmountOp_MatchesBothSpellings(t *testing.T) {
	for _, typeVal := range []string{"MEMORY", "MEMORY_MB", "memory"} {
		sv, _ := structpb.NewValue(typeVal)
		op := &recommenderpb.Operation{
			PathFilters: map[string]*structpb.Value{
				"/resources/*/type": sv,
			},
		}
		assert.True(t, isMemoryAmountOp(op), "isMemoryAmountOp must skip memory op with type %q", typeVal)
	}

	// A VCPU op (or an op with no type filter) must NOT be treated as memory.
	svVCPU, _ := structpb.NewValue("VCPU")
	vcpuOp := &recommenderpb.Operation{
		PathFilters: map[string]*structpb.Value{"/resources/*/type": svVCPU},
	}
	assert.False(t, isMemoryAmountOp(vcpuOp), "isMemoryAmountOp must not skip a VCPU op")
}

// TestBuildInsertRequest_RefusesMissingMemory is the regression test for the
// "fail loud" policy on missing memory: buildInsertRequest must return an error
// when rec.Details does not carry a ComputeDetails.MemoryGB value (i.e. the
// Recommender payload omitted the MEMORY op). A silent fallback to a fixed ratio
// would produce incorrect commitments for non-standard machine families.
//
// This test fails on pre-fix code that silently used memMBPerVCPU as a fallback.
func TestBuildInsertRequest_RefusesMissingMemory(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	rec := common.Recommendation{
		ResourceType: "n1-standard-4",
		Term:         "1yr",
		Count:        4,
		// No Details: memory absent from Recommender payload.
	}

	_, _, err := client.buildInsertRequest(rec, common.PurchaseOptions{})
	require.Error(t, err, "buildInsertRequest must refuse when memory is absent from the payload")
	assert.Contains(t, err.Error(), "MEMORY resource amount absent",
		"error must name the cause so operators can diagnose the missing Recommender field")

	// PurchaseCommitment must surface the error and not call Insert.
	mockSvc := &MockCommitmentsService{operation: &MockOperation{}}
	client.SetCommitmentsService(mockSvc)
	result, purchaseErr := client.PurchaseCommitment(ctx, &rec, common.PurchaseOptions{})
	require.Error(t, purchaseErr)
	assert.False(t, result.Success)
	assert.Empty(t, mockSvc.insertReqs, "Insert must not be called when memory is absent")
}

// TestTermPlan_UsesSdkEnumConstants verifies that termPlan returns the exact string
// produced by the SDK enum's String() method (not a hand-written literal), so any
// future SDK rename propagates automatically.
func TestTermPlan_UsesSdkEnumConstants(t *testing.T) {
	plan12, err := termPlan("1yr")
	require.NoError(t, err)
	assert.Equal(t, computepb.Commitment_TWELVE_MONTH.String(), plan12,
		"1yr must map to the SDK TWELVE_MONTH constant, not a string literal")

	plan36, err := termPlan("3yr")
	require.NoError(t, err)
	assert.Equal(t, computepb.Commitment_THIRTY_SIX_MONTH.String(), plan36,
		"3yr must map to the SDK THIRTY_SIX_MONTH constant, not a string literal")
}

// TestTermPlan_RejectsUnknownTerm is the regression test for the "fail loud" policy:
// termPlan must return an error rather than silently defaulting to 12 months when
// given an unrecognised or empty term. A silent mis-default can purchase the wrong
// duration and waste money.
//
// This test fails on pre-fix code that silently returned TWELVE_MONTH for any
// unrecognised input.
func TestTermPlan_RejectsUnknownTerm(t *testing.T) {
	for _, badTerm := range []string{"", "2yr", "invalid", "24mo", "forever"} {
		_, err := termPlan(badTerm)
		require.Error(t, err, "termPlan must reject unrecognised term %q (no silent 12-month default)", badTerm)
		assert.Contains(t, err.Error(), "unrecognised commitment term",
			"error for term %q must identify the bad input", badTerm)
	}
}

// TestTermPlan_AcceptsAllDocumentedForms asserts that termPlan accepts all
// documented 1-year and 3-year input forms.
func TestTermPlan_AcceptsAllDocumentedForms(t *testing.T) {
	for _, form := range []string{"1yr", "1", "12mo"} {
		got, err := termPlan(form)
		require.NoError(t, err, "termPlan must accept 1-year form %q", form)
		assert.Equal(t, computepb.Commitment_TWELVE_MONTH.String(), got,
			"1-year form %q must map to TWELVE_MONTH", form)
	}
	for _, form := range []string{"3yr", "3", "36mo"} {
		got, err := termPlan(form)
		require.NoError(t, err, "termPlan must accept 3-year form %q", form)
		assert.Equal(t, computepb.Commitment_THIRTY_SIX_MONTH.String(), got,
			"3-year form %q must map to THIRTY_SIX_MONTH", form)
	}
}

// TestConvertGCPRecommendation_PropagatesParamsTerm asserts that convertGCPRecommendation
// propagates params.Term to rec.Term (H-3 audit finding). Pre-fix code hardcoded
// "1yr" regardless of params.Term, so callers requesting "3yr" silently received
// 1-year recommendations and 1-year pricing.
//
// This test FAILS on the pre-fix code that hardcoded Term: "1yr".
func TestConvertGCPRecommendation_PropagatesParamsTerm(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	gcpRec := &recommenderpb.Recommendation{Name: "test-rec"}

	// 3yr must be propagated.
	rec := client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{Term: "3yr"})
	require.NotNil(t, rec)
	assert.Equal(t, "3yr", rec.Term,
		"params.Term=3yr must be propagated to rec.Term (H-3 fix); pre-fix code hardcoded 1yr")

	// 1yr explicit must be propagated.
	rec = client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{Term: "1yr"})
	require.NotNil(t, rec)
	assert.Equal(t, "1yr", rec.Term)

	// Empty term must default to "1yr".
	rec = client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{})
	require.NotNil(t, rec)
	assert.Equal(t, "1yr", rec.Term,
		"empty params.Term must default to 1yr")
}

// TestConvertGCPRecommendation_RejectsUnknownTerm asserts that convertGCPRecommendation
// returns nil when given an unrecognised params.Term (e.g. "5yr"). An unroutable
// recommendation must be dropped before it can reach buildInsertRequest and attempt
// to insert a commitment with an invalid plan.
//
// This test FAILS on the pre-fix code that either silently used "1yr" or panicked.
func TestConvertGCPRecommendation_RejectsUnknownTerm(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	gcpRec := &recommenderpb.Recommendation{Name: "test-rec"}

	rec := client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{Term: "5yr"})
	assert.Nil(t, rec,
		"convertGCPRecommendation must return nil for unrecognised term (not silently default to 12 months)")
}

// TestGetRecommendations_FiltersNonActiveStates is a regression test for H-1
// (GCP broad audit): the Recommender API returns recommendations in all states
// (ACTIVE/CLAIMED/SUCCEEDED/FAILED/DISMISSED); only ACTIVE ones must be surfaced
// as actionable. Acting on an already-CLAIMED or SUCCEEDED recommendation is a
// cross-run double-purchase vector and inflates actionable rec counts.
//
// This test FAILS on the pre-fix code that converted all recommendations
// regardless of state.
func TestGetRecommendations_FiltersNonActiveStates(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	stateActive := recommenderpb.RecommendationStateInfo_ACTIVE
	stateClaimed := recommenderpb.RecommendationStateInfo_CLAIMED
	stateSucceeded := recommenderpb.RecommendationStateInfo_SUCCEEDED
	stateFailed := recommenderpb.RecommendationStateInfo_FAILED
	stateDismissed := recommenderpb.RecommendationStateInfo_DISMISSED

	recs := []*recommenderpb.Recommendation{
		{
			Name:      "active-rec",
			StateInfo: &recommenderpb.RecommendationStateInfo{State: stateActive},
			PrimaryImpact: &recommenderpb.Impact{
				Category: recommenderpb.Impact_COST,
				Projection: &recommenderpb.Impact_CostProjection{
					CostProjection: &recommenderpb.CostProjection{
						Cost: &money.Money{Units: -100, CurrencyCode: "USD"},
					},
				},
			},
		},
		{
			Name:      "claimed-rec",
			StateInfo: &recommenderpb.RecommendationStateInfo{State: stateClaimed},
		},
		{
			Name:      "succeeded-rec",
			StateInfo: &recommenderpb.RecommendationStateInfo{State: stateSucceeded},
		},
		{
			Name:      "failed-rec",
			StateInfo: &recommenderpb.RecommendationStateInfo{State: stateFailed},
		},
		{
			Name:      "dismissed-rec",
			StateInfo: &recommenderpb.RecommendationStateInfo{State: stateDismissed},
		},
	}

	mockIterator := &MockRecommenderIterator{recommendations: recs}
	mockClient := &MockRecommenderClient{iterator: mockIterator}
	client.SetRecommenderClient(mockClient)

	results, err := client.GetRecommendations(ctx, &common.RecommendationParams{})
	require.NoError(t, err)
	require.Len(t, results, 1,
		"only the ACTIVE recommendation must be returned; CLAIMED/SUCCEEDED/FAILED/DISMISSED must be filtered (H-1)")
	assert.Equal(t, common.ProviderGCP, results[0].Provider)
}

// TestGetRecommendations_ActiveRecIncluded is the positive-case complement of
// TestGetRecommendations_FiltersNonActiveStates: a recommendation explicitly
// marked ACTIVE must pass the state filter.
func TestGetRecommendations_ActiveRecIncluded(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockIterator := &MockRecommenderIterator{
		recommendations: []*recommenderpb.Recommendation{
			{
				Name:      "active-only",
				StateInfo: &recommenderpb.RecommendationStateInfo{State: recommenderpb.RecommendationStateInfo_ACTIVE},
				PrimaryImpact: &recommenderpb.Impact{
					Category: recommenderpb.Impact_COST,
					Projection: &recommenderpb.Impact_CostProjection{
						CostProjection: &recommenderpb.CostProjection{
							Cost: &money.Money{Units: -50, CurrencyCode: "USD"},
						},
					},
				},
			},
		},
	}
	mockClient := &MockRecommenderClient{iterator: mockIterator}
	client.SetRecommenderClient(mockClient)

	results, err := client.GetRecommendations(ctx, &common.RecommendationParams{})
	require.NoError(t, err)
	require.Len(t, results, 1, "an ACTIVE recommendation must be included")
}
