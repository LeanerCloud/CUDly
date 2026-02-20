package auth

import (
	"context"
	"fmt"
	"time"
)

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
func (s *Service) CreateAPIKeyAPI(ctx context.Context, userID string, req interface{}) (interface{}, error) {
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

// ListUserAPIKeysAPI lists all API keys for a user and returns API-friendly response
func (s *Service) ListUserAPIKeysAPI(ctx context.Context, userID string) (interface{}, error) {
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
