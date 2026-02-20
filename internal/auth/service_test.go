package auth

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestService_Login(t *testing.T) {
	ctx := context.Background()

	t.Run("successful login", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "SecurePass@123")

		mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
		mockStore.On("CreateSession", ctx, mock.AnythingOfType("*auth.Session")).Return(nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		req := LoginRequest{
			Email:    "test@example.com",
			Password: "SecurePass@123",
		}

		resp, err := service.Login(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.NotEmpty(t, resp.Token)
		assert.Equal(t, testUser.ID, resp.User.ID)

		mockStore.AssertExpectations(t)
	})

	t.Run("user not found", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "notfound@example.com").Return(nil, nil).Once()

		req := LoginRequest{
			Email:    "notfound@example.com",
			Password: "password",
		}

		resp, err := service.Login(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "invalid email or password")

		mockStore.AssertExpectations(t)
	})

	t.Run("wrong password", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "SecurePass@123")

		mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Maybe()

		req := LoginRequest{
			Email:    "test@example.com",
			Password: "wrongpassword",
		}

		resp, err := service.Login(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "invalid email or password")

		mockStore.AssertExpectations(t)
	})

	t.Run("inactive user", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "SecurePass@123")
		testUser.Active = false

		mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Maybe()

		req := LoginRequest{
			Email:    "test@example.com",
			Password: "SecurePass@123",
		}

		resp, err := service.Login(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "account is disabled")

		mockStore.AssertExpectations(t)
	})

	t.Run("invalid email format", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		req := LoginRequest{
			Email:    "invalid-email",
			Password: "password",
		}

		resp, err := service.Login(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "invalid email format")
	})
}

func TestService_ValidateSession(t *testing.T) {
	ctx := context.Background()

	t.Run("valid session", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		// Token is hashed before lookup, so mock expects hashed value
		hashedToken := hashSessionToken("valid-token")

		validSession := &Session{
			Token:     hashedToken,
			UserID:    "user-123",
			Email:     "test@example.com",
			Role:      RoleUser,
			ExpiresAt: time.Now().Add(time.Hour),
		}

		mockStore.On("GetSession", ctx, hashedToken).Return(validSession, nil).Once()

		session, err := service.ValidateSession(ctx, "valid-token")
		require.NoError(t, err)
		assert.NotNil(t, session)
		assert.Equal(t, "user-123", session.UserID)

		mockStore.AssertExpectations(t)
	})

	t.Run("session not found", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		hashedToken := hashSessionToken("nonexistent-token")
		mockStore.On("GetSession", ctx, hashedToken).Return(nil, nil).Once()

		session, err := service.ValidateSession(ctx, "nonexistent-token")
		assert.Error(t, err)
		assert.Nil(t, session)
		assert.Contains(t, err.Error(), "session not found")

		mockStore.AssertExpectations(t)
	})

	t.Run("expired session", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		hashedToken := hashSessionToken("expired-token")

		expiredSession := &Session{
			Token:     hashedToken,
			UserID:    "user-123",
			ExpiresAt: time.Now().Add(-time.Hour),
		}

		mockStore.On("GetSession", ctx, hashedToken).Return(expiredSession, nil).Once()
		mockStore.On("DeleteSession", ctx, hashedToken).Return(nil).Once()

		session, err := service.ValidateSession(ctx, "expired-token")
		assert.Error(t, err)
		assert.Nil(t, session)
		assert.Contains(t, err.Error(), "session expired")

		mockStore.AssertExpectations(t)
	})
}

func TestService_Logout(t *testing.T) {
	ctx := context.Background()

	t.Run("successful logout", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		// Token is hashed before deletion, so mock expects hashed value
		hashedToken := hashSessionToken("session-token")
		mockStore.On("DeleteSession", ctx, hashedToken).Return(nil).Once()

		err := service.Logout(ctx, "session-token")
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})
}

func TestNewService(t *testing.T) {
	t.Run("creates service with default session duration", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)

		cfg := ServiceConfig{
			Store:        mockStore,
			EmailSender:  mockEmail,
			DashboardURL: "https://dashboard.example.com",
		}

		service := NewService(cfg)
		assert.NotNil(t, service)
		assert.Equal(t, 24*time.Hour, service.sessionDuration)
	})

	t.Run("creates service with custom session duration", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)

		cfg := ServiceConfig{
			Store:           mockStore,
			EmailSender:     mockEmail,
			SessionDuration: 12 * time.Hour,
			DashboardURL:    "https://dashboard.example.com",
		}

		service := NewService(cfg)
		assert.NotNil(t, service)
		assert.Equal(t, 12*time.Hour, service.sessionDuration)
	})

	t.Run("allows http://localhost dashboard URL", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)

		cfg := ServiceConfig{
			Store:        mockStore,
			EmailSender:  mockEmail,
			DashboardURL: "http://localhost:3000",
		}

		service := NewService(cfg)
		assert.NotNil(t, service)
		assert.Equal(t, "http://localhost:3000", service.dashboardURL)
	})

	t.Run("allows http://127.0.0.1 dashboard URL", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)

		cfg := ServiceConfig{
			Store:        mockStore,
			EmailSender:  mockEmail,
			DashboardURL: "http://127.0.0.1:8080",
		}

		service := NewService(cfg)
		assert.NotNil(t, service)
		assert.Equal(t, "http://127.0.0.1:8080", service.dashboardURL)
	})

	t.Run("warns about non-https non-localhost URL", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)

		cfg := ServiceConfig{
			Store:        mockStore,
			EmailSender:  mockEmail,
			DashboardURL: "http://example.com",
		}

		// Should create service but log warning
		service := NewService(cfg)
		assert.NotNil(t, service)
		assert.Equal(t, "http://example.com", service.dashboardURL)
	})

	t.Run("allows empty dashboard URL", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)

		cfg := ServiceConfig{
			Store:       mockStore,
			EmailSender: mockEmail,
		}

		service := NewService(cfg)
		assert.NotNil(t, service)
		assert.Empty(t, service.dashboardURL)
	})
}

func TestService_Login_MFA(t *testing.T) {
	ctx := context.Background()

	t.Run("MFA required when enabled", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "SecurePass@123")
		testUser.MFAEnabled = true

		mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()

		req := LoginRequest{
			Email:    "test@example.com",
			Password: "SecurePass@123",
			// No MFA code provided
		}

		resp, err := service.Login(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "MFA code required")

		mockStore.AssertExpectations(t)
	})
}

func TestLogin_WithMFA(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	service := createTestService(mockStore, mockEmail)

	// Generate a valid TOTP code
	mfaSecret := "JBSWY3DPEHPK3PXP"
	currentTime := time.Now().Unix()
	timeStep := int64(30)
	counter := currentTime / timeStep
	validCode := generateTOTP(mfaSecret, counter)

	// Hash the password using service method (salt no longer used)
	s := &Service{}
	hash, _ := s.hashPassword("SecurePass@123")

	user := &User{
		ID:           "user-123",
		Email:        "mfa@example.com",
		PasswordHash: hash,
		Salt:         "", // Not used anymore
		Active:       true,
		MFAEnabled:   true,
		MFASecret:    mfaSecret,
		Role:         RoleUser,
	}

	mockStore.On("GetUserByEmail", ctx, "mfa@example.com").Return(user, nil)
	mockStore.On("CreateSession", ctx, mock.AnythingOfType("*auth.Session")).Return(nil)
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil)

	req := LoginRequest{
		Email:    "mfa@example.com",
		Password: "SecurePass@123",
		MFACode:  validCode,
	}

	resp, err := service.Login(ctx, req)
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.Token)
}

func TestLogin_WithMFA_InvalidCode(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	service := createTestService(mockStore, mockEmail)

	s := &Service{}
	hash, _ := s.hashPassword("SecurePass@123")

	user := &User{
		ID:           "user-123",
		Email:        "mfa@example.com",
		PasswordHash: hash,
		Salt:         "", // Not used anymore
		Active:       true,
		MFAEnabled:   true,
		MFASecret:    "JBSWY3DPEHPK3PXP",
		Role:         RoleUser,
	}

	mockStore.On("GetUserByEmail", ctx, "mfa@example.com").Return(user, nil)
	// Add mock for failed login recording due to invalid MFA code
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Maybe()

	req := LoginRequest{
		Email:    "mfa@example.com",
		Password: "SecurePass@123",
		MFACode:  "000000", // Invalid code
	}

	resp, err := service.Login(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "invalid MFA code")
}

func TestLogin_WithMFA_MissingCode(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	service := createTestService(mockStore, mockEmail)

	s := &Service{}
	hash, _ := s.hashPassword("SecurePass@123")

	user := &User{
		ID:           "user-123",
		Email:        "mfa@example.com",
		PasswordHash: hash,
		Salt:         "", // Not used anymore
		Active:       true,
		MFAEnabled:   true,
		MFASecret:    "JBSWY3DPEHPK3PXP",
		Role:         RoleUser,
	}

	mockStore.On("GetUserByEmail", ctx, "mfa@example.com").Return(user, nil)

	req := LoginRequest{
		Email:    "mfa@example.com",
		Password: "SecurePass@123",
		MFACode:  "", // Missing code
	}

	resp, err := service.Login(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "MFA code required")
}

func TestLogin_WithMFA_NoSecret(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	service := createTestService(mockStore, mockEmail)

	s := &Service{}
	hash, _ := s.hashPassword("SecurePass@123")

	user := &User{
		ID:           "user-123",
		Email:        "mfa@example.com",
		PasswordHash: hash,
		Salt:         "", // Not used anymore
		Active:       true,
		MFAEnabled:   true,
		MFASecret:    "", // No secret configured
		Role:         RoleUser,
	}

	mockStore.On("GetUserByEmail", ctx, "mfa@example.com").Return(user, nil)

	req := LoginRequest{
		Email:    "mfa@example.com",
		Password: "SecurePass@123",
		MFACode:  "123456",
	}

	resp, err := service.Login(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "MFA is enabled but not configured")
}

// Test UpdateUserProfile
func TestService_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("createSession error", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "SecurePass@123")

		mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
		mockStore.On("CreateSession", ctx, mock.AnythingOfType("*auth.Session")).Return(fmt.Errorf("database error")).Once()

		req := LoginRequest{
			Email:    "test@example.com",
			Password: "SecurePass@123",
		}

		resp, err := service.Login(ctx, req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "failed to create session")

		mockStore.AssertExpectations(t)
	})

	t.Run("DeleteUser session cleanup error", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("DeleteUserSessions", ctx, "user-123").Return(fmt.Errorf("session cleanup error")).Once()
		mockStore.On("DeleteUser", ctx, "user-123").Return(nil).Once()

		err := service.DeleteUser(ctx, "user-123")
		// Should succeed even if session cleanup fails
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})

	t.Run("RequestPasswordReset email send error", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "SecurePass@123")

		mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()
		mockEmail.On("SendPasswordResetEmail", ctx, "test@example.com", mock.AnythingOfType("string")).Return(fmt.Errorf("email error")).Once()

		// Should not return error to prevent email enumeration
		err := service.RequestPasswordReset(ctx, "test@example.com")
		assert.NoError(t, err)

		mockStore.AssertExpectations(t)
		mockEmail.AssertExpectations(t)
	})

	t.Run("ValidateSession cleanup error on expired", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		// Token is hashed before lookup, so mock expects hashed value
		hashedToken := hashSessionToken("expired-token")

		expiredSession := &Session{
			Token:     hashedToken,
			UserID:    "user-123",
			ExpiresAt: time.Now().Add(-time.Hour),
		}

		mockStore.On("GetSession", ctx, hashedToken).Return(expiredSession, nil).Once()
		mockStore.On("DeleteSession", ctx, hashedToken).Return(fmt.Errorf("delete error")).Once()

		session, err := service.ValidateSession(ctx, "expired-token")
		assert.Error(t, err)
		assert.Nil(t, session)
		assert.Contains(t, err.Error(), "session expired")

		mockStore.AssertExpectations(t)
	})

	t.Run("ChangePassword session cleanup error", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "OldPassword123")

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()
		mockStore.On("DeleteUserSessions", ctx, "user-123").Return(fmt.Errorf("session error")).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		req := ChangePasswordRequest{
			CurrentPassword: "OldPassword123",
			NewPassword:     "SecureTest@456",
		}

		// Should succeed even if session cleanup fails
		err := service.ChangePassword(ctx, "user-123", req)
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})

	t.Run("ConfirmPasswordReset session cleanup error", func(t *testing.T) {
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

		mockStore.On("GetUserByResetToken", ctx, mock.AnythingOfType("string")).Return(testUser, nil).Once()
		mockStore.On("DeleteUserSessions", ctx, "user-123").Return(fmt.Errorf("session error")).Once()
		// UpdateUser is called twice: once to invalidate the token (security fix) and once to save the new password
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Twice()

		req := PasswordResetConfirm{
			Token:       "valid-reset-token",
			NewPassword: "SecureTest@789",
		}

		// Should succeed even if session cleanup fails
		err := service.ConfirmPasswordReset(ctx, req)
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})

	t.Run("Login update last login error", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "SecurePass@123")

		mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
		mockStore.On("CreateSession", ctx, mock.AnythingOfType("*auth.Session")).Return(nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(fmt.Errorf("update error")).Once()

		req := LoginRequest{
			Email:    "test@example.com",
			Password: "SecurePass@123",
		}

		// Should succeed even if last login update fails
		resp, err := service.Login(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, resp)

		mockStore.AssertExpectations(t)
	})

	t.Run("GetUserPermissions with store error on group", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			Role:     RoleUser,
			GroupIDs: []string{"group-1"},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "group-1").Return(nil, fmt.Errorf("database error")).Once()

		permissions, err := service.GetUserPermissions(ctx, "user-123")
		require.NoError(t, err)
		// Should still return user permissions even if group fetch fails
		assert.Len(t, permissions, 5)

		mockStore.AssertExpectations(t)
	})
}

func TestService_ValidateCSRFToken(t *testing.T) {
	ctx := context.Background()

	t.Run("successful CSRF validation", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		hashedToken := hashSessionToken("session-token")
		session := &Session{
			UserID:    "user-123",
			Token:     hashedToken,
			CSRFToken: "csrf-token-123",
			ExpiresAt: time.Now().Add(1 * time.Hour),
			CreatedAt: time.Now(),
		}

		mockStore.On("GetSession", ctx, hashedToken).Return(session, nil)

		err := service.ValidateCSRFToken(ctx, "session-token", "csrf-token-123")
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})

	t.Run("fail when CSRF token is empty", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		err := service.ValidateCSRFToken(ctx, "session-token", "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "CSRF token is required")
	})

	t.Run("fail when session is invalid", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		hashedToken := hashSessionToken("invalid-session")
		mockStore.On("GetSession", ctx, hashedToken).Return(nil, fmt.Errorf("session not found"))

		err := service.ValidateCSRFToken(ctx, "invalid-session", "csrf-token")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid session")

		mockStore.AssertExpectations(t)
	})

	t.Run("fail when session has no CSRF token", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		hashedToken := hashSessionToken("session-token")
		session := &Session{
			UserID:    "user-123",
			Token:     hashedToken,
			CSRFToken: "",
			ExpiresAt: time.Now().Add(1 * time.Hour),
			CreatedAt: time.Now(),
		}

		mockStore.On("GetSession", ctx, hashedToken).Return(session, nil)

		err := service.ValidateCSRFToken(ctx, "session-token", "csrf-token")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "re-authentication")

		mockStore.AssertExpectations(t)
	})

	t.Run("fail when CSRF tokens don't match", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		hashedToken := hashSessionToken("session-token")
		session := &Session{
			UserID:    "user-123",
			Token:     hashedToken,
			CSRFToken: "correct-csrf-token",
			ExpiresAt: time.Now().Add(1 * time.Hour),
			CreatedAt: time.Now(),
		}

		mockStore.On("GetSession", ctx, hashedToken).Return(session, nil)

		err := service.ValidateCSRFToken(ctx, "session-token", "wrong-csrf-token")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid CSRF token")

		mockStore.AssertExpectations(t)
	})
}
