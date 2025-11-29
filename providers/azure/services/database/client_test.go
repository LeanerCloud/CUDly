package database

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
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

// MockCapabilitiesClient mocks the CapabilitiesClient interface
type MockCapabilitiesClient struct {
	response armsql.CapabilitiesClientListByLocationResponse
	err      error
}

func (m *MockCapabilitiesClient) ListByLocation(ctx context.Context, locationName string, options *armsql.CapabilitiesClientListByLocationOptions) (armsql.CapabilitiesClientListByLocationResponse, error) {
	if m.err != nil {
		return armsql.CapabilitiesClientListByLocationResponse{}, m.err
	}
	return m.response, nil
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

func createSampleSQLPricingResponse() string {
	return `{
		"Items": [
			{
				"currencyCode": "USD",
				"retailPrice": 750.0,
				"unitPrice": 750.0,
				"armRegionName": "eastus",
				"location": "US East",
				"meterName": "S0 DTUs",
				"skuName": "Standard",
				"productName": "SQL Database",
				"serviceName": "SQL Database",
				"unitOfMeasure": "1 DTU/Hour",
				"type": "Reservation",
				"armSkuName": "Standard_S0",
				"reservationTerm": "1 Years"
			},
			{
				"currencyCode": "USD",
				"retailPrice": 0.096,
				"unitPrice": 0.096,
				"armRegionName": "eastus",
				"productName": "SQL Database",
				"serviceName": "SQL Database",
				"armSkuName": "Standard_S0",
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

func TestDatabaseClient_GetServiceType(t *testing.T) {
	client := NewClient(nil, "sub", "region")
	assert.Equal(t, common.ServiceRelationalDB, client.GetServiceType())
}

func TestDatabaseClient_GetRegion(t *testing.T) {
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
			name:     "Southeast Asia",
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

func TestSQLPricingStructure(t *testing.T) {
	pricing := SQLPricing{
		HourlyRate:        0.25,
		ReservationPrice:  2190.0,
		OnDemandPrice:     4380.0,
		Currency:          "USD",
		SavingsPercentage: 50.0,
	}

	assert.Equal(t, 0.25, pricing.HourlyRate)
	assert.Equal(t, 2190.0, pricing.ReservationPrice)
	assert.Equal(t, 4380.0, pricing.OnDemandPrice)
	assert.Equal(t, "USD", pricing.Currency)
	assert.Equal(t, 50.0, pricing.SavingsPercentage)
}

func TestAzureRetailPriceStructure(t *testing.T) {
	price := AzureRetailPrice{
		Count: 3,
		Items: []struct {
			CurrencyCode    string  `json:"currencyCode"`
			RetailPrice     float64 `json:"retailPrice"`
			UnitPrice       float64 `json:"unitPrice"`
			ArmRegionName   string  `json:"armRegionName"`
			Location        string  `json:"location"`
			MeterName       string  `json:"meterName"`
			SKUName         string  `json:"skuName"`
			ProductName     string  `json:"productName"`
			ServiceName     string  `json:"serviceName"`
			UnitOfMeasure   string  `json:"unitOfMeasure"`
			Type            string  `json:"type"`
			ArmSKUName      string  `json:"armSkuName"`
			ReservationTerm string  `json:"reservationTerm"`
		}{
			{
				CurrencyCode:    "USD",
				RetailPrice:     500.0,
				UnitPrice:       480.0,
				ArmRegionName:   "eastus",
				Location:        "US East",
				MeterName:       "S0 DTUs",
				SKUName:         "Standard",
				ProductName:     "SQL Database",
				ServiceName:     "SQL Database",
				UnitOfMeasure:   "1 DTU/Hour",
				Type:            "Reservation",
				ArmSKUName:      "Standard_S0",
				ReservationTerm: "1 Year",
			},
		},
		NextPageLink: "",
	}

	assert.Equal(t, 3, price.Count)
	require.Len(t, price.Items, 1)
	assert.Equal(t, "USD", price.Items[0].CurrencyCode)
	assert.Equal(t, "Standard_S0", price.Items[0].ArmSKUName)
	assert.Equal(t, "1 Year", price.Items[0].ReservationTerm)
}

func TestDatabaseClient_Fields(t *testing.T) {
	// Test that client stores fields correctly
	client := NewClient(nil, "test-sub", "northeurope")

	assert.Equal(t, "test-sub", client.subscriptionID)
	assert.Equal(t, "northeurope", client.region)
	assert.Nil(t, client.cred)
}

func TestNewClientWithHTTP(t *testing.T) {
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	require.NotNil(t, client)
	assert.Equal(t, "test-subscription", client.subscriptionID)
	assert.Equal(t, "eastus", client.region)
	assert.Equal(t, mockHTTP, client.httpClient)
}

func TestDatabaseClient_GetOfferingDetails_WithMock(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, createSampleSQLPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_S0",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, "Standard_S0", details.ResourceType)
	assert.Equal(t, "1yr", details.Term)
	assert.Equal(t, "USD", details.Currency)
}

func TestDatabaseClient_GetOfferingDetails_3YearTerm(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, createSampleSQLPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_S0",
		Term:          "3yr",
		PaymentOption: "monthly",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, "3yr", details.Term)
	assert.Equal(t, "monthly", details.PaymentOption)
}

func TestDatabaseClient_GetOfferingDetails_NoUpfront(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, createSampleSQLPricingResponse()),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_S0",
		Term:          "1yr",
		PaymentOption: "no-upfront",
	}

	details, err := client.GetOfferingDetails(ctx, rec)
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, float64(0), details.UpfrontCost)
	assert.Greater(t, details.RecurringCost, float64(0))
}

func TestDatabaseClient_GetOfferingDetails_APIError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusInternalServerError, "Internal Server Error"),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_S0",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pricing API returned status 500")
}

func TestDatabaseClient_GetOfferingDetails_NoPricing(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	client := NewClientWithHTTP(nil, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, `{"Items": []}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:  "Standard_S0",
		Term:          "1yr",
		PaymentOption: "upfront",
	}

	_, err := client.GetOfferingDetails(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no pricing data found")
}

func TestDatabaseClient_GetExistingCommitments_Empty(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Will return empty without credentials
	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestDatabaseClient_GetRecommendations_WithMockPager(t *testing.T) {
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

func TestDatabaseClient_GetRecommendations_MultiplePages(t *testing.T) {
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

func TestDatabaseClient_GetExistingCommitments_WithMockPager(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	reservationID := "test-reservation-123"
	skuName := "sql-standard-s0"

	// Create mock pager with SQL commitment
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
	assert.Equal(t, common.ServiceRelationalDB, commitments[0].Service)
}

func TestDatabaseClient_GetExistingCommitments_FilterNonSQL(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Test that non-SQL SKUs are filtered out
	nonSQLSKU := "redis-premium-p1"
	sqlSKU := "sql-premium-p2"
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
								SKUName:       &nonSQLSKU,
							},
						},
						{
							Properties: &armconsumption.ReservationDetailProperties{
								ReservationID: &reservationID2,
								SKUName:       &sqlSKU,
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

func TestDatabaseClient_GetExistingCommitments_NilProperties(t *testing.T) {
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

func TestDatabaseClient_GetExistingCommitments_PagerError(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Test that pager errors are handled gracefully
	mockPager := &MockReservationsDetailsPager{
		pages: []armconsumption.ReservationsDetailsClientListByReservationOrderResponse{{}},
		err:   errors.New("API error"),
	}

	client.SetReservationsPager(mockPager)

	commitments, err := client.GetExistingCommitments(ctx)
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestDatabaseClient_GetValidResourceTypes_WithMock(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	skuName := "Standard_S0"

	// Create mock capabilities client
	mockClient := &MockCapabilitiesClient{
		response: armsql.CapabilitiesClientListByLocationResponse{
			LocationCapabilities: armsql.LocationCapabilities{
				SupportedServerVersions: []*armsql.ServerVersionCapability{
					{
						SupportedEditions: []*armsql.EditionCapability{
							{
								SupportedServiceLevelObjectives: []*armsql.ServiceObjectiveCapability{
									{
										SKU: &armsql.SKU{
											Name: &skuName,
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

	client.SetCapabilitiesClient(mockClient)

	skus, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	require.Len(t, skus, 1)
	assert.Equal(t, skuName, skus[0])
}

func TestDatabaseClient_GetValidResourceTypes_ManagedInstance(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	editionName := "GeneralPurpose"

	// Create mock capabilities client with managed instance editions
	mockClient := &MockCapabilitiesClient{
		response: armsql.CapabilitiesClientListByLocationResponse{
			LocationCapabilities: armsql.LocationCapabilities{
				SupportedManagedInstanceVersions: []*armsql.ManagedInstanceVersionCapability{
					{
						SupportedEditions: []*armsql.ManagedInstanceEditionCapability{
							{
								Name: &editionName,
							},
						},
					},
				},
			},
		},
	}

	client.SetCapabilitiesClient(mockClient)

	skus, err := client.GetValidResourceTypes(ctx)
	require.NoError(t, err)
	require.Len(t, skus, 1)
	assert.Equal(t, editionName, skus[0])
}

func TestDatabaseClient_GetValidResourceTypes_Error(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	mockClient := &MockCapabilitiesClient{
		err: errors.New("API error"),
	}

	client.SetCapabilitiesClient(mockClient)

	_, err := client.GetValidResourceTypes(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list SQL capabilities")
}

func TestDatabaseClient_GetValidResourceTypes_Empty(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	mockClient := &MockCapabilitiesClient{
		response: armsql.CapabilitiesClientListByLocationResponse{
			LocationCapabilities: armsql.LocationCapabilities{},
		},
	}

	client.SetCapabilitiesClient(mockClient)

	_, err := client.GetValidResourceTypes(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no SQL Database SKUs found")
}

func TestDatabaseClient_SetterMethods(t *testing.T) {
	client := NewClient(nil, "test-subscription", "eastus")

	// Test SetRecommendationsPager
	mockRecPager := &MockRecommendationsPager{}
	client.SetRecommendationsPager(mockRecPager)
	assert.Equal(t, mockRecPager, client.recommendationsPager)

	// Test SetReservationsPager
	mockResPager := &MockReservationsDetailsPager{}
	client.SetReservationsPager(mockResPager)
	assert.Equal(t, mockResPager, client.reservationsPager)

	// Test SetCapabilitiesClient
	mockCapClient := &MockCapabilitiesClient{}
	client.SetCapabilitiesClient(mockCapClient)
	assert.Equal(t, mockCapClient, client.capabilitiesClient)
}

func TestDatabaseClient_ConvertAzureSQLRecommendation(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	// Test with nil recommendation
	rec := client.convertAzureSQLRecommendation(ctx, nil)
	require.NotNil(t, rec)
	assert.Equal(t, common.ProviderAzure, rec.Provider)
	assert.Equal(t, common.ServiceRelationalDB, rec.Service)
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

func TestDatabaseClient_PurchaseCommitment_Success(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusOK, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "GP_Gen5_8",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 5000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.NotEmpty(t, result.CommitmentID)
	assert.Equal(t, 5000.0, result.Cost)
}

func TestDatabaseClient_PurchaseCommitment_3YearTerm(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusCreated, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "GP_Gen5_8",
		Term:           "3yr",
		Count:          1,
		CommitmentCost: 12000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestDatabaseClient_PurchaseCommitment_Accepted(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusAccepted, `{"id": "reservation-123"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType:   "GP_Gen5_8",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 5000.0,
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestDatabaseClient_PurchaseCommitment_TokenError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{err: errors.New("token error")}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	rec := common.Recommendation{
		ResourceType: "GP_Gen5_8",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to get access token")
}

func TestDatabaseClient_PurchaseCommitment_HTTPError(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(nil, errors.New("network error"))

	rec := common.Recommendation{
		ResourceType: "GP_Gen5_8",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to purchase reservation")
}

func TestDatabaseClient_PurchaseCommitment_BadStatus(t *testing.T) {
	ctx := context.Background()
	mockHTTP := &MockHTTPClient{}
	mockCred := &MockTokenCredential{token: "test-token"}
	client := NewClientWithHTTP(mockCred, "test-subscription", "eastus", mockHTTP)

	mockHTTP.On("Do", mock.Anything).Return(
		createMockHTTPResponse(http.StatusBadRequest, `{"error": "invalid request"}`),
		nil,
	)

	rec := common.Recommendation{
		ResourceType: "GP_Gen5_8",
		Term:         "1yr",
	}

	result, err := client.PurchaseCommitment(ctx, rec)
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "reservation purchase failed with status 400")
}

func TestDatabaseClient_ValidateOffering_Valid(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	skuName := "GP_Gen5_8"
	mockClient := &MockCapabilitiesClient{
		response: armsql.CapabilitiesClientListByLocationResponse{
			LocationCapabilities: armsql.LocationCapabilities{
				SupportedServerVersions: []*armsql.ServerVersionCapability{
					{
						SupportedEditions: []*armsql.EditionCapability{
							{
								SupportedServiceLevelObjectives: []*armsql.ServiceObjectiveCapability{
									{SKU: &armsql.SKU{Name: &skuName}},
								},
							},
						},
					},
				},
			},
		},
	}

	client.SetCapabilitiesClient(mockClient)

	rec := common.Recommendation{
		ResourceType: "GP_Gen5_8",
	}

	err := client.ValidateOffering(ctx, rec)
	assert.NoError(t, err)
}

func TestDatabaseClient_ValidateOffering_Invalid(t *testing.T) {
	ctx := context.Background()
	client := NewClient(nil, "test-subscription", "eastus")

	skuName := "GP_Gen5_8"
	mockClient := &MockCapabilitiesClient{
		response: armsql.CapabilitiesClientListByLocationResponse{
			LocationCapabilities: armsql.LocationCapabilities{
				SupportedServerVersions: []*armsql.ServerVersionCapability{
					{
						SupportedEditions: []*armsql.EditionCapability{
							{
								SupportedServiceLevelObjectives: []*armsql.ServiceObjectiveCapability{
									{SKU: &armsql.SKU{Name: &skuName}},
								},
							},
						},
					},
				},
			},
		},
	}

	client.SetCapabilitiesClient(mockClient)

	rec := common.Recommendation{
		ResourceType: "InvalidSKU",
	}

	err := client.ValidateOffering(ctx, rec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Azure SQL Database SKU")
}
