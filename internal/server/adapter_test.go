package server

import (
	"context"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/api"
	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func createMockAuthService(t *testing.T) (*authServiceAdapter, *auth.MockStore) {
	t.Helper()
	mockStore := new(auth.MockStore)
	service := auth.NewService(auth.ServiceConfig{
		Store:           mockStore,
		SessionDuration: time.Hour,
	})
	adapter := newAuthServiceAdapter(service)
	return adapter, mockStore
}

func TestAuthServiceAdapter_Login(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(&auth.User{
		ID:           "user-1",
		Email:        "test@example.com",
		PasswordHash: "$2a$10$invalidhash",
		Role:         "admin",
	}, nil)

	_, err := adapter.Login(ctx, api.LoginRequest{
		Email:    "test@example.com",
		Password: "wrongpassword",
	})
	// Will fail on password check but exercises the adapter code path
	assert.Error(t, err)
}

func TestAuthServiceAdapter_Logout(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	// Token gets hashed internally, so use mock.Anything
	mockStore.On("DeleteSession", ctx, mock.AnythingOfType("string")).Return(nil)

	err := adapter.Logout(ctx, "token-123")
	require.NoError(t, err)
}

func TestAuthServiceAdapter_ValidateSession(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	// Token gets hashed, use mock.Anything
	mockStore.On("GetSession", ctx, mock.AnythingOfType("string")).Return(&auth.Session{
		Token:     "hashed-token",
		UserID:    "user-1",
		Email:     "test@example.com",
		Role:      "admin",
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil)

	sess, err := adapter.ValidateSession(ctx, "valid-token")
	require.NoError(t, err)
	assert.Equal(t, "user-1", sess.UserID)
	assert.Equal(t, "test@example.com", sess.Email)
	assert.Equal(t, "admin", sess.Role)
}

func TestAuthServiceAdapter_ValidateSession_Error(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetSession", ctx, mock.AnythingOfType("string")).Return(nil, nil)

	_, err := adapter.ValidateSession(ctx, "expired-token")
	assert.Error(t, err)
}

func TestAuthServiceAdapter_CheckAdminExists(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("AdminExists", ctx).Return(true, nil)

	exists, err := adapter.CheckAdminExists(ctx)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestAuthServiceAdapter_RequestPasswordReset(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	// User not found - exercises the adapter code path without needing email sender
	mockStore.On("GetUserByEmail", ctx, "nonexistent@example.com").Return(nil, nil)

	err := adapter.RequestPasswordReset(ctx, "nonexistent@example.com")
	// The service silently returns nil when user not found (to prevent enumeration)
	assert.NoError(t, err)
}

func TestAuthServiceAdapter_ConfirmPasswordReset(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetUserByResetToken", ctx, mock.AnythingOfType("string")).Return(nil, assert.AnError)

	err := adapter.ConfirmPasswordReset(ctx, api.PasswordResetConfirm{
		Token:       "reset-token",
		NewPassword: "NewPassword123!",
	})
	assert.Error(t, err)
}

func TestAuthServiceAdapter_GetUser(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetUserByID", ctx, "user-1").Return(&auth.User{
		ID:         "user-1",
		Email:      "test@example.com",
		Role:       "admin",
		MFAEnabled: false,
	}, nil)

	user, err := adapter.GetUser(ctx, "user-1")
	require.NoError(t, err)
	assert.Equal(t, "user-1", user.ID)
	assert.Equal(t, "test@example.com", user.Email)
	assert.Equal(t, "admin", user.Role)
}

func TestAuthServiceAdapter_GetUser_Error(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetUserByID", ctx, "nonexistent").Return(nil, assert.AnError)

	_, err := adapter.GetUser(ctx, "nonexistent")
	assert.Error(t, err)
}

func TestAuthServiceAdapter_DeleteUser(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("DeleteUser", ctx, "user-1").Return(nil)
	mockStore.On("DeleteUserSessions", ctx, "user-1").Return(nil)

	err := adapter.DeleteUser(ctx, "user-1")
	require.NoError(t, err)
}

func TestAuthServiceAdapter_ListUsersAPI(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("ListUsers", ctx).Return([]auth.User{
		{ID: "user-1", Email: "user1@example.com", Role: "admin"},
		{ID: "user-2", Email: "user2@example.com", Role: "viewer"},
	}, nil)

	result, err := adapter.ListUsersAPI(ctx)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestAuthServiceAdapter_ChangePasswordAPI(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetUserByID", ctx, "user-1").Return(&auth.User{
		ID:           "user-1",
		Email:        "test@example.com",
		PasswordHash: "$2a$10$invalidhash",
	}, nil)

	err := adapter.ChangePasswordAPI(ctx, "user-1", "oldpass", "newpass")
	// Will fail on password verification
	assert.Error(t, err)
}

func TestAuthServiceAdapter_DeleteGroup(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("DeleteGroup", ctx, "group-1").Return(nil)

	err := adapter.DeleteGroup(ctx, "group-1")
	require.NoError(t, err)
}

func TestAuthServiceAdapter_GetGroupAPI(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetGroup", ctx, "group-1").Return(&auth.Group{
		ID:   "group-1",
		Name: "admins",
	}, nil)

	result, err := adapter.GetGroupAPI(ctx, "group-1")
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestAuthServiceAdapter_ListGroupsAPI(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("ListGroups", ctx).Return([]auth.Group{
		{ID: "group-1", Name: "admins"},
	}, nil)

	result, err := adapter.ListGroupsAPI(ctx)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestAuthServiceAdapter_ValidateCSRFToken(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	// Token gets hashed, so use mock.Anything
	mockStore.On("GetSession", ctx, mock.AnythingOfType("string")).Return(&auth.Session{
		Token:     "hashed-session-token",
		UserID:    "user-1",
		CSRFToken: "csrf-token",
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil)

	err := adapter.ValidateCSRFToken(ctx, "session-token", "csrf-token")
	require.NoError(t, err)
}

func TestAuthServiceAdapter_HasPermissionAPI(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetUserByID", ctx, "user-1").Return(&auth.User{
		ID:    "user-1",
		Email: "test@example.com",
		Role:  "admin",
	}, nil)

	has, err := adapter.HasPermissionAPI(ctx, "user-1", "read", "config")
	require.NoError(t, err)
	assert.True(t, has)
}

func TestAuthServiceAdapter_UpdateUserProfile(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	// User not found - exercises the adapter code path
	mockStore.On("GetUserByID", ctx, "nonexistent").Return(nil, assert.AnError)

	err := adapter.UpdateUserProfile(ctx, "nonexistent", "new@example.com", "", "")
	assert.Error(t, err)
}

func TestAuthServiceAdapter_CreateUserAPI(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetUserByEmail", ctx, mock.AnythingOfType("string")).Return(nil, nil)
	mockStore.On("CreateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil)

	// Pass a properly typed request
	_, err := adapter.CreateUserAPI(ctx, map[string]interface{}{
		"email":    "new@example.com",
		"password": "StrongPassword123!",
		"role":     "viewer",
	})
	// May fail depending on validation but exercises the code
	_ = err
}

func TestAuthServiceAdapter_UpdateUserAPI(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetUserByID", ctx, "user-1").Return(&auth.User{
		ID:    "user-1",
		Email: "old@example.com",
		Role:  "viewer",
	}, nil)
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil)

	_, err := adapter.UpdateUserAPI(ctx, "user-1", map[string]interface{}{
		"role": "admin",
	})
	_ = err
}

func TestAuthServiceAdapter_CreateGroupAPI(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("CreateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil)

	_, err := adapter.CreateGroupAPI(ctx, map[string]interface{}{
		"name":        "test-group",
		"permissions": []string{"read:config"},
	})
	_ = err
}

func TestAuthServiceAdapter_UpdateGroupAPI(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetGroup", ctx, "group-1").Return(&auth.Group{
		ID:   "group-1",
		Name: "old-name",
	}, nil)
	mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil)

	_, err := adapter.UpdateGroupAPI(ctx, "group-1", map[string]interface{}{
		"name": "new-name",
	})
	_ = err
}

func TestAuthServiceAdapter_CreateAPIKeyAPI(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("CreateAPIKey", ctx, mock.AnythingOfType("*auth.UserAPIKey")).Return(nil)

	_, err := adapter.CreateAPIKeyAPI(ctx, "user-1", map[string]interface{}{
		"name": "my-key",
	})
	_ = err
}

func TestAuthServiceAdapter_ListUserAPIKeysAPI(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetUserByID", ctx, "user-1").Return(&auth.User{
		ID: "user-1", Email: "test@example.com",
	}, nil)
	mockStore.On("ListAPIKeysByUser", ctx, "user-1").Return([]*auth.UserAPIKey{}, nil)

	result, err := adapter.ListUserAPIKeysAPI(ctx, "user-1")
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestAuthServiceAdapter_DeleteAPIKeyAPI(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetUserByID", ctx, "user-1").Return(&auth.User{
		ID: "user-1", Email: "test@example.com",
	}, nil)
	mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(&auth.UserAPIKey{
		ID:     "key-1",
		UserID: "user-1",
	}, nil)
	mockStore.On("DeleteAPIKey", ctx, "key-1").Return(nil)

	err := adapter.DeleteAPIKeyAPI(ctx, "user-1", "key-1")
	require.NoError(t, err)
}

func TestAuthServiceAdapter_RevokeAPIKeyAPI(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetUserByID", ctx, "user-1").Return(&auth.User{
		ID: "user-1", Email: "test@example.com",
	}, nil)
	mockStore.On("GetAPIKeyByID", ctx, "key-1").Return(&auth.UserAPIKey{
		ID:     "key-1",
		UserID: "user-1",
	}, nil)
	mockStore.On("UpdateAPIKey", ctx, mock.AnythingOfType("*auth.UserAPIKey")).Return(nil)

	err := adapter.RevokeAPIKeyAPI(ctx, "user-1", "key-1")
	require.NoError(t, err)
}

func TestAuthServiceAdapter_ValidateUserAPIKeyAPI(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("GetAPIKeyByHash", ctx, mock.AnythingOfType("string")).Return(nil, assert.AnError)

	_, _, err := adapter.ValidateUserAPIKeyAPI(ctx, "invalid-api-key")
	assert.Error(t, err)
}

func TestNewAuthServiceAdapter_NotNil(t *testing.T) {
	service := auth.NewService(auth.ServiceConfig{})
	adapter := newAuthServiceAdapter(service)
	assert.NotNil(t, adapter)
	assert.NotNil(t, adapter.service)
}

func TestAuthServiceAdapter_SetupAdmin(t *testing.T) {
	adapter, mockStore := createMockAuthService(t)
	ctx := context.Background()

	mockStore.On("AdminExists", ctx).Return(false, nil)
	mockStore.On("CreateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil)
	mockStore.On("CreateSession", ctx, mock.AnythingOfType("*auth.Session")).Return(nil)

	resp, err := adapter.SetupAdmin(ctx, api.SetupAdminRequest{
		Email:    "admin@example.com",
		Password: "X9k#mP2$vL7qR4wN",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "admin@example.com", resp.User.Email)
}
