package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

// API Key handlers

// listAPIKeys handles GET /api/api-keys
func (h *Handler) listAPIKeys(ctx context.Context, req *events.LambdaFunctionURLRequest) (interface{}, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Get current user from token
	token := h.extractBearerToken(req)
	if token == "" {
		return nil, fmt.Errorf("no authorization token provided")
	}

	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("invalid session: %w", err)
	}

	// List API keys for the current user - call service directly
	keys, err := h.auth.ListUserAPIKeysAPI(ctx, session.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to list API keys: %w", err)
	}

	return keys, nil
}

// createAPIKey handles POST /api/api-keys
func (h *Handler) createAPIKey(ctx context.Context, req *events.LambdaFunctionURLRequest) (interface{}, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Get current user from token
	token := h.extractBearerToken(req)
	if token == "" {
		return nil, fmt.Errorf("no authorization token provided")
	}

	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("invalid session: %w", err)
	}

	// Rate limiting: 30 admin operations per user per minute
	if h.rateLimiter != nil {
		allowed, err := h.rateLimiter.AllowWithUser(ctx, session.UserID, "admin")
		if err != nil {
			// Log but continue on rate limiter errors
		} else if !allowed {
			return nil, fmt.Errorf("too many requests, please slow down")
		}
	}

	// Parse request body
	var createReq CreateAPIKeyRequest
	if err := json.Unmarshal([]byte(req.Body), &createReq); err != nil {
		return nil, fmt.Errorf("invalid request body: %w", err)
	}

	// Create API key - call service directly
	result, err := h.auth.CreateAPIKeyAPI(ctx, session.UserID, createReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create API key: %w", err)
	}

	return result, nil
}

// deleteAPIKey handles DELETE /api/api-keys/{id}
func (h *Handler) deleteAPIKey(ctx context.Context, req *events.LambdaFunctionURLRequest) (interface{}, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Get current user from token
	token := h.extractBearerToken(req)
	if token == "" {
		return nil, fmt.Errorf("no authorization token provided")
	}

	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("invalid session: %w", err)
	}

	// Extract key ID from path
	path := req.RequestContext.HTTP.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid path: missing key ID")
	}
	keyID := parts[len(parts)-1]

	// Validate UUID format
	if err := validateUUID(keyID); err != nil {
		return nil, err
	}

	// Delete API key - call service directly
	if err := h.auth.DeleteAPIKeyAPI(ctx, session.UserID, keyID); err != nil {
		return nil, fmt.Errorf("failed to delete API key: %w", err)
	}

	return map[string]string{"status": "deleted"}, nil
}

// revokeAPIKey handles POST /api/api-keys/{id}/revoke
func (h *Handler) revokeAPIKey(ctx context.Context, req *events.LambdaFunctionURLRequest) (interface{}, error) {
	if h.auth == nil {
		return nil, fmt.Errorf("authentication service not configured")
	}

	// Get current user from token
	token := h.extractBearerToken(req)
	if token == "" {
		return nil, fmt.Errorf("no authorization token provided")
	}

	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("invalid session: %w", err)
	}

	// Extract key ID from path (format: /api/api-keys/{id}/revoke)
	path := req.RequestContext.HTTP.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 {
		return nil, fmt.Errorf("invalid path: missing key ID")
	}
	keyID := parts[len(parts)-2] // Second to last part (before "revoke")

	// Validate UUID format
	if err := validateUUID(keyID); err != nil {
		return nil, err
	}

	// Revoke API key - call service directly
	if err := h.auth.RevokeAPIKeyAPI(ctx, session.UserID, keyID); err != nil {
		return nil, fmt.Errorf("failed to revoke API key: %w", err)
	}

	return map[string]string{"status": "revoked"}, nil
}

// Helper function to format time pointer as string
func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02T15:04:05Z07:00")
}
