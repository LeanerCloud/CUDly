package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// API wrapper methods for API key operations
// These methods return API-friendly types and handle type conversions

// APICreateAPIKeyRequest represents the API request to create an API key.
type APICreateAPIKeyRequest struct {
	ExpiresAt   *time.Time   `json:"expires_at,omitempty"`
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions,omitempty"`
}

// APIKeyInfo represents public API key information (without sensitive data).
type APIKeyInfo struct {
	CreatedAt   time.Time    `json:"created_at"`
	ExpiresAt   *time.Time   `json:"expires_at,omitempty"`
	LastUsedAt  *time.Time   `json:"last_used_at,omitempty"`
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	KeyPrefix   string       `json:"key_prefix"`
	Permissions []Permission `json:"permissions,omitempty"`
	IsActive    bool         `json:"is_active"`
}

// APICreateAPIKeyResponse represents the API response for creating an API key.
type APICreateAPIKeyResponse struct {
	Info   *APIKeyInfo `json:"info"`
	APIKey string      `json:"api_key"` //nolint:gosec // G117: intentional one-time raw API-key response -- the key is returned to its owner exactly once on creation and is never re-stored or re-serialized
	KeyID  string      `json:"key_id"`
}

// APIListAPIKeysResponse represents the API response for listing API keys.
type APIListAPIKeysResponse struct {
	APIKeys []*APIKeyInfo `json:"api_keys"`
}

// CreateAPIKeyAPI creates a new API key and returns API-friendly response.
//
// req may be any struct whose JSON representation is compatible with
// APICreateAPIKeyRequest (name, expires_at, permissions). The handler lives
// in a different package (api) and passes api.CreateAPIKeyRequest, which
// carries the same JSON fields but is a distinct Go type. A direct type
// assertion would always fail and return a 500; JSON re-encoding handles any
// caller-package type transparently (issue #1440).
func (s *Service) CreateAPIKeyAPI(ctx context.Context, userID string, req any) (any, error) {
	var createReq APICreateAPIKeyRequest
	switch r := req.(type) {
	case APICreateAPIKeyRequest:
		// Fast path: caller already supplies our concrete type (service tests,
		// future callers that import this package directly).
		createReq = r
	default:
		// Cross-package path: re-encode to JSON and back so any struct with
		// compatible json tags (e.g. api.CreateAPIKeyRequest) is accepted.
		b, err := json.Marshal(r)
		if err != nil {
			return nil, fmt.Errorf("invalid request: %w", err)
		}
		if err := json.Unmarshal(b, &createReq); err != nil {
			return nil, fmt.Errorf("invalid request: %w", err)
		}
	}

	// Create the API key
	apiKey, keyInfo, err := s.CreateAPIKey(ctx, userID, createReq.Name, createReq.Permissions, createReq.ExpiresAt)
	if err != nil {
		return nil, err
	}

	// Convert to API response
	return &APICreateAPIKeyResponse{
		APIKey: apiKey,
		KeyID:  keyInfo.ID,
		Info: &APIKeyInfo{
			ID:          keyInfo.ID,
			Name:        keyInfo.Name,
			KeyPrefix:   keyInfo.KeyPrefix,
			Permissions: keyInfo.Permissions,
			ExpiresAt:   keyInfo.ExpiresAt,
			CreatedAt:   keyInfo.CreatedAt,
			LastUsedAt:  keyInfo.LastUsedAt,
			IsActive:    keyInfo.IsActive,
		},
	}, nil
}

// ListUserAPIKeysAPI lists all API keys for a user and returns API-friendly response.
func (s *Service) ListUserAPIKeysAPI(ctx context.Context, userID string) (any, error) {
	keys, err := s.ListUserAPIKeys(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Convert to API response
	apiKeys := make([]*APIKeyInfo, 0, len(keys))
	for _, key := range keys {
		apiKeys = append(apiKeys, &APIKeyInfo{
			ID:          key.ID,
			Name:        key.Name,
			KeyPrefix:   key.KeyPrefix,
			Permissions: key.Permissions,
			ExpiresAt:   key.ExpiresAt,
			CreatedAt:   key.CreatedAt,
			LastUsedAt:  key.LastUsedAt,
			IsActive:    key.IsActive,
		})
	}

	return &APIListAPIKeysResponse{
		APIKeys: apiKeys,
	}, nil
}

// DeleteAPIKeyAPI deletes an API key.
func (s *Service) DeleteAPIKeyAPI(ctx context.Context, userID, keyID string) error {
	return s.DeleteAPIKey(ctx, userID, keyID)
}

// RevokeAPIKeyAPI revokes an API key.
func (s *Service) RevokeAPIKeyAPI(ctx context.Context, userID, keyID string) error {
	return s.RevokeAPIKey(ctx, userID, keyID)
}

// ValidateUserAPIKeyAPI validates a user API key and returns the key info and user
// This is the API-facing wrapper for ValidateUserAPIKey.
func (s *Service) ValidateUserAPIKeyAPI(ctx context.Context, apiKey string) (*UserAPIKey, *User, error) {
	return s.ValidateUserAPIKey(ctx, apiKey)
}

// HasAPIKeyPermissionAPI validates a user API key and checks the requested
// action/resource against the key's effective permissions: the intersection
// of the key's scoped permissions with the owning user's group-derived
// permissions (ComputeEffectivePermissions). A key created without explicit
// permissions inherits the owner's full permission set. Returns the owning
// user's ID, the key's database ID, and whether the permission is held.
// The key ID is threaded to callers so they can pass it to
// HasAPIKeyPermissionForConstraintsAPI without a redundant DB lookup.
//
// Fail closed: any validation failure (unknown, revoked, or expired key,
// inactive owner) or permission-lookup error returns a non-nil error and
// callers must deny access.
func (s *Service) HasAPIKeyPermissionAPI(ctx context.Context, apiKey, action, resource string) (userID, keyID string, allowed bool, err error) {
	key, user, err := s.ValidateUserAPIKey(ctx, apiKey)
	if err != nil {
		return "", "", false, err
	}

	perms, err := s.ComputeEffectivePermissions(ctx, key, user)
	if err != nil {
		return "", "", false, fmt.Errorf("computing effective API key permissions: %w", err)
	}

	// AuthContext.HasPermission applies the same matching semantics as
	// session-based checks, including the admin:* wildcard with its
	// money-spending carve-outs (issue #923).
	effective := &AuthContext{User: user, Permissions: perms}
	return user.ID, key.ID, effective.HasPermission(action, resource), nil
}

// HasAPIKeyPermissionForConstraintsAPI checks request-derived permission
// constraint sets against a user API key's effective permissions (the
// intersection of the key's own permissions with the owning user's
// group-derived permissions). Callers must have already confirmed the key
// grants action/resource via HasAPIKeyPermissionAPI; this enforces the
// Constraints dimension (MaxPurchaseAmount, Providers, Services, Regions,
// AccountIDs) at execution time for user-API-key sessions (adversarial-review
// F2, issue #1141 extension).
//
// Fail closed: an empty constraintSets slice is a caller bug; any DB lookup
// failure returns an error and callers must deny.
func (s *Service) HasAPIKeyPermissionForConstraintsAPI(ctx context.Context, keyID, userID, action, resource string, constraintSets []PermissionConstraints) (bool, error) {
	if len(constraintSets) == 0 {
		return false, fmt.Errorf("no permission constraint sets provided for %s on %s", action, resource)
	}
	key, err := s.store.GetAPIKeyByID(ctx, keyID)
	if err != nil {
		return false, fmt.Errorf("failed to load API key for constraint check: %w", err)
	}
	if key == nil {
		return false, fmt.Errorf("API key not found")
	}
	// lookupAPIKeyUser also verifies that the user is still active.
	user, err := s.lookupAPIKeyUser(ctx, userID)
	if err != nil {
		return false, err
	}
	// Fetch the owner's auth context once; it is used for two purposes:
	//   1. Computing the key's effective permissions (key ∩ owner at action/resource level)
	//   2. Independently enforcing the owner's group constraint limits.
	// This prevents a key whose MaxPurchaseAmount (or other constraint) exceeds the owner's
	// group limit from authorizing more than the owner's group allows (CR finding).
	ownerAuthCtx, err := s.GetAuthContext(ctx, user.ID)
	if err != nil {
		return false, fmt.Errorf("failed to get owner auth context for constraint check: %w", err)
	}
	perms := computeEffectivePermissionsFromAuthCtx(key, ownerAuthCtx)
	// Each constraint set must independently pass both gates:
	//   - The key's effective permissions (key's constraint limits, e.g. MaxPurchaseAmount).
	//   - The owner's group permissions (owner's constraint limits).
	for i := range constraintSets {
		if !s.permissionsAllow(perms, action, resource, &constraintSets[i]) {
			return false, nil
		}
		if !s.permissionsAllow(ownerAuthCtx.Permissions, action, resource, &constraintSets[i]) {
			return false, nil
		}
	}
	return true, nil
}
