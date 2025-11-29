package cosmosdb

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// MockRecommendationsPager mocks the RecommendationsPager interface
type MockRecommendationsPager struct {
	pages []armconsumption.ReservationRecommendationsClientListResponse
	index int
	err   error
}

func (m *MockRecommendationsPager) More() bool {
	if m.err != nil {
		return true // Return true to allow error to be returned on NextPage
	}
	return m.index < len(m.pages)
}

func (m *MockRecommendationsPager) NextPage(ctx context.Context) (armconsumption.ReservationRecommendationsClientListResponse, error) {
	if m.err != nil {
		return armconsumption.ReservationRecommendationsClientListResponse{}, m.err
	}
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
	if m.err != nil {
		return true // Return true to allow error to be returned on NextPage
	}
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

// MockCosmosAccountsPager mocks the CosmosAccountsPager interface
type MockCosmosAccountsPager struct {
	pages []armcosmos.DatabaseAccountsClientListResponse
	index int
	err   error
}

func (m *MockCosmosAccountsPager) More() bool {
	if m.err != nil {
		return true // Return true to allow error to be returned on NextPage
	}
	return m.index < len(m.pages)
}

func (m *MockCosmosAccountsPager) NextPage(ctx context.Context) (armcosmos.DatabaseAccountsClientListResponse, error) {
	if m.err != nil {
		return armcosmos.DatabaseAccountsClientListResponse{}, m.err
	}
	if m.index >= len(m.pages) {
		return armcosmos.DatabaseAccountsClientListResponse{}, errors.New("no more pages")
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

func createSampleCosmosPricingResponse() string {
	return `{
		"Items": [
			{
				"currencyCode": "USD",
				"retailPrice": 1000.0,
				"unitPrice": 1000.0,
				"armRegionName": "eastus",
				"productName": "Azure Cosmos DB",
				"serviceName": "Azure Cosmos DB",
				"skuName": "100RU",
				"meterName": "100 RU/s",
				"reservationTerm": "1 Years",
				"type": "Reservation"
			},
			{
				"currencyCode": "USD",
				"retailPrice": 0.008,
				"unitPrice": 0.008,
				"armRegionName": "eastus",
				"productName": "Azure Cosmos DB",
				"serviceName": "Azure Cosmos DB",
				"skuName": "100RU",
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

func TestCosmosDBClient_GetServiceType(t *testing.T) {
	client := NewClient(nil, "sub", "region")
	assert.Equal(t, common.ServiceNoSQLDB, client.GetServiceType())
}

func TestCosmosDBClient_GetRegion(t *testing.T) {
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

func TestCosmosDBClient_Fields(t *testing.T) {
	client := NewClient(nil, "test-sub", "northeurope")

	assert.Equal(t, "test-sub", client.subscriptionID)
	assert.Equal(t, "northeurope", client.region)
	assert.Nil(t, client.cred)
}

func TestCosmosDBClient_GetOfferingDetails_WithMock(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, createSampleCosmosPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "100RU",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, "100RU", details.ResourceType)
	assert.Equal(t, "1yr", details.Term)
	assert.Equal(t, "USD", details.Currency)
}

func TestCosmosDBClient_GetOfferingDetails_3YearTerm(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, createSampleCosmosPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "100RU",
		Term:          "3yr",
		PaymentOption: "monthly",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, "3yr", details.Term)
	assert.Equal(t, "monthly", details.PaymentOption)
}

func TestCosmosDBClient_GetOfferingDetails_NoUpfront(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, createSampleCosmosPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "100RU",
		Term:          "1yr",
		PaymentOption: "no-upfront",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, float64(0), details.UpfrontCost)
	assert.Greater(t, details.RecurringCost, float64(0))
}

func TestCosmosDBClient_GetOfferingDetails_APIError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusInternalServerError, "Internal Server Error"),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "100RU",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pricing API returned status 500")
}

func TestCosmosDBClient_GetOfferingDetails_NoPricing(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, `{"Items": []}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "100RU",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no pricing data found")
}

func TestCosmosDBClient_GetExistingCommitments_Empty(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Will return empty without credentials
	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestCosmosPricingStructure(t *testing.T) {
	pricing := CosmosPricing{
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

func TestCosmosDBClient_GetRecommendations_WithMockPager(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager with test data
	mockPager := &MockRecommendationsPager{
		pages: []armconsumption.ReservationRecommendationsClientListResponse{
			{
				ReservationRecommendationsListResult: armconsumption.ReservationRecommendationsListResult{
					Value: []armconsumption.ReservationRecommendationClassification{
						// The actual type would be more complex, but for testing we can use nil
						// as the convertAzureCosmosRecommendation handles nil gracefully
					},
				},
			},
		},
	}
	client.SetRecommendationsPager(mockPager)

	recommendations, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	require.NoError(t, err)
	assert.NotNil(t, recommendations)
}

func TestCosmosDBClient_GetRecommendations_PagerError(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager that will return an error
	mockPager := &MockRecommendationsPager{
		err: errors.New("API error"),
	}

	client.SetRecommendationsPager(mockPager)

	_, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get Cosmos DB recommendations")
}

func TestCosmosDBClient_GetRecommendations_MultiplePages(t *testing.T) {
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

	recommendations, err := client.GetRecommendations(ctx, common.RecommendationParams{})
	require.NoError(t, err)
	assert.NotNil(t, recommendations)
	assert.Equal(t, 2, mockPager.index) // Verify both pages were consumed
}

func TestCosmosDBClient_GetExistingCommitments_WithMockPager(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	reservationID := "test-reservation-123"
	skuName := "sql-db-standard" // Does NOT contain "cosmos" so won't be included

	// Create mock pager with test data
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
	assert.NotNil(t, commitments)
	// The SKU name doesn't contain "cosmos" so won't be included
	assert.Empty(t, commitments)
}

func TestCosmosDBClient_GetExistingCommitments_CosmosCommitments(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	reservationID := "test-reservation-123"
	skuName := "cosmos-db-standard" // Contains "cosmos"

	// Create mock pager with cosmos commitment
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
	assert.Equal(t, common.ServiceNoSQLDB, commitments[0].Service)
}

func TestCosmosDBClient_GetExistingCommitments_PagerError(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager that returns an error
	mockPager := &MockReservationsDetailsPager{
		err: errors.New("API error"),
	}
	client.SetReservationsPager(mockPager)

	// Should return empty (not error) due to graceful handling
	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestCosmosDBClient_GetExistingCommitments_NilProperties(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager with nil properties
	mockPager := &MockReservationsDetailsPager{
		pages: []armconsumption.ReservationsDetailsClientListByReservationOrderResponse{
			{
				ReservationDetailsListResult: armconsumption.ReservationDetailsListResult{
					Value: []*armconsumption.ReservationDetail{
						{
							Properties: nil, // Nil properties should be skipped
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

func TestCosmosDBClient_GetValidResourceTypes_WithMockPager(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	capability1 := "EnableCassandra"
	capability2 := "EnableMongo"

	// Create mock pager with test data
	mockPager := &MockCosmosAccountsPager{
		pages: []armcosmos.DatabaseAccountsClientListResponse{
			{
				DatabaseAccountsListResult: armcosmos.DatabaseAccountsListResult{
					Value: []*armcosmos.DatabaseAccountGetResults{
						{
							Properties: &armcosmos.DatabaseAccountGetProperties{
								Capabilities: []*armcosmos.Capability{
									{Name: &capability1},
									{Name: &capability2},
								},
							},
						},
					},
				},
			},
		},
	}
	client.SetCosmosAccountsPager(mockPager)

	skus, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	assert.Len(t, skus, 2)
	assert.Contains(t, skus, "EnableCassandra")
	assert.Contains(t, skus, "EnableMongo")
}

func TestCosmosDBClient_GetValidResourceTypes_PagerError(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager that returns an error
	mockPager := &MockCosmosAccountsPager{
		err: errors.New("API error"),
	}
	client.SetCosmosAccountsPager(mockPager)

	// Should fallback to common SKUs
	skus, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, skus)
	// Should contain the common SKUs
	assert.Contains(t, skus, "EnableCassandra")
	assert.Contains(t, skus, "EnableMongo")
}

func TestCosmosDBClient_GetValidResourceTypes_NoCapabilities(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Create mock pager with accounts but no capabilities
	mockPager := &MockCosmosAccountsPager{
		pages: []armcosmos.DatabaseAccountsClientListResponse{
			{
				DatabaseAccountsListResult: armcosmos.DatabaseAccountsListResult{
					Value: []*armcosmos.DatabaseAccountGetResults{
						{
							Properties: &armcosmos.DatabaseAccountGetProperties{
								Capabilities: nil,
							},
						},
					},
				},
			},
		},
	}
	client.SetCosmosAccountsPager(mockPager)

	// Should fallback to common SKUs
	skus, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, skus)
	// Should contain the common SKUs
	assert.Contains(t, skus, "EnableCassandra")
}

func TestCosmosDBClient_ValidateOffering_Valid(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	capability := "EnableCassandra"

	mockPager := &MockCosmosAccountsPager{
		pages: []armcosmos.DatabaseAccountsClientListResponse{
			{
				DatabaseAccountsListResult: armcosmos.DatabaseAccountsListResult{
					Value: []*armcosmos.DatabaseAccountGetResults{
						{
							Properties: &armcosmos.DatabaseAccountGetProperties{
								Capabilities: []*armcosmos.Capability{
									{Name: &capability},
								},
							},
						},
					},
				},
			},
		},
	}
	client.SetCosmosAccountsPager(mockPager)

	rec := common.Recommendation{
		ResourceType: "EnableCassandra",
	}

	err := client.ValidateOffering(ctx, rec)
	assert.NoError(t, err)
}

func TestCosmosDBClient_ValidateOffering_Invalid(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	capability := "EnableCassandra"

	mockPager := &MockCosmosAccountsPager{
		pages: []armcosmos.DatabaseAccountsClientListResponse{
			{
				DatabaseAccountsListResult: armcosmos.DatabaseAccountsListResult{
					Value: []*armcosmos.DatabaseAccountGetResults{
						{
							Properties: &armcosmos.DatabaseAccountGetProperties{
								Capabilities: []*armcosmos.Capability{
									{Name: &capability},
								},
							},
						},
					},
				},
			},
		},
	}
	client.SetCosmosAccountsPager(mockPager)

	rec := common.Recommendation{
		ResourceType: "InvalidSKU",
	}

	err := client.ValidateOffering(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Azure Cosmos DB SKU")
}

func TestCosmosDBClient_SetterMethods(t *testing.T) {
	client := NewClient(nil, "test-sub", "eastus")

	// Test SetRecommendationsPager
	mockRecPager := &MockRecommendationsPager{}
	client.SetRecommendationsPager(mockRecPager)
	assert.Equal(t, mockRecPager, client.recommendationsPager)

	// Test SetReservationsPager
	mockResPager := &MockReservationsDetailsPager{}
	client.SetReservationsPager(mockResPager)
	assert.Equal(t, mockResPager, client.reservationsPager)

	// Test SetCosmosAccountsPager
	mockAccountsPager := &MockCosmosAccountsPager{}
	client.SetCosmosAccountsPager(mockAccountsPager)
	assert.Equal(t, mockAccountsPager, client.cosmosAccountsPager)
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

func TestCosmosDBClient_PurchaseCommitment_Success(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "EnableCassandra",
		Term:           "1yr",
		Count:          100,
		CommitmentCost: 5000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.NotEmpty(t, result.CommitmentID)
	assert.Equal(t, 5000.0, result.Cost)
}

func TestCosmosDBClient_PurchaseCommitment_3YearTerm(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusCreated, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "EnableCassandra",
		Term:           "3yr",
		Count:          100,
		CommitmentCost: 12000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestCosmosDBClient_PurchaseCommitment_Accepted(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusAccepted, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "EnableCassandra",
		Term:           "1yr",
		Count:          100,
		CommitmentCost: 5000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestCosmosDBClient_PurchaseCommitment_TokenError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{err: errors.New("token error")}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	rec := common.Recommendation{
		ResourceType: "EnableCassandra",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to get access token")
}

func TestCosmosDBClient_PurchaseCommitment_HTTPError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(nil, errors.New("network error"))

	rec := common.Recommendation{
		ResourceType: "EnableCassandra",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to purchase reservation")
}

func TestCosmosDBClient_PurchaseCommitment_BadStatus(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusBadRequest, `{"error": "invalid request"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType: "EnableCassandra",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "reservation purchase failed with status 400")
}

func TestCosmosDBClient_ConvertAzureCosmosRecommendation(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Test with nil recommendation
	rec := client.convertAzureCosmosRecommendation(ctx, nil)
	require.NotNil(t, rec)
	assert.Equal(t, common.ProviderAzure, rec.Provider)
	assert.Equal(t, common.ServiceNoSQLDB, rec.Service)
	assert.Equal(t, "test-subscription", rec.Account)
	assert.Equal(t, "eastus", rec.Region)
	assert.Equal(t, common.CommitmentReservedInstance, rec.CommitmentType)
	assert.Equal(t, "1yr", rec.Term)
	assert.Equal(t, "upfront", rec.PaymentOption)
}
