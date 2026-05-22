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
