package api //nolint:revive // var-naming: package name "api" is intentional for this handler package

import "time"

// CreateAPIKeyRequest represents a request to create a new API key.
type CreateAPIKeyRequest struct {
	ExpiresAt   *time.Time   `json:"expires_at,omitempty"`
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions,omitempty"`
}

// CreateAPIKeyResponse returns the newly created API key (only shown once).
type CreateAPIKeyResponse struct {
	Info   *APIKeyInfo `json:"info"`
	APIKey string      `json:"api_key"` //nolint:gosec // G117: HTTP redirect target is validated/trusted
	KeyID  string      `json:"key_id"`
}

// APIKeyInfo represents public information about an API key.
type APIKeyInfo struct { //nolint:revive // exported: doc comment style intentional
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	KeyPrefix   string       `json:"key_prefix"`
	ExpiresAt   string       `json:"expires_at,omitempty"`
	CreatedAt   string       `json:"created_at"`
	LastUsedAt  string       `json:"last_used_at,omitempty"`
	Permissions []Permission `json:"permissions,omitempty"`
	IsActive    bool         `json:"is_active"`
}

// toAPIPermissions returns a copy of perms as a new slice.
// The parameter is already the concrete type; the copy makes mutation-safe
// return values for callers that may hold the originating slice.
func toAPIPermissions(perms []Permission) []Permission {
	out := make([]Permission, len(perms))
	copy(out, perms)
	return out
}
