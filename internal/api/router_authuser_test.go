package api //nolint:revive // var-naming: package name "api" is intentional for handler package

import (
	"context"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

// TestRequireAuth_UserAPIKey verifies that a valid user API key yields a
// Principal with Kind == PrincipalUserAPIKey and populated UserID/Email.
// The mock returns the real *auth.User concrete type — the same type that
// auth.Service.ValidateUserAPIKeyAPI returns in production — so this test
// exercises the same type assertion that principalFromUserAPIKey performs.
func TestRequireAuth_UserAPIKey(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	userRec := &auth.User{ID: "uid-123", Email: "alice@example.com"}
	mockAuth.On("ValidateUserAPIKeyAPI", ctx, "valid-user-key").
		Return(nil, userRec, nil)

	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"X-API-Key": "valid-user-key"},
	}
	p, err := h.requireAuth(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, PrincipalUserAPIKey, p.Kind)
	assert.Equal(t, "uid-123", p.UserID)
	assert.Equal(t, "alice@example.com", p.Email)
}

// TestRequireAuth_UserAPIKey_BadRecord verifies that principalFromUserAPIKey
// fails closed when ValidateUserAPIKeyAPI succeeds but returns a userRaw
// value that is not *auth.User (unexpected concrete type). Post-fix it
// must return nil and the overall requireAuth must return a 401 ClientError.
func TestRequireAuth_UserAPIKey_BadRecord(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	// Return a value that is NOT *auth.User — the concrete type principalFromUserAPIKey asserts.
	type unexpectedType struct{ Name string }
	mockAuth.On("ValidateUserAPIKeyAPI", ctx, "bad-record-key").
		Return(nil, &unexpectedType{Name: "oops"}, nil)
	// Bearer and session path must also return nothing.
	mockAuth.On("ValidateSession", ctx, mock.Anything).
		Return(nil, errors.New("no session")).Maybe()

	h := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"X-API-Key": "bad-record-key"},
	}
	_, err := h.requireAuth(ctx, req)
	require.Error(t, err, "expected denial when user record has unexpected type")
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 401, ce.code)
}

// TestRequireAuth_NilAuth_Rejects verifies that a Handler constructed with
// auth == nil returns a 401 ClientError for a non-admin credential, confirming
// the h.auth == nil branch fails closed.
func TestRequireAuth_NilAuth_Rejects(t *testing.T) {
	// No admin key configured, auth is nil.
	h := &Handler{auth: nil}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"X-API-Key": "some-key"},
	}
	_, err := h.requireAuth(context.Background(), req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 401, ce.code)
}
