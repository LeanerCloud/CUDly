package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateAPIKey creates a new user API key with scoped permissions
// Returns the full API key (shown only once), key info, and error
func (s *Service) CreateAPIKey(ctx context.Context, userID, name string, permissions []Permission, expiresAt *time.Time) (string, *UserAPIKey, error) {
	// Validate user exists and is active
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, fmt.Errorf("user not found")
		}
		return "", nil, fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return "", nil, fmt.Errorf("user not found")
	}
	if !user.Active {
		return "", nil, fmt.Errorf("user account is not active")
	}

	// Validate name
	if name == "" {
		return "", nil, fmt.Errorf("API key name is required")
	}

	// Generate a secure random key (32 bytes = 256 bits)
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return "", nil, fmt.Errorf("failed to generate random key: %w", err)
	}

	// Base64 encode to create the API key
	apiKey := base64.RawURLEncoding.EncodeToString(keyBytes)

	// Compute SHA-256 hash of the key for storage
	hash := sha256.Sum256([]byte(apiKey))
	keyHash := base64.RawURLEncoding.EncodeToString(hash[:])

	// Extract key prefix (first 8 chars) for display
	keyPrefix := apiKey[:8]

	// Validate permissions - ensure they don't exceed user's permissions
	if err := s.validateAPIKeyPermissions(ctx, user, permissions); err != nil {
		return "", nil, fmt.Errorf("invalid permissions: %w", err)
	}

	// Create UserAPIKey record
	now := time.Now()
	keyID := uuid.New().String()

	userAPIKey := &UserAPIKey{
		ID:          keyID,
		UserID:      userID,
		Name:        name,
		KeyPrefix:   keyPrefix,
		KeyHash:     keyHash,
		Permissions: permissions,
		ExpiresAt:   expiresAt,
		CreatedAt:   now,
		LastUsedAt:  nil,
		IsActive:    true,
	}

	// Store the API key
	if err := s.store.CreateAPIKey(ctx, userAPIKey); err != nil {
		return "", nil, fmt.Errorf("failed to create API key: %w", err)
	}

	logging.Infof("Created API key %s for user %s", keyPrefix, userID)

	return apiKey, userAPIKey, nil
}

// validateAPIKeyPermissions ensures the key's permissions don't exceed the user's permissions
func (s *Service) validateAPIKeyPermissions(ctx context.Context, user *User, permissions []Permission) error {
	// Admin users can create keys with any permissions
	if user.Role == RoleAdmin {
		return nil
	}

	// Get user's auth context to check their permissions
	authCtx, err := s.GetAuthContext(ctx, user.ID)
	if err != nil {
		return fmt.Errorf("failed to get user permissions: %w", err)
	}

	// Validate each requested permission
	for _, perm := range permissions {
		if !authCtx.HasPermission(perm.Action, perm.Resource) {
			return fmt.Errorf("user does not have permission for action=%s resource=%s", perm.Action, perm.Resource)
		}
	}

	return nil
}

// ListUserAPIKeys retrieves all API keys for a user
func (s *Service) ListUserAPIKeys(ctx context.Context, userID string) ([]*UserAPIKey, error) {
	// Validate user exists
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}

	keys, err := s.store.ListAPIKeysByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list API keys: %w", err)
	}

	return keys, nil
}

// GetAPIKeyByHash retrieves an API key by its hash (for authentication)
func (s *Service) GetAPIKeyByHash(ctx context.Context, keyHash string) (*UserAPIKey, error) {
	key, err := s.store.GetAPIKeyByHash(ctx, keyHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // treat not-found as nil key; callers check for nil
		}
		return nil, fmt.Errorf("failed to get API key: %w", err)
	}

	return key, nil
}

// RevokeAPIKey deactivates an API key (soft delete)
func (s *Service) RevokeAPIKey(ctx context.Context, userID, keyID string) error {
	// Get the key to verify ownership
	key, err := s.store.GetAPIKeyByID(ctx, keyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("API key not found")
		}
		return fmt.Errorf("failed to get API key: %w", err)
	}
	if key == nil {
		return fmt.Errorf("API key not found")
	}

	// Verify ownership (unless admin)
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("user not found")
		}
		return fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return fmt.Errorf("user not found")
	}

	if key.UserID != userID && user.Role != RoleAdmin {
		return fmt.Errorf("unauthorized: cannot revoke another user's API key")
	}

	// Revoke the key
	key.IsActive = false
	if err := s.store.UpdateAPIKey(ctx, key); err != nil {
		return fmt.Errorf("failed to revoke API key: %w", err)
	}

	logging.Infof("Revoked API key %s for user %s", key.KeyPrefix, key.UserID)

	return nil
}

// DeleteAPIKey permanently deletes an API key
func (s *Service) DeleteAPIKey(ctx context.Context, userID, keyID string) error {
	// Get the key to verify ownership
	key, err := s.store.GetAPIKeyByID(ctx, keyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("API key not found")
		}
		return fmt.Errorf("failed to get API key: %w", err)
	}
	if key == nil {
		return fmt.Errorf("API key not found")
	}

	// Verify ownership (unless admin)
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("user not found")
		}
		return fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return fmt.Errorf("user not found")
	}

	if key.UserID != userID && user.Role != RoleAdmin {
		return fmt.Errorf("unauthorized: cannot delete another user's API key")
	}

	// Delete the key
	if err := s.store.DeleteAPIKey(ctx, keyID); err != nil {
		return fmt.Errorf("failed to delete API key: %w", err)
	}

	logging.Infof("Deleted API key %s for user %s", key.KeyPrefix, key.UserID)

	return nil
}

// validateAPIKeyStatus checks that the key is active and not expired.
func validateAPIKeyStatus(key *UserAPIKey) error {
	if !key.IsActive {
		return fmt.Errorf("API key is revoked")
	}
	if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
		return fmt.Errorf("API key has expired")
	}
	return nil
}

// lookupAPIKeyUser retrieves and validates the user associated with an API key.
func (s *Service) lookupAPIKeyUser(ctx context.Context, userID string) (*User, error) {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user account not found")
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("user account not found")
	}
	if !user.Active {
		return nil, fmt.Errorf("user account is not active")
	}
	return user, nil
}

// ValidateUserAPIKey validates an API key and returns the key info and associated user
func (s *Service) ValidateUserAPIKey(ctx context.Context, apiKey string) (*UserAPIKey, *User, error) {
	hash := sha256.Sum256([]byte(apiKey))
	keyHash := base64.RawURLEncoding.EncodeToString(hash[:])

	key, err := s.GetAPIKeyByHash(ctx, keyHash)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to validate API key: %w", err)
	}
	if key == nil {
		return nil, nil, fmt.Errorf("invalid API key")
	}

	if err := validateAPIKeyStatus(key); err != nil {
		return nil, nil, err
	}

	user, err := s.lookupAPIKeyUser(ctx, key.UserID)
	if err != nil {
		return nil, nil, err
	}

	// Update last used timestamp (async to avoid blocking)
	go func() {
		updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.UpdateLastUsed(updateCtx, key.ID); err != nil {
			logging.Warnf("Failed to update API key last used timestamp: %v", err)
		}
	}()

	return key, user, nil
}

// UpdateLastUsed updates the last used timestamp for an API key atomically
func (s *Service) UpdateLastUsed(ctx context.Context, keyID string) error {
	return s.store.UpdateAPIKeyLastUsed(ctx, keyID)
}

// ComputeEffectivePermissions computes the intersection of API key permissions and user permissions
// This ensures an API key cannot grant more permissions than the user has
func (s *Service) ComputeEffectivePermissions(ctx context.Context, apiKey *UserAPIKey, user *User) ([]Permission, error) {
	// Admin users always have full permissions
	if user.Role == RoleAdmin {
		// If API key has specific permissions, use those (scoped admin key)
		if len(apiKey.Permissions) > 0 {
			return apiKey.Permissions, nil
		}
		// Otherwise return full admin permissions
		return DefaultAdminPermissions(), nil
	}

	// Get user's auth context
	authCtx, err := s.GetAuthContext(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user auth context: %w", err)
	}

	// If API key has no specific permissions, use user's permissions
	if len(apiKey.Permissions) == 0 {
		return authCtx.Permissions, nil
	}

	// Compute intersection: only permissions the user has AND the key has
	effectivePerms := []Permission{}
	for _, keyPerm := range apiKey.Permissions {
		if authCtx.HasPermission(keyPerm.Action, keyPerm.Resource) {
			effectivePerms = append(effectivePerms, keyPerm)
		}
	}

	return effectivePerms, nil
}
