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

	req := &events.LambdaFunctionURLRequest{Body: `{"email": "user@example.com"}`}
	result, err := handler.forgotPassword(ctx, req)
	require.NoError(t, err)

	resp := result.(map[string]string)
	assert.Equal(t, "if the email exists, a reset link has been sent", resp["status"])
}

func TestHandler_forgotPassword_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{auth: nil}

	req := &events.LambdaFunctionURLRequest{Body: `{"email": "user@example.com"}`}
	result, err := handler.forgotPassword(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_forgotPassword_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{Body: `{invalid json}`}
	result, err := handler.forgotPassword(ctx, req)
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

	req := &events.LambdaFunctionURLRequest{Body: `{"email": "nonexistent@example.com"}`}
	result, err := handler.forgotPassword(ctx, req)
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

	// The handler base64-decodes new_password before forwarding to the
	// service (issue #356), matching the frontend's submission shape. The
	// service still receives the plaintext.
	encoded := base64.StdEncoding.EncodeToString([]byte("newpassword123"))
	req := &events.LambdaFunctionURLRequest{Body: `{"token": "reset-token", "new_password": "` + encoded + `"}`}
	result, err := handler.resetPassword(ctx, req)
	require.NoError(t, err)

	resp := result.(map[string]string)
	assert.Equal(t, "password reset successful", resp["status"])
}

func TestHandler_resetPassword_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{auth: nil}

	req := &events.LambdaFunctionURLRequest{Body: `{"token": "reset-token", "new_password": "newpassword123"}`}
	result, err := handler.resetPassword(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_resetPassword_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{Body: `{invalid json}`}
	result, err := handler.resetPassword(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

// Issues #460 + #461: token-status endpoint surfaces expired / used
// state before the user types into a form that can never submit.
func TestHandler_resetPasswordStatus_Valid(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("ResetTokenStatus", ctx, "good-token").Return("valid", "reset", nil).Once()

	handler := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{QueryStringParameters: map[string]string{"token": "good-token"}}
	result, err := handler.resetPasswordStatus(ctx, req)
	require.NoError(t, err)

	resp := result.(map[string]string)
	assert.Equal(t, "valid", resp["state"])
	assert.Equal(t, "reset", resp["flow"])
	mockAuth.AssertExpectations(t)
}

func TestHandler_resetPasswordStatus_Invite(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("ResetTokenStatus", ctx, "invite-token").Return("valid", "invite", nil).Once()

	handler := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{QueryStringParameters: map[string]string{"token": "invite-token"}}
	result, err := handler.resetPasswordStatus(ctx, req)
	require.NoError(t, err)

	resp := result.(map[string]string)
	assert.Equal(t, "valid", resp["state"])
	assert.Equal(t, "invite", resp["flow"])
	mockAuth.AssertExpectations(t)
}

func TestHandler_resetPasswordStatus_Expired(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("ResetTokenStatus", ctx, "old-token").Return("expired", "reset", nil).Once()

	handler := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{QueryStringParameters: map[string]string{"token": "old-token"}}
	result, err := handler.resetPasswordStatus(ctx, req)
	require.NoError(t, err)

	resp := result.(map[string]string)
	assert.Equal(t, "expired", resp["state"])
	mockAuth.AssertExpectations(t)
}

func TestHandler_resetPasswordStatus_Used(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("ResetTokenStatus", ctx, "stale-token").Return("used", "reset", nil).Once()

	handler := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{QueryStringParameters: map[string]string{"token": "stale-token"}}
	result, err := handler.resetPasswordStatus(ctx, req)
	require.NoError(t, err)

	resp := result.(map[string]string)
	assert.Equal(t, "used", resp["state"])
	mockAuth.AssertExpectations(t)
}

func TestHandler_resetPasswordStatus_MissingToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{QueryStringParameters: map[string]string{}}
	_, err := handler.resetPasswordStatus(ctx, req)
	require.Error(t, err)

	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.Error(), "token is required")
}

func TestHandler_resetPasswordStatus_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{auth: nil}

	req := &events.LambdaFunctionURLRequest{QueryStringParameters: map[string]string{"token": "anything"}}
	_, err := handler.resetPasswordStatus(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_resetPassword_Error(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("ConfirmPasswordReset", ctx, mock.Anything).Return(errors.New("invalid or expired token"))

	handler := &Handler{auth: mockAuth}

	// new_password must be base64-encoded — the handler now decodes before
	// forwarding (issue #356). Encoding "newpassword123" so the decode step
	// passes and the error path under test (bad token) is the one that fires.
	encoded := base64.StdEncoding.EncodeToString([]byte("newpassword123"))
	req := &events.LambdaFunctionURLRequest{Body: `{"token": "bad-token", "new_password": "` + encoded + `"}`}
	result, err := handler.resetPassword(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid or expired token")
}

// Issue #459: ConfirmPasswordReset errors must surface as a 4xx client
// error with the original message preserved, so the frontend renders a
// specific reason rather than the opaque "Failed to reset password" that
// a generic 500 produces. Without the NewClientError wrap in the handler,
// the error escaped as a plain error and got mapped to 500 / "Internal
// server error" by the response writer.
func TestHandler_resetPassword_ErrorIsClientError(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("ConfirmPasswordReset", ctx, mock.Anything).
		Return(errors.New("this is your current password, choose a different one"))

	handler := &Handler{auth: mockAuth}

	encoded := base64.StdEncoding.EncodeToString([]byte("ReUsedPassW0rd!"))
	req := &events.LambdaFunctionURLRequest{Body: `{"token": "valid-token", "new_password": "` + encoded + `"}`}
	_, err := handler.resetPassword(ctx, req)
	require.Error(t, err)

	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ConfirmPasswordReset failures to be wrapped in a clientError")
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.Error(), "current password")
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

// TestHandler_resetPassword_DecodesBase64 verifies issue #356: the
// resetPassword handler must base64-decode new_password before forwarding to
// the service, matching the pattern used by login / change-password /
// update-profile. Without this, the bcrypt hash stored represents the base64
// string rather than the plaintext, and the user gets locked out after
// completing the reset.
func TestHandler_resetPassword_DecodesBase64(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	// The service must receive the DECODED password — that's what bcrypt then
	// hashes and stores. If the handler skipped the decode, the captured
	// req.NewPassword would be the base64 string and this expectation would
	// fail.
	mockAuth.On("ConfirmPasswordReset", ctx, PasswordResetConfirm{
		Token:       "tok-abc",
		NewPassword: "PlainText#1",
	}).Return(nil)

	handler := &Handler{auth: mockAuth}

	encoded := base64.StdEncoding.EncodeToString([]byte("PlainText#1"))
	req := &events.LambdaFunctionURLRequest{
		Body: `{"token": "tok-abc", "new_password": "` + encoded + `"}`,
	}

	result, err := handler.resetPassword(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	mockAuth.AssertExpectations(t)
}

// TestHandler_resetPassword_InvalidBase64 covers the malformed-input path —
// the regression test mentioned in #356's acceptance criteria. A garbled
// payload must surface as a 4xx, not a 5xx, so the operator gets a clear
// "the link/payload is bad" instead of "internal server error".
func TestHandler_resetPassword_InvalidBase64(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Body: `{"token": "tok-abc", "new_password": "!!!not-valid-base64!!!"}`,
	}

	_, err := handler.resetPassword(ctx, req)
	assert.Error(t, err)
	// decodeBase64Password returns a ClientError(400) — assert the helper
	// short-circuited before any service call fired.
	mockAuth.AssertNotCalled(t, "ConfirmPasswordReset", mock.Anything, mock.Anything)
}

// TestHandler_resetPassword_ClientErrorSubstrings verifies that user-correctable
// errors from ConfirmPasswordReset are mapped to 400 ClientError so the frontend
// can surface the specific reason (e.g. "password must contain..." criteria).
func TestHandler_resetPassword_ClientErrorSubstrings(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("ConfirmPasswordReset", ctx, mock.Anything).
		Return(errors.New("password must contain a number"))

	handler := &Handler{auth: mockAuth}

	encoded := base64.StdEncoding.EncodeToString([]byte("weakpass"))
	req := &events.LambdaFunctionURLRequest{Body: `{"token": "tok-xyz", "new_password": "` + encoded + `"}`}
	_, err := handler.resetPassword(ctx, req)
	require.Error(t, err)

	ce, ok := IsClientError(err)
	require.True(t, ok, "user-correctable ConfirmPasswordReset error must be a ClientError, got %T: %v", err, err)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.Error(), "password must contain a number")
}

// TestHandler_resetPassword_ServerSideErrorPassesThrough verifies that server-side
// errors from ConfirmPasswordReset (DB outages, crypto failures, etc.) are NOT
// wrapped as 400 ClientError so the framework's default 500-mapping can fire.
func TestHandler_resetPassword_ServerSideErrorPassesThrough(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	mockAuth.On("ConfirmPasswordReset", ctx, mock.Anything).
		Return(errors.New("database connection lost"))

	handler := &Handler{auth: mockAuth}

	encoded := base64.StdEncoding.EncodeToString([]byte("ValidPass1!"))
	req := &events.LambdaFunctionURLRequest{Body: `{"token": "tok-xyz", "new_password": "` + encoded + `"}`}
	_, err := handler.resetPassword(ctx, req)
	require.Error(t, err)

	_, ok := IsClientError(err)
	assert.False(t, ok, "server-side errors must NOT be wrapped as ClientError; got ok=true")
	assert.Contains(t, err.Error(), "database connection lost")
}

// ---------------------------------------------------------------
// MFA enrollment / lifecycle handler tests (issue #497).
// ---------------------------------------------------------------

// b64 is a tiny helper to keep the password-encoding noise out of
// the test bodies. The frontend always base64-encodes passwords on
// the wire; the handlers decode before delegating to auth.
func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func authedReq(token, body string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer " + token},
		Body:    body,
	}
}

func TestHandler_login_MFARequired_ReturnsMFASentinel(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("Login", ctx, mock.Anything).Return((*LoginResponse)(nil), ErrMFARequired_test()).Once()

	handler := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Body: `{"email":"u@x.com","password":"` + b64("p") + `"}`,
	}
	_, err := handler.login(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 401, ce.code)
	assert.Equal(t, "mfa_required", ce.Error())
}

func TestHandler_login_InvalidMFACode_ReturnsCodedSentinel(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("Login", ctx, mock.Anything).Return((*LoginResponse)(nil), ErrInvalidMFACode_test()).Once()

	handler := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Body: `{"email":"u@x.com","password":"` + b64("p") + `","mfa_code":"000000"}`,
	}
	_, err := handler.login(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 401, ce.code)
	assert.Equal(t, "invalid_mfa_code", ce.Error())
}

func TestHandler_mfaSetup_HappyPath(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	session := &Session{UserID: "user-1", Email: "u@x.com", Role: "user"}
	mockAuth.On("ValidateSession", ctx, "tok").Return(session, nil)
	mockAuth.On("MFASetupAPI", ctx, "user-1", "pw").
		Return("SECRET123", "otpauth://totp/CUDly:u@x.com?secret=SECRET123", nil)

	handler := &Handler{auth: mockAuth}
	resp, err := handler.mfaSetup(ctx, authedReq("tok", `{"password":"`+b64("pw")+`"}`))
	require.NoError(t, err)
	assert.Equal(t, "SECRET123", resp.Secret)
	assert.Contains(t, resp.ProvisioningURI, "otpauth://")
}

func TestHandler_mfaSetup_WrongPassword(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	session := &Session{UserID: "user-1", Email: "u@x.com", Role: "user"}
	mockAuth.On("ValidateSession", ctx, "tok").Return(session, nil)
	mockAuth.On("MFASetupAPI", ctx, "user-1", "wrong").
		Return("", "", errors.New("invalid password"))

	handler := &Handler{auth: mockAuth}
	_, err := handler.mfaSetup(ctx, authedReq("tok", `{"password":"`+b64("wrong")+`"}`))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.Error(), "invalid password")
}

func TestHandler_mfaEnable_HappyPath(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	session := &Session{UserID: "user-1", Email: "u@x.com", Role: "user"}
	mockAuth.On("ValidateSession", ctx, "tok").Return(session, nil)
	mockAuth.On("MFAEnableAPI", ctx, "user-1", "123456").
		Return([]string{"AAAA-BBBB", "CCCC-DDDD"}, nil)

	handler := &Handler{auth: mockAuth}
	resp, err := handler.mfaEnable(ctx, authedReq("tok", `{"code":"123456"}`))
	require.NoError(t, err)
	assert.Len(t, resp.RecoveryCodes, 2)
}

func TestHandler_mfaEnable_NoSession(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "bad").Return(nil, errors.New("invalid session"))
	handler := &Handler{auth: mockAuth}

	_, err := handler.mfaEnable(ctx, authedReq("bad", `{"code":"123456"}`))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 401, ce.code)
}

func TestHandler_mfaDisable_HappyPath(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	session := &Session{UserID: "user-1", Email: "u@x.com", Role: "user"}
	mockAuth.On("ValidateSession", ctx, "tok").Return(session, nil)
	mockAuth.On("MFADisableAPI", ctx, "user-1", "pw", "123456").Return(nil)

	handler := &Handler{auth: mockAuth}
	_, err := handler.mfaDisable(ctx, authedReq("tok", `{"password":"`+b64("pw")+`","code":"123456"}`))
	require.NoError(t, err)
}

func TestHandler_mfaRegenerateRecoveryCodes_HappyPath(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	session := &Session{UserID: "user-1", Email: "u@x.com", Role: "user"}
	mockAuth.On("ValidateSession", ctx, "tok").Return(session, nil)
	mockAuth.On("MFARegenerateRecoveryCodesAPI", ctx, "user-1", "123456").
		Return([]string{"AAAA-BBBB"}, nil)

	handler := &Handler{auth: mockAuth}
	resp, err := handler.mfaRegenerateRecoveryCodes(ctx, authedReq("tok", `{"code":"123456"}`))
	require.NoError(t, err)
	assert.Len(t, resp.RecoveryCodes, 1)
}

// ErrMFARequired_test / ErrInvalidMFACode_test return the sentinel
// errors from the auth package via a non-importing path so the api
// package's _test.go files don't need to import the whole auth
// package just for two values. Both are exported sentinels in the
// auth package (errors.go); the api package's login handler maps
// them via errors.Is(). Here we just wrap them so the mocked Login
// returns the same value the real service would.
func ErrMFARequired_test() error    { return mfaRequiredSentinel }
func ErrInvalidMFACode_test() error { return mfaInvalidSentinel }
