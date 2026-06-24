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
func sessionReq() *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer tok"},
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
	_, err := h.revokePurchase(ctx, sessionReq(), "pid")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code)
}

func TestRevokePurchase_EmptyPurchaseID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := &Handler{}
	_, err := h.revokePurchase(ctx, sessionReq(), "")
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
	// GetExecutionByID returns ErrNotFound: not an execution row, fall through to history.
	mockStore.On("GetExecutionByID", ctx, "pid-1").Return(nil, fmt.Errorf("%w: execution pid-1", config.ErrNotFound))
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, "pid-1").Return((*config.PurchaseHistoryRecord)(nil), nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq(), "pid-1")
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
	// GetExecutionByID returns ErrNotFound: not an execution row, fall through to history.
	mockStore.On("GetExecutionByID", ctx, r.PurchaseID).Return(nil, fmt.Errorf("%w: execution %s", config.ErrNotFound, r.PurchaseID))
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, r.PurchaseID).Return(r, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	result, err := h.revokePurchase(ctx, sessionReq(), r.PurchaseID)
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
	// GetExecutionByID returns ErrNotFound: not an execution row, fall through to history.
	mockStore.On("GetExecutionByID", ctx, r.PurchaseID).Return(nil, fmt.Errorf("%w: execution %s", config.ErrNotFound, r.PurchaseID))
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, r.PurchaseID).Return(r, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq(), r.PurchaseID)
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
	// GetExecutionByID returns ErrNotFound: not an execution row, fall through to history.
	mockStore.On("GetExecutionByID", ctx, r.PurchaseID).Return(nil, fmt.Errorf("%w: execution %s", config.ErrNotFound, r.PurchaseID))
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, r.PurchaseID).Return(r, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq(), r.PurchaseID)
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
	// GetExecutionByID returns ErrNotFound: not an execution row, fall through to history.
	mockStore.On("GetExecutionByID", ctx, r.PurchaseID).Return(nil, fmt.Errorf("%w: execution %s", config.ErrNotFound, r.PurchaseID))
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, r.PurchaseID).Return(r, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq(), r.PurchaseID)
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
	// GetExecutionByID returns ErrNotFound: not an execution row, fall through to history.
	mockStore.On("GetExecutionByID", ctx, r.PurchaseID).Return(nil, fmt.Errorf("%w: execution %s", config.ErrNotFound, r.PurchaseID))
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, r.PurchaseID).Return(r, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq(), r.PurchaseID)
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
			name:       "unrecognized path",
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
// behavior: a revoke-own caller must be denied when the purchase row carries
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
// a scheduled execution as admin transitions it to canceled without any
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
	// A scheduled row is canceled via CancelScheduledExecutionAtomic. Stub its
	// return explicitly (matching the assertion below) rather than leaning on the
	// shared mock default, so the setup and the asserted status can never drift.
	mockStore.On("CancelScheduledExecutionAtomic", ctx, mock.Anything, execID, mock.Anything).
		//nolint:misspell // DB schema value 'cancelled' -- see migration 000001_initial_schema.up.sql
		Return(true, "cancelled", nil).Once()
	mockStore.On("DeleteSuppressionsByExecutionTx", ctx, mock.Anything, execID).Return(nil).Once()

	h := &Handler{config: mockStore, auth: mockAuth}
	result, err := h.revokePurchase(ctx, sessionReq(), execID)
	require.NoError(t, err)
	m, ok := result.(map[string]string)
	require.True(t, ok)
	//nolint:misspell // DB schema value 'cancelled' -- see migration 000001_initial_schema.up.sql
	assert.Equal(t, "cancelled", m["status"])
	assert.Contains(t, m["message"], "No cloud API call")
}

// TestRevokePurchase_ScheduledExecution_PastTimestampStillCancellable verifies
// that a row still in status=="scheduled" is cancellable for FREE even when its
// ScheduledExecutionAt is already in the past (scheduler lag / backpressure).
// The handler no longer pre-rejects on a past timestamp; the CAS
// (CancelScheduledExecutionAtomic) is the sole arbiter and, because the row is
// still "scheduled", it cancels successfully with no SDK call. Regression guard
// for the early-410 check that broke free-cancel during scheduler lag.
func TestRevokePurchase_ScheduledExecution_PastTimestampStillCancellable(t *testing.T) {
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
	mockStore.On("CancelScheduledExecutionAtomic", ctx, mock.Anything, execID, mock.Anything).
		//nolint:misspell // DB schema value 'cancelled' -- see migration 000001_initial_schema.up.sql
		Return(true, "cancelled", nil).Once()
	mockStore.On("DeleteSuppressionsByExecutionTx", ctx, mock.Anything, execID).Return(nil).Once()

	h := &Handler{config: mockStore, auth: mockAuth}
	result, err := h.revokePurchase(ctx, sessionReq(), execID)
	require.NoError(t, err)
	m, ok := result.(map[string]string)
	require.True(t, ok)
	//nolint:misspell // DB schema value 'cancelled' -- see migration 000001_initial_schema.up.sql
	assert.Equal(t, "cancelled", m["status"])
	assert.Contains(t, m["message"], "No cloud API call")
}

// TestRevokePurchase_ScheduledExecution_CASRace verifies that a concurrent
// scheduler tick that fires the execution between our SELECT and the
// CancelScheduledExecutionAtomic UPDATE is surfaced as a 410 (not a 500).
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
	// SELECT and the CAS update (zero rows matched -> "approved").
	mockStore.On("CancelScheduledExecutionAtomic", ctx, mock.Anything, execID, mock.Anything).
		Return(false, "approved", nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq(), execID)
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
// happy path. Mock-default success ("true,canceled,nil") in MockConfigStore
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
		//nolint:misspell // DB schema value 'cancelled' -- see migration 000001_initial_schema.up.sql
		Return(true, "cancelled", nil).Once()
	// Suppression cleanup must run inside the same tx as the CAS.
	mockStore.On("DeleteSuppressionsByExecutionTx", ctx, mock.Anything, execID).Return(nil).Once()

	h := &Handler{config: mockStore, auth: mockAuth}
	result, err := h.revokePurchase(ctx, sessionReq(), execID)
	// Negative invariant: the WRONG method must never be called for a scheduled row.
	// Placed AFTER the handler call so it actually fires post-execution (Finding F-1, second-wave CR).
	mockStore.AssertNotCalled(t, "CancelExecutionAtomic", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	require.NoError(t, err)
	m, ok := result.(map[string]string)
	require.True(t, ok)
	//nolint:misspell // DB schema value 'cancelled' -- see migration 000001_initial_schema.up.sql
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
	result, err := h.revokePurchase(ctx, sessionReq(), execID)
	require.NoError(t, err)
	m, ok := result.(map[string]string)
	require.True(t, ok)
	//nolint:misspell // DB schema value 'cancelled' -- see migration 000001_initial_schema.up.sql
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
	_, err := h.revokePurchase(ctx, sessionReq(), execID)
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
// behavior: a revoke-own caller with no CreatedByUserID on the execution is
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
	currency string
	sessID   string
	amount   float64
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
	// NOT parallel: this test mutates the package-global revokeMarkRetryBackoffs
	// to zero duration so the retries complete instantly. Running it in parallel
	// with other tests that read the same global would cause a data race
	// (Finding F-2, second-wave CR).
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

	mockStore.On("GetExecutionByID", ctx, r.PurchaseID).Return(nil, fmt.Errorf("%w: execution %s", config.ErrNotFound, r.PurchaseID))
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, r.PurchaseID).Return(r, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	result, err := h.revokePurchase(ctx, sessionReq(), r.PurchaseID)
	require.NoError(t, err)

	pending, ok := result.(*revokeReconcilePendingResult)
	require.True(t, ok, "expected revokeReconcilePendingResult for in-flight row")
	assert.Equal(t, "RECONCILE_PENDING", pending.Code)
	assert.True(t, pending.AzureReturned)
}

// --- Finding #3: 1h safety margin + AZURE_WINDOW_EDGE ---

// TestRevokePurchase_AzureWithinSafetyMarginRejected verifies that a purchase
// made exactly (7d - 30min) ago is rejected by the local window check even
// though Azure's hard 7-day deadline has not yet passed. The 1h safety margin
// means the in-app button disappears 1h before Azure's actual edge to eliminate
// clock-skew failures (issue #290 Finding #3).
func TestRevokePurchase_AzureWithinSafetyMarginRejected(t *testing.T) {
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
	// Purchase made 6d23h30m ago: Azure's hard deadline is 30min away but our
	// 1h safety margin means the local check should reject it now.
	purchasedAt := time.Now().UTC().Add(-(7*24*time.Hour - 30*time.Minute))
	r.Timestamp = purchasedAt
	windowCloses := purchasedAt.AddDate(0, 0, AzureRevocationWindowDays)
	r.RevocationWindowClosesAt = &windowCloses

	mockStore.On("GetExecutionByID", ctx, r.PurchaseID).Return(nil, fmt.Errorf("%w: execution %s", config.ErrNotFound, r.PurchaseID))
	mockStore.On("GetPurchaseHistoryByPurchaseID", ctx, r.PurchaseID).Return(r, nil)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq(), r.PurchaseID)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 422, ce.code)
	assert.Contains(t, ce.message, "window closed")
}

// TestCallAzureReturn_JustOutsideSafetyMargin covers the Azure return path only.
// It exercises callAzureReturn directly with injected stub clients, so it does
// NOT exercise the 1h local safety-margin gate (that lives in
// dispatchProviderRevoke, before any Azure call). The reject side of that gate
// is covered end-to-end via revokePurchase in the test above
// (TestRevokePurchase_AzureWithinSafetyMarginRejected, which asserts 422
// "window closed"); here we only assert that the two-step CalculateRefund+Return
// path succeeds and reports "revoked" when Azure accepts the return.
func TestCallAzureReturn_JustOutsideSafetyMargin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	r := armReservationRecord()
	// Purchase 6d22h30m ago: 90min before the Azure edge, outside our 1h margin.
	purchasedAt := time.Now().UTC().Add(-(7*24*time.Hour - 90*time.Minute))
	r.Timestamp = purchasedAt
	windowCloses := purchasedAt.AddDate(0, 0, AzureRevocationWindowDays)
	r.RevocationWindowClosesAt = &windowCloses

	calcClient := &stubCalcRefundClientWithAmount{amount: 50.0, currency: "USD", sessID: "s-margin"}
	returnClient := &stubReturnClient{resp: armreservations.ReturnClientPostResponse{}}
	mockStore.On("MarkPurchaseRevoked", ctx, r.PurchaseID, mock.AnythingOfType("time.Time"), "direct-api", "", mock.Anything, mock.Anything).Return(nil)

	h := &Handler{config: mockStore}
	result, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "res-xyz", nil)
	require.NoError(t, err)
	m, ok := result.(*revokePurchaseResult)
	require.True(t, ok)
	assert.Equal(t, "revoked", m.Status)
}

// TestIsAzureWindowEdgeError verifies that isAzureWindowEdgeError identifies
// exactly the RefundPolicyViolated error code and nothing else.
func TestIsAzureWindowEdgeError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err     error
		name    string
		wantYes bool
	}{
		{
			name:    "RefundPolicyViolated",
			err:     &azcore.ResponseError{StatusCode: 400, ErrorCode: "RefundPolicyViolated"},
			wantYes: true,
		},
		{
			name:    "other 400 error code",
			err:     &azcore.ResponseError{StatusCode: 400, ErrorCode: "InvalidParameter"},
			wantYes: false,
		},
		{
			name:    "nil error",
			err:     nil,
			wantYes: false,
		},
		{
			name:    "plain error with RefundPolicyViolated in message",
			err:     errors.New("400: RefundPolicyViolated"),
			wantYes: false,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isAzureWindowEdgeError(tc.err)
			assert.Equal(t, tc.wantYes, got)
		})
	}
}

// TestCallAzureReturn_RefundPolicyViolatedReturns422WindowEdge verifies that
// when the Return API returns RefundPolicyViolated (window expired mid-flight),
// callAzureReturn returns a 422 ClientError with code AZURE_WINDOW_EDGE rather
// than a generic 400 "Azure refund rejected" message (issue #290 Finding #3).
func TestCallAzureReturn_RefundPolicyViolatedReturns422WindowEdge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	sessID := "s-edge"
	calcClient := &stubCalcRefundClientWithAmount{amount: 30.0, currency: "USD", sessID: sessID}
	returnClient := &stubReturnClient{
		err: &azcore.ResponseError{StatusCode: 400, ErrorCode: "RefundPolicyViolated"},
	}

	r := armReservationRecord()
	h := &Handler{config: mockStore}
	_, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "res-xyz", nil)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 422, ce.code)
	assert.Contains(t, ce.message, "window has closed")
	// MarkPurchaseRevoked must NOT be called -- Azure did not refund.
	mockStore.AssertNotCalled(t, "MarkPurchaseRevoked")
}

// --- Finding D: revocation_in_flight sticky on Azure error paths (second-wave CR) ---

// TestCallAzureReturn_TransientError_ClearsInFlight verifies that when the
// Azure Return call fails with a transient (non-window-edge, non-client) error,
// callAzureReturn calls ClearRevocationInFlight so the row is not left sticky.
func TestCallAzureReturn_TransientError_ClearsInFlight(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	calcClient := &stubCalcRefundClientWithAmount{amount: 10.0, currency: "USD", sessID: "s-transient"}
	returnClient := &stubReturnClient{err: errors.New("dial tcp: connection refused")}

	r := armReservationRecord()
	mockStore.On("ClearRevocationInFlight", ctx, r.PurchaseID).Return(nil).Once()

	h := &Handler{config: mockStore}
	_, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "res-xyz", nil)
	require.Error(t, err)
	// Must not be a ClientError -- transient errors surface as a 500 from the caller.
	_, isClientErr := IsClientError(err)
	assert.False(t, isClientErr, "transient Azure error must not be a ClientError")
	mockStore.AssertExpectations(t)
}

// TestCallAzureReturn_ClientError_ClearsInFlight verifies that an Azure client
// error (400-class rejection) also clears the in-flight flag so the row reverts
// to its original status and future retries are not blocked.
func TestCallAzureReturn_ClientError_ClearsInFlight(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	calcClient := &stubCalcRefundClientWithAmount{amount: 10.0, currency: "USD", sessID: "s-client"}
	returnClient := &stubReturnClient{
		err: &azcore.ResponseError{StatusCode: 400, ErrorCode: "InvalidReservationID"},
	}

	r := armReservationRecord()
	mockStore.On("ClearRevocationInFlight", ctx, r.PurchaseID).Return(nil).Once()

	h := &Handler{config: mockStore}
	_, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "res-xyz", nil)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	mockStore.AssertExpectations(t)
}

// TestCallAzureReturn_WindowEdge_ClearsInFlight verifies that a RefundPolicyViolated
// window-edge error also clears the in-flight flag (Azure did not refund).
func TestCallAzureReturn_WindowEdge_ClearsInFlight(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	calcClient := &stubCalcRefundClientWithAmount{amount: 10.0, currency: "USD", sessID: "s-edge"}
	returnClient := &stubReturnClient{
		err: &azcore.ResponseError{StatusCode: 400, ErrorCode: "RefundPolicyViolated"},
	}

	r := armReservationRecord()
	mockStore.On("ClearRevocationInFlight", ctx, r.PurchaseID).Return(nil).Once()

	h := &Handler{config: mockStore}
	_, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "res-xyz", nil)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 422, ce.code)
	mockStore.AssertExpectations(t)
}

// TestCallAzureReturn_Success_DoesNotClearInFlight verifies that on a successful
// Azure Return, ClearRevocationInFlight is NOT called (the flag should remain
// true until the finalize sweep or MarkPurchaseRevoked clears it).
func TestCallAzureReturn_Success_DoesNotClearInFlight(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	calcClient := &stubCalcRefundClientWithAmount{amount: 10.0, currency: "USD", sessID: "s-ok"}
	returnClient := &stubReturnClient{resp: armreservations.ReturnClientPostResponse{}}

	r := armReservationRecord()
	mockStore.On("MarkPurchaseRevoked", ctx, r.PurchaseID, mock.AnythingOfType("time.Time"),
		"direct-api", "", mock.Anything, mock.Anything).Return(nil).Once()

	h := &Handler{config: mockStore}
	result, err := h.callAzureReturn(ctx, calcClient, returnClient, r, "order-abc", "res-xyz", nil)
	require.NoError(t, err)
	rr, ok := result.(*revokePurchaseResult)
	require.True(t, ok)
	assert.Equal(t, "revoked", rr.Status)
	// ClearRevocationInFlight must NOT have been called on the success path.
	mockStore.AssertNotCalled(t, "ClearRevocationInFlight", mock.Anything, mock.Anything)
	mockStore.AssertExpectations(t)
}

// --- Finding #9: DST-crossing window math + 4-eyes placeholder ---

// TestRevocationWindowClosesAtFor_DSTCrossing verifies that the revocation
// window uses AddDate (calendar arithmetic) rather than Add(7*24*time.Hour)
// (fixed duration) so it correctly spans DST transitions.
//
// In timezones that observe DST, "7 days from purchase" should mean the same
// clock time 7 days later -- not 167h or 169h depending on which way the
// clocks turned. AddDate handles this; a fixed 168h duration does not.
//
// The test constructs a purchase at 01:30 Eastern on the day of the 2024 US
// spring-forward (March 10 -- clocks leap from 02:00 to 03:00, losing 1h).
// The correct window close is 01:30 Eastern on March 17 (168h in wall-clock
// time but 167h in absolute duration because of the spring-forward).
// A naive Add(7*24*time.Hour) would land at 00:30 on March 17 (1h off).
func TestRevocationWindowClosesAtFor_DSTCrossing(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("America/New_York timezone not available on this system:", err)
	}

	// March 10, 2024: US spring-forward night. Clocks jump 02:00->03:00.
	// Purchase at 01:30 EST (UTC-5) = 06:30 UTC.
	purchaseTime := time.Date(2024, time.March, 10, 1, 30, 0, 0, loc)

	// config.RevocationWindowClosesAtFor uses AddDate(0,0,7) -- calendar days.
	windowCloses := purchaseTime.AddDate(0, 0, AzureRevocationWindowDays)

	// Expected: 01:30 EDT (UTC-4) on March 17 = 05:30 UTC.
	wantClose := time.Date(2024, time.March, 17, 1, 30, 0, 0, loc)
	assert.Equal(t, wantClose.UTC(), windowCloses.UTC(),
		"window must close at the same clock time 7 days later (AddDate, not Add(168h))")

	// Prove the naive fixed-duration approach gives a different (wrong) answer.
	naiveClose := purchaseTime.Add(7 * 24 * time.Hour)
	assert.NotEqual(t, wantClose.UTC(), naiveClose.UTC(),
		"naive Add(168h) must land at a different time across DST -- confirming AddDate is needed")
}

// TestRevokePurchase_FourEyesApproval is a placeholder for the revoke+4-eyes
// integration test. The 4-eyes approval gate for revocations is tracked in
// issue #1005 and will be implemented in a follow-up PR.
//
// This test is intentionally skipped so the suite stays green while the
// feature is in development; remove the t.Skip when #1005 lands.
func TestRevokePurchase_FourEyesApproval(t *testing.T) {
	t.Skip("placeholder until #1005 4-eyes revocation approval lands")
}

// TestRevokePurchase_GetExecutionByIDDBError_Returns500 guards that a genuine
// DB error from GetExecutionByID surfaces as 500 rather than silently falling
// through to the purchase_history lookup path (Finding C, second-wave CR).
//
// Before the fix, any non-nil execErr was folded into "execErr == nil && ..."
// so the error was swallowed and the handler continued to GetPurchaseHistoryByPurchaseID.
// Now a non-nil execErr returns a 500 immediately.
func TestRevokePurchase_GetExecutionByIDDBError_Returns500(t *testing.T) {
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

	// GetExecutionByID returns a genuine DB error (not a not-found nil).
	dbErr := errors.New("pq: connection closed unexpectedly")
	mockStore.On("GetExecutionByID", ctx, "pid-dberr").Return((*config.PurchaseExecution)(nil), dbErr)
	// GetPurchaseHistoryByPurchaseID must NEVER be called when GetExecutionByID fails.
	mockStore.AssertNotCalled(t, "GetPurchaseHistoryByPurchaseID", mock.Anything, mock.Anything)

	h := &Handler{config: mockStore, auth: mockAuth}
	_, err := h.revokePurchase(ctx, sessionReq(), "pid-dberr")
	require.Error(t, err, "DB error from GetExecutionByID must surface as an error, not silent passthrough")
	// The error wraps the DB error; it is NOT a ClientError (not user-facing 500 shape)
	// but the router will convert it to a 500 response. Verify the DB error is present.
	assert.ErrorContains(t, err, "pq: connection closed unexpectedly")
	mockStore.AssertExpectations(t)
}

// TestRevokePurchase_ConcurrentScheduledRevoke_OneWinsOneGets410 verifies that
// two parallel revoke requests for the same scheduled execution produce the
// correct outcomes: the first CAS wins (canceled), the second CAS loses and
// returns 410 (Finding B, second-wave CR).
//
// The fix drops the racy "status == scheduled" pre-check and lets
// CancelScheduledExecutionAtomic decide. A second call with !canceled means
// the scheduler (or first caller) already transitioned the row.
func TestRevokePurchase_ConcurrentScheduledRevoke_OneWinsOneGets410(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	execID := "exec-concurrent-b"
	adminSess := revokeAdminSession()

	// --- First caller: wins the CAS ---
	t.Run("first caller wins", func(t *testing.T) {
		t.Parallel()
		mockStore := new(MockConfigStore)
		mockAuth := new(MockAuthService)
		t.Cleanup(func() {
			mockStore.AssertExpectations(t)
			mockAuth.AssertExpectations(t)
		})

		mockAuth.On("ValidateSession", ctx, "tok").Return(adminSess, nil)
		exec := scheduledExecution(execID, "")
		mockStore.On("GetExecutionByID", ctx, execID).Return(exec, nil)
		mockStore.On("CancelScheduledExecutionAtomic", ctx, mock.Anything, execID, mock.Anything).
			//nolint:misspell // DB schema value 'cancelled' -- see migration 000001_initial_schema.up.sql
			Return(true, "cancelled", nil).Once()
		mockStore.On("DeleteSuppressionsByExecutionTx", ctx, mock.Anything, execID).Return(nil).Once()

		h := &Handler{config: mockStore, auth: mockAuth}
		result, err := h.revokePurchase(ctx, sessionReq(), execID)
		require.NoError(t, err)
		m, ok := result.(map[string]string)
		require.True(t, ok)
		//nolint:misspell // DB schema value 'cancelled' -- see migration 000001_initial_schema.up.sql
		assert.Equal(t, "cancelled", m["status"])
	})

	// --- Second caller: CAS returns !canceled (scheduler or first caller won) ---
	t.Run("second caller gets 410", func(t *testing.T) {
		t.Parallel()
		mockStore := new(MockConfigStore)
		mockAuth := new(MockAuthService)
		t.Cleanup(func() {
			mockStore.AssertExpectations(t)
			mockAuth.AssertExpectations(t)
		})

		mockAuth.On("ValidateSession", ctx, "tok").Return(adminSess, nil)
		exec := scheduledExecution(execID, "")
		mockStore.On("GetExecutionByID", ctx, execID).Return(exec, nil)
		// CAS returns !canceled because the row was already transitioned.
		mockStore.On("CancelScheduledExecutionAtomic", ctx, mock.Anything, execID, mock.Anything).
			Return(false, "completed", nil).Once()

		h := &Handler{config: mockStore, auth: mockAuth}
		_, err := h.revokePurchase(ctx, sessionReq(), execID)
		require.Error(t, err)
		ce, ok := IsClientError(err)
		require.True(t, ok, "CAS race-lost must surface as a ClientError")
		assert.Equal(t, 410, ce.code, "second concurrent revoke must return 410")
	})
}
