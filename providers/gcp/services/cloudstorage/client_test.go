package cloudstorage

import (
	"context"
	"errors"
	"testing"

	"cloud.google.com/go/recommender/apiv1/recommenderpb"
	"cloud.google.com/go/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/genproto/googleapis/type/money"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// MockStorageService mocks the StorageService interface
type MockStorageService struct {
	buckets      []*storage.BucketAttrs
	listErr      error
	bucketName   string
	createErr    error
	createCalled *bool
}

func (m *MockStorageService) Buckets(ctx context.Context, projectID string) BucketIterator {
	return &MockBucketIterator{buckets: m.buckets, err: m.listErr}
}

func (m *MockStorageService) Bucket(name string) BucketHandle {
	m.bucketName = name
	return &MockBucketHandle{createErr: m.createErr, createCalled: m.createCalled}
}

func (m *MockStorageService) Close() error {
	return nil
}

// MockBucketIterator mocks the BucketIterator interface
type MockBucketIterator struct {
	buckets []*storage.BucketAttrs
	index   int
	err     error
}

func (m *MockBucketIterator) Next() (*storage.BucketAttrs, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.index >= len(m.buckets) {
		return nil, iterator.Done
	}
	b := m.buckets[m.index]
	m.index++
	return b, nil
}

// MockBucketHandle mocks the BucketHandle interface
type MockBucketHandle struct {
	createErr    error
	createCalled *bool
}

func (m *MockBucketHandle) Create(ctx context.Context, projectID string, attrs *storage.BucketAttrs) error {
	if m.createCalled != nil {
		*m.createCalled = true
	}
	return m.createErr
}

// MockRecommenderClient mocks the RecommenderClient interface
type MockRecommenderClient struct {
	recommendations []*recommenderpb.Recommendation
	err             error
	closed          bool
}

func (m *MockRecommenderClient) ListRecommendations(ctx context.Context, req *recommenderpb.ListRecommendationsRequest) RecommenderIterator {
	return &MockRecommenderIterator{recommendations: m.recommendations, err: m.err}
}

func (m *MockRecommenderClient) Close() error {
	m.closed = true
	return nil
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

func TestNewClient(t *testing.T) {
	ctx := context.Background()
	client, err := NewClient(ctx, "test-project", "us-central1")

	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Equal(t, "test-project", client.projectID)
	assert.Equal(t, "us-central1", client.region)
	assert.Equal(t, ctx, client.ctx)
}

func TestCloudStorageClient_GetServiceType(t *testing.T) {
	client := &CloudStorageClient{}
	assert.Equal(t, common.ServiceStorage, client.GetServiceType())
}

func TestCloudStorageClient_GetRegion(t *testing.T) {
	client := &CloudStorageClient{region: "europe-west1"}
	assert.Equal(t, "europe-west1", client.GetRegion())
}

func TestCloudStorageClient_GetValidResourceTypes(t *testing.T) {
	ctx := context.Background()
	client := &CloudStorageClient{
		ctx:       ctx,
		projectID: "test-project",
		region:    "us-central1",
	}

	types, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, types)

	// Verify expected storage classes
	assert.Contains(t, types, "STANDARD")
	assert.Contains(t, types, "NEARLINE")
	assert.Contains(t, types, "COLDLINE")
	assert.Contains(t, types, "ARCHIVE")
	assert.Len(t, types, 4)
}

func TestCloudStorageClient_ValidateOffering_ValidClasses(t *testing.T) {
	ctx := context.Background()
	client := &CloudStorageClient{
		ctx:       ctx,
		projectID: "test-project",
		region:    "us-central1",
	}

	validClasses := []string{"STANDARD", "NEARLINE", "COLDLINE", "ARCHIVE"}

	for _, class := range validClasses {
		t.Run(class, func(t *testing.T) {
			rec := common.Recommendation{
				ResourceType: class,
			}
			err := client.ValidateOffering(ctx, rec)
			assert.NoError(t, err)
		})
	}
}

func TestCloudStorageClient_ValidateOffering_InvalidClass(t *testing.T) {
	ctx := context.Background()
	client := &CloudStorageClient{
		ctx:       ctx,
		projectID: "test-project",
		region:    "us-central1",
	}

	rec := common.Recommendation{
		ResourceType: "INVALID_CLASS",
	}

	err := client.ValidateOffering(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Cloud Storage class")
}

func TestStoragePricing_Fields(t *testing.T) {
	pricing := &StoragePricing{
		HourlyRate:        0.026,
		CommitmentPrice:   100.0,
		OnDemandPrice:     125.0,
		Currency:          "USD",
		SavingsPercentage: 20.0,
	}

	assert.Equal(t, 0.026, pricing.HourlyRate)
	assert.Equal(t, 100.0, pricing.CommitmentPrice)
	assert.Equal(t, 125.0, pricing.OnDemandPrice)
	assert.Equal(t, "USD", pricing.Currency)
	assert.Equal(t, 20.0, pricing.SavingsPercentage)
}

func TestSkuMatchesStorageClass(t *testing.T) {
	tests := []struct {
		name         string
		description  string
		storageClass string
		region       string
		regions      []string
		expected     bool
	}{
		{
			name:         "Matches description and region",
			description:  "Standard Storage in us-central1",
			storageClass: "Standard",
			region:       "us-central1",
			regions:      []string{"us-central1", "us-east1"},
			expected:     true,
		},
		{
			name:         "Matches description, wrong region",
			description:  "Standard Storage in us-central1",
			storageClass: "Standard",
			region:       "europe-west1",
			regions:      []string{"us-central1", "us-east1"},
			expected:     false,
		},
		{
			name:         "Doesn't match description",
			description:  "Nearline Storage in us-central1",
			storageClass: "Standard",
			region:       "us-central1",
			regions:      []string{"us-central1"},
			expected:     false,
		},
		{
			name:         "No regions specified - matches description only",
			description:  "Standard Storage multi-region",
			storageClass: "Standard",
			region:       "us-central1",
			regions:      nil,
			expected:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sku := &cloudbilling.Sku{
				Description:    tt.description,
				ServiceRegions: tt.regions,
			}
			result := skuMatchesStorageClass(sku, tt.storageClass, tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCloudStorageClient_Fields(t *testing.T) {
	ctx := context.Background()
	client := &CloudStorageClient{
		ctx:       ctx,
		projectID: "my-project",
		region:    "asia-east1",
	}

	assert.Equal(t, ctx, client.ctx)
	assert.Equal(t, "my-project", client.projectID)
	assert.Equal(t, "asia-east1", client.region)
}

func TestCloudStorageClient_SetterMethods(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// Test SetStorageService
	mockStorage := &MockStorageService{}
	client.SetStorageService(mockStorage)
	assert.Equal(t, mockStorage, client.storageService)

	// Test SetRecommenderClient
	mockRec := &MockRecommenderClient{}
	client.SetRecommenderClient(mockRec)
	assert.Equal(t, mockRec, client.recommenderClient)

	// Test SetBillingService
	mockBilling := &MockBillingService{}
	client.SetBillingService(mockBilling)
	assert.Equal(t, mockBilling, client.billingService)
}

// TestCloudStorageClient_GetExistingCommitments_ReturnsEmpty asserts that
// GetExistingCommitments always returns an empty slice. GCS has no commitment
// API; enumerating regional buckets does not represent a commitment (10-L2).
func TestCloudStorageClient_GetExistingCommitments_ReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments, "Cloud Storage GetExistingCommitments must return empty (10-L2)")
}

// TestCloudStorageClient_PurchaseCommitment_NotSupported is the regression test for
// issue #640: Cloud Storage has no CUD or commitment purchase API, so
// PurchaseCommitment must return ErrCommitmentPurchaseNotSupported and MUST NOT call
// any resource-creation API (it previously created a new empty billable bucket).
func TestCloudStorageClient_PurchaseCommitment_NotSupported(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	createCalled := false
	mockService := &MockStorageService{createCalled: &createCalled}
	client.SetStorageService(mockService)

	rec := common.Recommendation{
		ResourceType:   "STANDARD",
		CommitmentCost: 100.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})

	require.Error(t, err)
	assert.ErrorIs(t, err, common.ErrCommitmentPurchaseNotSupported)
	assert.False(t, result.Success)
	assert.Empty(t, result.CommitmentID)
	assert.ErrorIs(t, result.Error, common.ErrCommitmentPurchaseNotSupported)
	// The critical guarantee: a "purchase" must never create infrastructure.
	assert.False(t, createCalled, "PurchaseCommitment must not call bucket Create")
}

func TestCloudStorageClient_GetRecommendations_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockClient := &MockRecommenderClient{
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
								{Resource: "projects/test/buckets/STANDARD"},
							},
						},
					},
				},
			},
		},
	}
	client.SetRecommenderClient(mockClient)

	recommendations, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	require.NoError(t, err)
	assert.Len(t, recommendations, 1)
	assert.Equal(t, common.ProviderGCP, recommendations[0].Provider)
	assert.Equal(t, common.ServiceStorage, recommendations[0].Service)
	assert.Equal(t, float64(100), recommendations[0].EstimatedSavings)
	assert.True(t, mockClient.closed)
}

func TestCloudStorageClient_GetRecommendations_Empty(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockClient := &MockRecommenderClient{
		recommendations: []*recommenderpb.Recommendation{},
	}
	client.SetRecommenderClient(mockClient)

	recommendations, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	require.NoError(t, err)
	assert.Empty(t, recommendations)
}

func TestCloudStorageClient_GetRecommendations_IteratorError(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockClient := &MockRecommenderClient{
		err: errors.New("API error"),
	}
	client.SetRecommenderClient(mockClient)

	// Iterator errors now propagate (issue #1022 H2 fix) -- they must not be
	// silently swallowed, as that would mask auth/quota failures and cause callers
	// to act on a partial (empty) recommendation list.
	recommendations, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cloudstorage: iterate recommendations")
	assert.Nil(t, recommendations)
}

// storageMockSkus returns a slice with both an on-demand and a commitment SKU for
// the given storage class and region. Required by tests that exercise GetOfferingDetails
// after the issue #1020 fix (fabricated commitment prices are no longer allowed).
func storageMockSkus(storageClass, region string, onDemandNanos, commitmentNanos int64) []*cloudbilling.Sku {
	return []*cloudbilling.Sku{
		{
			Description:    storageClass + " Storage in " + region,
			ServiceRegions: []string{region},
			PricingInfo: []*cloudbilling.PricingInfo{
				{
					PricingExpression: &cloudbilling.PricingExpression{
						TieredRates: []*cloudbilling.TierRate{
							{
								UnitPrice: &cloudbilling.Money{
									Units:        0,
									Nanos:        onDemandNanos,
									CurrencyCode: "USD",
								},
							},
						},
					},
				},
			},
		},
		{
			Description:    storageClass + " Storage commitment in " + region,
			ServiceRegions: []string{region},
			PricingInfo: []*cloudbilling.PricingInfo{
				{
					PricingExpression: &cloudbilling.PricingExpression{
						TieredRates: []*cloudbilling.TierRate{
							{
								UnitPrice: &cloudbilling.Money{
									Units:        0,
									Nanos:        commitmentNanos,
									CurrencyCode: "USD",
								},
							},
						},
					},
				},
			},
		},
	}
}

func TestCloudStorageClient_GetOfferingDetails_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// Both on-demand and commitment SKUs required after the issue #1020 fix.
	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: storageMockSkus("STANDARD", "us-central1", 26000000, 19500000),
		},
	}
	client.SetBillingService(mockService)

	rec := common.Recommendation{
		ResourceType:  "STANDARD",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	assert.Equal(t, "STANDARD", details.ResourceType)
	assert.Equal(t, "1yr", details.Term)
	assert.Equal(t, "USD", details.Currency)
	assert.Greater(t, details.TotalCost, float64(0))
	assert.Greater(t, details.UpfrontCost, float64(0))
	assert.Equal(t, float64(0), details.RecurringCost)
}

func TestCloudStorageClient_GetOfferingDetails_3yr(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// Both on-demand and commitment SKUs required after the issue #1020 fix.
	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: storageMockSkus("NEARLINE", "us-central1", 10000000, 7000000),
		},
	}
	client.SetBillingService(mockService)

	rec := common.Recommendation{
		ResourceType:  "NEARLINE",
		Term:          "3yr",
		PaymentOption: "monthly",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	assert.Equal(t, "NEARLINE", details.ResourceType)
	assert.Equal(t, "3yr", details.Term)
	assert.Equal(t, float64(0), details.UpfrontCost)
	assert.Greater(t, details.RecurringCost, float64(0))
}

func TestCloudStorageClient_GetOfferingDetails_NoPricing(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: []*cloudbilling.Sku{},
		},
	}
	client.SetBillingService(mockService)

	rec := common.Recommendation{
		ResourceType: "STANDARD",
	}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no pricing found")
}

func TestCloudStorageClient_GetOfferingDetails_BillingError(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockBillingService{
		err: errors.New("billing API error"),
	}
	client.SetBillingService(mockService)

	rec := common.Recommendation{
		ResourceType: "STANDARD",
	}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list SKUs")
}

func TestCloudStorageClient_GetOfferingDetails_DefaultPaymentOption(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// Both on-demand and commitment SKUs required after the issue #1020 fix.
	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: storageMockSkus("STANDARD", "us-central1", 26000000, 19500000),
		},
	}
	client.SetBillingService(mockService)

	rec := common.Recommendation{
		ResourceType:  "STANDARD",
		Term:          "1yr",
		PaymentOption: "unknown", // Default case
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	assert.Greater(t, details.UpfrontCost, float64(0))
}

func TestCloudStorageClient_ConvertGCPRecommendation(t *testing.T) {
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
						{Resource: "projects/test/buckets/COLDLINE"},
					},
				},
			},
		},
	}

	rec := client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{})
	require.NotNil(t, rec)
	assert.Equal(t, common.ProviderGCP, rec.Provider)
	assert.Equal(t, common.ServiceStorage, rec.Service)
	assert.Equal(t, "test-project", rec.Account)
	assert.Equal(t, "us-central1", rec.Region)
	assert.Equal(t, "COLDLINE", rec.ResourceType)
	assert.Equal(t, 50.5, rec.EstimatedSavings)
	assert.Equal(t, common.CommitmentReservedCapacity, rec.CommitmentType)
}

func TestCloudStorageClient_ConvertGCPRecommendation_NilContent(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	gcpRec := &recommenderpb.Recommendation{
		Name:    "test-rec",
		Content: nil,
	}

	rec := client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{})
	require.NotNil(t, rec)
	assert.Equal(t, common.ProviderGCP, rec.Provider)
	assert.Empty(t, rec.ResourceType)
}

func TestCloudStorageClient_ConvertGCPRecommendation_NilPrimaryImpact(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	gcpRec := &recommenderpb.Recommendation{
		Name:          "test-rec",
		PrimaryImpact: nil,
		Content: &recommenderpb.RecommendationContent{
			OperationGroups: []*recommenderpb.OperationGroup{
				{
					Operations: []*recommenderpb.Operation{
						{Resource: "projects/test/buckets/STANDARD"},
					},
				},
			},
		},
	}

	rec := client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{})
	require.NotNil(t, rec)
	assert.Equal(t, float64(0), rec.EstimatedSavings)
}

func TestCloudStorageClient_GetStoragePricing_WithCommitmentPrice(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: []*cloudbilling.Sku{
				{
					Description:    "Standard Storage in us-central1",
					ServiceRegions: []string{"us-central1"},
					PricingInfo: []*cloudbilling.PricingInfo{
						{
							PricingExpression: &cloudbilling.PricingExpression{
								TieredRates: []*cloudbilling.TierRate{
									{
										UnitPrice: &cloudbilling.Money{
											Units:        0,
											Nanos:        26000000,
											CurrencyCode: "USD",
										},
									},
								},
							},
						},
					},
				},
				{
					Description:    "Standard Storage Commitment in us-central1",
					ServiceRegions: []string{"us-central1"},
					PricingInfo: []*cloudbilling.PricingInfo{
						{
							PricingExpression: &cloudbilling.PricingExpression{
								TieredRates: []*cloudbilling.TierRate{
									{
										UnitPrice: &cloudbilling.Money{
											Units:        0,
											Nanos:        20000000,
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

	pricing, err := client.getStoragePricing(ctx, "STANDARD", "us-central1", 1)
	require.NoError(t, err)
	assert.Equal(t, "USD", pricing.Currency)
	assert.Greater(t, pricing.OnDemandPrice, float64(0))
	// CommitmentPrice is the term total (per-unit SKU price * hoursInTerm).
	// onDemand unit = 0.026, commitment unit = 0.020, hoursInTerm = 8760.
	assert.InDelta(t, 0.02*8760, pricing.CommitmentPrice, 0.01)
	// HourlyRate is the per-unit commitment price (not divided by hoursInTerm).
	assert.InDelta(t, 0.02, pricing.HourlyRate, 0.0001)
	// SavingsPercentage should be positive and less than 100.
	assert.Greater(t, pricing.SavingsPercentage, float64(0))
	assert.Less(t, pricing.SavingsPercentage, float64(100))
}

func TestCloudStorageClient_GetStoragePricing_3Year(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// Both on-demand and commitment SKUs required after the issue #1020 fix:
	// without a commitment SKU, getStoragePricing returns an error.
	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: storageMockSkus("STANDARD", "us-central1", 26000000, 18200000),
		},
	}
	client.SetBillingService(mockService)

	pricing, err := client.getStoragePricing(ctx, "STANDARD", "us-central1", 3)
	require.NoError(t, err)
	assert.Greater(t, pricing.SavingsPercentage, float64(0))
	assert.Greater(t, pricing.OnDemandPrice, float64(0))
	assert.Greater(t, pricing.CommitmentPrice, float64(0))
}

// TestConvertGCPRecommendation_PropagatesTermFromParams is a regression test for
// the finding that convertGCPRecommendation hardcoded rec.Term = "1yr",
// ignoring params.Term. A caller requesting "3yr" must get "3yr" in the output.
//
// This test FAILS on the pre-fix code that always set Term = "1yr".
func TestCloudStorageConvertGCPRecommendation_PropagatesTermFromParams(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	gcpRec := &recommenderpb.Recommendation{Name: "test-rec"}

	tests := []struct {
		inputTerm string
		wantTerm  string
	}{
		{"3yr", "3yr"},
		{"1yr", "1yr"},
		{"", "1yr"}, // empty defaults to "1yr"
	}

	for _, tt := range tests {
		params := common.RecommendationParams{Term: tt.inputTerm}
		rec := client.convertGCPRecommendation(ctx, gcpRec, params)
		require.NotNil(t, rec)
		assert.Equal(t, tt.wantTerm, rec.Term,
			"params.Term %q must produce rec.Term %q", tt.inputTerm, tt.wantTerm)
	}
}

// TestGetStoragePricing_CommitmentPriceIsTermTotal is a regression test for the
// unit-mismatch bug where commitmentPrice (per-unit from SKU) was passed
// directly to calculateStorageSavingsPercentage which expects a term total,
// producing a ~99.99% savings percentage. After the fix, CommitmentPrice must
// equal unitRate * hoursInTerm and SavingsPercentage must be realistic.
//
// This test FAILS on the pre-fix code where CommitmentPrice == 0.02 (per-unit).
func TestGetStoragePricing_CommitmentPriceIsTermTotal(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// onDemand = $0.026/unit, commitment = $0.020/unit
	// hoursInTerm (1yr) = 8760
	// Expected: CommitmentPrice = 0.020*8760 = 175.2, OnDemandPrice = 0.026*8760 = 227.76
	// Expected savings ~= (227.76-175.2)/227.76*100 ~= 23%
	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: []*cloudbilling.Sku{
				{
					Description:    "Standard Storage in us-central1",
					ServiceRegions: []string{"us-central1"},
					PricingInfo: []*cloudbilling.PricingInfo{
						{
							PricingExpression: &cloudbilling.PricingExpression{
								TieredRates: []*cloudbilling.TierRate{
									{
										UnitPrice: &cloudbilling.Money{
											Units:        0,
											Nanos:        26000000, // $0.026/unit
											CurrencyCode: "USD",
										},
									},
								},
							},
						},
					},
				},
				{
					Description:    "Standard Storage Commitment in us-central1",
					ServiceRegions: []string{"us-central1"},
					PricingInfo: []*cloudbilling.PricingInfo{
						{
							PricingExpression: &cloudbilling.PricingExpression{
								TieredRates: []*cloudbilling.TierRate{
									{
										UnitPrice: &cloudbilling.Money{
											Units:        0,
											Nanos:        20000000, // $0.020/unit
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

	pricing, err := client.getStoragePricing(ctx, "STANDARD", "us-central1", 1)
	require.NoError(t, err)

	const hoursInYear = 8760.0
	// CommitmentPrice must be a term total, not the raw per-unit SKU rate.
	assert.InDelta(t, 0.02*hoursInYear, pricing.CommitmentPrice, 0.01,
		"CommitmentPrice must be commitment SKU unit rate * hoursInTerm")
	// HourlyRate must be the per-unit commitment rate.
	assert.InDelta(t, 0.02, pricing.HourlyRate, 0.0001,
		"HourlyRate must be the per-unit commitment SKU rate")
	// OnDemandPrice stays a term total.
	assert.InDelta(t, 0.026*hoursInYear, pricing.OnDemandPrice, 0.01)
	// SavingsPercentage must be ~23%, not ~99.99%.
	assert.Greater(t, pricing.SavingsPercentage, float64(1),
		"SavingsPercentage must not be ~99.99% (unit-mismatch bug)")
	assert.Less(t, pricing.SavingsPercentage, float64(60),
		"SavingsPercentage must be a realistic storage commitment discount")
}

func TestSkuMatchesStorageClass_CaseInsensitive(t *testing.T) {
	sku := &cloudbilling.Sku{
		Description:    "STANDARD Storage in Americas",
		ServiceRegions: []string{"us-central1"},
	}
	assert.True(t, skuMatchesStorageClass(sku, "standard", "us-central1"))
}

// TestCloudStorageClient_ConvertGCPRecommendation_PopulatesRecurringMonthlyCost verifies
// that convertGCPRecommendation sets a non-nil RecurringMonthlyCost when the billing
// service returns valid pricing. This is the regression test for issue #264: the
// pre-fix state left RecurringMonthlyCost nil, causing the frontend to render "—".
func TestCloudStorageClient_ConvertGCPRecommendation_PopulatesRecurringMonthlyCost(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// Provide a billing mock that returns a non-zero on-demand price so that
	// getStoragePricing succeeds and produces a positive CommitmentPrice.
	mockBilling := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: []*cloudbilling.Sku{
				{
					Description:    "Standard Storage in us-central1",
					ServiceRegions: []string{"us-central1"},
					PricingInfo: []*cloudbilling.PricingInfo{
						{
							PricingExpression: &cloudbilling.PricingExpression{
								TieredRates: []*cloudbilling.TierRate{
									{
										UnitPrice: &cloudbilling.Money{
											Units:        0,
											Nanos:        26000000,
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
	client.SetBillingService(mockBilling)

	gcpRec := &recommenderpb.Recommendation{
		Name: "test-rec",
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
						{Resource: "projects/test/buckets/STANDARD"},
					},
				},
			},
		},
	}

	rec := client.convertGCPRecommendation(ctx, gcpRec)
	require.NotNil(t, rec)
	require.NotNil(t, rec.RecurringMonthlyCost, "RecurringMonthlyCost must be non-nil when billing lookup succeeds")
	assert.Greater(t, *rec.RecurringMonthlyCost, float64(0))
}

// TestCloudStorageClient_ConvertGCPRecommendation_BillingFailure_RecurringMonthlyCostNil
// verifies that convertGCPRecommendation leaves RecurringMonthlyCost nil (rather than
// coercing to 0) when the billing service call fails, matching the sibling services'
// non-fatal-failure contract.
func TestCloudStorageClient_ConvertGCPRecommendation_BillingFailure_RecurringMonthlyCostNil(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockBilling := &MockBillingService{
		err: errors.New("billing API unavailable"),
	}
	client.SetBillingService(mockBilling)

	gcpRec := &recommenderpb.Recommendation{
		Name: "test-rec",
		Content: &recommenderpb.RecommendationContent{
			OperationGroups: []*recommenderpb.OperationGroup{
				{
					Operations: []*recommenderpb.Operation{
						{Resource: "projects/test/buckets/STANDARD"},
					},
				},
			},
		},
	}

	rec := client.convertGCPRecommendation(ctx, gcpRec)
	require.NotNil(t, rec)
	assert.Nil(t, rec.RecurringMonthlyCost, "RecurringMonthlyCost must remain nil when billing lookup fails")
}
