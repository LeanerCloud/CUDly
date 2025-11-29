package cache

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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// MockRecommendationsPager mocks the RecommendationsPager interface
type MockRecommendationsPager struct {
	mock.Mock
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

// MockRedisCachesPager mocks the RedisCachesPager interface
type MockRedisCachesPager struct {
	pages []armredis.ClientListBySubscriptionResponse
	index int
	err   error
}

func (m *MockRedisCachesPager) More() bool {
	return m.index < len(m.pages)
}

func (m *MockRedisCachesPager) NextPage(ctx context.Context) (armredis.ClientListBySubscriptionResponse, error) {
	if m.err != nil {
		return armredis.ClientListBySubscriptionResponse{}, m.err
	}
	if m.index >= len(m.pages) {
		return armredis.ClientListBySubscriptionResponse{}, errors.New("no more pages")
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

func createSampleRedisPricingResponse() string {
	return `{
		"Items": [
			{
				"currencyCode": "USD",
				"retailPrice": 350.0,
				"unitPrice": 350.0,
				"armRegionName": "eastus",
				"productName": "Azure Cache for Redis",
				"serviceName": "Azure Cache for Redis",
				"armSkuName": "Premium_P1",
				"meterName": "P1 Instance",
				"reservationTerm": "1 Years",
				"type": "Reservation"
			},
			{
				"currencyCode": "USD",
				"retailPrice": 0.125,
				"unitPrice": 0.125,
				"armRegionName": "eastus",
				"productName": "Azure Cache for Redis",
				"serviceName": "Azure Cache for Redis",
				"armSkuName": "Premium_P1",
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

func TestCacheClient_GetServiceType(t *testing.T) {
	client := NewClient(nil, "sub", "region")
	assert.Equal(t, common.ServiceCache, client.GetServiceType())
}

func TestCacheClient_GetRegion(t *testing.T) {
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
			name:     "Australia East",
			region:   "australiaeast",
			expected: "australiaeast",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(nil, "sub", tt.region)
			assert.Equal(t, tt.expected, client.GetRegion())
		})
	}
}

func TestCacheClient_GetValidResourceTypes_Fallback(t *testing.T) {
	// When API calls fail, GetValidResourceTypes should return common SKUs
	client := NewClient(nil, "invalid-subscription", "eastus")

	skus, err := client.GetValidResourceTypes(nil)
	require.NoError(t, err)
	require.NotEmpty(t, skus)

	// Should contain standard Redis Cache SKUs
	assert.Contains(t, skus, "Basic_C0")
	assert.Contains(t, skus, "Standard_C1")
	assert.Contains(t, skus, "Premium_P1")
}

func TestCacheClient_ValidateOffering_InvalidSKU(t *testing.T) {
	client := NewClient(nil, "sub", "eastus")
	rec := common.Recommendation{
		ResourceType: "InvalidSKU_X99",
	}

	err := client.ValidateOffering(nil, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Azure Redis Cache SKU")
}

func TestAzureRetailPriceStructure(t *testing.T) {
	// Test that the struct can be properly constructed
	price := AzureRetailPrice{
		Count: 5,
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
				RetailPrice:     100.0,
				UnitPrice:       95.0,
				ArmRegionName:   "eastus",
				ProductName:     "Azure Cache for Redis",
				ServiceName:     "Azure Cache for Redis",
				ArmSKUName:      "Premium_P1",
				MeterName:       "P1 Instance",
				ReservationTerm: "1 Year",
				Type:            "Reservation",
			},
		},
		NextPageLink: "https://example.com/next",
	}

	assert.Equal(t, 5, price.Count)
	require.Len(t, price.Items, 1)
	assert.Equal(t, "USD", price.Items[0].CurrencyCode)
	assert.Equal(t, 100.0, price.Items[0].RetailPrice)
	assert.Equal(t, "Premium_P1", price.Items[0].ArmSKUName)
}

func TestRedisPricingStructure(t *testing.T) {
	pricing := RedisPricing{
		HourlyRate:        0.50,
		ReservationPrice:  4380.0, // 1 year
		OnDemandPrice:     8760.0, // 1 year at $1/hour
		Currency:          "USD",
		SavingsPercentage: 50.0,
	}

	assert.Equal(t, 0.50, pricing.HourlyRate)
	assert.Equal(t, 4380.0, pricing.ReservationPrice)
	assert.Equal(t, 8760.0, pricing.OnDemandPrice)
	assert.Equal(t, "USD", pricing.Currency)
	assert.Equal(t, 50.0, pricing.SavingsPercentage)
}

func TestNewClientWithHTTP(t *testing.T) {
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	require.NotNil(t, client)
	assert.Equal(t, "test-subscription", client.subscriptionID)
	assert.Equal(t, "eastus", client.region)
	assert.Equal(t, mockHTTP, client.httpClient)
}

func TestCacheClient_GetOfferingDetails_WithMock(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, createSampleRedisPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Premium_P1",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, "Premium_P1", details.ResourceType)
	assert.Equal(t, "1yr", details.Term)
	assert.Equal(t, "USD", details.Currency)
}

func TestCacheClient_GetOfferingDetails_3YearTerm(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, createSampleRedisPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Premium_P1",
		Term:          "3yr",
		PaymentOption: "monthly",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, "3yr", details.Term)
	assert.Equal(t, "monthly", details.PaymentOption)
}

func TestCacheClient_GetOfferingDetails_NoUpfront(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, createSampleRedisPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Premium_P1",
		Term:          "1yr",
		PaymentOption: "no-upfront",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, float64(0), details.UpfrontCost)
	assert.Greater(t, details.RecurringCost, float64(0))
}

func TestCacheClient_GetOfferingDetails_APIError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusInternalServerError, "Internal Server Error"),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Premium_P1",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pricing API returned status 500")
}

func TestCacheClient_GetOfferingDetails_NoPricing(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, `{"Items": []}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Premium_P1",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no pricing data found")
}

func TestCacheClient_GetExistingCommitments_Empty(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Will return empty without credentials
	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestCacheClient_ValidateOffering_ValidSKU(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	rec := common.Recommendation{
		ResourceType: "Premium_P1",
	}

	// Should pass validation against common SKUs fallback
	err := client.ValidateOffering(ctx, rec)
	assert.NoError(t, err)
}

func TestCacheClient_GetRecommendations_WithMockPager(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager with empty results
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

func TestCacheClient_GetRecommendations_MultiplePages(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager with multiple pages
	mockPager := &MockRecommendationsPager{
		pages: []armconsumption.ReservationRecommendationsClientListResponse{
			{
				ReservationRecommendationsListResult: armconsumption.ReservationRecommendationsListResult{
					Value: []armconsumption.ReservationRecommendationClassification{},
				},
			},
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

func TestCacheClient_GetExistingCommitments_WithMockPager(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	reservationID := "test-reservation-123"
	skuName := "redis-premium-p1"

	// Create mock pager with Redis commitment
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
	assert.Equal(t, common.ServiceCache, commitments[0].Service)
}

func TestCacheClient_GetExistingCommitments_FilterNonRedis(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Test that non-Redis SKUs are filtered out
	nonRedisSKU := "sql-standard-s1"
	redisSKU := "redis-premium-p2"
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
								SKUName:       &nonRedisSKU,
							},
						},
						{
							Properties: &armconsumption.ReservationDetailProperties{
								ReservationID: &reservationID2,
								SKUName:       &redisSKU,
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

func TestCacheClient_GetExistingCommitments_NilProperties(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Test that nil properties are handled gracefully
	mockPager := &MockReservationsDetailsPager{
		pages: []armconsumption.ReservationsDetailsClientListByReservationOrderResponse{
			{
				ReservationDetailsListResult: armconsumption.ReservationDetailsListResult{
					Value: []*armconsumption.ReservationDetail{
						{
							Properties: nil,
						},
					},
				},
			},
		},
	}

	client.SetReservationsPager(mockPager)

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestCacheClient_GetExistingCommitments_PagerError(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Test that pager errors are handled gracefully
	mockPager := &MockReservationsDetailsPager{
		pages: []armconsumption.ReservationsDetailsClientListByReservationOrderResponse{
			{},
		},
		err: errors.New("API error"),
	}

	client.SetReservationsPager(mockPager)

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestCacheClient_GetValidResourceTypes_WithMockPager(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	skuName := armredis.SKUNamePremium
	skuFamily := armredis.SKUFamilyP
	capacity := int32(1)

	// Create mock pager with Redis caches
	mockPager := &MockRedisCachesPager{
		pages: []armredis.ClientListBySubscriptionResponse{
			{
				ListResult: armredis.ListResult{
					Value: []*armredis.ResourceInfo{
						{
							Properties: &armredis.Properties{
								SKU: &armredis.SKU{
									Name:     &skuName,
									Family:   &skuFamily,
									Capacity: &capacity,
								},
							},
						},
					},
				},
			},
		},
	}

	client.SetRedisCachesPager(mockPager)

	skus, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	require.Len(t, skus, 1)
	assert.Equal(t, "Premium_P1", skus[0])
}

func TestCacheClient_GetValidResourceTypes_MultipleCaches(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	premiumName := armredis.SKUNamePremium
	standardName := armredis.SKUNameStandard
	familyP := armredis.SKUFamilyP
	familyC := armredis.SKUFamilyC
	capacity1 := int32(1)
	capacity2 := int32(2)

	mockPager := &MockRedisCachesPager{
		pages: []armredis.ClientListBySubscriptionResponse{
			{
				ListResult: armredis.ListResult{
					Value: []*armredis.ResourceInfo{
						{
							Properties: &armredis.Properties{
								SKU: &armredis.SKU{
									Name:     &premiumName,
									Family:   &familyP,
									Capacity: &capacity1,
								},
							},
						},
						{
							Properties: &armredis.Properties{
								SKU: &armredis.SKU{
									Name:     &standardName,
									Family:   &familyC,
									Capacity: &capacity2,
								},
							},
						},
					},
				},
			},
		},
	}

	client.SetRedisCachesPager(mockPager)

	skus, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	require.Len(t, skus, 2)
	assert.Contains(t, skus, "Premium_P1")
	assert.Contains(t, skus, "Standard_C2")
}

func TestCacheClient_GetValidResourceTypes_PagerError(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Test that pager errors result in fallback to common SKUs
	mockPager := &MockRedisCachesPager{
		pages: []armredis.ClientListBySubscriptionResponse{{}},
		err:   errors.New("API error"),
	}

	client.SetRedisCachesPager(mockPager)

	skus, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	// Should fall back to common SKUs
	assert.Contains(t, skus, "Premium_P1")
	assert.Contains(t, skus, "Standard_C1")
	assert.Contains(t, skus, "Basic_C0")
}

func TestCacheClient_GetValidResourceTypes_EmptyResults(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Test that empty results fall back to common SKUs
	mockPager := &MockRedisCachesPager{
		pages: []armredis.ClientListBySubscriptionResponse{
			{
				ListResult: armredis.ListResult{
					Value: []*armredis.ResourceInfo{},
				},
			},
		},
	}

	client.SetRedisCachesPager(mockPager)

	skus, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	// Should fall back to common SKUs
	assert.Contains(t, skus, "Premium_P1")
}

func TestCacheClient_SetterMethods(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")

	// Test SetRecommendationsPager
	mockRecPager := &MockRecommendationsPager{}
	client.SetRecommendationsPager(mockRecPager)
	assert.Equal(t, mockRecPager, client.recommendationsPager)

	// Test SetReservationsPager
	mockResPager := &MockReservationsDetailsPager{}
	client.SetReservationsPager(mockResPager)
	assert.Equal(t, mockResPager, client.reservationsPager)

	// Test SetRedisCachesPager
	mockRedisPager := &MockRedisCachesPager{}
	client.SetRedisCachesPager(mockRedisPager)
	assert.Equal(t, mockRedisPager, client.redisCachesPager)
}

func TestCacheClient_GetCommonSKUs(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")
	skus := client.getCommonSKUs()

	assert.Contains(t, skus, "Basic_C0")
	assert.Contains(t, skus, "Basic_C6")
	assert.Contains(t, skus, "Standard_C0")
	assert.Contains(t, skus, "Standard_C6")
	assert.Contains(t, skus, "Premium_P1")
	assert.Contains(t, skus, "Premium_P5")
}

func TestCacheClient_ConvertAzureRedisRecommendation(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Test with nil recommendation
	rec := client.convertAzureRedisRecommendation(ctx, nil)
	require.NotNil(t, rec)
	assert.Equal(t, common.ProviderAzure, rec.Provider)
	assert.Equal(t, common.ServiceCache, rec.Service)
	assert.Equal(t, "test-subscription", rec.Account)
	assert.Equal(t, "eastus", rec.Region)
	assert.Equal(t, common.CommitmentReservedInstance, rec.CommitmentType)
	assert.Equal(t, "1yr", rec.Term)
	assert.Equal(t, "upfront", rec.PaymentOption)
}

// Test the to package is properly imported (used in tests)
var _ = to.Ptr("test")

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

func TestCacheClient_PurchaseCommitment_Success(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "Premium_P1",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 1000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.NotEmpty(t, result.CommitmentID)
	assert.Equal(t, 1000.0, result.Cost)
}

func TestCacheClient_PurchaseCommitment_3YearTerm(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusCreated, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "Premium_P1",
		Term:           "3yr",
		Count:          1,
		CommitmentCost: 2500.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestCacheClient_PurchaseCommitment_Accepted(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusAccepted, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "Premium_P1",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 1000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestCacheClient_PurchaseCommitment_TokenError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{err: errors.New("token error")}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	rec := common.Recommendation{
		ResourceType: "Premium_P1",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to get access token")
}

func TestCacheClient_PurchaseCommitment_HTTPError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(nil, errors.New("network error"))

	rec := common.Recommendation{
		ResourceType: "Premium_P1",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to purchase reservation")
}

func TestCacheClient_PurchaseCommitment_BadStatus(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusBadRequest, `{"error": "invalid request"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType: "Premium_P1",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "reservation purchase failed with status 400")
}
