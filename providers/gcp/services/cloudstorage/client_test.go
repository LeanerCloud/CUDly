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
	buckets    []*storage.BucketAttrs
	listErr    error
	bucketName string
	createErr  error
}

func (m *MockStorageService) Buckets(ctx context.Context, projectID string) BucketIterator {
	return &MockBucketIterator{buckets: m.buckets, err: m.listErr}
}

func (m *MockStorageService) Bucket(name string) BucketHandle {
	m.bucketName = name
	return &MockBucketHandle{createErr: m.createErr}
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
	createErr error
}

func (m *MockBucketHandle) Create(ctx context.Context, projectID string, attrs *storage.BucketAttrs) error {
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

func TestCloudStorageClient_GetExistingCommitments_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockStorageService{
		buckets: []*storage.BucketAttrs{
			{
				Name:         "bucket-1",
				Location:     "us-central1",
				StorageClass: "STANDARD",
			},
			{
				Name:         "bucket-2",
				Location:     "us-central1",
				StorageClass: "NEARLINE",
			},
			{
				Name:         "bucket-other-region",
				Location:     "europe-west1",
				StorageClass: "COLDLINE",
			},
		},
	}
	client.SetStorageService(mockService)

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	// Only buckets in the matching region should be returned
	assert.Len(t, commitments, 2)
	assert.Equal(t, "bucket-1", commitments[0].CommitmentID)
	assert.Equal(t, "STANDARD", commitments[0].ResourceType)
	assert.Equal(t, common.ProviderGCP, commitments[0].Provider)
	assert.Equal(t, common.ServiceStorage, commitments[0].Service)
	assert.Equal(t, "bucket-2", commitments[1].CommitmentID)
	assert.Equal(t, "NEARLINE", commitments[1].ResourceType)
}

func TestCloudStorageClient_GetExistingCommitments_Error(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockStorageService{
		listErr: errors.New("API error"),
	}
	client.SetStorageService(mockService)

	_, err := client.GetExistingCommitments(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list buckets")
}

func TestCloudStorageClient_GetExistingCommitments_Empty(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockStorageService{
		buckets: []*storage.BucketAttrs{},
	}
	client.SetStorageService(mockService)

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestCloudStorageClient_PurchaseCommitment_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockStorageService{
		createErr: nil,
	}
	client.SetStorageService(mockService)

	rec := common.Recommendation{
		ResourceType:   "STANDARD",
		CommitmentCost: 100.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.NotEmpty(t, result.CommitmentID)
	assert.Equal(t, 100.0, result.Cost)
}

func TestCloudStorageClient_PurchaseCommitment_Error(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockStorageService{
		createErr: errors.New("bucket creation failed"),
	}
	client.SetStorageService(mockService)

	rec := common.Recommendation{
		ResourceType: "STANDARD",
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
	assert.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to create storage bucket")
}

func TestCloudStorageClient_GetRecommendations_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockClient := &MockRecommenderClient{
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

	recommendations, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	require.NoError(t, err) // Error during iteration is handled gracefully
	assert.Empty(t, recommendations)
}

func TestCloudStorageClient_GetOfferingDetails_WithMock(t *testing.T) {
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
			},
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

	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{
			Skus: []*cloudbilling.Sku{
				{
					Description:    "Nearline Storage in us-central1",
					ServiceRegions: []string{"us-central1"},
					PricingInfo: []*cloudbilling.PricingInfo{
						{
							PricingExpression: &cloudbilling.PricingExpression{
								TieredRates: []*cloudbilling.TierRate{
									{
										UnitPrice: &cloudbilling.Money{
											Units:        0,
											Nanos:        10000000,
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
			},
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

	rec := client.convertGCPRecommendation(ctx, gcpRec)
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

	rec := client.convertGCPRecommendation(ctx, gcpRec)
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

	rec := client.convertGCPRecommendation(ctx, gcpRec)
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
	// When commitment price is found, it should be used
	assert.Equal(t, float64(0.02), pricing.CommitmentPrice)
}

func TestCloudStorageClient_GetStoragePricing_3Year(t *testing.T) {
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
			},
		},
	}
	client.SetBillingService(mockService)

	pricing, err := client.getStoragePricing(ctx, "STANDARD", "us-central1", 3)
	require.NoError(t, err)
	// 3-year should have 30% savings vs 25% for 1-year
	assert.Greater(t, pricing.SavingsPercentage, float64(25))
}

func TestSkuMatchesStorageClass_CaseInsensitive(t *testing.T) {
	sku := &cloudbilling.Sku{
		Description:    "STANDARD Storage in Americas",
		ServiceRegions: []string{"us-central1"},
	}
	assert.True(t, skuMatchesStorageClass(sku, "standard", "us-central1"))
}
