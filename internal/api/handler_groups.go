// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/aws/aws-lambda-go/events"
)

// Group management handlers

// listGroups handles GET /api/groups.
func (h *Handler) listGroups(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	if _, err := h.requirePermission(ctx, req, "view", "groups"); err != nil {
		return nil, err
	}

	groups, err := h.auth.ListGroupsAPI(ctx)
	if err != nil {
		return nil, err
	}

	return map[string]any{"groups": groups}, nil
}

// createGroup handles POST /api/groups.
func (h *Handler) createGroup(ctx context.Context, req *events.LambdaFunctionURLRequest) (any, error) {
	session, err := h.requirePermission(ctx, req, "create", "groups")
	if err != nil {
		return nil, err
	}

	// Rate limiting: 30 admin operations per user per minute
	if h.rateLimiter != nil {
		allowed, rateLimitErr := h.rateLimiter.AllowWithUser(ctx, session.UserID, "admin")
		if rateLimitErr != nil {
			logging.Warnf("rate limiter error on admin operation (user %s): %v", session.UserID, rateLimitErr)
		} else if !allowed {
			return nil, NewClientError(429, "too many requests, please slow down")
		}
	}

	var createReq auth.APICreateGroupRequest
	err = json.Unmarshal([]byte(req.Body), &createReq)
	if err != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	group, err := h.auth.CreateGroupAPI(ctx, session.UserID, createReq)
	if err != nil {
		return nil, mapGroupAuthError(err)
	}

	return group, nil
}

// mapGroupAuthError maps the group-write sentinels to 403 ClientErrors so a
// refused grant surfaces the specific permission that was refused instead of
// a generic 500. Kept separate from mapAuthError (handler_users.go) because
// that switch is already at the cyclomatic limit and the two sentinel sets
// are disjoint. Unrecognized errors pass through for handleRequestError.
func mapGroupAuthError(err error) error {
	switch {
	case errors.Is(err, auth.ErrInvalidPermission):
		// Malformed input, not an authorization failure.
		return NewClientError(400, err.Error())
	case errors.Is(err, auth.ErrPermissionCeiling),
		errors.Is(err, auth.ErrPermissionNotGrantable),
		errors.Is(err, auth.ErrSystemManagedGroup):
		return NewClientError(403, err.Error())
	}
	return err
}

// getGroup handles GET /api/groups/{id}.
func (h *Handler) getGroup(ctx context.Context, req *events.LambdaFunctionURLRequest, groupID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(groupID); err != nil {
		return nil, err
	}

	if _, err := h.requirePermission(ctx, req, "view", "groups"); err != nil {
		return nil, err
	}

	group, err := h.auth.GetGroupAPI(ctx, groupID)
	if err != nil {
		return nil, err
	}

	return group, nil
}

// updateGroup handles PUT /api/groups/{id}.
func (h *Handler) updateGroup(ctx context.Context, req *events.LambdaFunctionURLRequest, groupID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(groupID); err != nil {
		return nil, err
	}

	session, err := h.requirePermission(ctx, req, "update", "groups")
	if err != nil {
		return nil, err
	}

	var updateReq auth.APIUpdateGroupRequest
	if unmarshalErr := json.Unmarshal([]byte(req.Body), &updateReq); unmarshalErr != nil {
		return nil, NewClientError(400, "invalid request body")
	}

	group, err := h.auth.UpdateGroupAPI(ctx, session.UserID, groupID, updateReq)
	if err != nil {
		return nil, mapGroupAuthError(err)
	}

	return group, nil
}

// deleteGroup handles DELETE /api/groups/{id}.
func (h *Handler) deleteGroup(ctx context.Context, req *events.LambdaFunctionURLRequest, groupID string) (any, error) {
	// Validate UUID format to prevent injection attacks
	if err := validateUUID(groupID); err != nil {
		return nil, err
	}

	if _, err := h.requirePermission(ctx, req, "delete", "groups"); err != nil {
		return nil, err
	}

	if err := h.auth.DeleteGroup(ctx, groupID); err != nil {
		return nil, mapGroupAuthError(err)
	}

	return map[string]string{"status": "group deleted"}, nil
}
