package synapse

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/azure/mocks"
	"github.com/LeanerCloud/CUDly/providers/azure/services/internal/reservations"
)

// ---- credential mock -------------------------------------------------------

type mockTokenCredential struct {
	token string
	err   error
}

func (m *mockTokenCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	if m.err != nil {
		return azcore.AccessToken{}, m.err
	}
	return azcore.AccessToken{
		Token:     m.token,
		ExpiresOn: time.Now().Add(time.Hour),
	}, nil
}

// ---- HTTP client mock -------------------------------------------------------

type mockHTTPClient struct {
	mock.Mock
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*http.Response), args.Error(1)
}

func newHTTPResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}

// captureHTTPClient captures the request body on each call.
type captureHTTPClient struct {
	response *http.Response
	captured []byte
}

func (c *captureHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		c.captured = b
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
	return c.response, nil
}

// ---- pager mocks -----------------------------------------------------------

type fakeRecommendationsPager struct {
	pages []armconsumption.ReservationRecommendationsClientListResponse
	index int
}

func (m *fakeRecommendationsPager) More() bool {
	return m.index < len(m.pages)
}

func (m *fakeRecommendationsPager) NextPage(_ context.Context) (armconsumption.ReservationRecommendationsClientListResponse, error) {
	if m.index >= len(m.pages) {
		return armconsumption.ReservationRecommendationsClientListResponse{}, errors.New("no more pages")
	}
	page := m.pages[m.index]
	m.index++
	return page, nil
}

// errorRecommendationsPager returns an error on the first NextPage call.
type errorRecommendationsPager struct {
	called bool
}

func (e *errorRecommendationsPager) More() bool { return !e.called }
func (e *errorRecommendationsPager) NextPage(_ context.Context) (armconsumption.ReservationRecommendationsClientListResponse, error) {
	e.called = true
	return armconsumption.ReservationRecommendationsClientListResponse{}, errors.New("API error")
}

type fakeReservationsPager struct {
	pages []armconsumption.ReservationsDetailsClientListResponse
	index int
	err   error
}

func (m *fakeReservationsPager) More() bool {
	return m.index < len(m.pages)
}

func (m *fakeReservationsPager) NextPage(_ context.Context) (armconsumption.ReservationsDetailsClientListResponse, error) {
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

// ---- helpers ---------------------------------------------------------------

func newTestClient() *SynapseClient {
	return &SynapseClient{
		subscriptionID: "sub-123",
		region:         "eastus",
	}
}

// ---- GetServiceType / GetRegion -------------------------------------------

func TestGetServiceType(t *testing.T) {
	c := newTestClient()
	assert.Equal(t, common.ServiceDataWarehouse, c.GetServiceType())
}

func TestGetRegion(t *testing.T) {
	c := newTestClient()
	assert.Equal(t, "eastus", c.GetRegion())
}

// ---- GetValidResourceTypes ------------------------------------------------

func TestGetValidResourceTypes(t *testing.T) {
	c := newTestClient()
	skus, err := c.GetValidResourceTypes(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, skus)
	assert.Contains(t, skus, "DW100c")
	assert.Contains(t, skus, "DW30000c")
	assert.Contains(t, skus, "DW1000c")
}

// ---- GetRecommendations ---------------------------------------------------

func TestGetRecommendations_empty(t *testing.T) {
	c := newTestClient()
	c.SetRecommendationsPager(&fakeRecommendationsPager{})

	recs, err := c.GetRecommendations(context.Background(), common.RecommendationParams{})
	require.NoError(t, err)
	assert.Empty(t, recs)
}

func TestGetRecommendations_singlePage(t *testing.T) {
	c := newTestClient()

	azRec := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("eastus"),
		mocks.WithNormalizedSize("DW1000c"),
		mocks.WithQuantity(2),
		mocks.WithCosts(5000.0, 3500.0, 1500.0),
	)
	c.SetRecommendationsPager(&fakeRecommendationsPager{
		pages: []armconsumption.ReservationRecommendationsClientListResponse{
			{
				ReservationRecommendationsListResult: armconsumption.ReservationRecommendationsListResult{
					Value: []armconsumption.ReservationRecommendationClassification{azRec},
				},
			},
		},
	})

	recs, err := c.GetRecommendations(context.Background(), common.RecommendationParams{})
	require.NoError(t, err)
	require.Len(t, recs, 1)

	r := recs[0]
	assert.Equal(t, common.ServiceDataWarehouse, r.Service)
	assert.Equal(t, "DW1000c", r.ResourceType)
	assert.Equal(t, 2, r.Count)
	assert.Equal(t, common.ProviderAzure, r.Provider)
	assert.Equal(t, common.CommitmentReservedInstance, r.CommitmentType)
	assert.Equal(t, "1yr", r.Term)
	assert.Equal(t, "upfront", r.PaymentOption)
	assert.InDelta(t, 5000.0, r.OnDemandCost, 0.01)
	assert.InDelta(t, 3500.0, r.CommitmentCost, 0.01)
	assert.InDelta(t, 1500.0, r.EstimatedSavings, 0.01)
	// Covered/effective cost (paid WITH the reservation) = CommitmentCost.
	require.NotNil(t, r.RecurringMonthlyCost)
	assert.InDelta(t, 3500.0, *r.RecurringMonthlyCost, 0.01)

	details, ok := r.Details.(common.DataWarehouseDetails)
	require.True(t, ok, "Details should be DataWarehouseDetails")
	assert.Equal(t, "DW1000c", details.NodeType)
	assert.Equal(t, "dedicated-sql-pool", details.ClusterType)
}

func TestGetRecommendations_multiPage(t *testing.T) {
	c := newTestClient()

	rec1 := mocks.BuildLegacyReservationRecommendation(
		mocks.WithNormalizedSize("DW500c"),
		mocks.WithQuantity(1),
	)
	rec2 := mocks.BuildLegacyReservationRecommendation(
		mocks.WithNormalizedSize("DW2000c"),
		mocks.WithQuantity(3),
	)

	c.SetRecommendationsPager(&fakeRecommendationsPager{
		pages: []armconsumption.ReservationRecommendationsClientListResponse{
			{
				ReservationRecommendationsListResult: armconsumption.ReservationRecommendationsListResult{
					Value: []armconsumption.ReservationRecommendationClassification{rec1},
				},
			},
			{
				ReservationRecommendationsListResult: armconsumption.ReservationRecommendationsListResult{
					Value: []armconsumption.ReservationRecommendationClassification{rec2},
				},
			},
		},
	})

	recs, err := c.GetRecommendations(context.Background(), common.RecommendationParams{})
	require.NoError(t, err)
	assert.Len(t, recs, 2)
}

func TestGetRecommendations_pagerError(t *testing.T) {
	c := newTestClient()
	c.SetRecommendationsPager(&errorRecommendationsPager{})

	_, err := c.GetRecommendations(context.Background(), common.RecommendationParams{})
	assert.Error(t, err)
}

func TestGetRecommendations_regionFilter(t *testing.T) {
	c := newTestClient() // region = "eastus"

	// One rec in "eastus", one in "westus"; only the matching one should survive.
	recMatch := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("eastus"),
		mocks.WithNormalizedSize("DW500c"),
		mocks.WithQuantity(1),
	)
	recOther := mocks.BuildLegacyReservationRecommendation(
		mocks.WithRegion("westus"),
		mocks.WithNormalizedSize("DW1000c"),
		mocks.WithQuantity(2),
	)

	c.SetRecommendationsPager(&fakeRecommendationsPager{
		pages: []armconsumption.ReservationRecommendationsClientListResponse{
			{
				ReservationRecommendationsListResult: armconsumption.ReservationRecommendationsListResult{
					Value: []armconsumption.ReservationRecommendationClassification{recMatch, recOther},
				},
			},
		},
	})

	recs, err := c.GetRecommendations(context.Background(), common.RecommendationParams{})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "DW500c", recs[0].ResourceType)
	assert.Equal(t, "eastus", recs[0].Region)
}

func TestGetRecommendations_modernShape(t *testing.T) {
	c := newTestClient() // region = "eastus"

	azRec := mocks.BuildModernReservationRecommendation(
		mocks.WithModernRegion("eastus"),
		mocks.WithModernSKUName("DW2000c"),
		mocks.WithModernQuantity(3),
	)

	c.SetRecommendationsPager(&fakeRecommendationsPager{
		pages: []armconsumption.ReservationRecommendationsClientListResponse{
			{
				ReservationRecommendationsListResult: armconsumption.ReservationRecommendationsListResult{
					Value: []armconsumption.ReservationRecommendationClassification{azRec},
				},
			},
		},
	})

	recs, err := c.GetRecommendations(context.Background(), common.RecommendationParams{})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "DW2000c", recs[0].ResourceType)
	assert.Equal(t, common.ProviderAzure, recs[0].Provider)
	assert.Equal(t, common.ServiceDataWarehouse, recs[0].Service)
	assert.Equal(t, "eastus", recs[0].Region)
}

// ---- GetExistingCommitments -----------------------------------------------

func TestGetExistingCommitments_empty(t *testing.T) {
	c := newTestClient()
	c.SetReservationsPager(&fakeReservationsPager{})

	commitments, err := c.GetExistingCommitments(context.Background())
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestGetExistingCommitments_synapseSKU(t *testing.T) {
	c := newTestClient()

	skuName := "DW1000c"
	reservationID := "synapse-res-123"
	c.SetReservationsPager(&fakeReservationsPager{
		pages: []armconsumption.ReservationsDetailsClientListResponse{
			{
				ReservationDetailsListResult: armconsumption.ReservationDetailsListResult{
					Value: []*armconsumption.ReservationDetail{
						{
							Properties: &armconsumption.ReservationDetailProperties{
								SKUName:       &skuName,
								ReservationID: &reservationID,
							},
						},
					},
				},
			},
		},
	})

	commitments, err := c.GetExistingCommitments(context.Background())
	require.NoError(t, err)
	require.Len(t, commitments, 1)
	assert.Equal(t, common.ServiceDataWarehouse, commitments[0].Service)
	assert.Equal(t, "DW1000c", commitments[0].ResourceType)
	assert.Equal(t, "synapse-res-123", commitments[0].CommitmentID)
	assert.Equal(t, common.CommitmentReservedInstance, commitments[0].CommitmentType)
	assert.Equal(t, common.ProviderAzure, commitments[0].Provider)
}

func TestGetExistingCommitments_filterNonSynapse(t *testing.T) {
	c := newTestClient()

	vmSKU := "Standard_D2s_v3"
	c.SetReservationsPager(&fakeReservationsPager{
		pages: []armconsumption.ReservationsDetailsClientListResponse{
			{
				ReservationDetailsListResult: armconsumption.ReservationDetailsListResult{
					Value: []*armconsumption.ReservationDetail{
						{
							Properties: &armconsumption.ReservationDetailProperties{
								SKUName: &vmSKU,
							},
						},
					},
				},
			},
		},
	})

	commitments, err := c.GetExistingCommitments(context.Background())
	require.NoError(t, err)
	assert.Empty(t, commitments, "non-Synapse SKU should be filtered out")
}

func TestGetExistingCommitments_pagerError(t *testing.T) {
	c := newTestClient()
	c.SetReservationsPager(&fakeReservationsPager{
		pages: []armconsumption.ReservationsDetailsClientListResponse{{}},
		err:   errors.New("pager error"),
	})

	_, err := c.GetExistingCommitments(context.Background())
	assert.Error(t, err)
}

// ---- ValidateOffering -----------------------------------------------------

func TestValidateOffering_valid(t *testing.T) {
	c := newTestClient()
	rec := common.Recommendation{ResourceType: "DW1000c"}
	err := c.ValidateOffering(context.Background(), rec)
	assert.NoError(t, err)
}

func TestValidateOffering_invalid(t *testing.T) {
	c := newTestClient()
	rec := common.Recommendation{ResourceType: "not-a-synapse-sku"}
	err := c.ValidateOffering(context.Background(), rec)
	assert.Error(t, err)
}

func TestValidateOffering_caseInsensitive(t *testing.T) {
	c := newTestClient()
	// Lowercase variant of DW1000c should validate.
	rec := common.Recommendation{ResourceType: "dw1000c"}
	err := c.ValidateOffering(context.Background(), rec)
	assert.NoError(t, err)
}

func TestValidateOffering_trimsWhitespace(t *testing.T) {
	c := newTestClient()
	rec := common.Recommendation{ResourceType: "  DW1000c  "}
	err := c.ValidateOffering(context.Background(), rec)
	assert.NoError(t, err)
}

// ---- GetOfferingDetails ---------------------------------------------------

const sampleSynapsePricingJSON = `{
	"Items": [
		{
			"currencyCode": "USD",
			"retailPrice": 0.50,
			"unitPrice": 0.50,
			"armRegionName": "eastus",
			"type": "Consumption",
			"skuName": "DW100c"
		},
		{
			"currencyCode": "USD",
			"retailPrice": 2000.0,
			"armRegionName": "eastus",
			"reservationTerm": "1 Year",
			"type": "Reservation",
			"skuName": "DW100c"
		}
	],
	"NextPageLink": "",
	"Count": 2
}`

func TestGetOfferingDetails_upfront(t *testing.T) {
	mHTTP := &mockHTTPClient{}
	mHTTP.On("Do", mock.Anything).Return(newHTTPResponse(http.StatusOK, sampleSynapsePricingJSON), nil)

	c := &SynapseClient{subscriptionID: "sub-123", region: "eastus", httpClient: mHTTP}

	rec := common.Recommendation{ResourceType: "DW100c", Term: "1yr", PaymentOption: "upfront"}
	details, err := c.GetOfferingDetails(context.Background(), rec)
	require.NoError(t, err)
	assert.Equal(t, "DW100c", details.ResourceType)
	assert.Equal(t, "1yr", details.Term)
	assert.InDelta(t, 2000.0, details.UpfrontCost, 0.01)
	assert.Equal(t, 0.0, details.RecurringCost)
	assert.Equal(t, "USD", details.Currency)
}

func TestGetOfferingDetails_monthly(t *testing.T) {
	mHTTP := &mockHTTPClient{}
	mHTTP.On("Do", mock.Anything).Return(newHTTPResponse(http.StatusOK, sampleSynapsePricingJSON), nil)

	c := &SynapseClient{subscriptionID: "sub-123", region: "eastus", httpClient: mHTTP}

	rec := common.Recommendation{ResourceType: "DW100c", Term: "1yr", PaymentOption: "monthly"}
	details, err := c.GetOfferingDetails(context.Background(), rec)
	require.NoError(t, err)
	assert.Equal(t, 0.0, details.UpfrontCost)
	assert.InDelta(t, 2000.0/12.0, details.RecurringCost, 0.01)
}

func TestGetOfferingDetails_httpError(t *testing.T) {
	mHTTP := &mockHTTPClient{}
	mHTTP.On("Do", mock.Anything).Return(nil, errors.New("network error"))

	c := &SynapseClient{subscriptionID: "sub-123", region: "eastus", httpClient: mHTTP}

	rec := common.Recommendation{ResourceType: "DW100c", Term: "1yr"}
	_, err := c.GetOfferingDetails(context.Background(), rec)
	assert.Error(t, err)
}

// calcPriceRespJSON returns a minimal calculatePrice JSON response with the
// given Azure-minted reservationOrderId.
func calcPriceRespJSON(orderID string) string {
	return `{"properties":{"reservationOrderId":"` + orderID + `"}}`
}

// ---- PurchaseCommitment ---------------------------------------------------

func TestPurchaseCommitment_success(t *testing.T) {
	mHTTP := &mockHTTPClient{}
	t.Cleanup(func() { mHTTP.AssertExpectations(t) })
	mHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(newHTTPResponse(http.StatusOK, calcPriceRespJSON("syn-order-001")), nil).Once()
	mHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/syn-order-001/purchase"
	})).Return(newHTTPResponse(http.StatusOK, `{}`), nil).Once()

	cred := &mockTokenCredential{token: "test-token"}
	c := NewClientWithHTTP(cred, "sub-123", "eastus", mHTTP)

	rec := common.Recommendation{
		ResourceType:   "DW1000c",
		Term:           "1yr",
		Count:          1,
		CommitmentCost: 5000.0,
	}
	result, err := c.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "syn-order-001", result.CommitmentID)
	assert.InDelta(t, 5000.0, result.Cost, 0.01)
}

func TestPurchaseCommitment_3yrTerm(t *testing.T) {
	mHTTP := &mockHTTPClient{}
	t.Cleanup(func() { mHTTP.AssertExpectations(t) })
	mHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(newHTTPResponse(http.StatusOK, calcPriceRespJSON("syn-order-3yr")), nil).Once()
	mHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/syn-order-3yr/purchase"
	})).Return(newHTTPResponse(http.StatusAccepted, `{}`), nil).Once()

	cred := &mockTokenCredential{token: "test-token"}
	c := NewClientWithHTTP(cred, "sub-123", "eastus", mHTTP)

	rec := common.Recommendation{ResourceType: "DW500c", Term: "3yr", Count: 2, CommitmentCost: 9000.0}
	result, err := c.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestPurchaseCommitment_withSource(t *testing.T) {
	// Capture the body sent to calculatePrice to verify the automation tag is present.
	var capturedBody []byte
	mHTTP := &mockHTTPClient{}
	t.Cleanup(func() { mHTTP.AssertExpectations(t) })
	mHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Run(func(args mock.Arguments) {
		req := args.Get(0).(*http.Request)
		if req.Body != nil {
			capturedBody, _ = io.ReadAll(req.Body)
			req.Body = io.NopCloser(bytes.NewReader(capturedBody))
		}
	}).Return(newHTTPResponse(http.StatusOK, calcPriceRespJSON("syn-order-src")), nil).Once()
	mHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/syn-order-src/purchase"
	})).Return(newHTTPResponse(http.StatusCreated, `{}`), nil).Once()

	cred := &mockTokenCredential{token: "test-token"}
	c := NewClientWithHTTP(cred, "sub-123", "eastus", mHTTP)

	rec := common.Recommendation{ResourceType: "DW500c", Term: "1yr", Count: 1}
	_, err := c.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{Source: "automation"})
	require.NoError(t, err)
	assert.Contains(t, string(capturedBody), "purchase-automation")
	assert.Contains(t, string(capturedBody), "automation")
}

func TestPurchaseCommitment_apiError(t *testing.T) {
	// calculatePrice succeeds; purchase fails with a non-timeout 400.
	mHTTP := &mockHTTPClient{}
	t.Cleanup(func() { mHTTP.AssertExpectations(t) })
	mHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(newHTTPResponse(http.StatusOK, calcPriceRespJSON("syn-order-err")), nil).Once()
	mHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/syn-order-err/purchase"
	})).Return(newHTTPResponse(http.StatusBadRequest, `{"error":"bad request"}`), nil).Once()

	cred := &mockTokenCredential{token: "test-token"}
	c := NewClientWithHTTP(cred, "sub-123", "eastus", mHTTP)

	rec := common.Recommendation{ResourceType: "DW1000c", Term: "1yr", Count: 1}
	result, err := c.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
}

func TestPurchaseCommitment_httpError(t *testing.T) {
	// calculatePrice network failure.
	mHTTP := &mockHTTPClient{}
	t.Cleanup(func() { mHTTP.AssertExpectations(t) })
	mHTTP.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/calculatePrice"
	})).Return(nil, errors.New("network error")).Once()

	cred := &mockTokenCredential{token: "test-token"}
	c := NewClientWithHTTP(cred, "sub-123", "eastus", mHTTP)

	rec := common.Recommendation{ResourceType: "DW1000c", Term: "1yr", Count: 1}
	result, err := c.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "calculatePrice HTTP call")
}

func TestPurchaseCommitment_tokenError(t *testing.T) {
	cred := &mockTokenCredential{err: errors.New("token error")}
	c := NewClientWithHTTP(cred, "sub-123", "eastus", nil)

	rec := common.Recommendation{ResourceType: "DW1000c", Term: "1yr", Count: 1}
	result, err := c.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
}

// ---- convertSynapseReservation --------------------------------------------

func TestConvertSynapseReservation_nil(t *testing.T) {
	c := newTestClient()
	assert.Nil(t, c.convertSynapseReservation(nil))
}

func TestConvertSynapseReservation_nilProperties(t *testing.T) {
	c := newTestClient()
	assert.Nil(t, c.convertSynapseReservation(&armconsumption.ReservationDetail{}))
}

func TestConvertSynapseReservation_nonSynapseSKU(t *testing.T) {
	c := newTestClient()
	sku := "Premium_P1"
	assert.Nil(t, c.convertSynapseReservation(&armconsumption.ReservationDetail{
		Properties: &armconsumption.ReservationDetailProperties{SKUName: &sku},
	}))
}

func TestConvertSynapseReservation_dwSKU(t *testing.T) {
	c := newTestClient()
	sku := "DW3000c"
	resID := "res-abc"
	commitment := c.convertSynapseReservation(&armconsumption.ReservationDetail{
		Properties: &armconsumption.ReservationDetailProperties{
			SKUName:       &sku,
			ReservationID: &resID,
		},
	})
	require.NotNil(t, commitment)
	assert.Equal(t, "DW3000c", commitment.ResourceType)
	assert.Equal(t, "res-abc", commitment.CommitmentID)
	assert.Equal(t, common.ServiceDataWarehouse, commitment.Service)
}

func TestConvertSynapseReservation_scuPrefix(t *testing.T) {
	c := newTestClient()
	// SCU prefix (Spark Compute Units) should be classified as Synapse.
	sku := "SCU_Standard"
	resID := "res-scu"
	commitment := c.convertSynapseReservation(&armconsumption.ReservationDetail{
		Properties: &armconsumption.ReservationDetailProperties{
			SKUName:       &sku,
			ReservationID: &resID,
		},
	})
	require.NotNil(t, commitment)
	assert.Equal(t, "SCU_Standard", commitment.ResourceType)
}

func TestConvertSynapseReservation_scuSubstringNotMatched(t *testing.T) {
	c := newTestClient()
	// Non-Synapse SKU that merely contains "scu" as a substring must not
	// be misclassified as a Synapse reservation. Guards against the prior
	// substring-match false-positive.
	sku := "rescue_premium"
	assert.Nil(t, c.convertSynapseReservation(&armconsumption.ReservationDetail{
		Properties: &armconsumption.ReservationDetailProperties{SKUName: &sku},
	}))
}

func TestConvertSynapseReservation_nilSKUName(t *testing.T) {
	c := newTestClient()
	assert.Nil(t, c.convertSynapseReservation(&armconsumption.ReservationDetail{
		Properties: &armconsumption.ReservationDetailProperties{},
	}))
}

// ---- extractSynapsePricing ------------------------------------------------

func TestExtractSynapsePricing_1yr(t *testing.T) {
	items := []SynapseRetailPriceItem{
		{CurrencyCode: "USD", RetailPrice: 0.5, Type: "Consumption"},
		{CurrencyCode: "USD", RetailPrice: 2000.0, ReservationTerm: "1 Year", Type: "Reservation"},
		{CurrencyCode: "USD", RetailPrice: 3500.0, ReservationTerm: "3 Years", Type: "Reservation"},
	}
	onDemand, reservation, currency := extractSynapsePricing(items, 1)
	assert.InDelta(t, 0.5, onDemand, 0.01)
	assert.InDelta(t, 2000.0, reservation, 0.01)
	assert.Equal(t, "USD", currency)
}

func TestExtractSynapsePricing_3yr(t *testing.T) {
	items := []SynapseRetailPriceItem{
		{CurrencyCode: "USD", RetailPrice: 0.5, Type: "Consumption"},
		{CurrencyCode: "USD", RetailPrice: 2000.0, ReservationTerm: "1 Year", Type: "Reservation"},
		{CurrencyCode: "USD", RetailPrice: 3500.0, ReservationTerm: "3 Years", Type: "Reservation"},
	}
	onDemand, reservation, currency := extractSynapsePricing(items, 3)
	assert.InDelta(t, 0.5, onDemand, 0.01)
	assert.InDelta(t, 3500.0, reservation, 0.01)
	assert.Equal(t, "USD", currency)
}

func TestExtractSynapsePricing_noReservation(t *testing.T) {
	items := []SynapseRetailPriceItem{
		{CurrencyCode: "USD", RetailPrice: 0.5, Type: "Consumption"},
	}
	onDemand, reservation, currency := extractSynapsePricing(items, 1)
	assert.InDelta(t, 0.5, onDemand, 0.01)
	assert.Equal(t, 0.0, reservation)
	assert.Equal(t, "USD", currency)
}

// ---- parseReservationTermYears --------------------------------------------

func TestParseReservationTermYears(t *testing.T) {
	tests := []struct {
		term    string
		want    int
		wantErr bool
	}{
		{"", 1, false},
		{"1", 1, false},
		{"1yr", 1, false},
		{"1y", 1, false},
		{"1YR", 1, false},
		{"3", 3, false},
		{"3yr", 3, false},
		{"3y", 3, false},
		{"3YR", 3, false},
		{"2yr", 0, true},
		{"5yr", 0, true},
		{"P1Y", 0, true},
		{"bogus", 0, true},
	}
	for _, tc := range tests {
		got, err := reservations.ParseTermYears(tc.term)
		if tc.wantErr {
			assert.Error(t, err, "term=%q should be an error", tc.term)
		} else {
			require.NoError(t, err, "term=%q should not error", tc.term)
			assert.Equal(t, tc.want, got, "term=%q", tc.term)
		}
	}
}

// ---- PurchaseCommitment input validation ----------------------------------

func TestPurchaseCommitment_emptyResourceType(t *testing.T) {
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub-123", "eastus", nil)
	rec := common.Recommendation{ResourceType: "", Term: "1yr", Count: 1}
	result, err := c.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "resource type is required")
}

func TestPurchaseCommitment_zeroCount(t *testing.T) {
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub-123", "eastus", nil)
	rec := common.Recommendation{ResourceType: "DW1000c", Term: "1yr", Count: 0}
	result, err := c.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "quantity must be greater than zero")
}

func TestPurchaseCommitment_negativeCount(t *testing.T) {
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub-123", "eastus", nil)
	rec := common.Recommendation{ResourceType: "DW1000c", Term: "1yr", Count: -1}
	result, err := c.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
}

func TestPurchaseCommitment_unsupportedTerm(t *testing.T) {
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub-123", "eastus", nil)
	rec := common.Recommendation{ResourceType: "DW1000c", Term: "5yr", Count: 1}
	result, err := c.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{Source: common.PurchaseSourceCLI})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "unsupported reservation term")
}

// TestPurchaseCommitment_requiresSource pins the dedupe guard:
// PurchaseCommitment must reject an empty opts.Source before issuing any HTTP
// call. Azure mints the reservation order ID server-side, so the
// purchase-automation tag derived from Source is the only stable dedupe
// signal CUDly controls -- proceeding without it would allow a re-driven
// purchase to create a duplicate reservation.
func TestPurchaseCommitment_requiresSource(t *testing.T) {
	mHTTP := &mockHTTPClient{}
	t.Cleanup(func() { mHTTP.AssertExpectations(t) })
	cred := &mockTokenCredential{token: "tok"}
	c := NewClientWithHTTP(cred, "sub-123", "eastus", mHTTP)
	rec := common.Recommendation{ResourceType: "DW1000c", Term: "1yr", Count: 1}
	result, err := c.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "purchase source is required")
	mHTTP.AssertNotCalled(t, "Do", mock.Anything)
}

// ---- GetOfferingDetails: no reservation price ----------------------------

func TestGetOfferingDetails_noReservationPrice(t *testing.T) {
	onDemandOnlyJSON := `{
		"Items": [
			{
				"currencyCode": "USD",
				"retailPrice": 0.50,
				"unitPrice": 0.50,
				"armRegionName": "eastus",
				"type": "Consumption",
				"skuName": "DW100c"
			}
		],
		"NextPageLink": "",
		"Count": 1
	}`
	mHTTP := &mockHTTPClient{}
	mHTTP.On("Do", mock.Anything).Return(newHTTPResponse(http.StatusOK, onDemandOnlyJSON), nil)
	c := &SynapseClient{subscriptionID: "sub-123", region: "eastus", httpClient: mHTTP}
	rec := common.Recommendation{ResourceType: "DW100c", Term: "1yr"}
	_, err := c.GetOfferingDetails(context.Background(), rec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pricing data unavailable")
}

// ---- reservation tag application ------------------------------------------
//
// The per-service applyPurchaseAutomationTag helper was removed when tag
// application moved to reservations.ApplyPurchaseTags (issue #721, so the
// idempotency-token tag rides alongside the purchase-automation tag). The
// helper's contract is now exercised in providers/azure/services/internal/reservations
// package tests; the synapse-side coverage is via the executor-level
// PurchaseCommitment tests above.

// ---- nil HTTP client fallback ----------------------------------------------

// TestNewClientWithHTTP_NilFallbackIsHardened is a regression test for the
// codebase-review finding SEC-03 (issue #1143): when httpClient is nil, the
// httpClient falls back to httpclient.New() (SSRF-hardened), not
// http.DefaultClient, so the fallback also rejects IMDS connections.
func TestNewClientWithHTTP_NilFallbackIsHardened(t *testing.T) {
	c := NewClientWithHTTP(nil, "sub-123", "eastus", nil)
	require.NotNil(t, c.httpClient)
	require.NotEqual(t, http.DefaultClient, c.httpClient,
		"nil fallback must never be http.DefaultClient")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://169.254.169.254/metadata/instance", nil)
	require.NoError(t, err)
	_, err = c.httpClient.Do(req) //nolint:bodyclose // Do always errors here; no body to close
	require.Error(t, err, "nil-fallback client must reject IMDS connections")
	assert.Contains(t, err.Error(), "blocked")
}
