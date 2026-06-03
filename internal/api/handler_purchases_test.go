package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// approvalTestExec builds a purchase execution wired to a single
// recommendation against an account whose contact_email is `contact`. Used
// to satisfy the post-hardening approver-set policy (see
// authorizeApprovalAction): the global notify mailbox is no longer an
// authorised approver, so tests must wire a per-account contact email.
func approvalTestExec(execID, contact string, mockConfig *MockConfigStore) *config.PurchaseExecution {
	accountID := "acct-1"
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
		Recommendations: []config.RecommendationRecord{
			{ID: "r1", CloudAccountID: &accountID},
		},
	}
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, ContactEmail: contact}, nil
	}
	return exec
}

func TestHandler_approvePurchase(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	approver := "admin@example.com"

	mockConfig := new(MockConfigStore)
	exec := approvalTestExec(execID, approver, mockConfig)
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &approver,
	}, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: approver}, nil)
	// After issue #286 the session-authed approve dispatch consults the
	// approve-{any,own} verb matrix BEFORE falling through to the token
	// branch. The session here is the approver's mailbox (no role / no
	// UserID), so the verb checks must explicitly return false to drop
	// into the legacy token-authed branch this test exercises. Mirrors
	// the cancel test's `.Maybe()` pattern below.
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-any", "purchases").Return(false, nil).Maybe()
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-own", "purchases").Return(false, nil).Maybe()

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("ApproveExecution", ctx, execID, "valid-token", approver).Return(nil)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	result, err := handler.approvePurchase(ctx, req, execID, "valid-token")
	require.NoError(t, err)

	// Issue #372: approve now also executes synchronously, so the JSON
	// response surfaces the final state instead of the transient
	// "approved" the old no-op flow returned.
	resultMap := result.(map[string]string)
	assert.Equal(t, "completed", resultMap["status"])
}

func TestHandler_cancelPurchase(t *testing.T) {
	ctx := context.Background()
	execID := "45645645-6456-4564-5645-645645645645"
	approver := "admin@example.com"

	mockConfig := new(MockConfigStore)
	exec := approvalTestExec(execID, approver, mockConfig)
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &approver,
	}, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: approver}, nil)
	// Session has no admin role and no cancel permissions, so cancelPurchase's
	// session-authed pre-check (added to fix the deep-link contact_email gate)
	// falls through to authorizeApprovalAction. The contact_email + globalNotify
	// fixture above is what proves the legacy email-link path still works.
	mockAuth.On("HasPermissionAPI", ctx, "", "cancel-any", "purchases").Return(false, nil).Maybe()
	mockAuth.On("HasPermissionAPI", ctx, "", "cancel-own", "purchases").Return(false, nil).Maybe()

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("CancelExecution", ctx, execID, "valid-token", approver).Return(nil)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	result, err := handler.cancelPurchase(ctx, req, execID, "valid-token")
	require.NoError(t, err)

	resultMap := result.(map[string]string)
	assert.Equal(t, "cancelled", resultMap["status"])
}

func TestHandler_approvePurchase_RejectsMismatchedSession(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	approver := "approver@example.com"

	mockConfig := new(MockConfigStore)
	exec := approvalTestExec(execID, approver, mockConfig)
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &approver,
	}, nil)

	mockAuth := new(MockAuthService)
	// Session belongs to someone who is NOT the authorised approver.
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: "wrong@example.com"}, nil)
	// After issue #286 the dispatch consults approve-{any,own} BEFORE
	// the contact_email gate. The wrong@example.com session has neither
	// verb, so the dispatch returns 403 from authorizeSessionApprove,
	// `isPermissionDenied(err)` matches, and execution falls through to
	// the token branch's authorizeApprovalAction — which is what
	// produces the "not the authorised approver" error this test pins.
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-any", "purchases").Return(false, nil).Maybe()
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-own", "purchases").Return(false, nil).Maybe()

	mockPurchase := new(MockPurchaseManager)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	_, err := handler.approvePurchase(ctx, req, execID, "valid-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not the authorised approver")
	// ApproveExecution must not have been called — purchase manager mock
	// asserts nothing by construction; a .On(...) entry above would create
	// a false positive, so we pin the negative by confirming the error is
	// the authz error, not an approval-manager error.
	mockPurchase.AssertNotCalled(t, "ApproveExecution")
}

// TestHandler_approvePurchase_RejectsMissingContactEmail covers the
// security-hardened behaviour: when an execution's recommendations do not
// resolve to ANY per-account contact_email, the approval is rejected even
// if the session belongs to the global notification mailbox. Closes the
// loophole where a catch-all inbox could approve purchases on accounts it
// doesn't own.
func TestHandler_approvePurchase_RejectsMissingContactEmail(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	globalNotify := "global@cudly.example"
	accountID := "acct-no-contact"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
		Recommendations: []config.RecommendationRecord{
			{ID: "r1", CloudAccountID: &accountID},
		},
	}
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id /* no ContactEmail */}, nil
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &globalNotify,
	}, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: globalNotify}, nil)
	// Issue #286 dispatch consults approve-{any,own} BEFORE the contact_email
	// gate. globalNotify session has neither verb → 403 → fall through to
	// token branch → authorizeApprovalAction surfaces the "no per-account
	// contact email" error this test pins.
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-any", "purchases").Return(false, nil).Maybe()
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-own", "purchases").Return(false, nil).Maybe()

	mockPurchase := new(MockPurchaseManager)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	_, err := handler.approvePurchase(ctx, req, execID, "valid-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no per-account contact email")
	mockPurchase.AssertNotCalled(t, "ApproveExecution")
}

func TestHandler_approvePurchase_RejectsMissingSession(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)

	handler := &Handler{config: mockConfig}

	// No Authorization header → no session → reject.
	_, err := handler.approvePurchase(ctx, &events.LambdaFunctionURLRequest{}, execID, "valid-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sign in")
}

func TestHandler_approvePurchase_AcceptsContactEmailSession(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	contactEmail := "contact@archera.example"
	globalNotify := "global@cudly.example"
	accountID := "acct-1"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
		Recommendations: []config.RecommendationRecord{
			{ID: "r1", CloudAccountID: &accountID},
		},
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &globalNotify,
	}, nil)
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, ContactEmail: contactEmail}, nil
	}

	mockAuth := new(MockAuthService)
	// Session email matches the account contact email — global notify is
	// NOT enough here because a contact email exists for the account.
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: contactEmail}, nil)
	// Issue #286: dispatch consults approve-{any,own} BEFORE the contact_email
	// gate. The contact-email session has neither verb (no Role / no UserID)
	// → 403 → fall through to token branch → authorizeApprovalAction matches
	// the contact email and the legacy ApproveExecution path runs.
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-any", "purchases").Return(false, nil).Maybe()
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-own", "purchases").Return(false, nil).Maybe()

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("ApproveExecution", ctx, execID, "valid-token", contactEmail).Return(nil)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	result, err := handler.approvePurchase(ctx, req, execID, "valid-token")
	require.NoError(t, err)
	// Issue #372: response now reflects post-execute state.
	assert.Equal(t, "completed", result.(map[string]string)["status"])
}

// TestHandler_approvePurchase_SessionApproveAnyChainsToExecute pins the
// session-authed branch added by issue #286 + made functional by issue
// #372: when an admin / approve-any session clicks Approve from the
// dashboard, the handler must call ApproveAndExecute (NOT
// ApproveExecution), and the JSON response must surface the post-execute
// status. This is the regression test for the bug where approval was a
// no-op beyond the status flip on the Lambda deployment.
func TestHandler_approvePurchase_SessionApproveAnyChainsToExecute(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	adminEmail := "admin@example.com"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
		Recommendations: []config.RecommendationRecord{
			{ID: "r1"},
		},
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: adminEmail}, nil)
	mockAuth.grantAdmin()
	// approvePurchaseViaSession enforces CSRF on the session-authed path (issue #404).
	mockAuth.On("ValidateCSRFToken", ctx, "sess-tok", "").Return(nil)

	mockPurchase := new(MockPurchaseManager)
	// CRITICAL ASSERTION: session-authed approve goes through
	// ApproveAndExecute, not ApproveExecution. The token-only path runs
	// ApproveExecution; the dashboard click runs ApproveAndExecute. Both
	// converge inside the Manager.
	mockPurchase.On("ApproveAndExecute", ctx, execID, adminEmail).Return(nil)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	// Empty token forces the dashboard branch.
	result, err := handler.approvePurchase(ctx, req, execID, "")
	require.NoError(t, err)
	assert.Equal(t, "completed", result.(map[string]string)["status"])
	mockPurchase.AssertExpectations(t)
	mockPurchase.AssertNotCalled(t, "ApproveExecution")
}

// TestHandler_approvePurchase_SessionExecuteFailureSurfacesAs409 pins the
// failure shape: when ApproveAndExecute returns an error (e.g. the AWS
// purchase fails or the row drifted out of pending/notified mid-flight),
// the session handler surfaces it as a 409 instead of the optimistic
// "approved" the pre-fix flow returned. Mirrors the rationale in
// approvePurchaseViaSession.
func TestHandler_approvePurchase_SessionExecuteFailureSurfacesAs409(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	adminEmail := "admin@example.com"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:     execID,
		ApprovalToken:   "valid-token",
		Status:          "pending",
		Recommendations: []config.RecommendationRecord{{ID: "r1"}},
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: adminEmail}, nil)
	mockAuth.grantAdmin()
	// approvePurchaseViaSession enforces CSRF on the session-authed path (issue #404).
	mockAuth.On("ValidateCSRFToken", ctx, "sess-tok", "").Return(nil)

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("ApproveAndExecute", ctx, execID, adminEmail).Return(errors.New("AWS RI purchase failed"))

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	_, err := handler.approvePurchase(ctx, req, execID, "")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError")
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, ce.Error(), "could not be approved")
}

// --- Regression tests for issue #609 (orphan-account guard) ---

// TestHandler_approvePurchase_AzureOrphanRejects409 is the regression guard
// for issue #609: an execution whose CloudAccountID is nil and whose provider
// is Azure must be rejected with 409 and a descriptive message before the
// cloud SDK is ever reached. Pre-fix this surfaced an opaque IMDS /
// missing-env-var error.
func TestHandler_approvePurchase_AzureOrphanRejects409(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
		// CloudAccountID intentionally nil — account was deleted.
		Recommendations: []config.RecommendationRecord{{ID: "r1", Provider: "azure"}},
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: "admin@example.com"}, nil)
	mockAuth.grantAdmin()

	mockPurchase := new(MockPurchaseManager)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	_, err := handler.approvePurchase(ctx, req, execID, "")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError")
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, ce.Error(), "no longer exists")
	assert.Contains(t, ce.Error(), "azure")
	// Guard fires before the purchase manager is touched.
	mockPurchase.AssertNotCalled(t, "ApproveAndExecute", mock.Anything, mock.Anything, mock.Anything)
	mockPurchase.AssertNotCalled(t, "ApproveExecution", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestHandler_approvePurchase_GCPOrphanRejects409 mirrors the Azure case for GCP.
func TestHandler_approvePurchase_GCPOrphanRejects409(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:     execID,
		ApprovalToken:   "valid-token",
		Status:          "pending",
		Recommendations: []config.RecommendationRecord{{ID: "r1", Provider: "gcp"}},
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: "admin@example.com"}, nil)
	mockAuth.grantAdmin()

	mockPurchase := new(MockPurchaseManager)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	_, err := handler.approvePurchase(ctx, req, execID, "")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError")
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, ce.Error(), "no longer exists")
	assert.Contains(t, ce.Error(), "gcp")
	mockPurchase.AssertNotCalled(t, "ApproveAndExecute", mock.Anything, mock.Anything, mock.Anything)
	mockPurchase.AssertNotCalled(t, "ApproveExecution", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestHandler_approvePurchase_AWSOrphanFallsThrough confirms that an AWS
// execution with nil CloudAccountID is NOT blocked by the issue-#609 guard.
// AWS has an ambient-host-account fallback (PR #607/#604) that handles it.
func TestHandler_approvePurchase_AWSOrphanFallsThrough(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	adminEmail := "admin@example.com"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
		// CloudAccountID nil — but provider is AWS, so fallback applies.
		Recommendations: []config.RecommendationRecord{{ID: "r1", Provider: "aws"}},
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: adminEmail}, nil)
	mockAuth.grantAdmin()
	// approvePurchaseViaSession enforces CSRF on the session-authed path (issue #404).
	mockAuth.On("ValidateCSRFToken", ctx, "sess-tok", "").Return(nil)

	mockPurchase := new(MockPurchaseManager)
	// Guard does not fire; ApproveAndExecute is called normally.
	mockPurchase.On("ApproveAndExecute", ctx, execID, adminEmail).Return(nil)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	result, err := handler.approvePurchase(ctx, req, execID, "")
	require.NoError(t, err)
	assert.Equal(t, "completed", result.(map[string]string)["status"])
	mockPurchase.AssertExpectations(t)
}

// TestHandler_approvePurchase_NonOrphanUnchanged confirms that an execution
// with a populated CloudAccountID is not affected by the issue-#609 guard.
func TestHandler_approvePurchase_NonOrphanUnchanged(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	adminEmail := "admin@example.com"
	accountID := "acct-azure-001"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
		// CloudAccountID is set — normal case, guard must not fire.
		Recommendations: []config.RecommendationRecord{{ID: "r1", Provider: "azure", CloudAccountID: &accountID}},
		CloudAccountID:  &accountID,
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: adminEmail}, nil)
	mockAuth.grantAdmin()
	// approvePurchaseViaSession enforces CSRF on the session-authed path (issue #404).
	mockAuth.On("ValidateCSRFToken", ctx, "sess-tok", "").Return(nil)

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("ApproveAndExecute", ctx, execID, adminEmail).Return(nil)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	result, err := handler.approvePurchase(ctx, req, execID, "")
	require.NoError(t, err)
	assert.Equal(t, "completed", result.(map[string]string)["status"])
	mockPurchase.AssertExpectations(t)
}

func TestHandler_approvePurchase_RejectsGlobalNotifyWhenContactSet(t *testing.T) {
	// Regression: once an account has contact_email set, the global
	// notification email is only CC'd — it should NOT be accepted as an
	// approver. The session owner of the global notify address must not
	// be able to approve on that account's behalf.
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	contactEmail := "contact@archera.example"
	globalNotify := "global@cudly.example"
	accountID := "acct-1"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
		Recommendations: []config.RecommendationRecord{
			{ID: "r1", CloudAccountID: &accountID},
		},
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &globalNotify,
	}, nil)
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, ContactEmail: contactEmail}, nil
	}

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: globalNotify}, nil)
	// Issue #286: dispatch consults approve-{any,own} BEFORE the
	// contact_email gate. Returning false for both verbs lets the
	// dispatch fall through to the token branch where the
	// "not the authorised approver" check fires.
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-any", "purchases").Return(false, nil).Maybe()
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-own", "purchases").Return(false, nil).Maybe()

	mockPurchase := new(MockPurchaseManager)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	_, err := handler.approvePurchase(ctx, req, execID, "valid-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not the authorised approver")
	mockPurchase.AssertNotCalled(t, "ApproveExecution")
}

// TestRouter_approvePurchaseHandler_RateLimited is a regression test for issue #400.
// The approve endpoint is AuthPublic (token-only); without rate limiting any
// attacker can flood approve attempts to brute-force a valid token.
// Once the "approve_cancel_public" bucket is exhausted the router wrapper must
// return a 429 before dispatching to the business-logic handler.
func TestRouter_approvePurchaseHandler_RateLimited(t *testing.T) {
	ctx := context.Background()

	mockRL := new(MockRateLimiter)
	// Simulate the rate limit already exceeded.
	mockRL.On("AllowWithIP", ctx, "1.2.3.4", "approve_cancel_public").Return(false, nil)

	h := &Handler{rateLimiter: mockRL}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				SourceIP: "1.2.3.4",
			},
		},
		QueryStringParameters: map[string]string{"token": "any-token"},
	}
	_, err := r.approvePurchaseHandler(ctx, req, map[string]string{"id": "12345678-1234-1234-1234-123456789abc"})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 429, ce.code)

	mockRL.AssertExpectations(t)
}

// TestRouter_cancelPurchaseHandler_RateLimited is a regression test for issue #400.
func TestRouter_cancelPurchaseHandler_RateLimited(t *testing.T) {
	ctx := context.Background()

	mockRL := new(MockRateLimiter)
	mockRL.On("AllowWithIP", ctx, "5.6.7.8", "approve_cancel_public").Return(false, nil)

	h := &Handler{rateLimiter: mockRL}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				SourceIP: "5.6.7.8",
			},
		},
		QueryStringParameters: map[string]string{"token": "any-token"},
	}
	_, err := r.cancelPurchaseHandler(ctx, req, map[string]string{"id": "45645645-6456-4564-5645-645645645645"})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 429, ce.code)

	mockRL.AssertExpectations(t)
}

func TestHandler_resolveApprovalRecipients_ContactBecomesTo(t *testing.T) {
	ctx := context.Background()
	contactA := "contact-a@example.com"
	contactB := "contact-b@example.com"
	globalNotify := "global@cudly.example"
	accountA := "acct-a"
	accountB := "acct-b"

	mockConfig := new(MockConfigStore)
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		switch id {
		case accountA:
			return &config.CloudAccount{ID: accountA, ContactEmail: contactA}, nil
		case accountB:
			return &config.CloudAccount{ID: accountB, ContactEmail: contactB}, nil
		}
		return nil, nil
	}

	h := &Handler{config: mockConfig}
	recs := []config.RecommendationRecord{
		{ID: "r1", CloudAccountID: &accountA},
		{ID: "r2", CloudAccountID: &accountB},
		{ID: "r3", CloudAccountID: &accountA}, // duplicate account
	}
	to, cc, approvers, err := h.resolveApprovalRecipients(ctx, recs, globalNotify)
	require.NoError(t, err)
	assert.Equal(t, contactA, to, "first contact email becomes To")
	assert.Equal(t, []string{contactB, globalNotify}, cc, "other contact + global end up in Cc")
	assert.Equal(t, []string{contactA, contactB}, approvers, "approvers are the contact emails, not global")
}

// TestHandler_resolveApprovalRecipients_NoContactEmail covers the security-
// hardened behaviour: when no recommendation has a per-account contact_email,
// the global notify mailbox receives the email (To) but is NOT added to the
// approver set. This closes the loophole where a catch-all inbox could
// authorise spend on accounts it doesn't own; authorizeApprovalAction will
// reject the approve/cancel because approvers is empty.
func TestHandler_resolveApprovalRecipients_NoContactEmail(t *testing.T) {
	ctx := context.Background()
	globalNotify := "global@cudly.example"
	accountID := "acct-no-contact"

	mockConfig := new(MockConfigStore)
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		// No ContactEmail — legacy account.
		return &config.CloudAccount{ID: id}, nil
	}

	h := &Handler{config: mockConfig}
	recs := []config.RecommendationRecord{
		{ID: "r1", CloudAccountID: &accountID},
	}
	to, cc, approvers, err := h.resolveApprovalRecipients(ctx, recs, globalNotify)
	require.NoError(t, err)
	assert.Equal(t, globalNotify, to, "global notify still receives the email as the To addressee")
	assert.Nil(t, cc)
	assert.Empty(t, approvers, "global notify must NOT be in the approver set — only per-account contact_email can approve")
}

// TestHandler_resolveApprovalRecipients_LookupErrorPropagates verifies
// the regression CodeRabbit flagged: a transient GetCloudAccount error
// must NOT silently degrade to a globalNotify-only fallback (which
// would change who is authorised to approve based on a DB blip).
// Instead, the lookup error propagates to the caller, which surfaces
// it as a retriable failure so the operator's next attempt sees the
// real approver list.
func TestHandler_resolveApprovalRecipients_LookupErrorPropagates(t *testing.T) {
	ctx := context.Background()
	globalNotify := "global@cudly.example"
	accountID := "acct-flaky"
	transient := errors.New("connection reset by peer")

	mockConfig := new(MockConfigStore)
	mockConfig.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) {
		return nil, transient
	}

	h := &Handler{config: mockConfig}
	recs := []config.RecommendationRecord{
		{ID: "r1", CloudAccountID: &accountID},
	}
	to, cc, approvers, err := h.resolveApprovalRecipients(ctx, recs, globalNotify)
	require.Error(t, err, "transient lookup error must propagate")
	assert.ErrorIs(t, err, transient, "wrapped error chain must preserve the underlying cause")
	assert.Empty(t, to)
	assert.Nil(t, cc)
	assert.Nil(t, approvers)
}

func TestHandler_getPlannedPurchases(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	scheduledDate := time.Now().AddDate(0, 0, 7)
	executions := []config.PurchaseExecution{
		{
			ExecutionID:      "11111111-1111-1111-1111-111111111111",
			PlanID:           "11111111-1111-1111-1111-111111111111",
			Status:           "pending",
			ScheduledDate:    scheduledDate,
			StepNumber:       1,
			EstimatedSavings: 100.0,
			TotalUpfrontCost: 500.0,
		},
	}

	plans := []config.PurchasePlan{
		{
			ID:   "11111111-1111-1111-1111-111111111111",
			Name: "Test Plan",
			Services: map[string]config.ServiceConfig{
				"aws/rds": {
					Provider: "aws",
					Service:  "rds",
					Term:     3,
					Payment:  "no-upfront",
				},
			},
			RampSchedule: config.RampSchedule{
				TotalSteps: 5,
			},
		},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	// The planned list must request paused executions alongside pending/notified
	// so a paused row stays VISIBLE. Assert the status set
	// explicitly rather than mock.Anything to lock the invariant.
	mockStore.On("GetPlannedExecutions", ctx,
		[]string{"pending", "notified", "paused"}, config.MaxListLimit).
		Return(executions, nil)
	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return(plans, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPlannedPurchases(ctx, req)
	require.NoError(t, err)
	mockStore.AssertExpectations(t)

	assert.Len(t, result.Purchases, 1)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", result.Purchases[0].ID)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", result.Purchases[0].PlanID)
	assert.Equal(t, "Test Plan", result.Purchases[0].PlanName)
	assert.Equal(t, "aws", result.Purchases[0].Provider)
	assert.Equal(t, "rds", result.Purchases[0].Service)
	assert.Equal(t, 3, result.Purchases[0].Term)
	assert.Equal(t, "no-upfront", result.Purchases[0].Payment)
	assert.Equal(t, 100.0, result.Purchases[0].EstimatedSavings)
	assert.Equal(t, 500.0, result.Purchases[0].UpfrontCost)
	assert.Equal(t, "pending", result.Purchases[0].Status)
	assert.Equal(t, 1, result.Purchases[0].StepNumber)
	assert.Equal(t, 5, result.Purchases[0].TotalSteps)
}

func TestHandler_getPlannedPurchases_ErrorGettingExecutions(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetPlannedExecutions", ctx, mock.Anything, mock.Anything).
		Return(nil, errors.New("database error"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPlannedPurchases(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to get planned executions")
}

// TestHandler_getPlannedPurchases_PausedStaysVisible is a regression guard:
// a paused execution must remain in the list (not silently disappear) and
// rows must be ordered soonest-first end-to-end.
func TestHandler_getPlannedPurchases_PausedStaysVisible(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	soon := time.Now().AddDate(0, 0, 3)
	later := time.Now().AddDate(0, 0, 10)
	// GetPlannedExecutions returns rows soonest-first (ASC); the handler
	// preserves that order with no in-memory re-sort.
	executions := []config.PurchaseExecution{
		{
			ExecutionID:   "33333333-3333-3333-3333-333333333333",
			PlanID:        "11111111-1111-1111-1111-111111111111",
			Status:        "pending",
			ScheduledDate: soon,
			StepNumber:    1,
		},
		{
			ExecutionID:   "22222222-2222-2222-2222-222222222222",
			PlanID:        "11111111-1111-1111-1111-111111111111",
			Status:        "paused",
			ScheduledDate: later,
			StepNumber:    2,
		},
	}
	plans := []config.PurchasePlan{
		{
			ID:           "11111111-1111-1111-1111-111111111111",
			Name:         "Test Plan",
			Services:     map[string]config.ServiceConfig{"aws/rds": {Provider: "aws", Service: "rds", Term: 3, Payment: "no-upfront"}},
			RampSchedule: config.RampSchedule{TotalSteps: 5},
		},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetPlannedExecutions", ctx,
		[]string{"pending", "notified", "paused"}, config.MaxListLimit).
		Return(executions, nil)
	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return(plans, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"Authorization": "Bearer admin-token"}}

	result, err := handler.getPlannedPurchases(ctx, req)
	require.NoError(t, err)
	mockStore.AssertExpectations(t)

	require.Len(t, result.Purchases, 2)
	// Soonest-first ordering.
	assert.Equal(t, "pending", result.Purchases[0].Status)
	assert.Equal(t, "paused", result.Purchases[1].Status)
	// The paused row is present, not dropped.
	var statuses []string
	for _, p := range result.Purchases {
		statuses = append(statuses, p.Status)
	}
	assert.Contains(t, statuses, "paused")
}

// TestHandler_getPlannedPurchases_SoonestRowsNotTruncated is the end-to-end
// regression guard for CodeRabbit's truncation finding on PR #904: when total
// pending/notified/paused rows exceed MaxListLimit, the store's DESC + LIMIT
// drops the soonest rows entirely, and an in-memory ASC re-sort of the
// already-truncated subset cannot recover them.
//
// The fix routes the handler through GetPlannedExecutions (ASC + LIMIT) and
// removes the post-fetch in-memory sort. This test:
//   - registers a mock expectation on GetPlannedExecutions and asserts
//     GetExecutionsByStatuses is NOT called (regressing to the DESC path
//     would surface here),
//   - returns rows in ASC order (mimicking what the real SQL would produce)
//     and asserts the handler preserves that order without re-shuffling,
//   - asserts all 5 rows survive (none are dropped by the handler itself).
func TestHandler_getPlannedPurchases_SoonestRowsNotTruncated(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	now := time.Now()
	// 5 rows, soonest first, the order the store will return them when
	// ORDER BY scheduled_date ASC is used (the fix). Pre-fix code called
	// GetExecutionsByStatuses (DESC) and re-sorted in-memory, but the
	// regression scenario is that the DB has truncated the soonest rows
	// away before the in-memory sort sees them.
	soonest := []config.PurchaseExecution{
		{ExecutionID: "11111111-1111-1111-1111-111111111111", PlanID: "00000000-0000-0000-0000-000000000001", Status: "pending", ScheduledDate: now.AddDate(0, 0, 1), StepNumber: 1},
		{ExecutionID: "22222222-2222-2222-2222-222222222222", PlanID: "00000000-0000-0000-0000-000000000001", Status: "notified", ScheduledDate: now.AddDate(0, 0, 2), StepNumber: 2},
		{ExecutionID: "33333333-3333-3333-3333-333333333333", PlanID: "00000000-0000-0000-0000-000000000001", Status: "paused", ScheduledDate: now.AddDate(0, 0, 3), StepNumber: 3},
		{ExecutionID: "44444444-4444-4444-4444-444444444444", PlanID: "00000000-0000-0000-0000-000000000001", Status: "pending", ScheduledDate: now.AddDate(0, 0, 4), StepNumber: 4},
		{ExecutionID: "55555555-5555-5555-5555-555555555555", PlanID: "00000000-0000-0000-0000-000000000001", Status: "pending", ScheduledDate: now.AddDate(0, 0, 5), StepNumber: 5},
	}
	plans := []config.PurchasePlan{
		{
			ID:           "00000000-0000-0000-0000-000000000001",
			Name:         "Test Plan",
			Services:     map[string]config.ServiceConfig{"aws/rds": {Provider: "aws", Service: "rds", Term: 3, Payment: "no-upfront"}},
			RampSchedule: config.RampSchedule{TotalSteps: 5},
		},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	// Strict args lock the contract: planned statuses + MaxListLimit (so the
	// DB receives the same cap the handler intends, no off-by-one budget).
	mockStore.On("GetPlannedExecutions", ctx,
		[]string{"pending", "notified", "paused"}, config.MaxListLimit).
		Return(soonest, nil)
	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return(plans, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"Authorization": "Bearer admin-token"}}

	result, err := handler.getPlannedPurchases(ctx, req)
	require.NoError(t, err)
	mockStore.AssertExpectations(t)
	// Regression anchor: the handler must NOT fall back to the DESC path.
	// AssertNotCalled fails if some refactor re-introduces a parallel
	// GetExecutionsByStatuses call on the planned list code path.
	mockStore.AssertNotCalled(t, "GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything)

	require.Len(t, result.Purchases, 5, "all 5 rows must reach the response, none dropped by handler post-processing")
	// Ordering preserved end-to-end: the store returns ASC, the handler must
	// pass that through unchanged (the in-memory re-sort has been removed).
	for i, expected := range []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
		"44444444-4444-4444-4444-444444444444",
		"55555555-5555-5555-5555-555555555555",
	} {
		assert.Equal(t, expected, result.Purchases[i].ID, "row %d (soonest-first) must be %s", i, expected)
	}
}

func TestHandler_pausePlannedPurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	paused := &config.PurchaseExecution{ExecutionID: "11111111-1111-1111-1111-111111111111", Status: "paused"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("TransitionExecutionStatus", ctx, "11111111-1111-1111-1111-111111111111", []string{"pending", "running"}, "paused").Return(paused, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.pausePlannedPurchase(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	assert.Equal(t, "paused", result.Status)
}

func TestHandler_pausePlannedPurchase_NotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("TransitionExecutionStatus", ctx, "99999999-9999-9999-9999-999999999999", []string{"pending", "running"}, "paused").Return(nil, fmt.Errorf("execution not found: 99999999-9999-9999-9999-999999999999"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.pausePlannedPurchase(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_resumePlannedPurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	resumed := &config.PurchaseExecution{ExecutionID: "11111111-1111-1111-1111-111111111111", Status: "pending"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("TransitionExecutionStatus", ctx, "11111111-1111-1111-1111-111111111111", []string{"paused"}, "pending").Return(resumed, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.resumePlannedPurchase(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	assert.Equal(t, "resumed", result.Status)
}

func TestHandler_runPlannedPurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	transitioned := &config.PurchaseExecution{
		ExecutionID: "11111111-1111-1111-1111-111111111111",
		Status:      "running",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("TransitionExecutionStatus", ctx, "11111111-1111-1111-1111-111111111111", []string{"pending", "paused"}, "running").Return(transitioned, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.runPlannedPurchase(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	resultMap := result.(map[string]interface{})
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", resultMap["execution_id"])
	assert.Equal(t, "running", resultMap["status"])
}

func TestHandler_deletePlannedPurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	cancelled := &config.PurchaseExecution{ExecutionID: "11111111-1111-1111-1111-111111111111", Status: "cancelled"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("TransitionExecutionStatus", ctx, "11111111-1111-1111-1111-111111111111", []string{"pending", "paused"}, "cancelled").Return(cancelled, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.deletePlannedPurchase(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	assert.Equal(t, "cancelled", result.Status)
}

// TestHandler_deletePlannedPurchase_DisablesPlan is a regression test for
// issue #774: disabling a plan from the scheduled-purchase row must set the
// plan's enabled flag to false in the same handler call.
func TestHandler_deletePlannedPurchase_DisablesPlan(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	planID := "22222222-2222-2222-2222-222222222222"
	execID := "11111111-1111-1111-1111-111111111111"

	cancelled := &config.PurchaseExecution{
		ExecutionID: execID,
		PlanID:      planID,
		Status:      "cancelled",
	}
	plan := &config.PurchasePlan{
		ID:      planID,
		Name:    "Test Plan",
		Enabled: true,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("TransitionExecutionStatus", ctx, execID, []string{"pending", "paused"}, "cancelled").Return(cancelled, nil)
	mockStore.On("GetPurchasePlan", ctx, planID).Return(plan, nil)
	// Assert that UpdatePurchasePlan is called with enabled=false.
	mockStore.On("UpdatePurchasePlan", ctx, mock.MatchedBy(func(p *config.PurchasePlan) bool {
		return p.ID == planID && !p.Enabled
	})).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.deletePlannedPurchase(ctx, req, execID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", result.Status)
	// Plan struct is mutated in place; confirm the flag was flipped.
	assert.False(t, plan.Enabled, "plan.Enabled must be false after disable")
}

// TestHandler_deletePlannedPurchase_AlreadyDisabledPlan verifies that if the
// plan is already disabled (enabled=false) the handler skips the UpdatePurchasePlan
// call and still returns success.
func TestHandler_deletePlannedPurchase_AlreadyDisabledPlan(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	planID := "33333333-3333-3333-3333-333333333333"
	execID := "44444444-4444-4444-4444-444444444444"

	cancelled := &config.PurchaseExecution{
		ExecutionID: execID,
		PlanID:      planID,
		Status:      "cancelled",
	}
	// Plan already disabled - UpdatePurchasePlan must NOT be called.
	plan := &config.PurchasePlan{
		ID:      planID,
		Name:    "Already Disabled Plan",
		Enabled: false,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("TransitionExecutionStatus", ctx, execID, []string{"pending", "paused"}, "cancelled").Return(cancelled, nil)
	mockStore.On("GetPurchasePlan", ctx, planID).Return(plan, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.deletePlannedPurchase(ctx, req, execID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", result.Status)
}

// TestHandler_deletePlannedPurchase_ConflictRetryDisablesPlan covers the
// idempotency path for Finding 1 of CR #781: when TransitionExecutionStatus
// returns ErrExecutionNotInExpectedStatus (the cancel already landed in a
// previous attempt), the handler must still disable the plan.
func TestHandler_deletePlannedPurchase_ConflictRetryDisablesPlan(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	planID := "55555555-5555-5555-5555-555555555555"
	execID := "66666666-6666-6666-6666-666666666666"

	// TransitionExecutionStatus fails with a CAS conflict (cancel already
	// applied by a previous attempt).
	conflictErr := fmt.Errorf("%w: execution %s cannot transition", config.ErrExecutionNotInExpectedStatus, execID)

	// GetExecutionByID is called to recover the PlanID after the conflict.
	existingExec := &config.PurchaseExecution{
		ExecutionID: execID,
		PlanID:      planID,
		Status:      "cancelled",
	}
	plan := &config.PurchasePlan{
		ID:      planID,
		Name:    "Retry Plan",
		Enabled: true,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("TransitionExecutionStatus", ctx, execID, []string{"pending", "paused"}, "cancelled").Return(nil, conflictErr)
	mockStore.On("GetExecutionByID", ctx, execID).Return(existingExec, nil)
	mockStore.On("GetPurchasePlan", ctx, planID).Return(plan, nil)
	mockStore.On("UpdatePurchasePlan", ctx, mock.MatchedBy(func(p *config.PurchasePlan) bool {
		return p.ID == planID && !p.Enabled
	})).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.deletePlannedPurchase(ctx, req, execID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", result.Status)
	assert.False(t, plan.Enabled, "plan.Enabled must be false after conflict-retry disable")
}

// TestHandler_deletePlannedPurchase_ConflictRetryAlreadyDisabled verifies that
// a second retry against an already-disabled plan is a no-op: UpdatePurchasePlan
// must NOT be called.
func TestHandler_deletePlannedPurchase_ConflictRetryAlreadyDisabled(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	planID := "77777777-7777-7777-7777-777777777777"
	execID := "88888888-8888-8888-8888-888888888888"

	conflictErr := fmt.Errorf("%w: execution %s cannot transition", config.ErrExecutionNotInExpectedStatus, execID)

	existingExec := &config.PurchaseExecution{
		ExecutionID: execID,
		PlanID:      planID,
		Status:      "cancelled",
	}
	// Plan already disabled; UpdatePurchasePlan must NOT be called.
	plan := &config.PurchasePlan{
		ID:      planID,
		Name:    "Already Disabled Retry Plan",
		Enabled: false,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("TransitionExecutionStatus", ctx, execID, []string{"pending", "paused"}, "cancelled").Return(nil, conflictErr)
	mockStore.On("GetExecutionByID", ctx, execID).Return(existingExec, nil)
	mockStore.On("GetPurchasePlan", ctx, planID).Return(plan, nil)
	// UpdatePurchasePlan is intentionally NOT registered; AssertExpectations
	// verifies it is never called.

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.deletePlannedPurchase(ctx, req, execID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", result.Status)
}

func TestHandler_pausePlannedPurchase_NilExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("TransitionExecutionStatus", ctx, "99999999-9999-9999-9999-999999999999", []string{"pending", "running"}, "paused").Return(nil, fmt.Errorf("execution not found: 99999999-9999-9999-9999-999999999999"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.pausePlannedPurchase(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestHandler_pausePlannedPurchase_IneligibleStatus verifies that attempting to
// pause an execution whose current status is not in the allowed set (e.g.
// 'completed') surfaces a 409 with a clear message rather than leaking the raw
// Postgres CHECK constraint error (SQLSTATE 23514). This is the regression test
// for issue #772.
func TestHandler_pausePlannedPurchase_IneligibleStatus(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	// Store returns ErrExecutionNotInExpectedStatus when the row is 'completed'
	// and cannot be transitioned to 'paused'.
	mockStore.On("TransitionExecutionStatus", ctx, "11111111-1111-1111-1111-111111111111", []string{"pending", "running"}, "paused").
		Return(nil, fmt.Errorf("%w: execution 11111111-1111-1111-1111-111111111111 cannot transition from %q to %q",
			config.ErrExecutionNotInExpectedStatus, "completed", "paused"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.pausePlannedPurchase(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.Error(t, err, "pausing a completed execution must fail")
	assert.Nil(t, result)

	// Must be a 409 client error, not a 500.
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 409, ce.code, "ineligible-status pause must return 409")
	assert.Contains(t, ce.message, "cannot be paused", "error message must name the action")
}

func TestHandler_resumePlannedPurchase_NilExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("TransitionExecutionStatus", ctx, "99999999-9999-9999-9999-999999999999", []string{"paused"}, "pending").Return(nil, fmt.Errorf("execution not found: 99999999-9999-9999-9999-999999999999"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.resumePlannedPurchase(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_runPlannedPurchase_NilExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("TransitionExecutionStatus", ctx, "99999999-9999-9999-9999-999999999999", []string{"pending", "paused"}, "running").Return(nil, fmt.Errorf("execution not found: 99999999-9999-9999-9999-999999999999"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.runPlannedPurchase(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_deletePlannedPurchase_NilExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("TransitionExecutionStatus", ctx, "99999999-9999-9999-9999-999999999999", []string{"pending", "paused"}, "cancelled").Return(nil, fmt.Errorf("execution not found: 99999999-9999-9999-9999-999999999999"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.deletePlannedPurchase(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_getPlannedPurchases_ErrorGettingPlans(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	executions := []config.PurchaseExecution{{ExecutionID: "11111111-1111-1111-1111-111111111111", PlanID: "11111111-1111-1111-1111-111111111111"}}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetPlannedExecutions", ctx, mock.Anything, mock.Anything).Return(executions, nil)
	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return(nil, errors.New("database error"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPlannedPurchases(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to get purchase plans")
}

// Tests for getPurchaseDetails

func TestHandler_getPurchaseDetails_Success(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	scheduledDate := time.Now().AddDate(0, 0, 7)
	execution := &config.PurchaseExecution{
		ExecutionID:      "11111111-1111-1111-1111-111111111111",
		PlanID:           "22222222-2222-2222-2222-222222222222",
		Status:           "pending",
		StepNumber:       1,
		ScheduledDate:    scheduledDate,
		TotalUpfrontCost: 1000.0,
		EstimatedSavings: 500.0,
	}

	plan := &config.PurchasePlan{
		ID:   "22222222-2222-2222-2222-222222222222",
		Name: "Test Plan",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetExecutionByID", ctx, "11111111-1111-1111-1111-111111111111").Return(execution, nil)
	mockStore.On("GetPurchasePlan", ctx, "22222222-2222-2222-2222-222222222222").Return(plan, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPurchaseDetails(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	resultMap := result.(map[string]interface{})
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", resultMap["execution_id"])
	assert.Equal(t, "22222222-2222-2222-2222-222222222222", resultMap["plan_id"])
	assert.Equal(t, "Test Plan", resultMap["plan_name"])
	assert.Equal(t, "pending", resultMap["status"])
	assert.Equal(t, 1, resultMap["step_number"])
	assert.Equal(t, 1000.0, resultMap["total_upfront_cost"])
	assert.Equal(t, 500.0, resultMap["estimated_savings"])
}

func TestHandler_getPurchaseDetails_InvalidUUID(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPurchaseDetails(ctx, req, "invalid-uuid")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid ID format")
}

func TestHandler_getPurchaseDetails_NotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetExecutionByID", ctx, "99999999-9999-9999-9999-999999999999").Return(nil, errors.New("not found"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPurchaseDetails(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "execution not found")
}

func TestHandler_getPurchaseDetails_NilExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetExecutionByID", ctx, "99999999-9999-9999-9999-999999999999").Return(nil, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPurchaseDetails(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "execution not found")
}

func TestHandler_getPurchaseDetails_WithTimestamps(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	scheduledDate := time.Now().AddDate(0, 0, 7)
	notificationSent := time.Now().AddDate(0, 0, -1)
	completedAt := time.Now()
	execution := &config.PurchaseExecution{
		ExecutionID:      "11111111-1111-1111-1111-111111111111",
		PlanID:           "22222222-2222-2222-2222-222222222222",
		Status:           "completed",
		StepNumber:       1,
		ScheduledDate:    scheduledDate,
		NotificationSent: &notificationSent,
		CompletedAt:      &completedAt,
		Error:            "some error",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetExecutionByID", ctx, "11111111-1111-1111-1111-111111111111").Return(execution, nil)
	mockStore.On("GetPurchasePlan", ctx, "22222222-2222-2222-2222-222222222222").Return(nil, errors.New("not found"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPurchaseDetails(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	resultMap := result.(map[string]interface{})
	assert.Equal(t, "completed", resultMap["status"])
	assert.NotNil(t, resultMap["notification_sent"])
	assert.NotNil(t, resultMap["completed_at"])
	assert.Equal(t, "some error", resultMap["error"])
}

// Tests for executePurchase

func TestHandler_executePurchase_Success(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	// executePurchase reads GlobalConfig to look up the per-provider
	// grace period. Return an empty-but-valid config so the grace
	// window falls back to defaults and no suppression rows get
	// written (the recs in this request have no CloudAccountID).
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	// The #644 idempotency lookup queries pending executions before creating.
	// No prior pending row → not a duplicate → proceeds to create.
	mockStore.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{}, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"recommendations": [{"id": "rec-1", "provider": "aws", "service": "ec2", "count": 1, "term": 1, "payment": "all-upfront", "upfront_cost": 100.0, "savings": 50.0}, {"id": "rec-2", "provider": "aws", "service": "ec2", "count": 2, "term": 1, "payment": "all-upfront", "upfront_cost": 200.0, "savings": 100.0}]}`,
	}
	result, err := handler.executePurchase(ctx, req)
	require.NoError(t, err)

	resultMap := result.(map[string]interface{})
	// With no emailNotifier wired on the handler the approval email cannot
	// send, and the execution is dead on arrival (no one can ever approve
	// it — the token only lives in the email). The handler flips the status
	// from "pending" to "failed" so the History view shows it correctly
	// instead of parking it in Pending forever.
	assert.Equal(t, "failed", resultMap["status"])
	assert.Equal(t, 2, resultMap["recommendation_count"])
	assert.Equal(t, 300.0, resultMap["total_upfront_cost"])
	assert.Equal(t, 150.0, resultMap["estimated_savings"])
	assert.NotEmpty(t, resultMap["execution_id"])
	assert.Equal(t, false, resultMap["email_sent"], "email_sent must be false when emailNotifier is nil")
	assert.Equal(t, "email notifier not configured for this deployment", resultMap["email_reason"])
}

func TestHandler_executePurchase_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `invalid json`,
	}
	result, err := handler.executePurchase(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_executePurchase_EmptyRecommendations(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"recommendations": []}`,
	}
	result, err := handler.executePurchase(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no recommendations provided")
}

func TestHandler_executePurchase_NegativeUpfrontCost(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"recommendations": [{"id": "rec-1", "provider": "aws", "service": "ec2", "count": 1, "term": 1, "payment": "all-upfront", "upfront_cost": -100.0, "savings": 50.0}]}`,
	}
	result, err := handler.executePurchase(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "negative upfront cost")
}

func TestHandler_executePurchase_NegativeSavings(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"recommendations": [{"id": "rec-1", "provider": "aws", "service": "ec2", "count": 1, "term": 1, "payment": "all-upfront", "upfront_cost": 100.0, "savings": -50.0}]}`,
	}
	result, err := handler.executePurchase(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "negative savings")
}

func TestHandler_executePurchase_TooManyRecommendations(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{auth: mockAuth}

	// Create JSON with 1001 recommendations (exceeds max of 1000)
	recommendations := make([]map[string]interface{}, 1001)
	for i := range recommendations {
		recommendations[i] = map[string]interface{}{
			"id":           fmt.Sprintf("rec-%d", i),
			"upfront_cost": 1.0,
			"savings":      0.5,
		}
	}
	body, _ := json.Marshal(map[string]interface{}{"recommendations": recommendations})

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: string(body),
	}
	result, err := handler.executePurchase(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "too many recommendations")
}

func TestHandler_executePurchase_ExceedsMaxAmount(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"recommendations": [{"id": "rec-1", "provider": "aws", "service": "ec2", "count": 1, "term": 1, "payment": "all-upfront", "upfront_cost": 15000000.0, "savings": 50.0}]}`,
	}
	result, err := handler.executePurchase(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "exceeds maximum allowed")
}

func TestHandler_executePurchase_SaveError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(errors.New("database error"))
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	mockStore.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{}, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"recommendations": [{"id": "rec-1", "provider": "aws", "service": "ec2", "count": 1, "term": 1, "payment": "all-upfront", "upfront_cost": 100.0, "savings": 50.0}]}`,
	}
	result, err := handler.executePurchase(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to save execution")
}

// TestHandler_pausePlannedPurchase_OutOfScope locks down that a non-admin
// user whose allowed_accounts do not intersect with the execution's plan
// gets 404 and never reaches TransitionExecutionStatus. Covers the
// requireExecutionAccess hop added in the plans/purchases scoping commit.
func TestHandler_pausePlannedPurchase_OutOfScope(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	executionID := "77777777-7777-7777-7777-777777777777"
	planID := "88888888-8888-8888-8888-888888888888"

	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(&Session{
		UserID: "viewer-1",
	}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "update", "purchases").Return(true, nil)
	mockAuth.On("GetAllowedAccountsAPI", ctx, "viewer-1").Return([]string{"Production"}, nil)
	mockStore.On("GetExecutionByID", ctx, executionID).Return(&config.PurchaseExecution{
		ExecutionID: executionID, PlanID: planID,
	}, nil)

	store := &mockStoreWithPlanAccounts{
		MockConfigStore: mockStore,
		planAccounts: map[string][]config.CloudAccount{
			planID: {{ID: "acc-stage", Name: "Staging"}},
		},
	}

	handler := &Handler{auth: mockAuth, config: store}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer viewer-token"},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Path: "/api/purchases/planned/" + executionID + "/pause",
			},
		},
	}
	_, err := handler.pausePlannedPurchase(ctx, req, executionID)
	require.Error(t, err)
	assert.True(t, IsNotFoundError(err), "expected 404 not-found, got %v", err)
	mockStore.AssertNotCalled(t, "TransitionExecutionStatus")
}

// ─── Session-authed Cancel (issue #46) ─────────────────────────────────────
//
// Covers the full cancel-any / cancel-own RBAC matrix on the
// session-authed branch of cancelPurchase (token == ""):
//
//   1. admin                                     → allowed (any execution)
//   2. user with cancel-any (e.g. ops role)      → allowed (any execution)
//   3. user with cancel-own + matching creator   → allowed
//   4. user with cancel-own + different creator  → 403
//   5. user with neither verb                    → 403
//   6. cancellable-state guard                   → 409 on non-pending status
//   7. legacy NULL creator + non-admin cancel-own → 403 (still reachable
//      via the email-token path, which is exercised by the existing
//      TestHandler_cancelPurchase happy-path test).

const cancelExecID = "55555555-5555-5555-5555-555555555555"
const cancelCallerID = "66666666-6666-6666-6666-666666666666"
const cancelOtherID = "77777777-7777-7777-7777-777777777777"

// buildSessionCancelHandler wires the handler with mocks the session-authed
// cancel tests share. Token is left empty by callers when invoking
// cancelPurchase to drive the new branch.
func buildSessionCancelHandler(exec *config.PurchaseExecution, session *Session, hasAny, hasOwn bool) (*Handler, *MockConfigStore, *MockAuthService) {
	mockConfig := new(MockConfigStore)
	mockConfig.On("GetExecutionByID", mock.Anything, exec.ExecutionID).Return(exec, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", mock.Anything, "sess-tok").Return(session, nil)
	// cancelPurchaseViaSession enforces CSRF on the session-authed path (issue
	// #404). sessionCancelReq carries no CSRF header, so ValidateCSRFToken is
	// called with an empty csrfToken. Tests exercising the session-authed cancel
	// branch must allow this call; deny tests that only exercise the RBAC check
	// may never reach ValidateCSRFToken if the session is nil.
	mockAuth.On("ValidateCSRFToken", mock.Anything, "sess-tok", "").Return(nil).Maybe()
	// Authorization is permission-based for every caller now (issue #907): even
	// an Administrators-group member resolves cancel-any/cancel-own through
	// HasPermissionAPI, so register the permission mocks unconditionally.
	if session != nil {
		mockAuth.On("HasPermissionAPI", mock.Anything, session.UserID, "cancel-any", "purchases").Return(hasAny, nil).Maybe()
		mockAuth.On("HasPermissionAPI", mock.Anything, session.UserID, "cancel-own", "purchases").Return(hasOwn, nil).Maybe()
	}

	return &Handler{config: mockConfig, auth: mockAuth}, mockConfig, mockAuth
}

func sessionCancelReq() *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
}

// runSessionCancelAllowed asserts the success path of the session-authed
// branch given a permission-matrix cell that should be allowed. The
// cancel commits in a single tx via CancelExecutionAtomic +
// DeleteSuppressionsByExecutionTx; the mock store's WithTx default
// forwards fn(nil) and CancelExecutionAtomic default returns
// (true, "cancelled", nil) when no explicit expectation is registered.
//
// Asserts the audit-stamp invariant: when session.Email is non-empty
// the cancelledBy pointer passed to CancelExecutionAtomic must carry
// that email so the DB column is stamped correctly for History UI
// attribution.
func runSessionCancelAllowed(t *testing.T, exec *config.PurchaseExecution, session *Session, hasAny, hasOwn bool) {
	t.Helper()
	handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, hasAny, hasOwn)

	// Capture the cancelledBy pointer passed to CancelExecutionAtomic
	// so we can assert attribution was stamped correctly.
	var capturedCancelledBy *string
	mockConfig.On("CancelExecutionAtomic", mock.Anything, mock.Anything, cancelExecID, mock.Anything).
		Run(func(args mock.Arguments) {
			if v, ok := args.Get(3).(*string); ok {
				capturedCancelledBy = v
			}
		}).
		Return(true, "cancelled", nil)
	// When cancel succeeds the transaction must also clean up suppressions.
	mockConfig.On("DeleteSuppressionsByExecutionTx", mock.Anything, mock.Anything, cancelExecID).
		Return(nil)

	result, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "")
	require.NoError(t, err)
	assert.Equal(t, "cancelled", result.(map[string]string)["status"])
	// Verify the atomic cancel was called — this is the primary guard against
	// regressions that skip the conditional UPDATE.
	mockConfig.AssertCalled(t, "CancelExecutionAtomic", mock.Anything, mock.Anything, cancelExecID, mock.Anything)
	// Verify suppression cleanup ran within the same transaction.
	mockConfig.AssertCalled(t, "DeleteSuppressionsByExecutionTx", mock.Anything, mock.Anything, cancelExecID)
	if session != nil && session.Email != "" {
		require.NotNil(t, capturedCancelledBy, "cancelledBy must be stamped when session has an email")
		assert.Equal(t, session.Email, *capturedCancelledBy, "cancelledBy must equal session.Email for audit attribution")
	}
	// Verify the session-auth boundary actually fired — without this a
	// regression that bypassed ValidateSession (or stopped consulting
	// HasPermissionAPI for non-admins) would silently still pass.
	mockAuth.AssertExpectations(t)
}

func TestHandler_cancelPurchase_Session_Admin_AllowsAny(t *testing.T) {
	creator := cancelOtherID
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "pending",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: cancelCallerID, Email: "admin@example.com"}
	// Admin == Administrators-group member, modelled as a cancel-any holder
	// (issue #907 removed the role short-circuit); the row belongs to another
	// user, so cancel-any is what authorises the action.
	runSessionCancelAllowed(t, exec, session, true, false)
}

func TestHandler_cancelPurchase_Session_CancelAny_AllowsAny(t *testing.T) {
	// Non-admin operator role with cancel-any:purchases. Future use case
	// (no role currently has it by default) but the verb exists today.
	creator := cancelOtherID
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "pending",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: cancelCallerID, Email: "ops@example.com"}
	runSessionCancelAllowed(t, exec, session, true, false)
}

func TestHandler_cancelPurchase_Session_CancelOwn_AllowsCreator(t *testing.T) {
	creator := cancelCallerID // execution created by the same user
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "notified",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: cancelCallerID, Email: "u1@example.com"}
	runSessionCancelAllowed(t, exec, session, false, true)
}

func TestHandler_cancelPurchase_Session_CancelOwn_RejectsNonCreator(t *testing.T) {
	creator := cancelOtherID // someone else created it
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "pending",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: cancelCallerID, Email: "u1@example.com"}

	handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, false, true)

	_, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another user's pending purchase")
	mockConfig.AssertNotCalled(t, "WithTx")
	mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
	mockAuth.AssertExpectations(t)
}

func TestHandler_cancelPurchase_Session_NoVerb_Rejects(t *testing.T) {
	creator := cancelCallerID // even own row is rejected without the verb
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "pending",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: cancelCallerID, Email: "u1@example.com"}

	handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, false, false)

	_, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cancel-any or cancel-own")
	mockConfig.AssertNotCalled(t, "WithTx")
	mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
	mockAuth.AssertExpectations(t)
}

func TestHandler_cancelPurchase_Session_RejectsTerminalStatus(t *testing.T) {
	creator := cancelCallerID
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "completed", // already done — cannot transition
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: cancelCallerID}

	handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, false, false)

	_, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be cancelled")
	assert.Contains(t, err.Error(), "completed")
	mockConfig.AssertNotCalled(t, "WithTx")
	mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
	mockAuth.AssertExpectations(t)
}

// TestHandler_cancelPurchase_Session_RejectsEachNonCancelableStatus is the
// session-path companion to the token-path #645 regression guard: every
// status outside pending/notified must be rejected with a 409 and no write,
// for parity with purchase.Manager.CancelExecution. The admin session keeps
// the focus on the status guard (which fires before authorizeSessionCancel)
// rather than the RBAC matrix, already covered by the matrix tests above.
func TestHandler_cancelPurchase_Session_RejectsEachNonCancelableStatus(t *testing.T) {
	rejected := []string{"approved", "running", "paused", "failed", "expired", "completed", "cancelled"}
	for _, status := range rejected {
		t.Run(status, func(t *testing.T) {
			creator := cancelCallerID
			exec := &config.PurchaseExecution{
				ExecutionID:     cancelExecID,
				Status:          status,
				CreatedByUserID: &creator,
			}
			session := &Session{UserID: cancelCallerID}

			handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, false, false)

			_, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "cannot be cancelled")
			assert.Contains(t, err.Error(), status)
			mockConfig.AssertNotCalled(t, "WithTx")
			mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
			mockAuth.AssertExpectations(t)
		})
	}
}

// TestHandler_cancelPurchase_Session_AllowsEachCancelableStatus confirms the
// inverse: pending and notified rows remain cancelable on the session path,
// guarding against an over-restriction that would break the dashboard cancel
// of a row awaiting approval.
func TestHandler_cancelPurchase_Session_AllowsEachCancelableStatus(t *testing.T) {
	allowed := []string{"pending", "notified"}
	for _, status := range allowed {
		t.Run(status, func(t *testing.T) {
			creator := cancelCallerID
			exec := &config.PurchaseExecution{
				ExecutionID:     cancelExecID,
				Status:          status,
				CreatedByUserID: &creator,
			}
			session := &Session{UserID: cancelCallerID, Email: "admin@example.com"}
			// Caller owns the row (creator == cancelCallerID); cancel-own
			// authorises it (issue #907 group-only authz).
			runSessionCancelAllowed(t, exec, session, false, true)
		})
	}
}

// TestHandler_cancelPurchase_Session_RaceWithApprove is the regression
// guard for issue #671 on the session-authed cancel path. When a concurrent
// approve transitions the execution out of pending/notified before the
// conditional UPDATE runs, CancelExecutionAtomic returns
// (false, "approved", nil) and the handler must 409 with the racing status
// rather than silently overwriting the approved row.
func TestHandler_cancelPurchase_Session_RaceWithApprove(t *testing.T) {
	creator := cancelCallerID
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "pending", // status at fetch time
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: cancelCallerID, Email: "admin@example.com"}

	// Caller owns the row; cancel-own authorises it (issue #907).
	handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, false, true)
	// Simulate concurrent approve winning between IsCancelable check and
	// the conditional UPDATE inside the tx.
	mockConfig.On("CancelExecutionAtomic", mock.Anything, mock.Anything, cancelExecID, mock.Anything).
		Return(false, "approved", nil)

	_, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "")
	require.Error(t, err)
	var ce *clientError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, ce.message, "approved", "409 body must surface the racing status")
	assert.Contains(t, ce.message, "concurrent", "409 body must mention the concurrent operation")
	// Suppression cleanup must NOT have been called because the atomic
	// UPDATE returned zero rows — the approve path owns the execution now.
	mockConfig.AssertNotCalled(t, "DeleteSuppressionsByExecutionTx", mock.Anything, mock.Anything, mock.Anything)
	mockAuth.AssertExpectations(t)
}

func TestHandler_cancelPurchase_Session_LegacyNullCreator_NonAdminRejected(t *testing.T) {
	// Pre-migration row: created_by_user_id is NULL. cancel-own can't
	// match a NULL creator, so a non-admin must be rejected. The email
	// token in the inbox stays the escape hatch (covered by the existing
	// TestHandler_cancelPurchase happy-path test).
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "pending",
		CreatedByUserID: nil,
	}
	session := &Session{UserID: cancelCallerID, Email: "u1@example.com"}

	handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, false, true)

	_, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another user's pending purchase")
	mockConfig.AssertNotCalled(t, "WithTx")
	mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
	mockAuth.AssertExpectations(t)
}

func TestHandler_cancelPurchase_Session_RejectsMissingSession(t *testing.T) {
	exec := &config.PurchaseExecution{ExecutionID: cancelExecID, Status: "pending"}
	mockConfig := new(MockConfigStore)
	mockConfig.On("GetExecutionByID", mock.Anything, cancelExecID).Return(exec, nil)

	handler := &Handler{config: mockConfig, auth: new(MockAuthService)}
	// No Authorization header → CSRF fires first (issue #404: cancelPurchaseViaSession
	// now enforces CSRF before requireSession). A tokenless request can't provide a
	// valid CSRF binding, so we get a 403 "CSRF validation failed".
	_, err := handler.cancelPurchase(context.Background(), &events.LambdaFunctionURLRequest{}, cancelExecID, "")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError, got: %v", err)
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, ce.Error(), "CSRF validation failed")
}

// TestHandler_cancelPurchase_DeepLink_AdminBypassesContactEmailGate is the
// regression for the cancel-from-email contact_email-gate UX bug:
//
//	"Failed to cancel purchase: no per-account contact email configured for
//	 this execution; set the cloud account's contact_email before approving"
//
// The deep-link cancel flow (frontend purchases-deeplink.ts) always POSTs
// /api/purchases/cancel/:id with both an X-Authorization Bearer session AND
// the email-link's URL token. Before this fix, cancelPurchase always took
// the token branch → authorizeApprovalAction → 403 when the execution's
// recs had no per-account contact_email (e.g. AWS ambient-credentials
// rows where CloudAccountID is nil). The same admin could already cancel
// the same execution from the History page Cancel button (session-authed,
// no contact_email gate); the email-link path was the only place they
// were locked out.
//
// Fix: when the caller has a valid session AND authorizeSessionCancel
// passes (admin / cancel-any / cancel-own match), use the session-authed
// cancel path, regardless of whether a token is present. Token-only
// callers (no session, e.g. forwarded email or shared inbox) still hit
// the contact_email gate from PR #101.
func TestHandler_cancelPurchase_DeepLink_AdminBypassesContactEmailGate(t *testing.T) {
	// Ambient-credentials execution: CloudAccountID is nil, so
	// gatherAccountContactEmails returns []. Pre-fix this would 403 even
	// for admin sessions. Post-fix the session-authed pre-check fires and
	// the cancel succeeds.
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "notified",
		CreatedByUserID: nil, // no creator binding either; admin can still cancel
		Recommendations: []config.RecommendationRecord{
			{ID: "r-ambient", CloudAccountID: nil},
		},
	}
	session := &Session{UserID: cancelCallerID, Email: "admin@example.com"}

	// "Admin" is now an Administrators-group member, i.e. a holder of the
	// cancel-any permission (issue #907). The contact-email-gate bypass under
	// test is independent of how that authority is derived.
	handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, true, false)

	// Capture cancelledBy to verify the audit-stamp is passed to the
	// atomic UPDATE.
	var capturedCancelledBy *string
	mockConfig.On("CancelExecutionAtomic", mock.Anything, mock.Anything, cancelExecID, mock.Anything).
		Run(func(args mock.Arguments) {
			if v, ok := args.Get(3).(*string); ok {
				capturedCancelledBy = v
			}
		}).
		Return(true, "cancelled", nil)

	// Token IS present in the URL — the deep-link flow always sends one.
	// The fix's whole point is that the admin session takes the
	// session-authed branch instead of routing through the token path.
	result, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "deep-link-token")
	require.NoError(t, err, "admin clicking Cancel from notification email must succeed even when no contact_email is configured")
	assert.Equal(t, "cancelled", result.(map[string]string)["status"])

	require.NotNil(t, capturedCancelledBy, "session-authed branch must stamp cancelledBy")
	assert.Equal(t, session.Email, *capturedCancelledBy)

	// Critical security assertion: the token branch's contact_email gate
	// (authorizeApprovalAction -> GetGlobalConfig -> resolveApprovalRecipients)
	// was NOT consulted. If a regression re-routed admins through the
	// token path, GetGlobalConfig would fire because the gate fetches
	// the global notification email; asserting it didn't is the cleanest
	// way to pin the new branch behaviour.
	mockConfig.AssertNotCalled(t, "GetGlobalConfig", mock.Anything)
	mockAuth.AssertExpectations(t)
}

// TestHandler_cancelPurchase_DeepLink_CancelOwnBypassesContactEmailGate is
// the cancel-own variant of the regression above: a non-admin user with
// cancel-own on a purchase they themselves created should be able to
// cancel it from the email link, bypassing the contact_email gate that
// would otherwise lock them out for ambient-credentials executions.
func TestHandler_cancelPurchase_DeepLink_CancelOwnBypassesContactEmailGate(t *testing.T) {
	creator := cancelCallerID // session user is the creator
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "notified",
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{
			{ID: "r-ambient", CloudAccountID: nil},
		},
	}
	session := &Session{UserID: cancelCallerID, Email: "u1@example.com"}

	handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, false /*hasAny*/, true /*hasOwn*/)
	// CancelExecutionAtomic is called by the session-authed branch.
	mockConfig.On("CancelExecutionAtomic", mock.Anything, mock.Anything, cancelExecID, mock.Anything).
		Return(true, "cancelled", nil)

	result, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "deep-link-token")
	require.NoError(t, err)
	assert.Equal(t, "cancelled", result.(map[string]string)["status"])
	mockConfig.AssertNotCalled(t, "GetGlobalConfig", mock.Anything)
	mockAuth.AssertExpectations(t)
}

// TestIsPermissionDenied pins the strict (un-wrapped) type-assertion
// invariant from CR pass 2 on PR #216: a wrapped 403 ClientError must
// NOT count as permission-denied because the wrapper changes the
// failure's outer category. Only an *exact* *clientError with code 403
// triggers the fall-through to the contact_email gate.
func TestIsPermissionDenied(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error is not denial", nil, false},
		{"plain 403 ClientError is denial", NewClientError(403, "permission denied"), true},
		{"500 ClientError is not denial", NewClientError(500, "auth service down"), false},
		{"401 ClientError is not denial", NewClientError(401, "no session"), false},
		{"non-ClientError is not denial", errors.New("auth backend timeout"), false},
		{
			name: "wrapped 403 is NOT denial (errors.As-style unwrap is rejected)",
			err:  fmt.Errorf("permission check failed: %w", NewClientError(403, "denied")),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isPermissionDenied(tc.err))
		})
	}
}

// TestHandler_cancelPurchase_DeepLink_TransientAuthErrorPropagates pins the
// CR-feedback hardening on PR #216: when authorizeSessionCancel returns a
// non-403 error (auth service down, HasPermissionAPI wrapped error,
// h.auth nil), the pre-check MUST surface it instead of silently falling
// through to the contact_email gate. A stale auth backend should not
// disguise itself as a "set the contact_email" 403, which would mislead
// operators investigating the failure.
func TestHandler_cancelPurchase_DeepLink_TransientAuthErrorPropagates(t *testing.T) {
	creator := cancelOtherID
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "notified",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: cancelCallerID, Email: "u1@example.com"}

	mockConfig := new(MockConfigStore)
	mockConfig.On("GetExecutionByID", mock.Anything, exec.ExecutionID).Return(exec, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", mock.Anything, "sess-tok").Return(session, nil)
	// Simulate a transient auth-backend failure on the cancel-any check.
	// authorizeSessionCancel wraps this as "permission check failed: …"
	// — NOT a 403 ClientError. The pre-check must propagate.
	mockAuth.On("HasPermissionAPI", mock.Anything, session.UserID, "cancel-any", "purchases").
		Return(false, errors.New("auth backend timeout"))

	handler := &Handler{config: mockConfig, auth: mockAuth}

	_, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "deep-link-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission check failed",
		"transient auth-service errors must surface, not silently route to the contact_email gate")
	assert.NotContains(t, err.Error(), "contact email",
		"the contact_email message would mislead operators about the actual failure cause")

	// Token branch must NOT have been reached — GetGlobalConfig is the
	// signature first call inside authorizeApprovalAction.
	mockConfig.AssertNotCalled(t, "GetGlobalConfig", mock.Anything)
	mockAuth.AssertExpectations(t)
}

// TestHandler_cancelPurchase_DeepLink_NonPrivilegedSessionStillHitsContactGate
// pins the security-model invariant from PR #101: a logged-in user
// without admin / cancel-any / cancel-own permission MUST still go
// through authorizeApprovalAction and hit the contact_email gate when
// clicking a forwarded cancel link. The session-authed pre-check is
// strictly additive — it widens for privileged users only.
func TestHandler_cancelPurchase_DeepLink_NonPrivilegedSessionStillHitsContactGate(t *testing.T) {
	creator := cancelOtherID // someone else created it; no cancel-own match
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		ApprovalToken:   "deep-link-token",
		Status:          "notified",
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{
			{ID: "r-ambient", CloudAccountID: nil},
		},
	}
	session := &Session{UserID: cancelCallerID, Email: "u1@example.com"}

	handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, false /*hasAny*/, false /*hasOwn*/)
	// Token branch fetches the global config to populate the Cc list.
	mockConfig.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil)

	_, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "deep-link-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no per-account contact email configured for this execution",
		"non-privileged session must fall through to the contact_email gate from PR #101")
	mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
	mockAuth.AssertExpectations(t)
}

// ─── Session-authed Retry (issue #47) ──────────────────────────────────────
//
// Mirror image of the cancel matrix above. retryPurchase creates a NEW
// execution from the failed row's stored Recommendations slice and stamps
// retry_execution_id on the original.
//
// Covered cells:
//   1. admin                                     → allowed (any failed row)
//   2. user with retry-any (operator role)       → allowed (any failed row)
//   3. user with retry-own + matching creator    → allowed
//   4. user with retry-own + different creator   → 403
//   5. user with neither verb                    → 403
//   6. failed-state guard                        → 409 on non-failed status
//   7. legacy NULL creator + non-admin retry-own → 403
//   8. persistent-failure block                  → 409 + ops_hint
//   9. threshold soft-block                      → 409 (n=5, no force)
//  10. threshold soft-block + force=true         → allowed (n=5, force=true)
//  11. just-under threshold                      → allowed (n=4)
//  12. happy path: linkage + retry_attempt_n+1

const retryExecID = "88888888-8888-8888-8888-888888888888"
const retryCallerID = "99999999-9999-9999-9999-999999999999"
const retryOtherID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

func buildSessionRetryHandler(failed *config.PurchaseExecution, session *Session, hasAny, hasOwn bool) (*Handler, *MockConfigStore, *MockAuthService) {
	mockConfig := new(MockConfigStore)
	mockConfig.On("GetExecutionByID", mock.Anything, failed.ExecutionID).Return(failed, nil)
	// GetGlobalConfig is consulted for grace-period suppressions; an
	// empty config is fine (no suppressions written).
	mockConfig.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil).Maybe()

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", mock.Anything, "sess-tok").Return(session, nil)
	// Permission-based for every caller (issue #907): register unconditionally.
	if session != nil {
		mockAuth.On("HasPermissionAPI", mock.Anything, session.UserID, "retry-any", "purchases").Return(hasAny, nil).Maybe()
		mockAuth.On("HasPermissionAPI", mock.Anything, session.UserID, "retry-own", "purchases").Return(hasOwn, nil).Maybe()
	}

	return &Handler{config: mockConfig, auth: mockAuth}, mockConfig, mockAuth
}

func sessionRetryReq() *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
}

func sessionRetryReqWithForce() *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers:               map[string]string{"authorization": "Bearer sess-tok"},
		QueryStringParameters: map[string]string{"force": "true"},
	}
}

// runSessionRetryAllowed asserts the success path of retryPurchase given a
// permission-matrix cell that should be allowed. Captures BOTH saves —
// the new successor execution AND the original failed row updated with
// the linkage pointer — so callers can assert linkage invariants
// (retry_attempt_n stamped to predecessor.n+1, RetryExecutionID on the
// original points at the successor).
func runSessionRetryAllowed(t *testing.T, failed *config.PurchaseExecution, session *Session, hasAny, hasOwn bool, req *events.LambdaFunctionURLRequest) (newExec, updatedOriginal *config.PurchaseExecution) {
	t.Helper()
	handler, mockConfig, mockAuth := buildSessionRetryHandler(failed, session, hasAny, hasOwn)
	saved := []*config.PurchaseExecution{}
	mockConfig.On("SavePurchaseExecution", mock.Anything, mock.AnythingOfType("*config.PurchaseExecution")).
		Run(func(args mock.Arguments) {
			// Copy so subsequent in-place mutations by the handler
			// (e.g. finalizePurchaseStatus flipping status to failed
			// when the email path errors out) don't retroactively
			// rewrite the captured record.
			snap := *args.Get(1).(*config.PurchaseExecution)
			saved = append(saved, &snap)
		}).
		Return(nil)

	result, err := handler.retryPurchase(context.Background(), req, failed.ExecutionID)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.NotEmpty(t, resp["execution_id"])
	assert.Equal(t, failed.ExecutionID, resp["original_execution"])
	require.GreaterOrEqual(t, len(saved), 2, "expected at least 2 SavePurchaseExecution calls (new + original linkage)")

	// First save is the new successor; second is the original with
	// retry_execution_id stamped. The retry tx orders them this way
	// so the FK constraint on retry_execution_id is satisfied.
	newExec = saved[0]
	updatedOriginal = saved[1]
	mockAuth.AssertExpectations(t)
	return newExec, updatedOriginal
}

func TestHandler_retryPurchase_Admin_AllowsAny(t *testing.T) {
	creator := retryOtherID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		Error:           "send failed: transient SES throttle",
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID, Email: "admin@example.com"}
	// Admin (Administrators-group member) modelled as a retry-any holder; the
	// row belongs to another user (issue #907 group-only authz).
	newExec, updated := runSessionRetryAllowed(t, failed, session, true, false, sessionRetryReq())
	assert.Equal(t, "pending", newExec.Status)
	assert.Equal(t, 1, newExec.RetryAttemptN, "fresh first retry → n=1")
	require.NotNil(t, updated.RetryExecutionID, "original must carry pointer to successor")
	assert.Equal(t, newExec.ExecutionID, *updated.RetryExecutionID)
	assert.Equal(t, "failed", updated.Status, "original keeps failed status as historical record")
}

func TestHandler_retryPurchase_RetryAny_AllowsAny(t *testing.T) {
	creator := retryOtherID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		Error:           "send failed: transient SES throttle",
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID, Email: "ops@example.com"}
	runSessionRetryAllowed(t, failed, session, true, false, sessionRetryReq())
}

func TestHandler_retryPurchase_RetryOwn_AllowsCreator(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		Error:           "send failed: SES recipient mailbox full",
		CreatedByUserID: &creator,
		RetryAttemptN:   2, // already retried twice
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID, Email: "u1@example.com"}
	newExec, updated := runSessionRetryAllowed(t, failed, session, false, true, sessionRetryReq())
	assert.Equal(t, 3, newExec.RetryAttemptN, "n=2 predecessor → n=3 successor")
	require.NotNil(t, updated.RetryExecutionID)
	assert.Equal(t, newExec.ExecutionID, *updated.RetryExecutionID)
}

func TestHandler_retryPurchase_RetryOwn_RejectsNonCreator(t *testing.T) {
	creator := retryOtherID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: retryCallerID, Email: "u1@example.com"}

	handler, mockConfig, mockAuth := buildSessionRetryHandler(failed, session, false, true)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another user's failed purchase")
	mockConfig.AssertNotCalled(t, "WithTx")
	mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
	mockAuth.AssertExpectations(t)
}

func TestHandler_retryPurchase_NoVerb_Rejects(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: retryCallerID, Email: "u1@example.com"}

	handler, mockConfig, mockAuth := buildSessionRetryHandler(failed, session, false, false)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retry-any or retry-own")
	mockConfig.AssertNotCalled(t, "WithTx")
	mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
	mockAuth.AssertExpectations(t)
}

func TestHandler_retryPurchase_RejectsNonFailedStatus(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "completed", // already done — no retry from here
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: retryCallerID}
	handler, mockConfig, _ := buildSessionRetryHandler(failed, session, false, false)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be retried")
	assert.Contains(t, err.Error(), "completed")
	mockConfig.AssertNotCalled(t, "WithTx")
}

func TestHandler_retryPurchase_LegacyNullCreator_NonAdminRejected(t *testing.T) {
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		CreatedByUserID: nil, // pre-migration row
	}
	session := &Session{UserID: retryCallerID, Email: "u1@example.com"}
	handler, mockConfig, mockAuth := buildSessionRetryHandler(failed, session, false, true)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another user's failed purchase")
	mockConfig.AssertNotCalled(t, "WithTx")
	mockAuth.AssertExpectations(t)
}

func TestHandler_retryPurchase_PersistentFailure_BlocksWithOpsHint(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		Error:           "FROM_EMAIL not configured for this deployment",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: retryCallerID}
	// Caller owns the row; retry-own authorises it (issue #907).
	handler, mockConfig, _ := buildSessionRetryHandler(failed, session, false, true)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "operator-fixable")
	// The structured details carry the ops hint so the frontend can
	// render the badge without parsing the message.
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 409, ce.code)
	require.NotNil(t, ce.Details())
	assert.Equal(t, "Set FROM_EMAIL tfvar then retry", ce.Details()["ops_hint"])
	mockConfig.AssertNotCalled(t, "WithTx")
}

func TestHandler_retryPurchase_PersistentFailure_NoMatch_AllowsRetry(t *testing.T) {
	// Transient SES throttle is NOT in the persistent-failure map →
	// retry proceeds normally. Sanity check that we don't accidentally
	// classify all SES errors as persistent.
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		Error:           "send failed: SES throttle exceeded, please retry",
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID}
	// Caller owns the row; retry-own authorises it (issue #907).
	runSessionRetryAllowed(t, failed, session, false, true, sessionRetryReq())
}

func TestHandler_retryPurchase_Threshold_BlocksAtFive_NoForce(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		RetryAttemptN:   5, // already at the threshold
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID}
	// Caller owns the row; retry-own authorises it (issue #907).
	handler, mockConfig, _ := buildSessionRetryHandler(failed, session, false, true)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "force=true")
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 409, ce.code)
	require.NotNil(t, ce.Details())
	assert.Equal(t, 5, ce.Details()["retry_attempt_n"])
	mockConfig.AssertNotCalled(t, "WithTx")
}

func TestHandler_retryPurchase_Threshold_AllowsWithForce(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		RetryAttemptN:   5,
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID}
	// Caller owns the row; retry-own authorises it (issue #907).
	newExec, _ := runSessionRetryAllowed(t, failed, session, false, true, sessionRetryReqWithForce())
	assert.Equal(t, 6, newExec.RetryAttemptN, "force=true past threshold still increments the chain count")
}

func TestHandler_retryPurchase_JustUnderThreshold_AllowsNoForce(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		RetryAttemptN:   4, // n=4 < threshold=5 → allowed
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID}
	// Caller owns the row; retry-own authorises it (issue #907).
	newExec, _ := runSessionRetryAllowed(t, failed, session, false, true, sessionRetryReq())
	assert.Equal(t, 5, newExec.RetryAttemptN)
}

func TestHandler_retryPurchase_AlreadyRetried_Rejects(t *testing.T) {
	// A failed row that already has a retry_execution_id pointer must
	// not be retried again — that would silently overwrite the linkage
	// and orphan the previous chain.
	creator := retryCallerID
	successor := "11111111-2222-3333-4444-555555555555"
	failed := &config.PurchaseExecution{
		ExecutionID:      retryExecID,
		Status:           "failed",
		CreatedByUserID:  &creator,
		RetryExecutionID: &successor,
		Recommendations:  []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID}
	// Caller owns the row; retry-own authorises it (issue #907).
	handler, mockConfig, _ := buildSessionRetryHandler(failed, session, false, true)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already retried")
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 409, ce.code)
	assert.Equal(t, successor, ce.Details()["retry_execution_id"])
	mockConfig.AssertNotCalled(t, "WithTx")
}

func TestHandler_retryPurchase_RejectsMissingSession(t *testing.T) {
	failed := &config.PurchaseExecution{ExecutionID: retryExecID, Status: "failed"}
	mockConfig := new(MockConfigStore)
	mockConfig.On("GetExecutionByID", mock.Anything, retryExecID).Return(failed, nil)
	handler := &Handler{config: mockConfig, auth: new(MockAuthService)}
	_, err := handler.retryPurchase(context.Background(), &events.LambdaFunctionURLRequest{}, retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authorization token provided")
}

// TestHandler_retryPurchase_PreservesPlanMetadata verifies that retry
// successors inherit PlanID + StepNumber from the predecessor (CR #168
// review — without this, a retried planned execution would drop out of
// plan-scoped history and lose its ramp-step attribution).
func TestHandler_retryPurchase_PreservesPlanMetadata(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		PlanID:          "plan-abc",
		StepNumber:      3,
		Status:          "failed",
		Error:           "send failed: transient SES throttle",
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID}
	// Caller owns the row; retry-own authorises it (issue #907).
	newExec, _ := runSessionRetryAllowed(t, failed, session, false, true, sessionRetryReq())
	assert.Equal(t, "plan-abc", newExec.PlanID, "successor must inherit predecessor PlanID")
	assert.Equal(t, 3, newExec.StepNumber, "successor must inherit predecessor StepNumber")
}

// TestHandler_retryPurchase_AlreadyRetried_RBACBeforeLeak verifies the
// fix to a CR #168 finding: the already-retried 409 must NOT fire for
// an unauthorized session, because doing so would leak the descendant
// execution UUID to anyone who can guess a failed-row ID. The
// authorization gate must run first and surface a 403 instead.
func TestHandler_retryPurchase_AlreadyRetried_RBACBeforeLeak(t *testing.T) {
	creator := retryOtherID // someone else owns the failed row
	successor := "11111111-2222-3333-4444-555555555555"
	failed := &config.PurchaseExecution{
		ExecutionID:      retryExecID,
		Status:           "failed",
		CreatedByUserID:  &creator,
		RetryExecutionID: &successor, // already retried
		Recommendations:  []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	// Caller is a non-admin holding NEITHER retry-any nor retry-own —
	// must hit the 403 from authorizeSessionRetry, NOT the 409 with
	// successor exposure.
	session := &Session{UserID: retryCallerID}
	handler, _, _ := buildSessionRetryHandler(failed, session, false, false)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code, "must fail with 403 (RBAC) not 409 (already-retried)")
	assert.NotContains(t, err.Error(), "already retried")
	// Most importantly: details must NOT leak the descendant UUID.
	if d := ce.Details(); d != nil {
		_, leaked := d["retry_execution_id"]
		assert.False(t, leaked, "403 must not surface retry_execution_id")
	}
}

// --- Regression tests for issue #408 (crypto/rand token in retry path) ---

// TestPersistRetryExecution_ApprovalTokenNotUUID is the regression guard for
// issue #408. Before the fix, persistRetryExecution set ApprovalToken via
// uuid.New().String(), producing a 36-char UUID (122 bits, known format).
// After the fix it uses common.GenerateApprovalToken(), producing a 64-char
// hex string (256 bits). We assert the length and character set rather than
// a specific value so the test never needs updating when entropy sources differ.
func TestPersistRetryExecution_ApprovalTokenNotUUID(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		Error:           "ses throttle",
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID, Email: "admin@example.com"}
	// Caller owns the row; retry-own authorises it (issue #907).
	newExec, _ := runSessionRetryAllowed(t, failed, session, false, true, sessionRetryReq())

	// 64 hex characters = 32 bytes = 256 bits. UUID format is 36 chars
	// (xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx). This length check is the
	// simplest distinguisher without importing crypto/rand in test code.
	assert.Len(t, newExec.ApprovalToken, 64,
		"retry approval token must be 64-char hex (256-bit crypto/rand), not a 36-char UUID")

	// Also verify it is all hex (no hyphens — UUID has 4 hyphens).
	for _, ch := range newExec.ApprovalToken {
		assert.True(t, (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f'),
			"approval token must be lowercase hex, got char %q", ch)
	}
}

// TestPersistRetryExecution_ApprovalTokenExpiresAtSet is the regression guard
// for issue #397 on the retry path: the new execution must carry a non-nil
// ApprovalTokenExpiresAt within the expected TTL window.
func TestPersistRetryExecution_ApprovalTokenExpiresAtSet(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		Error:           "ses throttle",
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID, Email: "admin@example.com"}

	before := time.Now()
	// Caller owns the row; retry-own authorises it (issue #907).
	newExec, _ := runSessionRetryAllowed(t, failed, session, false, true, sessionRetryReq())
	after := time.Now()

	require.NotNil(t, newExec.ApprovalTokenExpiresAt,
		"retry execution must have ApprovalTokenExpiresAt set (issue #397)")

	// Deadline must be strictly in the future from the test's perspective.
	assert.True(t, newExec.ApprovalTokenExpiresAt.After(after),
		"ApprovalTokenExpiresAt must be beyond now")
	// And must not exceed the TTL from before by more than a minute's slop.
	upperBound := before.Add(config.ApprovalTokenTTL).Add(time.Minute)
	assert.True(t, newExec.ApprovalTokenExpiresAt.Before(upperBound),
		"ApprovalTokenExpiresAt must not exceed ApprovalTokenTTL")
}

// --- Regression tests for issue #398 (token from POST body) ---

// TestResolveApprovalToken_PostBodyTakesPriority is the regression guard for
// issue #398. A POST with a token in the JSON body must use the body token,
// preventing the token from appearing in Lambda URL access logs (which record
// rawQueryString but not the body).
func TestResolveApprovalToken_PostBodyTakesPriority(t *testing.T) {
	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{Method: "POST"},
		},
		Body:                  `{"token":"body-token"}`,
		QueryStringParameters: map[string]string{"token": "qs-token"},
	}
	assert.Equal(t, "body-token", resolveApprovalToken(req),
		"POST body token must take priority over query-string token (issue #398)")
}

// TestResolveApprovalToken_GetFallsBackToQueryString verifies the backward-
// compat path: a GET request (e.g. a legacy email link that bypasses the
// frontend SPA) reads the token from the query string.
func TestResolveApprovalToken_GetFallsBackToQueryString(t *testing.T) {
	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{Method: "GET"},
		},
		QueryStringParameters: map[string]string{"token": "qs-token"},
	}
	assert.Equal(t, "qs-token", resolveApprovalToken(req),
		"GET request must read token from query string")
}

// TestResolveApprovalToken_PostNoBody falls back to query string when the POST
// body is empty (e.g. session-authed approve that sends no body).
func TestResolveApprovalToken_PostNoBody(t *testing.T) {
	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{Method: "POST"},
		},
		Body:                  "",
		QueryStringParameters: map[string]string{"token": "qs-token"},
	}
	assert.Equal(t, "qs-token", resolveApprovalToken(req),
		"POST with empty body must fall back to query-string token")
}

// TestResolveApprovalToken_PostBodyNoToken falls back to query string when the
// POST body exists but does not contain a "token" field.
func TestResolveApprovalToken_PostBodyNoToken(t *testing.T) {
	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{Method: "POST"},
		},
		Body:                  `{"other_field":"value"}`,
		QueryStringParameters: map[string]string{"token": "qs-token"},
	}
	assert.Equal(t, "qs-token", resolveApprovalToken(req),
		"POST body without token field must fall back to query-string token")
}

func TestResolveOpsHint(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty input", "", ""},
		{"transient SES throttle (no match)", "send failed: SES throttle exceeded", ""},
		{"FROM_EMAIL exact", "FROM_EMAIL not configured for this deployment", "Set FROM_EMAIL tfvar then retry"},
		{"SES sandbox case-insensitive", "send failed: ses sandbox active", "Move SES out of sandbox or verify recipient, then retry"},
		{"domain not verified", "send failed: SES domain not verified", "Verify SES domain in AWS console, then retry"},
		{"IAM denied", "AssumeRole error: IAM denied", "Grant the deploy role missing IAM permission, then retry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, resolveOpsHint(tt.input))
		})
	}
}

// TestHandler_approvalResponseRecipient_TrimsWhitespace verifies that a
// whitespace-only notification_email does not count as set: the contact_email
// fallback must be used instead, and no whitespace must appear in the response.
func TestHandler_approvalResponseRecipient_TrimsWhitespace(t *testing.T) {
	result := approvalResponseRecipient(" \t\n ", "contact@example.com")
	assert.Equal(t, "contact@example.com", result,
		"whitespace-only globalNotify must fall back to contact email")
}

// TestHandler_approvalResponseRecipient_TrimsNonEmptyValue verifies that when
// notification_email has surrounding whitespace the returned value is trimmed,
// so no stray spaces appear in the toast or email headers.
func TestHandler_approvalResponseRecipient_TrimsNonEmptyValue(t *testing.T) {
	result := approvalResponseRecipient("  cristi@example.com  ", "contact@example.com")
	assert.Equal(t, "cristi@example.com", result,
		"globalNotify with surrounding whitespace must be returned trimmed")
}

// --- Direct-execute path tests (issue #289) ---

// recBody is a minimal valid recommendations JSON suitable for executePurchase
// handler tests. Reused across the direct-execute suite to keep setup compact.
const directExecRecBody = `{
  "recommendations": [
    {
      "id": "rec-1",
      "provider": "aws",
      "service": "ec2",
      "count": 1,
      "term": 1,
      "payment": "all-upfront",
      "upfront_cost": 500.0,
      "savings": 100.0
    }
  ],
  "execute_mode": "direct"
}`

// setupDirectExecMocks wires the minimal store mocks needed by the
// executePurchase handler up to (but not including) the email/execute branch.
func setupDirectExecMocks(ctx context.Context, store *MockConfigStore) {
	store.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	store.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	store.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{}, nil)
}

// TestHandler_executePurchase_DirectExec_NoPermission verifies the fail-closed
// gate: a session with the base execute:purchases verb but without
// execute-any or execute-own on purchases receives a 403 when it requests
// execute_mode="direct". The handler must not fall through to the approval
// path (issue #289).
func TestHandler_executePurchase_DirectExec_NoPermission(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	userSession := &Session{
		UserID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		Email:  "user@example.com",
	}
	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
	// Base execute:purchases grant — passes the validateExecutePurchaseRequest
	// gate but does not carry execute-any or execute-own for the direct path.
	mockAuth.On("HasPermissionAPI", ctx, userSession.UserID, "execute", "purchases").Return(true, nil)
	mockAuth.On("HasPermissionAPI", ctx, userSession.UserID, "execute-any", "purchases").Return(false, nil)
	mockAuth.On("HasPermissionAPI", ctx, userSession.UserID, "execute-own", "purchases").Return(false, nil)
	// Scope check: no allowed_accounts restriction for this test.
	mockAuth.On("GetAllowedAccountsAPI", ctx, userSession.UserID).Return([]string{}, nil)
	setupDirectExecMocks(ctx, mockStore)

	handler := &Handler{config: mockStore, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer user-token"},
		Body:    directExecRecBody,
	}
	_, err := handler.executePurchase(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError")
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, ce.Error(), "execute-any or execute-own")
}

// TestHandler_executePurchase_DirectExec_ExecuteAny verifies that a session
// with execute-any:purchases can direct-execute a purchase (no ownership
// check). The handler must call ApproveAndExecute and return status=completed.
func TestHandler_executePurchase_DirectExec_ExecuteAny(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	mockPurchase := new(MockPurchaseManager)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	t.Cleanup(func() { mockPurchase.AssertExpectations(t) })

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	// execute-any grant covers admin users (Administrators-group {admin,*}
	// wildcard matches ActionExecuteAny). The old session.Role shortcut was
	// removed in issue #940 — HasPermissionAPI is now always consulted.
	// First, the outer gate: requirePermission("execute","purchases").
	mockAuth.On("HasPermissionAPI", ctx, adminSession.UserID, "execute", "purchases").Return(true, nil)
	// Then the direct-execute gate: authorizeSessionExecuteDirect("execute-any").
	mockAuth.On("HasPermissionAPI", ctx, adminSession.UserID, "execute-any", "purchases").Return(true, nil)
	// Scope check: no allowed_accounts restriction for this test.
	mockAuth.On("GetAllowedAccountsAPI", ctx, adminSession.UserID).Return([]string{}, nil)
	mockPurchase.On("ApproveAndExecute", ctx, mock.AnythingOfType("string"), adminSession.Email).Return(nil)
	setupDirectExecMocks(ctx, mockStore)

	handler := &Handler{config: mockStore, auth: mockAuth, purchase: mockPurchase}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    directExecRecBody,
	}
	result, err := handler.executePurchase(ctx, req)
	require.NoError(t, err)
	resultMap := result.(map[string]any)
	assert.Equal(t, "completed", resultMap["status"])
	assert.Equal(t, true, resultMap["direct_execute"])
	assert.Equal(t, 500.0, resultMap["total_upfront_cost"])
}

// TestHandler_executePurchase_DirectExec_ExecuteOwn_Owner verifies that a
// session with only execute-own:purchases can direct-execute when the
// execution's creator matches the session user.
func TestHandler_executePurchase_DirectExec_ExecuteOwn_Owner(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	mockPurchase := new(MockPurchaseManager)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	t.Cleanup(func() { mockPurchase.AssertExpectations(t) })

	ownerID := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	ownerSession := &Session{
		UserID: ownerID,
		Email:  "owner@example.com",
	}
	mockAuth.On("ValidateSession", ctx, "owner-token").Return(ownerSession, nil)
	mockAuth.On("HasPermissionAPI", ctx, ownerID, "execute", "purchases").Return(true, nil)
	mockAuth.On("HasPermissionAPI", ctx, ownerID, "execute-any", "purchases").Return(false, nil)
	mockAuth.On("HasPermissionAPI", ctx, ownerID, "execute-own", "purchases").Return(true, nil)
	mockAuth.On("GetAllowedAccountsAPI", ctx, ownerID).Return([]string{}, nil)
	mockPurchase.On("ApproveAndExecute", ctx, mock.AnythingOfType("string"), ownerSession.Email).Return(nil)
	setupDirectExecMocks(ctx, mockStore)

	handler := &Handler{config: mockStore, auth: mockAuth, purchase: mockPurchase}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer owner-token"},
		Body:    directExecRecBody,
	}
	result, err := handler.executePurchase(ctx, req)
	require.NoError(t, err)
	resultMap := result.(map[string]any)
	assert.Equal(t, "completed", resultMap["status"])
	assert.Equal(t, true, resultMap["direct_execute"])
}

// TestHandler_executePurchase_DirectExec_ExecuteOwn_NonOwner verifies the
// execute-own ownership gate: a session with execute-own:purchases but a
// different UserID than the execution creator receives a 403.
//
// Because executePurchase resolves the creator from the session (via
// resolveCreatorUserID), a non-owner scenario is produced by using a
// session whose UserID is non-empty and valid but differs from the creator
// that gets stamped. In practice creatorID == session.UserID always after a
// fresh submit (the creator IS the submitter), so the execute-own non-owner
// case can only arise when execute-own is misapplied to a pre-existing
// execution (not the fresh-submit path). We test the authorizeSessionExecuteDirect
// function directly to cover the non-owner branch.
func TestHandler_authorizeSessionExecuteDirect_ExecuteOwn_NonOwner(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	sessionUserID := "dddddddd-dddd-dddd-dddd-dddddddddddd"
	differentCreatorID := "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"
	session := &Session{UserID: sessionUserID}

	mockAuth.On("HasPermissionAPI", ctx, sessionUserID, "execute-any", "purchases").Return(false, nil)
	mockAuth.On("HasPermissionAPI", ctx, sessionUserID, "execute-own", "purchases").Return(true, nil)

	handler := &Handler{auth: mockAuth}
	err := handler.authorizeSessionExecuteDirect(ctx, session, differentCreatorID)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError")
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, ce.Error(), "execute-own requires you to be the creator")
}

// TestHandler_authorizeSessionExecuteDirect_AdminGroupViaExecuteAny verifies
// that an Administrators-group user whose {admin,*} wildcard resolves to
// execute-any is PERMITTED by the HasPermissionAPI path (not a dead
// session.Role shortcut that was removed in issue #940).
func TestHandler_authorizeSessionExecuteDirect_AdminGroupViaExecuteAny(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	adminUserID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	creatorID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	session := &Session{UserID: adminUserID}

	// Administrators-group wildcard {admin,*} covers execute-any.
	mockAuth.On("HasPermissionAPI", ctx, adminUserID, "execute-any", "purchases").Return(true, nil)

	handler := &Handler{auth: mockAuth}
	err := handler.authorizeSessionExecuteDirect(ctx, session, creatorID)
	require.NoError(t, err)
}

// TestHandler_authorizeSessionExecuteDirect_NoGrant verifies that a session
// without execute-any or execute-own on purchases is rejected with 403.
func TestHandler_authorizeSessionExecuteDirect_NoGrant(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	userID := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	creatorID := "dddddddd-dddd-dddd-dddd-dddddddddddd"
	session := &Session{UserID: userID}

	mockAuth.On("HasPermissionAPI", ctx, userID, "execute-any", "purchases").Return(false, nil)
	mockAuth.On("HasPermissionAPI", ctx, userID, "execute-own", "purchases").Return(false, nil)

	handler := &Handler{auth: mockAuth}
	err := handler.authorizeSessionExecuteDirect(ctx, session, creatorID)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError")
	assert.Equal(t, 403, ce.code)
}

// TestHandler_authorizeSessionExecuteDirect_NilAuth verifies that a nil auth
// component returns 500 (fail-closed per feedback_fail_closed_middleware.md).
func TestHandler_authorizeSessionExecuteDirect_NilAuth(t *testing.T) {
	ctx := context.Background()
	session := &Session{UserID: "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"}

	handler := &Handler{auth: nil}
	err := handler.authorizeSessionExecuteDirect(ctx, session, "")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError")
	assert.Equal(t, 500, ce.code)
}
