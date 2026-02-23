// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"encoding/json"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/aws/aws-lambda-go/events"
)

// Group management handlers

// listGroups handles GET /api/groups
func (h *Handler) listGroups(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	groups, err := h.auth.ListGroupsAPI(ctx)
	if err != nil {
		return nil, err
	}

	return map[string]any{"groups": groups}, nil
}

// createGroup handles POST /api/groups
func (h *Handler) createGroup(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
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

	var createReq auth.APICreateGroupRequest
	if err := json.Unmarshal([]byte(req.Body), &createReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	group, err := h.auth.CreateGroupAPI(ctx, createReq)
	if err != nil {
		return nil, err
	}

	return group, nil
}

// getGroup handles GET /api/groups/{id}
func (h *Handler) getGroup(ctx context.Context, req *events.LambdaFunctionURLRequest, groupID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(groupID); err != nil {
		return nil, err
	}

	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	group, err := h.auth.GetGroupAPI(ctx, groupID)
	if err != nil {
		return nil, err
	}

	return group, nil
}

// updateGroup handles PUT /api/groups/{id}
func (h *Handler) updateGroup(ctx context.Context, req *events.LambdaFunctionURLRequest, groupID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(groupID); err != nil {
		return nil, err
	}

	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	var updateReq auth.APIUpdateGroupRequest
	if err := json.Unmarshal([]byte(req.Body), &updateReq); err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	group, err := h.auth.UpdateGroupAPI(ctx, groupID, updateReq)
	if err != nil {
		return nil, err
	}

	return group, nil
}

// deleteGroup handles DELETE /api/groups/{id}
func (h *Handler) deleteGroup(ctx context.Context, req *events.LambdaFunctionURLRequest, groupID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(groupID); err != nil {
		return nil, err
	}

	if _, err := h.requireAdmin(ctx, req); err != nil {
		return nil, err
	}

	if err := h.auth.DeleteGroup(ctx, groupID); err != nil {
		return nil, err
	}

	return map[string]string{"status": "group deleted"}, nil
}
