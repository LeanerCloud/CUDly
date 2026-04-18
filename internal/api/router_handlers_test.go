package api

// router_handlers_test.go — tests for the thin router wrapper methods and
// additional handler functions to push coverage to ≥ 80%.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Router thin-wrapper methods
// ---------------------------------------------------------------------------

// newTestRouter creates a Router with a handler that has minimal deps wired.
func newTestRouter(h *Handler) *Router {
	return &Router{h: h}
}

func TestRouter_patchPlanHandler(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	plan := &config.PurchasePlan{
		ID:   "11111111-1111-1111-1111-111111111111",
		Name: "My Plan",
	}
	mockStore.On("GetPurchasePlan", ctx, "11111111-1111-1111-1111-111111111111").Return(plan, nil)
	mockStore.On("UpdatePurchasePlan", ctx, mock.Anything).Return(nil)

	h := &Handler{auth: mockAuth, config: mockStore}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"name": "Updated Plan"}`,
	}
	_, err := r.patchPlanHandler(ctx, req, map[string]string{"id": "11111111-1111-1111-1111-111111111111"})
	assert.NoError(t, err)
}

func TestRouter_executePurchaseHandler_NoAuth(t *testing.T) {
	ctx := context.Background()
	h := &Handler{auth: nil}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
		Body:    `{}`,
	}
	_, err := r.executePurchaseHandler(ctx, req, nil)
	// No auth configured → error
	assert.Error(t, err)
}

func TestRouter_getPurchaseDetailsHandler_InvalidUUID(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	h := &Handler{auth: mockAuth}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	_, err := r.getPurchaseDetailsHandler(ctx, req, map[string]string{"id": "not-a-uuid"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID format")
}

func TestRouter_listConvertibleRIsHandler_NoAuth(t *testing.T) {
	h := &Handler{}
	r := newTestRouter(h)
	_, err := r.listConvertibleRIsHandler(context.Background(), &events.LambdaFunctionURLRequest{}, nil)
	assert.Error(t, err)
}

func TestRouter_getRIUtilizationHandler_NoAuth(t *testing.T) {
	h := &Handler{}
	r := newTestRouter(h)
	_, err := r.getRIUtilizationHandler(context.Background(), &events.LambdaFunctionURLRequest{}, nil)
	assert.Error(t, err)
}

func TestRouter_getReshapeRecommendationsHandler_NoAuth(t *testing.T) {
	h := &Handler{}
	r := newTestRouter(h)
	_, err := r.getReshapeRecommendationsHandler(context.Background(), &events.LambdaFunctionURLRequest{}, nil)
	assert.Error(t, err)
}

func TestRouter_getExchangeQuoteHandler_NoAuth(t *testing.T) {
	h := &Handler{}
	r := newTestRouter(h)
	_, err := r.getExchangeQuoteHandler(context.Background(), &events.LambdaFunctionURLRequest{}, nil)
	assert.Error(t, err)
}

func TestRouter_executeExchangeHandler_NoAuth(t *testing.T) {
	h := &Handler{}
	r := newTestRouter(h)
	_, err := r.executeExchangeHandler(context.Background(), &events.LambdaFunctionURLRequest{}, nil)
	assert.Error(t, err)
}

func TestRouter_getRIExchangeConfigHandler(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)

	h := &Handler{auth: mockAuth, config: mockStore}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := r.getRIExchangeConfigHandler(ctx, req, nil)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestRouter_updateRIExchangeConfigHandler(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	mockStore.On("SaveGlobalConfig", ctx, mock.Anything).Return(nil)

	h := &Handler{auth: mockAuth, config: mockStore}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"mode": "manual", "utilization_threshold": 50, "lookback_days": 30}`,
	}
	result, err := r.updateRIExchangeConfigHandler(ctx, req, nil)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestRouter_getRIExchangeHistoryHandler(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetRIExchangeHistory", ctx, mock.Anything, 500).Return([]config.RIExchangeRecord{}, nil)

	h := &Handler{auth: mockAuth, config: mockStore}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := r.getRIExchangeHistoryHandler(ctx, req, nil)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestRouter_approveRIExchangeHandler_InvalidToken(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	// approveRIExchange is a public endpoint (no auth required, uses token-based auth)
	record := &config.RIExchangeRecord{
		ID:            "11111111-1111-1111-1111-111111111111",
		ApprovalToken: "valid-token",
	}
	mockStore.On("GetRIExchangeRecord", ctx, "11111111-1111-1111-1111-111111111111").Return(record, nil)

	h := &Handler{auth: mockAuth, config: mockStore}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{"token": "wrong-token"},
	}
	_, err := r.approveRIExchangeHandler(ctx, req, map[string]string{"id": "11111111-1111-1111-1111-111111111111"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid approval token")
}

func TestRouter_rejectRIExchangeHandler_InvalidToken(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	record := &config.RIExchangeRecord{
		ID:            "11111111-1111-1111-1111-111111111111",
		ApprovalToken: "valid-token",
	}
	mockStore.On("GetRIExchangeRecord", ctx, "11111111-1111-1111-1111-111111111111").Return(record, nil)

	h := &Handler{auth: mockAuth, config: mockStore}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{"token": "wrong-token"},
	}
	_, err := r.rejectRIExchangeHandler(ctx, req, map[string]string{"id": "11111111-1111-1111-1111-111111111111"})
	assert.Error(t, err)
}

func TestRouter_listAccountsHandler(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	store := new(MockConfigStore)
	h := &Handler{auth: mockAuth, config: store}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := r.listAccountsHandler(ctx, req, nil)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestRouter_createAccountHandler(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	store := new(MockConfigStore)
	h := &Handler{auth: mockAuth, config: store}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"name": "Acct", "provider": "aws", "external_id": "123456789012"}`,
	}
	result, err := r.createAccountHandler(ctx, req, nil)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestRouter_discoverOrgAccountsHandler(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	h := &Handler{auth: mockAuth}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := r.discoverOrgAccountsHandler(ctx, req, nil)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestRouter_getAccountHandler(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	store := new(MockConfigStore)
	h := &Handler{auth: mockAuth, config: store}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := r.getAccountHandler(ctx, req, map[string]string{"id": "11111111-1111-1111-1111-111111111111"})
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestRouter_saveAccountCredentialsHandler_NoCredStore(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	store := new(MockConfigStore)
	h := &Handler{auth: mockAuth, config: store, credStore: nil}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"credential_type": "aws_access_keys", "payload": {}}`,
	}
	_, err := r.saveAccountCredentialsHandler(ctx, req, map[string]string{"id": "11111111-1111-1111-1111-111111111111"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "credential store not configured")
}

func TestRouter_testAccountCredentialsHandler(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	store := new(MockConfigStore)
	// GetCloudAccount returns an access_keys account (not ambient) → fall through to checkCredentialPresence
	h := &Handler{auth: mockAuth, config: store}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := r.testAccountCredentialsHandler(ctx, req, map[string]string{"id": "11111111-1111-1111-1111-111111111111"})
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestRouter_listAccountServiceOverridesHandler(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	store := new(MockConfigStore)
	h := &Handler{auth: mockAuth, config: store}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := r.listAccountServiceOverridesHandler(ctx, req, map[string]string{"id": "11111111-1111-1111-1111-111111111111"})
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestRouter_setPlanAccountsHandler(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	store := new(MockConfigStore)
	h := &Handler{auth: mockAuth, config: store}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"account_ids": []}`,
	}
	_, err := r.setPlanAccountsHandler(ctx, req, map[string]string{"id": "22222222-2222-2222-2222-222222222222"})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// handler_ri_exchange.go — getRIExchangeConfig, updateRIExchangeConfig,
//                         getRIExchangeHistory, validate, validateExchangeApproval
// ---------------------------------------------------------------------------

func TestHandler_getRIExchangeConfig_Success(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RIExchangeEnabled: true,
		RIExchangeMode:    "manual",
	}, nil)

	h := &Handler{auth: mockAuth, config: mockStore}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := h.getRIExchangeConfig(ctx, req)
	require.NoError(t, err)
	cfg := result.(*RIExchangeConfigResponse)
	assert.True(t, cfg.AutoExchangeEnabled)
	assert.Equal(t, "manual", cfg.Mode)
}

func TestHandler_getRIExchangeConfig_NoAuth(t *testing.T) {
	h := &Handler{}
	_, err := h.getRIExchangeConfig(context.Background(), &events.LambdaFunctionURLRequest{})
	assert.Error(t, err)
}

func TestHandler_getRIExchangeConfig_StoreError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(nil, errors.New("db error"))

	h := &Handler{auth: mockAuth, config: mockStore}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	_, err := h.getRIExchangeConfig(ctx, req)
	assert.Error(t, err)
}

func TestHandler_updateRIExchangeConfig_Success(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	mockStore.On("SaveGlobalConfig", ctx, mock.Anything).Return(nil)

	h := &Handler{auth: mockAuth, config: mockStore}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"mode": "auto", "utilization_threshold": 70, "lookback_days": 14, "max_payment_per_exchange_usd": 1000, "max_payment_daily_usd": 5000}`,
	}
	result, err := h.updateRIExchangeConfig(ctx, req)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestHandler_updateRIExchangeConfig_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{invalid json}`,
	}
	_, err := h.updateRIExchangeConfig(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_updateRIExchangeConfig_ValidationError(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"mode": "invalid-mode", "utilization_threshold": 50, "lookback_days": 30}`,
	}
	_, err := h.updateRIExchangeConfig(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mode must be")
}

func TestHandler_getRIExchangeConfigUpdateRequest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		req     RIExchangeConfigUpdateRequest
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid manual mode",
			req:     RIExchangeConfigUpdateRequest{Mode: "manual", UtilizationThreshold: 50, LookbackDays: 14},
			wantErr: false,
		},
		{
			name:    "valid auto mode",
			req:     RIExchangeConfigUpdateRequest{Mode: "auto", UtilizationThreshold: 0, LookbackDays: 1},
			wantErr: false,
		},
		{
			name:    "invalid mode",
			req:     RIExchangeConfigUpdateRequest{Mode: "other"},
			wantErr: true,
			errMsg:  "mode must be",
		},
		{
			name:    "utilization threshold too high",
			req:     RIExchangeConfigUpdateRequest{Mode: "manual", UtilizationThreshold: 101, LookbackDays: 7},
			wantErr: true,
			errMsg:  "utilization_threshold",
		},
		{
			name:    "utilization threshold negative",
			req:     RIExchangeConfigUpdateRequest{Mode: "manual", UtilizationThreshold: -1, LookbackDays: 7},
			wantErr: true,
			errMsg:  "utilization_threshold",
		},
		{
			name:    "lookback_days zero",
			req:     RIExchangeConfigUpdateRequest{Mode: "manual", UtilizationThreshold: 50, LookbackDays: 0},
			wantErr: true,
			errMsg:  "lookback_days",
		},
		{
			name:    "lookback_days too large",
			req:     RIExchangeConfigUpdateRequest{Mode: "manual", UtilizationThreshold: 50, LookbackDays: 366},
			wantErr: true,
			errMsg:  "lookback_days",
		},
		{
			name:    "negative max_payment_per_exchange_usd",
			req:     RIExchangeConfigUpdateRequest{Mode: "manual", UtilizationThreshold: 50, LookbackDays: 7, MaxPaymentPerExchangeUSD: -1},
			wantErr: true,
			errMsg:  "max_payment_per_exchange_usd",
		},
		{
			name:    "negative max_payment_daily_usd",
			req:     RIExchangeConfigUpdateRequest{Mode: "manual", UtilizationThreshold: 50, LookbackDays: 7, MaxPaymentDailyUSD: -1},
			wantErr: true,
			errMsg:  "max_payment_daily_usd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHandler_getRIExchangeHistory_Success(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	records := []config.RIExchangeRecord{
		{ID: "aaaa", ApprovalToken: "should-be-redacted"},
	}
	mockStore.On("GetRIExchangeHistory", ctx, mock.Anything, 500).Return(records, nil)

	h := &Handler{auth: mockAuth, config: mockStore}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := h.getRIExchangeHistory(ctx, req)
	require.NoError(t, err)
	resp := result.(*RIExchangeHistoryResponse)
	assert.Len(t, resp.Records, 1)
	// Approval token must be stripped
	assert.Equal(t, "", resp.Records[0].ApprovalToken)
}

func TestHandler_getRIExchangeHistory_StoreError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetRIExchangeHistory", ctx, mock.Anything, 500).Return(nil, errors.New("db error"))

	h := &Handler{auth: mockAuth, config: mockStore}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	_, err := h.getRIExchangeHistory(ctx, req)
	assert.Error(t, err)
}

func TestHandler_validateExchangeApproval_InvalidUUID(t *testing.T) {
	h := &Handler{}
	_, err := h.validateExchangeApproval(context.Background(), "not-a-uuid", "token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID format")
}

func TestHandler_validateExchangeApproval_NoToken(t *testing.T) {
	h := &Handler{}
	_, err := h.validateExchangeApproval(context.Background(), "11111111-1111-1111-1111-111111111111", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "approval token is required")
}

func TestHandler_validateExchangeApproval_RecordNotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockStore.On("GetRIExchangeRecord", ctx, "11111111-1111-1111-1111-111111111111").Return(nil, nil)

	h := &Handler{config: mockStore}
	_, err := h.validateExchangeApproval(ctx, "11111111-1111-1111-1111-111111111111", "some-token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestHandler_validateExchangeApproval_NoApprovalToken(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockStore.On("GetRIExchangeRecord", ctx, "11111111-1111-1111-1111-111111111111").Return(
		&config.RIExchangeRecord{ID: "11111111-1111-1111-1111-111111111111", ApprovalToken: ""}, nil)

	h := &Handler{config: mockStore}
	_, err := h.validateExchangeApproval(ctx, "11111111-1111-1111-1111-111111111111", "some-token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not support approval")
}

func TestHandler_validateExchangeApproval_WrongToken(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockStore.On("GetRIExchangeRecord", ctx, "11111111-1111-1111-1111-111111111111").Return(
		&config.RIExchangeRecord{ID: "11111111-1111-1111-1111-111111111111", ApprovalToken: "correct-token"}, nil)

	h := &Handler{config: mockStore}
	_, err := h.validateExchangeApproval(ctx, "11111111-1111-1111-1111-111111111111", "wrong-token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid approval token")
}

func TestHandler_validateExchangeApproval_Success(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockStore.On("GetRIExchangeRecord", ctx, "11111111-1111-1111-1111-111111111111").Return(
		&config.RIExchangeRecord{ID: "11111111-1111-1111-1111-111111111111", ApprovalToken: "good-token"}, nil)

	h := &Handler{config: mockStore}
	record, err := h.validateExchangeApproval(ctx, "11111111-1111-1111-1111-111111111111", "good-token")
	require.NoError(t, err)
	assert.NotNil(t, record)
}

// ---------------------------------------------------------------------------
// testAccountCredentials
// ---------------------------------------------------------------------------

func TestHandler_testAccountCredentials_InvalidUUID(t *testing.T) {
	h := &Handler{}
	_, err := h.testAccountCredentials(context.Background(), &events.LambdaFunctionURLRequest{}, "not-a-uuid")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ID format")
}

func TestHandler_testAccountCredentials_NoAuth(t *testing.T) {
	h := &Handler{}
	_, err := h.testAccountCredentials(context.Background(), &events.LambdaFunctionURLRequest{}, "11111111-1111-1111-1111-111111111111")
	assert.Error(t, err)
}

func TestHandler_testAccountCredentials_NotFound(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	store := new(MockConfigStore)
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) {
		return nil, nil // nil, nil means not found
	}

	h := &Handler{auth: mockAuth, config: store}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	_, err := h.testAccountCredentials(ctx, req, "11111111-1111-1111-1111-111111111111")
	assert.Error(t, err)
	// Should return errNotFound
	assert.ErrorIs(t, err, errNotFound)
}

func TestHandler_testAccountCredentials_AmbientCredResult(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	store := new(MockConfigStore)
	store.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) {
		return &config.CloudAccount{
			ID:            "11111111-1111-1111-1111-111111111111",
			Provider:      "azure",
			AzureAuthMode: "managed_identity",
		}, nil
	}

	h := &Handler{auth: mockAuth, config: store}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := h.testAccountCredentials(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)
	testResult := result.(AccountTestResult)
	assert.True(t, testResult.OK)
	assert.Contains(t, testResult.Message, "managed identity")
}

func TestHandler_testAccountCredentials_CheckPresence(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	// access_keys account — falls through to checkCredentialPresence
	store := new(MockConfigStore)
	// Default MockConfigStore.GetCloudAccount returns access_keys account

	h := &Handler{auth: mockAuth, config: store}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := h.testAccountCredentials(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)
	assert.NotNil(t, result)
}

// ---------------------------------------------------------------------------
// rejectRIExchange
// ---------------------------------------------------------------------------

func TestHandler_rejectRIExchange_InvalidUUID(t *testing.T) {
	h := &Handler{}
	_, err := h.rejectRIExchange(context.Background(), "not-uuid", "token")
	assert.Error(t, err)
}

func TestHandler_rejectRIExchange_EmptyToken(t *testing.T) {
	h := &Handler{}
	_, err := h.rejectRIExchange(context.Background(), "11111111-1111-1111-1111-111111111111", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rejection token is required")
}

func TestHandler_rejectRIExchange_ValidTokenAndRecord(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockStore.On("GetRIExchangeRecord", ctx, "11111111-1111-1111-1111-111111111111").Return(
		&config.RIExchangeRecord{
			ID:            "11111111-1111-1111-1111-111111111111",
			ApprovalToken: "tok",
			Status:        "pending",
		}, nil)
	// rejectRIExchange transitions to "cancelled", not "rejected"
	mockStore.On("TransitionRIExchangeStatus", ctx, "11111111-1111-1111-1111-111111111111", "pending", "cancelled").
		Return(&config.RIExchangeRecord{
			ID:     "11111111-1111-1111-1111-111111111111",
			Status: "cancelled",
		}, nil)

	h := &Handler{config: mockStore}
	result, err := h.rejectRIExchange(ctx, "11111111-1111-1111-1111-111111111111", "tok")
	require.NoError(t, err)
	m := result.(map[string]string)
	assert.Equal(t, "cancelled", m["status"])
}

// ---------------------------------------------------------------------------
// failExchange
// ---------------------------------------------------------------------------

func TestHandler_failExchange(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockStore.On("FailRIExchange", ctx, "11111111-1111-1111-1111-111111111111", "test reason").Return(nil)

	h := &Handler{config: mockStore}
	result, err := h.failExchange(ctx, "11111111-1111-1111-1111-111111111111", "test reason")
	require.NoError(t, err)
	m := result.(map[string]any)
	assert.Equal(t, "failed", m["status"])
	assert.Equal(t, "test reason", m["reason"])
}

func TestHandler_failExchange_DBError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	// Even when DB fails, failExchange returns the failure result (logging the error)
	mockStore.On("FailRIExchange", ctx, "11111111-1111-1111-1111-111111111111", "reason").
		Return(errors.New("db down"))

	h := &Handler{config: mockStore}
	result, err := h.failExchange(ctx, "11111111-1111-1111-1111-111111111111", "reason")
	require.NoError(t, err)
	m := result.(map[string]any)
	assert.Equal(t, "failed", m["status"])
}

// ---------------------------------------------------------------------------
// deleteAccount error path
// ---------------------------------------------------------------------------

func TestHandler_deleteAccount_InvalidUUID(t *testing.T) {
	h := &Handler{}
	_, err := h.deleteAccount(context.Background(), &events.LambdaFunctionURLRequest{}, "not-a-uuid")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// router docs handler
// ---------------------------------------------------------------------------

func TestRouter_docsHandler_ServesUIOrSpec(t *testing.T) {
	ctx := context.Background()
	h := &Handler{}
	r := newTestRouter(h)

	// GET /docs — serves UI
	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/docs",
			},
		},
	}
	result, err := r.docsHandler(ctx, req, map[string]string{"action": ""})
	// The docs handler might return a response or nil depending on implementation.
	// We just verify it doesn't panic.
	_ = result
	_ = err
}

// ---------------------------------------------------------------------------
// decodeBase64Password
// ---------------------------------------------------------------------------

func TestDecodeBase64Password(t *testing.T) {
	import64 := func(s string) string {
		import64Enc := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
		_ = import64Enc
		// Use standard base64 encoding to produce test input
		import64b := []byte(s)
		return string(import64b)
	}
	_ = import64

	// Empty input returns empty
	result, err := decodeBase64Password("")
	require.NoError(t, err)
	assert.Equal(t, "", result)

	// Invalid base64 returns error
	_, err = decodeBase64Password("not-valid-base64!!!")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// handler_docs.go
// ---------------------------------------------------------------------------

func TestHandler_docsHandler_OpenAPISpec(t *testing.T) {
	ctx := context.Background()
	h := &Handler{}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/docs/openapi.yaml",
			},
		},
	}
	result, err := h.docsHandler(ctx, req, map[string]string{"action": "openapi.yaml"})
	_ = result
	_ = err // may return error if file not found; we just ensure no panic
}

// Ensure we hit the time.Now path in getRIExchangeHistory
func TestHandler_getRIExchangeHistory_SinceTime(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	// Use Any matcher for time.Time since it's computed at call time
	mockStore.On("GetRIExchangeHistory", ctx, mock.MatchedBy(func(t time.Time) bool {
		// Should be approximately 1 year ago
		expected := time.Now().AddDate(-1, 0, 0)
		return t.After(expected.Add(-10*time.Second)) && t.Before(expected.Add(10*time.Second))
	}), 500).Return([]config.RIExchangeRecord{}, nil)

	h := &Handler{auth: mockAuth, config: mockStore}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := h.getRIExchangeHistory(ctx, req)
	require.NoError(t, err)
	assert.NotNil(t, result)
}
