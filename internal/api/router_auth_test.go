package api

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// userSession returns a session for a non-admin authenticated user.
func userSession() *Session {
	return &Session{
		UserID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		Email:  "user@example.com",
		Role:   "user",
	}
}

// stubHandler is a no-op route handler — its presence proves the router
// reached the handler stage rather than short-circuiting on auth.
func stubHandler(_ context.Context, _ *events.LambdaFunctionURLRequest, _ map[string]string) (any, error) {
	return map[string]string{"status": "ok"}, nil
}

// TestRouter_AuthUser_RejectsMissingAuth pins the issue-#60 regression:
// a route declared as Auth: AuthUser must be rejected by Router.Route at
// the router layer when no Authorization header is present, regardless
// of whether validateSecurity middleware ran beforehand.
func TestRouter_AuthUser_RejectsMissingAuth(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	h := &Handler{auth: mockAuth}
	r := &Router{h: h}
	r.routes = []Route{
		{ExactPath: "/api/test/user", Method: "GET", Handler: stubHandler, Auth: AuthUser},
	}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{}, // NO Authorization header
	}
	_, err := r.Route(ctx, "GET", "/api/test/user", req)
	require.Error(t, err, "router must reject AuthUser routes without auth at the router layer")
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T", err)
	assert.Equal(t, 401, ce.code)
}

// TestRouter_AuthUser_RejectsInvalidSession pins that an invalid Bearer
// token (session not found / expired) returns 401 from the router layer.
func TestRouter_AuthUser_RejectsInvalidSession(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "stale-token").Return(nil, errors.New("session not found"))
	h := &Handler{auth: mockAuth}
	r := &Router{h: h}
	r.routes = []Route{
		{ExactPath: "/api/test/user", Method: "GET", Handler: stubHandler, Auth: AuthUser},
	}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer stale-token"},
	}
	_, err := r.Route(ctx, "GET", "/api/test/user", req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 401, ce.code)
}

// TestRouter_AuthUser_AcceptsValidSession confirms the router-layer
// check passes through to the handler when a valid session is present.
// This is the positive case for the new requireUser helper.
func TestRouter_AuthUser_AcceptsValidSession(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession(), nil)
	h := &Handler{auth: mockAuth}
	r := &Router{h: h}
	r.routes = []Route{
		{ExactPath: "/api/test/user", Method: "GET", Handler: stubHandler, Auth: AuthUser},
	}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer user-token"},
	}
	result, err := r.Route(ctx, "GET", "/api/test/user", req)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"status": "ok"}, result)
}

// TestRouter_AuthUser_AcceptsAdminAPIKey confirms that an admin API key
// also satisfies the AuthUser requirement (admins are users).
func TestRouter_AuthUser_AcceptsAdminAPIKey(t *testing.T) {
	ctx := context.Background()
	h := &Handler{apiKey: "admin-key"} // checkAdminAPIKey compares against this
	r := &Router{h: h}
	r.routes = []Route{
		{ExactPath: "/api/test/user", Method: "GET", Handler: stubHandler, Auth: AuthUser},
	}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"x-api-key": "admin-key"},
	}
	result, err := r.Route(ctx, "GET", "/api/test/user", req)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"status": "ok"}, result)
}

// TestRouter_AuthPublic_NoAuthCheck confirms AuthPublic routes still
// run with no auth at all — guard against accidentally tightening to
// AuthUser/AuthAdmin via the new switch's default branch.
func TestRouter_AuthPublic_NoAuthCheck(t *testing.T) {
	ctx := context.Background()
	h := &Handler{}
	r := &Router{h: h}
	r.routes = []Route{
		{ExactPath: "/api/test/public", Method: "GET", Handler: stubHandler, Auth: AuthPublic},
	}

	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{}}
	result, err := r.Route(ctx, "GET", "/api/test/public", req)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"status": "ok"}, result)
}
