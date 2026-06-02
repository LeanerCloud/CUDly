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
		// Capture the user passed to CreateAdminIfNone so we can assert
		// on the auto-assigned DefaultAdminGroupID. CreateAdminIfNone
		// replaces CreateUser on the bootstrap path (issue #349).
		var capturedUser *User
		mockStore.On("CreateAdminIfNone", ctx, mock.AnythingOfType("*auth.User")).
			Run(func(args mock.Arguments) { capturedUser = args.Get(1).(*User) }).
			Return(true, nil).Once()
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
		assert.Equal(t, []string{DefaultAdminGroupID}, resp.User.Groups)

		// Verify the admin user was auto-assigned to the Administrators group.
		require.NotNil(t, capturedUser)
		assert.Equal(t, []string{DefaultAdminGroupID}, capturedUser.GroupIDs)

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
			GroupIDs: []string{DefaultAdminGroupID},
		}

		result, err := service.CreateUser(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.User)
		assert.Equal(t, "newuser@example.com", result.User.Email)
		assert.Equal(t, []string{DefaultAdminGroupID}, result.User.GroupIDs)
		assert.True(t, result.User.Active)
		// Non-invite path: no invite-email status.
		assert.Nil(t, result.InviteEmailSent)
		assert.Empty(t, result.InviteEmailError)

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
			GroupIDs: []string{DefaultAdminGroupID},
		}

		result, err := service.CreateUser(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "email already in use")

		mockStore.AssertExpectations(t)
	})

	t.Run("zero groups rejected", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "newuser@example.com").Return(nil, nil).Once()

		// Authorization is group-membership-only (issue #907): a user with no
		// groups can do nothing, so creation must be rejected.
		req := CreateUserRequest{
			Email:    "newuser@example.com",
			Password: "SecurePass@123",
			GroupIDs: nil,
		}

		result, err := service.CreateUser(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrNoGroups)

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
			GroupIDs: []string{DefaultAdminGroupID},
		}

		result, err := service.CreateUser(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, result)

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
			GroupIDs: []string{DefaultAdminGroupID},
		}

		result, err := service.CreateUser(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, result)

		mockStore.AssertExpectations(t)
	})

	t.Run("invite flow when password omitted", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "invitee@example.com").Return(nil, nil).Once()

		var captured *User
		mockStore.On("CreateUser", ctx, mock.AnythingOfType("*auth.User")).
			Run(func(args mock.Arguments) { captured = args.Get(1).(*User) }).
			Return(nil).Once()
		mockEmail.On("SendUserInviteEmail", ctx, "invitee@example.com", mock.AnythingOfType("string")).
			Return(nil).Once()

		req := CreateUserRequest{
			Email:    "invitee@example.com",
			GroupIDs: []string{DefaultAdminGroupID},
			// Password intentionally empty — admin is inviting the user.
		}

		result, err := service.CreateUser(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, captured)

		// Invited users land inactive and only flip to active after they
		// set their password through the welcome-link flow.
		assert.False(t, captured.Active)

		// A setup token must be stored so the welcome-link flow can find
		// the user; the placeholder hash must not be empty so the
		// password_hash NOT NULL constraint is satisfied and no client
		// input can match it.
		assert.NotEmpty(t, captured.PasswordResetToken)
		require.NotNil(t, captured.PasswordResetExpiry)
		assert.True(t, captured.PasswordResetExpiry.After(time.Now()))
		assert.NotEmpty(t, captured.PasswordHash)

		// Email delivery succeeded — caller should see invite_email_sent=true.
		require.NotNil(t, result.InviteEmailSent)
		assert.True(t, *result.InviteEmailSent)
		assert.Empty(t, result.InviteEmailError)

		mockStore.AssertExpectations(t)
		mockEmail.AssertExpectations(t)
	})

	t.Run("invite flow surfaces delivery failure without 5xx", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "invitee@example.com").Return(nil, nil).Once()
		mockStore.On("CreateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()
		mockEmail.On("SendUserInviteEmail", ctx, "invitee@example.com", mock.AnythingOfType("string")).
			Return(assert.AnError).Once()

		req := CreateUserRequest{
			Email:    "invitee@example.com",
			GroupIDs: []string{DefaultAdminGroupID},
		}

		result, err := service.CreateUser(ctx, req)
		// The user row exists, so the caller must not see an error —
		// otherwise the admin assumes the operation rolled back and may
		// re-submit, hitting the duplicate-email guard. The delivery
		// failure is surfaced via the result fields instead so the UI
		// can show a warning and point the admin at Forgot Password.
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.User)
		require.NotNil(t, result.InviteEmailSent)
		assert.False(t, *result.InviteEmailSent)
		assert.NotEmpty(t, result.InviteEmailError)

		mockStore.AssertExpectations(t)
		mockEmail.AssertExpectations(t)
	})
}

func TestService_DeleteUser(t *testing.T) {
	ctx := context.Background()

	t.Run("successful user deletion", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		// Non-admin user: last-admin guard is skipped, so deletion proceeds.
		mockStore.On("GetUserByID", ctx, "user-123").
			Return(&User{ID: "user-123", GroupIDs: []string{"group-1"}}, nil).Once()
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
			ID:       "user-123",
			Email:    "test@example.com",
			GroupIDs: []string{DefaultAdminGroupID},
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

	t.Run("update groupIDs successfully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		existingUser := &User{
			ID:       "user-123",
			Email:    "test@example.com",
			GroupIDs: []string{"group-1"},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(existingUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		req := UpdateUserRequest{
			GroupIDs: []string{"group-2", "group-3"},
		}

		// Internal/admin caller (actorUserID == "") skips the self-escalation
		// guard; the group change is neither emptying nor removing the last
		// admin, so it succeeds.
		user, err := service.UpdateUser(ctx, "", "user-123", req)
		require.NoError(t, err)
		assert.Equal(t, []string{"group-2", "group-3"}, user.GroupIDs)

		mockStore.AssertExpectations(t)
	})

	t.Run("update active status successfully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		existingUser := &User{
			ID:       "user-123",
			Email:    "test@example.com",
			GroupIDs: []string{"group-1"},
			Active:   true,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(existingUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		inactive := false
		req := UpdateUserRequest{
			Active: &inactive,
		}

		user, err := service.UpdateUser(ctx, "", "user-123", req)
		require.NoError(t, err)
		assert.False(t, user.Active)

		mockStore.AssertExpectations(t)
	})

	t.Run("empty groups rejected", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		existingUser := &User{
			ID:       "user-123",
			Email:    "test@example.com",
			GroupIDs: []string{"group-1"},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(existingUser, nil).Once()

		// A non-nil but empty GroupIDs would leave a zero-group user, which
		// authorization-as-group-membership (issue #907) forbids.
		req := UpdateUserRequest{
			GroupIDs: []string{},
		}

		user, err := service.UpdateUser(ctx, "", "user-123", req)
		assert.Error(t, err)
		assert.Nil(t, user)
		assert.ErrorIs(t, err, ErrNoGroups)

		mockStore.AssertExpectations(t)
	})

	t.Run("user not found", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByID", ctx, "nonexistent").Return(nil, nil).Once()

		req := UpdateUserRequest{
			GroupIDs: []string{"group-1"},
		}

		user, err := service.UpdateUser(ctx, "", "nonexistent", req)
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
			Active:   true,
			GroupIDs: []string{"group-1"},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(existingUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		active := false
		req := UpdateUserRequest{
			Active:   &active,
			GroupIDs: []string{"group-2"},
		}

		user, err := service.UpdateUser(ctx, "", "user-123", req)
		require.NoError(t, err)
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
			GroupIDs: []string{DefaultAdminGroupID},
		}

		result, err := service.CreateUser(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, result)
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
			GroupIDs: []string{DefaultAdminGroupID},
		}

		result, err := service.CreateUser(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, result)

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
			GroupIDs: []string{"group-1", "group-2"},
		}

		result, err := service.CreateUser(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.User)
		assert.Equal(t, []string{"group-1", "group-2"}, result.User.GroupIDs)

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
			GroupIDs: []string{DefaultAdminGroupID},
		}

		result, err := service.CreateUser(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.User)
		assert.Equal(t, []string{DefaultAdminGroupID}, result.User.GroupIDs)

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
			GroupIDs: []string{"00000000-0000-5000-8000-000000000006"},
		}

		result, err := service.CreateUser(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.User)
		assert.Equal(t, []string{"00000000-0000-5000-8000-000000000006"}, result.User.GroupIDs)

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
		// Bootstrap path now calls CreateAdminIfNone (issue #349).
		mockStore.On("CreateAdminIfNone", ctx, mock.AnythingOfType("*auth.User")).Return(false, fmt.Errorf("database error")).Once()

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

	// TestSetupAdmin TOCTOU fix (issue #349, migration 000050): when
	// AdminExists() reports false but a concurrent SetupAdmin caller wins
	// the race to insert, the conditional INSERT-WHERE-NOT-EXISTS in
	// CreateAdminIfNone returns (false, nil) — meaning the row was not
	// inserted because the WHERE clause failed. The service must
	// translate that into ErrAdminExists, NOT a generic wrapped error.
	t.Run("admin creation loses the race", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("AdminExists", ctx).Return(false, nil).Once()
		// Simulate the conditional insert finding that another admin
		// already exists (inserted=false, no error).
		mockStore.On("CreateAdminIfNone", ctx, mock.AnythingOfType("*auth.User")).Return(false, nil).Once()

		req := SetupAdminRequest{
			Email:    "admin@example.com",
			Password: "SecurePass@123",
		}

		resp, err := service.SetupAdmin(ctx, req)
		require.Error(t, err)
		assert.Nil(t, resp)
		assert.ErrorIs(t, err, ErrAdminExists)
		// Do NOT leak the "failed to create admin" wrapper — the error
		// surface should be indistinguishable from the pre-race existence
		// check.
		assert.NotContains(t, err.Error(), "failed to create admin")

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
			Active:       true,
			CreatedAt:    time.Now(),
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()
		mockStore.On("GetUserByEmail", ctx, "new@example.com").Return(nil, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()
		mockStore.On("DeleteUserSessions", ctx, "user-123").Return(nil).Once()

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
			Active:       true,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()
		mockStore.On("GetUserByEmail", ctx, "new@example.com").Return(nil, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		err := service.UpdateUserProfile(ctx, "user-123", "new@example.com", "OldPassword123", "")
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})
}

// Test API conversion helpers
