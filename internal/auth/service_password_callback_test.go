package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestService_OnPasswordChange_ChangePassword(t *testing.T) {
	ctx := context.Background()

	t.Run("callback called with correct plaintext password", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)

		var capturedUserID, capturedPassword string
		service := &Service{
			store:              mockStore,
			emailSender:        mockEmail,
			sessionDuration:    24 * time.Hour,
			bcryptCostOverride: bcrypt.MinCost,
			onPasswordChange: func(_ context.Context, userID, newPassword string) {
				capturedUserID = userID
				capturedPassword = newPassword
			},
		}

		testUser := createTestUser(t, "OldSecure123!")

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()
		mockStore.On("DeleteUserSessions", ctx, "user-123").Return(nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		err := service.ChangePassword(ctx, "user-123", ChangePasswordRequest{
			CurrentPassword: "OldSecure123!",
			NewPassword:     "NewSecure@456",
		})
		require.NoError(t, err)
		assert.Equal(t, "user-123", capturedUserID)
		assert.Equal(t, "NewSecure@456", capturedPassword)
		mockStore.AssertExpectations(t)
	})

	t.Run("nil callback does not panic", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "OldSecure123!")

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()
		mockStore.On("DeleteUserSessions", ctx, "user-123").Return(nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		err := service.ChangePassword(ctx, "user-123", ChangePasswordRequest{
			CurrentPassword: "OldSecure123!",
			NewPassword:     "NewSecure@456",
		})
		require.NoError(t, err)
		mockStore.AssertExpectations(t)
	})

	t.Run("callback not called on update failure", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)

		callbackCalled := false
		service := &Service{
			store:              mockStore,
			emailSender:        mockEmail,
			sessionDuration:    24 * time.Hour,
			bcryptCostOverride: bcrypt.MinCost,
			onPasswordChange: func(_ context.Context, _, _ string) {
				callbackCalled = true
			},
		}

		testUser := createTestUser(t, "OldSecure123!")

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()
		mockStore.On("DeleteUserSessions", ctx, "user-123").Return(nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(assert.AnError).Once()

		err := service.ChangePassword(ctx, "user-123", ChangePasswordRequest{
			CurrentPassword: "OldSecure123!",
			NewPassword:     "NewSecure@456",
		})
		require.Error(t, err)
		assert.False(t, callbackCalled, "callback should not be called when UpdateUser fails")
		mockStore.AssertExpectations(t)
	})
}

func TestService_OnPasswordChange_ConfirmPasswordReset(t *testing.T) {
	ctx := context.Background()

	t.Run("callback called on successful password reset", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)

		var capturedUserID, capturedPassword string
		service := &Service{
			store:              mockStore,
			emailSender:        mockEmail,
			sessionDuration:    24 * time.Hour,
			bcryptCostOverride: bcrypt.MinCost,
			onPasswordChange: func(_ context.Context, userID, newPassword string) {
				capturedUserID = userID
				capturedPassword = newPassword
			},
		}

		expiry := time.Now().Add(1 * time.Hour)
		testUser := &User{
			ID:                  "user-456",
			Email:               "admin@example.com",
			PasswordHash:        "", // First password set
			Role:                RoleAdmin,
			Active:              false,
			PasswordResetToken:  hashSessionToken("valid-token"),
			PasswordResetExpiry: &expiry,
		}

		mockStore.On("GetUserByResetToken", ctx, mock.AnythingOfType("string")).Return(testUser, nil).Once()
		mockStore.On("DeleteUserSessions", ctx, "user-456").Return(nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		err := service.ConfirmPasswordReset(ctx, PasswordResetConfirm{
			Token:       "valid-token",
			NewPassword: "NewSecure@456",
		})
		require.NoError(t, err)
		assert.Equal(t, "user-456", capturedUserID)
		assert.Equal(t, "NewSecure@456", capturedPassword)
		mockStore.AssertExpectations(t)
	})
}

func TestService_OnPasswordChange_UpdateUserProfile(t *testing.T) {
	ctx := context.Background()

	t.Run("callback called when password changed via profile update", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)

		var capturedUserID, capturedPassword string
		service := &Service{
			store:              mockStore,
			emailSender:        mockEmail,
			sessionDuration:    24 * time.Hour,
			bcryptCostOverride: bcrypt.MinCost,
			onPasswordChange: func(_ context.Context, userID, newPassword string) {
				capturedUserID = userID
				capturedPassword = newPassword
			},
		}

		testUser := createTestUser(t, "OldSecure123!")

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()
		mockStore.On("DeleteUserSessions", ctx, "user-123").Return(nil).Once()

		err := service.UpdateUserProfile(ctx, "user-123", "", "OldSecure123!", "NewSecure@456")
		require.NoError(t, err)
		assert.Equal(t, "user-123", capturedUserID)
		assert.Equal(t, "NewSecure@456", capturedPassword)
		mockStore.AssertExpectations(t)
	})

	t.Run("callback not called when only email changed", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)

		callbackCalled := false
		service := &Service{
			store:              mockStore,
			emailSender:        mockEmail,
			sessionDuration:    24 * time.Hour,
			bcryptCostOverride: bcrypt.MinCost,
			onPasswordChange: func(_ context.Context, _, _ string) {
				callbackCalled = true
			},
		}

		testUser := createTestUser(t, "OldSecure123!")

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()
		mockStore.On("GetUserByEmail", ctx, "new@example.com").Return(nil, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		err := service.UpdateUserProfile(ctx, "user-123", "new@example.com", "OldSecure123!", "")
		require.NoError(t, err)
		assert.False(t, callbackCalled, "callback should not be called when password not changed")
		mockStore.AssertExpectations(t)
	})
}
