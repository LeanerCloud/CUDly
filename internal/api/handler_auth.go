// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
)

// Auth handlers

func (h *Handler) login(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Rate limiting: 5 attempts per IP per 15 minutes
	if err := h.checkRateLimit(ctx, req, "login"); err != nil {
		return nil, err
	}

	var loginReq LoginRequest
	if err := json.Unmarshal([]byte(req.Body), &loginReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Decode base64-encoded password
	if decoded, err := decodeBase64Password(loginReq.Password); err != nil {
		return nil, err
	} else {
		loginReq.Password = decoded
	}

	response, err := h.auth.Login(ctx, loginReq)
	if err != nil {
		return nil, NewClientError(401, err.Error())
	}

	return response, nil
}

func (h *Handler) logout(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Get token from Authorization header
	token := h.extractBearerToken(req)
	if token == "" {
		return nil, NewClientError(401, "no authorization token provided")
	}

	if err := h.auth.Logout(ctx, token); err != nil {
		return nil, NewClientError(401, "invalid session")
	}

	return map[string]string{"status": "logged out"}, nil
}

func (h *Handler) getCurrentUser(ctx context.Context, req *events.LambdaFunctionURLRequest) (*CurrentUserResponse, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Get token from Authorization header
	token := h.extractBearerToken(req)
	if token == "" {
		return nil, NewClientError(401, "no authorization token provided")
	}

	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil {
		return nil, NewClientError(401, "invalid session")
	}

	user, err := h.auth.GetUser(ctx, session.UserID)
	if err != nil {
		return nil, err
	}

	return &CurrentUserResponse{
		ID:         user.ID,
		Email:      user.Email,
		Role:       user.Role,
		MFAEnabled: user.MFAEnabled,
	}, nil
}

func (h *Handler) checkAdminExists(ctx context.Context, req *events.LambdaFunctionURLRequest) (*AdminExistsResponse, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Rate limiting: 100 requests per IP per minute (light limit for public endpoint)
	if err := h.checkRateLimit(ctx, req, "api_general"); err != nil {
		return nil, err
	}

	exists, err := h.auth.CheckAdminExists(ctx)
	if err != nil {
		return nil, err
	}

	return &AdminExistsResponse{AdminExists: exists}, nil
}

func (h *Handler) setupAdmin(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Rate limiting: 5 attempts per IP per 15 minutes (same as login)
	if err := h.checkRateLimit(ctx, req, "login"); err != nil {
		return nil, err
	}

	var setupReq SetupAdminRequest
	if err := json.Unmarshal([]byte(req.Body), &setupReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	response, err := h.auth.SetupAdmin(ctx, setupReq)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func (h *Handler) forgotPassword(ctx context.Context, body string) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	var pwdReq PasswordResetRequest
	if err := json.Unmarshal([]byte(body), &pwdReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Rate limiting: 3 attempts per email per hour to prevent enumeration attacks
	if h.rateLimiter != nil {
		allowed, err := h.rateLimiter.AllowWithEmail(ctx, pwdReq.Email, "forgot_password")
		if err != nil {
			logging.Warnf("Rate limiter error for email %s: %v", pwdReq.Email, err)
			// Continue on rate limiter errors to avoid blocking legitimate requests
		} else if !allowed {
			logging.Warnf("Rate limit exceeded for forgot password: %s", pwdReq.Email)
			// Always return success message to prevent email enumeration
			return map[string]string{"status": "if the email exists, a reset link has been sent"}, nil
		}
	}

	// Always return success to prevent email enumeration
	if err := h.auth.RequestPasswordReset(ctx, pwdReq.Email); err != nil {
		logging.Warnf("Password reset request error: %v", err)
	}

	return map[string]string{"status": "if the email exists, a reset link has been sent"}, nil
}

func (h *Handler) resetPassword(ctx context.Context, body string) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	var pwdResetReq PasswordResetConfirm
	if err := json.Unmarshal([]byte(body), &pwdResetReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	if err := h.auth.ConfirmPasswordReset(ctx, pwdResetReq); err != nil {
		return nil, err
	}

	return map[string]string{"status": "password reset successful"}, nil
}

func (h *Handler) updateProfile(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Get current user from token
	token := h.extractBearerToken(req)
	if token == "" {
		return nil, NewClientError(401, "no authorization token provided")
	}

	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil {
		return nil, NewClientError(401, "invalid session")
	}

	// Parse request body
	var profileReq ProfileUpdateRequest
	if err := json.Unmarshal([]byte(req.Body), &profileReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Decode base64-encoded passwords
	currentPassword, err := decodeBase64Password(profileReq.CurrentPassword)
	if err != nil {
		return nil, err
	}
	newPassword, err := decodeBase64Password(profileReq.NewPassword)
	if err != nil {
		return nil, err
	}

	// Update profile through auth service
	if err := h.auth.UpdateUserProfile(ctx, session.UserID, profileReq.Email, currentPassword, newPassword); err != nil {
		return nil, err
	}

	return map[string]string{"status": "profile updated"}, nil
}

// changePassword handles POST /api/auth/change-password
func (h *Handler) changePassword(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	token := h.extractBearerToken(req)
	if token == "" {
		return nil, NewClientError(401, "no authorization token provided")
	}

	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil {
		return nil, NewClientError(401, "invalid session")
	}

	var pwdReq ChangePasswordRequest
	if err := json.Unmarshal([]byte(req.Body), &pwdReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Decode base64-encoded passwords
	currentPassword, err := decodeBase64Password(pwdReq.CurrentPassword)
	if err != nil {
		return nil, err
	}
	newPassword, err := decodeBase64Password(pwdReq.NewPassword)
	if err != nil {
		return nil, err
	}

	if err := h.auth.ChangePasswordAPI(ctx, session.UserID, currentPassword, newPassword); err != nil {
		return nil, err
	}

	return map[string]string{"status": "password changed"}, nil
}
