package auth

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// sortAPIKeysByActivity sorts in place by request_count_24h desc, with
// request_count_total desc as the tiebreaker. Exported as a helper so
// it can be unit-tested without going through the full service.
func sortAPIKeysByActivity(keys []*UserAPIKey) {
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i].RequestCount24h != keys[j].RequestCount24h {
			return keys[i].RequestCount24h > keys[j].RequestCount24h
		}
		return keys[i].RequestCountTotal > keys[j].RequestCountTotal
	})
}

// API wrapper methods for API key operations
// These methods return API-friendly types and handle type conversions

// APICreateAPIKeyRequest represents the API request to create an API key
type APICreateAPIKeyRequest struct {
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions,omitempty"`
	ExpiresAt   *time.Time   `json:"expires_at,omitempty"`
}

// APIKeyInfo represents public API key information (without sensitive data)
type APIKeyInfo struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	KeyPrefix   string       `json:"key_prefix"`
	Permissions []Permission `json:"permissions,omitempty"`
	ExpiresAt   *time.Time   `json:"expires_at,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	LastUsedAt  *time.Time   `json:"last_used_at,omitempty"`
	IsActive    bool         `json:"is_active"`
	// Usage counters (issue #344 deferred sub-task).
	RequestCountTotal int64 `json:"request_count_total"`
	RequestCount24h   int64 `json:"request_count_24h"`
}

// APIKeysUsageStatsTopKey is one entry in the top-keys list returned by
// GetAPIKeysUsageStatsAPI. Trimmed to identifier + 24h count so the UI
// doesn't have to re-derive anything from the full APIKeyInfo blob.
type APIKeysUsageStatsTopKey struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	KeyPrefix       string `json:"key_prefix"`
	RequestCount24h int64  `json:"request_count_24h"`
}

// APIKeysUsageStatsResponse is the summary payload for the API keys
// section header (issue #344 deferred sub-task). Scoped to the calling
// user's own keys — same scope as ListUserAPIKeysAPI.
type APIKeysUsageStatsResponse struct {
	TotalActive           int                       `json:"total_active"`
	TotalRequests24h      int64                     `json:"total_requests_24h"`
	TotalRequestsLifetime int64                     `json:"total_requests_lifetime"`
	TopKeys               []APIKeysUsageStatsTopKey `json:"top_keys"`
}

// APICreateAPIKeyResponse represents the API response for creating an API key
type APICreateAPIKeyResponse struct {
	APIKey string      `json:"api_key"` // Full key - only returned once
	KeyID  string      `json:"key_id"`
	Info   *APIKeyInfo `json:"info"`
}

// APIListAPIKeysResponse represents the API response for listing API keys
type APIListAPIKeysResponse struct {
	APIKeys []*APIKeyInfo `json:"api_keys"`
}

// CreateAPIKeyAPI creates a new API key and returns API-friendly response
func (s *Service) CreateAPIKeyAPI(ctx context.Context, userID string, req any) (any, error) {
	// Type assert the request
	createReq, ok := req.(APICreateAPIKeyRequest)
	if !ok {
		return nil, fmt.Errorf("invalid request type")
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
			ID:                keyInfo.ID,
			Name:              keyInfo.Name,
			KeyPrefix:         keyInfo.KeyPrefix,
			Permissions:       keyInfo.Permissions,
			ExpiresAt:         keyInfo.ExpiresAt,
			CreatedAt:         keyInfo.CreatedAt,
			LastUsedAt:        keyInfo.LastUsedAt,
			IsActive:          keyInfo.IsActive,
			RequestCountTotal: keyInfo.RequestCountTotal,
			RequestCount24h:   keyInfo.RequestCount24h,
		},
	}, nil
}

// ListUserAPIKeysAPI lists all API keys for a user and returns API-friendly response
func (s *Service) ListUserAPIKeysAPI(ctx context.Context, userID string) (any, error) {
	keys, err := s.ListUserAPIKeys(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Convert to API response
	apiKeys := make([]*APIKeyInfo, 0, len(keys))
	for _, key := range keys {
		apiKeys = append(apiKeys, &APIKeyInfo{
			ID:                key.ID,
			Name:              key.Name,
			KeyPrefix:         key.KeyPrefix,
			Permissions:       key.Permissions,
			ExpiresAt:         key.ExpiresAt,
			CreatedAt:         key.CreatedAt,
			LastUsedAt:        key.LastUsedAt,
			IsActive:          key.IsActive,
			RequestCountTotal: key.RequestCountTotal,
			RequestCount24h:   key.RequestCount24h,
		})
	}

	return &APIListAPIKeysResponse{
		APIKeys: apiKeys,
	}, nil
}

// GetAPIKeysUsageStatsAPI computes section-level usage stats for the
// calling user's own API keys. Aggregated in-process so we don't need
// a separate DB round-trip — ListUserAPIKeys already returns the
// per-key counters from migration 000051.
//
// The "top keys" list is sorted by request_count_24h descending, with
// total-lifetime as the tiebreaker so a long-running idle key doesn't
// outrank an active one with the same 24h count. Up to 3 entries are
// surfaced; the UI clarifies this is "top by 24h activity".
func (s *Service) GetAPIKeysUsageStatsAPI(ctx context.Context, userID string) (any, error) {
	keys, err := s.ListUserAPIKeys(ctx, userID)
	if err != nil {
		return nil, err
	}

	resp := &APIKeysUsageStatsResponse{
		TopKeys: []APIKeysUsageStatsTopKey{},
	}
	for _, k := range keys {
		if k.IsActive {
			resp.TotalActive++
		}
		resp.TotalRequests24h += k.RequestCount24h
		resp.TotalRequestsLifetime += k.RequestCountTotal
	}

	// Sort by 24h count desc, then lifetime desc.
	sorted := make([]*UserAPIKey, len(keys))
	copy(sorted, keys)
	sortAPIKeysByActivity(sorted)

	const topN = 3
	limit := topN
	if len(sorted) < limit {
		limit = len(sorted)
	}
	for i := 0; i < limit; i++ {
		k := sorted[i]
		// Skip keys with zero 24h activity — surfacing a "top key"
		// with 0 requests is confusing rather than informative.
		if k.RequestCount24h == 0 {
			break
		}
		resp.TopKeys = append(resp.TopKeys, APIKeysUsageStatsTopKey{
			ID:              k.ID,
			Name:            k.Name,
			KeyPrefix:       k.KeyPrefix,
			RequestCount24h: k.RequestCount24h,
		})
	}

	return resp, nil
}

// DeleteAPIKeyAPI deletes an API key
func (s *Service) DeleteAPIKeyAPI(ctx context.Context, userID, keyID string) error {
	return s.DeleteAPIKey(ctx, userID, keyID)
}

// RevokeAPIKeyAPI revokes an API key
func (s *Service) RevokeAPIKeyAPI(ctx context.Context, userID, keyID string) error {
	return s.RevokeAPIKey(ctx, userID, keyID)
}

// ValidateUserAPIKeyAPI validates a user API key and returns the key info and user
// This is the API-facing wrapper for ValidateUserAPIKey
func (s *Service) ValidateUserAPIKeyAPI(ctx context.Context, apiKey string) (*UserAPIKey, *User, error) {
	return s.ValidateUserAPIKey(ctx, apiKey)
}
