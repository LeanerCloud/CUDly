package api //nolint:revive // var-naming: package name "api" is intentional for handler package

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// API key usage is booked in validateSecurityContext and nowhere else,
// because that is the one code path guaranteed to run exactly once per
// inbound request. These tests pin that contract at the api layer: which
// principal kinds book, how many times, and against which key ID.
//
// They cannot on their own prove the per-validation overcount is gone --
// MockAuthService stubs the validation and permission calls, so no real
// booking path runs. The end-to-end guards in
// internal/server/apikey_usage_booking_test.go cover that; these cover the
// handler-side half of the contract cheaply and deterministically.

func usageBookingLambdaRequest(headers map[string]string, method, path string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: headers,
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{Method: method, Path: path},
		},
	}
}

// TestHandleRequest_UserAPIKeyBooksUsageOnceWithResolvedKeyID verifies that an
// API-key-authenticated request books exactly one usage, against the key ID
// the credential resolved to -- even though the request validates the
// credential again inside the permission check.
func TestHandleRequest_UserAPIKeyBooksUsageOnceWithResolvedKeyID(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	user := &auth.User{ID: "api-key-user", Email: "user@example.com"}
	mockAuth.On("ValidateUserAPIKeyAPI", ctx, "user-key").
		Return(&auth.UserAPIKey{ID: "key-1"}, user, nil).Once()
	mockAuth.On("HasAPIKeyPermissionAPI", mock.Anything, "user-key", "view", "api-keys").
		Return("api-key-user", "key-1", true, nil).Once()
	mockAuth.On("GetAPIKeysUsageStatsAPI", mock.Anything, "api-key-user").
		Return(&auth.APIKeysUsageStatsResponse{}, nil).Once()

	h := &Handler{auth: mockAuth}
	resp, err := h.HandleRequest(ctx,
		usageBookingLambdaRequest(map[string]string{"X-API-Key": "user-key"},
			"GET", "/api/api-keys/usage-stats"))

	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode, "body: %s", resp.Body)
	require.Equal(t, []string{"key-1"}, mockAuth.UsageBookings(),
		"one request must book exactly one usage, against the resolved key ID")
}

// TestHandleRequest_SessionAuthBooksNoAPIKeyUsage verifies that a
// session-authenticated request books nothing: there is no key to attribute
// it to, and booking would inflate whichever key happened to be nearby.
func TestHandleRequest_SessionAuthBooksNoAPIKeyUsage(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockAuth.On("ValidateSession", ctx, "session-token").
		Return(&Session{UserID: "session-user"}, nil).Once()
	mockAuth.On("GetUserPermissionsAPI", mock.Anything, "session-user").
		Return([]auth.APIPermission{}, nil).Once()

	h := &Handler{auth: mockAuth}
	resp, err := h.HandleRequest(ctx,
		usageBookingLambdaRequest(map[string]string{"Authorization": "Bearer session-token"},
			"GET", "/api/auth/me/permissions"))

	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode, "body: %s", resp.Body)
	require.Empty(t, mockAuth.UsageBookings(), "session auth must not book API key usage")
}

// TestHandleRequest_AdminAPIKeyBooksNoUsage verifies that the shared admin
// API key books nothing. It is a stateless infrastructure credential with no
// api_keys row, so there is no counter to increment.
func TestHandleRequest_AdminAPIKeyBooksNoUsage(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	h := &Handler{auth: mockAuth, apiKey: "admin-key"}
	resp, err := h.HandleRequest(ctx,
		usageBookingLambdaRequest(map[string]string{"X-API-Key": "admin-key"},
			"GET", "/api/auth/me/permissions"))

	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode, "body: %s", resp.Body)
	require.Empty(t, mockAuth.UsageBookings(), "the admin API key has no api_keys row to book against")
}
