package api

import (
	"context"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestListConvertibleRIs_RequiresAdmin(t *testing.T) {
	h := &Handler{} // no auth configured
	_, err := h.listConvertibleRIs(context.Background(), &events.LambdaFunctionURLRequest{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "authentication")
}

func TestGetRIUtilization_RequiresAdmin(t *testing.T) {
	h := &Handler{}
	_, err := h.getRIUtilization(context.Background(), &events.LambdaFunctionURLRequest{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "authentication")
}

func TestGetReshapeRecommendations_RequiresAdmin(t *testing.T) {
	h := &Handler{}
	_, err := h.getReshapeRecommendations(context.Background(), &events.LambdaFunctionURLRequest{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "authentication")
}

func TestGetExchangeQuote_RequiresAdmin(t *testing.T) {
	h := &Handler{}
	_, err := h.getExchangeQuote(context.Background(), &events.LambdaFunctionURLRequest{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "authentication")
}

func TestExecuteExchange_RequiresAdmin(t *testing.T) {
	h := &Handler{}
	_, err := h.executeExchange(context.Background(), &events.LambdaFunctionURLRequest{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "authentication")
}

func TestGetExchangeQuote_Validation(t *testing.T) {
	// Test with a mock auth that always succeeds
	mockAuth := &mockAuthForExchange{}
	h := &Handler{auth: mockAuth}

	// Missing body
	_, err := h.getExchangeQuote(context.Background(), &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer test-token"},
		Body:    "{}",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ri_ids is required")

	// Missing target_offering_id
	_, err = h.getExchangeQuote(context.Background(), &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer test-token"},
		Body:    `{"ri_ids":["ri-123"]}`,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "target_offering_id is required")
}

func TestGetRIUtilization_LookbackValidation(t *testing.T) {
	mockAuth := &mockAuthForExchange{}
	h := &Handler{auth: mockAuth}

	tests := []struct {
		name    string
		days    string
		wantErr string
	}{
		{"negative", "-1", "lookback_days must be between 1 and 365"},
		{"zero", "0", "lookback_days must be between 1 and 365"},
		{"too large", "999", "lookback_days must be between 1 and 365"},
		{"not a number", "abc", "lookback_days must be between 1 and 365"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.getRIUtilization(context.Background(), &events.LambdaFunctionURLRequest{
				Headers:               map[string]string{"authorization": "Bearer test-token"},
				QueryStringParameters: map[string]string{"lookback_days": tt.days},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestGetReshapeRecommendations_LookbackValidation(t *testing.T) {
	mockAuth := &mockAuthForExchange{}
	h := &Handler{auth: mockAuth}

	tests := []struct {
		name    string
		days    string
		wantErr string
	}{
		{"negative", "-1", "lookback_days must be between 1 and 365"},
		{"zero", "0", "lookback_days must be between 1 and 365"},
		{"too large", "999", "lookback_days must be between 1 and 365"},
		{"not a number", "abc", "lookback_days must be between 1 and 365"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.getReshapeRecommendations(context.Background(), &events.LambdaFunctionURLRequest{
				Headers:               map[string]string{"authorization": "Bearer test-token"},
				QueryStringParameters: map[string]string{"lookback_days": tt.days},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestGetReshapeRecommendations_ThresholdValidation(t *testing.T) {
	mockAuth := &mockAuthForExchange{}
	h := &Handler{auth: mockAuth}

	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{"negative", "-1", "threshold must be a number between 0 and 100"},
		{"over 100", "101", "threshold must be a number between 0 and 100"},
		{"not a number", "abc", "threshold must be a number between 0 and 100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.getReshapeRecommendations(context.Background(), &events.LambdaFunctionURLRequest{
				Headers:               map[string]string{"authorization": "Bearer test-token"},
				QueryStringParameters: map[string]string{"threshold": tt.value},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestExecuteExchange_Validation(t *testing.T) {
	mockAuth := &mockAuthForExchange{}
	h := &Handler{auth: mockAuth}

	// Missing max_payment_due_usd
	_, err := h.executeExchange(context.Background(), &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer test-token"},
		Body:    `{"ri_ids":["ri-123"],"target_offering_id":"offering-1","target_count":1}`,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max_payment_due_usd is required")

	// Invalid max_payment_due_usd
	_, err = h.executeExchange(context.Background(), &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer test-token"},
		Body:    `{"ri_ids":["ri-123"],"target_offering_id":"offering-1","target_count":1,"max_payment_due_usd":"abc"}`,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid max_payment_due_usd")
}

// --- Approve/Reject edge case tests ---

func TestRejectRIExchange_AlreadyCompleted(t *testing.T) {
	mockStore := new(MockConfigStore)
	h := &Handler{config: mockStore}
	ctx := context.Background()
	id := "550e8400-e29b-41d4-a716-446655440000"
	token := "valid-token-123"

	// Record exists but is already completed
	mockStore.On("GetRIExchangeRecord", ctx, id).Return(&config.RIExchangeRecord{
		ID:            id,
		ApprovalToken: token,
		Status:        "completed",
		ExchangeID:    "exch-already-done",
	}, nil)

	// Transition from pending→cancelled fails (record is not pending)
	mockStore.On("TransitionRIExchangeStatus", ctx, id, "pending", "cancelled").
		Return((*config.RIExchangeRecord)(nil), nil)

	_, err := h.rejectRIExchange(ctx, id, token)
	assert.Error(t, err)
	ce, ok := IsClientError(err)
	assert.True(t, ok)
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, err.Error(), "already processed")

	mockStore.AssertExpectations(t)
}

func TestApproveRIExchange_AlreadyCancelled(t *testing.T) {
	mockStore := new(MockConfigStore)
	h := &Handler{config: mockStore}
	ctx := context.Background()
	id := "550e8400-e29b-41d4-a716-446655440001"
	token := "valid-token-456"

	// Record exists but was cancelled by a newer analysis run
	mockStore.On("GetRIExchangeRecord", ctx, id).Return(&config.RIExchangeRecord{
		ID:            id,
		ApprovalToken: token,
		Status:        "cancelled",
	}, nil)

	// Transition from pending→processing fails (record is cancelled)
	mockStore.On("TransitionRIExchangeStatus", ctx, id, "pending", "processing").
		Return((*config.RIExchangeRecord)(nil), nil)

	_, err := h.approveRIExchange(ctx, id, token)
	assert.Error(t, err)
	ce, ok := IsClientError(err)
	assert.True(t, ok)
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, err.Error(), "already processed")

	mockStore.AssertExpectations(t)
}

func TestApproveRIExchange_DoubleApprove(t *testing.T) {
	mockStore := new(MockConfigStore)
	h := &Handler{config: mockStore}
	ctx := context.Background()
	id := "550e8400-e29b-41d4-a716-446655440002"
	token := "valid-token-789"

	// Record exists and is already being processed (first approve succeeded)
	mockStore.On("GetRIExchangeRecord", ctx, id).Return(&config.RIExchangeRecord{
		ID:            id,
		ApprovalToken: token,
		Status:        "processing",
		SourceRIIDs:   []string{"ri-123"},
		PaymentDue:    "5.00",
	}, nil)

	// Transition from pending→processing fails (already processing)
	mockStore.On("TransitionRIExchangeStatus", ctx, id, "pending", "processing").
		Return((*config.RIExchangeRecord)(nil), nil)

	_, err := h.approveRIExchange(ctx, id, token)
	assert.Error(t, err)
	ce, ok := IsClientError(err)
	assert.True(t, ok)
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, err.Error(), "already processed")

	mockStore.AssertExpectations(t)
}

func TestApproveRIExchange_InvalidToken(t *testing.T) {
	mockStore := new(MockConfigStore)
	h := &Handler{config: mockStore}
	ctx := context.Background()
	id := "550e8400-e29b-41d4-a716-446655440003"

	mockStore.On("GetRIExchangeRecord", ctx, id).Return(&config.RIExchangeRecord{
		ID:            id,
		ApprovalToken: "correct-token",
		Status:        "pending",
	}, nil)

	_, err := h.approveRIExchange(ctx, id, "wrong-token")
	assert.Error(t, err)
	ce, ok := IsClientError(err)
	assert.True(t, ok)
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, err.Error(), "invalid approval token")
}

func TestRejectRIExchange_MissingToken(t *testing.T) {
	h := &Handler{}
	ctx := context.Background()
	id := "550e8400-e29b-41d4-a716-446655440004"

	_, err := h.rejectRIExchange(ctx, id, "")
	assert.Error(t, err)
	ce, ok := IsClientError(err)
	assert.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, err.Error(), "rejection token is required")
}

// Suppress unused import warnings
var _ = mock.Anything
var _ = time.Now
var _ = config.RIExchangeRecord{}

// mockAuthForExchange is a minimal auth mock that returns an admin session.
type mockAuthForExchange struct{}

func (m *mockAuthForExchange) Login(_ context.Context, _ LoginRequest) (*LoginResponse, error) {
	return nil, nil
}
func (m *mockAuthForExchange) Logout(_ context.Context, _ string) error { return nil }
func (m *mockAuthForExchange) ValidateSession(_ context.Context, _ string) (*Session, error) {
	return &Session{UserID: "admin", Email: "admin@test.com", Role: "admin"}, nil
}
func (m *mockAuthForExchange) ValidateCSRFToken(_ context.Context, _, _ string) error { return nil }
func (m *mockAuthForExchange) SetupAdmin(_ context.Context, _ SetupAdminRequest) (*LoginResponse, error) {
	return nil, nil
}
func (m *mockAuthForExchange) CheckAdminExists(_ context.Context) (bool, error)       { return true, nil }
func (m *mockAuthForExchange) RequestPasswordReset(_ context.Context, _ string) error { return nil }
func (m *mockAuthForExchange) ConfirmPasswordReset(_ context.Context, _ PasswordResetConfirm) error {
	return nil
}
func (m *mockAuthForExchange) GetUser(_ context.Context, _ string) (*User, error) { return nil, nil }
func (m *mockAuthForExchange) UpdateUserProfile(_ context.Context, _, _, _, _ string) error {
	return nil
}
func (m *mockAuthForExchange) CreateUserAPI(_ context.Context, _ any) (any, error) { return nil, nil }
func (m *mockAuthForExchange) UpdateUserAPI(_ context.Context, _ string, _ any) (any, error) {
	return nil, nil
}
func (m *mockAuthForExchange) DeleteUser(_ context.Context, _ string) error              { return nil }
func (m *mockAuthForExchange) ListUsersAPI(_ context.Context) (any, error)               { return nil, nil }
func (m *mockAuthForExchange) ChangePasswordAPI(_ context.Context, _, _, _ string) error { return nil }
func (m *mockAuthForExchange) CreateGroupAPI(_ context.Context, _ any) (any, error)      { return nil, nil }
func (m *mockAuthForExchange) UpdateGroupAPI(_ context.Context, _ string, _ any) (any, error) {
	return nil, nil
}
func (m *mockAuthForExchange) DeleteGroup(_ context.Context, _ string) error        { return nil }
func (m *mockAuthForExchange) GetGroupAPI(_ context.Context, _ string) (any, error) { return nil, nil }
func (m *mockAuthForExchange) ListGroupsAPI(_ context.Context) (any, error)         { return nil, nil }
func (m *mockAuthForExchange) HasPermissionAPI(_ context.Context, _, _, _ string) (bool, error) {
	return true, nil
}
func (m *mockAuthForExchange) CreateAPIKeyAPI(_ context.Context, _ string, _ any) (any, error) {
	return nil, nil
}
func (m *mockAuthForExchange) ListUserAPIKeysAPI(_ context.Context, _ string) (any, error) {
	return nil, nil
}
func (m *mockAuthForExchange) DeleteAPIKeyAPI(_ context.Context, _, _ string) error { return nil }
func (m *mockAuthForExchange) RevokeAPIKeyAPI(_ context.Context, _, _ string) error { return nil }
func (m *mockAuthForExchange) ValidateUserAPIKeyAPI(_ context.Context, _ string) (any, any, error) {
	return nil, nil, nil
}
func (m *mockAuthForExchange) GetAllowedAccountsAPI(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
