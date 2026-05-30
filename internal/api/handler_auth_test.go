package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/auth"
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

// TestHandler_login_OpaqueError_HidesInternalMessage is a regression test for
// issue #937: the login catch-all 401 must return the opaque "invalid
// credentials" string regardless of what the auth service returns. Internal
// error messages (e.g. "internal: user 42 locked since 2025-01-01") must never
// be forwarded to the client because they reveal account state.
func TestHandler_login_OpaqueError_HidesInternalMessage(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	// A service error containing distinctive internal detail that must never
	// reach the client.
	internalMsg := "internal: user 42 locked since 2025-01-01T00:00:00Z"
	mockAuth.On("Login", ctx, mock.Anything).Return((*LoginResponse)(nil), errors.New(internalMsg)).Once()

	handler := &Handler{auth: mockAuth}

	encodedPassword := base64.StdEncoding.EncodeToString([]byte("anypassword"))
	req := &events.LambdaFunctionURLRequest{
		Body: `{"email": "victim@example.com", "password": "` + encodedPassword + `"}`,
	}

	result, err := handler.login(ctx, req)
	assert.Nil(t, result)
	require.Error(t, err)

	ce, ok := IsClientError(err)
	require.True(t, ok, "login failure must be a ClientError")
	assert.Equal(t, 401, ce.code)
	// The opaque message must be returned.
	assert.Equal(t, "invalid credentials", ce.message)
	// The internal message must not leak to the client.
	assert.NotContains(t, ce.message, "internal:", "internal error detail must not be forwarded to the client")
	assert.NotContains(t, ce.message, "user 42", "user identifier must not appear in the 401 response")
	assert.NotContains(t, ce.message, "locked", "account state must not appear in the 401 response")
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
	}
	user := &User{
		ID:         "12345678-1234-1234-1234-123456789abc",
		Email:      "test@example.com",
		Groups:     []string{"00000000-0000-5000-8000-000000000001"},
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
	assert.Equal(t, []string{"00000000-0000-5000-8000-000000000001"}, result.Groups)
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

	// Use the exact string the real service emits so isResetPasswordClientError
	// classifies it as a 400 client error, not a 500 internal error.
	mockAuth.On("ConfirmPasswordReset", ctx, mock.Anything).Return(errors.New("invalid or expired reset token"))

	handler := &Handler{auth: mockAuth}

	// new_password must be base64-encoded — the handler now decodes before
	// forwarding (issue #356). Encoding "newpassword123" so the decode step
	// passes and the error path under test (bad token) is the one that fires.
	encoded := base64.StdEncoding.EncodeToString([]byte("newpassword123"))
	req := &events.LambdaFunctionURLRequest{Body: `{"token": "bad-token", "new_password": "` + encoded + `"}`}
	result, err := handler.resetPassword(ctx, req)
	require.Error(t, err)
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expired-token error must be wrapped as a client error (400)")
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.Error(), "invalid or expired reset token")
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

// TestHandler_updateProfile_RejectsInvalidEmail is a regression guard for
// issue #868: the profile-update handler must reject TLD-less addresses such
// as "user@host" with a 400 before reaching the auth service, mirroring the
// constraint that sign-up already enforces.
func TestHandler_updateProfile_RejectsInvalidEmail(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "12345678-1234-1234-1234-123456789abc"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	// UpdateUserProfile must NOT be called — validation should short-circuit first.

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer test-token"},
		Body:    `{"email": "user@host"}`,
	}

	result, err := handler.updateProfile(ctx, req)
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a ClientError, got: %v", err)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.message, "email")
	// Confirm UpdateUserProfile was never reached.
	mockAuth.AssertNotCalled(t, "UpdateUserProfile")
}

// TestHandler_updateProfile_AcceptsValidEmail verifies that a well-formed
// address passes validation and reaches the auth service unchanged.
func TestHandler_updateProfile_AcceptsValidEmail(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	session := &Session{UserID: "12345678-1234-1234-1234-123456789abc"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.On("UpdateUserProfile", ctx, "12345678-1234-1234-1234-123456789abc", "new@example.com", "", "").Return(nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer test-token"},
		Body:    `{"email": "new@example.com"}`,
	}

	result, err := handler.updateProfile(ctx, req)
	require.NoError(t, err)
	resp := result.(map[string]string)
	assert.Equal(t, "profile updated", resp["status"])
	mockAuth.AssertCalled(t, "UpdateUserProfile", ctx, "12345678-1234-1234-1234-123456789abc", "new@example.com", "", "")
}

// TestHandler_updateProfile_WrongCurrentPassword verifies that a wrong current
// password returned by the auth service is surfaced as 401, not a generic 500
// (issue #929). The acting user is checking their own credential so a precise
// message is safe and helpful.
func TestHandler_updateProfile_WrongCurrentPassword(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	session := &Session{UserID: "12345678-1234-1234-1234-123456789abc"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.On("UpdateUserProfile", mock.MatchedBy(func(c context.Context) bool { return c != nil }), "12345678-1234-1234-1234-123456789abc", "", "wrongpass", "").
		Return(auth.ErrCurrentPasswordIncorrect)

	handler := &Handler{auth: mockAuth}

	encoded := base64.StdEncoding.EncodeToString([]byte("wrongpass"))
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer test-token"},
		Body:    `{"email": "", "current_password": "` + encoded + `"}`,
	}

	_, err := handler.updateProfile(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "wrong-password error must be a ClientError, not a 500")
	assert.Equal(t, 401, ce.code)
	assert.Equal(t, auth.ErrCurrentPasswordIncorrect.Error(), ce.message)
}

// TestHandler_updateProfile_DuplicateEmail verifies that a duplicate-email
// conflict is surfaced as 409 with a privacy-preserving message that does NOT
// confirm whether another account holds the address (issue #929).
func TestHandler_updateProfile_DuplicateEmail(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	session := &Session{UserID: "12345678-1234-1234-1234-123456789abc"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(session, nil)
	mockAuth.On("UpdateUserProfile", mock.MatchedBy(func(c context.Context) bool { return c != nil }), "12345678-1234-1234-1234-123456789abc", "taken@example.com", "mypass", "").
		Return(auth.ErrEmailInUse)

	handler := &Handler{auth: mockAuth}

	encoded := base64.StdEncoding.EncodeToString([]byte("mypass"))
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer test-token"},
		Body:    `{"email": "taken@example.com", "current_password": "` + encoded + `"}`,
	}

	_, err := handler.updateProfile(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "duplicate-email error must be a ClientError, not a 500")
	assert.Equal(t, 409, ce.code)
	// Must NOT expose the raw ErrEmailInUse message which would confirm another
	// account exists. The privacy-preserving message is "Unable to update email".
	assert.Equal(t, "Unable to update email", ce.message)
	assert.NotContains(t, ce.message, "already in use", "response must not confirm another account's existence")
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
	mockAuth.AssertExpectations(t)
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
	mockAuth.AssertExpectations(t)
}

func TestHandler_mfaSetup_HappyPath(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	session := &Session{UserID: "user-1", Email: "u@x.com"}
	mockAuth.On("ValidateSession", ctx, "tok").Return(session, nil)
	mockAuth.On("MFASetupAPI", ctx, "user-1", "pw").
		Return("SECRET123", "otpauth://totp/CUDly:u@x.com?secret=SECRET123", nil)

	handler := &Handler{auth: mockAuth}
	resp, err := handler.mfaSetup(ctx, authedReq("tok", `{"password":"`+b64("pw")+`"}`))
	require.NoError(t, err)
	assert.Equal(t, "SECRET123", resp.Secret)
	assert.Contains(t, resp.ProvisioningURI, "otpauth://")
	mockAuth.AssertExpectations(t)
}

func TestHandler_mfaSetup_WrongPassword(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	session := &Session{UserID: "user-1", Email: "u@x.com"}
	mockAuth.On("ValidateSession", ctx, "tok").Return(session, nil)
	// The real service returns a wrapped sentinel; the handler maps it to a
	// 400 ClientError via errors.Is (issue #512). The mock must return the
	// same sentinel so the errors.Is check in mapMFAServiceError fires.
	mockAuth.On("MFASetupAPI", ctx, "user-1", "wrong").
		Return("", "", fmt.Errorf("%w", auth.ErrMFAInvalidPassword))

	handler := &Handler{auth: mockAuth}
	_, err := handler.mfaSetup(ctx, authedReq("tok", `{"password":"`+b64("wrong")+`"}`))
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.Error(), "invalid password")
	mockAuth.AssertExpectations(t)
}

func TestHandler_mfaEnable_HappyPath(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	session := &Session{UserID: "user-1", Email: "u@x.com"}
	mockAuth.On("ValidateSession", ctx, "tok").Return(session, nil)
	mockAuth.On("MFAEnableAPI", ctx, "user-1", "123456").
		Return([]string{"AAAA-BBBB", "CCCC-DDDD"}, nil)

	handler := &Handler{auth: mockAuth}
	resp, err := handler.mfaEnable(ctx, authedReq("tok", `{"code":"123456"}`))
	require.NoError(t, err)
	assert.Len(t, resp.RecoveryCodes, 2)
	mockAuth.AssertExpectations(t)
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
	mockAuth.AssertExpectations(t)
}

func TestHandler_mfaDisable_HappyPath(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	session := &Session{UserID: "user-1", Email: "u@x.com"}
	mockAuth.On("ValidateSession", ctx, "tok").Return(session, nil)
	mockAuth.On("MFADisableAPI", ctx, "user-1", "pw", "123456").Return(nil)

	handler := &Handler{auth: mockAuth}
	_, err := handler.mfaDisable(ctx, authedReq("tok", `{"password":"`+b64("pw")+`","code":"123456"}`))
	require.NoError(t, err)
	mockAuth.AssertExpectations(t)
}

func TestHandler_mfaRegenerateRecoveryCodes_HappyPath(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	session := &Session{UserID: "user-1", Email: "u@x.com"}
	mockAuth.On("ValidateSession", ctx, "tok").Return(session, nil)
	mockAuth.On("MFARegenerateRecoveryCodesAPI", ctx, "user-1", "123456").
		Return([]string{"AAAA-BBBB"}, nil)

	handler := &Handler{auth: mockAuth}
	resp, err := handler.mfaRegenerateRecoveryCodes(ctx, authedReq("tok", `{"code":"123456"}`))
	require.NoError(t, err)
	assert.Len(t, resp.RecoveryCodes, 1)
	mockAuth.AssertExpectations(t)
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

// Tests for GET /api/auth/me/permissions (issue #917).

func TestHandler_getCurrentUserPermissions_NoAuthService(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{auth: nil}
	req := &events.LambdaFunctionURLRequest{}
	_, err := handler.getCurrentUserPermissions(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication service not configured")
}

func TestHandler_getCurrentUserPermissions_NoToken(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	handler := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{}}
	_, err := handler.getCurrentUserPermissions(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 401, ce.code)
}

func TestHandler_getCurrentUserPermissions_InvalidSession(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	mockAuth.On("ValidateSession", ctx, "bad-token").Return((*Session)(nil), errors.New("expired"))
	handler := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"Authorization": "Bearer bad-token"}}
	_, err := handler.getCurrentUserPermissions(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 401, ce.code)
}

// TestHandler_getCurrentUserPermissions_RegularUser asserts that a non-admin user
// with two groups gets the union of their groups' permissions and is_admin == false.
func TestHandler_getCurrentUserPermissions_RegularUser(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	session := &Session{UserID: "user-1"}
	mockAuth.On("ValidateSession", ctx, "tok").Return(session, nil)

	// Two groups: one grants view:recommendations, the other view:plans.
	perms := []auth.APIPermission{
		{Action: "view", Resource: "recommendations"},
		{Action: "view", Resource: "plans"},
	}
	mockAuth.On("GetUserPermissionsAPI", ctx, "user-1").Return(perms, nil)

	handler := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"Authorization": "Bearer tok"}}
	result, err := handler.getCurrentUserPermissions(ctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsAdmin)
	require.Len(t, result.Permissions, 2)
	assert.Equal(t, PermissionEntry{Action: "view", Resource: "recommendations"}, result.Permissions[0])
	assert.Equal(t, PermissionEntry{Action: "view", Resource: "plans"}, result.Permissions[1])
}

// TestHandler_getCurrentUserPermissions_Admin asserts that an Administrators-group
// member gets the {admin, *} wildcard and is_admin == true.
func TestHandler_getCurrentUserPermissions_Admin(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	session := &Session{UserID: "admin-1"}
	mockAuth.On("ValidateSession", ctx, "admin-tok").Return(session, nil)

	perms := []auth.APIPermission{
		{Action: auth.ActionAdmin, Resource: auth.ResourceAll},
	}
	mockAuth.On("GetUserPermissionsAPI", ctx, "admin-1").Return(perms, nil)

	handler := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"Authorization": "Bearer admin-tok"}}
	result, err := handler.getCurrentUserPermissions(ctx, req)
	require.NoError(t, err)
	assert.True(t, result.IsAdmin)
	require.Len(t, result.Permissions, 1)
	assert.Equal(t, PermissionEntry{Action: "admin", Resource: "*"}, result.Permissions[0])
}

// TestHandler_getCurrentUserPermissions_Unauth ensures unauthenticated requests get 401.
func TestHandler_getCurrentUserPermissions_Unauth(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockAuth.On("ValidateSession", ctx, mock.Anything).Return((*Session)(nil), errors.New("invalid"))

	handler := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"Authorization": "Bearer bogus"}}
	_, err := handler.getCurrentUserPermissions(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 401, ce.code)
}

// TestHandler_getCurrentUserPermissions_UnexpectedPayload guards CR #922 F1:
// when GetUserPermissionsAPI returns an unexpected payload shape, the handler
// must fail loudly (server error) rather than silently degrade to an empty
// permission set (which would render as "user lost access" in the frontend).
func TestHandler_getCurrentUserPermissions_UnexpectedPayload(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	session := &Session{UserID: "user-1"}
	mockAuth.On("ValidateSession", ctx, "tok").Return(session, nil)
	// Adapter returns the wrong concrete type (e.g. a misconfigured
	// implementation). The handler must surface this as a server error,
	// not silently return an empty PermissionEntry slice.
	mockAuth.On("GetUserPermissionsAPI", ctx, "user-1").Return("not-a-permissions-slice", nil)

	handler := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"Authorization": "Bearer tok"}}
	_, err := handler.getCurrentUserPermissions(ctx, req)
	require.Error(t, err)
	// Not a 4xx ClientError — it's a server bug, must surface as 500.
	_, isClient := IsClientError(err)
	assert.False(t, isClient, "unexpected payload should NOT be a ClientError (would mask the server bug as a 4xx)")
	assert.Contains(t, err.Error(), "GetUserPermissionsAPI returned unexpected payload type")
}

// TestHandler_getCurrentUserPermissions_AdminAPIKey guards CR #922 F2:
// the AuthUser route admits the stateless admin API key as well as bearer
// sessions, so the handler must honour an X-API-Key-authenticated request
// instead of forcing a bearer session a second time and returning 401.
func TestHandler_getCurrentUserPermissions_AdminAPIKey(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	handler := &Handler{auth: mockAuth, apiKey: "admin-secret"}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"X-API-Key": "admin-secret"}}
	result, err := handler.getCurrentUserPermissions(ctx, req)
	require.NoError(t, err)
	// The stateless admin API key has no backing user row, so the handler
	// short-circuits to {admin, *} + is_admin=true rather than calling
	// GetUserPermissionsAPI (which would fail to find an admin-api-key row).
	assert.True(t, result.IsAdmin)
	require.Len(t, result.Permissions, 1)
	assert.Equal(t, PermissionEntry{Action: auth.ActionAdmin, Resource: auth.ResourceAll}, result.Permissions[0])
}

// TestHandler_getCurrentUserPermissions_UserAPIKey guards CR #922 F2:
// a user API key resolves to the owning user, and the handler must look
// up that user's effective permissions instead of returning 401 because
// the request has no bearer token.
func TestHandler_getCurrentUserPermissions_UserAPIKey(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	user := &auth.User{ID: "user-42"}
	mockAuth.On("ValidateUserAPIKeyAPI", ctx, "user-key").Return((any)(nil), any(user), nil)

	perms := []auth.APIPermission{
		{Action: "view", Resource: "recommendations"},
	}
	mockAuth.On("GetUserPermissionsAPI", ctx, "user-42").Return(perms, nil)

	handler := &Handler{auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"X-API-Key": "user-key"}}
	result, err := handler.getCurrentUserPermissions(ctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsAdmin)
	require.Len(t, result.Permissions, 1)
	assert.Equal(t, PermissionEntry{Action: "view", Resource: "recommendations"}, result.Permissions[0])
}

// TestHandler_login_ErrorEquivalence verifies that two failed login attempts --
// one for a non-existent user and one for a wrong password on an existing user
// -- produce IDENTICAL ClientError status codes and messages at the handler
// layer (issue #416).
//
// This is the regression guard for username-enumeration via distinct error
// messages: if either the HTTP status or the error body differ between the two
// paths, an attacker can determine whether an email address is registered.
func TestHandler_login_ErrorEquivalence(t *testing.T) {
	ctx := context.Background()
	encodedPassword := base64.StdEncoding.EncodeToString([]byte("SomePassword1!"))

	// Path 1: service returns the "user not found" message (maps to the
	// GetUserByEmail-error path in the real service).
	mockAuthNotFound := new(MockAuthService)
	mockAuthNotFound.On("Login", ctx, mock.Anything).
		Return((*LoginResponse)(nil), errors.New("Check your email address and password and try again")).Once()
	t.Cleanup(func() { mockAuthNotFound.AssertExpectations(t) })

	handlerNotFound := &Handler{auth: mockAuthNotFound}
	reqNotFound := &events.LambdaFunctionURLRequest{
		Body: `{"email":"nonexistent@example.com","password":"` + encodedPassword + `"}`,
	}
	_, errNotFound := handlerNotFound.login(ctx, reqNotFound)
	require.Error(t, errNotFound, "expected login to fail for unknown user")

	ceNotFound, okNotFound := IsClientError(errNotFound)
	require.True(t, okNotFound, "expected ClientError for unknown-user path")

	// Path 2: service returns the "wrong password" message (existing user).
	mockAuthWrongPass := new(MockAuthService)
	mockAuthWrongPass.On("Login", ctx, mock.Anything).
		Return((*LoginResponse)(nil), errors.New("Check your email address and password and try again")).Once()
	t.Cleanup(func() { mockAuthWrongPass.AssertExpectations(t) })

	handlerWrongPass := &Handler{auth: mockAuthWrongPass}
	reqWrongPass := &events.LambdaFunctionURLRequest{
		Body: `{"email":"existing@example.com","password":"` + encodedPassword + `"}`,
	}
	_, errWrongPass := handlerWrongPass.login(ctx, reqWrongPass)
	require.Error(t, errWrongPass, "expected login to fail for wrong password")

	ceWrongPass, okWrongPass := IsClientError(errWrongPass)
	require.True(t, okWrongPass, "expected ClientError for wrong-password path")

	// Assert IDENTICAL status code and message body for both failure paths.
	assert.Equal(t, ceNotFound.code, ceWrongPass.code,
		"HTTP status must be identical for unknown-user and wrong-password paths")
	assert.Equal(t, ceNotFound.Error(), ceWrongPass.Error(),
		"error body must be identical for unknown-user and wrong-password paths to prevent enumeration")
}

// ---------------------------------------------------------------
// mapMFAServiceError sentinel-to-HTTP-code mapping tests (issue #512).
//
// Each test verifies that a specific auth sentinel maps to the
// expected HTTP status code in mapMFAServiceError. These tests would
// fail if someone renames a sentinel value in the auth package
// without updating the switch in mapMFAServiceError.
// ---------------------------------------------------------------

func TestMapMFAServiceError_Nil(t *testing.T) {
	assert.Nil(t, mapMFAServiceError(nil))
}

func TestMapMFAServiceError_NonSentinelPassesThrough(t *testing.T) {
	plain := errors.New("database connection lost")
	got := mapMFAServiceError(plain)
	_, isClient := IsClientError(got)
	assert.False(t, isClient, "non-sentinel errors must not be wrapped as ClientError")
	assert.Equal(t, plain, got)
}

func testMFASentinel400(t *testing.T, sentinel error, name string) {
	t.Helper()
	wrapped := fmt.Errorf("some context: %w", sentinel)
	got := mapMFAServiceError(wrapped)
	ce, ok := IsClientError(got)
	require.True(t, ok, "%s must map to a ClientError, got %T: %v", name, got, got)
	assert.Equal(t, 400, ce.code, "%s must map to HTTP 400", name)
	assert.Contains(t, ce.Error(), sentinel.Error())
}

func TestMapMFAServiceError_InvalidPassword_Is400(t *testing.T) {
	testMFASentinel400(t, auth.ErrMFAInvalidPassword, "ErrMFAInvalidPassword")
}

func TestMapMFAServiceError_InvalidCode_Is400(t *testing.T) {
	testMFASentinel400(t, auth.ErrMFAInvalidCode, "ErrMFAInvalidCode")
}

func TestMapMFAServiceError_CodeRequired_Is400(t *testing.T) {
	testMFASentinel400(t, auth.ErrMFACodeRequired, "ErrMFACodeRequired")
}

func TestMapMFAServiceError_NoEnrollmentInProgress_Is400(t *testing.T) {
	testMFASentinel400(t, auth.ErrMFANoEnrollmentInProgress, "ErrMFANoEnrollmentInProgress")
}

func TestMapMFAServiceError_EnrollmentExpired_Is400(t *testing.T) {
	testMFASentinel400(t, auth.ErrMFAEnrollmentExpired, "ErrMFAEnrollmentExpired")
}

func TestMapMFAServiceError_NotEnabled_Is400(t *testing.T) {
	testMFASentinel400(t, auth.ErrMFANotEnabled, "ErrMFANotEnabled")
}

func TestMapMFAServiceError_AuthFailed_Is401(t *testing.T) {
	// ErrMFAAuthFailed must map to 401 (not 400) to prevent user enumeration:
	// both "user not found" and "DB error" paths surface as opaque 401.
	wrapped := fmt.Errorf("some context: %w", auth.ErrMFAAuthFailed)
	got := mapMFAServiceError(wrapped)
	ce, ok := IsClientError(got)
	require.True(t, ok, "ErrMFAAuthFailed must map to a ClientError")
	assert.Equal(t, 401, ce.code, "ErrMFAAuthFailed must map to HTTP 401")
	assert.Contains(t, ce.Error(), auth.ErrMFAAuthFailed.Error())
}
