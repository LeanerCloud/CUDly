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

func TestService_CreateAPIKeyAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully create API key via API", func(t *testing.T) {
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

		req := APICreateAPIKeyRequest{
			Name:        "Test API Key",
			Permissions: permissions,
			ExpiresAt:   nil,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("CreateAPIKey", ctx, mock.AnythingOfType("*auth.UserAPIKey")).Return(nil)

		result, err := service.CreateAPIKeyAPI(ctx, "user-123", req)

		require.NoError(t, err)
		require.NotNil(t, result)
		resp := result.(*APICreateAPIKeyResponse)
		assert.NotEmpty(t, resp.APIKey)
		assert.NotNil(t, resp.Info)
		assert.Equal(t, "Test API Key", resp.Info.Name)
		assert.Equal(t, permissions, resp.Info.Permissions)
		mockStore.AssertExpectations(t)
	})

	t.Run("successfully create API key with expiration via API", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		user := &User{
			ID:     "user-123",
			Email:  "test@example.com",
			Active: true,
			Role:   RoleAdmin,
		}

		expiresAt := time.Now().Add(30 * 24 * time.Hour)
		req := APICreateAPIKeyRequest{
			Name:        "Test API Key",
			Permissions: []Permission{{Action: ActionView, Resource: ResourceRecommendations}},
			ExpiresAt:   &expiresAt,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("CreateAPIKey", ctx, mock.AnythingOfType("*auth.UserAPIKey")).Return(nil)

		result, err := service.CreateAPIKeyAPI(ctx, "user-123", req)

		require.NoError(t, err)
		require.NotNil(t, result)
		resp := result.(*APICreateAPIKeyResponse)
		assert.NotNil(t, resp.Info.ExpiresAt)
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

		req := APICreateAPIKeyRequest{
			Name:        "Test API Key",
			Permissions: []Permission{},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)

		resp, err := service.CreateAPIKeyAPI(ctx, "user-123", req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		mockStore.AssertExpectations(t)
	})
}

func TestService_ListUserAPIKeysAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully list API keys via API", func(t *testing.T) {
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
				IsActive:  false,
				CreatedAt: now,
			},
		}

		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(expectedKeys, nil)

		result, err := service.ListUserAPIKeysAPI(ctx, "user-123")

		require.NoError(t, err)
		require.NotNil(t, result)
		resp := result.(*APIListAPIKeysResponse)
		assert.Len(t, resp.APIKeys, 2)
		assert.Equal(t, "key-1", resp.APIKeys[0].ID)
		assert.Equal(t, "Key 1", resp.APIKeys[0].Name)
		assert.True(t, resp.APIKeys[0].IsActive)
		assert.Equal(t, "key-2", resp.APIKeys[1].ID)
		assert.False(t, resp.APIKeys[1].IsActive)
		mockStore.AssertExpectations(t)
	})

	t.Run("return empty list when no keys", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return([]*UserAPIKey{}, nil)

		result, err := service.ListUserAPIKeysAPI(ctx, "user-123")

		require.NoError(t, err)
		require.NotNil(t, result)
		resp := result.(*APIListAPIKeysResponse)
		assert.Empty(t, resp.APIKeys)
		mockStore.AssertExpectations(t)
	})

	t.Run("return error when store fails", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(nil, assert.AnError)

		resp, err := service.ListUserAPIKeysAPI(ctx, "user-123")

		assert.Error(t, err)
		assert.Nil(t, resp)
		mockStore.AssertExpectations(t)
	})
}

func TestService_DeleteAPIKeyAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully delete API key via API", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		existingKey := &UserAPIKey{
			ID:     "key-1",
			UserID: "user-123",
		}
		user := &User{
			ID:   "user-123",
			Role: RoleUser,
		}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(existingKey, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("DeleteAPIKey", ctx, "key-1").Return(nil)

		err := service.DeleteAPIKeyAPI(ctx, "user-123", "key-1")

		require.NoError(t, err)
		mockStore.AssertExpectations(t)
	})

	t.Run("return error when delete fails", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(nil, assert.AnError)

		err := service.DeleteAPIKeyAPI(ctx, "user-123", "key-1")

		assert.Error(t, err)
		mockStore.AssertExpectations(t)
	})
}

func TestService_RevokeAPIKeyAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully revoke API key via API", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		existingKey := &UserAPIKey{
			ID:       "key-1",
			UserID:   "user-123",
			IsActive: true,
		}
		user := &User{
			ID:   "user-123",
			Role: RoleUser,
		}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(existingKey, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("UpdateAPIKey", ctx, mock.MatchedBy(func(key *UserAPIKey) bool {
			return key.ID == "key-1" && !key.IsActive
		})).Return(nil)

		err := service.RevokeAPIKeyAPI(ctx, "user-123", "key-1")

		require.NoError(t, err)
		mockStore.AssertExpectations(t)
	})

	t.Run("return error when revoke fails", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(nil, assert.AnError)

		err := service.RevokeAPIKeyAPI(ctx, "user-123", "key-1")

		assert.Error(t, err)
		mockStore.AssertExpectations(t)
	})
}

func TestService_ValidateUserAPIKeyAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully validate API key via API", func(t *testing.T) {
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
			Permissions: []Permission{{Action: ActionView, Resource: ResourceRecommendations}},
		}

		mockStore.On("GetAPIKeyByHash", ctx, keyHash).Return(apiKeyRecord, nil)
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetAPIKeyByID", mock.Anything, "key-1").Return(apiKeyRecord, nil).Maybe()
		mockStore.On("UpdateAPIKey", mock.Anything, mock.AnythingOfType("*auth.UserAPIKey")).Return(nil).Maybe()

		resultKey, resultUser, err := service.ValidateUserAPIKeyAPI(ctx, apiKey)

		require.NoError(t, err)
		assert.Equal(t, apiKeyRecord, resultKey)
		assert.Equal(t, user, resultUser)
		time.Sleep(10 * time.Millisecond) // Allow goroutine to complete
		mockStore.AssertExpectations(t)
	})

	t.Run("fail when API key is invalid", func(t *testing.T) {
		mockStore := new(MockStore)
		service := &Service{store: mockStore}

		apiKey := "test-api-key-123456"
		hash := sha256.Sum256([]byte(apiKey))
		keyHash := base64.RawURLEncoding.EncodeToString(hash[:])

		mockStore.On("GetAPIKeyByHash", ctx, keyHash).Return(nil, assert.AnError)

		resultKey, resultUser, err := service.ValidateUserAPIKeyAPI(ctx, apiKey)

		assert.Error(t, err)
		assert.Nil(t, resultKey)
		assert.Nil(t, resultUser)
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

		resultKey, resultUser, err := service.ValidateUserAPIKeyAPI(ctx, apiKey)

		assert.Error(t, err)
		assert.Nil(t, resultKey)
		assert.Nil(t, resultUser)
		mockStore.AssertExpectations(t)
	})
}
