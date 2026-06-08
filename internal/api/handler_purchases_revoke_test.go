package api

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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
	resp  armreservations.ReturnClientPostResponse
	err   error
	calls int // incremented on each Post call; lets tests assert "was NOT called"
}

func (s *stubReturnClient) Post(ctx context.Context, orderID string, body armreservations.RefundRequest, opts *armreservations.ReturnClientPostOptions) (armreservations.ReturnClientPostResponse, error) {
	s.calls++
	return s.resp, s.err
}

// sessionReq builds a minimal request with a bearer token.
func sessionReq(token string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer " + token},
	}
}

// adminSession returns an admin session for handler tests. Uses the
// apiKeyAdminUserID sentinel so authorizeSessionRevoke short-circuits via
// the stateless-admin-key branch and no HasPermissionAPI mocks are needed.
// Group-based admin users (post-#907) go through the {admin, *} permission
// match; that path is covered by the dedicated authorizeSessionRevoke tests
// below, which assert the HasPermissionAPI call shape explicitly.
func revokeAdminSession() *Session {
	return &Session{
		UserID: apiKeyAdminUserID,
		Email:  "admin@example.com",
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
	// Pre-check: not a scheduled execution.
	mockStore.On("GetExecutionByID", ctx, "pid-1").Return((*config.PurchaseExecution)(nil), errors.New("not found"))
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
	// Pre-check: not a scheduled execution.
	mockStore.On("GetExecutionByID", ctx, r.PurchaseID).Return((*config.PurchaseExecution)(nil), errors.New("not found"))
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
	// Pre-check: not a scheduled execution.
	mockStore.On("GetExecutionByID", ctx, r.PurchaseID).Return((*config.PurchaseExecution)(nil), errors.New("not found"))
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
	// Pre-check: not a scheduled execution.
	mockStore.On("GetExecutionByID", ctx, r.PurchaseID).Return((*config.PurchaseExecution)(nil), errors.New("not found"))
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
	// Pre-check: not a scheduled execution.
	mockStore.On("GetExecutionByID", ctx, r.PurchaseID).Return((*config.PurchaseExecution)(nil), errors.New("not found"))
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
	mockStore.On("MarkPurchaseRevoked", ctx, r.PurchaseID, mock.AnythingOfType("time.Time"), "direct-api", "", mock.Anything, mock.Anything).Return(nil)

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
	result, err := h.callAzureReturn(ctx, calcClient, returnClient, r, orderID, resID, nil)
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
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	r := armReservationRecord()
	// Use a typed *azcore.ResponseError so isAzureClientError classifies it
	// correctly after the Finding #7 fix (typed check, not substring match).
	calcClient := &stubCalcRefundClient{err: &azcore.ResponseError{StatusCode: 400}}
	returnClient := &stubReturnClient{}

	h := &Handler{config: mockStore}
	_, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "res-xyz", nil)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
}

func TestRevokePurchase_AzureReturnClientError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
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
	h := &Handler{config: mockStore}
	_, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "res-xyz", nil)
	require.Error(t, err)
	// 500 is not a client error -- expect wrapped error, not ClientError.
	_, isClient := IsClientError(err)
	assert.False(t, isClient, "server-side Azure errors should not be wrapped as 4xx ClientError")
}

// TestRevokePurchase_UsesStampedWindow asserts the window check reads
// RevocationWindowClosesAt (the value stamped at purchase time, issue #290) as
// the single source of truth, not a recompute from Timestamp. A row whose
// Timestamp is recent (would pass a recompute) but whose stamped window is in
// the past must be denied -- proving the stamped column drives the decision.
func TestRevokePurchase_UsesStampedWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	mockAuth.On("ValidateSession", ctx, "tok").Return(revokeAdminSession(), nil)

	r := armReservationRecord()
	// Timestamp is recent (a Timestamp-based recompute would say "open")...
	r.Timestamp = time.Now().UTC().Add(-1 * time.Hour)
	// ...but the stamped window already closed an hour ago.
	closed := time.Now().UTC().Add(-1 * time.Hour)
	r.RevocationWindowClosesAt = &closed
	// Pre-check: not a scheduled execution.
	mockStore.On("GetExecutionByID", ctx, r.PurchaseID).Return((*config.PurchaseExecution)(nil), errors.New("not found"))
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, r.PurchaseID).Return(r, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq("tok"), r.PurchaseID)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 422, ce.code)
	assert.Contains(t, ce.message, "window closed")
}

// TestRevokePurchase_EmptyReservationIDRejected asserts callAzureReturn rejects
// an order-only ARM path (empty reservationID) up front rather than submitting
// an empty Return to Azure (issue #290 robustness gap).
func TestRevokePurchase_EmptyReservationIDRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	// No MarkPurchaseRevoked / client calls expected: must reject before any API.
	calcClient := &stubCalcRefundClient{err: errors.New("should-not-be-called")}
	returnClient := &stubReturnClient{err: errors.New("should-not-be-called")}

	r := armReservationRecord()
	h := &Handler{config: mockStore}
	_, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "", nil)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 422, ce.code)
	assert.Contains(t, ce.message, "reservation ID")
}

// --- parseAzureReservationIDs ---

func TestParseAzureReservationIDs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		purchaseID  string
		wantOrderID string
		wantResID   string
		wantErr     bool
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
	// Stateless-admin-key sentinel short-circuits via session.UserID ==
	// apiKeyAdminUserID. No HasPermissionAPI mocks required.
	adminSess := &Session{UserID: apiKeyAdminUserID}
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
	sess := &Session{UserID: "u-1"}
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
	sess := &Session{UserID: "u-1"}
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
	sess := &Session{UserID: "u-1"}
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
	sess := &Session{UserID: "u-1"}
	r := &config.PurchaseHistoryRecord{}
	err := h.authorizeSessionRevoke(ctx, sess, r)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
}

// TestAuthorizeSessionRevoke_RevokeOwn_NilAccountID verifies the fail-closed
// behaviour: a revoke-own caller must be denied when the purchase row carries
// no cloud_account_id (legacy/unscoped row), because ownership cannot be
// verified without an account association.
func TestAuthorizeSessionRevoke_RevokeOwn_NilAccountID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockAuth.On("HasPermissionAPI", ctx, "u-1", "revoke-any", "purchases").Return(false, nil)
	mockAuth.On("HasPermissionAPI", ctx, "u-1", "revoke-own", "purchases").Return(true, nil)

	h := &Handler{auth: mockAuth}
	sess := &Session{UserID: "u-1"}
	// Nil CloudAccountID: legacy row with no account association.
	r := &config.PurchaseHistoryRecord{CloudAccountID: nil}
	err := h.authorizeSessionRevoke(ctx, sess, r)
	require.Error(t, err, "revoke-own on unscoped row must be denied")
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, ce.Error(), "cannot verify ownership")
}

// --- Gmail-style pre-fire delay tests (issue #291 wave-2) ---

// scheduledExecution returns a PurchaseExecution in status=scheduled with
// ScheduledExecutionAt in the future (within the revocation window).
func scheduledExecution(executionID string, createdByUserID string) *config.PurchaseExecution {
	future := time.Now().UTC().Add(47 * time.Hour)
	ex := &config.PurchaseExecution{
		ExecutionID:          executionID,
		Status:               "scheduled",
		ScheduledExecutionAt: &future,
	}
	if createdByUserID != "" {
		ex.CreatedByUserID = &createdByUserID
	}
	return ex
}

// TestRevokePurchase_ScheduledExecution_AdminFreeCancel verifies that revoking
// a scheduled execution as admin transitions it to cancelled without any
// provider SDK call (no MarkPurchaseRevoked expected).
func TestRevokePurchase_ScheduledExecution_AdminFreeCancel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	execID := "exec-sched-1"
	adminSess := revokeAdminSession()
	mockAuth.On("ValidateSession", ctx, "tok").Return(adminSess, nil)
	exec := scheduledExecution(execID, "")
	mockStore.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	// CancelExecutionAtomic and DeleteSuppressionsByExecutionTx use mock defaults
	// (WithTx calls fn(nil), CancelExecutionAtomic returns true/"cancelled"/nil).

	h := &Handler{config: mockStore, auth: mockAuth}
	result, err := h.revokePurchase(ctx, sessionReq("tok"), execID)
	require.NoError(t, err)
	m, ok := result.(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "cancelled", m["status"])
	assert.Contains(t, m["message"], "No cloud API call")
}

// TestRevokePurchase_ScheduledExecution_WindowExpired verifies that revoking a
// scheduled execution whose ScheduledExecutionAt is in the past returns 410.
func TestRevokePurchase_ScheduledExecution_WindowExpired(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	execID := "exec-expired-1"
	adminSess := revokeAdminSession()
	mockAuth.On("ValidateSession", ctx, "tok").Return(adminSess, nil)

	past := time.Now().UTC().Add(-1 * time.Minute)
	exec := &config.PurchaseExecution{
		ExecutionID:          execID,
		Status:               "scheduled",
		ScheduledExecutionAt: &past,
	}
	mockStore.On("GetExecutionByID", ctx, execID).Return(exec, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq("tok"), execID)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 410, ce.code)
	assert.Contains(t, ce.message, "revocation window has closed")
}

// TestRevokePurchase_ScheduledExecution_CASRace verifies that a concurrent
// scheduler tick that fires the execution between our window-check SELECT and
// the CancelScheduledExecutionAtomic UPDATE is surfaced as a 410 (not a 500).
func TestRevokePurchase_ScheduledExecution_CASRace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	execID := "exec-race-1"
	adminSess := revokeAdminSession()
	mockAuth.On("ValidateSession", ctx, "tok").Return(adminSess, nil)
	exec := scheduledExecution(execID, "")
	mockStore.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	// Simulate the scheduler transitioning the row to "approved" between our
	// window-check and the CAS update (zero rows matched -> "approved").
	mockStore.On("CancelScheduledExecutionAtomic", ctx, mock.Anything, execID, mock.Anything).
		Return(false, "approved", nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq("tok"), execID)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 410, ce.code)
	assert.Contains(t, ce.message, "revocation window has closed")
}

// TestRevokePurchase_ScheduledExecution_BugReg_HappyPathCAS is the regression
// test for the migration 000066 / handler bug where the revoke-scheduled flow
// dispatched into CancelExecutionAtomic. That method's SQL guard is
// status IN ('pending','notified'), which never matches a scheduled row, so
// EVERY revoke attempt on a scheduled execution returned 410 -- including the
// happy path. Mock-default success ("true,cancelled,nil") in MockConfigStore
// hid the bug; the handler now calls CancelScheduledExecutionAtomic instead.
//
// This test pins the expected mock method explicitly with a captured assertion
// rather than the default; if a future refactor flips the call back to the
// wrong method, this expectation is unmet and AssertExpectations fails.
func TestRevokePurchase_ScheduledExecution_BugReg_HappyPathCAS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	execID := "exec-bugreg-happy"
	adminSess := revokeAdminSession()
	mockAuth.On("ValidateSession", ctx, "tok").Return(adminSess, nil)
	exec := scheduledExecution(execID, "")
	mockStore.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockStore.On("CancelScheduledExecutionAtomic", ctx, mock.Anything, execID, mock.Anything).
		Return(true, "cancelled", nil).Once()
	// Suppression cleanup must run inside the same tx as the CAS.
	mockStore.On("DeleteSuppressionsByExecutionTx", ctx, mock.Anything, execID).Return(nil).Once()
	// Negative invariant: the WRONG method must never be called for a scheduled row.
	mockStore.AssertNotCalled(t, "CancelExecutionAtomic", mock.Anything, mock.Anything, mock.Anything, mock.Anything)

	h := &Handler{config: mockStore, auth: mockAuth}
	result, err := h.revokePurchase(ctx, sessionReq("tok"), execID)
	require.NoError(t, err)
	m, ok := result.(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "cancelled", m["status"])
	assert.Contains(t, m["message"], "No cloud API call")
}

// TestRevokePurchase_ScheduledExecution_RevokeOwnCreator verifies that
// revoke-own is satisfied when the execution's CreatedByUserID matches the
// session user.
func TestRevokePurchase_ScheduledExecution_RevokeOwnCreator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	execID := "exec-own-1"
	userID := "user-abc"
	sess := &Session{UserID: userID, Email: "owner@example.com"}
	mockAuth.On("ValidateSession", ctx, "tok").Return(sess, nil)
	mockAuth.On("HasPermissionAPI", ctx, userID, "revoke-any", "purchases").Return(false, nil)
	mockAuth.On("HasPermissionAPI", ctx, userID, "revoke-own", "purchases").Return(true, nil)

	exec := scheduledExecution(execID, userID)
	mockStore.On("GetExecutionByID", ctx, execID).Return(exec, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	result, err := h.revokePurchase(ctx, sessionReq("tok"), execID)
	require.NoError(t, err)
	m, ok := result.(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "cancelled", m["status"])
}

// TestRevokePurchase_ScheduledExecution_RevokeOwnWrongCreator verifies that
// revoke-own is denied when the execution belongs to a different user.
func TestRevokePurchase_ScheduledExecution_RevokeOwnWrongCreator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	execID := "exec-notmine-1"
	userID := "user-requester"
	otherUser := "user-owner"
	sess := &Session{UserID: userID, Email: "requester@example.com"}
	mockAuth.On("ValidateSession", ctx, "tok").Return(sess, nil)
	mockAuth.On("HasPermissionAPI", ctx, userID, "revoke-any", "purchases").Return(false, nil)
	mockAuth.On("HasPermissionAPI", ctx, userID, "revoke-own", "purchases").Return(true, nil)

	exec := scheduledExecution(execID, otherUser)
	mockStore.On("GetExecutionByID", ctx, execID).Return(exec, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq("tok"), execID)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, ce.message, "cannot revoke another user")
}

// TestAuthorizeSessionRevokeExecution_Admin verifies the admin short-circuit.
func TestAuthorizeSessionRevokeExecution_Admin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	h := &Handler{auth: mockAuth}
	adminSess := &Session{UserID: apiKeyAdminUserID}
	exec := &config.PurchaseExecution{ExecutionID: "e-1"}
	err := h.authorizeSessionRevokeExecution(ctx, adminSess, exec)
	require.NoError(t, err)
}

// TestAuthorizeSessionRevokeExecution_NilCreatorDenied verifies fail-closed
// behaviour: a revoke-own caller with no CreatedByUserID on the execution is
// denied.
func TestAuthorizeSessionRevokeExecution_NilCreatorDenied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockAuth.On("HasPermissionAPI", ctx, "u-1", "revoke-any", "purchases").Return(false, nil)
	mockAuth.On("HasPermissionAPI", ctx, "u-1", "revoke-own", "purchases").Return(true, nil)

	h := &Handler{auth: mockAuth}
	sess := &Session{UserID: "u-1"}
	exec := &config.PurchaseExecution{ExecutionID: "e-1", CreatedByUserID: nil}
	err := h.authorizeSessionRevokeExecution(ctx, sess, exec)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, ce.message, "cannot revoke another user")
}

// --- Two-step quote-then-confirm: Finding #4 TOCTOU tests ---

// stubCalcRefundClientWithAmount is a CalculateRefund stub that returns a
// specified refund amount + currency.
type stubCalcRefundClientWithAmount struct {
	amount   float64
	currency string
	sessID   string
}

func (s *stubCalcRefundClientWithAmount) Post(_ context.Context, _ string, _ armreservations.CalculateRefundRequest, _ *armreservations.CalculateRefundClientPostOptions) (armreservations.CalculateRefundClientPostResponse, error) {
	return armreservations.CalculateRefundClientPostResponse{
		CalculateRefundResponse: armreservations.CalculateRefundResponse{
			Properties: &armreservations.RefundResponseProperties{
				SessionID: &s.sessID,
				BillingRefundAmount: &armreservations.Price{
					Amount:       &s.amount,
					CurrencyCode: &s.currency,
				},
			},
		},
	}, nil
}

// TestCallAzureReturn_TOCTOUDivergenceRejectedWith422 verifies that when the
// user confirmed a refund of $100.00 but Azure's CalculateRefund now quotes
// $99.00 (beyond revokeQuoteEpsilon), the call is rejected with 422 before
// the Return API is called.
func TestCallAzureReturn_TOCTOUDivergenceRejectedWith422(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	r := armReservationRecord()
	// User confirmed $100.00; Azure now quotes $99.00 (> $0.01 divergence).
	userConfirmed := 100.0
	calcClient := &stubCalcRefundClientWithAmount{amount: 99.0, currency: "USD", sessID: "s-1"}
	returnClient := &stubReturnClient{}

	h := &Handler{config: mockStore}
	_, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "res-xyz", &userConfirmed)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 422, ce.code)
	assert.Contains(t, ce.message, "refund amount diverged")
	// Return API must NOT be called — no state mutation after divergence.
	assert.Empty(t, returnClient.calls)
}

// TestCallAzureReturn_TOCTOUWithinEpsilonSucceeds verifies that a divergence
// within revokeQuoteEpsilon ($0.01) is accepted and the Return is called.
func TestCallAzureReturn_TOCTOUWithinEpsilonSucceeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	r := armReservationRecord()
	// User confirmed $100.00; Azure now quotes $100.005 (within $0.01).
	userConfirmed := 100.0
	calcClient := &stubCalcRefundClientWithAmount{amount: 100.005, currency: "USD", sessID: "s-1"}
	mockStore.On("MarkPurchaseRevoked", ctx, r.PurchaseID, mock.AnythingOfType("time.Time"), "direct-api", "", mock.Anything, mock.Anything).Return(nil)
	returnClient := &stubReturnClient{resp: armreservations.ReturnClientPostResponse{}}

	h := &Handler{config: mockStore}
	result, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "res-xyz", &userConfirmed)
	require.NoError(t, err)
	m, ok := result.(*revokePurchaseResult)
	require.True(t, ok)
	assert.Equal(t, "revoked", m.Status)
}

// TestCallAzureReturn_AuditRowPopulatedWithQuote verifies that MarkPurchaseRevoked
// is called with the non-nil calcRefundAmount and calcRefundCurrency from
// CalculateRefund, so the audit row captures the quoted values.
func TestCallAzureReturn_AuditRowPopulatedWithQuote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	r := armReservationRecord()
	calcClient := &stubCalcRefundClientWithAmount{amount: 42.50, currency: "EUR", sessID: "s-2"}
	returnClient := &stubReturnClient{resp: armreservations.ReturnClientPostResponse{}}

	// Assert MarkPurchaseRevoked receives the quote amount and currency.
	mockStore.On("MarkPurchaseRevoked", ctx, r.PurchaseID, mock.AnythingOfType("time.Time"), "direct-api", "",
		mock.MatchedBy(func(v *float64) bool { return v != nil && *v == 42.50 }),
		"EUR",
	).Return(nil)

	h := &Handler{config: mockStore}
	_, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "res-xyz", nil)
	require.NoError(t, err)
	mockStore.AssertExpectations(t)
}

// --- Finding #7: typed Azure error classification ---

// TestIsAzureClientError_SubstringFalsePositive verifies that an error whose
// string representation contains "400" (e.g. a timeout message) but is NOT an
// *azcore.ResponseError is correctly classified as a server-side (non-client)
// error. The old substring-match approach would have misclassified this.
func TestIsAzureClientError_SubstringFalsePositive(t *testing.T) {
	t.Parallel()
	// An error whose message contains "400" but is just a plain error.
	err := errors.New("timeout after 400ms waiting for connection")
	assert.False(t, isAzureClientError(err),
		"a plain error containing '400' in its message should NOT be a client error")
}

// TestIsAzureClientError_TypedResponseError verifies that a real
// *azcore.ResponseError with a 4xx status code is correctly classified as a
// client error.
func TestIsAzureClientError_TypedResponseError(t *testing.T) {
	t.Parallel()
	for _, code := range []int{400, 403, 404, 409, 422} {
		code := code
		t.Run(fmt.Sprintf("HTTP%d", code), func(t *testing.T) {
			t.Parallel()
			err := &azcore.ResponseError{StatusCode: code}
			assert.True(t, isAzureClientError(err), "HTTP %d should be a client error", code)
		})
	}

	// 5xx must not be classified as a client error.
	for _, code := range []int{500, 502, 503} {
		code := code
		t.Run(fmt.Sprintf("HTTP%d_not_client", code), func(t *testing.T) {
			t.Parallel()
			err := &azcore.ResponseError{StatusCode: code}
			assert.False(t, isAzureClientError(err), "HTTP %d should NOT be a client error", code)
		})
	}
}

// --- Finding #6: partial-success reconciliation (RECONCILE_PENDING 207 path) ---

// TestCallAzureReturn_MarkPurchaseRevokedFailAllRetries verifies that when
// MarkPurchaseRevoked fails on all attempts, callAzureReturn returns a
// revokeReconcilePendingResult (207 Multi-Status body) rather than an error,
// so the frontend does not offer a retry button (which would hit Azure's
// "already returned" error).
func TestCallAzureReturn_MarkPurchaseRevokedFailAllRetries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	// Override revokeMarkRetryBackoffs to zero duration so the test doesn't actually sleep.
	orig := revokeMarkRetryBackoffs
	revokeMarkRetryBackoffs = []time.Duration{0, 0, 0}
	t.Cleanup(func() { revokeMarkRetryBackoffs = orig })

	r := armReservationRecord()
	calcClient := &stubCalcRefundClientWithAmount{amount: 10.0, currency: "USD", sessID: "s-fail"}
	returnClient := &stubReturnClient{resp: armreservations.ReturnClientPostResponse{}}

	// MarkPurchaseRevoked is called 1 + len(backoffs) = 4 times total (initial + 3 retries).
	mockStore.On("MarkPurchaseRevoked", ctx, r.PurchaseID, mock.AnythingOfType("time.Time"), "direct-api", "", mock.Anything, mock.Anything).
		Return(errors.New("db down")).Times(4)

	h := &Handler{config: mockStore}
	result, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "res-xyz", nil)
	require.NoError(t, err, "a DB failure after Azure success should not surface as an error")

	pending, ok := result.(*revokeReconcilePendingResult)
	require.True(t, ok, "expected revokeReconcilePendingResult when all retries fail")
	assert.Equal(t, "RECONCILE_PENDING", pending.Code)
	assert.True(t, pending.AzureReturned)
	mockStore.AssertExpectations(t)
}

// TestLoadAndRevokePurchaseHistory_RevocationInFlightReturns207 verifies that
// when GetPurchaseHistoryByPurchaseID returns a row with revocation_in_flight=true
// and revoked_at=nil, the endpoint returns 207 RECONCILE_PENDING rather than
// re-attempting the Azure Return (which would fail "already returned").
func TestLoadAndRevokePurchaseHistory_RevocationInFlightReturns207(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	mockAuth.On("ValidateSession", ctx, "tok").Return(revokeAdminSession(), nil)

	r := armReservationRecord()
	r.RevocationInFlight = true
	r.RevokedAt = nil

	mockStore.On("GetExecutionByID", ctx, r.PurchaseID).Return((*config.PurchaseExecution)(nil), errors.New("not found"))
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, r.PurchaseID).Return(r, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	result, err := h.revokePurchase(ctx, sessionReq("tok"), r.PurchaseID)
	require.NoError(t, err)

	pending, ok := result.(*revokeReconcilePendingResult)
	require.True(t, ok, "expected revokeReconcilePendingResult for in-flight row")
	assert.Equal(t, "RECONCILE_PENDING", pending.Code)
	assert.True(t, pending.AzureReturned)
}
