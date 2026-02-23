package api

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandler_login_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	loginResp := &LoginResponse{
		Token:     "test-token",
		ExpiresAt: "2024-12-31T23:59:59Z",
		User: &UserInfo{
			ID:    "12345678-1234-1234-1234-123456789abc",
			Email: "test@example.com",
			Role:  "admin",
		},
	}

	mockAuth.On("Login", ctx, LoginRequest{
		Email:    "test@example.com",
		Password: "password123",
	}).Return(loginResp, nil)

	handler := &Handler{auth: mockAuth}

	// Password must be base64-encoded in the request body
	encodedPassword := base64.StdEncoding.EncodeToString([]byte("password123"))
	req := &events.LambdaFunctionURLRequest{
		Body: `{"email": "test@example.com", "password": "` + encodedPassword + `"}`,
	}

	result, err := handler.login(ctx, req)
	require.NoError(t, err)

	resp := result.(*LoginResponse)
	assert.Equal(t, "test-token", resp.Token)
	assert.Equal(t, "12345678-1234-1234-1234-123456789abc", resp.User.ID)
}

func TestHandler_login_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{auth: nil}

	req := &events.LambdaFunctionURLRequest{
		Body: `{"email": "test@example.com", "password": "password123"}`,
	}

	result, err := handler.login(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_login_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Body: `{invalid json}`,
	}

	result, err := handler.login(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_login_AuthError(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("Login", ctx, mock.Anything).Return(nil, errors.New("invalid credentials"))

	handler := &Handler{auth: mockAuth}

	// Password must be base64-encoded in the request body
	encodedPassword := base64.StdEncoding.EncodeToString([]byte("wrong"))
	req := &events.LambdaFunctionURLRequest{
		Body: `{"email": "test@example.com", "password": "` + encodedPassword + `"}`,
	}

	result, err := handler.login(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid credentials")

	// Verify it's a 401 client error
	ce, ok := IsClientError(err)
	assert.True(t, ok)
	assert.Equal(t, 401, ce.code)
}

func TestHandler_logout_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("Logout", ctx, "test-token").Return(nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
	}

	result, err := handler.logout(ctx, req)
	require.NoError(t, err)

	resp := result.(map[string]string)
	assert.Equal(t, "logged out", resp["status"])
}

func TestHandler_logout_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{auth: nil}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
	}

	result, err := handler.logout(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_logout_NoToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
	}

	result, err := handler.logout(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no authorization token provided")
}

func TestHandler_logout_Error(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("Logout", ctx, "test-token").Return(errors.New("session not found"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
	}

	result, err := handler.logout(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "invalid session", err.Error())

	ce, ok := IsClientError(err)
	assert.True(t, ok)
	assert.Equal(t, 401, ce.code)
}

func TestHandler_getCurrentUser_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{
		UserID: "12345678-1234-1234-1234-123456789abc",
		Email:  "test@example.com",
		Role:   "admin",
	}
	user := &User{
		ID:         "12345678-1234-1234-1234-123456789abc",
		Email:      "test@example.com",
		Role:       "admin",
		MFAEnabled: true,
	}

	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.On("GetUser", ctx, "12345678-1234-1234-1234-123456789abc").Return(user, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
	}

	result, err := handler.getCurrentUser(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, "12345678-1234-1234-1234-123456789abc", result.ID)
	assert.Equal(t, "test@example.com", result.Email)
	assert.Equal(t, "admin", result.Role)
	assert.True(t, result.MFAEnabled)
}

func TestHandler_getCurrentUser_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{auth: nil}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
	}

	result, err := handler.getCurrentUser(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_getCurrentUser_NoToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
	}

	result, err := handler.getCurrentUser(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no authorization token provided")
}

func TestHandler_getCurrentUser_InvalidSession(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("ValidateSession", ctx, "invalid-token").Return(nil, errors.New("invalid session"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer invalid-token",
		},
	}

	result, err := handler.getCurrentUser(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid session")
}

func TestHandler_getCurrentUser_UserNotFound(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{
		UserID: "12345678-1234-1234-1234-123456789abc",
		Email:  "test@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.On("GetUser", ctx, "12345678-1234-1234-1234-123456789abc").Return(nil, errors.New("user not found"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
	}

	result, err := handler.getCurrentUser(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "user not found")
}

func TestHandler_checkAdminExists_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("CheckAdminExists", ctx).Return(true, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				SourceIP: "127.0.0.1",
			},
		},
	}

	result, err := handler.checkAdminExists(ctx, req)
	require.NoError(t, err)

	assert.True(t, result.AdminExists)
}

func TestHandler_checkAdminExists_NoAdmin(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("CheckAdminExists", ctx).Return(false, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				SourceIP: "127.0.0.1",
			},
		},
	}

	result, err := handler.checkAdminExists(ctx, req)
	require.NoError(t, err)

	assert.False(t, result.AdminExists)
}

func TestHandler_checkAdminExists_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{auth: nil}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				SourceIP: "127.0.0.1",
			},
		},
	}

	result, err := handler.checkAdminExists(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_checkAdminExists_Error(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("CheckAdminExists", ctx).Return(false, errors.New("database error"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				SourceIP: "127.0.0.1",
			},
		},
	}

	result, err := handler.checkAdminExists(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "database error")
}

func TestHandler_setupAdmin_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	loginResp := &LoginResponse{
		Token:     "admin-token",
		ExpiresAt: "2024-12-31T23:59:59Z",
		User: &UserInfo{
			ID:    "admin-123",
			Email: "admin@example.com",
			Role:  "admin",
		},
	}

	mockAuth.On("SetupAdmin", ctx, SetupAdminRequest{
		Email:    "admin@example.com",
		Password: "admin123",
	}).Return(loginResp, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				SourceIP: "127.0.0.1",
			},
		},
		Body: `{"email": "admin@example.com", "password": "admin123"}`,
	}

	result, err := handler.setupAdmin(ctx, req)
	require.NoError(t, err)

	resp := result.(*LoginResponse)
	assert.Equal(t, "admin-token", resp.Token)
}

func TestHandler_setupAdmin_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{auth: nil}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				SourceIP: "127.0.0.1",
			},
		},
		Body: `{"email": "admin@example.com", "password": "admin123"}`,
	}

	result, err := handler.setupAdmin(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_setupAdmin_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				SourceIP: "127.0.0.1",
			},
		},
		Body: `{invalid json}`,
	}

	result, err := handler.setupAdmin(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_setupAdmin_Error(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("SetupAdmin", ctx, mock.Anything).Return(nil, errors.New("admin already exists"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				SourceIP: "127.0.0.1",
			},
		},
		Body: `{"email": "admin@example.com", "password": "admin123"}`,
	}

	result, err := handler.setupAdmin(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "admin already exists")
}

func TestHandler_forgotPassword_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("RequestPasswordReset", ctx, "user@example.com").Return(nil)

	handler := &Handler{auth: mockAuth}

	result, err := handler.forgotPassword(ctx, `{"email": "user@example.com"}`)
	require.NoError(t, err)

	resp := result.(map[string]string)
	assert.Equal(t, "if the email exists, a reset link has been sent", resp["status"])
}

func TestHandler_forgotPassword_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{auth: nil}

	result, err := handler.forgotPassword(ctx, `{"email": "user@example.com"}`)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_forgotPassword_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	result, err := handler.forgotPassword(ctx, `{invalid json}`)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_forgotPassword_ErrorStillReturnsSuccess(t *testing.T) {
	// Even if the email doesn't exist, we return success to prevent email enumeration
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("RequestPasswordReset", ctx, "nonexistent@example.com").Return(errors.New("user not found"))

	handler := &Handler{auth: mockAuth}

	result, err := handler.forgotPassword(ctx, `{"email": "nonexistent@example.com"}`)
	require.NoError(t, err) // Should still succeed to prevent enumeration

	resp := result.(map[string]string)
	assert.Equal(t, "if the email exists, a reset link has been sent", resp["status"])
}

func TestHandler_resetPassword_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("ConfirmPasswordReset", ctx, PasswordResetConfirm{
		Token:       "reset-token",
		NewPassword: "newpassword123",
	}).Return(nil)

	handler := &Handler{auth: mockAuth}

	result, err := handler.resetPassword(ctx, `{"token": "reset-token", "new_password": "newpassword123"}`)
	require.NoError(t, err)

	resp := result.(map[string]string)
	assert.Equal(t, "password reset successful", resp["status"])
}

func TestHandler_resetPassword_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{auth: nil}

	result, err := handler.resetPassword(ctx, `{"token": "reset-token", "new_password": "newpassword123"}`)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_resetPassword_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	result, err := handler.resetPassword(ctx, `{invalid json}`)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_resetPassword_Error(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("ConfirmPasswordReset", ctx, mock.Anything).Return(errors.New("invalid or expired token"))

	handler := &Handler{auth: mockAuth}

	result, err := handler.resetPassword(ctx, `{"token": "bad-token", "new_password": "newpassword123"}`)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid or expired token")
}
func TestHandler_updateProfile_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{
		UserID: "12345678-1234-1234-1234-123456789abc",
		Email:  "old@example.com",
		Role:   "user",
	}

	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.On("UpdateUserProfile", ctx, "12345678-1234-1234-1234-123456789abc", "new@example.com", "currentpass", "newpass").Return(nil)

	handler := &Handler{auth: mockAuth}

	// Passwords must be base64-encoded
	currentPass := base64.StdEncoding.EncodeToString([]byte("currentpass"))
	newPass := base64.StdEncoding.EncodeToString([]byte("newpass"))

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		Body: `{"email": "new@example.com", "current_password": "` + currentPass + `", "new_password": "` + newPass + `"}`,
	}

	result, err := handler.updateProfile(ctx, req)
	require.NoError(t, err)

	resp := result.(map[string]string)
	assert.Equal(t, "profile updated", resp["status"])
}

func TestHandler_updateProfile_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{auth: nil}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		Body: `{"email": "new@example.com"}`,
	}

	result, err := handler.updateProfile(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_updateProfile_NoToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
		Body:    `{"email": "new@example.com"}`,
	}

	result, err := handler.updateProfile(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no authorization token provided")
}

func TestHandler_updateProfile_InvalidSession(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("ValidateSession", ctx, "invalid-token").Return(nil, errors.New("invalid session"))

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer invalid-token",
		},
		Body: `{"email": "new@example.com"}`,
	}

	result, err := handler.updateProfile(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid session")
}

// changePassword endpoint test

func TestHandler_changePassword_Success(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{
		UserID: "11111111-1111-1111-1111-111111111111",
		Email:  "user@example.com",
		Role:   "user",
	}

	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.On("ChangePasswordAPI", ctx, "11111111-1111-1111-1111-111111111111", "oldpass", "newpass").Return(nil)

	handler := &Handler{auth: mockAuth}

	// Passwords must be base64-encoded
	currentPass := base64.StdEncoding.EncodeToString([]byte("oldpass"))
	newPass := base64.StdEncoding.EncodeToString([]byte("newpass"))

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		Body: `{"current_password": "` + currentPass + `", "new_password": "` + newPass + `"}`,
	}

	result, err := handler.changePassword(ctx, req)
	require.NoError(t, err)

	resp := result.(map[string]string)
	assert.Equal(t, "password changed", resp["status"])
}

func TestHandler_changePassword_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{auth: nil}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		Body: `{"current_password": "old", "new_password": "new"}`,
	}

	result, err := handler.changePassword(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_changePassword_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{
		UserID: "11111111-1111-1111-1111-111111111111",
		Email:  "user@example.com",
		Role:   "user",
	}

	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
		},
		Body: `{invalid json}`,
	}

	result, err := handler.changePassword(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_updateProfile_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "12345678-1234-1234-1234-123456789abc"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer test-token"},
		Body:    `{invalid json}`,
	}

	result, err := handler.updateProfile(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}
