package api

// coverage_gaps_test.go — additional tests to push internal/api coverage above 80%.
// Targets: parseAccountIDs, redactEmail, mergeServiceConfig, checkRateLimit,
//          checkUserAPIKey, ambientCredResult, credTypeForAccount,
//          checkCredentialPresence, validateCSRF, requireAdmin,
//          getRecommendations (more cases), sendPurchaseApprovalEmail.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// parseAccountIDs
// ---------------------------------------------------------------------------

func TestParseAccountIDs(t *testing.T) {
	validUUID := "12345678-1234-1234-1234-123456789abc"
	validUUID2 := "abcdefab-abcd-abcd-abcd-abcdefabcdef"

	tests := []struct {
		name    string
		input   string
		wantLen int
		wantErr bool
	}{
		{"empty returns nil", "", 0, false},
		{"single valid UUID", validUUID, 1, false},
		{"two valid UUIDs", validUUID + "," + validUUID2, 2, false},
		{"valid UUID with spaces", "  " + validUUID + "  ", 1, false},
		{"skip empty entries", validUUID + ",,", 1, false},
		{"invalid UUID returns error", "not-a-uuid", 0, true},
		{"mixed valid and invalid", validUUID + ",not-a-uuid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids, err := parseAccountIDs(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, ids, tt.wantLen)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// redactEmail
// ---------------------------------------------------------------------------

func TestRedactEmail(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user@example.com", "us***@example.com"},
		{"ab@example.com", "***@example.com"}, // <=2 local chars → full redact
		{"a@example.com", "***@example.com"},
		{"noemail", "***"}, // no @ at all
		{"", "***"},
		{"alice@corp.io", "al***@corp.io"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := redactEmail(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// ---------------------------------------------------------------------------
// mergeServiceConfig
// ---------------------------------------------------------------------------

func TestMergeServiceConfig_NewRecord(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	// Simulate "not found" error
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").
		Return(nil, errors.New("not found"))

	incoming := config.ServiceConfig{
		Provider: "aws",
		Service:  "rds",
		Enabled:  true,
		Term:     3,
		Coverage: 80,
	}

	result, err := mergeServiceConfig(ctx, mockStore, incoming)
	require.NoError(t, err)
	assert.Equal(t, incoming, result)
}

func TestMergeServiceConfig_ExistingRecord(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	existing := &config.ServiceConfig{
		Provider:       "aws",
		Service:        "rds",
		Enabled:        false,
		Term:           1,
		Coverage:       50.0,
		IncludeEngines: []string{"mysql"},
	}
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(existing, nil)

	incoming := config.ServiceConfig{
		Provider: "aws",
		Service:  "rds",
		Enabled:  true,
		Term:     3,
		Coverage: 80.0,
	}

	result, err := mergeServiceConfig(ctx, mockStore, incoming)
	require.NoError(t, err)

	// UI-editable fields are updated
	assert.True(t, result.Enabled)
	assert.Equal(t, 3, result.Term)
	assert.Equal(t, 80.0, result.Coverage)
	// Non-UI fields are preserved
	assert.Equal(t, []string{"mysql"}, result.IncludeEngines)
}

func TestMergeServiceConfig_DBError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	mockStore.On("GetServiceConfig", ctx, "aws", "rds").
		Return(nil, errors.New("connection refused"))

	incoming := config.ServiceConfig{Provider: "aws", Service: "rds"}

	_, err := mergeServiceConfig(ctx, mockStore, incoming)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read existing service config")
}

func TestMergeServiceConfig_NilExisting(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	// GetServiceConfig returns nil, nil (record does not exist but no error)
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(nil, nil)

	incoming := config.ServiceConfig{
		Provider: "aws",
		Service:  "rds",
		Enabled:  true,
		Term:     3,
	}

	result, err := mergeServiceConfig(ctx, mockStore, incoming)
	require.NoError(t, err)
	assert.Equal(t, incoming, result)
}

// ---------------------------------------------------------------------------
// checkRateLimit
// ---------------------------------------------------------------------------

func TestHandler_checkRateLimit_NilLimiter(t *testing.T) {
	h := &Handler{rateLimiter: nil}
	req := &events.LambdaFunctionURLRequest{}
	err := h.checkRateLimit(context.Background(), req, "login")
	assert.NoError(t, err)
}

func TestHandler_checkRateLimit_Allowed(t *testing.T) {
	rl := NewInMemoryRateLimiter()
	h := &Handler{rateLimiter: rl}
	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				SourceIP: "1.2.3.4",
			},
		},
	}
	err := h.checkRateLimit(context.Background(), req, "login")
	assert.NoError(t, err)
}

func TestHandler_checkRateLimit_Exceeded(t *testing.T) {
	rl := NewInMemoryRateLimiter()
	// Override login limit to 1 attempt so we can exhaust it quickly
	rl.SetLimit("login", NewRateLimitConfig(1, 60))

	h := &Handler{rateLimiter: rl}
	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				SourceIP: "5.6.7.8",
			},
		},
	}
	ctx := context.Background()

	// First request — allowed
	err := h.checkRateLimit(ctx, req, "login")
	assert.NoError(t, err)

	// Second request — should be rate-limited
	err = h.checkRateLimit(ctx, req, "login")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too many requests")
}

// ---------------------------------------------------------------------------
// checkUserAPIKey
// ---------------------------------------------------------------------------

func TestHandler_checkUserAPIKey_ValidKey(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateUserAPIKeyAPI", ctx, "valid-user-key").
		Return("user-id", map[string]interface{}{}, nil)

	h := &Handler{auth: mockAuth}
	assert.True(t, h.checkUserAPIKey(ctx, "valid-user-key"))
}

func TestHandler_checkUserAPIKey_InvalidKey(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateUserAPIKeyAPI", ctx, "bad-key").
		Return(nil, nil, errors.New("invalid key"))

	h := &Handler{auth: mockAuth}
	assert.False(t, h.checkUserAPIKey(ctx, "bad-key"))
}

func TestHandler_checkUserAPIKey_EmptyKey(t *testing.T) {
	h := &Handler{auth: nil}
	assert.False(t, h.checkUserAPIKey(context.Background(), ""))
}

func TestHandler_checkUserAPIKey_NilAuth(t *testing.T) {
	h := &Handler{auth: nil}
	assert.False(t, h.checkUserAPIKey(context.Background(), "some-key"))
}

// ---------------------------------------------------------------------------
// requireAdmin
// ---------------------------------------------------------------------------

func TestHandler_requireAdmin_AdminAPIKey(t *testing.T) {
	h := &Handler{apiKey: "admin-secret"}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"X-API-Key": "admin-secret"},
	}
	session, err := h.requireAdmin(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "admin", session.Role)
}

func TestHandler_requireAdmin_NoAuthService(t *testing.T) {
	h := &Handler{auth: nil}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer some-token"},
	}
	_, err := h.requireAdmin(context.Background(), req)
	assert.Error(t, err)
}

func TestHandler_requireAdmin_NoToken(t *testing.T) {
	mockAuth := new(MockAuthService)
	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
	}
	_, err := h.requireAdmin(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no authorization token")
}

func TestHandler_requireAdmin_InvalidSession(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "bad-token").Return(nil, errors.New("expired"))
	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer bad-token"},
	}
	_, err := h.requireAdmin(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid session")
}

func TestHandler_requireAdmin_NonAdmin(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	userSession := &Session{UserID: "uid", Role: "user"}
	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer user-token"},
	}
	_, err := h.requireAdmin(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "admin access required")
}

func TestHandler_requireAdmin_AdminRole(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "admin-uid", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	session, err := h.requireAdmin(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "admin", session.Role)
}

// ---------------------------------------------------------------------------
// validateCSRF
// ---------------------------------------------------------------------------

func TestHandler_validateCSRF_NilAuthService(t *testing.T) {
	h := &Handler{auth: nil}
	req := &events.LambdaFunctionURLRequest{}
	err := h.validateCSRF(context.Background(), req)
	assert.Error(t, err)
}

func TestHandler_validateCSRF_AdminAPIKey(t *testing.T) {
	h := &Handler{apiKey: "admin-key", auth: new(MockAuthService)}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"X-API-Key": "admin-key"},
	}
	err := h.validateCSRF(context.Background(), req)
	assert.NoError(t, err)
}

func TestHandler_validateCSRF_ValidUserAPIKey(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateUserAPIKeyAPI", ctx, "user-api-key").
		Return("uid", map[string]interface{}{}, nil)
	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"X-API-Key": "user-api-key"},
	}
	err := h.validateCSRF(ctx, req)
	assert.NoError(t, err)
}

func TestHandler_validateCSRF_InvalidAPIKeyFallsThrough(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	// Invalid API key — also no bearer token → error about missing session
	mockAuth.On("ValidateUserAPIKeyAPI", ctx, "invalid-key").
		Return(nil, nil, errors.New("bad key"))
	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"X-API-Key": "invalid-key"},
	}
	err := h.validateCSRF(ctx, req)
	assert.Error(t, err)
}

func TestHandler_validateCSRF_NoSessionToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
	}
	err := h.validateCSRF(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no session token")
}

func TestHandler_validateCSRF_ValidCSRF(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateCSRFToken", ctx, "session-tok", "csrf-tok").Return(nil)
	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer session-tok",
			"X-CSRF-Token":  "csrf-tok",
		},
	}
	err := h.validateCSRF(ctx, req)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// requiresCSRFValidation
// ---------------------------------------------------------------------------

func TestHandler_requiresCSRFValidation(t *testing.T) {
	h := &Handler{}

	// GET never requires CSRF
	assert.False(t, h.requiresCSRFValidation("GET", "/api/plans"))

	// POST on login is exempt
	assert.False(t, h.requiresCSRFValidation("POST", "/api/auth/login"))

	// POST on a protected endpoint requires CSRF
	assert.True(t, h.requiresCSRFValidation("POST", "/api/plans"))

	// DELETE on protected endpoint requires CSRF
	assert.True(t, h.requiresCSRFValidation("DELETE", "/api/plans/123"))

	// POST on approve (token-based) is exempt
	assert.False(t, h.requiresCSRFValidation("POST", "/api/purchases/approve/uuid"))
}

// ---------------------------------------------------------------------------
// ambientCredResult
// ---------------------------------------------------------------------------

func TestAmbientCredResult(t *testing.T) {
	tests := []struct {
		name        string
		acct        *config.CloudAccount
		wantOK      bool
		wantFound   bool
		msgContains string
	}{
		{
			name:        "aws workload_identity_federation no ARN",
			acct:        &config.CloudAccount{Provider: "aws", AWSAuthMode: "workload_identity_federation"},
			wantOK:      false,
			wantFound:   true,
			msgContains: "aws_role_arn",
		},
		{
			name:        "aws workload_identity_federation with ARN",
			acct:        &config.CloudAccount{Provider: "aws", AWSAuthMode: "workload_identity_federation", AWSRoleARN: "arn:aws:iam::123456789012:role/R"},
			wantOK:      true,
			wantFound:   true,
			msgContains: "web identity",
		},
		{
			name:        "aws role_arn no ARN",
			acct:        &config.CloudAccount{Provider: "aws", AWSAuthMode: "role_arn"},
			wantOK:      false,
			wantFound:   true,
			msgContains: "aws_role_arn",
		},
		{
			name:        "aws role_arn with ARN",
			acct:        &config.CloudAccount{Provider: "aws", AWSAuthMode: "role_arn", AWSRoleARN: "arn:aws:iam::123456789012:role/R"},
			wantOK:      true,
			wantFound:   true,
			msgContains: "role assumption",
		},
		{
			name:      "aws unknown mode — not handled (falls through to credential check)",
			acct:      &config.CloudAccount{Provider: "aws", AWSAuthMode: "unknown_mode"},
			wantFound: false,
		},
		{
			name:      "aws access_keys — not handled",
			acct:      &config.CloudAccount{Provider: "aws", AWSAuthMode: "access_keys"},
			wantFound: false,
		},
		{
			name:        "azure managed_identity",
			acct:        &config.CloudAccount{Provider: "azure", AzureAuthMode: "managed_identity"},
			wantOK:      true,
			wantFound:   true,
			msgContains: "managed identity",
		},
		{
			name:      "azure other mode — not handled",
			acct:      &config.CloudAccount{Provider: "azure", AzureAuthMode: "service_principal"},
			wantFound: false,
		},
		{
			name:        "gcp application_default",
			acct:        &config.CloudAccount{Provider: "gcp", GCPAuthMode: "application_default"},
			wantOK:      true,
			wantFound:   true,
			msgContains: "application default",
		},
		{
			name:      "gcp other mode — not handled",
			acct:      &config.CloudAccount{Provider: "gcp", GCPAuthMode: "service_account"},
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, found := ambientCredResult(tt.acct)
			assert.Equal(t, tt.wantFound, found)
			if tt.wantFound {
				assert.Equal(t, tt.wantOK, result.OK)
				if tt.msgContains != "" {
					assert.True(t, strings.Contains(result.Message, tt.msgContains), "want %q in %q", tt.msgContains, result.Message)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// credTypeForAccount
// ---------------------------------------------------------------------------

func TestCredTypeForAccount(t *testing.T) {
	tests := []struct {
		acct     *config.CloudAccount
		expected string
	}{
		{&config.CloudAccount{Provider: "aws"}, "aws_access_keys"},
		{&config.CloudAccount{Provider: "azure", AzureAuthMode: "service_principal"}, "azure_client_secret"},
		{&config.CloudAccount{Provider: "azure", AzureAuthMode: "workload_identity_federation"}, "azure_wif_private_key"},
		{&config.CloudAccount{Provider: "gcp", GCPAuthMode: "service_account"}, "gcp_service_account"},
		{&config.CloudAccount{Provider: "gcp", GCPAuthMode: "workload_identity_federation"}, "gcp_workload_identity_config"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, credTypeForAccount(tt.acct))
		})
	}
}

// ---------------------------------------------------------------------------
// checkCredentialPresence
// ---------------------------------------------------------------------------

func TestHandler_checkCredentialPresence_WithCredStore_Found(t *testing.T) {
	ctx := context.Background()
	mockCred := &MockCredentialStore{}
	acct := &config.CloudAccount{ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Provider: "aws"}

	// MockCredentialStore.HasCredential always returns false; use a custom mock
	customCred := &mockCredStoreHas{has: true}
	h := &Handler{credStore: customCred}

	result, err := h.checkCredentialPresence(ctx, acct)
	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Contains(t, result.Message, "configured")
	_ = mockCred
}

func TestHandler_checkCredentialPresence_WithCredStore_NotFound(t *testing.T) {
	ctx := context.Background()
	acct := &config.CloudAccount{ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Provider: "aws"}

	customCred := &mockCredStoreHas{has: false}
	h := &Handler{credStore: customCred}

	result, err := h.checkCredentialPresence(ctx, acct)
	require.NoError(t, err)
	assert.False(t, result.OK)
}

func TestHandler_checkCredentialPresence_FallbackToConfigStore(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	acct := &config.CloudAccount{ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Provider: "aws"}

	// MockConfigStore.HasAccountCredentials returns false by default
	h := &Handler{credStore: nil, config: mockStore}

	result, err := h.checkCredentialPresence(ctx, acct)
	require.NoError(t, err)
	// Default MockConfigStore returns (false, nil)
	assert.False(t, result.OK)
}

// ---------------------------------------------------------------------------
// getRecommendations — additional branches
// ---------------------------------------------------------------------------

func TestHandler_getRecommendations_InvalidProvider(t *testing.T) {
	h := &Handler{apiKey: "test-key"}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"x-api-key": "test-key"}}
	_, err := h.getRecommendations(context.Background(), req, map[string]string{
		"provider": "unknown-cloud",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid provider")
}

func TestHandler_getRecommendations_InvalidService(t *testing.T) {
	h := &Handler{apiKey: "test-key"}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"x-api-key": "test-key"}}
	_, err := h.getRecommendations(context.Background(), req, map[string]string{
		"provider": "aws",
		"service":  "UPPERCASE",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid service name")
}

func TestHandler_getRecommendations_InvalidRegion(t *testing.T) {
	h := &Handler{apiKey: "test-key"}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"x-api-key": "test-key"}}
	_, err := h.getRecommendations(context.Background(), req, map[string]string{
		"provider": "aws",
		"service":  "rds",
		"region":   "INVALID_REGION!",
	})
	require.Error(t, err)
}

func TestHandler_getRecommendations_InvalidAccountIDs(t *testing.T) {
	h := &Handler{apiKey: "test-key"}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"x-api-key": "test-key"}}
	_, err := h.getRecommendations(context.Background(), req, map[string]string{
		"provider":    "aws",
		"account_ids": "not-a-uuid",
	})
	require.Error(t, err)
}

func TestHandler_getRecommendations_SchedulerError(t *testing.T) {
	ctx := context.Background()
	mockScheduler := new(MockScheduler)
	mockScheduler.On("GetRecommendations", ctx, mock.Anything).
		Return(nil, errors.New("scheduler down"))

	h := &Handler{scheduler: mockScheduler, apiKey: "test-key"}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"x-api-key": "test-key"}}
	_, err := h.getRecommendations(ctx, req, map[string]string{"provider": "aws"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get recommendations")
}

func TestHandler_getRecommendations_WithResults(t *testing.T) {
	ctx := context.Background()
	mockScheduler := new(MockScheduler)
	recs := []config.RecommendationRecord{
		{Service: "rds", Region: "us-east-1", Savings: 100.0, UpfrontCost: 200.0},
		{Service: "rds", Region: "eu-west-1", Savings: 50.0, UpfrontCost: 100.0},
	}
	mockScheduler.On("GetRecommendations", ctx, mock.Anything).Return(recs, nil)

	h := &Handler{scheduler: mockScheduler, apiKey: "test-key"}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"x-api-key": "test-key"}}
	result, err := h.getRecommendations(ctx, req, map[string]string{"provider": "aws"})
	require.NoError(t, err)

	assert.Equal(t, 2, result.Summary.TotalCount)
	assert.InDelta(t, 150.0, result.Summary.TotalMonthlySavings, 0.001)
	assert.InDelta(t, 300.0, result.Summary.TotalUpfrontCost, 0.001)
	// AvgPayback = upfront / savings = 300/150 = 2
	assert.InDelta(t, 2.0, result.Summary.AvgPaybackMonths, 0.001)
	assert.Len(t, result.Regions, 2)
}

func TestHandler_getRecommendations_ZeroSavings(t *testing.T) {
	ctx := context.Background()
	mockScheduler := new(MockScheduler)
	recs := []config.RecommendationRecord{
		{Service: "rds", Region: "us-east-1", Savings: 0, UpfrontCost: 0},
	}
	mockScheduler.On("GetRecommendations", ctx, mock.Anything).Return(recs, nil)

	h := &Handler{scheduler: mockScheduler, apiKey: "test-key"}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"x-api-key": "test-key"}}
	result, err := h.getRecommendations(ctx, req, map[string]string{})
	require.NoError(t, err)
	// With zero savings, AvgPayback should be 0 (no division by zero)
	assert.Equal(t, float64(0), result.Summary.AvgPaybackMonths)
}

// ---------------------------------------------------------------------------
// sendPurchaseApprovalEmail — nil emailNotifier path
// ---------------------------------------------------------------------------

func TestHandler_sendPurchaseApprovalEmail_NilNotifier(t *testing.T) {
	// When emailNotifier is nil the function returns immediately without panicking.
	h := &Handler{emailNotifier: nil}
	// Should not panic.
	h.sendPurchaseApprovalEmail(
		context.Background(),
		&config.PurchaseExecution{ExecutionID: "test-id"},
		nil,
		0,
		0,
	)
}

func TestHandler_sendPurchaseApprovalEmail_NoNotificationEmail(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	// GetGlobalConfig returns a config without a notification email
	globalCfg := &config.GlobalConfig{}
	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)

	// Use a non-nil emailNotifier stub so we exercise the GetGlobalConfig branch.
	h := &Handler{
		config:        mockStore,
		emailNotifier: &stubEmailNotifier{},
	}

	// Should not panic or call the notifier.
	h.sendPurchaseApprovalEmail(
		ctx,
		&config.PurchaseExecution{ExecutionID: "test-id"},
		nil,
		0,
		0,
	)
}

// ---------------------------------------------------------------------------
// Helper types for tests above
// ---------------------------------------------------------------------------

// mockCredStoreHas is a credential store stub where HasCredential is configurable.
type mockCredStoreHas struct {
	MockCredentialStore
	has bool
	err error
}

func (m *mockCredStoreHas) HasCredential(_ context.Context, _, _ string) (bool, error) {
	return m.has, m.err
}

// stubEmailNotifier implements email.SenderInterface with no-ops.
// It is used to inject a non-nil notifier without triggering actual sends.
type stubEmailNotifier struct{}

func (s *stubEmailNotifier) SendNotification(_ context.Context, _, _ string) error { return nil }
func (s *stubEmailNotifier) SendToEmail(_ context.Context, _, _, _ string) error   { return nil }
func (s *stubEmailNotifier) SendNewRecommendationsNotification(_ context.Context, _ email.NotificationData) error {
	return nil
}
func (s *stubEmailNotifier) SendScheduledPurchaseNotification(_ context.Context, _ email.NotificationData) error {
	return nil
}
func (s *stubEmailNotifier) SendPurchaseConfirmation(_ context.Context, _ email.NotificationData) error {
	return nil
}
func (s *stubEmailNotifier) SendPurchaseFailedNotification(_ context.Context, _ email.NotificationData) error {
	return nil
}
func (s *stubEmailNotifier) SendPasswordResetEmail(_ context.Context, _, _ string) error { return nil }
func (s *stubEmailNotifier) SendWelcomeEmail(_ context.Context, _, _, _ string) error    { return nil }
func (s *stubEmailNotifier) SendRIExchangePendingApproval(_ context.Context, _ email.RIExchangeNotificationData) error {
	return nil
}
func (s *stubEmailNotifier) SendRIExchangeCompleted(_ context.Context, _ email.RIExchangeNotificationData) error {
	return nil
}
func (s *stubEmailNotifier) SendPurchaseApprovalRequest(_ context.Context, _ email.NotificationData) error {
	return nil
}
func (s *stubEmailNotifier) SendRegistrationReceivedNotification(_ context.Context, _ email.RegistrationNotificationData) error {
	return nil
}
func (s *stubEmailNotifier) SendRegistrationDecisionNotification(_ context.Context, _ string, _ email.RegistrationDecisionData) error {
	return nil
}

var _ email.SenderInterface = (*stubEmailNotifier)(nil)
