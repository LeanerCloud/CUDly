package search

import (
	"bytes"
	"context"
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
	pages []armconsumption.ReservationsDetailsClientListByReservationOrderResponse
	index int
	err   error
}

func (m *MockReservationsDetailsPager) More() bool {
	return m.index < len(m.pages)
}

func (m *MockReservationsDetailsPager) NextPage(ctx context.Context) (armconsumption.ReservationsDetailsClientListByReservationOrderResponse, error) {
	if m.err != nil {
		return armconsumption.ReservationsDetailsClientListByReservationOrderResponse{}, m.err
	}
	if m.index >= len(m.pages) {
		return armconsumption.ReservationsDetailsClientListByReservationOrderResponse{}, errors.New("no more pages")
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
				"reservationTerm": "1 Years",
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
	assert.Equal(t, common.ServiceOther, client.GetServiceType())
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

func TestSearchClient_GetExistingCommitments_Empty(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Will return empty without credentials
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

	recs, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	require.NoError(t, err)
	assert.Empty(t, recs)
}

func TestSearchClient_GetExistingCommitments_WithMockPager(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	reservationID := "test-reservation-123"
	skuName := "search-standard-s1"

	mockPager := &MockReservationsDetailsPager{
		pages: []armconsumption.ReservationsDetailsClientListByReservationOrderResponse{
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
		pages: []armconsumption.ReservationsDetailsClientListByReservationOrderResponse{
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

	mockPager := &MockReservationsDetailsPager{
		pages: []armconsumption.ReservationsDetailsClientListByReservationOrderResponse{{}},
		err:   errors.New("API error"),
	}

	client.SetReservationsPager(mockPager)

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments)
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

func TestSearchClient_ConvertAzureSearchRecommendation(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	rec := client.convertAzureSearchRecommendation(ctx, nil)
	require.NotNil(t, rec)
	assert.Equal(t, common.ProviderAzure, rec.Provider)
	assert.Equal(t, common.ServiceOther, rec.Service)
	assert.Equal(t, "test-subscription", rec.Account)
	assert.Equal(t, "eastus", rec.Region)
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

func TestSearchClient_PurchaseCommitment_Success(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "standard",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 3000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.NotEmpty(t, result.CommitmentID)
	assert.Equal(t, 3000.0, result.Cost)
}

func TestSearchClient_PurchaseCommitment_3YearTerm(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusCreated, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "standard",
		Term:           "3yr",
		Count:          1,
		CommitmentCost: 7500.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestSearchClient_PurchaseCommitment_Accepted(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusAccepted, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "standard",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 3000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestSearchClient_PurchaseCommitment_TokenError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{err: errors.New("token error")}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	rec := common.Recommendation{
		ResourceType: "standard",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to get access token")
}

func TestSearchClient_PurchaseCommitment_HTTPError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(nil, errors.New("network error"))

	rec := common.Recommendation{
		ResourceType: "standard",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to purchase reservation")
}

func TestSearchClient_PurchaseCommitment_BadStatus(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusBadRequest, `{"error": "invalid request"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType: "standard",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "reservation purchase failed with status 400")
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
