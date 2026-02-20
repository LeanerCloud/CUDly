package auth

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestService_SetupAdmin(t *testing.T) {
	ctx := context.Background()

	t.Run("successful admin setup", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("AdminExists", ctx).Return(false, nil).Once()
		mockStore.On("CreateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()
		mockStore.On("CreateSession", ctx, mock.AnythingOfType("*auth.Session")).Return(nil).Once()

		req := SetupAdminRequest{
			Email:    "admin@example.com",
			Password: "SecurePass@123",
		}

		resp, err := service.SetupAdmin(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.NotEmpty(t, resp.Token)
		assert.Equal(t, "admin@example.com", resp.User.Email)
		assert.Equal(t, RoleAdmin, resp.User.Role)

		mockStore.AssertExpectations(t)
	})

	t.Run("admin already exists", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("AdminExists", ctx).Return(true, nil).Once()

		req := SetupAdminRequest{
			Email:    "admin@example.com",
			Password: "SecurePass@123",
		}

		resp, err := service.SetupAdmin(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "admin user already exists")

		mockStore.AssertExpectations(t)
	})

	t.Run("weak password", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("AdminExists", ctx).Return(false, nil).Once()

		req := SetupAdminRequest{
			Email:    "admin@example.com",
			Password: "weak",
		}

		resp, err := service.SetupAdmin(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)

		mockStore.AssertExpectations(t)
	})

	t.Run("invalid email format", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("AdminExists", ctx).Return(false, nil).Once()

		req := SetupAdminRequest{
			Email:    "invalid-email",
			Password: "SecurePass@123",
		}

		resp, err := service.SetupAdmin(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "invalid email format")

		mockStore.AssertExpectations(t)
	})
}

func TestService_CreateUser(t *testing.T) {
	ctx := context.Background()

	t.Run("successful user creation", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "newuser@example.com").Return(nil, nil).Once()
		mockStore.On("CreateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		req := CreateUserRequest{
			Email:    "newuser@example.com",
			Password: "SecurePass@123",
			Role:     RoleUser,
		}

		user, err := service.CreateUser(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, user)
		assert.Equal(t, "newuser@example.com", user.Email)
		assert.Equal(t, RoleUser, user.Role)
		assert.True(t, user.Active)

		mockStore.AssertExpectations(t)
	})

	t.Run("email already exists", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		existingUser := &User{
			ID:    "existing-user",
			Email: "existing@example.com",
		}
		mockStore.On("GetUserByEmail", ctx, "existing@example.com").Return(existingUser, nil).Once()

		req := CreateUserRequest{
			Email:    "existing@example.com",
			Password: "SecurePass@123",
			Role:     RoleUser,
		}

		user, err := service.CreateUser(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, user)
		assert.Contains(t, err.Error(), "email already in use")

		mockStore.AssertExpectations(t)
	})

	t.Run("invalid role", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "newuser@example.com").Return(nil, nil).Once()

		req := CreateUserRequest{
			Email:    "newuser@example.com",
			Password: "SecurePass@123",
			Role:     "invalid-role",
		}

		user, err := service.CreateUser(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, user)
		assert.Contains(t, err.Error(), "invalid role")

		mockStore.AssertExpectations(t)
	})

	t.Run("return error when GetUserByEmail fails", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "newuser@example.com").Return(nil, assert.AnError).Once()

		req := CreateUserRequest{
			Email:    "newuser@example.com",
			Password: "SecurePass@123",
			Role:     RoleUser,
		}

		user, err := service.CreateUser(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, user)

		mockStore.AssertExpectations(t)
	})

	t.Run("return error when CreateUser store operation fails", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "newuser@example.com").Return(nil, nil).Once()
		mockStore.On("CreateUser", ctx, mock.AnythingOfType("*auth.User")).Return(assert.AnError).Once()

		req := CreateUserRequest{
			Email:    "newuser@example.com",
			Password: "SecurePass@123",
			Role:     RoleUser,
		}

		user, err := service.CreateUser(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, user)

		mockStore.AssertExpectations(t)
	})
}

func TestService_DeleteUser(t *testing.T) {
	ctx := context.Background()

	t.Run("successful user deletion", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("DeleteUserSessions", ctx, "user-123").Return(nil).Once()
		mockStore.On("DeleteUser", ctx, "user-123").Return(nil).Once()

		err := service.DeleteUser(ctx, "user-123")
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})
}

func TestService_ListUsers(t *testing.T) {
	ctx := context.Background()

	t.Run("list users successfully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		users := []User{
			{ID: "user-1", Email: "user1@example.com"},
			{ID: "user-2", Email: "user2@example.com"},
		}

		mockStore.On("ListUsers", ctx).Return(users, nil).Once()

		result, err := service.ListUsers(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)

		mockStore.AssertExpectations(t)
	})
}

func TestService_GetUser(t *testing.T) {
	ctx := context.Background()

	t.Run("get user successfully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := &User{
			ID:    "user-123",
			Email: "test@example.com",
			Role:  RoleUser,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()

		user, err := service.GetUser(ctx, "user-123")
		require.NoError(t, err)
		assert.Equal(t, "user-123", user.ID)
		assert.Equal(t, "test@example.com", user.Email)

		mockStore.AssertExpectations(t)
	})

	t.Run("user not found", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByID", ctx, "nonexistent").Return(nil, nil).Once()

		user, err := service.GetUser(ctx, "nonexistent")
		require.NoError(t, err)
		assert.Nil(t, user)

		mockStore.AssertExpectations(t)
	})
}

func TestService_CheckAdminExists(t *testing.T) {
	ctx := context.Background()

	t.Run("admin exists", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("AdminExists", ctx).Return(true, nil).Once()

		exists, err := service.CheckAdminExists(ctx)
		require.NoError(t, err)
		assert.True(t, exists)

		mockStore.AssertExpectations(t)
	})

	t.Run("no admin", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("AdminExists", ctx).Return(false, nil).Once()

		exists, err := service.CheckAdminExists(ctx)
		require.NoError(t, err)
		assert.False(t, exists)

		mockStore.AssertExpectations(t)
	})
}

func TestService_UpdateUser(t *testing.T) {
	ctx := context.Background()

	t.Run("update role successfully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		existingUser := &User{
			ID:    "user-123",
			Email: "test@example.com",
			Role:  RoleUser,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(existingUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		newRole := RoleAdmin
		req := UpdateUserRequest{
			Role: &newRole,
		}

		user, err := service.UpdateUser(ctx, "user-123", req)
		require.NoError(t, err)
		assert.Equal(t, RoleAdmin, user.Role)

		mockStore.AssertExpectations(t)
	})

	t.Run("update groupIDs successfully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		existingUser := &User{
			ID:       "user-123",
			Email:    "test@example.com",
			Role:     RoleUser,
			GroupIDs: []string{"group-1"},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(existingUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		req := UpdateUserRequest{
			GroupIDs: []string{"group-2", "group-3"},
		}

		user, err := service.UpdateUser(ctx, "user-123", req)
		require.NoError(t, err)
		assert.Equal(t, []string{"group-2", "group-3"}, user.GroupIDs)

		mockStore.AssertExpectations(t)
	})

	t.Run("update active status successfully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		existingUser := &User{
			ID:     "user-123",
			Email:  "test@example.com",
			Role:   RoleUser,
			Active: true,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(existingUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		inactive := false
		req := UpdateUserRequest{
			Active: &inactive,
		}

		user, err := service.UpdateUser(ctx, "user-123", req)
		require.NoError(t, err)
		assert.False(t, user.Active)

		mockStore.AssertExpectations(t)
	})

	t.Run("update with invalid role", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		existingUser := &User{
			ID:    "user-123",
			Email: "test@example.com",
			Role:  RoleUser,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(existingUser, nil).Once()

		invalidRole := "superadmin"
		req := UpdateUserRequest{
			Role: &invalidRole,
		}

		user, err := service.UpdateUser(ctx, "user-123", req)
		assert.Error(t, err)
		assert.Nil(t, user)
		assert.Contains(t, err.Error(), "invalid role")

		mockStore.AssertExpectations(t)
	})

	t.Run("user not found", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByID", ctx, "nonexistent").Return(nil, nil).Once()

		newRole := RoleAdmin
		req := UpdateUserRequest{
			Role: &newRole,
		}

		user, err := service.UpdateUser(ctx, "nonexistent", req)
		assert.Error(t, err)
		assert.Nil(t, user)
		assert.Contains(t, err.Error(), "user not found")

		mockStore.AssertExpectations(t)
	})

	t.Run("update multiple fields at once", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		existingUser := &User{
			ID:       "user-123",
			Email:    "test@example.com",
			Role:     RoleUser,
			Active:   true,
			GroupIDs: []string{"group-1"},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(existingUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		newRole := RoleReadOnly
		active := false
		req := UpdateUserRequest{
			Role:     &newRole,
			Active:   &active,
			GroupIDs: []string{"group-2"},
		}

		user, err := service.UpdateUser(ctx, "user-123", req)
		require.NoError(t, err)
		assert.Equal(t, RoleReadOnly, user.Role)
		assert.False(t, user.Active)
		assert.Equal(t, []string{"group-2"}, user.GroupIDs)

		mockStore.AssertExpectations(t)
	})
}

func TestService_CreateUser_EdgeCases(t *testing.T) {
	ctx := context.Background()

	t.Run("create user with invalid email", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		req := CreateUserRequest{
			Email:    "not-an-email",
			Password: "SecurePass@123",
			Role:     RoleUser,
		}

		user, err := service.CreateUser(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, user)
		assert.Contains(t, err.Error(), "invalid email format")
	})

	t.Run("create user with weak password", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "newuser@example.com").Return(nil, nil).Once()

		req := CreateUserRequest{
			Email:    "newuser@example.com",
			Password: "weak",
			Role:     RoleUser,
		}

		user, err := service.CreateUser(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, user)

		mockStore.AssertExpectations(t)
	})

	t.Run("create user with group IDs", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "newuser@example.com").Return(nil, nil).Once()
		mockStore.On("CreateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		req := CreateUserRequest{
			Email:    "newuser@example.com",
			Password: "SecurePass@123",
			Role:     RoleUser,
			GroupIDs: []string{"group-1", "group-2"},
		}

		user, err := service.CreateUser(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, user)
		assert.Equal(t, []string{"group-1", "group-2"}, user.GroupIDs)

		mockStore.AssertExpectations(t)
	})

	t.Run("create admin user", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "admin@example.com").Return(nil, nil).Once()
		mockStore.On("CreateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		req := CreateUserRequest{
			Email:    "admin@example.com",
			Password: "SecurePass@123",
			Role:     RoleAdmin,
		}

		user, err := service.CreateUser(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, user)
		assert.Equal(t, RoleAdmin, user.Role)

		mockStore.AssertExpectations(t)
	})

	t.Run("create readonly user", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "readonly@example.com").Return(nil, nil).Once()
		mockStore.On("CreateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		req := CreateUserRequest{
			Email:    "readonly@example.com",
			Password: "SecurePass@123",
			Role:     RoleReadOnly,
		}

		user, err := service.CreateUser(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, user)
		assert.Equal(t, RoleReadOnly, user.Role)

		mockStore.AssertExpectations(t)
	})
}

func TestService_SetupAdmin_EdgeCases(t *testing.T) {
	ctx := context.Background()

	t.Run("admin creation fails", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("AdminExists", ctx).Return(false, nil).Once()
		mockStore.On("CreateUser", ctx, mock.AnythingOfType("*auth.User")).Return(fmt.Errorf("database error")).Once()

		req := SetupAdminRequest{
			Email:    "admin@example.com",
			Password: "SecurePass@123",
		}

		resp, err := service.SetupAdmin(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "failed to create admin")

		mockStore.AssertExpectations(t)
	})

	t.Run("admin exists check fails", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("AdminExists", ctx).Return(false, fmt.Errorf("database error")).Once()

		req := SetupAdminRequest{
			Email:    "admin@example.com",
			Password: "SecurePass@123",
		}

		resp, err := service.SetupAdmin(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "failed to check admin")

		mockStore.AssertExpectations(t)
	})
}

// Test TOTP functions
func TestService_UpdateUserProfile(t *testing.T) {
	ctx := context.Background()

	t.Run("update email and password successfully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		// Create user with bcrypt hash for UpdateUserProfile test
		hash, _ := bcrypt.GenerateFromPassword([]byte("OldPassword123"), bcrypt.DefaultCost)
		testUser := &User{
			ID:           "user-123",
			Email:        "old@example.com",
			PasswordHash: string(hash),
			Role:         RoleUser,
			Active:       true,
			CreatedAt:    time.Now(),
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		err := service.UpdateUserProfile(ctx, "user-123", "new@example.com", "OldPassword123", "SecureTest@456")
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})

	t.Run("wrong current password", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		hash, _ := bcrypt.GenerateFromPassword([]byte("OldPassword123"), bcrypt.DefaultCost)
		testUser := &User{
			ID:           "user-123",
			Email:        "old@example.com",
			PasswordHash: string(hash),
			Role:         RoleUser,
			Active:       true,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()

		err := service.UpdateUserProfile(ctx, "user-123", "new@example.com", "WrongPassword", "SecureTest@456")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "current password is incorrect")

		mockStore.AssertExpectations(t)
	})

	t.Run("invalid email format", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		hash, _ := bcrypt.GenerateFromPassword([]byte("OldPassword123"), bcrypt.DefaultCost)
		testUser := &User{
			ID:           "user-123",
			Email:        "old@example.com",
			PasswordHash: string(hash),
			Role:         RoleUser,
			Active:       true,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()

		err := service.UpdateUserProfile(ctx, "user-123", "invalid-email", "OldPassword123", "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid email format")

		mockStore.AssertExpectations(t)
	})

	t.Run("weak new password", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		hash, _ := bcrypt.GenerateFromPassword([]byte("OldPassword123"), bcrypt.DefaultCost)
		testUser := &User{
			ID:           "user-123",
			Email:        "old@example.com",
			PasswordHash: string(hash),
			Role:         RoleUser,
			Active:       true,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()

		err := service.UpdateUserProfile(ctx, "user-123", "", "OldPassword123", "weak")
		assert.Error(t, err)

		mockStore.AssertExpectations(t)
	})

	t.Run("user not found", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByID", ctx, "user-123").Return(nil, nil).Once()

		err := service.UpdateUserProfile(ctx, "user-123", "", "OldPassword123", "SecureTest@456")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user not found")

		mockStore.AssertExpectations(t)
	})

	t.Run("update email only", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		hash, _ := bcrypt.GenerateFromPassword([]byte("OldPassword123"), bcrypt.DefaultCost)
		testUser := &User{
			ID:           "user-123",
			Email:        "old@example.com",
			PasswordHash: string(hash),
			Role:         RoleUser,
			Active:       true,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		err := service.UpdateUserProfile(ctx, "user-123", "new@example.com", "OldPassword123", "")
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})
}

// Test API conversion helpers
