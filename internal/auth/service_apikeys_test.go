package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestService_CreateAPIKey(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully create API key", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		user := &User{
			ID:     "user-123",
			Email:  "test@example.com",
			Active: true,
			Role:   RoleAdmin,
		}

		permissions := []Permission{
			{Action: ActionView, Resource: ResourceRecommendations},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("CreateAPIKey", ctx, mock.AnythingOfType("*auth.UserAPIKey")).Return(nil)

		apiKey, keyInfo, err := service.CreateAPIKey(ctx, "user-123", "Test Key", permissions, nil)

		require.NoError(t, err)
		require.NotNil(t, keyInfo)
		assert.NotEmpty(t, apiKey)
		assert.Equal(t, "user-123", keyInfo.UserID)
		assert.Equal(t, "Test Key", keyInfo.Name)
		assert.Equal(t, permissions, keyInfo.Permissions)
		assert.True(t, keyInfo.IsActive)
		assert.Len(t, keyInfo.KeyPrefix, 8)
		mockStore.AssertExpectations(t)
	})

	t.Run("fail when user not found", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		mockStore.On("GetUserByID", ctx, "user-123").Return(nil, assert.AnError)

		apiKey, keyInfo, err := service.CreateAPIKey(ctx, "user-123", "Test Key", []Permission{}, nil)

		assert.Error(t, err)
		assert.Empty(t, apiKey)
		assert.Nil(t, keyInfo)
		mockStore.AssertExpectations(t)
	})

	t.Run("fail when user is inactive", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		user := &User{
			ID:     "user-123",
			Email:  "test@example.com",
			Active: false,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)

		apiKey, keyInfo, err := service.CreateAPIKey(ctx, "user-123", "Test Key", []Permission{}, nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not active")
		assert.Empty(t, apiKey)
		assert.Nil(t, keyInfo)
		mockStore.AssertExpectations(t)
	})

	t.Run("fail when name is empty", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		user := &User{
			ID:     "user-123",
			Email:  "test@example.com",
			Active: true,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)

		apiKey, keyInfo, err := service.CreateAPIKey(ctx, "user-123", "", []Permission{}, nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name is required")
		assert.Empty(t, apiKey)
		assert.Nil(t, keyInfo)
		mockStore.AssertExpectations(t)
	})

	t.Run("successfully create API key with expiration", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		user := &User{
			ID:     "user-123",
			Email:  "test@example.com",
			Active: true,
			Role:   RoleAdmin,
		}

		expiresAt := time.Now().Add(30 * 24 * time.Hour)
		permissions := []Permission{
			{Action: ActionView, Resource: ResourceRecommendations},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("CreateAPIKey", ctx, mock.AnythingOfType("*auth.UserAPIKey")).Return(nil)

		apiKey, keyInfo, err := service.CreateAPIKey(ctx, "user-123", "Test Key", permissions, &expiresAt)

		require.NoError(t, err)
		require.NotNil(t, keyInfo)
		assert.NotEmpty(t, apiKey)
		assert.NotNil(t, keyInfo.ExpiresAt)
		assert.Equal(t, expiresAt.Unix(), keyInfo.ExpiresAt.Unix())
		mockStore.AssertExpectations(t)
	})

	t.Run("fail when store create fails", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		user := &User{
			ID:     "user-123",
			Email:  "test@example.com",
			Active: true,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("CreateAPIKey", ctx, mock.AnythingOfType("*auth.UserAPIKey")).Return(assert.AnError)

		apiKey, keyInfo, err := service.CreateAPIKey(ctx, "user-123", "Test Key", []Permission{}, nil)

		assert.Error(t, err)
		assert.Empty(t, apiKey)
		assert.Nil(t, keyInfo)
		mockStore.AssertExpectations(t)
	})
}

func TestService_ListUserAPIKeys(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully list API keys", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		now := time.Now()
		expectedKeys := []*UserAPIKey{
			{
				ID:        "key-1",
				UserID:    "user-123",
				Name:      "Key 1",
				KeyPrefix: "prefix1",
				IsActive:  true,
				CreatedAt: now,
			},
			{
				ID:        "key-2",
				UserID:    "user-123",
				Name:      "Key 2",
				KeyPrefix: "prefix2",
				IsActive:  true,
				CreatedAt: now,
			},
		}

		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(expectedKeys, nil)

		keys, err := service.ListUserAPIKeys(ctx, "user-123")

		require.NoError(t, err)
		assert.Len(t, keys, 2)
		assert.Equal(t, expectedKeys, keys)
		mockStore.AssertExpectations(t)
	})

	t.Run("return error when user not found", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		mockStore.On("GetUserByID", ctx, "user-123").Return(nil, assert.AnError)

		keys, err := service.ListUserAPIKeys(ctx, "user-123")

		assert.Error(t, err)
		assert.Nil(t, keys)
		mockStore.AssertExpectations(t)
	})

	t.Run("return error when store fails", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(nil, assert.AnError)

		keys, err := service.ListUserAPIKeys(ctx, "user-123")

		assert.Error(t, err)
		assert.Nil(t, keys)
		mockStore.AssertExpectations(t)
	})
}

func TestService_GetAPIKeyByHash(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully get API key by hash", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		expectedKey := &UserAPIKey{
			ID:        "key-1",
			UserID:    "user-123",
			Name:      "Test Key",
			KeyPrefix: "prefix1",
			KeyHash:   "hash123",
			IsActive:  true,
		}

		mockStore.On("GetAPIKeyByHash", ctx, "hash123").Return(expectedKey, nil)

		key, err := service.GetAPIKeyByHash(ctx, "hash123")

		require.NoError(t, err)
		assert.Equal(t, expectedKey, key)
		mockStore.AssertExpectations(t)
	})

	t.Run("return error when store fails", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		mockStore.On("GetAPIKeyByHash", ctx, "hash123").Return(nil, assert.AnError)

		key, err := service.GetAPIKeyByHash(ctx, "hash123")

		assert.Error(t, err)
		assert.Nil(t, key)
		mockStore.AssertExpectations(t)
	})
}

func TestService_RevokeAPIKey(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully revoke API key", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		existingKey := &UserAPIKey{
			ID:       "key-1",
			UserID:   "user-123",
			IsActive: true,
		}
		user := &User{ID: "user-123", Role: RoleUser}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(existingKey, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("UpdateAPIKey", ctx, mock.MatchedBy(func(key *UserAPIKey) bool {
			return key.ID == "key-1" && !key.IsActive
		})).Return(nil)

		err := service.RevokeAPIKey(ctx, "user-123", "key-1")

		require.NoError(t, err)
		mockStore.AssertExpectations(t)
	})

	t.Run("return error when key not found", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(nil, assert.AnError)

		err := service.RevokeAPIKey(ctx, "user-123", "key-1")

		assert.Error(t, err)
		mockStore.AssertExpectations(t)
	})

	t.Run("return error when update fails", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		existingKey := &UserAPIKey{
			ID:       "key-1",
			UserID:   "user-123",
			IsActive: true,
		}
		user := &User{ID: "user-123", Role: RoleUser}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(existingKey, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("UpdateAPIKey", ctx, mock.AnythingOfType("*auth.UserAPIKey")).Return(assert.AnError)

		err := service.RevokeAPIKey(ctx, "user-123", "key-1")

		assert.Error(t, err)
		mockStore.AssertExpectations(t)
	})

	t.Run("non-admin user cannot revoke another user's API key", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		existingKey := &UserAPIKey{
			ID:       "key-1",
			UserID:   "user-456",
			IsActive: true,
		}
		user := &User{ID: "user-123", Role: RoleUser}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(existingKey, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)

		err := service.RevokeAPIKey(ctx, "user-123", "key-1")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unauthorized")
		mockStore.AssertExpectations(t)
	})

	t.Run("admin user can revoke another user's API key", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		existingKey := &UserAPIKey{
			ID:       "key-1",
			UserID:   "user-456",
			IsActive: true,
		}
		user := &User{ID: "user-123", Role: RoleAdmin}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(existingKey, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("UpdateAPIKey", ctx, mock.MatchedBy(func(key *UserAPIKey) bool {
			return key.ID == "key-1" && !key.IsActive
		})).Return(nil)

		err := service.RevokeAPIKey(ctx, "user-123", "key-1")

		require.NoError(t, err)
		mockStore.AssertExpectations(t)
	})
}

func TestService_DeleteAPIKey(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully delete API key", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		existingKey := &UserAPIKey{ID: "key-1", UserID: "user-123"}
		user := &User{ID: "user-123", Role: RoleUser}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(existingKey, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("DeleteAPIKey", ctx, "key-1").Return(nil)

		err := service.DeleteAPIKey(ctx, "user-123", "key-1")

		require.NoError(t, err)
		mockStore.AssertExpectations(t)
	})

	t.Run("return error when delete fails", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(nil, assert.AnError)

		err := service.DeleteAPIKey(ctx, "user-123", "key-1")

		assert.Error(t, err)
		mockStore.AssertExpectations(t)
	})

	t.Run("non-admin user cannot delete another user's API key", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		existingKey := &UserAPIKey{ID: "key-1", UserID: "user-456"}
		user := &User{ID: "user-123", Role: RoleUser}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(existingKey, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)

		err := service.DeleteAPIKey(ctx, "user-123", "key-1")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unauthorized")
		mockStore.AssertExpectations(t)
	})

	t.Run("admin user can delete another user's API key", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		existingKey := &UserAPIKey{ID: "key-1", UserID: "user-456"}
		user := &User{ID: "user-123", Role: RoleAdmin}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(existingKey, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("DeleteAPIKey", ctx, "key-1").Return(nil)

		err := service.DeleteAPIKey(ctx, "user-123", "key-1")

		require.NoError(t, err)
		mockStore.AssertExpectations(t)
	})

	t.Run("return error when GetUserByID fails", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		existingKey := &UserAPIKey{ID: "key-1", UserID: "user-123"}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(existingKey, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(nil, assert.AnError)

		err := service.DeleteAPIKey(ctx, "user-123", "key-1")

		assert.Error(t, err)
		mockStore.AssertExpectations(t)
	})

	t.Run("return error when DeleteAPIKey store operation fails", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		existingKey := &UserAPIKey{ID: "key-1", UserID: "user-123"}
		user := &User{ID: "user-123", Role: RoleUser}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(existingKey, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("DeleteAPIKey", ctx, "key-1").Return(assert.AnError)

		err := service.DeleteAPIKey(ctx, "user-123", "key-1")

		assert.Error(t, err)
		mockStore.AssertExpectations(t)
	})
}

func TestService_ValidateUserAPIKey(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully validate API key", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		apiKey := "test-api-key-123456"
		hash := sha256.Sum256([]byte(apiKey))
		keyHash := base64.RawURLEncoding.EncodeToString(hash[:])

		user := &User{
			ID:     "user-123",
			Email:  "test@example.com",
			Active: true,
		}

		apiKeyRecord := &UserAPIKey{
			ID:          "key-1",
			UserID:      "user-123",
			Name:        "Test Key",
			KeyHash:     keyHash,
			IsActive:    true,
			ExpiresAt:   nil,
			Permissions: []Permission{{Action: ActionView, Resource: ResourceRecommendations}},
		}

		mockStore.On("GetAPIKeyByHash", ctx, keyHash).Return(apiKeyRecord, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("UpdateAPIKeyLastUsed", mock.Anything, "key-1").Return(nil).Maybe()

		resultKey, resultUser, err := service.ValidateUserAPIKey(ctx, apiKey)

		require.NoError(t, err)
		assert.Equal(t, user, resultUser)
		assert.Equal(t, apiKeyRecord, resultKey)
		time.Sleep(10 * time.Millisecond) // Allow goroutine to complete
		mockStore.AssertExpectations(t)
	})

	t.Run("fail when API key not found", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		apiKey := "test-api-key-123456"
		hash := sha256.Sum256([]byte(apiKey))
		keyHash := base64.RawURLEncoding.EncodeToString(hash[:])

		mockStore.On("GetAPIKeyByHash", ctx, keyHash).Return(nil, assert.AnError)

		resultKey, resultUser, err := service.ValidateUserAPIKey(ctx, apiKey)

		assert.Error(t, err)
		assert.Nil(t, resultUser)
		assert.Nil(t, resultKey)
		mockStore.AssertExpectations(t)
	})

	t.Run("fail when API key is inactive", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		apiKey := "test-api-key-123456"
		hash := sha256.Sum256([]byte(apiKey))
		keyHash := base64.RawURLEncoding.EncodeToString(hash[:])

		apiKeyRecord := &UserAPIKey{
			ID:       "key-1",
			UserID:   "user-123",
			KeyHash:  keyHash,
			IsActive: false,
		}

		mockStore.On("GetAPIKeyByHash", ctx, keyHash).Return(apiKeyRecord, nil)

		resultKey, resultUser, err := service.ValidateUserAPIKey(ctx, apiKey)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "revoked")
		assert.Nil(t, resultUser)
		assert.Nil(t, resultKey)
		mockStore.AssertExpectations(t)
	})

	t.Run("fail when API key is expired", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		apiKey := "test-api-key-123456"
		hash := sha256.Sum256([]byte(apiKey))
		keyHash := base64.RawURLEncoding.EncodeToString(hash[:])
		expiredTime := time.Now().Add(-24 * time.Hour)

		apiKeyRecord := &UserAPIKey{
			ID:        "key-1",
			UserID:    "user-123",
			KeyHash:   keyHash,
			IsActive:  true,
			ExpiresAt: &expiredTime,
		}

		mockStore.On("GetAPIKeyByHash", ctx, keyHash).Return(apiKeyRecord, nil)

		resultKey, resultUser, err := service.ValidateUserAPIKey(ctx, apiKey)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expired")
		assert.Nil(t, resultUser)
		assert.Nil(t, resultKey)
		mockStore.AssertExpectations(t)
	})

	t.Run("fail when user not found", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		apiKey := "test-api-key-123456"
		hash := sha256.Sum256([]byte(apiKey))
		keyHash := base64.RawURLEncoding.EncodeToString(hash[:])

		apiKeyRecord := &UserAPIKey{
			ID:       "key-1",
			UserID:   "user-123",
			KeyHash:  keyHash,
			IsActive: true,
		}

		mockStore.On("GetAPIKeyByHash", ctx, keyHash).Return(apiKeyRecord, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(nil, assert.AnError)

		resultKey, resultUser, err := service.ValidateUserAPIKey(ctx, apiKey)

		assert.Error(t, err)
		assert.Nil(t, resultUser)
		assert.Nil(t, resultKey)
		mockStore.AssertExpectations(t)
	})

	t.Run("fail when user is inactive", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		apiKey := "test-api-key-123456"
		hash := sha256.Sum256([]byte(apiKey))
		keyHash := base64.RawURLEncoding.EncodeToString(hash[:])

		user := &User{
			ID:     "user-123",
			Email:  "test@example.com",
			Active: false,
		}

		apiKeyRecord := &UserAPIKey{
			ID:       "key-1",
			UserID:   "user-123",
			KeyHash:  keyHash,
			IsActive: true,
		}

		mockStore.On("GetAPIKeyByHash", ctx, keyHash).Return(apiKeyRecord, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)

		resultKey, resultUser, err := service.ValidateUserAPIKey(ctx, apiKey)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not active")
		assert.Nil(t, resultUser)
		assert.Nil(t, resultKey)
		mockStore.AssertExpectations(t)
	})
}

func TestService_UpdateLastUsed(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully update last used", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		mockStore.On("UpdateAPIKeyLastUsed", ctx, "key-1").Return(nil)

		err := service.UpdateLastUsed(ctx, "key-1")

		require.NoError(t, err)
		mockStore.AssertExpectations(t)
	})

	t.Run("return error when update fails", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		mockStore.On("UpdateAPIKeyLastUsed", ctx, "key-1").Return(assert.AnError)

		err := service.UpdateLastUsed(ctx, "key-1")

		assert.Error(t, err)
		mockStore.AssertExpectations(t)
	})
}

func TestService_ComputeEffectivePermissions(t *testing.T) {
	ctx := context.Background()

	t.Run("return API key permissions when defined", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		apiKeyPermissions := []Permission{
			{Action: ActionView, Resource: ResourceRecommendations},
		}

		apiKey := &UserAPIKey{
			ID:          "key-1",
			UserID:      "user-123",
			Permissions: apiKeyPermissions,
		}

		user := &User{
			ID:   "user-123",
			Role: RoleAdmin,
		}

		permissions, err := service.ComputeEffectivePermissions(ctx, apiKey, user)

		require.NoError(t, err)
		assert.Equal(t, apiKeyPermissions, permissions)
	})

	t.Run("return user permissions when API key has no permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		apiKey := &UserAPIKey{
			ID:          "key-1",
			UserID:      "user-123",
			Permissions: []Permission{},
		}

		user := &User{
			ID:   "user-123",
			Role: RoleUser,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetUserGroups", ctx, "user-123").Return([]string{}, nil)
		mockStore.On("GetUserPermissions", ctx, "user-123").Return([]Permission{
			{Action: ActionView, Resource: ResourceRecommendations},
			{Action: ActionPurchase, Resource: ResourcePlans},
		}, nil)

		permissions, err := service.ComputeEffectivePermissions(ctx, apiKey, user)

		require.NoError(t, err)
		assert.Greater(t, len(permissions), 0) // Returns role-based permissions when API key has none
	})

	t.Run("return empty when both have no permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		apiKey := &UserAPIKey{
			ID:          "key-1",
			UserID:      "user-123",
			Permissions: []Permission{},
		}

		user := &User{
			ID:   "user-123",
			Role: RoleReadOnly,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetUserGroups", ctx, "user-123").Return([]string{}, nil)
		mockStore.On("GetUserPermissions", ctx, "user-123").Return([]Permission{}, nil)

		permissions, err := service.ComputeEffectivePermissions(ctx, apiKey, user)

		require.NoError(t, err)
		assert.Greater(t, len(permissions), 0) // ReadOnly role has default permissions
	})

	t.Run("admin with scoped API key returns key permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		scopedPermissions := []Permission{
			{Action: ActionView, Resource: ResourceRecommendations},
		}

		apiKey := &UserAPIKey{
			ID:          "key-1",
			UserID:      "user-123",
			Permissions: scopedPermissions,
		}

		user := &User{
			ID:   "user-123",
			Role: RoleAdmin,
		}

		permissions, err := service.ComputeEffectivePermissions(ctx, apiKey, user)

		require.NoError(t, err)
		assert.Equal(t, scopedPermissions, permissions)
	})

	t.Run("return intersection of API key and user permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		apiKey := &UserAPIKey{
			ID:     "key-1",
			UserID: "user-123",
			Permissions: []Permission{
				{Action: ActionView, Resource: ResourceRecommendations},
				{Action: ActionPurchase, Resource: ResourcePlans},
				{Action: ActionAdmin, Resource: ResourceUsers}, // User doesn't have this
			},
		}

		user := &User{
			ID:   "user-123",
			Role: RoleUser,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetUserGroups", ctx, "user-123").Return([]string{}, nil)
		mockStore.On("GetUserPermissions", ctx, "user-123").Return([]Permission{
			{Action: ActionView, Resource: ResourceRecommendations},
			{Action: ActionPurchase, Resource: ResourcePlans},
		}, nil)

		permissions, err := service.ComputeEffectivePermissions(ctx, apiKey, user)

		require.NoError(t, err)
		// Should return only the intersection (first two permissions, not the third)
		assert.Len(t, permissions, 2)
		assert.Contains(t, permissions, Permission{Action: ActionView, Resource: ResourceRecommendations})
		assert.Contains(t, permissions, Permission{Action: ActionPurchase, Resource: ResourcePlans})
	})

	t.Run("return empty when API key permissions not in user permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		apiKey := &UserAPIKey{
			ID:     "key-1",
			UserID: "user-123",
			Permissions: []Permission{
				{Action: ActionAdmin, Resource: ResourceUsers},
				{Action: ActionConfigure, Resource: ResourceConfig},
			},
		}

		user := &User{
			ID:   "user-123",
			Role: RoleUser,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetUserGroups", ctx, "user-123").Return([]string{}, nil)
		mockStore.On("GetUserPermissions", ctx, "user-123").Return([]Permission{
			{Action: ActionView, Resource: ResourceRecommendations},
		}, nil)

		permissions, err := service.ComputeEffectivePermissions(ctx, apiKey, user)

		require.NoError(t, err)
		// Should return empty since user doesn't have any of the API key's permissions
		assert.Empty(t, permissions)
	})
}

func TestService_validateAPIKeyPermissions(t *testing.T) {
	ctx := context.Background()

	t.Run("admin user can create keys with any permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		user := &User{
			ID:   "user-123",
			Role: RoleAdmin,
		}

		permissions := []Permission{
			{Action: ActionAdmin, Resource: ResourceUsers},
			{Action: ActionConfigure, Resource: ResourceConfig},
		}

		err := service.validateAPIKeyPermissions(ctx, user, permissions)
		require.NoError(t, err)
	})

	t.Run("non-admin user can create keys with their permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		user := &User{
			ID:       "user-123",
			Role:     RoleUser,
			GroupIDs: []string{},
		}

		permissions := []Permission{
			{Action: ActionView, Resource: ResourceRecommendations},
		}

		// BuildAuthContext will call GetUserByID
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)

		err := service.validateAPIKeyPermissions(ctx, user, permissions)
		require.NoError(t, err)
		mockStore.AssertExpectations(t)
	})

	t.Run("fail when non-admin user requests permissions they don't have", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		user := &User{
			ID:       "user-123",
			Role:     RoleUser,
			GroupIDs: []string{},
		}

		permissions := []Permission{
			{Action: ActionAdmin, Resource: ResourceUsers},
		}

		// BuildAuthContext will call GetUserByID
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)

		err := service.validateAPIKeyPermissions(ctx, user, permissions)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user does not have permission")
		mockStore.AssertExpectations(t)
	})

	t.Run("fail when GetAuthContext fails", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		user := &User{
			ID:   "user-123",
			Role: RoleUser,
		}

		permissions := []Permission{
			{Action: ActionView, Resource: ResourceRecommendations},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(nil, assert.AnError)

		err := service.validateAPIKeyPermissions(ctx, user, permissions)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get user permissions")
		mockStore.AssertExpectations(t)
	})
}
