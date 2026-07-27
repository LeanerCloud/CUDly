package api

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// listAPIKeysUsageStats handler -- section-level summary (issue #340/#344
// deferred sub-task). Mirrors the listAPIKeys test layout: happy path for
// an authenticated session, a no-auth-service failure, a service-error
// passthrough, and a permission-denied path for a caller lacking the
// view/api-keys permission.

func TestHandler_listAPIKeysUsageStats_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "user-123", Email: "user@example.com"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.grantAdmin()

	expectedStats := map[string]any{
		"total_active":            2,
		"total_requests_window":   int64(15),
		"total_requests_lifetime": int64(120),
		"top_keys":                []map[string]any{{"id": "key-1", "name": "k1", "key_prefix": "abc12345", "request_count_window": int64(10)}},
	}
	mockAuth.On("GetAPIKeysUsageStatsAPI", ctx, "user-123").Return(expectedStats, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer test-token"},
	}

	result, err := handler.listAPIKeysUsageStats(ctx, req)
	require.NoError(t, err)
	got, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 2, got["total_active"])
	mockAuth.AssertCalled(t, "GetAPIKeysUsageStatsAPI", ctx, "user-123")
}

func TestHandler_listAPIKeysUsageStats_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	req := &events.LambdaFunctionURLRequest{}

	_, err := handler.listAPIKeysUsageStats(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_listAPIKeysUsageStats_ServiceError(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "user-123", Email: "user@example.com"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.grantAdmin()
	mockAuth.On("GetAPIKeysUsageStatsAPI", ctx, "user-123").Return(nil, errors.New("database error"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer test-token"},
	}

	_, err := handler.listAPIKeysUsageStats(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load API key usage stats")
}

func TestHandler_listAPIKeysUsageStats_PermissionDenied(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "viewer-1", Email: "viewer@example.com"}
	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(session, nil)
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "view", "api-keys").Return(false, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer viewer-token"},
	}

	_, err := handler.listAPIKeysUsageStats(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
	mockAuth.AssertNotCalled(t, "GetAPIKeysUsageStatsAPI", mock.Anything, mock.Anything)
}
