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
	instances    *sqladmin.InstancesListResponse
	tiers        *sqladmin.TiersListResponse
	operation    *sqladmin.Operation
	err          error
	insertCalled bool
}

func (m *MockSQLAdminService) ListInstances(projectID string) (*sqladmin.InstancesListResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.instances, nil
}

func (m *MockSQLAdminService) InsertInstance(projectID string, instance *sqladmin.DatabaseInstance) (*sqladmin.Operation, error) {
	m.insertCalled = true
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

// TestCloudSQLClient_ValidateOffering_TierListError verifies that an error
// from the SQLAdmin tier-list call propagates through ValidateOffering.
//
// The previous variant of this test relied on absent application-default
// credentials to trigger the error path. That approach is environment-
// sensitive: machines with ADC configured (CI runners, developer laptops with
// gcloud auth) return nil from sqladmin.NewService, causing a false pass.
// Using an injected mock that always errors is deterministic regardless of
// the host environment. See issue #251.
func TestCloudSQLClient_ValidateOffering_TierListError(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockSQLAdminService{
		err: errors.New("injected tier-list failure"),
	}
	client.SetSQLAdminService(mockService)

	rec := common.Recommendation{
		ResourceType: "db-n1-standard-1",
	}

	err := client.ValidateOffering(ctx, rec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list SQL tiers")
}

// TestCloudSQLClient_GetExistingCommitments_ReturnsEmpty asserts that
// GetExistingCommitments always returns an empty slice regardless of the
// sqladmin instance list. Cloud SQL spend-based CUDs are not detectable via
// the sqladmin API; PricingPlan "PACKAGE" is a legacy billing mode, not a
// commitment indicator (10-L3).
func TestCloudSQLClient_GetExistingCommitments_ReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments, "Cloud SQL GetExistingCommitments must return empty (10-L3)")
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

// TestCloudSQLClient_PurchaseCommitment_NotSupported is the regression test for
// issue #640: Cloud SQL CUDs are spend-based and have no programmatic purchase API,
// so PurchaseCommitment must return ErrCommitmentPurchaseNotSupported and MUST NOT
// call any resource-creation API (it previously created a new billable SQL instance).
func TestCloudSQLClient_PurchaseCommitment_NotSupported(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	mockService := &MockSQLAdminService{
		// If PurchaseCommitment ever calls InsertInstance, this would report a
		// completed operation; the assertions below ensure it is never invoked.
		operation: &sqladmin.Operation{Status: "DONE"},
	}
	client.SetSQLAdminService(mockService)

	rec := common.Recommendation{
		ResourceType:   "db-n1-standard-1",
		CommitmentCost: 1000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})

	require.Error(t, err)
	assert.ErrorIs(t, err, common.ErrCommitmentPurchaseNotSupported)
	assert.False(t, result.Success)
	assert.Empty(t, result.CommitmentID)
	assert.ErrorIs(t, result.Error, common.ErrCommitmentPurchaseNotSupported)
	// The critical guarantee: a "purchase" must never create infrastructure.
	assert.False(t, mockService.insertCalled, "PurchaseCommitment must not call InsertInstance")
}

// sqlMockSkus returns a slice with both an on-demand and a commitment SKU for
// the given tier and region. Required by tests that exercise GetOfferingDetails
// after the issue #1020 fix (fabricated commitment prices are no longer allowed;
// both SKUs must be present for getSQLPricing to succeed).
func sqlMockSkus(tier, region string) []*cloudbilling.Sku {
	onDemandSKU := &cloudbilling.Sku{
		Description:    tier + " Cloud SQL",
		ServiceRegions: []string{region},
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
	}
	commitmentSKU := &cloudbilling.Sku{
		Description:    tier + " Cloud SQL commitment 1yr",
		ServiceRegions: []string{region},
		PricingInfo: []*cloudbilling.PricingInfo{
			{
				PricingExpression: &cloudbilling.PricingExpression{
					TieredRates: []*cloudbilling.TierRate{
						{
							UnitPrice: &cloudbilling.Money{
								Units:        0,
								Nanos:        42000000,
								CurrencyCode: "USD",
							},
						},
					},
				},
			},
		},
	}
	return []*cloudbilling.Sku{onDemandSKU, commitmentSKU}
}

func TestCloudSQLClient_GetOfferingDetails_WithMock(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// Both on-demand and commitment SKUs are required after the issue #1020 fix.
	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{Skus: sqlMockSkus("db-n1-standard-1", "us-central1")},
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

	// Both on-demand and commitment SKUs are required after the issue #1020 fix.
	mockService := &MockBillingService{
		skus: &cloudbilling.ListSkusResponse{Skus: sqlMockSkus("db-n1-standard-1", "us-central1")},
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

	rec := client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{})
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

	rec := client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{})
	require.NotNil(t, rec)
	assert.Equal(t, common.ProviderGCP, rec.Provider)
}

// TestCloudSQLConvertGCPRecommendation_PropagatesTermFromParams is a regression
// test for the finding that convertGCPRecommendation hardcoded rec.Term = "1yr",
// ignoring params.Term. A caller requesting "3yr" must get "3yr" in the output.
//
// This test FAILS on the pre-fix code that always set Term = "1yr".
func TestCloudSQLConvertGCPRecommendation_PropagatesTermFromParams(t *testing.T) {
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

// TestGetSQLPricing_CommitmentPriceIsTermTotal is a regression test for the
// unit-mismatch bug where commitmentPrice (per-hour from SKU) was passed
// directly to calculateSQLSavingsPercentage which expects a term total,
// producing a ~99.99% savings percentage. After the fix, CommitmentPrice must
// equal hourlyRate * hoursInTerm and SavingsPercentage must be realistic.
//
// This test FAILS on the pre-fix code where CommitmentPrice == 0.042 (per-hour).
func TestGetSQLPricing_CommitmentPriceIsTermTotal(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// onDemand = $0.05/hr, commitment = $0.042/hr
	// hoursInTerm (1yr) = 8760
	// Expected: CommitmentPrice = 0.042*8760 = 367.92, OnDemandPrice = 0.05*8760 = 438
	// Expected savings ~= (438-367.92)/438*100 ~= 16%
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
											Nanos:        50000000, // $0.05/hr
											CurrencyCode: "USD",
										},
									},
								},
							},
						},
					},
				},
				{
					Description:    "db-n1-standard-1 Cloud SQL commitment",
					ServiceRegions: []string{"us-central1"},
					PricingInfo: []*cloudbilling.PricingInfo{
						{
							PricingExpression: &cloudbilling.PricingExpression{
								TieredRates: []*cloudbilling.TierRate{
									{
										UnitPrice: &cloudbilling.Money{
											Units:        0,
											Nanos:        42000000, // $0.042/hr
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

	pricing, err := client.getSQLPricing(ctx, "db-n1-standard-1", "us-central1", 1)
	require.NoError(t, err)

	const hoursInYear = 8760.0
	// CommitmentPrice must be a term total, not the raw per-hour SKU rate.
	assert.InDelta(t, 0.042*hoursInYear, pricing.CommitmentPrice, 0.01,
		"CommitmentPrice must be commitment SKU hourly rate * hoursInTerm")
	// HourlyRate must be the per-hour commitment rate.
	assert.InDelta(t, 0.042, pricing.HourlyRate, 0.0001,
		"HourlyRate must be the per-hour commitment SKU rate")
	// SavingsPercentage must be realistic (not ~99.99%).
	assert.Greater(t, pricing.SavingsPercentage, float64(0),
		"SavingsPercentage must be positive")
	assert.Less(t, pricing.SavingsPercentage, float64(50),
		"SavingsPercentage must be realistic (not ~100%)")
}

func TestCloudSQLClient_ConvertGCPRecommendation_RecurringMonthlyCost(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// Inject a billing mock with a known on-demand price and a separate
	// commitment SKU. getSQLPricing derives CommitmentPrice from the
	// "commitment" SKU (CommitmentPrice = commitmentHourly * 8760), so
	// RecurringMonthlyCost must equal CommitmentPrice / 12 (one year = 12 months).
	const onDemandHourly = 0.12    // USD/h per vCPU -- representative db-n1-standard-1 value
	const commitmentHourly = 0.102 // USD/h -- 1yr CUD rate from the catalog
	mockBilling := &MockBillingService{
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
											Nanos:        int64(onDemandHourly * 1e9),
											CurrencyCode: "USD",
										},
									},
								},
							},
						},
					},
				},
				{
					// Commitment SKU required by getSQLPricing: without a
					// "commitment" SKU it errors and RecurringMonthlyCost stays nil.
					Description:    "db-n1-standard-1 Cloud SQL commitment 1yr",
					ServiceRegions: []string{"us-central1"},
					PricingInfo: []*cloudbilling.PricingInfo{
						{
							PricingExpression: &cloudbilling.PricingExpression{
								TieredRates: []*cloudbilling.TierRate{
									{
										UnitPrice: &cloudbilling.Money{
											Units:        0,
											Nanos:        int64(commitmentHourly * 1e9),
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
					Cost: &money.Money{Units: -100, CurrencyCode: "USD"},
				},
			},
		},
		Content: &recommenderpb.RecommendationContent{
			OperationGroups: []*recommenderpb.OperationGroup{
				{
					Operations: []*recommenderpb.Operation{
						{Resource: "projects/test/instances/db-n1-standard-1"},
					},
				},
			},
		},
	}

	rec := client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{})
	require.NotNil(t, rec)

	// RecurringMonthlyCost must be a non-nil pointer to a positive value for
	// a monthly Cloud SQL CUD recommendation.
	require.NotNil(t, rec.RecurringMonthlyCost, "RecurringMonthlyCost must be non-nil when billing lookup succeeds")
	assert.Greater(t, *rec.RecurringMonthlyCost, 0.0, "RecurringMonthlyCost must be positive for a monthly Cloud SQL CUD")

	// Verify the value matches CommitmentPrice / 12 exactly. CommitmentPrice is
	// the commitment SKU's hourly rate scaled to the 1yr term total.
	const hoursIn1yr = 8760.0
	expectedMonthly := commitmentHourly * hoursIn1yr / 12
	assert.InDelta(t, expectedMonthly, *rec.RecurringMonthlyCost, 1e-6)
}

func TestCloudSQLClient_ConvertGCPRecommendation_RecurringMonthlyCost_BillingFailure(t *testing.T) {
	ctx := context.Background()
	client, _ := NewClient(ctx, "test-project", "us-central1")

	// Inject a billing mock that always errors; RecurringMonthlyCost must
	// remain nil (frontend renders "—") rather than a zero or stale value.
	client.SetBillingService(&MockBillingService{err: errors.New("billing unavailable")})

	gcpRec := &recommenderpb.Recommendation{
		Name: "test-rec",
		Content: &recommenderpb.RecommendationContent{
			OperationGroups: []*recommenderpb.OperationGroup{
				{Operations: []*recommenderpb.Operation{{Resource: "projects/test/instances/db-n1-standard-1"}}},
			},
		},
	}

	rec := client.convertGCPRecommendation(ctx, gcpRec, common.RecommendationParams{})
	require.NotNil(t, rec)
	assert.Nil(t, rec.RecurringMonthlyCost, "RecurringMonthlyCost must be nil when billing lookup fails")
}
