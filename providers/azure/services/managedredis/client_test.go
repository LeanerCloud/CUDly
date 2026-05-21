package managedredis

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

type mockHTTPClient struct{ mock.Mock }

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*http.Response), args.Error(1)
}

func fakeHTTPResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}

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

func TestNewClientWithHTTP(t *testing.T) {
	h := &mockHTTPClient{}
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
	err := c.ValidateOffering(nil, common.Recommendation{ResourceType: "Bogus_Z99"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Azure Cache for Redis SKU")
}

// -- GetRecommendations --

func TestGetRecommendations_EmptyPager(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	c.SetRecommendationsPager(&mockRecommendationsPager{
		pages: []armconsumption.ReservationRecommendationsClientListResponse{
			{ReservationRecommendationsListResult: armconsumption.ReservationRecommendationsListResult{
				Value: []armconsumption.ReservationRecommendationClassification{},
			}},
		},
	})
	recs, err := c.GetRecommendations(context.Background(), common.RecommendationParams{})
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
	recs, err := c.GetRecommendations(context.Background(), common.RecommendationParams{})
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
	h := &mockHTTPClient{}
	h.On("Do", mock.Anything).Return(fakeHTTPResp(http.StatusOK, samplePricingJSON()), nil)
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
	h := &mockHTTPClient{}
	h.On("Do", mock.Anything).Return(fakeHTTPResp(http.StatusOK, samplePricingJSON()), nil)
	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	details, err := c.GetOfferingDetails(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "3yr", PaymentOption: "monthly",
	})
	require.NoError(t, err)
	assert.Equal(t, "3yr", details.Term)
	assert.Equal(t, float64(0), details.UpfrontCost)
	assert.Greater(t, details.RecurringCost, float64(0))
}

func TestGetOfferingDetails_NoUpfront(t *testing.T) {
	h := &mockHTTPClient{}
	h.On("Do", mock.Anything).Return(fakeHTTPResp(http.StatusOK, samplePricingJSON()), nil)
	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	details, err := c.GetOfferingDetails(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr", PaymentOption: "no-upfront",
	})
	require.NoError(t, err)
	assert.Equal(t, float64(0), details.UpfrontCost)
	assert.Greater(t, details.RecurringCost, float64(0))
}

func TestGetOfferingDetails_APIError(t *testing.T) {
	h := &mockHTTPClient{}
	h.On("Do", mock.Anything).Return(fakeHTTPResp(http.StatusInternalServerError, "Internal Server Error"), nil)
	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	_, err := c.GetOfferingDetails(context.Background(), common.Recommendation{ResourceType: "Premium_P1", Term: "1yr"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pricing API returned status 500")
}

func TestGetOfferingDetails_NoPricing(t *testing.T) {
	h := &mockHTTPClient{}
	h.On("Do", mock.Anything).Return(fakeHTTPResp(http.StatusOK, `{"Items": []}`), nil)
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
				"reservationTerm": "1 Years",
				"type": "Reservation"
			}
		],
		"NextPageLink": "",
		"Count": 1
	}`
	h := &mockHTTPClient{}
	h.On("Do", mock.Anything).Return(fakeHTTPResp(http.StatusOK, page1), nil).Once()
	h.On("Do", mock.Anything).Return(fakeHTTPResp(http.StatusOK, page2), nil).Once()
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

func TestPurchaseCommitment_Success(t *testing.T) {
	h := &mockHTTPClient{}
	h.On("Do", mock.Anything).Return(fakeHTTPResp(http.StatusOK, `{"id":"res-123"}`), nil)
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr", Count: 1, CommitmentCost: 500.0,
	}, common.PurchaseOptions{})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.NotEmpty(t, result.CommitmentID)
	assert.Equal(t, 500.0, result.Cost)
}

func TestPurchaseCommitment_3yr(t *testing.T) {
	h := &mockHTTPClient{}
	h.On("Do", mock.Anything).Return(fakeHTTPResp(http.StatusCreated, `{"id":"res-456"}`), nil)
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P2", Term: "3yr", Count: 2, CommitmentCost: 1200.0,
	}, common.PurchaseOptions{})
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestPurchaseCommitment_Accepted(t *testing.T) {
	h := &mockHTTPClient{}
	h.On("Do", mock.Anything).Return(fakeHTTPResp(http.StatusAccepted, `{"id":"res-789"}`), nil)
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr",
	}, common.PurchaseOptions{})
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestPurchaseCommitment_TokenError(t *testing.T) {
	h := &mockHTTPClient{}
	cred := &mockTokenCredential{err: errors.New("token error")}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr",
	}, common.PurchaseOptions{})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to get access token")
}

func TestPurchaseCommitment_HTTPError(t *testing.T) {
	h := &mockHTTPClient{}
	h.On("Do", mock.Anything).Return(nil, errors.New("network error"))
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr",
	}, common.PurchaseOptions{})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "failed to purchase reservation")
}

func TestPurchaseCommitment_BadStatus(t *testing.T) {
	h := &mockHTTPClient{}
	h.On("Do", mock.Anything).Return(fakeHTTPResp(http.StatusBadRequest, `{"error":"bad"}`), nil)
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub", "eastus", h)
	result, err := c.PurchaseCommitment(context.Background(), common.Recommendation{
		ResourceType: "Premium_P1", Term: "1yr",
	}, common.PurchaseOptions{})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "reservation purchase failed with status 400")
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
	require.NotNil(t, rec.RecurringMonthlyCost)
	assert.Equal(t, 0.0, *rec.RecurringMonthlyCost)
}

// -- AzureRetailPrice struct --

func TestAzureRetailPriceStruct(t *testing.T) {
	p := AzureRetailPrice{Count: 2}
	assert.Equal(t, 2, p.Count)
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
