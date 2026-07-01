package apihttp

import "time"

// CreateAPIKeyRequest represents a request to create a new API key
type CreateAPIKeyRequest struct {
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions,omitempty"`
	ExpiresAt   *time.Time   `json:"expires_at,omitempty"`
}

// CreateAPIKeyResponse returns the newly created API key (only shown once)
type CreateAPIKeyResponse struct {
	APIKey string      `json:"api_key"` // Full key - only returned on creation
	KeyID  string      `json:"key_id"`
	Info   *APIKeyInfo `json:"info"`
}

// APIKeyInfo represents public information about an API key
type APIKeyInfo struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	KeyPrefix   string       `json:"key_prefix"` // First 8 chars for display
	Permissions []Permission `json:"permissions,omitempty"`
	ExpiresAt   string       `json:"expires_at,omitempty"`
	CreatedAt   string       `json:"created_at"`
	LastUsedAt  string       `json:"last_used_at,omitempty"`
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
