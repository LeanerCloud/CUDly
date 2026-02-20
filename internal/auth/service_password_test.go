package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestService_ChangePassword(t *testing.T) {
	ctx := context.Background()

	t.Run("successful password change", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "OldSecure123!")

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()
		mockStore.On("DeleteUserSessions", ctx, "user-123").Return(nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		req := ChangePasswordRequest{
			CurrentPassword: "OldSecure123!",
			NewPassword:     "NewSecure@456",
		}

		err := service.ChangePassword(ctx, "user-123", req)
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})

	t.Run("wrong old password", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "OldSecure123!")

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()

		req := ChangePasswordRequest{
			CurrentPassword: "WrongSecure123!",
			NewPassword:     "NewSecure@456",
		}

		err := service.ChangePassword(ctx, "user-123", req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "current password is incorrect")

		mockStore.AssertExpectations(t)
	})

	t.Run("weak new password", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "OldSecure123!")

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()

		req := ChangePasswordRequest{
			CurrentPassword: "OldSecure123!",
			NewPassword:     "weak",
		}

		err := service.ChangePassword(ctx, "user-123", req)
		assert.Error(t, err)

		mockStore.AssertExpectations(t)
	})

	t.Run("user not found", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByID", ctx, "user-123").Return(nil, nil).Once()

		req := ChangePasswordRequest{
			CurrentPassword: "OldSecure123!",
			NewPassword:     "NewSecure@456",
		}

		err := service.ChangePassword(ctx, "user-123", req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user not found")

		mockStore.AssertExpectations(t)
	})

	t.Run("password reuse prevention - current password", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		// Create user with password history (using a non-common password)
		currentPass := "MyCurrentS3cur3!"
		testUser := createTestUser(t, currentPass)

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()

		// Try to reuse the same (current) password
		req := ChangePasswordRequest{
			CurrentPassword: currentPass,
			NewPassword:     currentPass,
		}

		err := service.ChangePassword(ctx, "user-123", req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "used recently")

		mockStore.AssertExpectations(t)
	})

	t.Run("password history maintained", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		// Create user with existing password history
		testUser := createTestUser(t, "CurrentS3cur3!")
		originalHash := testUser.PasswordHash // Save the original hash
		hash1, _ := service.hashPassword("HistoryS3cur31@")
		hash2, _ := service.hashPassword("HistoryS3cur32@")
		testUser.PasswordHistory = []string{hash1, hash2}

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()
		mockStore.On("DeleteUserSessions", ctx, "user-123").Return(nil).Once()
		mockStore.On("UpdateUser", ctx, mock.MatchedBy(func(u *User) bool {
			// Verify password history includes old password and maintains limit
			// Should have: original current password (newly added to history) + 2 existing = 3 total
			return len(u.PasswordHistory) == 3 &&
				len(u.PasswordHistory) <= passwordHistorySize &&
				u.PasswordHistory[0] == originalHash && // Original current password should be first in history
				u.PasswordHistory[1] == hash1 && // Previous history items should follow
				u.PasswordHistory[2] == hash2
		})).Return(nil).Once()

		req := ChangePasswordRequest{
			CurrentPassword: "CurrentS3cur3!",
			NewPassword:     "BrandNewS3cur3@789",
		}

		err := service.ChangePassword(ctx, "user-123", req)
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})

	t.Run("cannot reuse password from history", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		// Create user with password history
		oldPasswordFromHistory := "OldHistoryS3cur31!"
		testUser := createTestUser(t, "CurrentS3cur3!")
		hash1, _ := service.hashPassword(oldPasswordFromHistory)
		testUser.PasswordHistory = []string{hash1}

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()

		// Try to reuse a password from history
		req := ChangePasswordRequest{
			CurrentPassword: "CurrentS3cur3!",
			NewPassword:     oldPasswordFromHistory,
		}

		err := service.ChangePassword(ctx, "user-123", req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "used recently")

		mockStore.AssertExpectations(t)
	})
}

func TestService_RequestPasswordReset(t *testing.T) {
	ctx := context.Background()

	t.Run("successful password reset request", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "SecureS3cur3@123")

		mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()
		mockEmail.On("SendPasswordResetEmail", ctx, "test@example.com", mock.AnythingOfType("string")).Return(nil).Once()

		err := service.RequestPasswordReset(ctx, "test@example.com")
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
		mockEmail.AssertExpectations(t)
	})

	t.Run("user not found - no error for security", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "notfound@example.com").Return(nil, nil).Once()

		// Should not return error to prevent email enumeration
		err := service.RequestPasswordReset(ctx, "notfound@example.com")
		assert.NoError(t, err)

		mockStore.AssertExpectations(t)
	})

	t.Run("return error when GetUserByEmail fails", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(nil, assert.AnError).Once()

		err := service.RequestPasswordReset(ctx, "test@example.com")
		assert.Error(t, err)

		mockStore.AssertExpectations(t)
	})

	t.Run("return error when UpdateUser fails", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "SecureS3cur3@123")

		mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(assert.AnError).Once()

		err := service.RequestPasswordReset(ctx, "test@example.com")
		assert.Error(t, err)

		mockStore.AssertExpectations(t)
	})

	t.Run("continue when email send fails - no error for security", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "SecureS3cur3@123")

		mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()
		mockEmail.On("SendPasswordResetEmail", ctx, "test@example.com", mock.AnythingOfType("string")).Return(assert.AnError).Once()

		// Should not return error to prevent email enumeration
		err := service.RequestPasswordReset(ctx, "test@example.com")
		assert.NoError(t, err)

		mockStore.AssertExpectations(t)
		mockEmail.AssertExpectations(t)
	})
}

func TestService_ConfirmPasswordReset(t *testing.T) {
	ctx := context.Background()

	t.Run("successful password reset", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		expiry := time.Now().Add(time.Hour)
		testUser := &User{
			ID:                  "user-123",
			Email:               "test@example.com",
			PasswordResetToken:  "hashed-token", // Now stores the hash
			PasswordResetExpiry: &expiry,
			Active:              true,
		}

		// Token is hashed before lookup, use mock.Anything to match the hash
		mockStore.On("GetUserByResetToken", ctx, mock.AnythingOfType("string")).Return(testUser, nil).Once()
		mockStore.On("DeleteUserSessions", ctx, "user-123").Return(nil).Once()
		// UpdateUser is called once: password change + token invalidation in single call
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		req := PasswordResetConfirm{
			Token:       "valid-reset-token",
			NewPassword: "SecureT3st@789",
		}

		err := service.ConfirmPasswordReset(ctx, req)
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})

	t.Run("invalid token", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		// Token is hashed before lookup, use mock.Anything to match the hash
		mockStore.On("GetUserByResetToken", ctx, mock.AnythingOfType("string")).Return(nil, nil).Once()

		req := PasswordResetConfirm{
			Token:       "invalid-token",
			NewPassword: "SecureT3st@789",
		}

		err := service.ConfirmPasswordReset(ctx, req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid or expired reset token")

		mockStore.AssertExpectations(t)
	})

	t.Run("expired token", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		expiry := time.Now().Add(-time.Hour)
		expiredUser := &User{
			ID:                  "user-456",
			Email:               "expired@example.com",
			PasswordResetToken:  "hashed-expired-token",
			PasswordResetExpiry: &expiry,
			Active:              true,
		}

		// Token is hashed before lookup
		mockStore.On("GetUserByResetToken", ctx, mock.AnythingOfType("string")).Return(expiredUser, nil).Once()

		req := PasswordResetConfirm{
			Token:       "expired-reset-token",
			NewPassword: "SecureT3st@789",
		}

		err := service.ConfirmPasswordReset(ctx, req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expired")

		mockStore.AssertExpectations(t)
	})

	t.Run("weak new password", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		expiry := time.Now().Add(time.Hour)
		testUser := &User{
			ID:                  "user-123",
			Email:               "test@example.com",
			PasswordResetToken:  "hashed-token",
			PasswordResetExpiry: &expiry,
			Active:              true,
		}

		// Token is hashed before lookup
		mockStore.On("GetUserByResetToken", ctx, mock.AnythingOfType("string")).Return(testUser, nil).Once()
		// Token is invalidated even on password validation failure (one-time use)
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		req := PasswordResetConfirm{
			Token:       "valid-reset-token",
			NewPassword: "weak",
		}

		err := service.ConfirmPasswordReset(ctx, req)
		assert.Error(t, err)

		mockStore.AssertExpectations(t)
	})

	t.Run("password history checked on reset", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		oldPasswordFromHistory := "OldHistoryS3cur31!"
		hash, _ := service.hashPassword(oldPasswordFromHistory)

		expiry := time.Now().Add(time.Hour)
		testUser := &User{
			ID:                  "user-123",
			Email:               "test@example.com",
			PasswordResetToken:  "hashed-token",
			PasswordResetExpiry: &expiry,
			PasswordHistory:     []string{hash},
			Active:              true,
		}

		mockStore.On("GetUserByResetToken", ctx, mock.AnythingOfType("string")).Return(testUser, nil).Once()
		// Token is invalidated even on password validation failure (one-time use)
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		// Try to reuse a password from history
		req := PasswordResetConfirm{
			Token:       "valid-reset-token",
			NewPassword: oldPasswordFromHistory,
		}

		err := service.ConfirmPasswordReset(ctx, req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "used recently")

		mockStore.AssertExpectations(t)
	})
}

// Test password validation rules
func TestValidatePassword(t *testing.T) {
	service := &Service{}

	tests := []struct {
		name     string
		password string
		wantErr  bool
		errMsg   string
	}{
		{
			name:     "valid strong password",
			password: "StrongS3cur3!",
			wantErr:  false,
		},
		{
			name:     "too short",
			password: "Short1!",
			wantErr:  true,
			errMsg:   "at least 12 characters",
		},
		{
			name:     "too long",
			password: "VeryLongS3cur3!" + string(make([]byte, 120)),
			wantErr:  true,
			errMsg:   "must not exceed 128 characters",
		},
		{
			name:     "missing uppercase",
			password: "lowercases3!",
			wantErr:  true,
			errMsg:   "uppercase letter",
		},
		{
			name:     "missing lowercase",
			password: "UPPERCASES3!",
			wantErr:  true,
			errMsg:   "lowercase letter",
		},
		{
			name:     "missing number",
			password: "NoNumberSecure!",
			wantErr:  true,
			errMsg:   "one number",
		},
		{
			name:     "missing special character",
			password: "NoSpecialS3cur3",
			wantErr:  true,
			errMsg:   "special character",
		},
		{
			name:     "common password is rejected by other rules first",
			password: "password",
			wantErr:  true,
			errMsg:   "", // fails length/complexity before reaching common check
		},
		{
			name:     "qwerty substring is now allowed",
			password: "MyQwerty123!",
			wantErr:  false,
		},
		{
			name:     "admin substring is now allowed",
			password: "MyAdmin@12345678",
			wantErr:  false,
		},
		{
			name:     "sequential identical chars - aaa",
			password: "S3cureAaaa123!",
			wantErr:  true,
			errMsg:   "identical consecutive characters",
		},
		{
			name:     "sequential identical chars - 111",
			password: "S3cure1111xyz!",
			wantErr:  true,
			errMsg:   "identical consecutive characters",
		},
		{
			name:     "sequential identical chars - ###",
			password: "S3cureXyz###1",
			wantErr:  true,
			errMsg:   "identical consecutive characters",
		},
		{
			name:     "two identical chars ok",
			password: "S3cureXyz11!",
			wantErr:  false,
		},
		{
			name:     "valid with special chars",
			password: "C0mpl3x!S3cur3",
			wantErr:  false,
		},
		{
			name:     "valid with various special chars",
			password: "My$ecur3#S3cur3!",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.validatePassword(tt.password)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test checkCommonPasswords uses exact match (not substring)
func TestCheckCommonPasswords(t *testing.T) {
	service := &Service{}

	// Exact common password should be rejected
	assert.Error(t, service.checkCommonPasswords("password"))
	assert.Error(t, service.checkCommonPasswords("PASSWORD")) // case insensitive
	assert.Error(t, service.checkCommonPasswords("admin123"))

	// Passwords containing common words as substrings should be allowed
	assert.NoError(t, service.checkCommonPasswords("MyPassword123!"))
	assert.NoError(t, service.checkCommonPasswords("SuperAdmin2024"))
}

// Test containsSequentialChars function
func TestContainsSequentialChars(t *testing.T) {
	tests := []struct {
		name     string
		password string
		n        int
		want     bool
	}{
		{
			name:     "three identical chars",
			password: "aaa",
			n:        3,
			want:     true,
		},
		{
			name:     "three identical chars in middle",
			password: "pa111ssword",
			n:        3,
			want:     true,
		},
		{
			name:     "four identical chars",
			password: "pass1111word",
			n:        3,
			want:     true,
		},
		{
			name:     "two identical chars only",
			password: "password11",
			n:        3,
			want:     false,
		},
		{
			name:     "no sequential chars",
			password: "password123",
			n:        3,
			want:     false,
		},
		{
			name:     "special chars sequential",
			password: "pass###word",
			n:        3,
			want:     true,
		},
		{
			name:     "empty password",
			password: "",
			n:        3,
			want:     false,
		},
		{
			name:     "n greater than password length",
			password: "ab",
			n:        3,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsSequentialChars(tt.password, tt.n)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Test addToPasswordHistory function
func TestAddToPasswordHistory(t *testing.T) {
	tests := []struct {
		name            string
		currentHash     string
		existingHistory []string
		expectedLen     int
		expectedFirst   string
	}{
		{
			name:            "empty history",
			currentHash:     "hash1",
			existingHistory: []string{},
			expectedLen:     1,
			expectedFirst:   "hash1",
		},
		{
			name:            "history with one item",
			currentHash:     "hash2",
			existingHistory: []string{"hash1"},
			expectedLen:     2,
			expectedFirst:   "hash2",
		},
		{
			name:            "history at limit",
			currentHash:     "hash6",
			existingHistory: []string{"hash5", "hash4", "hash3", "hash2", "hash1"},
			expectedLen:     5,
			expectedFirst:   "hash6",
		},
		{
			name:            "history below limit",
			currentHash:     "hash4",
			existingHistory: []string{"hash3", "hash2", "hash1"},
			expectedLen:     4,
			expectedFirst:   "hash4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := addToPasswordHistory(tt.currentHash, tt.existingHistory)
			assert.Equal(t, tt.expectedLen, len(result))
			assert.Equal(t, tt.expectedFirst, result[0])
			// Ensure we don't exceed the limit
			assert.LessOrEqual(t, len(result), passwordHistorySize)
		})
	}
}

// Test checkPasswordHistory
func TestCheckPasswordHistory(t *testing.T) {
	service := &Service{}

	t.Run("password not in history", func(t *testing.T) {
		newPassword := "NewS3cur3123!"
		currentHash, _ := service.hashPassword("CurrentS3cur3!")
		hash1, _ := service.hashPassword("OldS3cur31!")
		hash2, _ := service.hashPassword("OldS3cur32!")

		err := service.checkPasswordHistory(newPassword, currentHash, []string{hash1, hash2})
		assert.NoError(t, err)
	})

	t.Run("password matches current hash", func(t *testing.T) {
		currentPassword := "CurrentS3cur3!"
		currentHash, _ := service.hashPassword(currentPassword)

		err := service.checkPasswordHistory(currentPassword, currentHash, []string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "used recently")
	})

	t.Run("password found in history", func(t *testing.T) {
		oldPassword := "OldS3cur3123!"
		currentHash, _ := service.hashPassword("CurrentS3cur3!")
		hash, _ := service.hashPassword(oldPassword)

		err := service.checkPasswordHistory(oldPassword, currentHash, []string{hash})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "used recently")
	})

	t.Run("empty history and current", func(t *testing.T) {
		newPassword := "NewS3cur3123!"
		err := service.checkPasswordHistory(newPassword, "", []string{})
		assert.NoError(t, err)
	})

	t.Run("password found in middle of history", func(t *testing.T) {
		oldPassword := "OldS3cur3123!"
		currentHash, _ := service.hashPassword("CurrentS3cur3!")
		hash1, _ := service.hashPassword("DifferentS3cur31!")
		hash2, _ := service.hashPassword(oldPassword)
		hash3, _ := service.hashPassword("DifferentS3cur32!")

		err := service.checkPasswordHistory(oldPassword, currentHash, []string{hash1, hash2, hash3})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "used recently")
	})
}
