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
	h := &Handler{}
	_, err := h.approvePurchase(context.Background(), nil, "11111111-1111-1111-1111-111111111111", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "approval token is required")
}

func TestHandler_approvePurchase_PurchaseError(t *testing.T) {
	ctx := context.Background()
	execID := "11111111-1111-1111-1111-111111111111"
	approver := "approver@example.com"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "tok",
		Status:        "pending",
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &approver,
	}, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: approver}, nil)

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
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "tok",
		Status:        "pending",
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &approver,
	}, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: approver}, nil)

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
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
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
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
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
	mockStore.On("TransitionRIExchangeStatus", ctx, "11111111-1111-1111-1111-111111111111", "pending", "cancelled").
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
	mockStore.On("TransitionRIExchangeStatus", ctx, "11111111-1111-1111-1111-111111111111", "pending", "processing").
		Return(nil, nil)

	h := &Handler{config: mockStore}
	_, err := h.approveRIExchange(ctx, "11111111-1111-1111-1111-111111111111", "tok")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already processed")
}
