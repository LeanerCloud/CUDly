package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockRateLimiter is a mock implementation of RateLimiterInterface
type MockRateLimiter struct {
	mock.Mock
}

func (m *MockRateLimiter) Allow(ctx context.Context, key, limitType string) (bool, error) {
	args := m.Called(ctx, key, limitType)
	return args.Bool(0), args.Error(1)
}

func (m *MockRateLimiter) AllowWithIP(ctx context.Context, ip, limitType string) (bool, error) {
	args := m.Called(ctx, ip, limitType)
	return args.Bool(0), args.Error(1)
}

func (m *MockRateLimiter) AllowWithEmail(ctx context.Context, email, limitType string) (bool, error) {
	args := m.Called(ctx, email, limitType)
	return args.Bool(0), args.Error(1)
}

func (m *MockRateLimiter) AllowWithUser(ctx context.Context, userID, limitType string) (bool, error) {
	args := m.Called(ctx, userID, limitType)
	return args.Bool(0), args.Error(1)
}

func TestHandler_listAPIKeys_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "user-123", Email: "user@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)

	expectedKeys := []map[string]interface{}{
		{"key_id": "key-1", "name": "Test Key 1"},
		{"key_id": "key-2", "name": "Test Key 2"},
	}
	mockAuth.On("ListUserAPIKeysAPI", ctx, "user-123").Return(expectedKeys, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
	}

	result, err := handler.listAPIKeys(ctx, req)
	require.NoError(t, err)

	keys, ok := result.([]map[string]interface{})
	require.True(t, ok)
	assert.Len(t, keys, 2)
}

func TestHandler_listAPIKeys_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	req := &events.LambdaFunctionURLRequest{}

	_, err := handler.listAPIKeys(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_listAPIKeys_NoToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
	}

	_, err := handler.listAPIKeys(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authorization token provided")
}

func TestHandler_listAPIKeys_InvalidSession(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("ValidateSession", ctx, "invalid-token").Return(nil, errors.New("invalid session"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer invalid-token",
		},
	}

	_, err := handler.listAPIKeys(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid session")
}

func TestHandler_listAPIKeys_ServiceError(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "user-123", Email: "user@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.On("ListUserAPIKeysAPI", ctx, "user-123").Return(nil, errors.New("database error"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
	}

	_, err := handler.listAPIKeys(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list API keys")
}

func TestHandler_createAPIKey_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockRateLimiter := new(MockRateLimiter)

	session := &Session{UserID: "user-123", Email: "user@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockRateLimiter.On("AllowWithUser", ctx, "user-123", "admin").Return(true, nil)

	expectedResult := map[string]string{"api_key": "new-key-value", "key_id": "key-123"}
	mockAuth.On("CreateAPIKeyAPI", ctx, "user-123", mock.Anything).Return(expectedResult, nil)

	handler := &Handler{auth: mockAuth, rateLimiter: mockRateLimiter}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		Body: `{"name": "My API Key"}`,
	}

	result, err := handler.createAPIKey(ctx, req)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestHandler_createAPIKey_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	req := &events.LambdaFunctionURLRequest{}

	_, err := handler.createAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_createAPIKey_NoToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{}

	_, err := handler.createAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authorization token provided")
}

func TestHandler_createAPIKey_InvalidSession(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("ValidateSession", ctx, "bad-token").Return(nil, errors.New("invalid session"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer bad-token",
		},
	}

	_, err := handler.createAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid session")
}

func TestHandler_createAPIKey_RateLimited(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockRateLimiter := new(MockRateLimiter)

	session := &Session{UserID: "user-123", Email: "user@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockRateLimiter.On("AllowWithUser", ctx, "user-123", "admin").Return(false, nil)

	handler := &Handler{auth: mockAuth, rateLimiter: mockRateLimiter}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		Body: `{"name": "My API Key"}`,
	}

	_, err := handler.createAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too many requests")
}

func TestHandler_createAPIKey_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "user-123", Email: "user@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		Body: `invalid json`,
	}

	_, err := handler.createAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_createAPIKey_ServiceError(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "user-123", Email: "user@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.On("CreateAPIKeyAPI", ctx, "user-123", mock.Anything).Return(nil, errors.New("creation failed"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		Body: `{"name": "My API Key"}`,
	}

	_, err := handler.createAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create API key")
}

func TestHandler_deleteAPIKey_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "user-123", Email: "user@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.On("DeleteAPIKeyAPI", ctx, "user-123", "11111111-1111-1111-1111-111111111111").Return(nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Path: "/api/api-keys/11111111-1111-1111-1111-111111111111",
			},
		},
	}

	result, err := handler.deleteAPIKey(ctx, req)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "deleted", resultMap["status"])
}

func TestHandler_deleteAPIKey_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	req := &events.LambdaFunctionURLRequest{}

	_, err := handler.deleteAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_deleteAPIKey_NoToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{}

	_, err := handler.deleteAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authorization token provided")
}

func TestHandler_deleteAPIKey_InvalidPath(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "user-123", Email: "user@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Path: "/api",
			},
		},
	}

	_, err := handler.deleteAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing key ID")
}

func TestHandler_deleteAPIKey_InvalidUUID(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "user-123", Email: "user@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Path: "/api/api-keys/invalid-uuid",
			},
		},
	}

	_, err := handler.deleteAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a valid UUID")
}

func TestHandler_deleteAPIKey_ServiceError(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "user-123", Email: "user@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.On("DeleteAPIKeyAPI", ctx, "user-123", "11111111-1111-1111-1111-111111111111").Return(errors.New("delete failed"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Path: "/api/api-keys/11111111-1111-1111-1111-111111111111",
			},
		},
	}

	_, err := handler.deleteAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete API key")
}

func TestHandler_revokeAPIKey_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "user-123", Email: "user@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.On("RevokeAPIKeyAPI", ctx, "user-123", "11111111-1111-1111-1111-111111111111").Return(nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Path: "/api/api-keys/11111111-1111-1111-1111-111111111111/revoke",
			},
		},
	}

	result, err := handler.revokeAPIKey(ctx, req)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "revoked", resultMap["status"])
}

func TestHandler_revokeAPIKey_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	req := &events.LambdaFunctionURLRequest{}

	_, err := handler.revokeAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_revokeAPIKey_NoToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{}

	_, err := handler.revokeAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authorization token provided")
}

func TestHandler_revokeAPIKey_InvalidPath(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "user-123", Email: "user@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Path: "/api",
			},
		},
	}

	_, err := handler.revokeAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing key ID")
}

func TestHandler_revokeAPIKey_InvalidUUID(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "user-123", Email: "user@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Path: "/api/api-keys/invalid-uuid/revoke",
			},
		},
	}

	_, err := handler.revokeAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a valid UUID")
}

func TestHandler_revokeAPIKey_ServiceError(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "user-123", Email: "user@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.On("RevokeAPIKeyAPI", ctx, "user-123", "11111111-1111-1111-1111-111111111111").Return(errors.New("revoke failed"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Path: "/api/api-keys/11111111-1111-1111-1111-111111111111/revoke",
			},
		},
	}

	_, err := handler.revokeAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to revoke API key")
}

// Permission-denial tests: a non-admin user without the required api-keys
// permission must be rejected with 403 BEFORE any owner-scoped operation runs.

func TestHandler_listAPIKeys_PermissionDenied(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "viewer-1", Email: "viewer@example.com", Role: "user"}
	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(session, nil)
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "view", "api-keys").Return(false, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer viewer-token"},
	}

	_, err := handler.listAPIKeys(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
	mockAuth.AssertNotCalled(t, "ListUserAPIKeysAPI", mock.Anything, mock.Anything)
}

func TestHandler_createAPIKey_PermissionDenied(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "viewer-1", Email: "viewer@example.com", Role: "user"}
	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(session, nil)
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "create", "api-keys").Return(false, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer viewer-token"},
		Body:    `{"name": "should not happen"}`,
	}

	_, err := handler.createAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
	mockAuth.AssertNotCalled(t, "CreateAPIKeyAPI", mock.Anything, mock.Anything, mock.Anything)
}

func TestHandler_deleteAPIKey_PermissionDenied(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "viewer-1", Email: "viewer@example.com", Role: "user"}
	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(session, nil)
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "delete", "api-keys").Return(false, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer viewer-token"},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Path: "/api/api-keys/11111111-1111-1111-1111-111111111111",
			},
		},
	}

	_, err := handler.deleteAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
	mockAuth.AssertNotCalled(t, "DeleteAPIKeyAPI", mock.Anything, mock.Anything, mock.Anything)
}

func TestHandler_revokeAPIKey_PermissionDenied(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "viewer-1", Email: "viewer@example.com", Role: "user"}
	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(session, nil)
	// Revoke reuses the "delete" verb in the permission model.
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "delete", "api-keys").Return(false, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer viewer-token"},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Path: "/api/api-keys/11111111-1111-1111-1111-111111111111/revoke",
			},
		},
	}

	_, err := handler.revokeAPIKey(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
	mockAuth.AssertNotCalled(t, "RevokeAPIKeyAPI", mock.Anything, mock.Anything, mock.Anything)
}

func TestFormatTimePtr(t *testing.T) {
	t.Run("nil pointer returns empty string", func(t *testing.T) {
		result := formatTimePtr(nil)
		assert.Empty(t, result)
	})

	t.Run("valid time formats correctly", func(t *testing.T) {
		testTime := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
		result := formatTimePtr(&testTime)
		assert.Equal(t, "2024-06-15T10:30:00Z", result)
	})

	t.Run("time with timezone formats correctly", func(t *testing.T) {
		loc, _ := time.LoadLocation("America/New_York")
		testTime := time.Date(2024, 6, 15, 10, 30, 0, 0, loc)
		result := formatTimePtr(&testTime)
		assert.Contains(t, result, "2024-06-15T10:30:00")
	})
}
