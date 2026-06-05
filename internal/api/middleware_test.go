package api

import (
	"context"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandler_isPublicEndpoint(t *testing.T) {
	handler := &Handler{corsAllowedOrigin: "*"}

	tests := []struct {
		path     string
		expected bool
	}{
		{"/api/health", true},
		{"/api/purchases/approve/12345678-1234-1234-1234-123456789abc", true},
		{"/api/purchases/cancel/45645645-6456-4564-5645-645645645645", true},
		{"/api/config", false},
		{"/api/recommendations", false},
		{"/api/plans", false},
		{"/api/history", false},
		// /version is a public endpoint but must be exact-matched only.
		{"/version", true},
		{"/version-evil", false},      // prefix overlap must not bypass auth
		{"/versionXYZ", false},        // prefix overlap must not bypass auth
		{"/api/register", true},       // POST /api/register (exact)
		{"/api/registrations", false}, // must not match via prefix
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := handler.isPublicEndpoint(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_authenticate(t *testing.T) {
	tests := []struct {
		name     string
		apiKey   string
		headers  map[string]string
		params   map[string]string
		expected bool
	}{
		{
			name:     "no API key configured - deny access",
			apiKey:   "",
			headers:  map[string]string{},
			params:   map[string]string{},
			expected: false,
		},
		{
			name:     "valid API key in X-API-Key header",
			apiKey:   "secret-key",
			headers:  map[string]string{"X-API-Key": "secret-key"},
			params:   map[string]string{},
			expected: true,
		},
		{
			name:     "valid API key in x-api-key header (lowercase)",
			apiKey:   "secret-key",
			headers:  map[string]string{"x-api-key": "secret-key"},
			params:   map[string]string{},
			expected: true,
		},
		{
			name:     "API key in query parameter (not supported for security)",
			apiKey:   "secret-key",
			headers:  map[string]string{},
			params:   map[string]string{"api_key": "secret-key"},
			expected: false, // Query parameters are not supported for security reasons
		},
		{
			name:     "invalid API key",
			apiKey:   "secret-key",
			headers:  map[string]string{"X-API-Key": "wrong-key"},
			params:   map[string]string{},
			expected: false,
		},
		{
			name:     "missing API key when configured",
			apiKey:   "secret-key",
			headers:  map[string]string{},
			params:   map[string]string{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			handler := &Handler{apiKey: tt.apiKey}
			req := &events.LambdaFunctionURLRequest{
				Headers:               tt.headers,
				QueryStringParameters: tt.params,
			}
			result := handler.authenticate(ctx, req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_extractBearerToken(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		expected string
	}{
		{
			name:     "valid bearer token with X-Authorization header",
			headers:  map[string]string{"X-Authorization": "Bearer my-token-x"},
			expected: "my-token-x",
		},
		{
			name:     "valid bearer token with lowercase x-authorization header",
			headers:  map[string]string{"x-authorization": "Bearer my-token-x-lower"},
			expected: "my-token-x-lower",
		},
		{
			name:     "X-Authorization takes priority over Authorization",
			headers:  map[string]string{"X-Authorization": "Bearer x-token", "Authorization": "Bearer auth-token"},
			expected: "x-token",
		},
		{
			name:     "valid bearer token with Authorization header",
			headers:  map[string]string{"Authorization": "Bearer my-token-123"},
			expected: "my-token-123",
		},
		{
			name:     "valid bearer token with lowercase authorization header",
			headers:  map[string]string{"authorization": "Bearer my-token-456"},
			expected: "my-token-456",
		},
		{
			name:     "no bearer prefix",
			headers:  map[string]string{"Authorization": "my-token-789"},
			expected: "",
		},
		{
			name:     "empty authorization header",
			headers:  map[string]string{"Authorization": ""},
			expected: "",
		},
		{
			name:     "no authorization header",
			headers:  map[string]string{},
			expected: "",
		},
		{
			name:     "bearer without token",
			headers:  map[string]string{"Authorization": "Bearer "},
			expected: "",
		},
		{
			name:     "lowercase bearer scheme",
			headers:  map[string]string{"Authorization": "bearer my-token-lowercase"},
			expected: "my-token-lowercase",
		},
		{
			name:     "uppercase bearer scheme",
			headers:  map[string]string{"Authorization": "BEARER my-token-upper"},
			expected: "my-token-upper",
		},
	}

	handler := &Handler{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &events.LambdaFunctionURLRequest{
				Headers: tt.headers,
			}
			result := handler.extractBearerToken(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandler_authenticate_BearerToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{
		UserID: "11111111-1111-1111-1111-111111111111",
		Email:  "user@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "valid-token").Return(session, nil)
	mockAuth.On("ValidateSession", ctx, "invalid-token").Return(nil, errors.New("invalid session"))

	handler := &Handler{auth: mockAuth, apiKey: ""}

	// Test valid token - valid bearer token allows access
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer valid-token",
		},
	}
	assert.True(t, handler.authenticate(ctx, req))

	// Test invalid token - invalid bearer token denies access
	req = &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer invalid-token",
		},
	}
	// Should be false because token is invalid and no API key provided
	assert.False(t, handler.authenticate(ctx, req))
}

func TestHandler_authenticate_BearerTokenWithAPIKey(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{
		UserID: "11111111-1111-1111-1111-111111111111",
		Email:  "user@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "valid-token").Return(session, nil)
	mockAuth.On("ValidateSession", ctx, "invalid-token").Return(nil, errors.New("invalid session"))

	handler := &Handler{auth: mockAuth, apiKey: "configured-api-key"}

	// Test valid bearer token when API key is configured
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer valid-token",
		},
	}
	assert.True(t, handler.authenticate(ctx, req))

	// Test invalid bearer token when API key is configured
	req = &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer invalid-token",
		},
	}
	// Should be false because API key is configured and bearer token is invalid
	assert.False(t, handler.authenticate(ctx, req))
}

// TestHandler_requiresCSRFValidation_ExactMatch asserts that paths sharing a
// prefix with a CSRF-exempt route are NOT themselves exempt. The fix uses
// exact-match (switch) instead of strings.HasPrefix so that a path like
// /api/register-malicious is not silently exempted because it starts with
// /api/register.
func TestHandler_requiresCSRFValidation_ExactMatch(t *testing.T) {
	handler := &Handler{}

	tests := []struct {
		method       string
		path         string
		requiresCSRF bool
	}{
		// Exempt routes must still pass (exact-match still covers them).
		{"POST", "/api/auth/login", false},
		{"POST", "/api/auth/setup-admin", false},
		{"POST", "/api/auth/forgot-password", false},
		{"POST", "/api/auth/reset-password", false},
		{"POST", "/api/register", false},
		// Paths sharing only a PREFIX with an exempt route must require CSRF.
		{"POST", "/api/register-malicious", true},
		{"POST", "/api/register-anything", true},
		{"POST", "/api/auth/login-evil", true},
		{"POST", "/api/auth/forgot-password-extra", true},
		// Non-exempt POST paths
		{"POST", "/api/config", true},
		{"POST", "/api/recommendations", true},
		// GET never requires CSRF regardless of path
		{"GET", "/api/register", false},
		{"GET", "/api/config", false},
		// DELETE on a protected endpoint requires CSRF
		{"DELETE", "/api/plans", true},
	}

	for _, tt := range tests {
		name := tt.method + " " + tt.path
		t.Run(name, func(t *testing.T) {
			result := handler.requiresCSRFValidation(tt.method, tt.path)
			assert.Equal(t, tt.requiresCSRF, result)
		})
	}
}

// ---------------------------------------------------------------------------
// CSRF boundary tests for session-authed approve/cancel (issue #404)
// ---------------------------------------------------------------------------

// TestApproveViaSession_RequiresCSRF asserts that a session-authenticated POST
// to approvePurchaseViaSession is rejected when the request carries no CSRF
// token.  Pre-fix, the blanket "/api/purchases/approve/" middleware exemption
// meant this call succeeded without a CSRF token.
func TestApproveViaSession_RequiresCSRF(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	adminEmail := "admin@example.com"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:     execID,
		ApprovalToken:   "email-tok",
		Status:          "pending",
		Recommendations: []config.RecommendationRecord{{ID: "r1"}},
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)

	mockAuth := new(MockAuthService)
	adminSession := &Session{Email: adminEmail}
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(adminSession, nil)
	// Authorization is group-membership-only after issue #907: the session must
	// pass the approve-* HasPermissionAPI check to reach the CSRF guard, since
	// the dispatcher authorizes before invoking approvePurchaseViaSession.
	mockAuth.grantAdmin()
	// CSRF token is empty → ValidateCSRFToken must return an error so the
	// request is rejected. This is the critical regression assertion for #404.
	mockAuth.On("ValidateCSRFToken", ctx, "sess-tok", "").Return(errors.New("csrf mismatch"))
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	handler := &Handler{config: mockConfig, auth: mockAuth}

	// Request with a valid session but NO CSRF token header.
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	_, err := handler.approvePurchase(ctx, req, execID, "")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a ClientError, got: %v", err)
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, ce.Error(), "CSRF validation failed")
}

// TestApproveViaSession_PassesCSRF asserts that a session-authenticated POST
// to approvePurchaseViaSession succeeds when a valid CSRF token is supplied.
// This confirms the CSRF guard does not block the legitimate dashboard flow.
func TestApproveViaSession_PassesCSRF(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	adminEmail := "admin@example.com"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:     execID,
		ApprovalToken:   "email-tok",
		Status:          "pending",
		Recommendations: []config.RecommendationRecord{{ID: "r1"}},
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)

	mockAuth := new(MockAuthService)
	adminSession := &Session{Email: adminEmail}
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(adminSession, nil)
	// Authorization is group-membership-only after issue #907: the session must
	// pass the approve-* HasPermissionAPI check before the CSRF guard runs.
	mockAuth.grantAdmin()
	// Valid CSRF token supplied → ValidateCSRFToken succeeds.
	mockAuth.On("ValidateCSRFToken", ctx, "sess-tok", "csrf-abc").Return(nil)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("ApproveAndExecute", ctx, execID, adminEmail, (*string)(nil)).Return(nil)

	handler := &Handler{config: mockConfig, auth: mockAuth, purchase: mockPurchase}

	// Request with a valid session AND a valid CSRF token header.
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"authorization": "Bearer sess-tok",
			"x-csrf-token":  "csrf-abc",
		},
	}
	result, err := handler.approvePurchase(ctx, req, execID, "")
	require.NoError(t, err)
	assert.Equal(t, "completed", result.(map[string]string)["status"])
}

// TestCancelViaSession_RequiresCSRF asserts that a session-authenticated POST
// to cancelPurchaseViaSession is rejected when the request carries no CSRF
// token.  Mirror of TestApproveViaSession_RequiresCSRF for the cancel path.
func TestCancelViaSession_RequiresCSRF(t *testing.T) {
	ctx := context.Background()
	execID := "55555555-5555-5555-5555-555555555555"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID: execID,
		Status:      "pending",
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)

	mockAuth := new(MockAuthService)
	adminSession := &Session{Email: "admin@example.com"}
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(adminSession, nil)
	// Authorization is group-membership-only after issue #907: the session must
	// pass the cancel-* HasPermissionAPI check to reach the CSRF guard, since
	// the dispatcher authorizes before invoking cancelPurchaseViaSession.
	mockAuth.grantAdmin()
	// No CSRF token → ValidateCSRFToken must return error to block the request.
	mockAuth.On("ValidateCSRFToken", ctx, "sess-tok", "").Return(errors.New("csrf mismatch"))
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	handler := &Handler{config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	_, err := handler.cancelPurchase(ctx, req, execID, "")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a ClientError, got: %v", err)
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, ce.Error(), "CSRF validation failed")
}

// TestTokenOnlyApprove_BypassesCSRF asserts that the email-link token path
// does NOT invoke approvePurchaseViaSession, so CSRF is not checked on that
// code path. The token path uses authorizeApprovalAction which validates the
// signed token directly -- it has no session cookie to forge.
//
// The caller must supply a session whose email matches the contact_email (the
// approval gate added in PR #101 ensures only the intended inbox can confirm
// via the email link). Because the session has no approve-* RBAC, the handler
// falls through from the session-authed branch to the token branch.
func TestTokenOnlyApprove_BypassesCSRF(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	contactEmail := "contact@example.com"

	mockConfig := new(MockConfigStore)
	accountID := "acct-1"
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "email-token",
		Status:        "pending",
		Recommendations: []config.RecommendationRecord{
			{ID: "r1", CloudAccountID: &accountID},
		},
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, ContactEmail: contactEmail}, nil
	}
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &contactEmail,
	}, nil)

	// Session is present but has no approve-* permissions → RBAC denies,
	// falls through to the token branch. CSRF is NOT called on the token
	// branch (approvePurchaseViaSession is never reached).
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: contactEmail}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-any", "purchases").Return(false, nil).Maybe()
	mockAuth.On("HasPermissionAPI", ctx, "", "approve-own", "purchases").Return(false, nil).Maybe()
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("ApproveExecution", ctx, execID, "email-token", contactEmail).Return(nil)

	handler := &Handler{config: mockConfig, auth: mockAuth, purchase: mockPurchase}

	// Session header present (so tryGetSession succeeds) but no CSRF header.
	// The session has no approve-* permission → falls to token branch.
	// No ValidateCSRFToken mock is registered here, so if the code ever
	// called ValidateCSRFToken, the test would panic with "unexpected call".
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
		// Intentionally no x-csrf-token header.
	}
	result, err := handler.approvePurchase(ctx, req, execID, "email-token")
	require.NoError(t, err)
	assert.Equal(t, "completed", result.(map[string]string)["status"])
}

// Regression test for #404: approve/cancel/reject paths must require CSRF
// when the request carries a session bearer token. Previously they were
// unconditionally exempt, meaning a logged-in user could be CSRF-attacked
// into approving a purchase via a malicious page.
func TestRequiresCSRFValidation_TokenBasedPathsWithSession(t *testing.T) {
	h := &Handler{}

	tokenOnlyReq := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
	}
	sessionReq := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer some-session-token",
		},
	}

	tokenPaths := []string{
		"/api/purchases/approve/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"/api/purchases/cancel/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"/api/ri-exchange/approve/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"/api/ri-exchange/reject/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	}

	for _, path := range tokenPaths {
		// Token-only (no session): CSRF should NOT be required.
		if got := h.requiresCSRFValidation("POST", path, tokenOnlyReq); got {
			t.Errorf("path %s with no session: expected requiresCSRFValidation=false (token-only flow), got true (regression of #404)", path)
		}

		// Session present: CSRF MUST be required (issue #404).
		if got := h.requiresCSRFValidation("POST", path, sessionReq); !got {
			t.Errorf("path %s with session: expected requiresCSRFValidation=true (session flow requires CSRF), got false (regression of #404)", path)
		}
	}
}

// Regression test for #1017: /api/registrations/* admin routes must require CSRF.
// Previously the csrfExemptAlways list contained "/api/register" matched with
// strings.HasPrefix, which also matched /api/registrations/<id>/approve|reject|delete.
// Those routes are AuthAdmin and state-changing: approving a registration creates
// an enabled cloud account and stores attacker-supplied credentials.
func TestRequiresCSRFValidation_RegistrationsAdminRoutesAreNotExempt(t *testing.T) {
	h := &Handler{}
	sessionReq := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer some-session-token",
		},
	}

	adminRoutes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/registrations/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/approve"},
		{"POST", "/api/registrations/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/reject"},
		{"DELETE", "/api/registrations/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
	}

	for _, tc := range adminRoutes {
		got := h.requiresCSRFValidation(tc.method, tc.path, sessionReq)
		if !got {
			t.Errorf("admin route %s %s: expected requiresCSRFValidation=true (must be CSRF-protected, regression of #1017), got false",
				tc.method, tc.path)
		}
	}
}

// Regression guard: unconditionally-exempt paths (login, setup-admin, etc.) must
// remain exempt regardless of whether a session is present.
func TestRequiresCSRFValidation_AlwaysExemptPaths(t *testing.T) {
	h := &Handler{}

	sessionReq := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer some-session-token",
		},
	}

	alwaysExempt := []string{
		"/api/auth/login",
		"/api/auth/setup-admin",
		"/api/auth/forgot-password",
		"/api/auth/reset-password",
		"/api/register",
	}

	for _, path := range alwaysExempt {
		if got := h.requiresCSRFValidation("POST", path, sessionReq); got {
			t.Errorf("always-exempt path %s: expected requiresCSRFValidation=false even with session, got true", path)
		}
	}
}

// Regression test for 02-M1: checkRateLimitStrict must return 503 when the
// rate limiter encounters an error (fail-closed), whereas checkRateLimit must
// return nil (fail-open) for the same error.
func TestHandler_checkRateLimitStrict_FailsClosedOnError(t *testing.T) {
	ctx := context.Background()
	limiterErr := errors.New("db pool exhausted")

	mockRL := new(MockRateLimiter)
	mockRL.On("AllowWithIP", ctx, "1.2.3.4", "login").Return(false, limiterErr)
	t.Cleanup(func() { mockRL.AssertExpectations(t) })

	h := &Handler{rateLimiter: mockRL}
	req := makeSourceIPReq("1.2.3.4")

	err := h.checkRateLimitStrict(ctx, req, "login")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "checkRateLimitStrict must return a ClientError on limiter error")
	assert.Equal(t, 503, ce.code, "checkRateLimitStrict must return 503 (fail-closed) on limiter error")
}

func TestHandler_checkRateLimit_FailsOpenOnError(t *testing.T) {
	ctx := context.Background()
	limiterErr := errors.New("db pool exhausted")

	mockRL := new(MockRateLimiter)
	mockRL.On("AllowWithIP", ctx, "1.2.3.4", "api_general").Return(false, limiterErr)
	t.Cleanup(func() { mockRL.AssertExpectations(t) })

	h := &Handler{rateLimiter: mockRL}
	req := makeSourceIPReq("1.2.3.4")

	err := h.checkRateLimit(ctx, req, "api_general")
	assert.NoError(t, err, "checkRateLimit must allow through (fail-open) on limiter error")
}

func TestHandler_checkRateLimitStrict_Returns429WhenDenied(t *testing.T) {
	ctx := context.Background()

	mockRL := new(MockRateLimiter)
	mockRL.On("AllowWithIP", ctx, "1.2.3.4", "login").Return(false, nil)
	t.Cleanup(func() { mockRL.AssertExpectations(t) })

	h := &Handler{rateLimiter: mockRL}
	req := makeSourceIPReq("1.2.3.4")

	err := h.checkRateLimitStrict(ctx, req, "login")
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 429, ce.code)
}

// makeSourceIPReq builds a minimal request with the given source IP.
func makeSourceIPReq(ip string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				SourceIP: ip,
			},
		},
	}
}
