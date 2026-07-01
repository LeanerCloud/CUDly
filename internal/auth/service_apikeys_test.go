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
			ID:       "user-123",
			Email:    "test@example.com",
			Active:   true,
			GroupIDs: []string{DefaultAdminGroupID},
		}

		permissions := []Permission{
			{Action: ActionView, Resource: ResourceRecommendations},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(&Group{
			ID:          DefaultAdminGroupID,
			Permissions: []Permission{{Action: ActionAdmin, Resource: ResourceAll}},
		}, nil)
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
			ID:       "user-123",
			Email:    "test@example.com",
			Active:   true,
			GroupIDs: []string{DefaultAdminGroupID},
		}

		expiresAt := time.Now().Add(30 * 24 * time.Hour)
		permissions := []Permission{
			{Action: ActionView, Resource: ResourceRecommendations},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(&Group{
			ID:          DefaultAdminGroupID,
			Permissions: []Permission{{Action: ActionAdmin, Resource: ResourceAll}},
		}, nil)
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
		user := &User{ID: "user-123"}

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
		user := &User{ID: "user-123"}

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
		user := &User{ID: "user-123"}

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
		// Admin == member of the Administrators group ({admin, *}).
		user := &User{ID: "user-123", GroupIDs: []string{DefaultAdminGroupID}}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(existingKey, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(&Group{
			ID:          DefaultAdminGroupID,
			Permissions: []Permission{{Action: ActionAdmin, Resource: ResourceAll}},
		}, nil)
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
		user := &User{ID: "user-123"}

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
		user := &User{ID: "user-123"}

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
		// Admin == member of the Administrators group ({admin, *}).
		user := &User{ID: "user-123", GroupIDs: []string{DefaultAdminGroupID}}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(existingKey, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(&Group{
			ID:          DefaultAdminGroupID,
			Permissions: []Permission{{Action: ActionAdmin, Resource: ResourceAll}},
		}, nil)
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
		user := &User{ID: "user-123"}

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

	// adminGrp / userGrp are reused across sub-cases. Permissions derive
	// purely from group membership now (issue #907).
	adminGrp := func() *Group {
		return &Group{ID: DefaultAdminGroupID, Permissions: []Permission{{Action: ActionAdmin, Resource: ResourceAll}}}
	}
	userGrpID := "00000000-0000-5000-8000-000000000005"
	userGrp := func() *Group {
		return &Group{ID: userGrpID, Permissions: []Permission{
			{Action: ActionView, Resource: ResourceRecommendations},
			{Action: ActionCreate, Resource: ResourcePlans},
		}}
	}

	t.Run("admin with scoped API key returns key permissions (intersection passes)", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		scoped := []Permission{{Action: ActionView, Resource: ResourceRecommendations}}
		apiKey := &UserAPIKey{ID: "key-1", UserID: "user-123", Permissions: scoped}
		user := &User{ID: "user-123", GroupIDs: []string{DefaultAdminGroupID}}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(adminGrp(), nil)

		permissions, err := service.ComputeEffectivePermissions(ctx, apiKey, user)
		require.NoError(t, err)
		// Admin holds {admin, *} so the scoped key permission passes the
		// intersection and is returned.
		assert.Equal(t, scoped, permissions)
	})

	t.Run("admin with unscoped key returns full group permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		apiKey := &UserAPIKey{ID: "key-1", UserID: "user-123", Permissions: []Permission{}}
		user := &User{ID: "user-123", GroupIDs: []string{DefaultAdminGroupID}}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(adminGrp(), nil)

		permissions, err := service.ComputeEffectivePermissions(ctx, apiKey, user)
		require.NoError(t, err)
		assert.Equal(t, []Permission{{Action: ActionAdmin, Resource: ResourceAll}}, permissions)
	})

	t.Run("zero-group user with unscoped key returns no permissions (fail closed)", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		apiKey := &UserAPIKey{ID: "key-1", UserID: "user-123", Permissions: []Permission{}}
		user := &User{ID: "user-123", GroupIDs: nil}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)

		permissions, err := service.ComputeEffectivePermissions(ctx, apiKey, user)
		require.NoError(t, err)
		assert.Empty(t, permissions)
	})

	t.Run("return intersection of API key and group permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		apiKey := &UserAPIKey{
			ID:     "key-1",
			UserID: "user-123",
			Permissions: []Permission{
				{Action: ActionView, Resource: ResourceRecommendations},
				{Action: ActionCreate, Resource: ResourcePlans},
				{Action: ActionAdmin, Resource: ResourceUsers}, // group lacks this
			},
		}
		user := &User{ID: "user-123", GroupIDs: []string{userGrpID}}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetGroup", ctx, userGrpID).Return(userGrp(), nil)

		permissions, err := service.ComputeEffectivePermissions(ctx, apiKey, user)
		require.NoError(t, err)
		assert.Len(t, permissions, 2)
		assert.Contains(t, permissions, Permission{Action: ActionView, Resource: ResourceRecommendations})
		assert.Contains(t, permissions, Permission{Action: ActionCreate, Resource: ResourcePlans})
	})

	t.Run("return empty when API key permissions not in group permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		apiKey := &UserAPIKey{
			ID:     "key-1",
			UserID: "user-123",
			Permissions: []Permission{
				{Action: ActionAdmin, Resource: ResourceUsers},
				{Action: ActionUpdate, Resource: ResourceConfig},
			},
		}
		user := &User{ID: "user-123", GroupIDs: []string{userGrpID}}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetGroup", ctx, userGrpID).Return(userGrp(), nil)

		permissions, err := service.ComputeEffectivePermissions(ctx, apiKey, user)
		require.NoError(t, err)
		assert.Empty(t, permissions)
	})
}

// TestService_HasAPIKeyPermissionAPI is the regression test for issue #1142:
// per-key scoped permissions were persisted and UI-exposed but never enforced
// at request time (ComputeEffectivePermissions had no request-path caller).
// HasAPIKeyPermissionAPI is the request-path enforcement point wired into
// requirePermission.
func TestService_HasAPIKeyPermissionAPI(t *testing.T) {
	ctx := context.Background()

	rawKey := "test-api-key-123456"
	hash := sha256.Sum256([]byte(rawKey))
	keyHash := base64.RawURLEncoding.EncodeToString(hash[:])

	userGrpID := "00000000-0000-5000-8000-000000000005"
	userGrp := func() *Group {
		return &Group{ID: userGrpID, Permissions: []Permission{
			{Action: ActionView, Resource: ResourceRecommendations},
			{Action: ActionCreate, Resource: ResourcePlans},
		}}
	}

	// setup returns a service whose store resolves rawKey to a key scoped to
	// keyPerms, owned by an active user whose group grants view:recommendations
	// and create:plans. This is the exact data shape of a scoped key created
	// through POST /api/auth/api-keys.
	setup := func(t *testing.T, keyPerms []Permission) *Service {
		t.Helper()
		mockStore := new(MockStore)
		service := &Service{store: mockStore}
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		user := &User{ID: "user-123", Email: "test@example.com", Active: true, GroupIDs: []string{userGrpID}}
		keyRecord := &UserAPIKey{
			ID:          "key-1",
			UserID:      "user-123",
			KeyHash:     keyHash,
			IsActive:    true,
			Permissions: keyPerms,
		}

		mockStore.On("GetAPIKeyByHash", ctx, keyHash).Return(keyRecord, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("UpdateAPIKeyLastUsed", mock.Anything, "key-1").Return(nil).Maybe()
		mockStore.On("GetGroup", ctx, userGrpID).Return(userGrp(), nil)
		return service
	}

	t.Run("scoped key grants its in-scope permission", func(t *testing.T) {
		service := setup(t, []Permission{{Action: ActionView, Resource: ResourceRecommendations}})

		userID, has, err := service.HasAPIKeyPermissionAPI(ctx, rawKey, ActionView, ResourceRecommendations)
		require.NoError(t, err)
		assert.Equal(t, "user-123", userID)
		assert.True(t, has)
	})

	t.Run("regression #1142: scoped key cannot exceed its scope even when the user holds the permission", func(t *testing.T) {
		// The key is scoped to view:recommendations only; the owning user's
		// group also grants create:plans. The key must NOT inherit it.
		service := setup(t, []Permission{{Action: ActionView, Resource: ResourceRecommendations}})

		userID, has, err := service.HasAPIKeyPermissionAPI(ctx, rawKey, ActionCreate, ResourcePlans)
		require.NoError(t, err)
		assert.Equal(t, "user-123", userID)
		assert.False(t, has, "scoped key must not grant permissions outside its scope")
	})

	t.Run("key permission the user lacks is denied (intersection)", func(t *testing.T) {
		// The key claims admin:users but the user's group never granted it.
		service := setup(t, []Permission{{Action: ActionAdmin, Resource: ResourceUsers}})

		userID, has, err := service.HasAPIKeyPermissionAPI(ctx, rawKey, ActionAdmin, ResourceUsers)
		require.NoError(t, err)
		assert.Equal(t, "user-123", userID)
		assert.False(t, has, "key must not grant permissions the owning user does not hold")
	})

	t.Run("unscoped key inherits the owner's group permissions", func(t *testing.T) {
		service := setup(t, nil)

		userID, has, err := service.HasAPIKeyPermissionAPI(ctx, rawKey, ActionCreate, ResourcePlans)
		require.NoError(t, err)
		assert.Equal(t, "user-123", userID)
		assert.True(t, has)
	})

	t.Run("invalid key fails closed with an error", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		mockStore.On("GetAPIKeyByHash", ctx, keyHash).Return(nil, nil)

		userID, has, err := service.HasAPIKeyPermissionAPI(ctx, rawKey, ActionView, ResourceRecommendations)
		assert.Error(t, err)
		assert.Empty(t, userID)
		assert.False(t, has)
	})

	// Revoked + expired keys must hit the fail-closed path through the
	// request-time entry point too (not just through ValidateUserAPIKey
	// in isolation). This is the security boundary an attacker holding a
	// revoked / expired key would probe; keep it explicitly covered so
	// any future refactor of HasAPIKeyPermissionAPI that bypasses the
	// validation step is caught here rather than at the auth perimeter.
	t.Run("revoked key fails closed with an error", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		revoked := &UserAPIKey{
			ID: "key-1", UserID: "user-123", KeyHash: keyHash, IsActive: false,
		}
		mockStore.On("GetAPIKeyByHash", ctx, keyHash).Return(revoked, nil)

		userID, has, err := service.HasAPIKeyPermissionAPI(ctx, rawKey, ActionView, ResourceRecommendations)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "revoked")
		assert.Empty(t, userID)
		assert.False(t, has)
	})

	t.Run("expired key fails closed with an error", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		past := time.Now().Add(-1 * time.Hour)
		expired := &UserAPIKey{
			ID: "key-1", UserID: "user-123", KeyHash: keyHash, IsActive: true, ExpiresAt: &past,
		}
		mockStore.On("GetAPIKeyByHash", ctx, keyHash).Return(expired, nil)

		userID, has, err := service.HasAPIKeyPermissionAPI(ctx, rawKey, ActionView, ResourceRecommendations)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expired")
		assert.Empty(t, userID)
		assert.False(t, has)
	})

	t.Run("key owned by inactive user fails closed with an error", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		inactiveUser := &User{ID: "user-123", Email: "test@example.com", Active: false}
		keyRecord := &UserAPIKey{
			ID: "key-1", UserID: "user-123", KeyHash: keyHash, IsActive: true,
		}
		mockStore.On("GetAPIKeyByHash", ctx, keyHash).Return(keyRecord, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(inactiveUser, nil)

		userID, has, err := service.HasAPIKeyPermissionAPI(ctx, rawKey, ActionView, ResourceRecommendations)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not active")
		assert.Empty(t, userID)
		assert.False(t, has)
	})
}

func TestService_validateAPIKeyPermissions(t *testing.T) {
	ctx := context.Background()

	t.Run("admin user can create keys with any permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		user := &User{ID: "user-123", GroupIDs: []string{DefaultAdminGroupID}}
		permissions := []Permission{
			{Action: ActionAdmin, Resource: ResourceUsers},
			{Action: ActionUpdate, Resource: ResourceConfig},
		}

		// validateAPIKeyPermissions resolves the user's group permissions; the
		// admin group's {admin, *} satisfies any requested permission.
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(&Group{
			ID:          DefaultAdminGroupID,
			Permissions: []Permission{{Action: ActionAdmin, Resource: ResourceAll}},
		}, nil)

		err := service.validateAPIKeyPermissions(ctx, user, permissions)
		require.NoError(t, err)
	})

	t.Run("non-admin user can create keys with their group permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		grpID := "viewer-group"
		user := &User{ID: "user-123", GroupIDs: []string{grpID}}
		permissions := []Permission{{Action: ActionView, Resource: ResourceRecommendations}}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetGroup", ctx, grpID).Return(&Group{
			ID:          grpID,
			Permissions: []Permission{{Action: ActionView, Resource: ResourceRecommendations}},
		}, nil)

		err := service.validateAPIKeyPermissions(ctx, user, permissions)
		require.NoError(t, err)
	})

	t.Run("fail when user requests permissions their groups don't grant", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		grpID := "viewer-group"
		user := &User{ID: "user-123", GroupIDs: []string{grpID}}
		permissions := []Permission{{Action: ActionAdmin, Resource: ResourceUsers}}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetGroup", ctx, grpID).Return(&Group{
			ID:          grpID,
			Permissions: []Permission{{Action: ActionView, Resource: ResourceRecommendations}},
		}, nil)

		err := service.validateAPIKeyPermissions(ctx, user, permissions)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user does not have permission")
	})

	t.Run("fail when GetAuthContext fails", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		user := &User{ID: "user-123", GroupIDs: []string{"g"}}
		permissions := []Permission{{Action: ActionView, Resource: ResourceRecommendations}}

		mockStore.On("GetUserByID", ctx, "user-123").Return(nil, assert.AnError)

		err := service.validateAPIKeyPermissions(ctx, user, permissions)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get user permissions")
	})
}
