package savingsplans

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/billingbenefits/armbillingbenefits"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// --- mock helpers ---

// mockListAllPager is a simple struct-based mock for SavingsPlanListAllPager.
type mockListAllPager struct {
	results   []*armbillingbenefits.SavingsPlanModel
	err       error
	pageCount int
}

func (p *mockListAllPager) More() bool { return p.pageCount == 0 }

func (p *mockListAllPager) NextPage(_ context.Context) (armbillingbenefits.SavingsPlanClientListAllResponse, error) {
	p.pageCount++
	if p.err != nil {
		return armbillingbenefits.SavingsPlanClientListAllResponse{}, p.err
	}
	return armbillingbenefits.SavingsPlanClientListAllResponse{
		SavingsPlanModelListResult: armbillingbenefits.SavingsPlanModelListResult{
			Value: p.results,
		},
	}, nil
}

// mockOrderAliasClient is a struct-based mock for SavingsPlanOrderAliasAPI.
type mockOrderAliasClient struct {
	createPoller SavingsPlanOrderAliasPoller
	createErr    error
}

func (m *mockOrderAliasClient) BeginCreate(
	_ context.Context,
	_ string,
	_ armbillingbenefits.SavingsPlanOrderAliasModel,
	_ *armbillingbenefits.SavingsPlanOrderAliasClientBeginCreateOptions,
) (SavingsPlanOrderAliasPoller, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	return m.createPoller, nil
}

// mockPoller is a struct-based mock for SavingsPlanOrderAliasPoller.
type mockPoller struct {
	resp armbillingbenefits.SavingsPlanOrderAliasClientCreateResponse
	err  error
}

func (p *mockPoller) PollUntilDone(_ context.Context, _ *PollOptions) (armbillingbenefits.SavingsPlanOrderAliasClientCreateResponse, error) {
	return p.resp, p.err
}

// mockRPValidate is a struct-based mock for RPValidateAPI.
type mockRPValidate struct {
	resp armbillingbenefits.RPClientValidatePurchaseResponse
	err  error
}

func (m *mockRPValidate) ValidatePurchase(
	_ context.Context,
	_ armbillingbenefits.SavingsPlanPurchaseValidateRequest,
	_ *armbillingbenefits.RPClientValidatePurchaseOptions,
) (armbillingbenefits.RPClientValidatePurchaseResponse, error) {
	return m.resp, m.err
}

// --- constructor tests ---

func TestNewClient(t *testing.T) {
	c := NewClient(nil, "sub-123", "eastus")
	require.NotNil(t, c)
	assert.Equal(t, "sub-123", c.subscriptionID)
	assert.Equal(t, "eastus", c.region)
	assert.NotNil(t, c.httpClient)
}

func TestGetServiceType(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	assert.Equal(t, common.ServiceSavingsPlans, c.GetServiceType())
}

func TestGetRegion(t *testing.T) {
	regions := []string{"eastus", "westeurope", "japaneast"}
	for _, r := range regions {
		c := NewClient(nil, "sub", r)
		assert.Equal(t, r, c.GetRegion())
	}
}

// --- GetRecommendations ---

func TestGetRecommendations_AlwaysEmpty(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	recs, err := c.GetRecommendations(context.Background(), common.RecommendationParams{})
	require.NoError(t, err)
	assert.Empty(t, recs)
}

// --- GetValidResourceTypes ---

func TestGetValidResourceTypes(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	types, err := c.GetValidResourceTypes(context.Background())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"Compute", "MachineLearning"}, types)
}

// --- GetExistingCommitments ---

func TestGetExistingCommitments_Empty(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	c.SetListAllPager(&mockListAllPager{results: nil})

	commitments, err := c.GetExistingCommitments(context.Background())
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestGetExistingCommitments_Happy(t *testing.T) {
	now := time.Now().UTC()
	expiry := now.Add(365 * 24 * time.Hour)
	prov := armbillingbenefits.ProvisioningStateSucceeded
	amount := 10.5
	grain := armbillingbenefits.CommitmentGrainHourly
	currCode := "USD"
	spID := "/subscriptions/sub-abc/providers/Microsoft.BillingBenefits/savingsPlanOrders/ord1/savingsPlans/sp1"
	spName := "my-savings-plan"

	sp := &armbillingbenefits.SavingsPlanModel{
		ID:   &spID,
		Name: &spName,
		Properties: &armbillingbenefits.SavingsPlanModelProperties{
			ProvisioningState: &prov,
			EffectiveDateTime: &now,
			ExpiryDateTime:    &expiry,
			Commitment: &armbillingbenefits.Commitment{
				Amount:       &amount,
				CurrencyCode: &currCode,
				Grain:        &grain,
			},
		},
	}

	c := NewClient(nil, "sub-abc", "eastus")
	c.SetListAllPager(&mockListAllPager{results: []*armbillingbenefits.SavingsPlanModel{sp}})

	commitments, err := c.GetExistingCommitments(context.Background())
	require.NoError(t, err)
	require.Len(t, commitments, 1)

	got := commitments[0]
	assert.Equal(t, common.ProviderAzure, got.Provider)
	assert.Equal(t, common.CommitmentSavingsPlan, got.CommitmentType)
	assert.Equal(t, common.ServiceSavingsPlans, got.Service)
	assert.Equal(t, spID, got.CommitmentID)
	assert.Equal(t, spName, got.ResourceType)
	assert.Equal(t, "Succeeded", got.State)
	assert.Equal(t, amount, got.Cost)
}

func TestGetExistingCommitments_NilModel(t *testing.T) {
	// A nil entry in the page should be skipped without panicking.
	c := NewClient(nil, "sub", "eastus")
	c.SetListAllPager(&mockListAllPager{results: []*armbillingbenefits.SavingsPlanModel{nil}})

	commitments, err := c.GetExistingCommitments(context.Background())
	require.NoError(t, err)
	assert.Empty(t, commitments)
}

func TestGetExistingCommitments_PagerError(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	c.SetListAllPager(&mockListAllPager{err: errors.New("api error")})

	_, err := c.GetExistingCommitments(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api error")
}

// --- PurchaseCommitment ---

func makeRec(term string) common.Recommendation {
	return common.Recommendation{
		Term:          term,
		PaymentOption: "No Upfront",
		Details: &common.SavingsPlanDetails{
			PlanType:         "Compute",
			HourlyCommitment: 1.5,
		},
	}
}

func TestPurchaseCommitment_Success(t *testing.T) {
	orderID := "/subscriptions/sub/providers/Microsoft.BillingBenefits/savingsPlanOrders/order1"
	resp := armbillingbenefits.SavingsPlanOrderAliasClientCreateResponse{
		SavingsPlanOrderAliasModel: armbillingbenefits.SavingsPlanOrderAliasModel{
			ID: &orderID,
		},
	}

	c := NewClient(nil, "sub", "eastus")
	c.SetOrderAliasClient(&mockOrderAliasClient{
		createPoller: &mockPoller{resp: resp},
	})

	result, err := c.PurchaseCommitment(context.Background(), makeRec("1yr"), common.PurchaseOptions{})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, orderID, result.CommitmentID)
}

func TestPurchaseCommitment_BeginCreateError(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	c.SetOrderAliasClient(&mockOrderAliasClient{createErr: errors.New("begin failed")})

	result, err := c.PurchaseCommitment(context.Background(), makeRec("1yr"), common.PurchaseOptions{})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "begin failed")
}

func TestPurchaseCommitment_PollError(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	c.SetOrderAliasClient(&mockOrderAliasClient{
		createPoller: &mockPoller{err: errors.New("poll failed")},
	})

	result, err := c.PurchaseCommitment(context.Background(), makeRec("3yr"), common.PurchaseOptions{})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "poll failed")
}

func TestPurchaseCommitment_WrongDetails(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	rec := common.Recommendation{Term: "1yr", Details: nil}

	result, err := c.PurchaseCommitment(context.Background(), rec, common.PurchaseOptions{})
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "invalid service details")
}

func TestPurchaseCommitment_BadTerm(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	result, err := c.PurchaseCommitment(context.Background(), makeRec("99yr"), common.PurchaseOptions{})
	require.Error(t, err)
	assert.False(t, result.Success)
}

// --- ValidateOffering ---

func TestValidateOffering_Valid(t *testing.T) {
	valid := true
	resp := armbillingbenefits.RPClientValidatePurchaseResponse{
		SavingsPlanValidateResponse: armbillingbenefits.SavingsPlanValidateResponse{
			Benefits: []*armbillingbenefits.SavingsPlanValidResponseProperty{
				{Valid: &valid},
			},
		},
	}

	c := NewClient(nil, "sub", "eastus")
	c.SetRPValidateClient(&mockRPValidate{resp: resp})

	err := c.ValidateOffering(context.Background(), makeRec("1yr"))
	assert.NoError(t, err)
}

func TestValidateOffering_Invalid(t *testing.T) {
	valid := false
	reason := "unsupported SKU"
	resp := armbillingbenefits.RPClientValidatePurchaseResponse{
		SavingsPlanValidateResponse: armbillingbenefits.SavingsPlanValidateResponse{
			Benefits: []*armbillingbenefits.SavingsPlanValidResponseProperty{
				{Valid: &valid, Reason: &reason},
			},
		},
	}

	c := NewClient(nil, "sub", "eastus")
	c.SetRPValidateClient(&mockRPValidate{resp: resp})

	err := c.ValidateOffering(context.Background(), makeRec("1yr"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported SKU")
}

func TestValidateOffering_APIError(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	c.SetRPValidateClient(&mockRPValidate{err: errors.New("rp error")})

	err := c.ValidateOffering(context.Background(), makeRec("1yr"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rp error")
}

func TestValidateOffering_WrongDetails(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	rec := common.Recommendation{Term: "1yr", Details: nil}

	err := c.ValidateOffering(context.Background(), rec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid service details")
}

// --- GetOfferingDetails ---

func TestGetOfferingDetails_1yr_NoUpfront(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	rec := common.Recommendation{
		Term:          "1yr",
		PaymentOption: "No Upfront",
		Details:       &common.SavingsPlanDetails{PlanType: "Compute", HourlyCommitment: 1.0},
	}

	details, err := c.GetOfferingDetails(context.Background(), rec)
	require.NoError(t, err)
	require.NotNil(t, details)

	assert.Equal(t, "1yr", details.Term)
	assert.Equal(t, "No Upfront", details.PaymentOption)
	assert.InDelta(t, 1.0, details.EffectiveHourlyRate, 0.001)
	assert.InDelta(t, 8760.0, details.TotalCost, 0.001)
	assert.Equal(t, 0.0, details.UpfrontCost)
	assert.InDelta(t, 1.0, details.RecurringCost, 0.001)
	assert.Equal(t, "USD", details.Currency)
}

func TestGetOfferingDetails_3yr_AllUpfront(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	rec := common.Recommendation{
		Term:          "3yr",
		PaymentOption: "All Upfront",
		Details:       &common.SavingsPlanDetails{PlanType: "Compute", HourlyCommitment: 1.0},
	}

	details, err := c.GetOfferingDetails(context.Background(), rec)
	require.NoError(t, err)
	require.NotNil(t, details)

	assert.Equal(t, "3yr", details.Term)
	assert.InDelta(t, 26280.0, details.UpfrontCost, 0.001)
	assert.Equal(t, 0.0, details.RecurringCost)
}

func TestGetOfferingDetails_5yr_AllUpfront(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	rec := common.Recommendation{
		Term:          "5yr",
		PaymentOption: "All Upfront",
		Details:       &common.SavingsPlanDetails{PlanType: "Compute", HourlyCommitment: 1.0},
	}

	details, err := c.GetOfferingDetails(context.Background(), rec)
	require.NoError(t, err)
	require.NotNil(t, details)

	assert.Equal(t, "5yr", details.Term)
	assert.InDelta(t, 43800.0, details.UpfrontCost, 0.001)
	assert.Equal(t, 0.0, details.RecurringCost)
	assert.InDelta(t, 43800.0, details.TotalCost, 0.001)
}

func TestGetOfferingDetails_BadTerm(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	rec := common.Recommendation{
		Term:          "99yr",
		PaymentOption: "No Upfront",
		Details:       &common.SavingsPlanDetails{PlanType: "Compute", HourlyCommitment: 1.0},
	}

	_, err := c.GetOfferingDetails(context.Background(), rec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported savings plan term")
}

func TestGetOfferingDetails_BadPaymentOption(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	rec := common.Recommendation{
		Term:          "1yr",
		PaymentOption: "Quarterly",
		Details:       &common.SavingsPlanDetails{PlanType: "Compute", HourlyCommitment: 1.0},
	}

	_, err := c.GetOfferingDetails(context.Background(), rec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported payment option")
}

func TestGetOfferingDetails_WrongDetails(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	rec := common.Recommendation{Term: "1yr", Details: nil}

	_, err := c.GetOfferingDetails(context.Background(), rec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid service details")
}

// --- HTTP client regression tests (issue #1021) ---

// mockHTTPClient is a testify/mock-based HTTPClient stub for savingsplans tests.
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

// TestNewClient_UsesHardenedHTTPClient verifies that NewClient installs the
// SSRF-hardened httpclient (blocks IMDS at 169.254.169.254) rather than a
// bare &http.Client{}. Pre-fix this would have passed with a bare client that
// allows IMDS connections (issue #1021 H1).
func TestNewClient_UsesHardenedHTTPClient(t *testing.T) {
	c := NewClient(nil, "sub", "eastus")
	require.NotNil(t, c.httpClient)
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

// TestFetchOnDemandRate_NotFound verifies that fetchOnDemandRate returns an
// error (not (0, nil)) when no pricing item is found. Pre-fix the function
// returned (0, nil) which conflated "not found" with "rate is zero" and would
// silently corrupt downstream savings calculations (issue #1021 H3,
// feedback_nullable_not_zero, feedback_empty_string_vs_error).
func TestFetchOnDemandRate_NotFound(t *testing.T) {
	h := &mockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	h.On("Do", mock.Anything).Return(fakeHTTPResp(http.StatusOK, `{"Items":[],"NextPageLink":""}`), nil)

	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	_, err := c.fetchOnDemandRate(context.Background(), "Compute")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no on-demand pricing found")
}

// TestFetchOnDemandRate_ReturnsFirstPositivePrice verifies the happy path:
// when at least one item with RetailPrice > 0 exists, it is returned without error.
func TestFetchOnDemandRate_ReturnsFirstPositivePrice(t *testing.T) {
	h := &mockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	body := `{
		"Items": [
			{"currencyCode":"USD","retailPrice":1.5,"unitPrice":1.5,"type":"Consumption","armSkuName":"Compute"}
		],
		"NextPageLink": ""
	}`
	h.On("Do", mock.Anything).Return(fakeHTTPResp(http.StatusOK, body), nil)

	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	rate, err := c.fetchOnDemandRate(context.Background(), "Compute")
	require.NoError(t, err)
	assert.InDelta(t, 1.5, rate, 0.001)
}

// TestFetchOnDemandRate_URLEncoding verifies that the $filter value is built
// with url.Values.Encode() so that spaces and quotes are percent-encoded rather
// than passed raw in the URL (issue #1021 H3).
func TestFetchOnDemandRate_URLEncoding(t *testing.T) {
	h := &mockHTTPClient{}
	t.Cleanup(func() { h.AssertExpectations(t) })
	// Capture the request URL and assert it is properly encoded.
	var capturedURL string
	h.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		capturedURL = req.URL.RawQuery
		return true
	})).Return(fakeHTTPResp(http.StatusOK, `{"Items":[],"NextPageLink":""}`), nil)

	c := NewClientWithHTTP(nil, "sub", "eastus", h)
	_, _ = c.fetchOnDemandRate(context.Background(), "Compute")

	// The raw query must not contain unencoded spaces or single-quotes.
	assert.NotContains(t, capturedURL, " ", "URL must not contain unencoded spaces")
	assert.NotContains(t, capturedURL, "'", "URL must not contain unencoded single-quotes")
	assert.Contains(t, capturedURL, "%24filter", "URL must contain percent-encoded $filter key")
}

// --- toAzureTerm ---

func TestToAzureTerm(t *testing.T) {
	tests := []struct {
		input    string
		expected armbillingbenefits.Term
		wantErr  bool
	}{
		{"1yr", armbillingbenefits.TermP1Y, false},
		{"1", armbillingbenefits.TermP1Y, false},
		{"P1Y", armbillingbenefits.TermP1Y, false},
		{"", armbillingbenefits.TermP1Y, false},
		{"3yr", armbillingbenefits.TermP3Y, false},
		{"3", armbillingbenefits.TermP3Y, false},
		{"P3Y", armbillingbenefits.TermP3Y, false},
		{"5yr", armbillingbenefits.TermP5Y, false},
		{"P5Y", armbillingbenefits.TermP5Y, false},
		{"99yr", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := toAzureTerm(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, got)
			}
		})
	}
}
