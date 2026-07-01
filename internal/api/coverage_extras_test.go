package api

// coverage_extras_test.go — additional micro-tests for the remaining ~0.6% gap.

import (
	"context"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// approvePurchase / cancelPurchase error paths
// ---------------------------------------------------------------------------

func TestHandler_approvePurchase_InvalidUUID(t *testing.T) {
	h := &Handler{}
	_, err := h.approvePurchase(context.Background(), nil, "not-uuid", "token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID format")
}

func TestHandler_approvePurchase_EmptyToken(t *testing.T) {
	// After issue #286 the empty-token path is no longer a pre-flight 400 —
	// approvePurchase dispatches three ways (session-authed RBAC,
	// token-authed legacy, session-authed fallback when token=="").
	// The contract this test pins: a malformed caller (empty token + no
	// session) must NOT silently succeed. We require an error AND
	// we fail loudly on panic — CR pass on PR #299 flagged the prior
	// `recover()` swallow as a false-green risk: a panic in any new
	// dispatch branch could pass the test without ever asserting on
	// `err`.
	//
	// Wire a minimal MockConfigStore that returns a clean "execution
	// not found" error from GetExecutionByID so the dispatch reaches a
	// proper NewClientError(404) instead of nil-deref'ing on h.config.
	// (Pre-#286 the empty-token check short-circuited at the very top
	// of approvePurchase, so a zero-Handler test was sufficient. The
	// dispatch refactor moved that check after GetExecutionByID; the
	// test surface adapts in step.)
	ctx := context.Background()
	execID := "11111111-1111-1111-1111-111111111111"
	mockConfig := new(MockConfigStore)
	mockConfig.On("GetExecutionByID", ctx, execID).Return(nil, errors.New("execution not found"))
	h := &Handler{config: mockConfig}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("approvePurchase should return an error for empty token + zero handler, not panic: %v", r)
		}
	}()
	_, err := h.approvePurchase(ctx, nil, execID, "")
	require.Error(t, err, "approvePurchase with empty token + zero handler must fail")
}

func TestHandler_approvePurchase_PurchaseError(t *testing.T) {
	ctx := context.Background()
	execID := "11111111-1111-1111-1111-111111111111"
	approver := "approver@example.com"

	mockConfig := new(MockConfigStore)
	accountID := "acct-1"
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "tok",
		Status:        "pending",
		Recommendations: []config.RecommendationRecord{
			{ID: "r1", CloudAccountID: &accountID},
		},
	}
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, ContactEmail: approver}, nil
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	// authorizeApprovalAction reads GetGlobalConfig only for Cc-recipient
	// derivation; with per-account contact_email gating, the global notify
	// mailbox no longer participates in authorization. Return an empty
	// config so the test stays minimal.
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: approver}, nil)
	// After issue #286, approvePurchase is session-first: with a Bearer
	// header present the dispatch consults the approve-{any,own} RBAC
	// matrix BEFORE falling through to the token branch. The session
	// here is the approver's mailbox (no role / no UserID), so the verb
	// checks must explicitly return false to drop into the legacy
	// token-authed branch this test exercises. (Pre-#286 the dispatch
	// went straight to the token branch and these mocks weren't needed.)
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-any", "purchases").Return(false, nil)
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-own", "purchases").Return(false, nil)

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("ApproveExecution", ctx, execID, "tok", approver).
		Return(errors.New("approval failed"))

	h := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	_, err := h.approvePurchase(ctx, req, execID, "tok")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "approval failed")
}

func TestHandler_cancelPurchase_InvalidUUID(t *testing.T) {
	h := &Handler{}
	_, err := h.cancelPurchase(context.Background(), nil, "not-uuid", "token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID format")
}

// TestHandler_cancelPurchase_EmptyToken_FallsThroughToSession asserts that
// the token-empty branch no longer short-circuits with "cancellation token
// is required" — the empty-token path is now the dispatch into the
// session-authed cancel flow (issue #46). Without an execution to load,
// GetExecutionByID is the first thing that runs; with no config wired,
// the call surfaces a downstream error rather than the legacy 400.
func TestHandler_cancelPurchase_EmptyToken_FallsThroughToSession(t *testing.T) {
	execID := "11111111-1111-1111-1111-111111111111"
	mockConfig := new(MockConfigStore)
	mockConfig.On("GetExecutionByID", mock.Anything, execID).Return(nil, errors.New("store error"))
	h := &Handler{config: mockConfig}
	_, err := h.cancelPurchase(context.Background(), nil, execID, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get execution")
}

func TestHandler_cancelPurchase_PurchaseError(t *testing.T) {
	ctx := context.Background()
	execID := "11111111-1111-1111-1111-111111111111"
	approver := "approver@example.com"

	mockConfig := new(MockConfigStore)
	accountID := "acct-1"
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "tok",
		Status:        "pending",
		Recommendations: []config.RecommendationRecord{
			{ID: "r1", CloudAccountID: &accountID},
		},
	}
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, ContactEmail: approver}, nil
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	// See approve test above — GetGlobalConfig is no longer authorization-
	// relevant; return an empty config to keep the test minimal.
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: approver}, nil)
	// Session has no admin role / cancel permissions → cancelPurchase's
	// session-authed pre-check falls through to authorizeApprovalAction →
	// purchase manager invocation, which is what this test's
	// "cancel failed" assertion exercises.
	mockAuth.On("HasPermissionAPI", ctx, "", "cancel-any", "purchases").Return(false, nil).Maybe()
	mockAuth.On("HasPermissionAPI", ctx, "", "cancel-own", "purchases").Return(false, nil).Maybe()

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("CancelExecution", ctx, execID, "tok", approver).
		Return(errors.New("cancel failed"))

	h := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	_, err := h.cancelPurchase(ctx, req, execID, "tok")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cancel failed")
}

// ---------------------------------------------------------------------------
// forgotPassword — rate-limit branch
// ---------------------------------------------------------------------------

func TestHandler_forgotPassword_RateLimitExceeded(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	rl := NewInMemoryRateLimiter()
	rl.SetLimit("forgot_password", NewRateLimitConfig(1, 60))

	// First request — consume the quota
	allowed, _ := rl.AllowWithEmail(ctx, "user@example.com", "forgot_password")
	require.True(t, allowed)

	h := &Handler{auth: mockAuth, rateLimiter: rl}
	req := &events.LambdaFunctionURLRequest{
		Body: `{"email": "user@example.com"}`,
	}

	result, err := h.forgotPassword(ctx, req)
	require.NoError(t, err)
	// Always returns success message for enumeration protection
	m := result.(map[string]string)
	assert.Contains(t, m["status"], "if the email exists")
}

// ---------------------------------------------------------------------------
// applyOverrideSlices — exercise all branches
// ---------------------------------------------------------------------------

func TestApplyOverrideSlices(t *testing.T) {
	override := &config.AccountServiceOverride{}
	req := AccountServiceOverrideRequest{
		IncludeEngines: []string{"mysql"},
		ExcludeEngines: []string{"postgres"},
		IncludeRegions: []string{"us-east-1"},
		ExcludeRegions: []string{"eu-west-1"},
		IncludeTypes:   []string{"db.t3.medium"},
		ExcludeTypes:   []string{"db.r5.large"},
	}

	applyOverrideSlices(override, req)

	assert.Equal(t, []string{"mysql"}, override.IncludeEngines)
	assert.Equal(t, []string{"postgres"}, override.ExcludeEngines)
	assert.Equal(t, []string{"us-east-1"}, override.IncludeRegions)
	assert.Equal(t, []string{"eu-west-1"}, override.ExcludeRegions)
	assert.Equal(t, []string{"db.t3.medium"}, override.IncludeTypes)
	assert.Equal(t, []string{"db.r5.large"}, override.ExcludeTypes)
}

func TestApplyOverrideSlices_NilFields(t *testing.T) {
	override := &config.AccountServiceOverride{
		IncludeEngines: []string{"existing"},
	}
	// Nil fields should not overwrite existing values
	applyOverrideSlices(override, AccountServiceOverrideRequest{})
	assert.Equal(t, []string{"existing"}, override.IncludeEngines)
}

// ---------------------------------------------------------------------------
// sendPurchaseApprovalEmail — GetGlobalConfig error path
// ---------------------------------------------------------------------------

func TestHandler_sendPurchaseApprovalEmail_ConfigError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockStore.On("GetGlobalConfig", ctx).Return(nil, errors.New("db down"))

	h := &Handler{
		config:        mockStore,
		emailNotifier: &stubEmailNotifier{},
	}
	// Should not panic; error is swallowed (non-blocking path)
	h.sendPurchaseApprovalEmail(ctx, nil, &config.PurchaseExecution{ExecutionID: "x"}, nil, 0, 0)
}

// ---------------------------------------------------------------------------
// updateRIExchangeConfig — GetGlobalConfig error after validation passes
// ---------------------------------------------------------------------------

func TestHandler_updateRIExchangeConfig_GetGlobalConfigError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetGlobalConfig", ctx).Return(nil, errors.New("db error"))

	h := &Handler{auth: mockAuth, config: mockStore}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"mode": "manual", "utilization_threshold": 50, "lookback_days": 30}`,
	}
	_, err := h.updateRIExchangeConfig(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load config")
}

func TestHandler_updateRIExchangeConfig_SaveError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	mockStore.On("SaveGlobalConfig", ctx, mock.Anything).Return(errors.New("save failed"))

	h := &Handler{auth: mockAuth, config: mockStore}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"mode": "auto", "utilization_threshold": 50, "lookback_days": 7}`,
	}
	_, err := h.updateRIExchangeConfig(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to save config")
}

// ---------------------------------------------------------------------------
// rejectRIExchange — record not found path
// ---------------------------------------------------------------------------

func TestHandler_rejectRIExchange_RecordNotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockStore.On("GetRIExchangeRecord", ctx, "11111111-1111-1111-1111-111111111111").Return(nil, nil)

	h := &Handler{config: mockStore}
	_, err := h.rejectRIExchange(ctx, "11111111-1111-1111-1111-111111111111", "tok")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestHandler_rejectRIExchange_WrongToken(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockStore.On("GetRIExchangeRecord", ctx, "11111111-1111-1111-1111-111111111111").Return(
		&config.RIExchangeRecord{ID: "11111111-1111-1111-1111-111111111111", ApprovalToken: "correct"}, nil)

	h := &Handler{config: mockStore}
	_, err := h.rejectRIExchange(ctx, "11111111-1111-1111-1111-111111111111", "wrong")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid rejection token")
}

func TestHandler_rejectRIExchange_AlreadyProcessed(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockStore.On("GetRIExchangeRecord", ctx, "11111111-1111-1111-1111-111111111111").Return(
		&config.RIExchangeRecord{ID: "11111111-1111-1111-1111-111111111111", ApprovalToken: "tok"}, nil)
	// Transition returns nil indicating already processed
	//nolint:misspell // DB schema value 'cancelled' -- see migration 000009_ri_exchange_history.up.sql
	mockStore.On("TransitionRIExchangeStatus", ctx, "11111111-1111-1111-1111-111111111111", "pending", "cancelled", mock.Anything).
		Return(nil, nil)

	h := &Handler{config: mockStore}
	_, err := h.rejectRIExchange(ctx, "11111111-1111-1111-1111-111111111111", "tok")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already processed")
}

// ---------------------------------------------------------------------------
// validateExchangeApproval — DB error path
// ---------------------------------------------------------------------------

func TestHandler_validateExchangeApproval_DBError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockStore.On("GetRIExchangeRecord", ctx, "11111111-1111-1111-1111-111111111111").
		Return(nil, errors.New("db error"))

	h := &Handler{config: mockStore}
	_, err := h.validateExchangeApproval(ctx, "11111111-1111-1111-1111-111111111111", "some-token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to look up exchange record")
}

// ---------------------------------------------------------------------------
// approveRIExchange — transition returns nil (already processed)
// ---------------------------------------------------------------------------

func TestHandler_approveRIExchange_AlreadyProcessed(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockStore.On("GetRIExchangeRecord", ctx, "11111111-1111-1111-1111-111111111111").Return(
		&config.RIExchangeRecord{ID: "11111111-1111-1111-1111-111111111111", ApprovalToken: "tok"}, nil)
	mockStore.On("TransitionRIExchangeStatus", ctx, "11111111-1111-1111-1111-111111111111", "pending", "processing", mock.Anything).
		Return(nil, nil)

	h := &Handler{config: mockStore}
	_, err := h.approveRIExchange(ctx, &events.LambdaFunctionURLRequest{}, "11111111-1111-1111-1111-111111111111", "tok")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already processed")
}

// ---------------------------------------------------------------------------
// validUUIDPtrOrNil — actor-stamp helper (issue #1009)
// ---------------------------------------------------------------------------

// TestValidUUIDPtrOrNil_ReturnsPointerForValidUUID asserts the happy-path:
// a string that parses as a UUID is passed through unchanged.
func TestValidUUIDPtrOrNil_ReturnsPointerForValidUUID(t *testing.T) {
	uid := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	result := validUUIDPtrOrNil(&uid)
	require.NotNil(t, result, "valid UUID must return non-nil pointer")
	assert.Equal(t, uid, *result)
}

// TestValidUUIDPtrOrNil_ReturnsNilForNonUUID asserts that a non-UUID string
// (e.g. "admin-api-key") returns nil so it is never used as a FK actor.
func TestValidUUIDPtrOrNil_ReturnsNilForNonUUID(t *testing.T) {
	s := "admin-api-key"
	assert.Nil(t, validUUIDPtrOrNil(&s), "non-UUID reviewer_by must map to nil actor")
}

// TestValidUUIDPtrOrNil_ReturnsNilForNilInput asserts that a nil *string
// returns nil (no panic on nil dereference).
func TestValidUUIDPtrOrNil_ReturnsNilForNilInput(t *testing.T) {
	assert.Nil(t, validUUIDPtrOrNil(nil))
}

// TestHandler_TransitionRegistrationStatus_ActorStamped drives the real
// rejectRegistration handler and asserts that TransitionRegistrationStatus is
// called with a non-nil actor equal to the reviewing admin session's UUID (the
// common human-reviewed path). Exercising the handler (not the store directly)
// keeps the test honest: it fails if the handler ever stops deriving the actor
// from the session and threading it through (CR feedback on PR #1011).
func TestHandler_TransitionRegistrationStatus_ActorStamped(t *testing.T) {
	ctx := context.Background()
	const regID = "11111111-1111-1111-1111-111111111111"
	const actorID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	reg := &config.AccountRegistration{ID: regID, Status: "pending"}
	mockStore.On("GetAccountRegistration", ctx, regID).Return(reg, nil)
	// setReviewMetadata stamps reg.ReviewedBy = session.UserID, so the actor
	// threaded into the store must equal the session UUID.
	mockStore.On("TransitionRegistrationStatus", ctx, reg, "pending",
		mock.MatchedBy(func(a *string) bool { return a != nil && *a == actorID }),
	).Return(nil)

	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{UserID: actorID, Email: "admin@example.com"}, nil)
	mockAuth.grantAdmin()

	handler := &Handler{config: mockStore, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	_, err := handler.rejectRegistration(ctx, req, regID)
	require.NoError(t, err)
}

// TestHandler_TransitionRegistrationStatus_NonUUIDActorIsNil drives the real
// rejectRegistration handler via the admin-API-key path, where requireAdmin
// returns a session whose UserID is the literal "admin-api-key" (not a UUID
// FK). The handler must pass nil as the actor so transitioned_by = NULL.
func TestHandler_TransitionRegistrationStatus_NonUUIDActorIsNil(t *testing.T) {
	ctx := context.Background()
	const regID = "22222222-2222-2222-2222-222222222222"

	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	reg := &config.AccountRegistration{ID: regID, Status: "pending"}
	mockStore.On("GetAccountRegistration", ctx, regID).Return(reg, nil)
	// API-key reviewer ("admin-api-key") is not a UUID: actor must be nil.
	mockStore.On("TransitionRegistrationStatus", ctx, reg, "pending", (*string)(nil)).Return(nil)

	handler := &Handler{config: mockStore, apiKey: "admin-secret"}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"x-api-key": "admin-secret"},
	}
	_, err := handler.rejectRegistration(ctx, req, regID)
	require.NoError(t, err)
}
