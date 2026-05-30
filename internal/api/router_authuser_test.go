package api //nolint:revive // var-naming: package name "api" is intentional for handler package

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the defense-in-depth AuthUser enforcement added to
// Router.Route — see issue #60. Before the fix, AuthUser routes (e.g.
// /api/auth/logout, /api/api-keys, /api/federation/iac) fell through the
// router with no auth check; only the validateSecurity middleware
// protected them. These tests pin the router-level enforcement so a
// future middleware refactor can't silently expose them.

// TestRouterAuthUser_NoCredentials_Rejects verifies that an AuthUser
// route returns a 401 ClientError when no credential is presented.
func TestRouterAuthUser_NoCredentials_Rejects(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	h := &Handler{auth: mockAuth}
	r := NewRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
	}

	_, err := r.Route(ctx, "POST", "/api/auth/logout", req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 401, ce.code)
}

// TestRouterAuthUser_InvalidBearerToken_Rejects verifies that an AuthUser
// route returns 401 when the bearer token is not recognized by the auth
// service.
func TestRouterAuthUser_InvalidBearerToken_Rejects(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateUserAPIKeyAPI", ctx, "").
		Return(nil, nil, errors.New("empty key"))
	mockAuth.On("ValidateSession", ctx, "bad-token").
		Return(nil, errors.New("expired"))
	h := &Handler{auth: mockAuth}
	r := NewRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer bad-token"},
	}

	_, err := r.Route(ctx, "GET", "/api/api-keys", req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 401, ce.code)
}

// TestRouterAuthUser_ValidUserSession_Accepts verifies that an AuthUser
// route dispatches to the handler when a valid non-admin user session is
// present. Before the fix, AuthUser dispatch was unconditional — this
// test ensures the new check doesn't accidentally reject legitimate
// users.
func TestRouterAuthUser_ValidUserSession_Accepts(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	userSession := &Session{UserID: "11111111-1111-1111-1111-111111111111"}
	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
	mockAuth.On("Logout", ctx, "user-token").Return(nil)
	h := &Handler{auth: mockAuth}
	r := NewRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer user-token"},
	}

	result, err := r.Route(ctx, "POST", "/api/auth/logout", req)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

// TestRouterAuthPublic_NoCredentials_Accepts verifies that AuthPublic
// routes still dispatch with no credentials — the new switch in
// Router.Route must not regress public-endpoint behavior.
func TestRouterAuthPublic_NoCredentials_Accepts(t *testing.T) {
	ctx := context.Background()
	h := &Handler{}
	r := NewRouter(h)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
	}

	// /api/health is AuthPublic; healthCheckHandler doesn't depend on auth.
	_, err := r.Route(ctx, "GET", "/api/health", req)
	require.NoError(t, err)
}

// TestRequireAuth_AdminAPIKey verifies that requireAuth accepts the admin
// API key and returns a Principal of kind PrincipalAdminAPIKey.
func TestRequireAuth_AdminAPIKey(t *testing.T) {
	h := &Handler{apiKey: "admin-secret"}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"X-API-Key": "admin-secret"},
	}
	p, err := h.requireAuth(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, PrincipalAdminAPIKey, p.Kind)
	assert.Equal(t, "admin", p.Role)
}

// TestRequireAuth_UserSession verifies requireAuth accepts a valid
// non-admin user session and returns a populated Principal.
func TestRequireAuth_UserSession(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	userSession := &Session{UserID: "uid"}
	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer user-token"},
	}
	p, err := h.requireAuth(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, PrincipalSession, p.Kind)
	assert.Equal(t, "uid", p.UserID)
	assert.Equal(t, "user", p.Role)
	assert.Equal(t, userSession, p.Session)
}

// TestRequireAuth_NoCredential_Rejects verifies requireAuth returns a 401
// ClientError when no credential is presented.
func TestRequireAuth_NoCredential_Rejects(t *testing.T) {
	mockAuth := new(MockAuthService)
	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{}}
	_, err := h.requireAuth(context.Background(), req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 401, ce.code)
}
