package api

import "time"

// CreateAPIKeyRequest represents a request to create a new API key.
type CreateAPIKeyRequest struct {
	ExpiresAt   *time.Time   `json:"expires_at,omitempty"`
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions,omitempty"`
}

// CreateAPIKeyResponse returns the newly created API key (only shown once).
type CreateAPIKeyResponse struct {
	APIKey string   `json:"api_key"` //nolint:gosec
	Info   *KeyInfo `json:"info"`
	KeyID  string   `json:"key_id"`
}

// KeyInfo represents public information about an API key.
type KeyInfo struct {
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
