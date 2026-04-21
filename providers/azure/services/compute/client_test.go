package compute

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/azure/mocks"
)

func TestNewClient(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")

	require.NotNil(t, client)
	assert.Equal(t, "test-subscription", client.subscriptionID)
	assert.Equal(t, "eastus", client.region)
	assert.NotNil(t, client.httpClient)
}

func TestNewClientWithHTTP(t *testing.T) {
	mockHTTP := &mocks.MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	require.NotNil(t, client)
	assert.Equal(t, "test-subscription", client.subscriptionID)
	assert.Equal(t, "eastus", client.region)
	assert.Equal(t, mockHTTP, client.httpClient)
}

func TestComputeClient_GetServiceType(t *testing.T) {
	client := NewClient(nil, "sub", "region")
	assert.Equal(t, common.ServiceCompute, client.GetServiceType())
}

func TestComputeClient_GetRegion(t *testing.T) {
	tests := []struct {
		name     string
		region   string
		expected string
	}{
		{
			name:     "East US",
			region:   "eastus",
			expected: "eastus",
		},
		{
			name:     "West Europe",
			region:   "westeurope",
			expected: "westeurope",
		},
		{
			name:     "Japan East",
			region:   "japaneast",
			expected: "japaneast",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(nil, "sub", tt.region)
			assert.Equal(t, tt.expected, client.GetRegion())
		})
	}
}

func TestComputeClient_isAvailableInRegion(t *testing.T) {
	client := NewClient(nil, "sub", "eastus")

	eastus := "eastus"
	westus := "westus"
	westeurope := "westeurope"

	tests := []struct {
		name     string
		sku      *armcompute.ResourceSKU
		region   string
		expected bool
	}{
		{
			name: "SKU available in region",
			sku: &armcompute.ResourceSKU{
				Locations: []*string{&eastus, &westus},
			},
			region:   "eastus",
			expected: true,
		},
		{
			name: "SKU not available in region",
			sku: &armcompute.ResourceSKU{
				Locations: []*string{&westus, &westeurope},
			},
			region:   "eastus",
			expected: false,
		},
		{
			name: "SKU with nil locations",
			sku: &armcompute.ResourceSKU{
				Locations: nil,
			},
			region:   "eastus",
			expected: false,
		},
		{
			name: "Case insensitive match",
			sku: &armcompute.ResourceSKU{
				Locations: []*string{&eastus},
			},
			region:   "EastUS",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.isAvailableInRegion(tt.sku, tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestVMPricingStructure(t *testing.T) {
	pricing := VMPricing{
		HourlyRate:        0.10,
		ReservationPrice:  876.0,
		OnDemandPrice:     1752.0,
		Currency:          "USD",
		SavingsPercentage: 50.0,
	}

	assert.Equal(t, 0.10, pricing.HourlyRate)
	assert.Equal(t, 876.0, pricing.ReservationPrice)
	assert.Equal(t, 1752.0, pricing.OnDemandPrice)
	assert.Equal(t, "USD", pricing.Currency)
	assert.Equal(t, 50.0, pricing.SavingsPercentage)
}

func TestAzureRetailPriceStructure(t *testing.T) {
	price := AzureRetailPrice{
		Items: []AzureRetailPriceItem{
			{
				CurrencyCode:    "USD",
				RetailPrice:     0.10,
				UnitPrice:       0.10,
				ArmRegionName:   "eastus",
				ProductName:     "Virtual Machines Dv3 Series",
				ServiceName:     "Virtual Machines",
				ArmSKUName:      "Standard_D2_v3",
				ReservationTerm: "1 Year",
				Type:            "Reservation",
			},
		},
	}

	require.Len(t, price.Items, 1)
	assert.Equal(t, "USD", price.Items[0].CurrencyCode)
	assert.Equal(t, "Standard_D2_v3", price.Items[0].ArmSKUName)
	assert.Equal(t, "Virtual Machines", price.Items[0].ServiceName)
}

func TestComputeClient_Fields(t *testing.T) {
	// Test that client stores fields correctly
	client := NewClient(nil, "test-sub", "westus2")

	assert.Equal(t, "test-sub", client.subscriptionID)
	assert.Equal(t, "westus2", client.region)
	assert.Nil(t, client.cred)
}

func TestComputeClient_SetPagers(t *testing.T) {
	client := NewClient(nil, "sub", "eastus")

	recPager := &mocks.MockRecommendationsPager{}
	resPager := &mocks.MockReservationsDetailsPager{}
	skuPager := &mocks.MockResourceSKUsPager{}

	client.SetRecommendationsPager(recPager)
	client.SetReservationsPager(resPager)
	client.SetResourceSKUsPager(skuPager)

	assert.Equal(t, recPager, client.recommendationsPager)
	assert.Equal(t, resPager, client.reservationsPager)
	assert.Equal(t, skuPager, client.resourceSKUsPager)
}

func TestComputeClient_GetRecommendations_WithMock(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager with no recommendations
	mockPager := &mocks.MockRecommendationsPager{
		Results: nil,
		HasMore: true,
	}
	client.SetRecommendationsPager(mockPager)

	params := common.RecommendationParams{
		Service: common.ServiceCompute,
		Region:  "eastus",
	}

	recommendations, err := client.GetRecommendations(ctx, params)
	require.NoError(t, err)
	assert.Empty(t, recommendations)
}

func TestComputeClient_GetExistingCommitments_WithMock(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager with reservation details
	mockPager := &mocks.MockReservationsDetailsPager{
		Results: mocks.CreateSampleReservationDetails("test-subscription", "eastus"),
		HasMore: true,
	}
	client.SetReservationsPager(mockPager)

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Len(t, commitments, 1)
	assert.Equal(t, common.ProviderAzure, commitments[0].Provider)
	assert.Equal(t, common.ServiceCompute, commitments[0].Service)
	assert.Equal(t, "reservation-123", commitments[0].CommitmentID)
}

func TestComputeClient_GetExistingCommitments_Empty(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager with no reservation details
	mockPager := &mocks.MockReservationsDetailsPager{
		Results: nil,
		HasMore: true,
	}
	client.SetReservationsPager(mockPager)

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestComputeClient_GetValidResourceTypes_WithMock(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager with SKUs
	mockPager := &mocks.MockResourceSKUsPager{
		Results: mocks.CreateSampleResourceSKUs("eastus"),
		HasMore: true,
	}
	client.SetResourceSKUsPager(mockPager)

	vmSizes, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	assert.Len(t, vmSizes, 3)
	assert.Contains(t, vmSizes, "Standard_D2s_v3")
	assert.Contains(t, vmSizes, "Standard_D4s_v3")
	assert.Contains(t, vmSizes, "Standard_D8s_v3")
}

func TestComputeClient_GetValidResourceTypes_NoSKUs(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager with no SKUs
	mockPager := &mocks.MockResourceSKUsPager{
		Results: []*armcompute.ResourceSKU{},
		HasMore: true,
	}
	client.SetResourceSKUsPager(mockPager)

	_, err := client.GetValidResourceTypes(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no VM sizes found")
}

func TestComputeClient_ValidateOffering_Valid(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager with SKUs
	mockPager := &mocks.MockResourceSKUsPager{
		Results: mocks.CreateSampleResourceSKUs("eastus"),
		HasMore: true,
	}
	client.SetResourceSKUsPager(mockPager)

	rec := common.Recommendation{
		ResourceType: "Standard_D2s_v3",
	}

	err := client.ValidateOffering(ctx, rec)
	assert.NoError(t, err)
}

func TestComputeClient_ValidateOffering_Invalid(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager with SKUs
	mockPager := &mocks.MockResourceSKUsPager{
		Results: mocks.CreateSampleResourceSKUs("eastus"),
		HasMore: true,
	}
	client.SetResourceSKUsPager(mockPager)

	rec := common.Recommendation{
		ResourceType: "Invalid_SKU",
	}

	err := client.ValidateOffering(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Azure VM SKU")
}

func TestComputeClient_GetOfferingDetails_WithMock(t *testing.T) {
	ctx := context.Background()

	mockHTTP := &mocks.MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	// Setup mock HTTP response
	mockHTTP.On("Do", mock.Anything).Return(
		mocks.CreateMockHTTPResponse(http.StatusOK, mocks.CreateSampleVMPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_D2s_v3",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, "Standard_D2s_v3", details.ResourceType)
	assert.Equal(t, "1yr", details.Term)
	assert.Equal(t, "USD", details.Currency)
}

func TestComputeClient_GetOfferingDetails_3YearTerm(t *testing.T) {
	ctx := context.Background()

	mockHTTP := &mocks.MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	// Setup mock HTTP response
	mockHTTP.On("Do", mock.Anything).Return(
		mocks.CreateMockHTTPResponse(http.StatusOK, mocks.CreateSampleVMPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_D2s_v3",
		Term:          "3yr",
		PaymentOption: "monthly",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, "3yr", details.Term)
	assert.Equal(t, "monthly", details.PaymentOption)
}

func TestComputeClient_GetOfferingDetails_APIError(t *testing.T) {
	ctx := context.Background()

	mockHTTP := &mocks.MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	// Setup mock HTTP response with error status
	mockHTTP.On("Do", mock.Anything).Return(
		mocks.CreateMockHTTPResponse(http.StatusInternalServerError, "Internal Server Error"),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_D2s_v3",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pricing API returned status 500")
}

func TestComputeClient_GetOfferingDetails_NoPricing(t *testing.T) {
	ctx := context.Background()

	mockHTTP := &mocks.MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	// Setup mock HTTP response with empty items
	mockHTTP.On("Do", mock.Anything).Return(
		mocks.CreateMockHTTPResponse(http.StatusOK, `{"Items": []}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_D2s_v3",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no pricing data found")
}

// MockTokenCredential for testing PurchaseCommitment
type MockTokenCredential struct {
	token string
	err   error
}

func (m *MockTokenCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	if m.err != nil {
		return azcore.AccessToken{}, m.err
	}
	return azcore.AccessToken{
		Token:     m.token,
		ExpiresOn: time.Now().Add(time.Hour),
	}, nil
}

func TestComputeClient_PurchaseCommitment_Success(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		mocks.CreateMockHTTPResponse(http.StatusOK, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "Standard_D2s_v3",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 2000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.NotEmpty(t, result.CommitmentID)
	assert.Equal(t, 2000.0, result.Cost)
}

func TestComputeClient_PurchaseCommitment_3YearTerm(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		mocks.CreateMockHTTPResponse(http.StatusCreated, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "Standard_D2s_v3",
		Term:           "3yr",
		Count:          1,
		CommitmentCost: 5000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestComputeClient_PurchaseCommitment_Accepted(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		mocks.CreateMockHTTPResponse(http.StatusAccepted, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "Standard_D2s_v3",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 2000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestComputeClient_PurchaseCommitment_TokenError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{err: errors.New("token error")}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	rec := common.Recommendation{
		ResourceType: "Standard_D2s_v3",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to get access token")
}

func TestComputeClient_PurchaseCommitment_HTTPError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(nil, errors.New("network error"))

	rec := common.Recommendation{
		ResourceType: "Standard_D2s_v3",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to purchase reservation")
}

func TestComputeClient_PurchaseCommitment_BadStatus(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		mocks.CreateMockHTTPResponse(http.StatusBadRequest, `{"error": "invalid request"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType: "Standard_D2s_v3",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "reservation purchase failed with status 400")
}

// TestComputeClient_ConvertAzureVMRecommendation_NilGuards pins the new
// contract: unusable SDK payloads (nil, wrong concrete type, nil Properties)
// produce a nil *Recommendation so the caller can filter it out. Before
// this converter was wired through the shared Extract helper the stub
// returned a non-nil but useless recommendation for every nil input.
func TestComputeClient_ConvertAzureVMRecommendation_NilGuards(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")
	assert.Nil(t, client.convertAzureVMRecommendation(context.Background(), nil))
}

// TestComputeClient_ConvertAzureVMRecommendation_PopulatesAllFields asserts
// the converter forwards every helper-extracted field into the result and
// applies the Compute-service-specific constants (Service, CommitmentType,
// PaymentOption). Subscription/Account comes from the client, not the rec.
func TestComputeClient_ConvertAzureVMRecommendation_PopulatesAllFields(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")
	rec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("westeurope"),
		mocks.WithScope("Shared"),
		mocks.WithTerm("P3Y"),
		mocks.WithQuantity(2),
		mocks.WithNormalizedSize("Standard_D2s_v3"),
		mocks.WithCosts(100, 70, 30),
	)
	out := client.convertAzureVMRecommendation(context.Background(), rec)
	require.NotNil(t, out)
	assert.Equal(t, common.ProviderAzure, out.Provider)
	assert.Equal(t, common.ServiceCompute, out.Service)
	assert.Equal(t, "test-subscription", out.Account)
	assert.Equal(t, "westeurope", out.Region)
	assert.Equal(t, "Standard_D2s_v3", out.ResourceType)
	assert.Equal(t, 2, out.Count)
	assert.InDelta(t, 100.0, out.OnDemandCost, 1e-9)
	assert.InDelta(t, 70.0, out.CommitmentCost, 1e-9)
	assert.InDelta(t, 30.0, out.EstimatedSavings, 1e-9)
	assert.Equal(t, common.CommitmentReservedInstance, out.CommitmentType)
	assert.Equal(t, "3yr", out.Term)
	assert.Equal(t, "upfront", out.PaymentOption)

	// Details is populated from the payload's ResourceType (InstanceType
	// only — Platform/Tenancy/Scope are deferred to batched enrichment).
	require.NotNil(t, out.Details)
	details, ok := out.Details.(common.ComputeDetails)
	require.True(t, ok, "Details must be a common.ComputeDetails value")
	assert.Equal(t, "Standard_D2s_v3", details.InstanceType)
	assert.Empty(t, details.Platform, "Platform is deferred to batched enrichment")
	assert.Empty(t, details.Tenancy, "Tenancy is deferred to batched enrichment")
}

// TestFetchAzurePricing_WrapperSmokeTest verifies the compute-specific
// wrapper around pricing.FetchAll — constructs the URL with filter +
// api-version, passes the compute item type, and re-wraps the returned
// slice in the service-local *AzureRetailPrice envelope. Exhaustive
// pagination / self-referential / per-page-timeout behaviour lives in
// providers/azure/internal/pricing/retail_prices_test.go and is not
// duplicated here.
func TestFetchAzurePricing_WrapperSmokeTest(t *testing.T) {
	mockHTTP := &mocks.MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	body := `{"Items":[{"armSkuName":"Standard_D2s_v3","reservationTerm":"1 Year","type":"Reservation","retailPrice":100.0,"unitPrice":100.0,"currencyCode":"USD"}],"NextPageLink":""}`
	mockHTTP.On("Do", mock.Anything).Return(mocks.CreateMockHTTPResponse(http.StatusOK, body), nil).Once()

	result, err := client.fetchAzurePricing(context.Background(), "anything")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "Standard_D2s_v3", result.Items[0].ArmSKUName)
}
