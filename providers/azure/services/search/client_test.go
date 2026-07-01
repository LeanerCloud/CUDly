package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/search/armsearch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/azure/mocks"
)

// MockRecommendationsPager mocks the RecommendationsPager interface
type MockRecommendationsPager struct {
	pages []armconsumption.ReservationRecommendationsClientListResponse
	index int
}

func (m *MockRecommendationsPager) More() bool {
	return m.index < len(m.pages)
}

func (m *MockRecommendationsPager) NextPage(ctx context.Context) (armconsumption.ReservationRecommendationsClientListResponse, error) {
	if m.index >= len(m.pages) {
		return armconsumption.ReservationRecommendationsClientListResponse{}, errors.New("no more pages")
	}
	page := m.pages[m.index]
	m.index++
	return page, nil
}

// MockReservationsDetailsPager mocks the ReservationsDetailsPager interface
type MockReservationsDetailsPager struct {
	pages []armconsumption.ReservationsDetailsClientListResponse
	index int
	err   error
}

func (m *MockReservationsDetailsPager) More() bool {
	return m.index < len(m.pages)
}

func (m *MockReservationsDetailsPager) NextPage(ctx context.Context) (armconsumption.ReservationsDetailsClientListResponse, error) {
	if m.err != nil {
		return armconsumption.ReservationsDetailsClientListResponse{}, m.err
	}
	if m.index >= len(m.pages) {
		return armconsumption.ReservationsDetailsClientListResponse{}, errors.New("no more pages")
	}
	page := m.pages[m.index]
	m.index++
	return page, nil
}

// MockSearchServicesPager mocks the SearchServicesPager interface
type MockSearchServicesPager struct {
	pages []armsearch.ServicesClientListBySubscriptionResponse
	index int
	err   error
}

func (m *MockSearchServicesPager) More() bool {
	return m.index < len(m.pages)
}

func (m *MockSearchServicesPager) NextPage(ctx context.Context) (armsearch.ServicesClientListBySubscriptionResponse, error) {
	if m.err != nil {
		return armsearch.ServicesClientListBySubscriptionResponse{}, m.err
	}
	if m.index >= len(m.pages) {
		return armsearch.ServicesClientListBySubscriptionResponse{}, errors.New("no more pages")
	}
	page := m.pages[m.index]
	m.index++
	return page, nil
}

// MockHTTPClient mocks HTTP client for testing
type MockHTTPClient struct {
	mock.Mock
}

func (m *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*http.Response), args.Error(1)
}

func createMockHTTPResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}

func createSampleSearchPricingResponse() string {
	return `{
		"Items": [
			{
				"currencyCode": "USD",
				"retailPrice": 500.0,
				"unitPrice": 500.0,
				"armRegionName": "eastus",
				"productName": "Azure Cognitive Search",
				"serviceName": "Azure Cognitive Search",
				"armSkuName": "Standard_S1",
				"meterName": "S1 Search Unit",
				"reservationTerm": "1 Year",
				"type": "Reservation"
			},
			{
				"currencyCode": "USD",
				"retailPrice": 1200.0,
				"unitPrice": 1200.0,
				"armRegionName": "eastus",
				"productName": "Azure Cognitive Search",
				"serviceName": "Azure Cognitive Search",
				"armSkuName": "Standard_S1",
				"meterName": "S1 Search Unit",
				"reservationTerm": "3 Years",
				"type": "Reservation"
			},
			{
				"currencyCode": "USD",
				"retailPrice": 0.20,
				"unitPrice": 0.20,
				"armRegionName": "eastus",
				"productName": "Azure Cognitive Search",
				"serviceName": "Azure Cognitive Search",
				"armSkuName": "Standard_S1",
				"type": "Consumption"
			}
		]
	}`
}

func TestNewClient(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")

	require.NotNil(t, client)
	assert.Equal(t, "test-subscription", client.subscriptionID)
	assert.Equal(t, "eastus", client.region)
	assert.NotNil(t, client.httpClient)
}

func TestNewClientWithHTTP(t *testing.T) {
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	require.NotNil(t, client)
	assert.Equal(t, "test-subscription", client.subscriptionID)
	assert.Equal(t, "eastus", client.region)
	assert.Equal(t, mockHTTP, client.httpClient)
}

func TestSearchClient_GetServiceType(t *testing.T) {
	client := NewClient(nil, "sub", "region")
	assert.Equal(t, common.ServiceSearch, client.GetServiceType())
}

func TestSearchClient_GetRegion(t *testing.T) {
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
			name:     "Asia Pacific",
			region:   "southeastasia",
			expected: "southeastasia",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(nil, "sub", tt.region)
			assert.Equal(t, tt.expected, client.GetRegion())
		})
	}
}

func TestSearchClient_Fields(t *testing.T) {
	client := NewClient(nil, "test-sub", "northeurope")

	assert.Equal(t, "test-sub", client.subscriptionID)
	assert.Equal(t, "northeurope", client.region)
	assert.Nil(t, client.cred)
}

func TestSearchClient_GetOfferingDetails_WithMock(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, createSampleSearchPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_S1",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, "Standard_S1", details.ResourceType)
	assert.Equal(t, "1yr", details.Term)
	assert.Equal(t, "USD", details.Currency)
}

func TestSearchClient_GetOfferingDetails_3YearTerm(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, createSampleSearchPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_S1",
		Term:          "3yr",
		PaymentOption: "monthly",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, "3yr", details.Term)
	assert.Equal(t, "monthly", details.PaymentOption)
}

func TestSearchClient_GetOfferingDetails_NoUpfront(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, createSampleSearchPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_S1",
		Term:          "1yr",
		PaymentOption: "no-upfront",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, float64(0), details.UpfrontCost)
	assert.Greater(t, details.RecurringCost, float64(0))
}

func TestSearchClient_GetOfferingDetails_APIError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusInternalServerError, "Internal Server Error"),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_S1",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pricing API returned status 500")
}

func TestSearchClient_GetOfferingDetails_NoPricing(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, `{"Items": []}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_S1",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no pricing data found")
}

// TestSearchClient_GetOfferingDetails_NoReservationPricing verifies that when
// on-demand pricing is present but no reservation line is returned, the client
// returns an error rather than fabricating a price from a hardcoded multiplier
// (issue #1020 H4). Pre-fix this would have silently surfaced a fabricated
// TotalCost/SavingsPercentage as a real quote.
func TestSearchClient_GetOfferingDetails_NoReservationPricing(t *testing.T) {
	ctx := context.Background()

	onDemandOnly := `{
		"Items": [
			{
				"currencyCode": "USD",
				"retailPrice": 0.20,
				"unitPrice": 0.20,
				"armRegionName": "eastus",
				"armSkuName": "Standard_S1",
				"type": "Consumption"
			}
		]
	}`

	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)
	mockHTTP.On("Do", mock.Anything).Return(createMockHTTPResponse(http.StatusOK, onDemandOnly), nil)

	rec := common.Recommendation{
		ResourceType:  "Standard_S1",
		Term:          "1yr",
		PaymentOption: "upfront",
	}
	_, err := client.GetOfferingDetails(ctx, rec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no reservation pricing found")
}

func TestSearchClient_GetExistingCommitments_Empty(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Mock pager returns no pages — the empty-subscription case.
	client.SetReservationsPager(&MockReservationsDetailsPager{})

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestAzureRetailPriceStructure(t *testing.T) {
	price := AzureRetailPrice{
		Count: 2,
		Items: []struct {
			CurrencyCode    string  `json:"currencyCode"`
			RetailPrice     float64 `json:"retailPrice"`
			UnitPrice       float64 `json:"unitPrice"`
			ArmRegionName   string  `json:"armRegionName"`
			ProductName     string  `json:"productName"`
			ServiceName     string  `json:"serviceName"`
			ArmSKUName      string  `json:"armSkuName"`
			MeterName       string  `json:"meterName"`
			ReservationTerm string  `json:"reservationTerm"`
			Type            string  `json:"type"`
		}{
			{
				CurrencyCode:    "USD",
				RetailPrice:     500.0,
				UnitPrice:       500.0,
				ArmRegionName:   "eastus",
				ProductName:     "Azure Cognitive Search",
				ServiceName:     "Azure Cognitive Search",
				ArmSKUName:      "Standard_S1",
				MeterName:       "S1 Search Unit",
				ReservationTerm: "1 Year",
				Type:            "Reservation",
			},
		},
		NextPageLink: "",
	}

	assert.Equal(t, 2, price.Count)
	require.Len(t, price.Items, 1)
	assert.Equal(t, "USD", price.Items[0].CurrencyCode)
	assert.Equal(t, "Standard_S1", price.Items[0].ArmSKUName)
}

func TestSearchPricingStructure(t *testing.T) {
	pricing := SearchPricing{
		HourlyRate:        0.15,
		ReservationPrice:  1314.0,
		OnDemandPrice:     2628.0,
		Currency:          "USD",
		SavingsPercentage: 50.0,
	}

	assert.Equal(t, 0.15, pricing.HourlyRate)
	assert.Equal(t, 1314.0, pricing.ReservationPrice)
	assert.Equal(t, 2628.0, pricing.OnDemandPrice)
	assert.Equal(t, "USD", pricing.Currency)
	assert.Equal(t, 50.0, pricing.SavingsPercentage)
}

func TestSearchClient_GetRecommendations_WithMockPager(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	mockPager := &MockRecommendationsPager{
		pages: []armconsumption.ReservationRecommendationsClientListResponse{
			{
				ReservationRecommendationsListResult: armconsumption.ReservationRecommendationsListResult{
					Value: []armconsumption.ReservationRecommendationClassification{},
				},
			},
		},
	}

	client.SetRecommendationsPager(mockPager)

	recs, err := client.GetRecommendations(ctx, &common.RecommendationParams{})
	require.NoError(t, err)
	assert.Empty(t, recs)
}

func TestSearchClient_GetExistingCommitments_WithMockPager(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	reservationID := "test-reservation-123"
	skuName := "search-standard-s1"

	mockPager := &MockReservationsDetailsPager{
		pages: []armconsumption.ReservationsDetailsClientListResponse{
			{
				ReservationDetailsListResult: armconsumption.ReservationDetailsListResult{
					Value: []*armconsumption.ReservationDetail{
						{
							Properties: &armconsumption.ReservationDetailProperties{
								ReservationID: &reservationID,
								SKUName:       &skuName,
							},
						},
					},
				},
			},
		},
	}

	client.SetReservationsPager(mockPager)

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	require.Len(t, commitments, 1)
	assert.Equal(t, reservationID, commitments[0].CommitmentID)
	assert.Equal(t, skuName, commitments[0].ResourceType)
}

func TestSearchClient_GetExistingCommitments_FilterNonSearch(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	nonSearchSKU := "sql-standard-s1"
	searchSKU := "search-standard-s2"
	reservationID1 := "test-reservation-1"
	reservationID2 := "test-reservation-2"

	mockPager := &MockReservationsDetailsPager{
		pages: []armconsumption.ReservationsDetailsClientListResponse{
			{
				ReservationDetailsListResult: armconsumption.ReservationDetailsListResult{
					Value: []*armconsumption.ReservationDetail{
						{
							Properties: &armconsumption.ReservationDetailProperties{
								ReservationID: &reservationID1,
								SKUName:       &nonSearchSKU,
							},
						},
						{
							Properties: &armconsumption.ReservationDetailProperties{
								ReservationID: &reservationID2,
								SKUName:       &searchSKU,
							},
						},
					},
				},
			},
		},
	}

	client.SetReservationsPager(mockPager)

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	require.Len(t, commitments, 1)
	assert.Equal(t, reservationID2, commitments[0].CommitmentID)
}

func TestSearchClient_GetExistingCommitments_PagerError(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Pagination errors must propagate — see the compute client for the
	// full rationale (partial lists are unsafe for the purchase flow).
	mockPager := &MockReservationsDetailsPager{
		pages: []armconsumption.ReservationsDetailsClientListResponse{{}},
		err:   errors.New("API error"),
	}

	client.SetReservationsPager(mockPager)

	commitments, err := client.GetExistingCommitments(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "search: list reservations")
	assert.Nil(t, commitments)
}

func TestSearchClient_GetValidResourceTypes_WithMockPager(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	skuName := armsearch.SKUNameStandard

	mockPager := &MockSearchServicesPager{
		pages: []armsearch.ServicesClientListBySubscriptionResponse{
			{
				ServiceListResult: armsearch.ServiceListResult{
					Value: []*armsearch.Service{
						{
							SKU: &armsearch.SKU{
								Name: &skuName,
							},
						},
					},
				},
			},
		},
	}

	client.SetSearchServicesPager(mockPager)

	skus, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	require.Len(t, skus, 1)
	assert.Equal(t, string(skuName), skus[0])
}

func TestSearchClient_GetValidResourceTypes_PagerError(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	mockPager := &MockSearchServicesPager{
		pages: []armsearch.ServicesClientListBySubscriptionResponse{{}},
		err:   errors.New("API error"),
	}

	client.SetSearchServicesPager(mockPager)

	skus, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	// Should fall back to common SKUs
	assert.Contains(t, skus, "standard")
	assert.Contains(t, skus, "basic")
}

func TestSearchClient_GetValidResourceTypes_EmptyResults(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	mockPager := &MockSearchServicesPager{
		pages: []armsearch.ServicesClientListBySubscriptionResponse{
			{
				ServiceListResult: armsearch.ServiceListResult{
					Value: []*armsearch.Service{},
				},
			},
		},
	}

	client.SetSearchServicesPager(mockPager)

	skus, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	// Should fall back to common SKUs
	assert.Contains(t, skus, "standard")
}

func TestSearchClient_SetterMethods(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")

	mockRecPager := &MockRecommendationsPager{}
	client.SetRecommendationsPager(mockRecPager)
	assert.Equal(t, mockRecPager, client.recommendationsPager)

	mockResPager := &MockReservationsDetailsPager{}
	client.SetReservationsPager(mockResPager)
	assert.Equal(t, mockResPager, client.reservationsPager)

	mockSearchPager := &MockSearchServicesPager{}
	client.SetSearchServicesPager(mockSearchPager)
	assert.Equal(t, mockSearchPager, client.searchServicesPager)
}

func TestSearchClient_GetCommonSKUs(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")
	skus := client.getCommonSKUs()

	assert.Contains(t, skus, "basic")
	assert.Contains(t, skus, "standard")
	assert.Contains(t, skus, "standard2")
	assert.Contains(t, skus, "standard3")
	assert.Contains(t, skus, "storage_optimized_l1")
	assert.Contains(t, skus, "storage_optimized_l2")
}

// TestSearchClient_ConvertAzureSearchRecommendation_NilGuards pins the contract:
// unusable SDK payloads (nil or nil Properties) produce a nil *Recommendation.
func TestSearchClient_ConvertAzureSearchRecommendation_NilGuards(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")
	assert.Nil(t, client.convertAzureSearchRecommendation(context.Background(), nil))
}

// TestSearchClient_ConvertAzureSearchRecommendation_PopulatesAllFields asserts
// the converter forwards every helper-extracted field plus the Search-service
// constants (Provider, Service, CommitmentType, PaymentOption).
func TestSearchClient_ConvertAzureSearchRecommendation_PopulatesAllFields(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")

	azRec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("eastus"),
		mocks.WithTerm("P1Y"),
		mocks.WithQuantity(2),
		mocks.WithNormalizedSize("standard2"),
		mocks.WithCosts(120, 80, 40),
	)
	rec := client.convertAzureSearchRecommendation(context.Background(), azRec)
	require.NotNil(t, rec)
	assert.Equal(t, common.ProviderAzure, rec.Provider)
	assert.Equal(t, common.ServiceSearch, rec.Service)
	assert.Equal(t, "test-subscription", rec.Account)
	assert.Equal(t, "eastus", rec.Region)
	assert.Equal(t, "standard2", rec.ResourceType)
	assert.Equal(t, 2, rec.Count)
	assert.InDelta(t, 120.0, rec.OnDemandCost, 1e-9)
	assert.InDelta(t, 80.0, rec.CommitmentCost, 1e-9)
	assert.InDelta(t, 40.0, rec.EstimatedSavings, 1e-9)
	assert.Equal(t, common.CommitmentReservedInstance, rec.CommitmentType)
	assert.Equal(t, "1yr", rec.Term)
	assert.Equal(t, "upfront", rec.PaymentOption)
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

// calcPriceRespJSON returns a minimal calculatePrice response JSON for tests.
func calcPriceRespJSON(orderID string) string {
	return `{"properties":{"reservationOrderId":"` + orderID + `"}}`
}

func TestSearchClient_PurchaseCommitment_Success(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(createMockHTTPResponse(http.StatusOK, calcPriceRespJSON("search-order-001")), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/search-order-001/purchase"
	})).Return(createMockHTTPResponse(http.StatusOK, `{}`), nil).Once()

	rec := common.Recommendation{
		ResourceType:   "standard",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 3000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "search-order-001", result.CommitmentID)
	assert.Equal(t, 3000.0, result.Cost)
	mockHTTP.AssertExpectations(t)
}

func TestSearchClient_PurchaseCommitment_3YearTerm(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(createMockHTTPResponse(http.StatusOK, calcPriceRespJSON("search-order-3yr")), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/search-order-3yr/purchase"
	})).Return(createMockHTTPResponse(http.StatusCreated, `{}`), nil).Once()

	rec := common.Recommendation{
		ResourceType:   "standard",
		Term:           "3yr",
		Count:          1,
		CommitmentCost: 7500.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "search-order-3yr", result.CommitmentID)
	mockHTTP.AssertExpectations(t)
}

func TestSearchClient_PurchaseCommitment_Accepted(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(createMockHTTPResponse(http.StatusOK, calcPriceRespJSON("search-order-202")), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/search-order-202/purchase"
	})).Return(createMockHTTPResponse(http.StatusAccepted, `{}`), nil).Once()

	rec := common.Recommendation{
		ResourceType:   "standard",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 3000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	mockHTTP.AssertExpectations(t)
}

func TestSearchClient_PurchaseCommitment_TokenError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{err: errors.New("token error")}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	rec := common.Recommendation{
		ResourceType: "standard",
		Term:         "1yr",
		Count:        1,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to get access token")
}

func TestSearchClient_PurchaseCommitment_HTTPError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(nil, errors.New("network error")).Once()

	rec := common.Recommendation{
		ResourceType: "standard",
		Term:         "1yr",
		Count:        1,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "calculatePrice HTTP call")
}

func TestSearchClient_PurchaseCommitment_BadStatus(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(createMockHTTPResponse(http.StatusOK, calcPriceRespJSON("search-order-bad")), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/search-order-bad/purchase"
	})).Return(createMockHTTPResponse(http.StatusBadRequest, `{"error": "invalid request"}`), nil).Once()

	rec := common.Recommendation{
		ResourceType: "standard",
		Term:         "1yr",
		Count:        1,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "reservation purchase failed with status 400")
	mockHTTP.AssertExpectations(t)
}

func TestSearchClient_ValidateOffering_Valid(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	skuName := armsearch.SKUNameStandard
	mockPager := &MockSearchServicesPager{
		pages: []armsearch.ServicesClientListBySubscriptionResponse{
			{
				ServiceListResult: armsearch.ServiceListResult{
					Value: []*armsearch.Service{
						{
							SKU: &armsearch.SKU{Name: &skuName},
						},
					},
				},
			},
		},
	}

	client.SetSearchServicesPager(mockPager)

	rec := common.Recommendation{
		ResourceType: "standard",
	}

	err := client.ValidateOffering(ctx, rec)
	assert.NoError(t, err)
}

func TestSearchClient_ValidateOffering_Invalid(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	skuName := armsearch.SKUNameStandard
	mockPager := &MockSearchServicesPager{
		pages: []armsearch.ServicesClientListBySubscriptionResponse{
			{
				ServiceListResult: armsearch.ServiceListResult{
					Value: []*armsearch.Service{
						{
							SKU: &armsearch.SKU{Name: &skuName},
						},
					},
				},
			},
		},
	}

	client.SetSearchServicesPager(mockPager)

	rec := common.Recommendation{
		ResourceType: "InvalidSKU",
	}

	err := client.ValidateOffering(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Azure Search SKU")
}

// TestSearchClient_PurchaseCommitment_TwoStepFlow verifies the two-step
// calculatePrice->purchase flow for the search client (issue #677 regression test).
func TestSearchClient_PurchaseCommitment_TwoStepFlow(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	const azureMintedOrderID = "azure-search-order-677"

	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(createMockHTTPResponse(http.StatusOK, calcPriceRespJSON(azureMintedOrderID)), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost &&
			r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/"+azureMintedOrderID+"/purchase"
	})).Return(createMockHTTPResponse(http.StatusOK, `{}`), nil).Once()

	rec := common.Recommendation{
		ResourceType:   "standard",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 3000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, azureMintedOrderID, result.CommitmentID,
		"CommitmentID must be the Azure-minted order ID from calculatePrice")
	mockHTTP.AssertExpectations(t)
	mockHTTP.AssertNumberOfCalls(t, "Do", 2)
}

// TestSearchClient_PurchaseCommitment_TagInjection verifies that the
// purchase-automation tag is present in the calculatePrice request body when
// opts.Source is set. The empty-Source path is covered separately by
// TestSearchClient_PurchaseCommitment_RequiresSource because the dedupe-guard
// now fails fast at function entry.
func TestSearchClient_PurchaseCommitment_TagInjection(t *testing.T) {
	const orderID = "azure-search-tag-test"
	const source = "cudly-web"

	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	var capturedBody []byte
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		if r.Method != http.MethodPost || r.URL.Path != "/providers/Microsoft.Capacity/calculatePrice" {
			return false
		}
		capturedBody, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(capturedBody))
		return true
	})).Return(createMockHTTPResponse(http.StatusOK, calcPriceRespJSON(orderID)), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost &&
			r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/"+orderID+"/purchase"
	})).Return(createMockHTTPResponse(http.StatusOK, `{}`), nil).Once()

	rec := common.Recommendation{ResourceType: "standard", Term: "1yr", Count: 1, CommitmentCost: 3000.0}
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

// TestSearchClient_PurchaseCommitment_RequiresSource pins the dedupe guard:
// PurchaseCommitment must reject an empty opts.Source before issuing any HTTP
// call. Azure mints the reservation order ID server-side, so the
// purchase-automation tag derived from Source is the only stable dedupe
// signal CUDly controls -- proceeding without it would allow a re-driven
// purchase to create a duplicate reservation.
func TestSearchClient_PurchaseCommitment_RequiresSource(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	rec := common.Recommendation{ResourceType: "standard", Term: "1yr", Count: 1}
	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "purchase source is required")
	mockHTTP.AssertNotCalled(t, "Do", mock.Anything)
}

func TestSearchClient_ValidateOffering_CaseInsensitive(t *testing.T) {
	ctx := context.Background()

	skuName := armsearch.SKUNameStandard
	mockPager := func() *MockSearchServicesPager {
		return &MockSearchServicesPager{
			pages: []armsearch.ServicesClientListBySubscriptionResponse{
				{
					ServiceListResult: armsearch.ServiceListResult{
						Value: []*armsearch.Service{
							{SKU: &armsearch.SKU{Name: &skuName}},
						},
					},
				},
			},
		}
	}

	t.Run("case_insensitive", func(t *testing.T) {
		client := NewClient(nil, "test-subscription", "eastus")
		client.SetSearchServicesPager(mockPager())
		rec := common.Recommendation{ResourceType: "STANDARD"}
		err := client.ValidateOffering(ctx, rec)
		assert.NoError(t, err)
	})

	t.Run("whitespace_trimmed", func(t *testing.T) {
		client := NewClient(nil, "test-subscription", "eastus")
		client.SetSearchServicesPager(mockPager())
		rec := common.Recommendation{ResourceType: "  standard  "}
		err := client.ValidateOffering(ctx, rec)
		assert.NoError(t, err)
	})
}

// TestSearchClient_PurchaseCommitment_DisplayNameConformsToAzureAllowlist guards
// against regression: displayName in the calculatePrice body must match
// [A-Za-z0-9_-]{1,64} (Azure rejects DisplayNameInvalid otherwise).
func TestSearchClient_PurchaseCommitment_DisplayNameConformsToAzureAllowlist(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	const orderID = "azure-search-displayname"
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
	})).Return(createMockHTTPResponse(http.StatusOK, calcPriceRespJSON(orderID)), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost &&
			r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/"+orderID+"/purchase"
	})).Return(createMockHTTPResponse(http.StatusOK, `{}`), nil).Once()

	rec := common.Recommendation{
		ResourceType:   "standard2",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 800.0,
	}
	_, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.NotEmpty(t, capturedDisplayName)
	assert.Regexp(t, `^[A-Za-z0-9_-]{1,64}$`, capturedDisplayName)
	// Rich-format guards: service code is correct and SKU is preserved
	// (see providers/azure/services/internal/reservations/displayname.go).
	assert.Regexp(t, `^search-`, capturedDisplayName)
	assert.Contains(t, capturedDisplayName, "standard2")
}
