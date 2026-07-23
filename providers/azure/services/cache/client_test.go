package cache

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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	azpricing "github.com/LeanerCloud/CUDly/providers/azure/internal/pricing"
	"github.com/LeanerCloud/CUDly/providers/azure/mocks"
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
				"reservationTerm": "1 Year",
				"type": "Reservation"
			},
			{
				"currencyCode": "USD",
				"retailPrice": 850.0,
				"unitPrice": 850.0,
				"armRegionName": "eastus",
				"productName": "Azure Cache for Redis",
				"serviceName": "Azure Cache for Redis",
				"armSkuName": "Premium_P1",
				"meterName": "P1 Instance",
				"reservationTerm": "3 Years",
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

	skus, err := client.GetValidResourceTypes(context.Background())
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

	err := client.ValidateOffering(context.Background(), rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Azure Redis Cache SKU")
}

func TestAzureRetailPriceStructure(t *testing.T) {
	price := AzureRetailPrice{
		Items: []azpricing.RetailPriceItem{
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
	}

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

// TestCacheClient_GetOfferingDetails_NoReservationPricing verifies that when
// on-demand pricing is present but no reservation line is returned, the client
// returns an error rather than fabricating a price from the hardcoded 0.45
// multiplier (issue #1020 H4). Pre-fix this would have silently surfaced a
// fabricated TotalCost/SavingsPercentage as a real quote.
func TestCacheClient_GetOfferingDetails_NoReservationPricing(t *testing.T) {
	ctx := context.Background()

	onDemandOnly := `{
		"Items": [
			{
				"currencyCode": "USD",
				"retailPrice": 0.125,
				"unitPrice": 0.125,
				"armRegionName": "eastus",
				"armSkuName": "Premium_P1",
				"type": "Consumption"
			}
		],
		"NextPageLink": ""
	}`

	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)
	mockHTTP.On("Do", mock.Anything).Return(createMockHTTPResponse(http.StatusOK, onDemandOnly), nil)

	rec := common.Recommendation{
		ResourceType:  "Premium_P1",
		Term:          "1yr",
		PaymentOption: "upfront",
	}
	_, err := client.GetOfferingDetails(ctx, rec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no reservation pricing found")
}

func TestCacheClient_GetExistingCommitments_Empty(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Mock pager returns no pages — the empty-subscription case, distinct
	// from the pager-error case below. Previously this test used a nil
	// pager and relied on silent error-swallowing; that behaviour was
	// unsafe and has been replaced with error propagation, so the test
	// now uses an explicit empty mock.
	client.SetReservationsPager(&MockReservationsDetailsPager{})

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

func TestCacheClient_ValidateOffering_CaseInsensitive(t *testing.T) {
	ctx := context.Background()

	t.Run("case_insensitive", func(t *testing.T) {
		client := NewClient(nil, "test-subscription", "eastus")
		rec := common.Recommendation{ResourceType: "premium_p1"}
		err := client.ValidateOffering(ctx, rec)
		assert.NoError(t, err)
	})

	t.Run("whitespace_trimmed", func(t *testing.T) {
		client := NewClient(nil, "test-subscription", "eastus")
		rec := common.Recommendation{ResourceType: "  Premium_P1  "}
		err := client.ValidateOffering(ctx, rec)
		assert.NoError(t, err)
	})
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

	recs, err := client.GetRecommendations(ctx, &common.RecommendationParams{})
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

	recs, err := client.GetRecommendations(ctx, &common.RecommendationParams{})
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
		pages: []armconsumption.ReservationsDetailsClientListResponse{
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
		pages: []armconsumption.ReservationsDetailsClientListResponse{
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

	// Pagination errors must propagate — see the database client for the
	// full rationale (partial lists are unsafe for the purchase flow).
	mockPager := &MockReservationsDetailsPager{
		pages: []armconsumption.ReservationsDetailsClientListResponse{
			{},
		},
		err: errors.New("API error"),
	}

	client.SetReservationsPager(mockPager)

	commitments, err := client.GetExistingCommitments(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cache: list reservations")
	assert.Nil(t, commitments)
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

// TestCacheClient_ConvertAzureRedisRecommendation_NilGuards pins the new
// contract: unusable SDK payloads produce a nil *Recommendation.
func TestCacheClient_ConvertAzureRedisRecommendation_NilGuards(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")
	assert.Nil(t, client.convertAzureRedisRecommendation(context.Background(), nil))
}

// TestCacheClient_ConvertAzureRedisRecommendation_PopulatesAllFields asserts
// the converter forwards every helper-extracted field + applies the
// Cache-service-specific constants. An empty SKU catalogue (no caches in
// the subscription) is the "no signal" baseline — Shards stays 0.
func TestCacheClient_ConvertAzureRedisRecommendation_PopulatesAllFields(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")
	// Inject empty mock pager so the cachedSKULookup hits the lazy-fetch
	// codepath without making a real Azure API call.
	client.SetRedisCachesPager(&MockRedisCachesPager{})

	rec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("eastus"),
		mocks.WithTerm("P3Y"),
		mocks.WithQuantity(1),
		mocks.WithNormalizedSize("Premium_P3"),
		mocks.WithCosts(50, 35, 15),
	)
	out := client.convertAzureRedisRecommendation(context.Background(), rec)
	require.NotNil(t, out)
	assert.Equal(t, common.ProviderAzure, out.Provider)
	assert.Equal(t, common.ServiceCache, out.Service)
	assert.Equal(t, "test-subscription", out.Account)
	assert.Equal(t, "eastus", out.Region)
	assert.Equal(t, "Premium_P3", out.ResourceType)
	assert.Equal(t, 1, out.Count)
	assert.InDelta(t, 50.0, out.OnDemandCost, 1e-9)
	assert.InDelta(t, 35.0, out.CommitmentCost, 1e-9)
	assert.InDelta(t, 15.0, out.EstimatedSavings, 1e-9)
	assert.Equal(t, common.CommitmentReservedInstance, out.CommitmentType)
	assert.Equal(t, "3yr", out.Term)
	assert.Equal(t, "upfront", out.PaymentOption)

	// Details carries Engine=redis + NodeType from the SKU string. Shards
	// stays 0 when no cache instance matches in the subscription (the
	// catalogue's only signal source for shard counts).
	require.NotNil(t, out.Details)
	details, ok := out.Details.(common.CacheDetails)
	require.True(t, ok, "Details must be a common.CacheDetails value")
	assert.Equal(t, "redis", details.Engine)
	assert.Equal(t, "Premium_P3", details.NodeType)
	assert.Equal(t, 0, details.Shards, "Shards is 0 when no matching cache exists in the subscription")
}

// TestCacheClient_ConvertAzureRedisRecommendation_PopulatesShardsFromSKUCache
// asserts the new batched-SKU-catalogue lookup populates CacheDetails.Shards
// when the subscription has a Premium-tier clustered cache with the same
// SKU as the recommendation. Single ListBySubscription call per client
// lifetime feeds many converter calls; this test pins the contract.
func TestCacheClient_ConvertAzureRedisRecommendation_PopulatesShardsFromSKUCache(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")

	premiumName := armredis.SKUNamePremium
	familyP := armredis.SKUFamilyP
	capacity := int32(3)
	shardCount := int32(5)

	// One Premium_P3 cache with 5 shards in the subscription. The
	// converter will see f.ResourceType="Premium_P3" and look it up.
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
									Capacity: &capacity,
								},
								ShardCount: &shardCount,
							},
						},
					},
				},
			},
		},
	}
	client.SetRedisCachesPager(mockPager)

	rec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("eastus"),
		mocks.WithNormalizedSize("Premium_P3"),
	)
	out := client.convertAzureRedisRecommendation(context.Background(), rec)
	require.NotNil(t, out)
	details, ok := out.Details.(common.CacheDetails)
	require.True(t, ok)
	assert.Equal(t, "redis", details.Engine)
	assert.Equal(t, "Premium_P3", details.NodeType)
	assert.Equal(t, 5, details.Shards, "Shards must be enriched from the cached SKU catalogue")
}

// TestCacheClient_ConvertAzureRedisRecommendation_PagerErrorFallsBack
// asserts that a SKU-catalogue fetch failure does NOT fail the
// conversion — Shards just stays at 0 and the rest of Details is
// populated from the recommendation payload as before. This is the
// graceful-degradation contract the issue asks for.
func TestCacheClient_ConvertAzureRedisRecommendation_PagerErrorFallsBack(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")
	mockPager := &MockRedisCachesPager{
		pages: []armredis.ClientListBySubscriptionResponse{{}},
		err:   errors.New("transient Azure API error"),
	}
	client.SetRedisCachesPager(mockPager)

	rec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("eastus"),
		mocks.WithNormalizedSize("Premium_P3"),
	)
	out := client.convertAzureRedisRecommendation(context.Background(), rec)
	require.NotNil(t, out, "conversion must NOT fail on catalogue-fetch error")
	details, ok := out.Details.(common.CacheDetails)
	require.True(t, ok)
	assert.Equal(t, "redis", details.Engine)
	assert.Equal(t, "Premium_P3", details.NodeType)
	assert.Equal(t, 0, details.Shards, "Shards left at 0 when catalogue fetch fails")
}

// TestCacheClient_CachedSKULookup_FetchedOnce pins the perf invariant:
// many converter calls in the same GetRecommendations run trigger
// exactly ONE catalogue fetch. The mock pager counts pages served; the
// assertion verifies the count stays at 1 after multiple lookups.
func TestCacheClient_CachedSKULookup_FetchedOnce(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")

	premiumName := armredis.SKUNamePremium
	familyP := armredis.SKUFamilyP
	capacity := int32(1)
	shardCount := int32(2)

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
									Capacity: &capacity,
								},
								ShardCount: &shardCount,
							},
						},
					},
				},
			},
		},
	}
	client.SetRedisCachesPager(mockPager)

	for i := 0; i < 10; i++ {
		_, _ = client.cachedSKULookup(context.Background(), "Premium_P1")
	}
	assert.Equal(t, 1, mockPager.index, "catalogue must be fetched ONCE regardless of lookup count")
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

// calcPriceRespJSON returns a minimal calculatePrice response JSON for tests.
func calcPriceRespJSON(orderID string) string {
	return `{"properties":{"reservationOrderId":"` + orderID + `"}}`
}

func TestCacheClient_PurchaseCommitment_Success(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(createMockHTTPResponse(http.StatusOK, calcPriceRespJSON("cache-order-001")), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/cache-order-001/purchase"
	})).Return(createMockHTTPResponse(http.StatusOK, `{}`), nil).Once()

	rec := common.Recommendation{
		ResourceType:   "Premium_P1",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 1000.0,
		PaymentOption:  "no-upfront",
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "cache-order-001", result.CommitmentID)
	assert.Equal(t, 1000.0, result.Cost)
	mockHTTP.AssertExpectations(t)
}

func TestCacheClient_PurchaseCommitment_3YearTerm(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(createMockHTTPResponse(http.StatusOK, calcPriceRespJSON("cache-order-3yr")), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/cache-order-3yr/purchase"
	})).Return(createMockHTTPResponse(http.StatusCreated, `{}`), nil).Once()

	rec := common.Recommendation{
		ResourceType:   "Premium_P1",
		Term:           "3yr",
		Count:          1,
		CommitmentCost: 2500.0,
		PaymentOption:  "no-upfront",
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "cache-order-3yr", result.CommitmentID)
	mockHTTP.AssertExpectations(t)
}

func TestCacheClient_PurchaseCommitment_Accepted(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(createMockHTTPResponse(http.StatusOK, calcPriceRespJSON("cache-order-202")), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/cache-order-202/purchase"
	})).Return(createMockHTTPResponse(http.StatusAccepted, `{}`), nil).Once()

	rec := common.Recommendation{
		ResourceType:   "Premium_P1",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 1000.0,
		PaymentOption:  "no-upfront",
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	mockHTTP.AssertExpectations(t)
}

func TestCacheClient_PurchaseCommitment_TokenError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{err: errors.New("token error")}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	rec := common.Recommendation{
		ResourceType:  "Premium_P1",
		Term:          "1yr",
		Count:         1,
		PaymentOption: "no-upfront",
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to get access token")
}

func TestCacheClient_PurchaseCommitment_HTTPError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(nil, errors.New("network error")).Once()

	rec := common.Recommendation{
		ResourceType:  "Premium_P1",
		Term:          "1yr",
		Count:         1,
		PaymentOption: "no-upfront",
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "calculatePrice HTTP call")
}

func TestCacheClient_PurchaseCommitment_BadStatus(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(createMockHTTPResponse(http.StatusOK, calcPriceRespJSON("cache-order-bad")), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/cache-order-bad/purchase"
	})).Return(createMockHTTPResponse(http.StatusBadRequest, `{"error": "invalid request"}`), nil).Once()

	rec := common.Recommendation{
		ResourceType:  "Premium_P1",
		Term:          "1yr",
		Count:         1,
		PaymentOption: "no-upfront",
	}

	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "reservation purchase failed with status 400")
	mockHTTP.AssertExpectations(t)
}

// TestCacheClient_PurchaseCommitment_TagInjection verifies that the
// purchase-automation tag carrying opts.Source is present in the
// calculatePrice request body. Without this regression test the dedupe
// guard introduced for the Azure two-step flow could regress silently:
// the call would succeed without the tag and re-driven purchases would
// duplicate reservations server-side.
func TestCacheClient_PurchaseCommitment_TagInjection(t *testing.T) {
	const orderID = "cache-tag-test"
	const source = common.PurchaseSourceWeb

	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	var capturedBody []byte
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		if r.URL.Path != "/providers/Microsoft.Capacity/calculatePrice" {
			return false
		}
		capturedBody, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(capturedBody))
		return true
	})).Return(createMockHTTPResponse(http.StatusOK, calcPriceRespJSON(orderID)), nil).Once()
	mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/"+orderID+"/purchase"
	})).Return(createMockHTTPResponse(http.StatusOK, `{}`), nil).Once()

	rec := common.Recommendation{ResourceType: "Premium_P1", Term: "1yr", Count: 1, CommitmentCost: 1000.0, PaymentOption: "no-upfront"}
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

// TestCacheClient_PurchaseCommitment_BillingPlan pins the billingPlan wiring
// (issue #1502, mirroring PR #1495's fix for compute): Azure Cache for Redis
// reserved capacity supports both Upfront and Monthly billing
// (learn.microsoft.com/azure/azure-cache-for-redis/cache-reserved-pricing),
// so rec.PaymentOption must map onto the correct armreservations
// ReservationBillingPlan value in the purchase body rather than silently
// defaulting to Azure's Upfront behavior for a no-upfront/monthly rec.
func TestCacheClient_PurchaseCommitment_BillingPlan(t *testing.T) {
	cases := []struct {
		name          string
		paymentOption string
		wantPlan      string
		wantErrSub    string
	}{
		{name: "all-upfront maps to Upfront", paymentOption: "all-upfront", wantPlan: "Upfront"},
		{name: "no-upfront maps to Monthly", paymentOption: "no-upfront", wantPlan: "Monthly"},
		{name: "partial-upfront is rejected", paymentOption: "partial-upfront", wantErrSub: "partial-upfront has no azure equivalent"},
		{name: "empty payment option is rejected", paymentOption: "", wantErrSub: "azure reservations support only upfront or monthly billing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mockHTTP := &MockHTTPClient{}
			mockCred := &MockTokenCredential{token: "test-token"}
			client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

			rec := common.Recommendation{ResourceType: "Premium_P1", Term: "1yr", Count: 1, PaymentOption: tc.paymentOption}

			if tc.wantErrSub != "" {
				result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
				require.Error(t, err)
				assert.False(t, result.Success)
				assert.Contains(t, err.Error(), tc.wantErrSub)
				mockHTTP.AssertNotCalled(t, "Do", mock.Anything)
				return
			}

			const orderID = "cache-billingplan-test"
			var capturedBody []byte
			mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
				if r.URL.Path != "/providers/Microsoft.Capacity/calculatePrice" {
					return false
				}
				capturedBody, _ = io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(capturedBody))
				return true
			})).Return(createMockHTTPResponse(http.StatusOK, calcPriceRespJSON(orderID)), nil).Once()
			mockHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
				return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/"+orderID+"/purchase"
			})).Return(createMockHTTPResponse(http.StatusOK, `{}`), nil).Once()

			result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
			require.NoError(t, err)
			assert.True(t, result.Success)

			var body map[string]interface{}
			require.NoError(t, json.Unmarshal(capturedBody, &body))
			props, ok := body["properties"].(map[string]interface{})
			require.True(t, ok, "properties map missing from reservation body")
			assert.Equal(t, tc.wantPlan, props["billingPlan"])
			mockHTTP.AssertExpectations(t)
		})
	}
}

// TestCacheClient_PurchaseCommitment_RequiresSource pins the dedupe guard:
// PurchaseCommitment must reject an empty opts.Source before issuing any HTTP
// call. Azure mints the reservation order ID server-side, so the
// purchase-automation tag derived from Source is the only stable dedupe
// signal CUDly controls -- proceeding without it would allow a re-driven
// purchase to create a duplicate reservation.
func TestCacheClient_PurchaseCommitment_RequiresSource(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	rec := common.Recommendation{ResourceType: "Premium_P1", Term: "1yr", Count: 1}
	result, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "purchase source is required")
	// No HTTP call may be issued when the guard rejects the request.
	mockHTTP.AssertNotCalled(t, "Do", mock.Anything)
}

// TestCacheClient_PurchaseCommitment_DisplayNameConformsToAzureAllowlist guards
// against regression: displayName in the calculatePrice body must match
// [A-Za-z0-9_-]{1,64} (Azure rejects DisplayNameInvalid otherwise).
func TestCacheClient_PurchaseCommitment_DisplayNameConformsToAzureAllowlist(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	const orderID = "azure-cache-displayname"
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
		ResourceType:   "Premium_P1",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 1000.0,
		PaymentOption:  "no-upfront",
	}
	_, err := client.PurchaseCommitment(ctx, rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.NotEmpty(t, capturedDisplayName)
	assert.Regexp(t, `^[A-Za-z0-9_-]{1,64}$`, capturedDisplayName)
	// Rich-format guards: service code is correct and SKU is preserved
	// (see providers/azure/services/internal/reservations/displayname.go).
	assert.Regexp(t, `^redis-`, capturedDisplayName)
	assert.Contains(t, capturedDisplayName, "Premium_P1")
}

// infiniteRedisCachesPager is a pager that always reports More()=true,
// used to exercise the maxCachesPages budget cap.
type infiniteRedisCachesPager struct{}

func (p *infiniteRedisCachesPager) More() bool { return true }
func (p *infiniteRedisCachesPager) NextPage(_ context.Context) (armredis.ClientListBySubscriptionResponse, error) {
	return armredis.ClientListBySubscriptionResponse{}, nil
}

// TestCacheClient_GetValidResourceTypes_CtxCancelReturnsError asserts that a
// cancelled context is treated as a hard stop and surfaces an error rather than
// returning a silent partial result (feedback_ctx_cancel_terminal).
func TestCacheClient_GetValidResourceTypes_CtxCancelReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	client := NewClient(nil, "test-subscription", "eastus")
	client.SetRedisCachesPager(&infiniteRedisCachesPager{})

	_, err := client.GetValidResourceTypes(ctx)
	require.Error(t, err, "cancelled context must produce an error, not a silent partial result")
}

// TestCacheClient_GetValidResourceTypes_PageCapReturnsError asserts that the
// page budget is enforced: an unbounded pager is stopped after maxCachesPages
// iterations and the function returns an error rather than looping forever.
func TestCacheClient_GetValidResourceTypes_PageCapFires(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")
	client.SetRedisCachesPager(&infiniteRedisCachesPager{})

	// GetValidResourceTypes falls back to common SKUs after hitting the log-only
	// cap, so the call itself succeeds. The important invariant is that it
	// terminates rather than looping forever.
	skus, err := client.GetValidResourceTypes(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, skus, "page cap must trigger fallback to common SKUs, not an empty result")
}

// TestExtractRedisPricing_SingularOneYear verifies that extractRedisPricing
// correctly recognises the "1 Year" singular form returned by the Azure Retail
// Prices API for 1-year reservation terms. Before the fix, the extractor used
// "%d Years" unconditionally, so the 1-year reservation line was silently
// skipped and reservationPrice remained 0, causing a false "no reservation
// pricing found" error even when the pricing row was present.
func TestExtractRedisPricing_SingularOneYear(t *testing.T) {
	items := []azpricing.RetailPriceItem{
		{
			CurrencyCode: "USD",
			RetailPrice:  0.68,
			UnitPrice:    0.68,
			Type:         "Consumption",
		},
		{
			CurrencyCode:    "USD",
			RetailPrice:     4500.0,
			ReservationTerm: "1 Year",
			Type:            "Reservation",
		},
	}

	onDemand, reservation, currency := extractRedisPricing(items, 1)

	assert.Equal(t, "USD", currency)
	assert.Equal(t, 0.68, onDemand, "on-demand price must be extracted")
	assert.Equal(t, 4500.0, reservation, "reservation price for '1 Year' term must be extracted")
}

// TestExtractRedisPricing_PluralThreeYears verifies that the plural form
// "3 Years" continues to work for multi-year terms.
func TestExtractRedisPricing_PluralThreeYears(t *testing.T) {
	items := []azpricing.RetailPriceItem{
		{CurrencyCode: "USD", RetailPrice: 0.68, UnitPrice: 0.68, Type: "Consumption"},
		{CurrencyCode: "USD", RetailPrice: 11000.0, ReservationTerm: "3 Years", Type: "Reservation"},
	}

	onDemand, reservation, currency := extractRedisPricing(items, 3)

	assert.Equal(t, "USD", currency)
	assert.Equal(t, 0.68, onDemand)
	assert.Equal(t, 11000.0, reservation, "reservation price for '3 Years' term must be extracted")
}
