package compute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
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

// TestComputeClient_GetRecommendations_EmitsBothPaymentVariants asserts that
// a single Azure reservation recommendation from the API is expanded into two
// entries — "upfront" and "monthly" — with correct cashflow split and
// identical savings figures.
func TestComputeClient_GetRecommendations_EmitsBothPaymentVariants(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Inject a single recommendation via the mock pager.
	apiRec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("eastus"),
		mocks.WithScope("Shared"),
		mocks.WithTerm("P1Y"),
		mocks.WithQuantity(1),
		mocks.WithNormalizedSize("Standard_D2s_v3"),
		mocks.WithCosts(120, 84, 36), // onDemand=120, commitment=84, savings=36
	)
	mockPager := &mocks.MockRecommendationsPager{
		Results: []armconsumption.ReservationRecommendationClassification{apiRec},
		HasMore: true,
	}
	client.SetRecommendationsPager(mockPager)

	recs, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	require.NoError(t, err)
	require.Len(t, recs, 2, "one API rec must expand to two payment-variant entries")

	payments := make(map[string]common.Recommendation)
	for _, r := range recs {
		payments[r.PaymentOption] = r
	}
	require.Contains(t, payments, "upfront", "upfront variant must be present")
	require.Contains(t, payments, "monthly", "monthly variant must be present")

	allUp := payments["upfront"]
	noUp := payments["monthly"]

	// Cashflow: upfront has zero recurring; monthly spreads over 12 months.
	require.NotNil(t, allUp.RecurringMonthlyCost)
	assert.InDelta(t, 0.0, *allUp.RecurringMonthlyCost, 1e-9)
	require.NotNil(t, noUp.RecurringMonthlyCost)
	assert.InDelta(t, 84.0/12.0, *noUp.RecurringMonthlyCost, 1e-9)

	// Savings must be identical across variants.
	assert.InDelta(t, allUp.EstimatedSavings, noUp.EstimatedSavings, 1e-9)
	assert.InDelta(t, allUp.SavingsPercentage, noUp.SavingsPercentage, 1e-9)

	// Shared fields.
	assert.Equal(t, "test-subscription", allUp.Account)
	assert.Equal(t, common.ServiceCompute, allUp.Service)
	assert.Equal(t, "1yr", allUp.Term)
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

func TestComputeClient_ValidateOffering_CaseInsensitive(t *testing.T) {
	ctx := context.Background()

	t.Run("case_insensitive", func(t *testing.T) {
		client := NewClient(nil, "test-subscription", "eastus")
		mockPager := &mocks.MockResourceSKUsPager{
			Results: mocks.CreateSampleResourceSKUs("eastus"),
			HasMore: true,
		}
		client.SetResourceSKUsPager(mockPager)
		rec := common.Recommendation{ResourceType: "standard_d2s_v3"}
		err := client.ValidateOffering(ctx, rec)
		assert.NoError(t, err)
	})

	t.Run("whitespace_trimmed", func(t *testing.T) {
		client := NewClient(nil, "test-subscription", "eastus")
		mockPager := &mocks.MockResourceSKUsPager{
			Results: mocks.CreateSampleResourceSKUs("eastus"),
			HasMore: true,
		}
		client.SetResourceSKUsPager(mockPager)
		rec := common.Recommendation{ResourceType: "  Standard_D2s_v3  "}
		err := client.ValidateOffering(ctx, rec)
		assert.NoError(t, err)
	})
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

// TestComputeClient_GetOfferingDetails_UnsupportedPaymentOption verifies that an
// unrecognised payment option fails loud rather than being silently billed as
// all-upfront (owner policy: no silent fallbacks on money-affecting fields).
// Pricing is fully present, so the failure is solely on the payment-option
// branch. Pre-fix the default branch set upfrontCost = totalCost and returned a
// valid OfferingDetails with no error.
func TestComputeClient_GetOfferingDetails_UnsupportedPaymentOption(t *testing.T) {
	ctx := context.Background()

	mockHTTP := &mocks.MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)
	mockHTTP.On("Do", mock.Anything).Return(
		mocks.CreateMockHTTPResponse(http.StatusOK, mocks.CreateSampleVMPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_D2s_v3",
		Term:          "1yr",
		PaymentOption: "weekly-bananas",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.Error(t, err)
	assert.Nil(t, details)
	assert.Contains(t, err.Error(), "unsupported payment option")
}

// TestComputeClient_GetOfferingDetails_NoReservationPricing verifies that when
// on-demand pricing is present but no reservation line is returned, the client
// returns an error rather than fabricating a price from the hardcoded 0.62
// multiplier (issue #1020 H4). Pre-fix this would have silently surfaced a
// fabricated TotalCost/SavingsPercentage as a real quote.
func TestComputeClient_GetOfferingDetails_NoReservationPricing(t *testing.T) {
	ctx := context.Background()

	onDemandOnly := `{
		"Items": [
			{
				"currencyCode": "USD",
				"retailPrice": 0.096,
				"unitPrice": 0.096,
				"armRegionName": "eastus",
				"armSkuName": "Standard_D2s_v3",
				"type": "Consumption"
			}
		],
		"NextPageLink": ""
	}`

	mockHTTP := &mocks.MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)
	mockHTTP.On("Do", mock.Anything).Return(
		mocks.CreateMockHTTPResponse(http.StatusOK, onDemandOnly), nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_D2s_v3",
		Term:          "1yr",
		PaymentOption: "upfront",
	}
	_, err := client.GetOfferingDetails(ctx, rec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no reservation pricing found")
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

// calcPriceRespJSON returns a minimal valid calculatePrice JSON response with the
// given reservationOrderId. Used by PurchaseCommitment tests that need the
// two-step calculatePrice->purchase flow.
func calcPriceRespJSON(orderID string) string {
	return `{"properties":{"reservationOrderId":"` + orderID + `"}}`
}

// capacityProviderRegistered is the JSON response that satisfies
// ensureCapacityProviderRegistered's GET check when the provider is already
// registered. The compute client calls this once per client lifetime before any
// purchase attempt, so tests that call PurchaseCommitment must expect it.
const capacityProviderRegistered = `{"registrationState":"Registered"}`

// mockCapacityProviderCheck adds a mock expectation for the capacity provider
// registration GET request made by ensureCapacityProviderRegistered.
func mockCapacityProviderCheck(m *mocks.MockHTTPClient) {
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path, "providers/Microsoft.Capacity") &&
			!strings.Contains(r.URL.Path, "calculatePrice") &&
			!strings.Contains(r.URL.Path, "reservationOrders")
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, capacityProviderRegistered), nil).Once()
}

func TestComputeClient_PurchaseCommitment_Success(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockCapacityProviderCheck(mockHTTP)
	// Step 1: calculatePrice returns a reservationOrderId.
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, calcPriceRespJSON("order-vm-001")), nil).Once()
	// Step 2: purchase with the Azure-minted order ID.
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/order-vm-001/purchase"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, `{"id": "order-vm-001"}`), nil).Once()

	rec := common.Recommendation{
		ResourceType:   "Standard_D2s_v3",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 2000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "order-vm-001", result.CommitmentID)
	assert.Equal(t, 2000.0, result.Cost)
	mockHTTP.AssertExpectations(t)
}

func TestComputeClient_PurchaseCommitment_3YearTerm(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockCapacityProviderCheck(mockHTTP)
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, calcPriceRespJSON("order-vm-3yr")), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/order-vm-3yr/purchase"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusCreated, `{}`), nil).Once()

	rec := common.Recommendation{
		ResourceType:   "Standard_D2s_v3",
		Term:           "3yr",
		Count:          1,
		CommitmentCost: 5000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "order-vm-3yr", result.CommitmentID)
	mockHTTP.AssertExpectations(t)
}

func TestComputeClient_PurchaseCommitment_Accepted(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockCapacityProviderCheck(mockHTTP)
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, calcPriceRespJSON("order-vm-202")), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/order-vm-202/purchase"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusAccepted, `{}`), nil).Once()

	rec := common.Recommendation{
		ResourceType:   "Standard_D2s_v3",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 2000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	mockHTTP.AssertExpectations(t)
}

func TestComputeClient_PurchaseCommitment_TokenError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{err: errors.New("token error")}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	rec := common.Recommendation{
		ResourceType: "Standard_D2s_v3",
		Term:         "1yr",
		Count:        1,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to get access token")
}

func TestComputeClient_PurchaseCommitment_HTTPError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockCapacityProviderCheck(mockHTTP)
	// Network error on the calculatePrice call.
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(nil, errors.New("network error")).Once()

	rec := common.Recommendation{
		ResourceType: "Standard_D2s_v3",
		Term:         "1yr",
		Count:        1,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "calculatePrice HTTP call")
}

func TestComputeClient_PurchaseCommitment_BadStatus(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockCapacityProviderCheck(mockHTTP)
	// calculatePrice returns 200 with an order ID, but purchase returns 400.
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, calcPriceRespJSON("order-bad")), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/order-bad/purchase"
	})).Return(
		mocks.CreateMockHTTPResponse(http.StatusBadRequest, `{"error":{"code":"InvalidScope","message":"invalid request"}}`),
		nil,
	).Once()

	rec := common.Recommendation{
		ResourceType: "Standard_D2s_v3",
		Term:         "1yr",
		Count:        1,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "reservation purchase failed with status 400")
	mockHTTP.AssertExpectations(t)
}

// TestComputeClient_PurchaseCommitment_TwoStepFlow verifies that
// PurchaseCommitment makes exactly two HTTP calls: POST calculatePrice then
// POST purchase, and that the CommitmentID is the Azure-minted order ID (not a
// client-generated GUID). This is the regression test for issue #677.
func TestComputeClient_PurchaseCommitment_TwoStepFlow(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	const azureMintedOrderID = "azure-minted-order-677"

	mockCapacityProviderCheck(mockHTTP)
	// Expect exactly one calculatePrice POST.
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, calcPriceRespJSON(azureMintedOrderID)), nil).Once()

	// Expect exactly one purchase POST to the Azure-minted order path.
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost &&
			r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/"+azureMintedOrderID+"/purchase"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, `{}`), nil).Once()

	rec := common.Recommendation{
		ResourceType:   "Standard_B2ats_v2", // The SKU that triggered issue #677.
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 500.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	// CommitmentID must be the Azure-minted ID, not a client-generated GUID.
	assert.Equal(t, azureMintedOrderID, result.CommitmentID)
	// Exactly 3 HTTP calls: capacity-provider-check + calculatePrice + purchase.
	mockHTTP.AssertExpectations(t)
	mockHTTP.AssertNumberOfCalls(t, "Do", 3)
}

// TestComputeClient_PurchaseCommitment_SessionTimeoutRetry verifies that a
// "Session timed out" 400 on the purchase endpoint causes PurchaseCommitment to
// re-run calculatePrice and retry the purchase (issue #677 regression test).
func TestComputeClient_PurchaseCommitment_SessionTimeoutRetry(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	sessionTimeoutBody := `{"error":{"code":"BadRequest","message":"Session timed out - Call CalculatePrice again and provide the new Reservation Order ID for purchase"}}`

	mockCapacityProviderCheck(mockHTTP)
	// First calculatePrice: mints "order-first".
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, calcPriceRespJSON("order-first")), nil).Once()
	// First purchase: session timeout.
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/order-first/purchase"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusBadRequest, sessionTimeoutBody), nil).Once()

	// Second calculatePrice: mints "order-second".
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, calcPriceRespJSON("order-second")), nil).Once()
	// Second purchase: succeeds.
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/order-second/purchase"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, `{}`), nil).Once()

	rec := common.Recommendation{ResourceType: "Standard_B2ats_v2", Term: "1yr", Count: 1}
	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "order-second", result.CommitmentID)
	mockHTTP.AssertExpectations(t)
}

// TestComputeClient_PurchaseCommitment_TagInjection verifies that the
// purchase-automation tag carrying opts.Source is present in the
// calculatePrice request body. Without this regression test the dedupe
// guard introduced for the Azure two-step flow could regress silently:
// the call would succeed without the tag and re-driven purchases would
// duplicate reservations server-side.
func TestComputeClient_PurchaseCommitment_TagInjection(t *testing.T) {
	const orderID = "compute-tag-test"
	const source = common.PurchaseSourceWeb

	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockCapacityProviderCheck(mockHTTP)

	var capturedBody []byte
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		if r.Method != http.MethodPost || r.URL.Path != "/providers/Microsoft.Capacity/calculatePrice" {
			return false
		}
		capturedBody, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(capturedBody))
		return true
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, calcPriceRespJSON(orderID)), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost &&
			r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/"+orderID+"/purchase"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, `{}`), nil).Once()

	rec := common.Recommendation{ResourceType: "Standard_D2s_v3", Term: "1yr", Count: 1, CommitmentCost: 2000.0}
	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: source})
	require.NoError(t, err)
	assert.True(t, result.Success)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(capturedBody, &body))
	tags, hasTags := body["tags"].(map[string]interface{})
	require.True(t, hasTags, "tags field must be present in calculatePrice body when Source is set")
	assert.Equal(t, source, tags[common.PurchaseTagKey], "tag value must match opts.Source")
	mockHTTP.AssertExpectations(t)
}

// TestComputeClient_PurchaseCommitment_RequiresSource pins the dedupe guard:
// PurchaseCommitment must reject an empty opts.Source before issuing any HTTP
// call (including the cached Microsoft.Capacity provider-registration check).
// Azure mints the reservation order ID server-side, so the
// purchase-automation tag derived from Source is the only stable dedupe
// signal CUDly controls -- proceeding without it would allow a re-driven
// purchase to create a duplicate reservation.
func TestComputeClient_PurchaseCommitment_RequiresSource(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	rec := common.Recommendation{ResourceType: "Standard_D2s_v3", Term: "1yr", Count: 1}
	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "purchase source is required")
	mockHTTP.AssertNotCalled(t, "Do", mock.Anything)
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

	// RecurringMonthlyCost is the covered/effective cost (paid WITH the
	// reservation) = TotalCostWithReservedInstances, not 0.
	require.NotNil(t, out.RecurringMonthlyCost, "RecurringMonthlyCost must be populated for Azure compute recs")
	assert.InDelta(t, 70.0, *out.RecurringMonthlyCost, 1e-9)

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

func TestBuildReservationBody_IncludesPurchaseAutomationTag(t *testing.T) {
	c := &ComputeClient{region: "eastus", subscriptionID: "sub-abc"}
	rec := common.Recommendation{ResourceType: "Standard_D2s_v3", Count: 1, Term: "1yr"}

	body, err := c.buildReservationBody(rec, common.PurchaseSourceWeb, "")
	require.NoError(t, err)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &got))
	tags, ok := got["tags"].(map[string]interface{})
	require.True(t, ok, "tags map missing from reservation body")
	assert.Equal(t, common.PurchaseSourceWeb, tags[common.PurchaseTagKey])
}

func TestBuildReservationBody_OmitsTagsWhenSourceAndTokenEmpty(t *testing.T) {
	c := &ComputeClient{region: "eastus", subscriptionID: "sub-abc"}
	rec := common.Recommendation{ResourceType: "Standard_D2s_v3", Count: 1, Term: "1yr"}

	body, err := c.buildReservationBody(rec, "", "")
	require.NoError(t, err)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &got))
	_, present := got["tags"]
	assert.False(t, present, "tags must be absent when both source and idempotency token are empty")
}

// TestBuildReservationBody_IncludesIdempotencyTokenTag pins the issue #721
// fix: the cudly-idempotency-token tag MUST ride along with the
// purchase-automation tag in the reservation body so a re-driven purchase
// can find the prior reservation via FindReservationOrderByIdempotencyToken
// and skip the duplicate buy.
func TestBuildReservationBody_IncludesIdempotencyTokenTag(t *testing.T) {
	c := &ComputeClient{region: "eastus", subscriptionID: "sub-abc"}
	rec := common.Recommendation{ResourceType: "Standard_D2s_v3", Count: 1, Term: "1yr"}
	token := common.DeriveIdempotencyToken("exec-721-compute", 0)

	body, err := c.buildReservationBody(rec, common.PurchaseSourceWeb, token)
	require.NoError(t, err)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &got))
	tags, ok := got["tags"].(map[string]interface{})
	require.True(t, ok, "tags map missing from reservation body")
	assert.Equal(t, common.PurchaseSourceWeb, tags[common.PurchaseTagKey])
	assert.Equal(t, token, tags[common.IdempotencyTagKey], "idempotency tag must be stamped when token is supplied")
}

// --- Issue #148: VCPU/MemoryGB enrichment via cached SKU catalogue ---

// vmSKUCatalogueMockPager is a multi-page-and-error-capable mock used
// only by the issue-148 SKU-catalogue tests below. The shared
// mocks.MockResourceSKUsPager doesn't expose its page counter and
// can't simulate a NextPage error — file-scoped here keeps the shared
// mock surface untouched (matches the cosmosdb / cache test pattern
// where each service defines its own catalogue mock).
type vmSKUCatalogueMockPager struct {
	pages    []armcompute.ResourceSKUsClientListResponse
	index    int
	err      error
	pageHits int // count of NextPage invocations — pinned by FetchedOnce
}

func (m *vmSKUCatalogueMockPager) More() bool {
	return m.index < len(m.pages)
}

func (m *vmSKUCatalogueMockPager) NextPage(_ context.Context) (armcompute.ResourceSKUsClientListResponse, error) {
	m.pageHits++
	if m.err != nil {
		return armcompute.ResourceSKUsClientListResponse{}, m.err
	}
	if m.index >= len(m.pages) {
		return armcompute.ResourceSKUsClientListResponse{}, errors.New("no more pages")
	}
	page := m.pages[m.index]
	m.index++
	return page, nil
}

// buildVMSKU constructs an armcompute.ResourceSKU for "virtualMachines"
// in the given region with the standard vCPUs / MemoryGB capabilities
// the converter parses out.
func buildVMSKU(name, region string, vCPUs int, memoryGB string) *armcompute.ResourceSKU {
	resourceType := "virtualMachines"
	regionStr := region
	nameStr := name
	vcpuName := "vCPUs"
	vcpuVal := strconv.Itoa(vCPUs)
	memName := "MemoryGB"
	memVal := memoryGB
	return &armcompute.ResourceSKU{
		Name:         &nameStr,
		ResourceType: &resourceType,
		Locations:    []*string{&regionStr},
		Capabilities: []*armcompute.ResourceSKUCapabilities{
			{Name: &vcpuName, Value: &vcpuVal},
			{Name: &memName, Value: &memVal},
		},
	}
}

// TestComputeClient_ConvertAzureVMRecommendation_PopulatesVCPUAndMemoryFromSKUCache
// asserts the cached SKU-catalogue lookup populates ComputeDetails.VCPU
// and ComputeDetails.MemoryGB when the catalogue contains the SKU named
// in the recommendation. Pin for issue #148.
func TestComputeClient_ConvertAzureVMRecommendation_PopulatesVCPUAndMemoryFromSKUCache(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")

	mockPager := &vmSKUCatalogueMockPager{
		pages: []armcompute.ResourceSKUsClientListResponse{
			{
				ResourceSKUsResult: armcompute.ResourceSKUsResult{
					Value: []*armcompute.ResourceSKU{
						buildVMSKU("Standard_D2s_v3", "eastus", 2, "8"),
						buildVMSKU("Standard_D4s_v3", "eastus", 4, "16"),
					},
				},
			},
		},
	}
	client.SetResourceSKUsPager(mockPager)

	rec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("eastus"),
		mocks.WithNormalizedSize("Standard_D2s_v3"),
	)
	out := client.convertAzureVMRecommendation(context.Background(), rec)
	require.NotNil(t, out)
	details, ok := out.Details.(common.ComputeDetails)
	require.True(t, ok)
	assert.Equal(t, "Standard_D2s_v3", details.InstanceType)
	assert.Equal(t, 2, details.VCPU, "VCPU must be enriched from the cached SKU catalogue")
	assert.Equal(t, 8.0, details.MemoryGB, "MemoryGB must be enriched from the cached SKU catalogue")
}

// TestComputeClient_ConvertAzureVMRecommendation_PagerErrorFallsBack
// asserts that a SKU-catalogue fetch failure does NOT fail the
// conversion — VCPU/MemoryGB stay at 0 and the rest of Details is
// populated from the recommendation payload. Graceful-degradation
// contract from PR #81, now extended to compute.
func TestComputeClient_ConvertAzureVMRecommendation_PagerErrorFallsBack(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")
	mockPager := &vmSKUCatalogueMockPager{
		pages: []armcompute.ResourceSKUsClientListResponse{{}},
		err:   errors.New("transient Azure API error"),
	}
	client.SetResourceSKUsPager(mockPager)

	rec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("eastus"),
		mocks.WithNormalizedSize("Standard_D2s_v3"),
	)
	out := client.convertAzureVMRecommendation(context.Background(), rec)
	require.NotNil(t, out, "conversion must NOT fail on catalogue-fetch error")
	details, ok := out.Details.(common.ComputeDetails)
	require.True(t, ok)
	assert.Equal(t, "Standard_D2s_v3", details.InstanceType)
	assert.Equal(t, 0, details.VCPU, "VCPU left at 0 when catalogue fetch fails")
	assert.Equal(t, 0.0, details.MemoryGB, "MemoryGB left at 0 when catalogue fetch fails")
}

// TestComputeClient_ConvertAzureVMRecommendation_NoMatchLeavesFieldsZero
// asserts that when the recommendation's SKU isn't in the catalogue
// (e.g. SKU listed for another region only), VCPU/MemoryGB stay at 0
// and the conversion still produces a usable recommendation.
func TestComputeClient_ConvertAzureVMRecommendation_NoMatchLeavesFieldsZero(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")
	mockPager := &vmSKUCatalogueMockPager{
		pages: []armcompute.ResourceSKUsClientListResponse{
			{
				ResourceSKUsResult: armcompute.ResourceSKUsResult{
					Value: []*armcompute.ResourceSKU{
						buildVMSKU("Standard_D2s_v3", "eastus", 2, "8"),
					},
				},
			},
		},
	}
	client.SetResourceSKUsPager(mockPager)

	rec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("eastus"),
		mocks.WithNormalizedSize("Standard_NC6s_v3"), // not in catalogue
	)
	out := client.convertAzureVMRecommendation(context.Background(), rec)
	require.NotNil(t, out)
	details, ok := out.Details.(common.ComputeDetails)
	require.True(t, ok)
	assert.Equal(t, "Standard_NC6s_v3", details.InstanceType)
	assert.Equal(t, 0, details.VCPU)
	assert.Equal(t, 0.0, details.MemoryGB)
}

// TestComputeClient_CachedSKULookup_FetchedOnce pins the perf
// invariant from PR #81 (now also enforced for compute, per #148):
// many converter calls in the same GetRecommendations run trigger
// exactly ONE catalogue fetch.
func TestComputeClient_CachedSKULookup_FetchedOnce(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")
	mockPager := &vmSKUCatalogueMockPager{
		pages: []armcompute.ResourceSKUsClientListResponse{
			{
				ResourceSKUsResult: armcompute.ResourceSKUsResult{
					Value: []*armcompute.ResourceSKU{
						buildVMSKU("Standard_D2s_v3", "eastus", 2, "8"),
					},
				},
			},
		},
	}
	client.SetResourceSKUsPager(mockPager)

	for i := 0; i < 10; i++ {
		_, _ = client.cachedSKULookup(context.Background(), "Standard_D2s_v3")
	}
	assert.Equal(t, 1, mockPager.pageHits, "catalogue must be fetched ONCE regardless of lookup count")
}

// TestComputeClient_FetchSKUCatalogue_CancelledContextFallsBack asserts
// that a cancelled context is terminal in the SKU catalogue pagination
// loop — the catalogue returns nil and Details.VCPU/MemoryGB stay at 0,
// but the conversion itself succeeds (graceful-degradation contract).
// Pins feedback_ctx_cancel_terminal.md for the compute SKU path.
func TestComputeClient_FetchSKUCatalogue_CancelledContextFallsBack(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so ctx.Err() is set on first loop iteration

	mockPager := &vmSKUCatalogueMockPager{
		pages: []armcompute.ResourceSKUsClientListResponse{
			{
				ResourceSKUsResult: armcompute.ResourceSKUsResult{
					Value: []*armcompute.ResourceSKU{
						buildVMSKU("Standard_D2s_v3", "eastus", 2, "8"),
					},
				},
			},
		},
	}
	client.SetResourceSKUsPager(mockPager)

	result := client.fetchSKUCatalogue(ctx)
	assert.Nil(t, result, "cancelled context must return nil catalogue")
	assert.Equal(t, 0, mockPager.pageHits, "NextPage must not be called after context is already cancelled")
}

// TestComputeClient_PurchaseCommitment_DisplayNameConformsToAzureAllowlist guards
// against regression: displayName in the calculatePrice body must match
// [A-Za-z0-9_-]{1,64} (Azure rejects DisplayNameInvalid otherwise).
func TestComputeClient_PurchaseCommitment_DisplayNameConformsToAzureAllowlist(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockCapacityProviderCheck(mockHTTP)

	const orderID = "azure-vm-displayname"
	var capturedDisplayName string
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		if r.Method != http.MethodPost || r.URL.Path != "/providers/Microsoft.Capacity/calculatePrice" {
			return false
		}
		if r.Body == nil {
			return true
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		var body map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &body); err == nil {
			if props, ok := body["properties"].(map[string]interface{}); ok {
				if dn, ok := props["displayName"].(string); ok {
					capturedDisplayName = dn
				}
			}
		}
		return true
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, calcPriceRespJSON(orderID)), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost &&
			r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/"+orderID+"/purchase"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, `{}`), nil).Once()

	rec := common.Recommendation{
		ResourceType:   "Standard_D2s_v3",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 2000.0,
	}
	_, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.NotEmpty(t, capturedDisplayName)
	assert.Regexp(t, `^[A-Za-z0-9_-]{1,64}$`, capturedDisplayName)
	// Rich-format guards: service code is correct and SKU is preserved
	// (see providers/azure/services/internal/reservations/displayname.go).
	assert.Regexp(t, `^vm-`, capturedDisplayName)
	assert.Contains(t, capturedDisplayName, "Standard_D2s_v3")
}

// TestCheckAndRegisterCapacityProvider_NonTwoxx is a regression test for M3:
// before this fix, checkAndRegisterCapacityProvider decoded the body
// unconditionally without checking resp.StatusCode, so a 403/429 with a
// non-JSON body would produce a misleading "decode provider state" error
// instead of a clear HTTP status error.
func TestCheckAndRegisterCapacityProvider_NonTwoxx(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	t.Cleanup(func() { mockHTTP.AssertExpectations(t) })
	cred := &MockTokenCredential{token: "tok"}
	client := NewClientWithHTTP(cred, "sub", "eastus", mockHTTP)

	// Return a 403 Forbidden instead of 200 Registered.
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path, "providers/Microsoft.Capacity") &&
			!strings.Contains(r.URL.Path, "reservationOrders") &&
			!strings.Contains(r.URL.Path, "calculatePrice")
	})).Return(mocks.CreateMockHTTPResponse(http.StatusForbidden, `{"error":"AuthorizationFailed"}`), nil).Once()

	err := client.checkAndRegisterCapacityProvider(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403", "error must surface the HTTP status code")
}

// TestExtractVMPricing_SingularOneYear verifies that extractVMPricing correctly
// recognises the "1 Year" singular form returned by the Azure Retail Prices API
// for 1-year reservation terms. Before the fix, the extractor used "%d Years"
// unconditionally, so the 1-year reservation line was silently skipped and
// reservationPrice remained 0, causing a false "no reservation pricing found"
// error even when the pricing row was present.
func TestExtractVMPricing_SingularOneYear(t *testing.T) {
	items := []AzureRetailPriceItem{
		{
			CurrencyCode:  "USD",
			RetailPrice:   0.096,
			UnitPrice:     0.096,
			ArmRegionName: "eastus",
			ArmSKUName:    "Standard_D2s_v3",
			Type:          "Consumption",
		},
		{
			CurrencyCode:    "USD",
			RetailPrice:     730.0,
			ArmRegionName:   "eastus",
			ArmSKUName:      "Standard_D2s_v3",
			ReservationTerm: "1 Year",
			Type:            "Reservation",
		},
	}

	onDemand, reservation, currency := extractVMPricing(items, 1)

	assert.Equal(t, "USD", currency)
	assert.Equal(t, 0.096, onDemand, "on-demand price must be extracted")
	assert.Equal(t, 730.0, reservation, "reservation price for '1 Year' term must be extracted")
}

// TestExtractVMPricing_PluralThreeYears verifies that the plural form "3 Years"
// continues to work for multi-year terms.
func TestExtractVMPricing_PluralThreeYears(t *testing.T) {
	items := []AzureRetailPriceItem{
		{CurrencyCode: "USD", RetailPrice: 0.096, UnitPrice: 0.096, Type: "Consumption"},
		{CurrencyCode: "USD", RetailPrice: 1900.0, ReservationTerm: "3 Years", Type: "Reservation"},
	}

	onDemand, reservation, currency := extractVMPricing(items, 3)

	assert.Equal(t, "USD", currency)
	assert.Equal(t, 0.096, onDemand)
	assert.Equal(t, 1900.0, reservation, "reservation price for '3 Years' term must be extracted")
}

// TestComputeClient_PurchaseCommitment_ZeroCountRejected is a regression test
// for M6: PurchaseCommitment must reject Count==0 before any HTTP call.
func TestComputeClient_PurchaseCommitment_ZeroCountRejected(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &mocks.MockHTTPClient{}
	t.Cleanup(func() { mockHTTP.AssertExpectations(t) })
	cred := &MockTokenCredential{token: "tok"}
	client := NewClientWithHTTP(cred, "sub", "eastus", mockHTTP)

	result, err := client.PurchaseCommitment(ctx, common.Recommendation{
		ResourceType: "Standard_D2s_v3", Term: "1yr", Count: 0,
	}, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "quantity must be greater than zero")
	mockHTTP.AssertNotCalled(t, "Do", mock.Anything)
}
