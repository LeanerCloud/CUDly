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
			ID:       "user-123",
			Email:    "test@example.com",
			Active:   true,
			GroupIDs: []string{DefaultAdminGroupID},
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
		mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(&Group{
			ID:          DefaultAdminGroupID,
			Permissions: []Permission{{Action: ActionAdmin, Resource: ResourceAll}},
		}, nil)
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
			ID:       "user-123",
			Email:    "test@example.com",
			Active:   true,
			GroupIDs: []string{DefaultAdminGroupID},
		}

		expiresAt := time.Now().Add(30 * 24 * time.Hour)
		req := APICreateAPIKeyRequest{
			Name:        "Test API Key",
			Permissions: []Permission{{Action: ActionView, Resource: ResourceRecommendations}},
			ExpiresAt:   &expiresAt,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(&Group{
			ID:          DefaultAdminGroupID,
			Permissions: []Permission{{Action: ActionAdmin, Resource: ResourceAll}},
		}, nil)
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

// TestService_CreateAPIKeyAPI_CrossPackageType is the regression test for
// issue #1440. The HTTP handler (internal/api) unmarshals the request body
// into api.CreateAPIKeyRequest - a struct in a different package that has
// the same json tags as APICreateAPIKeyRequest but is a distinct Go type.
// The former type-assertion implementation always returned "invalid request
// type", causing a 500. The fix uses JSON re-encoding so any struct with
// compatible json fields is accepted.
func TestService_CreateAPIKeyAPI_CrossPackageType(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	service := &Service{store: mockStore}

	user := &User{
		ID:       "user-123",
		Email:    "test@example.com",
		Active:   true,
		GroupIDs: []string{DefaultAdminGroupID},
	}
	mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
	mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(&Group{
		ID:          DefaultAdminGroupID,
		Permissions: []Permission{{Action: ActionAdmin, Resource: ResourceAll}},
	}, nil)
	mockStore.On("CreateAPIKey", ctx, mock.AnythingOfType("*auth.UserAPIKey")).Return(nil)

	// Simulate what the api.Handler does: an anonymous struct with the same
	// json field names but a different Go type than APICreateAPIKeyRequest.
	// This is exactly the value passed by the handler in production (issue #1440).
	crossPkgReq := struct {
		Name        string     `json:"name"`
		ExpiresAt   *time.Time `json:"expires_at,omitempty"`
		Permissions []struct {
			Action   string `json:"action"`
			Resource string `json:"resource"`
		} `json:"permissions,omitempty"`
	}{
		Name: "My API Key",
	}

	result, err := service.CreateAPIKeyAPI(ctx, "user-123", crossPkgReq)

	// Before the fix this returned "invalid request type" (500); now it must succeed.
	require.NoError(t, err, "CreateAPIKeyAPI must accept cross-package types with compatible JSON fields (issue #1440)")
	require.NotNil(t, result)
	resp, ok := result.(*APICreateAPIKeyResponse)
	require.True(t, ok)
	assert.NotEmpty(t, resp.APIKey)
	assert.Equal(t, "My API Key", resp.Info.Name)
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

	// Regression: the per-row "Requests (window)" cell must not show a count
	// left over from a window that has already closed. The stored column
	// keeps its value until the key's next request, so the read path is what
	// has to zero it.
	t.Run("zeroes an expired window and keeps a live one", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		service := &Service{store: mockStore}

		stale := time.Now().Add(-48 * time.Hour)
		fresh := time.Now().Add(-3 * time.Hour)
		keys := []*UserAPIKey{
			{ID: "stale", RequestCountWindow: 900, RequestCountTotal: 900, RequestCountWindowStart: &stale},
			{ID: "live", RequestCountWindow: 11, RequestCountTotal: 40, RequestCountWindowStart: &fresh},
			{ID: "never-used", RequestCountWindow: 0, RequestCountTotal: 0},
		}
		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(keys, nil)

		result, err := service.ListUserAPIKeysAPI(ctx, "user-123")
		require.NoError(t, err)

		resp, ok := result.(*APIListAPIKeysResponse)
		require.True(t, ok)
		require.Len(t, resp.APIKeys, 3)

		// Expired window: no current activity, and no window start to point
		// at a period that is over.
		assert.Equal(t, int64(0), resp.APIKeys[0].RequestCountWindow)
		assert.Nil(t, resp.APIKeys[0].RequestCountWindowStart)
		// Lifetime is unaffected by the window expiring.
		require.NotNil(t, resp.APIKeys[0].RequestCountTotal)
		assert.Equal(t, int64(900), *resp.APIKeys[0].RequestCountTotal)

		// Live window passes through untouched.
		assert.Equal(t, int64(11), resp.APIKeys[1].RequestCountWindow)
		require.NotNil(t, resp.APIKeys[1].RequestCountWindowStart)
		assert.Equal(t, fresh, *resp.APIKeys[1].RequestCountWindowStart)

		// Never used: zero, with no window start.
		assert.Equal(t, int64(0), resp.APIKeys[2].RequestCountWindow)
		assert.Nil(t, resp.APIKeys[2].RequestCountWindowStart)
	})

	// Regression: migration 000094 added request_count_total with DEFAULT 0,
	// so a key that was already serving traffic reads as zero. Reporting that
	// as "0 requests" states a volume nobody measured. It must come back as
	// unknown (nil) instead, while a genuinely-unused key still reports 0.
	t.Run("reports an unmeasured lifetime count as unknown, not zero", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		service := &Service{store: mockStore}

		usedAt := time.Now().Add(-1 * time.Hour)
		keys := []*UserAPIKey{
			// Used before the counters existed: zero here means "not recorded".
			{ID: "pre-migration", LastUsedAt: &usedAt, RequestCountTotal: 0},
			// Never used at all: zero is the true count.
			{ID: "never-used", RequestCountTotal: 0},
			// Counted normally.
			{ID: "counted", LastUsedAt: &usedAt, RequestCountTotal: 12},
		}
		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(keys, nil)

		result, err := service.ListUserAPIKeysAPI(ctx, "user-123")
		require.NoError(t, err)

		resp, ok := result.(*APIListAPIKeysResponse)
		require.True(t, ok)
		require.Len(t, resp.APIKeys, 3)

		assert.Nil(t, resp.APIKeys[0].RequestCountTotal, "pre-migration key must report unknown, not 0")

		require.NotNil(t, resp.APIKeys[1].RequestCountTotal)
		assert.Equal(t, int64(0), *resp.APIKeys[1].RequestCountTotal)

		require.NotNil(t, resp.APIKeys[2].RequestCountTotal)
		assert.Equal(t, int64(12), *resp.APIKeys[2].RequestCountTotal)
	})

	// #492: last_used_at must round-trip through ListUserAPIKeysAPI so the
	// frontend can render "Last used" without a separate endpoint.
	t.Run("last_used_at is present in response when key was used (issue #492)", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		service := &Service{store: mockStore}

		createdAt := time.Now()
		usedAt := createdAt.Add(-2 * time.Hour)
		keys := []*UserAPIKey{
			{
				ID:         "key-used",
				UserID:     "user-123",
				Name:       "Used Key",
				KeyPrefix:  "prefix1",
				IsActive:   true,
				CreatedAt:  createdAt,
				LastUsedAt: &usedAt,
			},
		}
		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(keys, nil)

		result, err := service.ListUserAPIKeysAPI(ctx, "user-123")

		require.NoError(t, err)
		resp := result.(*APIListAPIKeysResponse)
		require.Len(t, resp.APIKeys, 1)
		require.NotNil(t, resp.APIKeys[0].LastUsedAt, "last_used_at must be set in the API response")
		assert.True(t, resp.APIKeys[0].LastUsedAt.Equal(usedAt))
	})

	// #492: last_used_at must be nil (not zero-time) for a never-used key.
	t.Run("last_used_at is nil in response for never-used key (issue #492)", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		service := &Service{store: mockStore}

		keys := []*UserAPIKey{
			{
				ID:        "key-new",
				UserID:    "user-123",
				Name:      "New Key",
				KeyPrefix: "prefix2",
				IsActive:  true,
				CreatedAt: time.Now(),
				// LastUsedAt intentionally nil
			},
		}
		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(keys, nil)

		result, err := service.ListUserAPIKeysAPI(ctx, "user-123")

		require.NoError(t, err)
		resp := result.(*APIListAPIKeysResponse)
		require.Len(t, resp.APIKeys, 1)
		assert.Nil(t, resp.APIKeys[0].LastUsedAt, "last_used_at must be nil for a never-used key")
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
			ID: "user-123",
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
			ID: "user-123",
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
		mockStore.On("RecordAPIKeyUsage", mock.Anything, "key-1", mock.Anything).Return(nil).Maybe()

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

func TestSortAPIKeysByActivity(t *testing.T) {
	t.Run("sorts by window count descending", func(t *testing.T) {
		activity := []apiKeyActivity{
			{key: &UserAPIKey{ID: "low", RequestCountTotal: 100}, window: 1},
			{key: &UserAPIKey{ID: "high", RequestCountTotal: 5}, window: 10},
			{key: &UserAPIKey{ID: "mid", RequestCountTotal: 50}, window: 5},
		}

		sortAPIKeysByActivity(activity)

		require.Len(t, activity, 3)
		assert.Equal(t, "high", activity[0].key.ID)
		assert.Equal(t, "mid", activity[1].key.ID)
		assert.Equal(t, "low", activity[2].key.ID)
	})

	t.Run("uses lifetime total as tiebreaker", func(t *testing.T) {
		activity := []apiKeyActivity{
			{key: &UserAPIKey{ID: "idle-high-lifetime", RequestCountTotal: 900}, window: 3},
			{key: &UserAPIKey{ID: "active-low-lifetime", RequestCountTotal: 10}, window: 3},
		}

		sortAPIKeysByActivity(activity)

		require.Len(t, activity, 2)
		assert.Equal(t, "idle-high-lifetime", activity[0].key.ID)
		assert.Equal(t, "active-low-lifetime", activity[1].key.ID)
	})

	t.Run("ranks by effective window, not the stale stored count", func(t *testing.T) {
		// Regression for the stale-window bug: a key with a huge count left
		// over from an expired window must not outrank a genuinely active
		// key. The caller zeroes the expired window via effectiveWindowUsage
		// before sorting, so the sort sees window=0 for the stale key.
		activity := []apiKeyActivity{
			{key: &UserAPIKey{ID: "stale-but-huge", RequestCountWindow: 9999, RequestCountTotal: 9999}, window: 0},
			{key: &UserAPIKey{ID: "currently-active", RequestCountWindow: 4, RequestCountTotal: 4}, window: 4},
		}

		sortAPIKeysByActivity(activity)

		require.Len(t, activity, 2)
		assert.Equal(t, "currently-active", activity[0].key.ID)
	})
}

func TestService_GetAPIKeysUsageStatsAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("aggregates totals and returns top-3 by window activity", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		service := &Service{store: mockStore}

		fresh := time.Now().Add(-1 * time.Hour)
		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		keys := []*UserAPIKey{
			{ID: "key-1", Name: "Busy", KeyPrefix: "aaaa1111", IsActive: true, RequestCountWindow: 30, RequestCountTotal: 300, RequestCountWindowStart: &fresh},
			{ID: "key-2", Name: "Medium", KeyPrefix: "bbbb2222", IsActive: true, RequestCountWindow: 12, RequestCountTotal: 120, RequestCountWindowStart: &fresh},
			{ID: "key-3", Name: "Quiet", KeyPrefix: "cccc3333", IsActive: false, RequestCountWindow: 3, RequestCountTotal: 60, RequestCountWindowStart: &fresh},
			{ID: "key-4", Name: "Idle", KeyPrefix: "dddd4444", IsActive: true, RequestCountWindow: 0, RequestCountTotal: 5},
		}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(keys, nil)

		result, err := service.GetAPIKeysUsageStatsAPI(ctx, "user-123")
		require.NoError(t, err)

		resp, ok := result.(*APIKeysUsageStatsResponse)
		require.True(t, ok)
		assert.Equal(t, 3, resp.TotalActive) // key-3 is inactive
		assert.Equal(t, int64(45), resp.TotalRequestsWindow)
		assert.Equal(t, int64(485), resp.TotalRequestsLifetime)
		require.Len(t, resp.TopKeys, 3)
		assert.Equal(t, "key-1", resp.TopKeys[0].ID)
		assert.Equal(t, "key-2", resp.TopKeys[1].ID)
		assert.Equal(t, "key-3", resp.TopKeys[2].ID)
		// key-4 has zero window activity and must be omitted, even though the
		// top-N slice has room for a 4th entry.
		for _, k := range resp.TopKeys {
			assert.NotEqual(t, "key-4", k.ID)
		}
	})

	t.Run("omits top list entirely when no key has window activity", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		service := &Service{store: mockStore}

		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		keys := []*UserAPIKey{
			{ID: "key-1", IsActive: true, RequestCountWindow: 0, RequestCountTotal: 7},
		}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(keys, nil)

		result, err := service.GetAPIKeysUsageStatsAPI(ctx, "user-123")
		require.NoError(t, err)

		resp, ok := result.(*APIKeysUsageStatsResponse)
		require.True(t, ok)
		assert.Equal(t, 1, resp.TotalActive)
		assert.Equal(t, int64(0), resp.TotalRequestsWindow)
		assert.Equal(t, int64(7), resp.TotalRequestsLifetime)
		assert.Empty(t, resp.TopKeys)
	})

	// Regression: the window counter on an api_keys row is only rewritten by
	// the key's NEXT request, so a key that went idle keeps its last window
	// count forever. Summing that column verbatim reported requests from a
	// window that closed arbitrarily long ago as current activity.
	t.Run("excludes counts from expired windows", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		service := &Service{store: mockStore}

		stale := time.Now().Add(-30 * 24 * time.Hour)
		fresh := time.Now().Add(-2 * time.Hour)
		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		keys := []*UserAPIKey{
			// Hammered a month ago, untouched since: its window is long gone.
			{ID: "stale", Name: "Stale", KeyPrefix: "aaaa1111", IsActive: true, RequestCountWindow: 5000, RequestCountTotal: 5000, RequestCountWindowStart: &stale},
			// Genuinely active inside the current window.
			{ID: "active", Name: "Active", KeyPrefix: "bbbb2222", IsActive: true, RequestCountWindow: 7, RequestCountTotal: 7, RequestCountWindowStart: &fresh},
		}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(keys, nil)

		result, err := service.GetAPIKeysUsageStatsAPI(ctx, "user-123")
		require.NoError(t, err)

		resp, ok := result.(*APIKeysUsageStatsResponse)
		require.True(t, ok)
		// Only the live window counts; the 5000 stale requests are not
		// current activity. Lifetime still includes them.
		assert.Equal(t, int64(7), resp.TotalRequestsWindow)
		assert.Equal(t, int64(5007), resp.TotalRequestsLifetime)
		// The stale key must not be listed as "most active", and must not
		// outrank the key that is actually being used.
		require.Len(t, resp.TopKeys, 1)
		assert.Equal(t, "active", resp.TopKeys[0].ID)
		assert.Equal(t, int64(7), resp.TopKeys[0].RequestCountWindow)
	})

	// Regression: TotalActive counted any unrevoked key, so a key past its
	// expiry was summarized as "active" while the keys table right below the
	// summary card rendered the same key as "Expired". The authentication
	// path (validateAPIKeyStatus / GetAPIKeyByHash) already treats an
	// expired key as unusable.
	t.Run("does not count expired keys as active", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		service := &Service{store: mockStore}

		expired := time.Now().Add(-1 * time.Hour)
		future := time.Now().Add(24 * time.Hour)
		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		keys := []*UserAPIKey{
			{ID: "expired", IsActive: true, ExpiresAt: &expired},
			{ID: "revoked", IsActive: false},
			{ID: "live", IsActive: true, ExpiresAt: &future},
			{ID: "no-expiry", IsActive: true},
		}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(keys, nil)

		result, err := service.GetAPIKeysUsageStatsAPI(ctx, "user-123")
		require.NoError(t, err)

		resp, ok := result.(*APIKeysUsageStatsResponse)
		require.True(t, ok)
		assert.Equal(t, 2, resp.TotalActive)
	})

	// Regression: an unmeasured key must not contribute a fabricated 0 to the
	// lifetime total silently. It is excluded and the response says so, so the
	// UI can show a lower bound instead of an exact-looking undercount.
	t.Run("flags the lifetime total as partial when a key predates the counters", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		service := &Service{store: mockStore}

		usedAt := time.Now().Add(-1 * time.Hour)
		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		keys := []*UserAPIKey{
			{ID: "pre-migration", IsActive: true, LastUsedAt: &usedAt, RequestCountTotal: 0},
			{ID: "counted", IsActive: true, LastUsedAt: &usedAt, RequestCountTotal: 40},
		}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(keys, nil)

		result, err := service.GetAPIKeysUsageStatsAPI(ctx, "user-123")
		require.NoError(t, err)

		resp, ok := result.(*APIKeysUsageStatsResponse)
		require.True(t, ok)
		assert.Equal(t, int64(40), resp.TotalRequestsLifetime)
		assert.True(t, resp.LifetimePartial)
	})

	t.Run("does not flag the total as partial when every key is measured", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		service := &Service{store: mockStore}

		usedAt := time.Now().Add(-1 * time.Hour)
		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		keys := []*UserAPIKey{
			{ID: "counted", IsActive: true, LastUsedAt: &usedAt, RequestCountTotal: 40},
			{ID: "never-used", IsActive: true, RequestCountTotal: 0},
		}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(keys, nil)

		result, err := service.GetAPIKeysUsageStatsAPI(ctx, "user-123")
		require.NoError(t, err)

		resp, ok := result.(*APIKeysUsageStatsResponse)
		require.True(t, ok)
		assert.Equal(t, int64(40), resp.TotalRequestsLifetime)
		assert.False(t, resp.LifetimePartial)
	})

	t.Run("returns empty stats when user has no keys", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		service := &Service{store: mockStore}

		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return([]*UserAPIKey{}, nil)

		result, err := service.GetAPIKeysUsageStatsAPI(ctx, "user-123")
		require.NoError(t, err)

		resp, ok := result.(*APIKeysUsageStatsResponse)
		require.True(t, ok)
		assert.Equal(t, 0, resp.TotalActive)
		assert.Equal(t, int64(0), resp.TotalRequestsWindow)
		assert.Equal(t, int64(0), resp.TotalRequestsLifetime)
		assert.Empty(t, resp.TopKeys)
	})

	t.Run("propagates store error", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		service := &Service{store: mockStore}

		user := &User{ID: "user-123", Email: "test@example.com", Active: true}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("ListAPIKeysByUser", ctx, "user-123").Return(nil, assert.AnError)

		result, err := service.GetAPIKeysUsageStatsAPI(ctx, "user-123")
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}
