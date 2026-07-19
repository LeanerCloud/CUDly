package managedredis

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/azure/mocks"
)

// -- mock helpers --

type mockRecommendationsPager struct {
	pages []armconsumption.ReservationRecommendationsClientListResponse
	index int
}

func (m *mockRecommendationsPager) More() bool { return m.index < len(m.pages) }
func (m *mockRecommendationsPager) NextPage(_ context.Context) (armconsumption.ReservationRecommendationsClientListResponse, error) {
	if m.index >= len(m.pages) {
		return armconsumption.ReservationRecommendationsClientListResponse{}, errors.New("no more pages")
	}
	p := m.pages[m.index]
	m.index++
	return p, nil
}

type mockReservationsPager struct {
	pages []armconsumption.ReservationsDetailsClientListResponse
	index int
	err   error
}

func (m *mockReservationsPager) More() bool { return m.index < len(m.pages) }
func (m *mockReservationsPager) NextPage(_ context.Context) (armconsumption.ReservationsDetailsClientListResponse, error) {
	if m.err != nil {
		return armconsumption.ReservationsDetailsClientListResponse{}, m.err
	}
	if m.index >= len(m.pages) {
		return armconsumption.ReservationsDetailsClientListResponse{}, errors.New("no more pages")
	}
	p := m.pages[m.index]
	m.index++
	return p, nil
}

type mockRedisPager struct {
	pages []armredis.ClientListBySubscriptionResponse
	index int
	err   error
}

func (m *mockRedisPager) More() bool { return m.index < len(m.pages) }
func (m *mockRedisPager) NextPage(_ context.Context) (armredis.ClientListBySubscriptionResponse, error) {
	if m.err != nil {
		return armredis.ClientListBySubscriptionResponse{}, m.err
	}
	if m.index >= len(m.pages) {
		return armredis.ClientListBySubscriptionResponse{}, errors.New("no more pages")
	}
	p := m.pages[m.index]
	m.index++
	return p, nil
}

// The mock HTTP client and response helper live in the shared
// providers/azure/mocks package (mocks.MockHTTPClient, mocks.CreateMockHTTPResponse).

func samplePricingJSON() string {
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
				"retailPrice": 900.0,
				"unitPrice": 900.0,
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

type mockTokenCredential struct {
	token string
	err   error
}

func (m *mockTokenCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	if m.err != nil {
		return azcore.AccessToken{}, m.err
	}
	return azcore.AccessToken{Token: m.token, ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// -- constructor tests --

func TestNewClient(t *testing.T) {
	c := NewClient(nil, "sub-123", "eastus")
	require.NotNil(t, c)
	assert.Equal(t, "sub-123", c.subscriptionID)
	assert.Equal(t, "eastus", c.region)
	assert.NotNil(t, c.httpClient)
}

// TestNewClient_UsesHardenedHTTPClient verifies that NewClient installs the
// SSRF-hardened httpclient (blocks IMDS at 169.254.169.254) rather than a
// bare &http.Client{}. Pre-fix this would have passed with a bare client that
// allows IMDS connections (issue #1021 H1).
func TestNewClient_UsesHardenedHTTPClient(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	require.NotNil(t, c.httpClient)
	// The hardened client blocks IMDS; the bare client does not.
	// Attempt a dial to the IMDS address and confirm it is rejected.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://169.254.169.254/metadata/instance", nil)
	require.NoError(t, err)
	_, err = c.httpClient.Do(req)
	require.Error(t, err, "hardened client must reject IMDS connections")
	assert.Contains(t, err.Error(), "blocked")
}

// TestNewClientWithHTTP_NilFallbackIsHardened verifies that passing nil as the
// httpClient falls back to httpclient.New() (SSRF-hardened), not bare &http.Client{}.
func TestNewClientWithHTTP_NilFallbackIsHardened(t *testing.T) {
	c := NewClientWithHTTP(nil, "sub", "eastus", nil)
	require.NotNil(t, c.httpClient)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://169.254.169.254/metadata/instance", nil)
	require.NoError(t, err)
	_, err = c.httpClient.Do(req)
	require.Error(t, err, "nil-fallback client must also reject IMDS connections")
	assert.Contains(t, err.Error(), "blocked")
}

// TestGetOfferingDetails_NoReservationPricing verifies that when the Retail
// Prices API returns on-demand pricing but no reservation line, GetOfferingDetails
// returns an error rather than fabricating a price from a hardcoded multiplier
// (issue #1020 H4). Pre-fix this would have returned a fabricated price silently.
func TestGetOfferingDetails_NoReservationPricing(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	onDemandOnly := `{
		"Items": [
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
		],
		"NextPageLink": "",
		"Count": 1
	}`
	h.On("Do", mock.Anything).Return(mocks.CreateMockHTTPResponse(http.StatusOK, onDemandOnly), nil)
	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	_, err := c.GetOfferingDetails(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no reservation pricing found")
}

func TestNewClientWithHTTP(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	c := NewClientWithHTTP(nil, "sub-123", "eastus", h)
	require.NotNil(t, c)
	assert.Equal(t, h, c.httpClient)
}

// -- interface method tests --

func TestGetServiceType(t *testing.T) {
	c := NewClient(nil, "sub", "region")
	assert.Equal(t, common.ServiceMemoryDB, c.GetServiceType())
}

func TestGetRegion(t *testing.T) {
	for _, region := range []string{"eastus", "westeurope", "australiaeast"} {
		c := NewClient(nil, "sub", region)
		assert.Equal(t, region, c.GetRegion())
	}
}

// -- GetValidResourceTypes --

func TestGetValidResourceTypes_Fallback(t *testing.T) {
	c := NewClient(nil, "invalid-sub", "eastus")
	skus, err := c.GetValidResourceTypes(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, skus)
	assert.Contains(t, skus, "Basic_C0")
	assert.Contains(t, skus, "Standard_C1")
	assert.Contains(t, skus, "Premium_P1")
}

func TestGetValidResourceTypes_FromMockPager(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	name := armredis.SKUNamePremium
	family := armredis.SKUFamilyP
	cap := int32(2)
	c.SetRedisCachesPager(&mockRedisPager{
		pages: []armredis.ClientListBySubscriptionResponse{
			{ListResult: armredis.ListResult{Value: []*armredis.ResourceInfo{
				{Properties: &armredis.Properties{SKU: &armredis.SKU{Name: &name, Family: &family, Capacity: &cap}}},
			}}},
		},
	})
	skus, err := c.GetValidResourceTypes(context.Background())
	require.NoError(t, err)
	require.Len(t, skus, 1)
	assert.Equal(t, "Premium_P2", skus[0])
}

func TestGetValidResourceTypes_PagerError_FallsBack(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	c.SetRedisCachesPager(&mockRedisPager{
		pages: []armredis.ClientListBySubscriptionResponse{{}},
		err:   errors.New("api error"),
	})
	skus, err := c.GetValidResourceTypes(context.Background())
	require.NoError(t, err)
	assert.Contains(t, skus, "Premium_P1")
}

func TestGetValidResourceTypes_EmptyResults_FallsBack(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	c.SetRedisCachesPager(&mockRedisPager{
		pages: []armredis.ClientListBySubscriptionResponse{
			{ListResult: armredis.ListResult{Value: []*armredis.ResourceInfo{}}},
		},
	})
	skus, err := c.GetValidResourceTypes(context.Background())
	require.NoError(t, err)
	assert.Contains(t, skus, "Premium_P1")
}

func TestGetValidResourceTypes_MultipleCaches(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	pName := armredis.SKUNamePremium
	sName := armredis.SKUNameStandard
	pFam := armredis.SKUFamilyP
	cFam := armredis.SKUFamilyC
	cap1 := int32(1)
	cap2 := int32(3)
	c.SetRedisCachesPager(&mockRedisPager{
		pages: []armredis.ClientListBySubscriptionResponse{
			{ListResult: armredis.ListResult{Value: []*armredis.ResourceInfo{
				{Properties: &armredis.Properties{SKU: &armredis.SKU{Name: &pName, Family: &pFam, Capacity: &cap1}}},
				{Properties: &armredis.Properties{SKU: &armredis.SKU{Name: &sName, Family: &cFam, Capacity: &cap2}}},
			}}},
		},
	})
	skus, err := c.GetValidResourceTypes(context.Background())
	require.NoError(t, err)
	assert.Len(t, skus, 2)
	assert.Contains(t, skus, "Premium_P1")
	assert.Contains(t, skus, "Standard_C3")
}

// -- ValidateOffering --

func TestValidateOffering_ValidSKU(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	err := c.ValidateOffering(context.Background(), common.Recommendation{ResourceType: "Premium_P1"})
	assert.NoError(t, err)
}

func TestValidateOffering_InvalidSKU(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	err := c.ValidateOffering(context.Background(), common.Recommendation{ResourceType: "Bogus_Z99"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Azure Cache for Redis SKU")
}

// -- GetRecommendations --

// TestRecommendationsListArgs_UsesSubscriptionScope is the regression guard for
// the pager-construction bug: NewListPager's first argument must be the
// subscription billing scope, not the ODATA filter (the wrong shape produced a
// malformed URL that errored on every request, breaking Managed Redis recs).
// The injected mock pager bypasses NewListPager, so this asserts the args helper.
func TestRecommendationsListArgs_UsesSubscriptionScope(t *testing.T) {
	c := NewClient(nil, "sub-123", "eastus")
	scope, opts := c.recommendationsListArgs()
	assert.Equal(t, "/subscriptions/sub-123", scope,
		"first NewListPager arg must be the subscription scope, not the filter")
	require.NotNil(t, opts)
	require.NotNil(t, opts.Filter, "the ODATA filter must be passed via options.Filter")
	assert.Contains(t, *opts.Filter, "resourceType eq 'RedisCache'")
}

func TestGetRecommendations_EmptyPager(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	c.SetRecommendationsPager(&mockRecommendationsPager{
		pages: []armconsumption.ReservationRecommendationsClientListResponse{
			{ReservationRecommendationsListResult: armconsumption.ReservationRecommendationsListResult{
				Value: []armconsumption.ReservationRecommendationClassification{},
			}},
		},
	})
	recs, err := c.GetRecommendations(context.Background(), &common.RecommendationParams{})
	require.NoError(t, err)
	assert.Empty(t, recs)
}

func TestGetRecommendations_MultiplePages(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	c.SetRecommendationsPager(&mockRecommendationsPager{
		pages: []armconsumption.ReservationRecommendationsClientListResponse{
			{ReservationRecommendationsListResult: armconsumption.ReservationRecommendationsListResult{
				Value: []armconsumption.ReservationRecommendationClassification{},
			}},
			{ReservationRecommendationsListResult: armconsumption.ReservationRecommendationsListResult{
				Value: []armconsumption.ReservationRecommendationClassification{},
			}},
		},
	})
	recs, err := c.GetRecommendations(context.Background(), &common.RecommendationParams{})
	require.NoError(t, err)
	assert.Empty(t, recs)
}

// -- GetExistingCommitments --

func TestGetExistingCommitments_Empty(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	c.SetReservationsPager(&mockReservationsPager{})
	commitments, err := c.GetExistingCommitments(context.Background())
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestGetExistingCommitments_RedisSKUIncluded(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	resID := "res-123"
	sku := "redis-premium-p1"
	c.SetReservationsPager(&mockReservationsPager{
		pages: []armconsumption.ReservationsDetailsClientListResponse{
			{ReservationDetailsListResult: armconsumption.ReservationDetailsListResult{
				Value: []*armconsumption.ReservationDetail{
					{Properties: &armconsumption.ReservationDetailProperties{ReservationID: &resID, SKUName: &sku}},
				},
			}},
		},
	})
	commitments, err := c.GetExistingCommitments(context.Background())
	require.NoError(t, err)
	require.Len(t, commitments, 1)
	assert.Equal(t, resID, commitments[0].CommitmentID)
	assert.Equal(t, sku, commitments[0].ResourceType)
	assert.Equal(t, common.ServiceMemoryDB, commitments[0].Service)
	assert.Equal(t, common.ProviderAzure, commitments[0].Provider)
}

func TestGetExistingCommitments_NonRedisFiltered(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	sqlSKU := "sql-standard-s1"
	redisSKU := "redis-premium-p2"
	id1 := "res-1"
	id2 := "res-2"
	c.SetReservationsPager(&mockReservationsPager{
		pages: []armconsumption.ReservationsDetailsClientListResponse{
			{ReservationDetailsListResult: armconsumption.ReservationDetailsListResult{
				Value: []*armconsumption.ReservationDetail{
					{Properties: &armconsumption.ReservationDetailProperties{ReservationID: &id1, SKUName: &sqlSKU}},
					{Properties: &armconsumption.ReservationDetailProperties{ReservationID: &id2, SKUName: &redisSKU}},
				},
			}},
		},
	})
	commitments, err := c.GetExistingCommitments(context.Background())
	require.NoError(t, err)
	require.Len(t, commitments, 1)
	assert.Equal(t, id2, commitments[0].CommitmentID)
}

func TestGetExistingCommitments_NilProperties(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	c.SetReservationsPager(&mockReservationsPager{
		pages: []armconsumption.ReservationsDetailsClientListResponse{
			{ReservationDetailsListResult: armconsumption.ReservationDetailsListResult{
				Value: []*armconsumption.ReservationDetail{{Properties: nil}},
			}},
		},
	})
	commitments, err := c.GetExistingCommitments(context.Background())
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestGetExistingCommitments_PagerError(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	c.SetReservationsPager(&mockReservationsPager{
		pages: []armconsumption.ReservationsDetailsClientListResponse{{}},
		err:   errors.New("api error"),
	})
	_, err := c.GetExistingCommitments(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch reservations details page")
}

// -- GetOfferingDetails --

func TestGetOfferingDetails_1yr(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	h.On("Do", mock.Anything).Return(mocks.CreateMockHTTPResponse(http.StatusOK, samplePricingJSON()), nil)
	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	details, err := c.GetOfferingDetails(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr", PaymentOption: "upfront",
	})
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, "Premium_P1", details.ResourceType)
	assert.Equal(t, "1yr", details.Term)
	assert.Equal(t, "USD", details.Currency)
	assert.Contains(t, details.OfferingID, "azure-managed-redis")
}

func TestGetOfferingDetails_3yr(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	h.On("Do", mock.Anything).Return(mocks.CreateMockHTTPResponse(http.StatusOK, samplePricingJSON()), nil)
	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	details, err := c.GetOfferingDetails(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "3yr", PaymentOption: "monthly",
	})
	require.NoError(t, err)
	assert.Equal(t, "3yr", details.Term)
	assert.Equal(t, float64(0), details.UpfrontCost)
	// 3-year fixture: 900.0 reservation price over 36 months = 25.0/month.
	// Asserting the exact value confirms the 3-year branch is selected and
	// the term-specific pricing item is parsed, not the 1-year one.
	assert.InDelta(t, 25.0, details.RecurringCost, 1e-9)
	assert.InDelta(t, 900.0, details.TotalCost, 1e-9)
}

func TestGetOfferingDetails_NoUpfront(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	h.On("Do", mock.Anything).Return(mocks.CreateMockHTTPResponse(http.StatusOK, samplePricingJSON()), nil)
	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	details, err := c.GetOfferingDetails(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr", PaymentOption: "no-upfront",
	})
	require.NoError(t, err)
	assert.Equal(t, float64(0), details.UpfrontCost)
	assert.Greater(t, details.RecurringCost, float64(0))
}

func TestGetOfferingDetails_APIError(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	h.On("Do", mock.Anything).Return(mocks.CreateMockHTTPResponse(http.StatusInternalServerError, "Internal Server Error"), nil)
	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	_, err := c.GetOfferingDetails(context.Background(), common.Recommendation{ResourceType: "Premium_P1", Term: "1yr"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pricing API returned status 500")
}

func TestGetOfferingDetails_NoPricing(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	h.On("Do", mock.Anything).Return(mocks.CreateMockHTTPResponse(http.StatusOK, `{"Items": []}`), nil)
	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	_, err := c.GetOfferingDetails(context.Background(), common.Recommendation{ResourceType: "Premium_P1", Term: "1yr"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no pricing data found")
}

func TestGetOfferingDetails_Paginated(t *testing.T) {
	// Page 1: on-demand price only, with a NextPageLink pointing to page 2.
	page1 := `{
		"Items": [
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
		],
		"NextPageLink": "https://prices.azure.com/api/retail/prices?page=2",
		"Count": 1
	}`
	// Page 2: reservation price only, no further pages.
	page2 := `{
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
			}
		],
		"NextPageLink": "",
		"Count": 1
	}`
	h := &mocks.MockHTTPClient{}
	h.On("Do", mock.Anything).Return(mocks.CreateMockHTTPResponse(http.StatusOK, page1), nil).Once()
	h.On("Do", mock.Anything).Return(mocks.CreateMockHTTPResponse(http.StatusOK, page2), nil).Once()
	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	details, err := c.GetOfferingDetails(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr", PaymentOption: "upfront",
	})
	require.NoError(t, err)
	require.NotNil(t, details)
	assert.Equal(t, "USD", details.Currency)
	assert.Greater(t, details.TotalCost, float64(0))
	h.AssertExpectations(t)
}

// -- PurchaseCommitment --

// calcPriceRespJSON returns a minimal calculatePrice response JSON for tests.
func calcPriceRespJSON(orderID string) string {
	return `{"properties":{"reservationOrderId":"` + orderID + `"}}`
}

func TestPurchaseCommitment_Success(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	h.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, calcPriceRespJSON("mr-order-001")), nil).Once()
	h.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/mr-order-001/purchase"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, `{}`), nil).Once()
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr", Count: 1, CommitmentCost: 500.0,
	}, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "mr-order-001", result.CommitmentID)
	assert.Equal(t, 500.0, result.Cost)
}

func TestPurchaseCommitment_3yr(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	h.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, calcPriceRespJSON("mr-order-3yr")), nil).Once()
	h.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/mr-order-3yr/purchase"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusCreated, `{}`), nil).Once()
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P2", Term: "3yr", Count: 2, CommitmentCost: 1200.0,
	}, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "mr-order-3yr", result.CommitmentID)
}

func TestPurchaseCommitment_Accepted(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	h.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, calcPriceRespJSON("mr-order-202")), nil).Once()
	h.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/mr-order-202/purchase"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusAccepted, `{}`), nil).Once()
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr", Count: 1,
	}, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestPurchaseCommitment_TokenError(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	cred := &mockTokenCredential{err: errors.New("token error")}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr", Count: 1,
	}, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to get access token")
}

func TestPurchaseCommitment_HTTPError(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	h.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(nil, errors.New("network error")).Once()
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr", Count: 1,
	}, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "calculatePrice HTTP call")
}

func TestPurchaseCommitment_BadStatus(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	h.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, calcPriceRespJSON("mr-order-bad")), nil).Once()
	h.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/mr-order-bad/purchase"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusBadRequest, `{"error":"bad"}`), nil).Once()
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr", Count: 1,
	}, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "reservation purchase failed with status 400")
}

func TestPurchaseCommitment_InvalidTerm(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "5yr", Count: 1,
	}, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "unsupported reservation term")
}

// TestPurchaseCommitment_RequiresSource pins the dedupe guard:
// PurchaseCommitment must reject an empty opts.Source before issuing any HTTP
// call. Azure mints the reservation order ID server-side, so the
// purchase-automation tag derived from Source is the only stable dedupe
// signal CUDly controls -- proceeding without it would allow a re-driven
// purchase to create a duplicate reservation.
func TestPurchaseCommitment_RequiresSource(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr", Count: 1,
	}, common.PurchaseOptions{})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "purchase source is required")
	h.AssertNotCalled(t, "Do", mock.Anything)
}

func TestGetOfferingDetails_InvalidTerm(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	_, err := c.GetOfferingDetails(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "5yr",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported reservation term")
}

// -- setter tests --

func TestSetterMethods(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")

	recPager := &mockRecommendationsPager{}
	c.SetRecommendationsPager(recPager)
	assert.Equal(t, recPager, c.recommendationsPager)

	resPager := &mockReservationsPager{}
	c.SetReservationsPager(resPager)
	assert.Equal(t, resPager, c.reservationsPager)

	redisPager := &mockRedisPager{}
	c.SetRedisCachesPager(redisPager)
	assert.Equal(t, redisPager, c.redisCachesPager)
}

// -- commonSKUs coverage --

func TestCommonSKUs(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	skus := c.commonSKUs()
	assert.Contains(t, skus, "Basic_C0")
	assert.Contains(t, skus, "Basic_C6")
	assert.Contains(t, skus, "Standard_C0")
	assert.Contains(t, skus, "Standard_C6")
	assert.Contains(t, skus, "Premium_P1")
	assert.Contains(t, skus, "Premium_P5")
}

// -- convertRecommendation --

func TestConvertRecommendation_nil(t *testing.T) {
	c := NewClient(nil, "sub-abc", "westeurope")
	rec := c.convertRecommendation(context.Background(), nil)
	assert.Nil(t, rec, "nil input should return nil")
}

func TestConvertRecommendation_legacy(t *testing.T) {
	c := NewClient(nil, "sub-abc", "westeurope")
	azRec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("westeurope"),
		mocks.WithNormalizedSize("Premium_P1"),
		mocks.WithQuantity(2),
		mocks.WithCosts(1000.0, 700.0, 300.0),
	)
	rec := c.convertRecommendation(context.Background(), azRec)
	require.NotNil(t, rec)
	assert.Equal(t, common.ProviderAzure, rec.Provider)
	assert.Equal(t, common.ServiceMemoryDB, rec.Service)
	assert.Equal(t, "sub-abc", rec.Account)
	assert.Equal(t, "westeurope", rec.Region)
	assert.Equal(t, "Premium_P1", rec.ResourceType)
	assert.Equal(t, 2, rec.Count)
	assert.Equal(t, common.CommitmentReservedInstance, rec.CommitmentType)
	assert.Equal(t, "1yr", rec.Term)
	assert.Equal(t, "upfront", rec.PaymentOption)
	assert.InDelta(t, 1000.0, rec.OnDemandCost, 0.01)
	assert.InDelta(t, 700.0, rec.CommitmentCost, 0.01)
	assert.InDelta(t, 300.0, rec.EstimatedSavings, 0.01)
	// Covered/effective cost (paid WITH the reservation) = CommitmentCost.
	require.NotNil(t, rec.RecurringMonthlyCost)
	assert.InDelta(t, 700.0, *rec.RecurringMonthlyCost, 0.01)
}

// -- RedisPricing struct --

func TestRedisPricingStruct(t *testing.T) {
	p := RedisPricing{
		HourlyRate: 0.5, ReservationPrice: 4380.0, OnDemandPrice: 8760.0,
		Currency: "USD", SavingsPercentage: 50.0,
	}
	assert.Equal(t, 0.5, p.HourlyRate)
	assert.Equal(t, "USD", p.Currency)
	assert.Equal(t, 50.0, p.SavingsPercentage)
}

// TestPurchaseCommitment_TagInjection verifies that the purchase-automation tag
// is present in the purchase request body when opts.Source is set. The
// empty-Source path is covered separately by TestPurchaseCommitment_RequiresSource
// because the dedupe-guard now fails fast at function entry.
func TestPurchaseCommitment_TagInjection(t *testing.T) {
	const orderID = "mr-tag-test-order"
	const source = "cudly-web"

	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)

	h.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, calcPriceRespJSON(orderID)), nil).Once()

	var capturedBody []byte
	h.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		if r.URL.Path != "/providers/Microsoft.Capacity/reservationOrders/"+orderID+"/purchase" {
			return false
		}
		capturedBody, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(capturedBody))
		return true
	})).Return(mocks.CreateMockHTTPResponse(http.StatusOK, `{}`), nil).Once()

	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr", Count: 1, CommitmentCost: 500.0,
	}, common.PurchaseOptions{Source: source})
	require.NoError(t, err)
	assert.True(t, result.Success)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(capturedBody, &body))
	tags, hasTags := body["tags"].(map[string]interface{})
	require.True(t, hasTags, "tags field must be present in purchase body when Source is set")
	assert.Equal(t, source, tags[common.PurchaseTagKey], "tag value must match opts.Source")
}

// TestPurchaseCommitment_ZeroCountRejected is a regression test for M6:
// PurchaseCommitment must reject Count==0 before issuing any HTTP call.
// An Advisor recommendation with a missing qty field defaults to 0 -- without
// this guard a zero-quantity purchase would reach the Azure API and produce a
// confusing 400.
func TestPurchaseCommitment_ZeroCountRejected(t *testing.T) {
	h := &mocks.MockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr", Count: 0,
	}, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "quantity must be greater than zero")
	h.AssertNotCalled(t, "Do", mock.Anything)
}
