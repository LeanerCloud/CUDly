package api

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the defence-in-depth AuthUser enforcement added to
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
// route returns 401 when the bearer token is not recognised by the auth
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
	userSession := &Session{UserID: "11111111-1111-1111-1111-111111111111", Role: "user"}
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
// Router.Route must not regress public-endpoint behaviour.
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

// TestRequireAuth_AdminAPIKey verifies the new requireAuth helper accepts
// the admin API key.
func TestRequireAuth_AdminAPIKey(t *testing.T) {
	h := &Handler{apiKey: "admin-secret"}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"X-API-Key": "admin-secret"},
	}
	require.NoError(t, h.requireAuth(context.Background(), req))
}

// TestRequireAuth_UserSession verifies requireAuth accepts a valid
// non-admin user session.
func TestRequireAuth_UserSession(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	userSession := &Session{UserID: "uid", Role: "user"}
	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer user-token"},
	}
	require.NoError(t, h.requireAuth(ctx, req))
}

// TestRequireAuth_NoCredential_Rejects verifies requireAuth returns a 401
// ClientError when no credential is presented.
func TestRequireAuth_NoCredential_Rejects(t *testing.T) {
	mockAuth := new(MockAuthService)
	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{}}
	err := h.requireAuth(context.Background(), req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 401, ce.code)
}
