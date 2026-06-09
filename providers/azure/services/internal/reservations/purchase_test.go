package reservations

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// mockHTTPClient implements HTTPClient for tests.
type mockHTTPClient struct{ mock.Mock }

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*http.Response), args.Error(1)
}

func fakeResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}

const calcURL = "https://management.azure.com/providers/Microsoft.Capacity/calculatePrice?api-version=2022-11-01"
const testBody = `{"sku":{"name":"Standard_B2ats_v2"},"location":"eastus","properties":{"reservedResourceType":"VirtualMachines","quantity":1}}`

// TestDoPurchaseTwoStep_HappyPath tests successful calculatePrice->purchase flow.
func TestDoPurchaseTwoStep_HappyPath(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	// calculatePrice returns a valid order ID.
	calcResp := `{"properties":{"reservationOrderId":"azure-order-abc123","paymentSchedule":{}}}`
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.String() == calcURL
	})).Return(fakeResp(http.StatusOK, calcResp), nil).Once()

	// purchase returns 200.
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/azure-order-abc123/purchase"
	})).Return(fakeResp(http.StatusOK, `{"id":"azure-order-abc123"}`), nil).Once()

	orderID, err := DoPurchaseTwoStep(ctx, m, calcURL, []byte(testBody), "test-token")
	require.NoError(t, err)
	assert.Equal(t, "azure-order-abc123", orderID)
	m.AssertExpectations(t)
}

// TestDoPurchaseTwoStep_PurchaseAccepted tests that 202 Accepted counts as success.
func TestDoPurchaseTwoStep_PurchaseAccepted(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	calcResp := `{"properties":{"reservationOrderId":"order-202"}}`
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.String() == calcURL
	})).Return(fakeResp(http.StatusOK, calcResp), nil).Once()

	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/order-202/purchase"
	})).Return(fakeResp(http.StatusAccepted, `{}`), nil).Once()

	orderID, err := DoPurchaseTwoStep(ctx, m, calcURL, []byte(testBody), "tok")
	require.NoError(t, err)
	assert.Equal(t, "order-202", orderID)
}

// TestDoPurchaseTwoStep_SessionTimeoutThenSuccess tests that a "Session timed out"
// 400 on the purchase endpoint triggers a re-run of calculatePrice and succeeds on
// the second attempt.
func TestDoPurchaseTwoStep_SessionTimeoutThenSuccess(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	sessionTimeoutBody := `{"error":{"code":"BadRequest","message":"Session timed out - Call CalculatePrice again and provide the new Reservation Order ID for purchase"}}`

	// First calculatePrice call -- returns order ID "order-first".
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.String() == calcURL
	})).Return(fakeResp(http.StatusOK, `{"properties":{"reservationOrderId":"order-first"}}`), nil).Once()

	// First purchase call returns session timeout.
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/order-first/purchase"
	})).Return(fakeResp(http.StatusBadRequest, sessionTimeoutBody), nil).Once()

	// Second calculatePrice call -- returns order ID "order-second".
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.String() == calcURL
	})).Return(fakeResp(http.StatusOK, `{"properties":{"reservationOrderId":"order-second"}}`), nil).Once()

	// Second purchase call succeeds.
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/order-second/purchase"
	})).Return(fakeResp(http.StatusOK, `{}`), nil).Once()

	orderID, err := DoPurchaseTwoStep(ctx, m, calcURL, []byte(testBody), "tok")
	require.NoError(t, err)
	assert.Equal(t, "order-second", orderID)
	m.AssertExpectations(t)
}

// TestDoPurchaseTwoStep_CalculateFailure tests that a calculatePrice 4xx is
// returned immediately without retrying.
func TestDoPurchaseTwoStep_CalculateFailure(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	m.On("Do", mock.Anything).Return(
		fakeResp(http.StatusUnprocessableEntity, `{"error":{"code":"InvalidSKU"}}`), nil,
	).Once()

	_, err := DoPurchaseTwoStep(ctx, m, calcURL, []byte(testBody), "tok")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "calculatePrice failed with status 422")
	// Only one HTTP call should have been made (no retry for calculate failures).
	m.AssertNumberOfCalls(t, "Do", 1)
}

// TestDoPurchaseTwoStep_PurchaseNonTimeoutFailure tests that a non-session-timeout
// 4xx from the purchase endpoint is returned immediately without retrying.
func TestDoPurchaseTwoStep_PurchaseNonTimeoutFailure(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.String() == calcURL
	})).Return(fakeResp(http.StatusOK, `{"properties":{"reservationOrderId":"ord-x"}}`), nil).Once()

	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/ord-x/purchase"
	})).Return(fakeResp(http.StatusForbidden, `{"error":"Forbidden"}`), nil).Once()

	_, err := DoPurchaseTwoStep(ctx, m, calcURL, []byte(testBody), "tok")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reservation purchase failed with status 403")
	// Two calls total: one calculatePrice, one failed purchase -- no retry.
	m.AssertNumberOfCalls(t, "Do", 2)
}

// TestDoPurchaseTwoStep_CalculateHTTPError tests network-level errors on calculatePrice.
func TestDoPurchaseTwoStep_CalculateHTTPError(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	m.On("Do", mock.Anything).Return(nil, errors.New("dial tcp: connection refused")).Once()

	_, err := DoPurchaseTwoStep(ctx, m, calcURL, []byte(testBody), "tok")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "calculatePrice HTTP call")
}

// TestDoPurchaseTwoStep_PurchaseHTTPError tests network-level errors on the purchase step.
func TestDoPurchaseTwoStep_PurchaseHTTPError(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.String() == calcURL
	})).Return(fakeResp(http.StatusOK, `{"properties":{"reservationOrderId":"ord-y"}}`), nil).Once()

	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/ord-y/purchase"
	})).Return(nil, errors.New("network timeout")).Once()

	_, err := DoPurchaseTwoStep(ctx, m, calcURL, []byte(testBody), "tok")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to purchase reservation")
}

// TestDoPurchaseTwoStep_EmptyOrderID tests that calculatePrice returning an
// empty reservationOrderId is treated as an error.
func TestDoPurchaseTwoStep_EmptyOrderID(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	m.On("Do", mock.Anything).Return(
		fakeResp(http.StatusOK, `{"properties":{"reservationOrderId":""}}`), nil,
	).Once()

	_, err := DoPurchaseTwoStep(ctx, m, calcURL, []byte(testBody), "tok")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty reservationOrderId")
}

// TestIsSessionTimeout tests the session timeout classifier.
func TestIsSessionTimeout(t *testing.T) {
	assert.False(t, IsSessionTimeout(nil))
	assert.False(t, IsSessionTimeout(errors.New("some other error")))
	assert.True(t, IsSessionTimeout(errors.New("reservation purchase failed with status 400: Session timed out - Call CalculatePrice again and provide the new Reservation Order ID for purchase")))
	assert.True(t, IsSessionTimeout(errors.New("Session timed out")))
}

// TestCalculatePriceURL and TestPurchaseURL verify the URL helpers produce
// the expected endpoints.
func TestCalculatePriceURL(t *testing.T) {
	u := CalculatePriceURL()
	assert.Equal(t, "https://management.azure.com/providers/Microsoft.Capacity/calculatePrice?api-version=2022-11-01", u)
}

func TestPurchaseURL(t *testing.T) {
	u := PurchaseURL("abc-def-123")
	assert.Equal(t, "https://management.azure.com/providers/Microsoft.Capacity/reservationOrders/abc-def-123/purchase?api-version=2022-11-01", u)
}

func TestReservationOrdersListURL(t *testing.T) {
	u := ReservationOrdersListURL()
	assert.Equal(t, "https://management.azure.com/providers/Microsoft.Capacity/reservationOrders?api-version=2022-11-01", u)
}

// ---- ApplyPurchaseTags ----------------------------------------------------

// TestApplyPurchaseTags_BothTags pins the issue #721 invariant: both the
// purchase-automation tag and the cudly-idempotency-token tag must be written
// into the same tags map so the resulting Azure reservation order is BOTH
// attributable AND findable by FindReservationOrderByIdempotencyToken on a
// re-drive.
func TestApplyPurchaseTags_BothTags(t *testing.T) {
	body := map[string]interface{}{}
	ApplyPurchaseTags(body, common.PurchaseSourceWeb, "idem-tok-abc")

	tags, ok := body["tags"].(map[string]string)
	require.True(t, ok, "tags map must be present when at least one tag is supplied")
	assert.Equal(t, common.PurchaseSourceWeb, tags[common.PurchaseTagKey])
	assert.Equal(t, "idem-tok-abc", tags[common.IdempotencyTagKey])
	assert.Len(t, tags, 2, "no extra tag keys must be written")
}

func TestApplyPurchaseTags_OnlySource(t *testing.T) {
	body := map[string]interface{}{}
	ApplyPurchaseTags(body, common.PurchaseSourceCLI, "")

	tags, ok := body["tags"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, common.PurchaseSourceCLI, tags[common.PurchaseTagKey])
	_, hasIdem := tags[common.IdempotencyTagKey]
	assert.False(t, hasIdem, "idempotency tag must be absent when token is empty (CLI path)")
}

func TestApplyPurchaseTags_OnlyIdempotency(t *testing.T) {
	body := map[string]interface{}{}
	ApplyPurchaseTags(body, "", "idem-only")

	tags, ok := body["tags"].(map[string]string)
	require.True(t, ok)
	_, hasSource := tags[common.PurchaseTagKey]
	assert.False(t, hasSource, "purchase-automation tag must be absent when source is empty")
	assert.Equal(t, "idem-only", tags[common.IdempotencyTagKey])
}

// TestApplyPurchaseTags_NoTags verifies the CLI legacy shape: with both
// source and idempotency token empty the body's tags field must not even be
// present. This preserves the existing snapshot tests in the per-service
// client_test.go files that assert the absence of "tags" when source is "".
func TestApplyPurchaseTags_NoTags(t *testing.T) {
	body := map[string]interface{}{"sku": "Standard_D2s_v3"}
	ApplyPurchaseTags(body, "", "")

	_, present := body["tags"]
	assert.False(t, present, "tags must be omitted entirely when both source and token are empty")
}

// ---- FindReservationOrderByIdempotencyToken -------------------------------

// orderListJSON renders a single-page list-reservation-orders response with
// the given orders. Used by all FindReservationOrderByIdempotencyToken tests.
func orderListJSON(orders []struct {
	Name              string
	IdempotencyToken  string
	ProvisioningState string
	OtherTags         map[string]string
}, nextLink string) string {
	out := `{"value":[`
	for i, o := range orders {
		if i > 0 {
			out += ","
		}
		out += `{"name":"` + o.Name + `","tags":{`
		first := true
		if o.IdempotencyToken != "" {
			out += `"` + common.IdempotencyTagKey + `":"` + o.IdempotencyToken + `"`
			first = false
		}
		for k, v := range o.OtherTags {
			if !first {
				out += ","
			}
			out += `"` + k + `":"` + v + `"`
			first = false
		}
		out += `},"properties":{"provisioningState":"` + o.ProvisioningState + `"}}`
	}
	out += `]`
	if nextLink != "" {
		out += `,"nextLink":"` + nextLink + `"`
	}
	out += "}"
	return out
}

const listURL = "https://management.azure.com/providers/Microsoft.Capacity/reservationOrders?api-version=2022-11-01"

func TestFindReservationOrderByIdempotencyToken_Match(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	body := orderListJSON([]struct {
		Name              string
		IdempotencyToken  string
		ProvisioningState string
		OtherTags         map[string]string
	}{
		{Name: "order-other", IdempotencyToken: "other-tok", ProvisioningState: "Succeeded"},
		{Name: "order-match", IdempotencyToken: "wanted-tok", ProvisioningState: "Succeeded"},
	}, "")

	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodGet && r.URL.String() == listURL
	})).Return(fakeResp(http.StatusOK, body), nil).Once()

	orderID, found, err := FindReservationOrderByIdempotencyToken(ctx, m, "tok", "wanted-tok")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "order-match", orderID)
}

func TestFindReservationOrderByIdempotencyToken_NoMatch(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	body := orderListJSON([]struct {
		Name              string
		IdempotencyToken  string
		ProvisioningState string
		OtherTags         map[string]string
	}{
		{Name: "order-1", IdempotencyToken: "some-other-tok", ProvisioningState: "Succeeded"},
	}, "")

	m.On("Do", mock.Anything).Return(fakeResp(http.StatusOK, body), nil).Once()

	orderID, found, err := FindReservationOrderByIdempotencyToken(ctx, m, "tok", "wanted-tok")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, orderID)
}

// TestFindReservationOrderByIdempotencyToken_SkipsTerminalFailed pins the
// state filter: a cancelled/failed/expired order carrying the same idempotency
// tag MUST NOT short-circuit a legitimate fresh purchase. Mirrors the AWS EC2
// findRIByIdempotencyToken filter (state in active|payment-pending).
func TestFindReservationOrderByIdempotencyToken_SkipsTerminalFailed(t *testing.T) {
	for _, state := range []string{"Cancelled", "Failed", "Expired"} {
		t.Run(state, func(t *testing.T) {
			m := &mockHTTPClient{}
			ctx := context.Background()

			body := orderListJSON([]struct {
				Name              string
				IdempotencyToken  string
				ProvisioningState string
				OtherTags         map[string]string
			}{
				{Name: "order-dead", IdempotencyToken: "wanted-tok", ProvisioningState: state},
			}, "")

			m.On("Do", mock.Anything).Return(fakeResp(http.StatusOK, body), nil).Once()

			orderID, found, err := FindReservationOrderByIdempotencyToken(ctx, m, "tok", "wanted-tok")
			require.NoError(t, err)
			assert.False(t, found, "terminal-failed (%s) order must not short-circuit a fresh purchase", state)
			assert.Empty(t, orderID)
		})
	}
}

// TestFindReservationOrderByIdempotencyToken_AcceptsInFlightStates verifies
// that orders in non-terminal states (Succeeded, Pending, Creating,
// ConfirmedBilling) DO short-circuit the purchase: those orders are either
// already-paid-for or currently being processed and a re-drive would create
// a duplicate.
func TestFindReservationOrderByIdempotencyToken_AcceptsInFlightStates(t *testing.T) {
	for _, state := range []string{"Succeeded", "Pending", "Creating", "ConfirmedBilling", ""} {
		t.Run(state, func(t *testing.T) {
			m := &mockHTTPClient{}
			ctx := context.Background()

			body := orderListJSON([]struct {
				Name              string
				IdempotencyToken  string
				ProvisioningState string
				OtherTags         map[string]string
			}{
				{Name: "order-live", IdempotencyToken: "wanted-tok", ProvisioningState: state},
			}, "")

			m.On("Do", mock.Anything).Return(fakeResp(http.StatusOK, body), nil).Once()

			orderID, found, err := FindReservationOrderByIdempotencyToken(ctx, m, "tok", "wanted-tok")
			require.NoError(t, err)
			assert.True(t, found, "non-terminal state %q must short-circuit a re-drive", state)
			assert.Equal(t, "order-live", orderID)
		})
	}
}

func TestFindReservationOrderByIdempotencyToken_HTTPError(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	m.On("Do", mock.Anything).Return(nil, errors.New("dial tcp: connection refused")).Once()

	_, found, err := FindReservationOrderByIdempotencyToken(ctx, m, "tok", "wanted-tok")
	require.Error(t, err)
	assert.False(t, found)
	assert.Contains(t, err.Error(), "list reservation orders HTTP call")
}

func TestFindReservationOrderByIdempotencyToken_403(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	m.On("Do", mock.Anything).Return(fakeResp(http.StatusForbidden, `{"error":"insufficient permissions"}`), nil).Once()

	_, found, err := FindReservationOrderByIdempotencyToken(ctx, m, "tok", "wanted-tok")
	require.Error(t, err)
	assert.False(t, found)
	assert.Contains(t, err.Error(), "status 403")
}

func TestFindReservationOrderByIdempotencyToken_EmptyToken(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	// No HTTP call expected: empty token short-circuits at function entry.
	_, found, err := FindReservationOrderByIdempotencyToken(ctx, m, "tok", "")
	require.NoError(t, err)
	assert.False(t, found)
	m.AssertNotCalled(t, "Do", mock.Anything)
}

func TestFindReservationOrderByIdempotencyToken_PaginatedFollowsNextLink(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	nextURL := "https://management.azure.com/providers/Microsoft.Capacity/reservationOrders?api-version=2022-11-01&$skiptoken=p2"

	// Page 1: no match, but a nextLink to page 2.
	page1 := orderListJSON([]struct {
		Name              string
		IdempotencyToken  string
		ProvisioningState string
		OtherTags         map[string]string
	}{
		{Name: "order-p1", IdempotencyToken: "other-tok", ProvisioningState: "Succeeded"},
	}, nextURL)
	// Page 2: the match.
	page2 := orderListJSON([]struct {
		Name              string
		IdempotencyToken  string
		ProvisioningState string
		OtherTags         map[string]string
	}{
		{Name: "order-p2-match", IdempotencyToken: "wanted-tok", ProvisioningState: "Succeeded"},
	}, "")

	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.String() == listURL
	})).Return(fakeResp(http.StatusOK, page1), nil).Once()
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.String() == nextURL
	})).Return(fakeResp(http.StatusOK, page2), nil).Once()

	orderID, found, err := FindReservationOrderByIdempotencyToken(ctx, m, "tok", "wanted-tok")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "order-p2-match", orderID)
	m.AssertExpectations(t)
}

// ---- DoIdempotentPurchaseTwoStep -----------------------------------------

// TestDoIdempotentPurchaseTwoStep_EmptyToken_NoLookup pins the CLI legacy path:
// when no idempotency token is supplied the wrapper falls straight through to
// the raw DoPurchaseTwoStep (no list call), preserving the pre-issue-721
// behaviour for callers without an owning execution.
func TestDoIdempotentPurchaseTwoStep_EmptyToken_NoLookup(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	// Only the standard calculatePrice + purchase calls -- no list call.
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.String() == calcURL
	})).Return(fakeResp(http.StatusOK, `{"properties":{"reservationOrderId":"order-no-tok"}}`), nil).Once()
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/order-no-tok/purchase"
	})).Return(fakeResp(http.StatusOK, `{}`), nil).Once()

	orderID, err := DoIdempotentPurchaseTwoStep(ctx, m, calcURL, []byte(testBody), "tok", "")
	require.NoError(t, err)
	assert.Equal(t, "order-no-tok", orderID)
	// No GET to the list endpoint must have happened.
	m.AssertNotCalled(t, "Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodGet
	}))
}

// TestDoIdempotentPurchaseTwoStep_NoMatch_FallsThroughToPurchase verifies a
// first-time purchase: the lookup returns no match, so the wrapper proceeds
// with the two-step purchase flow as normal.
func TestDoIdempotentPurchaseTwoStep_NoMatch_FallsThroughToPurchase(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	// Step 1: list call returns empty.
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodGet && r.URL.String() == listURL
	})).Return(fakeResp(http.StatusOK, `{"value":[]}`), nil).Once()
	// Step 2: calculatePrice.
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.String() == calcURL
	})).Return(fakeResp(http.StatusOK, `{"properties":{"reservationOrderId":"order-fresh"}}`), nil).Once()
	// Step 3: purchase.
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/order-fresh/purchase"
	})).Return(fakeResp(http.StatusOK, `{}`), nil).Once()

	orderID, err := DoIdempotentPurchaseTwoStep(ctx, m, calcURL, []byte(testBody), "tok", "fresh-tok-1")
	require.NoError(t, err)
	assert.Equal(t, "order-fresh", orderID)
	m.AssertExpectations(t)
}

// TestDoIdempotentPurchaseTwoStep_Match_ShortCircuits is THE invariant test
// from issue #721: a re-driven purchase with the same idempotency token MUST
// NOT issue a second calculatePrice/purchase call. The list call finds the
// existing order and the wrapper returns it directly.
func TestDoIdempotentPurchaseTwoStep_Match_ShortCircuits(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	body := orderListJSON([]struct {
		Name              string
		IdempotencyToken  string
		ProvisioningState string
		OtherTags         map[string]string
	}{
		{Name: "order-already-bought", IdempotencyToken: "redrive-tok", ProvisioningState: "Succeeded"},
	}, "")

	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodGet && r.URL.String() == listURL
	})).Return(fakeResp(http.StatusOK, body), nil).Once()

	orderID, err := DoIdempotentPurchaseTwoStep(ctx, m, calcURL, []byte(testBody), "tok", "redrive-tok")
	require.NoError(t, err)
	assert.Equal(t, "order-already-bought", orderID)
	// CRITICAL: zero POST calls -- no calculatePrice, no purchase. The
	// regression test that proves issue #721 is fixed.
	m.AssertNotCalled(t, "Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost
	}))
	m.AssertExpectations(t)
}

// TestDoIdempotentPurchaseTwoStep_LookupFailure_DoesNotPurchase pins the
// safety contract: a failed lookup MUST NOT fall through to a purchase. If
// it did, the dedupe guard could be silently bypassed by a transient list
// failure and a re-drive would double-buy. Mirrors the EC2 safety pattern.
func TestDoIdempotentPurchaseTwoStep_LookupFailure_DoesNotPurchase(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodGet
	})).Return(fakeResp(http.StatusInternalServerError, `{"error":"upstream down"}`), nil).Once()

	_, err := DoIdempotentPurchaseTwoStep(ctx, m, calcURL, []byte(testBody), "tok", "tok-failing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "idempotency lookup failed")
	assert.Contains(t, err.Error(), "refusing to purchase")
	// No POSTs at all -- the failed list must abort the whole flow.
	m.AssertNotCalled(t, "Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost
	}))
}

// TestDoIdempotentPurchaseTwoStep_DifferentTokens_DistinctReservations verifies
// the dedupe key isolates per-token: two purchases with different tokens see
// independent lookup results and both purchase normally. testify/mock's
// Once()/.Return() ordering with multiple matchers on the same method is
// fragile across calls, so each purchase is exercised against its own freshly
// programmed mock to keep the per-call expectations clearly partitioned.
func TestDoIdempotentPurchaseTwoStep_DifferentTokens_DistinctReservations(t *testing.T) {
	ctx := context.Background()
	tok1 := common.DeriveIdempotencyToken("exec-1", 0)
	tok2 := common.DeriveIdempotencyToken("exec-1", 1)
	require.NotEqual(t, tok1, tok2, "test premise: derived tokens must differ by recIndex")

	runOne := func(t *testing.T, idemTok, mintedOrderID string) string {
		t.Helper()
		m := &mockHTTPClient{}
		// Lookup: empty (no prior order for this token).
		m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
			return r.Method == http.MethodGet && r.URL.String() == listURL
		})).Return(fakeResp(http.StatusOK, `{"value":[]}`), nil).Once()
		// calculatePrice mints the order ID.
		m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
			return r.Method == http.MethodPost && r.URL.String() == calcURL
		})).Return(fakeResp(http.StatusOK, `{"properties":{"reservationOrderId":"`+mintedOrderID+`"}}`), nil).Once()
		// purchase succeeds.
		m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
			return r.Method == http.MethodPost && r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/"+mintedOrderID+"/purchase"
		})).Return(fakeResp(http.StatusOK, `{}`), nil).Once()

		got, err := DoIdempotentPurchaseTwoStep(ctx, m, calcURL, []byte(testBody), "tok", idemTok)
		require.NoError(t, err)
		m.AssertExpectations(t)
		return got
	}

	orderID1 := runOne(t, tok1, "order-A")
	orderID2 := runOne(t, tok2, "order-B")
	assert.Equal(t, "order-A", orderID1)
	assert.Equal(t, "order-B", orderID2)
	assert.NotEqual(t, orderID1, orderID2, "distinct idempotency tokens must produce distinct reservations")
}

// TestDoIdempotentPurchaseTwoStep_PreservesTwoStepFlow verifies the two-step
// flow's session-timeout retry semantics from PR #680 still work under the
// new wrapper -- the wrapper is purely additive for the lookup, and once it
// falls through to DoPurchaseTwoStep the original retry behaviour applies.
func TestDoIdempotentPurchaseTwoStep_PreservesTwoStepFlow(t *testing.T) {
	m := &mockHTTPClient{}
	ctx := context.Background()

	sessionTimeoutBody := `{"error":{"code":"BadRequest","message":"Session timed out - Call CalculatePrice again"}}`

	// Lookup: no match.
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodGet
	})).Return(fakeResp(http.StatusOK, `{"value":[]}`), nil).Once()
	// calculatePrice #1.
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.String() == calcURL
	})).Return(fakeResp(http.StatusOK, `{"properties":{"reservationOrderId":"first"}}`), nil).Once()
	// purchase #1 returns session-timeout.
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/first/purchase"
	})).Return(fakeResp(http.StatusBadRequest, sessionTimeoutBody), nil).Once()
	// calculatePrice #2 (retry).
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.String() == calcURL
	})).Return(fakeResp(http.StatusOK, `{"properties":{"reservationOrderId":"second"}}`), nil).Once()
	// purchase #2 succeeds.
	m.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.Path == "/providers/Microsoft.Capacity/reservationOrders/second/purchase"
	})).Return(fakeResp(http.StatusOK, `{}`), nil).Once()

	orderID, err := DoIdempotentPurchaseTwoStep(ctx, m, calcURL, []byte(testBody), "tok", "retry-tok")
	require.NoError(t, err)
	assert.Equal(t, "second", orderID)
	m.AssertExpectations(t)
}

// TestParseTermYears verifies the canonical term parser (M4 regression:
// before this fix, five service clients used a literal "3yr"||"3" check that
// silently treated unrecognised terms as 1yr instead of returning an error).
func TestParseTermYears(t *testing.T) {
	tests := []struct {
		term    string
		want    int
		wantErr bool
	}{
		{"1yr", 1, false},
		{"1", 1, false},
		{"1y", 1, false},
		{"", 1, false},
		{"3yr", 3, false},
		{"3", 3, false},
		{"3y", 3, false},
		{"1YR", 1, false},   // case-insensitive
		{"3YR", 3, false},   // case-insensitive
		{" 1yr ", 1, false}, // whitespace-tolerant
		{"5yr", 0, true},    // unrecognised term must error (pre-fix: silently returned 1)
		{"P1Y", 0, true},    // ISO 8601 form not supported by this parser
		{"bogus", 0, true},
	}
	for _, tc := range tests {
		got, err := ParseTermYears(tc.term)
		if tc.wantErr {
			assert.Error(t, err, "term=%q should be an error", tc.term)
			assert.Equal(t, 0, got, "term=%q error return should be 0", tc.term)
		} else {
			require.NoError(t, err, "term=%q should not error", tc.term)
			assert.Equal(t, tc.want, got, "term=%q", tc.term)
		}
	}
}
