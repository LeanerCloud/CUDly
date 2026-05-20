package api

import (
	"context"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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

	// Missing both target_offering_id and targets[]
	_, err = h.getExchangeQuote(context.Background(), &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer test-token"},
		Body:    `{"ri_ids":["ri-123"]}`,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "targets[] or target_offering_id is required")

	// Empty offering_id inside targets[] is rejected per-entry so a
	// caller can't sneak through with a zero-valued target.
	_, err = h.getExchangeQuote(context.Background(), &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer test-token"},
		Body:    `{"ri_ids":["ri-123"],"targets":[{"offering_id":"","count":1}]}`,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "targets[0].offering_id is required")
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

	// Missing both target_offering_id and targets[]
	_, err = h.executeExchange(context.Background(), &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer test-token"},
		Body:    `{"ri_ids":["ri-123"],"max_payment_due_usd":"10"}`,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "targets[] or target_offering_id is required")

	// Empty offering_id inside targets[] rejected.
	_, err = h.executeExchange(context.Background(), &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer test-token"},
		Body:    `{"ri_ids":["ri-123"],"targets":[{"offering_id":"","count":1}],"max_payment_due_usd":"10"}`,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "targets[0].offering_id is required")
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

// TestRejectRIExchange_EmptyStoredToken is a regression test for issue #399.
// A record with an empty ApprovalToken must be rejected with 403 rather than
// being cancelled by any caller passing an empty token string, because
// crypto/subtle.ConstantTimeCompare([]byte(""), []byte("")) == 1.
func TestRejectRIExchange_EmptyStoredToken(t *testing.T) {
	mockStore := new(MockConfigStore)
	h := &Handler{config: mockStore}
	ctx := context.Background()
	id := "550e8400-e29b-41d4-a716-446655440005"

	// Record exists but was persisted without an ApprovalToken.
	mockStore.On("GetRIExchangeRecord", ctx, id).Return(&config.RIExchangeRecord{
		ID:            id,
		ApprovalToken: "", // empty stored token
		Status:        "pending",
	}, nil)

	// Passing an empty token must NOT match the empty stored token.
	_, err := h.rejectRIExchange(ctx, id, "sometoken")
	assert.Error(t, err)
	ce, ok := IsClientError(err)
	assert.True(t, ok)
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, err.Error(), "does not support rejection")

	mockStore.AssertExpectations(t)
}

// fakeReshapeEC2Stub is a unit-test stub for reshapeEC2Client.
// Returns a single convertible RI so the downstream
// AnalyzeReshapingWithRecs actually invokes purchaseRecLookupFromStore's
// closure — without an RI in the input slice the recs lookup is never
// called and the region-normalization assertion would never fire
// (silent test trap). Mirrors the integration-test fake but lives
// here so this unit test runs without the //go:build integration tag.
type fakeReshapeEC2Stub struct {
	instances []ec2svc.ConvertibleRI
}

func (f *fakeReshapeEC2Stub) ListConvertibleReservedInstances(_ context.Context) ([]ec2svc.ConvertibleRI, error) {
	return f.instances, nil
}

// fakeReshapeRecsStub returns a configurable RI utilization slice so
// the reshape pipeline runs without a real Cost Explorer dependency.
// AnalyzeReshapingWithRecs only emits a recommendation (and only then
// invokes the recs lookup closure) when an RI has a utilization entry
// BELOW the threshold — without one the analyzer returns nil and the
// closure never fires, producing a silent test. Seed with util-percent
// well under the request's threshold (95) to guarantee the closure
// runs.
type fakeReshapeRecsStub struct {
	utilization []recommendations.RIUtilization
}

func (f *fakeReshapeRecsStub) GetRIUtilization(_ context.Context, _ int) ([]recommendations.RIUtilization, error) {
	return f.utilization, nil
}

// TestGetReshapeRecommendations_EmptyRegionUsesConfigRegion pins the
// region-normalization fix from commit afc3aa1ff: when the caller
// omits ?region= (or sends ?region=), the handler MUST adopt
// cfg.Region (the value the AWS SDK clients are actually talking to)
// before threading the region through to the recs lookup. Without
// the normalization the recs query lands as Region:"" — an unscoped
// read that surfaces alternatives from every region onto the reshape
// page.
//
// Mock injection strategy: build a Handler with the same factory-
// injection seams the integration test uses (reshapeEC2Factory,
// reshapeRecsFactory, reshapeAccountResolver) so the request runs
// without real AWS calls or Postgres. Pre-seal awsCfgOnce with
// Region:"us-west-2" so loadAWSConfigWithRegion(ctx, "") returns a
// config carrying that region. Wire MockConfigStore.ListStoredRecommendations
// with mock.Run to capture every filter the closure passes — the
// load-bearing assertion is filter.Region == "us-west-2" (not "").
//
// Reverting the `if region == "" { region = cfg.Region }` block from
// handler_ri_exchange.go makes this test fail loudly with the captured
// filter showing Region:"".
func TestGetReshapeRecommendations_EmptyRegionUsesConfigRegion(t *testing.T) {
	const cfgRegion = "us-west-2"

	mockStore := &MockConfigStore{}
	// mock.Run captures every invocation's arguments without affecting
	// the return value. Returning nil/nil from ListStoredRecommendations
	// means "no recs in this region" which the downstream pipeline
	// treats as empty alternatives — fine for our purposes.
	var capturedFilters []config.RecommendationFilter
	mockStore.On("ListStoredRecommendations", mock.Anything, mock.Anything).
		Return([]config.RecommendationRecord(nil), nil).
		Run(func(args mock.Arguments) {
			capturedFilters = append(capturedFilters, args.Get(1).(config.RecommendationFilter))
		})

	h := &Handler{
		config: mockStore,
		auth:   &mockAuthForExchange{},
		// Inject one convertible RI so AnalyzeReshapingWithRecs
		// actually drives purchaseRecLookupFromStore — otherwise the
		// closure never fires and the assertion is silent.
		reshapeEC2Factory: func(_ aws.Config) reshapeEC2Client {
			return &fakeReshapeEC2Stub{
				instances: []ec2svc.ConvertibleRI{
					{ReservedInstanceID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, CurrencyCode: "USD"},
				},
			}
		},
		reshapeRecsFactory: func(_ aws.Config) reshapeRecsClient {
			// Util well below threshold (95) → analyzeRI emits a
			// recommendation → AnalyzeReshapingWithRecs invokes the
			// recs lookup closure → MockConfigStore.ListStoredRecommendations
			// captures the filter the assertion below inspects.
			return &fakeReshapeRecsStub{
				utilization: []recommendations.RIUtilization{
					{ReservedInstanceID: "ri-1", UtilizationPercent: 50.0},
				},
			}
		},
		// Bypass STS so the test is hermetic. Empty cloud-account ID
		// is the legitimate "no scope filter" path; this test isolates
		// the region-normalization regression, not account scoping.
		reshapeAccountResolver: func(_ context.Context) (string, error) { return "", nil },
	}
	// Pre-seal awsCfgOnce with the cfg.Region the handler must adopt
	// when the caller sends an empty ?region=. loadAWSConfigWithRegion
	// returns this config unmodified (region override is skipped on
	// empty input), so cfg.Region == cfgRegion downstream.
	h.awsCfgOnce.Do(func() {
		h.awsCfg = aws.Config{Region: cfgRegion}
	})

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer test-token"},
		QueryStringParameters: map[string]string{
			// Explicit empty value — the regression case from issue #151.
			// Equivalent to omitting the key entirely (Go map zero-value).
			"region":        "",
			"lookback_days": "30",
			"threshold":     "95.0",
		},
	}

	_, err := h.getReshapeRecommendations(context.Background(), req)
	require.NoError(t, err, "reshape handler must succeed when region is empty (cfg.Region used)")

	// Guard against a silent test: if AnalyzeReshapingWithRecs ever
	// stops invoking the closure for synthetic RIs, the region check
	// would vacuously pass. Require at least one captured filter.
	require.NotEmpty(t, capturedFilters,
		"recs lookup must be invoked at least once — without it the region-normalization "+
			"assertion would silently pass even if the fix were reverted")

	// Load-bearing assertion: every captured filter must carry the
	// normalized cfg.Region value, NOT the empty string the request
	// arrived with. If the `if region == "" { region = cfg.Region }`
	// block in getReshapeRecommendations is removed, this fails.
	for i, f := range capturedFilters {
		assert.Equal(t, cfgRegion, f.Region,
			"capturedFilters[%d].Region must equal cfg.Region (%q), not %q — empty ?region= "+
				"must normalize to cfg.Region or the recs lookup runs unscoped and leaks "+
				"alternatives from other regions onto the reshape page (commit afc3aa1ff)",
			i, cfgRegion, f.Region)
	}
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
func (m *mockAuthForExchange) ResetTokenStatus(_ context.Context, _ string) (string, string, error) {
	return "valid", "reset", nil
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
func (m *mockAuthForExchange) MFASetupAPI(_ context.Context, _, _ string) (string, string, error) {
	return "", "", nil
}
func (m *mockAuthForExchange) MFAEnableAPI(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}
func (m *mockAuthForExchange) MFADisableAPI(_ context.Context, _, _, _ string) error { return nil }
func (m *mockAuthForExchange) MFARegenerateRecoveryCodesAPI(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}
