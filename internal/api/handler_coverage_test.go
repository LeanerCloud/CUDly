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

// Tests for buildResponse error handling

func TestHandler_buildResponse_WithError(t *testing.T) {
	handler := &Handler{}
	headers := map[string]string{"Content-Type": "application/json"}

	resp, err := handler.buildResponse(200, headers, nil, errors.New("test error"))
	require.NoError(t, err)

	assert.Equal(t, 500, resp.StatusCode)
	assert.Contains(t, resp.Body, "internal server error")
}

func TestHandler_buildResponse_MarshalError(t *testing.T) {
	handler := &Handler{}
	headers := map[string]string{"Content-Type": "application/json"}

	// Create an unmarshalable value (channel)
	type badType struct {
		Ch chan int `json:"ch"`
	}
	badValue := badType{Ch: make(chan int)}

	resp, err := handler.buildResponse(200, headers, badValue, nil)
	require.NoError(t, err)

	assert.Equal(t, 500, resp.StatusCode)
	assert.Contains(t, resp.Body, "internal server error")
}

func TestHandler_buildResponse_NilBody(t *testing.T) {
	handler := &Handler{}
	headers := map[string]string{"Content-Type": "application/json"}

	resp, err := handler.buildResponse(200, headers, nil, nil)
	require.NoError(t, err)

	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "", resp.Body)
}

// Tests for validateSecurity

func TestHandler_validateSecurity_PublicEndpoint(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	req := &events.LambdaFunctionURLRequest{}
	headers := map[string]string{}

	// Public endpoint should return nil
	resp := handler.validateSecurity(ctx, req, "GET", "/api/health", headers)
	assert.Nil(t, resp)
}

func TestHandler_validateSecurity_Unauthorized(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{apiKey: "secret-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
	}
	headers := map[string]string{}

	resp := handler.validateSecurity(ctx, req, "GET", "/api/config", headers)
	assert.NotNil(t, resp)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestHandler_validateSecurity_CSRFFailure(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	// Setup authentication to pass (via bearer token), then CSRF to fail
	mockAuth.On("ValidateSession", mock.Anything, "test-token").Return(&Session{UserID: "user-1"}, nil)
	mockAuth.On("ValidateCSRFToken", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("csrf failed"))

	// Use bearer token auth (not API key) to trigger CSRF validation path
	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
	}
	headers := map[string]string{}

	// PUT on /api/config requires CSRF
	resp := handler.validateSecurity(ctx, req, "PUT", "/api/config", headers)
	require.NotNil(t, resp)
	assert.Equal(t, 403, resp.StatusCode)
}

// Tests for request validation

func TestHandler_validateRequest_BodyTooLarge(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	// Create a large body (over 1MB)
	largeBody := make([]byte, 1024*1024+1)
	req := &events.LambdaFunctionURLRequest{
		Body: string(largeBody),
	}
	headers := map[string]string{}

	resp := handler.validateRequest(ctx, req, "POST", "/api/config", headers)
	assert.NotNil(t, resp)
	assert.Equal(t, 413, resp.StatusCode)
}

func TestHandler_validateRequest_InvalidContentType(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	// Content-Type validation happens before security checks
	// For a POST request with body and wrong content-type, it should return 415
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Content-Type": "text/plain",
		},
		Body: "some body content",
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "POST",
			},
		},
	}
	headers := map[string]string{}

	resp := handler.validateRequest(ctx, req, "POST", "/api/health", headers)
	require.NotNil(t, resp)
	assert.Equal(t, 415, resp.StatusCode)
}

// Tests for setSecurityHeaders - additional coverage

func TestSetSecurityHeaders_AllHeaders(t *testing.T) {
	headers := make(map[string]string)
	result := setSecurityHeaders(headers)

	assert.Contains(t, result["Content-Security-Policy"], "default-src 'none'")
	assert.Contains(t, result["Strict-Transport-Security"], "max-age=")
	assert.Equal(t, "nosniff", result["X-Content-Type-Options"])
	assert.Equal(t, "DENY", result["X-Frame-Options"])
	// X-XSS-Protection has been removed: deprecated and no-op in modern browsers
	assert.Empty(t, result["X-XSS-Protection"])
	assert.Contains(t, result["Referrer-Policy"], "strict-origin")
	assert.Contains(t, result["Permissions-Policy"], "geolocation=()")
	assert.Contains(t, result["Cache-Control"], "no-store")
}

// Tests for router handlers that are wrappers

func TestRouter_Handlers_Coverage(t *testing.T) {
	ctx := context.Background()

	t.Run("loginHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("Login", ctx, mock.Anything).Return(&LoginResponse{Token: "tok"}, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Body: `{"email": "test@example.com", "password": "pass"}`,
		}

		result, err := router.loginHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("logoutHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("Logout", ctx, "test-token").Return(nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer test-token"},
		}

		result, err := router.logoutHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("getCurrentUserHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "test-token").Return(&Session{UserID: "user-1"}, nil)
		mockAuth.On("GetUser", ctx, "user-1").Return(&User{ID: "user-1", Email: "test@example.com"}, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer test-token"},
		}

		result, err := router.getCurrentUserHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("checkAdminExistsHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("CheckAdminExists", ctx).Return(true, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		result, err := router.checkAdminExistsHandler(ctx, nil, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("setupAdminHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("SetupAdmin", ctx, mock.Anything).Return(&LoginResponse{Token: "admin-tok"}, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Body: `{"email": "admin@example.com", "password": "pass123"}`,
		}

		result, err := router.setupAdminHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("forgotPasswordHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("RequestPasswordReset", ctx, "test@example.com").Return(nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Body: `{"email": "test@example.com"}`,
		}

		result, err := router.forgotPasswordHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("resetPasswordHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ConfirmPasswordReset", ctx, mock.Anything).Return(nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Body: `{"token": "reset-token", "password": "newpass"}`,
		}

		result, err := router.resetPasswordHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("updateProfileHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "test-token").Return(&Session{UserID: "user-1"}, nil)
		mockAuth.On("UpdateUserProfile", ctx, "user-1", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer test-token"},
			Body:    `{"email": "newemail@example.com"}`,
		}

		result, err := router.updateProfileHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("changePasswordHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "test-token").Return(&Session{UserID: "user-1"}, nil)
		// Passwords need to be base64 encoded
		// "oldpass" -> "b2xkcGFzcw==", "newpass" -> "bmV3cGFzcw=="
		mockAuth.On("ChangePasswordAPI", ctx, "user-1", "oldpass", "newpass").Return(nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer test-token"},
			Body:    `{"current_password": "b2xkcGFzcw==", "new_password": "bmV3cGFzcw=="}`,
		}

		result, err := router.changePasswordHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("listAPIKeysHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "test-token").Return(&Session{UserID: "user-1"}, nil)
		mockAuth.On("ListUserAPIKeysAPI", ctx, "user-1").Return([]interface{}{}, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer test-token"},
		}

		result, err := router.listAPIKeysHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("createAPIKeyHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "test-token").Return(&Session{UserID: "user-1"}, nil)
		mockAuth.On("CreateAPIKeyAPI", ctx, "user-1", mock.Anything).Return(map[string]string{"key_id": "key-1"}, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer test-token"},
			Body:    `{"name": "My Key"}`,
		}

		result, err := router.createAPIKeyHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("deleteAPIKeyHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "test-token").Return(&Session{UserID: "user-1"}, nil)
		mockAuth.On("DeleteAPIKeyAPI", ctx, "user-1", "11111111-1111-1111-1111-111111111111").Return(nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer test-token"},
			RequestContext: events.LambdaFunctionURLRequestContext{
				HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
					Path: "/api/api-keys/11111111-1111-1111-1111-111111111111",
				},
			},
		}

		result, err := router.deleteAPIKeyHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("revokeAPIKeyHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "test-token").Return(&Session{UserID: "user-1"}, nil)
		mockAuth.On("RevokeAPIKeyAPI", ctx, "user-1", "11111111-1111-1111-1111-111111111111").Return(nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer test-token"},
			RequestContext: events.LambdaFunctionURLRequestContext{
				HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
					Path: "/api/api-keys/11111111-1111-1111-1111-111111111111/revoke",
				},
			},
		}

		result, err := router.revokeAPIKeyHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("listUsersHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{UserID: "admin", Role: "admin"}, nil)
		mockAuth.On("ListUsersAPI", ctx).Return([]interface{}{}, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer admin-token"},
		}

		result, err := router.listUsersHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("createUserHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{UserID: "admin", Role: "admin"}, nil)
		mockAuth.On("CreateUserAPI", ctx, mock.Anything).Return(map[string]string{"id": "new-user"}, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		// Password needs to be base64 encoded: "pass123" -> "cGFzczEyMw=="
		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer admin-token"},
			Body:    `{"email": "newuser@example.com", "password": "cGFzczEyMw==", "role": "user"}`,
		}

		result, err := router.createUserHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("getUserHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{UserID: "admin", Role: "admin"}, nil)
		mockAuth.On("GetUser", ctx, "11111111-1111-1111-1111-111111111111").Return(&User{ID: "11111111-1111-1111-1111-111111111111"}, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer admin-token"},
		}
		params := map[string]string{"id": "11111111-1111-1111-1111-111111111111"}

		result, err := router.getUserHandler(ctx, req, params)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("updateUserHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{UserID: "admin", Role: "admin"}, nil)
		mockAuth.On("UpdateUserAPI", ctx, "11111111-1111-1111-1111-111111111111", mock.Anything).Return(map[string]string{}, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer admin-token"},
			Body:    `{"email": "updated@example.com"}`,
		}
		params := map[string]string{"id": "11111111-1111-1111-1111-111111111111"}

		result, err := router.updateUserHandler(ctx, req, params)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("listGroupsHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{UserID: "admin", Role: "admin"}, nil)
		mockAuth.On("ListGroupsAPI", ctx).Return([]interface{}{}, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer admin-token"},
		}

		result, err := router.listGroupsHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("createGroupHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{UserID: "admin", Role: "admin"}, nil)
		mockAuth.On("CreateGroupAPI", ctx, mock.Anything).Return(map[string]string{"id": "new-group"}, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer admin-token"},
			Body:    `{"name": "New Group"}`,
		}

		result, err := router.createGroupHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("getGroupHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{UserID: "admin", Role: "admin"}, nil)
		mockAuth.On("GetGroupAPI", ctx, "11111111-1111-1111-1111-111111111111").Return(map[string]interface{}{"id": "11111111-1111-1111-1111-111111111111"}, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer admin-token"},
		}
		params := map[string]string{"id": "11111111-1111-1111-1111-111111111111"}

		result, err := router.getGroupHandler(ctx, req, params)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("updateGroupHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{UserID: "admin", Role: "admin"}, nil)
		mockAuth.On("UpdateGroupAPI", ctx, "11111111-1111-1111-1111-111111111111", mock.Anything).Return(map[string]string{}, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer admin-token"},
			Body:    `{"name": "Updated Group"}`,
		}
		params := map[string]string{"id": "11111111-1111-1111-1111-111111111111"}

		result, err := router.updateGroupHandler(ctx, req, params)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("deleteGroupHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{UserID: "admin", Role: "admin"}, nil)
		mockAuth.On("DeleteGroup", ctx, "11111111-1111-1111-1111-111111111111").Return(nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer admin-token"},
		}
		params := map[string]string{"id": "11111111-1111-1111-1111-111111111111"}

		result, err := router.deleteGroupHandler(ctx, req, params)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("getPublicInfoHandler", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("CheckAdminExists", ctx).Return(true, nil)

		h := &Handler{auth: mockAuth}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{
			RequestContext: events.LambdaFunctionURLRequestContext{
				HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
					SourceIP: "192.168.1.1",
				},
			},
		}

		result, err := router.getPublicInfoHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("getHistoryAnalyticsHandler", func(t *testing.T) {
		mockClient := new(MockAnalyticsClient)
		mockClient.On("QueryHistory", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]HistoryDataPoint{}, (*HistorySummary)(nil), nil)

		h := &Handler{analyticsClient: mockClient}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{}
		result, err := router.getHistoryAnalyticsHandler(ctx, req, map[string]string{})
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("getHistoryBreakdownHandler", func(t *testing.T) {
		mockClient := new(MockAnalyticsClient)
		mockClient.On("QueryBreakdown", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(map[string]BreakdownValue{}, nil)

		h := &Handler{analyticsClient: mockClient}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{}
		result, err := router.getHistoryBreakdownHandler(ctx, req, map[string]string{})
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("triggerAnalyticsCollectionHandler", func(t *testing.T) {
		mockCollector := new(MockAnalyticsCollector)
		mockCollector.On("Collect", ctx).Return(nil)

		h := &Handler{analyticsCollector: mockCollector}
		router := NewRouter(h)

		req := &events.LambdaFunctionURLRequest{}
		result, err := router.triggerAnalyticsCollectionHandler(ctx, req, nil)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})
}

// Test handler_plans functions
func TestHandler_listPlans_Error(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockStore.On("ListPurchasePlans", mock.Anything).Return(nil, errors.New("db error"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer test-token"},
	}

	_, err := handler.listPlans(ctx, req)
	assert.Error(t, err)
}

func TestHandler_getPlan_NotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockStore.On("GetPurchasePlan", mock.Anything, "11111111-1111-1111-1111-111111111111").Return(nil, errors.New("not found"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer test-token"},
	}

	_, err := handler.getPlan(ctx, req, "11111111-1111-1111-1111-111111111111")
	assert.Error(t, err)
}

// Tests for handler_users

func TestHandler_deleteUser_CannotDeleteSelf(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	// Use a valid UUID format
	adminID := "11111111-1111-1111-1111-111111111111"
	adminSession := &Session{UserID: adminID, Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}

	_, err := handler.deleteUser(ctx, req, adminID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot delete your own account")
}

func TestHandler_deleteUser_Error(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.On("DeleteUser", ctx, "other-user-id").Return(errors.New("delete failed"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}

	_, err := handler.deleteUser(ctx, req, "other-user-id")
	assert.Error(t, err)
}

// Tests for handler_groups

func TestHandler_deleteGroup_DeleteFailed(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.On("DeleteGroup", ctx, "group-1").Return(errors.New("delete failed"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}

	_, err := handler.deleteGroup(ctx, req, "group-1")
	assert.Error(t, err)
}

// Tests for forgotPassword

func TestHandler_forgotPassword_SuccessOnError(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	// Even when RequestPasswordReset returns an error, forgotPassword should return success
	// (to prevent email enumeration)
	mockAuth.On("RequestPasswordReset", ctx, "test@example.com").Return(errors.New("user not found"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{Body: `{"email": "test@example.com"}`}
	result, err := handler.forgotPassword(ctx, req)
	require.NoError(t, err)

	response, ok := result.(map[string]string)
	require.True(t, ok)
	assert.Contains(t, response["status"], "if the email exists")
}

// Test for validation decodeBase64Password

func TestDecodeBase64Password_InvalidBase64(t *testing.T) {
	// Test with invalid base64
	_, err := decodeBase64Password("not-valid-base64!!!")
	assert.Error(t, err)
}

func TestDecodeBase64Password_ValidBase64(t *testing.T) {
	// "test123" encoded in base64
	result, err := decodeBase64Password("dGVzdDEyMw==")
	assert.NoError(t, err)
	assert.Equal(t, "test123", result)
}

// Test for types_apikeys toAPIPermissions

func TestToAPIPermissions_EmptySlice(t *testing.T) {
	perms := []interface{}{}

	result := toAPIPermissions(perms)

	assert.Len(t, result, 0)
}

func TestToAPIPermissions_WithPermissions(t *testing.T) {
	perms := []interface{}{
		Permission{Action: "read", Resource: "config"},
		Permission{Action: "write", Resource: "plans"},
	}

	result := toAPIPermissions(perms)

	assert.Len(t, result, 2)
	assert.Equal(t, "config", result[0].Resource)
	assert.Equal(t, "read", result[0].Action)
}

func TestToAPIPermissions_NonPermissionItems(t *testing.T) {
	perms := []interface{}{
		"not a permission",
		123,
		Permission{Action: "read", Resource: "config"},
	}

	result := toAPIPermissions(perms)

	// Only the valid Permission should be included
	assert.Len(t, result, 1)
	assert.Equal(t, "config", result[0].Resource)
}

// Test NewHandler with API key loaded
func TestNewHandler_WithDependencies(t *testing.T) {
	mockStore := new(MockConfigStore)
	mockScheduler := new(MockScheduler)
	mockPurchase := new(MockPurchaseManager)
	mockAuth := new(MockAuthService)

	cfg := HandlerConfig{
		ConfigStore:       mockStore,
		Scheduler:         mockScheduler,
		PurchaseManager:   mockPurchase,
		AuthService:       mockAuth,
		CORSAllowedOrigin: "https://example.com",
	}

	handler := NewHandler(cfg)

	assert.NotNil(t, handler)
	assert.Equal(t, "https://example.com", handler.corsAllowedOrigin)
}

// Test handler_groups additional coverage

func TestHandler_createGroup_NoToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
	}

	_, err := handler.createGroup(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authorization token")
}

func TestHandler_createGroup_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{invalid json}`,
	}

	_, err := handler.createGroup(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid request")
}

func TestHandler_getGroup_NoToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
	}

	_, err := handler.getGroup(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authorization token")
}

// Test handler_plans additional coverage

func TestHandler_deletePlan_Success(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("DeletePurchasePlan", mock.Anything, "11111111-1111-1111-1111-111111111111").Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}

	result, err := handler.deletePlan(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	resultMap, ok := result.(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "deleted", resultMap["status"])
}

func TestHandler_deletePlan_NoToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
	}

	_, err := handler.deletePlan(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authorization token")
}

func TestHandler_deletePlan_Error(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("DeletePurchasePlan", mock.Anything, "11111111-1111-1111-1111-111111111111").Return(errors.New("not found"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}

	_, err := handler.deletePlan(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.Error(t, err)
}
