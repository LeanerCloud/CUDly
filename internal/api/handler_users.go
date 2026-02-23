// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"encoding/json"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/aws/aws-lambda-go/events"
)

// User management handlers

// listUsers handles GET /api/users
func (h *Handler) listUsers(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	users, err := h.auth.ListUsersAPI(ctx)
	if err != nil {
		return nil, err
	}

	return map[string]any{"users": users}, nil
}

// createUser handles POST /api/users
func (h *Handler) createUser(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requireAdmin(ctx, req)
	if err != nil {
		return nil, err
	}

	// Rate limiting: 30 admin operations per user per minute
	if h.rateLimiter != nil {
		allowed, err := h.rateLimiter.AllowWithUser(ctx, session.UserID, "admin")
		if err != nil {
			// Log but continue on rate limiter errors
		} else if !allowed {
			return nil, NewClientError(429, "too many requests, please slow down")
		}
	}

	var createReq auth.APICreateUserRequest
	if err := json.Unmarshal([]byte(req.Body), &createReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	// Decode base64-encoded password
	if decoded, err := decodeBase64Password(createReq.Password); err != nil {
		return nil, err
	} else {
		createReq.Password = decoded
	}

	user, err := h.auth.CreateUserAPI(ctx, createReq)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// getUser handles GET /api/users/{id}
func (h *Handler) getUser(ctx context.Context, req *events.LambdaFunctionURLRequest, userID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(userID); err != nil {
		return nil, err
	}

	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	user, err := h.auth.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// updateUser handles PUT /api/users/{id}
func (h *Handler) updateUser(ctx context.Context, req *events.LambdaFunctionURLRequest, userID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(userID); err != nil {
		return nil, err
	}

	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	var updateReq auth.APIUpdateUserRequest
	if err := json.Unmarshal([]byte(req.Body), &updateReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	user, err := h.auth.UpdateUserAPI(ctx, userID, updateReq)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// deleteUser handles DELETE /api/users/{id}
func (h *Handler) deleteUser(ctx context.Context, req *events.LambdaFunctionURLRequest, userID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(userID); err != nil {
		return nil, err
	}

	session, err := h.requireAdmin(ctx, req)
	if err != nil {
		return nil, err
	}

	// Prevent self-deletion
	if session.UserID == userID {
		return nil, NewClientError(400, "cannot delete your own account")
	}

	if err := h.auth.DeleteUser(ctx, userID); err != nil {
		return nil, err
	}

	return map[string]string{"status": "user deleted"}, nil
}
