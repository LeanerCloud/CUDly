package api

import (
	"context"
	"errors"
	"testing"
	"time"

	armreservations "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/reservations/armreservations"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- stub Azure clients for testing ---

type stubCalcRefundClient struct {
	resp armreservations.CalculateRefundClientPostResponse
	err  error
}

func (s *stubCalcRefundClient) Post(ctx context.Context, orderID string, body armreservations.CalculateRefundRequest, opts *armreservations.CalculateRefundClientPostOptions) (armreservations.CalculateRefundClientPostResponse, error) {
	return s.resp, s.err
}

type stubReturnClient struct {
	resp armreservations.ReturnClientPostResponse
	err  error
}

func (s *stubReturnClient) Post(ctx context.Context, orderID string, body armreservations.RefundRequest, opts *armreservations.ReturnClientPostOptions) (armreservations.ReturnClientPostResponse, error) {
	return s.resp, s.err
}

// sessionReq builds a minimal request with a bearer token.
func sessionReq(token string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer " + token},
	}
}

// adminSession returns an admin session for handler tests.
func revokeAdminSession() *Session {
	return &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}
}

// withinWindowRecord returns a completed Azure purchase_history record whose
// purchase timestamp is recent enough to be within the 7-day return window.
func withinWindowRecord(purchaseID string) *config.PurchaseHistoryRecord {
	ts := time.Now().UTC().Add(-24 * time.Hour) // 24h ago -- inside the 7-day window
	return &config.PurchaseHistoryRecord{
		PurchaseID: purchaseID,
		AccountID:  "acct-1",
		Provider:   "azure",
		Service:    "compute",
		Timestamp:  ts,
		Count:      1,
		Term:       1,
		Payment:    "monthly",
	}
}

// armReservationRecord returns a record where PurchaseID is the ARM resource
// path (the form the handler parses).
func armReservationRecord() *config.PurchaseHistoryRecord {
	r := withinWindowRecord("")
	r.PurchaseID = "/providers/Microsoft.Capacity/reservationOrders/order-abc/reservations/res-xyz"
	return r
}

// --- tests ---

func TestRevokePurchase_NilAuthService(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// auth == nil: the handler must fail closed with a 403 ClientError before
	// reaching any session or store call. No mock setup needed.
	h := &Handler{auth: nil}
	_, err := h.revokePurchase(ctx, sessionReq("tok"), "pid")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
}

func TestRevokePurchase_EmptyPurchaseID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := &Handler{}
	_, err := h.revokePurchase(ctx, sessionReq("tok"), "")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

func TestRevokePurchase_PurchaseNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	adminSess := revokeAdminSession()
	mockAuth.On("ValidateSession", ctx, "tok").Return(adminSess, nil)
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, "pid-1").Return((*config.PurchaseHistoryRecord)(nil), nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq("tok"), "pid-1")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 404, ce.code)
}

func TestRevokePurchase_AlreadyRevoked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	adminSess := revokeAdminSession()
	mockAuth.On("ValidateSession", ctx, "tok").Return(adminSess, nil)

	revokedAt := time.Now().UTC().Add(-1 * time.Hour)
	r := armReservationRecord()
	r.RevokedAt = &revokedAt
	r.RevokedVia = "direct-api"
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, r.PurchaseID).Return(r, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	result, err := h.revokePurchase(ctx, sessionReq("tok"), r.PurchaseID)
	require.NoError(t, err)
	m, ok := result.(*revokePurchaseResult)
	require.True(t, ok)
	assert.Equal(t, "already_revoked", m.Status)
	assert.Equal(t, "direct-api", m.RevokedVia)
}

func TestRevokePurchase_AWSReturns422(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	adminSess := revokeAdminSession()
	mockAuth.On("ValidateSession", ctx, "tok").Return(adminSess, nil)

	r := armReservationRecord()
	r.Provider = "aws"
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, r.PurchaseID).Return(r, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq("tok"), r.PurchaseID)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 422, ce.code)
}

func TestRevokePurchase_GCPReturns422(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	adminSess := revokeAdminSession()
	mockAuth.On("ValidateSession", ctx, "tok").Return(adminSess, nil)

	r := armReservationRecord()
	r.Provider = "gcp"
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, r.PurchaseID).Return(r, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq("tok"), r.PurchaseID)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 422, ce.code)
}

func TestRevokePurchase_AzureOutsideWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	adminSess := revokeAdminSession()
	mockAuth.On("ValidateSession", ctx, "tok").Return(adminSess, nil)

	r := armReservationRecord()
	r.Timestamp = time.Now().UTC().Add(-8 * 24 * time.Hour) // 8 days ago -- window closed
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, r.PurchaseID).Return(r, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq("tok"), r.PurchaseID)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 422, ce.code)
	assert.Contains(t, ce.message, "window closed")
}

func TestRevokePurchase_AzureSuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	r := armReservationRecord()
	mockStore.On("MarkPurchaseRevoked", ctx, r.PurchaseID, mock.AnythingOfType("time.Time"), "direct-api", "").Return(nil)

	sessID := "test-session"
	calcClient := &stubCalcRefundClient{
		resp: armreservations.CalculateRefundClientPostResponse{
			CalculateRefundResponse: armreservations.CalculateRefundResponse{
				Properties: &armreservations.RefundResponseProperties{
					SessionID: &sessID,
				},
			},
		},
	}
	returnClient := &stubReturnClient{resp: armreservations.ReturnClientPostResponse{}}

	h := &Handler{config: mockStore}
	orderID := "order-abc"
	resID := "res-xyz"
	result, err := h.callAzureReturn(ctx, calcClient, returnClient, r, orderID, resID)
	require.NoError(t, err)
	m, ok := result.(*revokePurchaseResult)
	require.True(t, ok)
	assert.Equal(t, "revoked", m.Status)
	assert.Equal(t, "direct-api", m.RevokedVia)
}

func TestRevokePurchase_AzureCalcRefundClientError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	r := armReservationRecord()
	calcClient := &stubCalcRefundClient{err: errors.New("400: RefundPolicyViolated")}
	returnClient := &stubReturnClient{}

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "res-xyz")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

func TestRevokePurchase_AzureReturnClientError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	sessID := "session-1"
	calcClient := &stubCalcRefundClient{
		resp: armreservations.CalculateRefundClientPostResponse{
			CalculateRefundResponse: armreservations.CalculateRefundResponse{
				Properties: &armreservations.RefundResponseProperties{
					SessionID: &sessID,
				},
			},
		},
	}
	returnClient := &stubReturnClient{err: errors.New("500: InternalServerError")}

	r := armReservationRecord()
	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "res-xyz")
	require.Error(t, err)
	// 500 is not a client error -- expect wrapped error, not ClientError.
	_, isClient := IsClientError(err)
	assert.False(t, isClient, "server-side Azure errors should not be wrapped as 4xx ClientError")
}

// --- parseAzureReservationIDs ---

func TestParseAzureReservationIDs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		purchaseID   string
		wantOrderID  string
		wantResID    string
		wantErr      bool
	}{
		{
			name:        "full ARM path",
			purchaseID:  "/subscriptions/sub-1/providers/Microsoft.Capacity/reservationOrders/order-123/reservations/res-456",
			wantOrderID: "order-123",
			wantResID:   "res-456",
		},
		{
			name:        "no subscription prefix",
			purchaseID:  "/providers/Microsoft.Capacity/reservationOrders/order-abc/reservations/res-xyz",
			wantOrderID: "order-abc",
			wantResID:   "res-xyz",
		},
		{
			name:        "order only (no reservation segment)",
			purchaseID:  "/providers/Microsoft.Capacity/reservationOrders/order-only",
			wantOrderID: "order-only",
			wantResID:   "",
		},
		{
			name:       "unrecognised path",
			purchaseID: "some-plain-id",
			wantErr:    true,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			orderID, resID, err := parseAzureReservationIDs(tc.purchaseID)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantOrderID, orderID)
			assert.Equal(t, tc.wantResID, resID)
		})
	}
}

// --- authorizeSessionRevoke ---

func TestAuthorizeSessionRevoke_Admin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	h := &Handler{auth: mockAuth}
	adminSess := &Session{Role: "admin", UserID: "u-1"}
	r := &config.PurchaseHistoryRecord{}
	err := h.authorizeSessionRevoke(ctx, adminSess, r)
	require.NoError(t, err)
}

func TestAuthorizeSessionRevoke_RevokeAny(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockAuth.On("HasPermissionAPI", ctx, "u-1", "revoke-any", "purchases").Return(true, nil)

	h := &Handler{auth: mockAuth}
	sess := &Session{Role: "user", UserID: "u-1"}
	r := &config.PurchaseHistoryRecord{}
	err := h.authorizeSessionRevoke(ctx, sess, r)
	require.NoError(t, err)
}

func TestAuthorizeSessionRevoke_RevokeOwn_AccountAccessGranted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	accountUUID := "aaaa-1111"
	mockAuth.On("HasPermissionAPI", ctx, "u-1", "revoke-any", "purchases").Return(false, nil)
	mockAuth.On("HasPermissionAPI", ctx, "u-1", "revoke-own", "purchases").Return(true, nil)
	mockAuth.On("GetAllowedAccountsAPI", ctx, "u-1").Return([]string{accountUUID}, nil)

	h := &Handler{auth: mockAuth}
	sess := &Session{Role: "user", UserID: "u-1"}
	r := &config.PurchaseHistoryRecord{CloudAccountID: &accountUUID}
	err := h.authorizeSessionRevoke(ctx, sess, r)
	require.NoError(t, err)
}

func TestAuthorizeSessionRevoke_RevokeOwn_WrongAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	accountUUID := "aaaa-1111"
	otherUUID := "bbbb-2222"
	mockAuth.On("HasPermissionAPI", ctx, "u-1", "revoke-any", "purchases").Return(false, nil)
	mockAuth.On("HasPermissionAPI", ctx, "u-1", "revoke-own", "purchases").Return(true, nil)
	mockAuth.On("GetAllowedAccountsAPI", ctx, "u-1").Return([]string{otherUUID}, nil)

	h := &Handler{auth: mockAuth}
	sess := &Session{Role: "user", UserID: "u-1"}
	r := &config.PurchaseHistoryRecord{CloudAccountID: &accountUUID}
	err := h.authorizeSessionRevoke(ctx, sess, r)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
}

func TestAuthorizeSessionRevoke_NoPermission(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockAuth.On("HasPermissionAPI", ctx, "u-1", "revoke-any", "purchases").Return(false, nil)
	mockAuth.On("HasPermissionAPI", ctx, "u-1", "revoke-own", "purchases").Return(false, nil)

	h := &Handler{auth: mockAuth}
	sess := &Session{Role: "user", UserID: "u-1"}
	r := &config.PurchaseHistoryRecord{}
	err := h.authorizeSessionRevoke(ctx, sess, r)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
}
