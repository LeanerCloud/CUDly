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

// TestLogin_AccountLockout_BeforePasswordCheck verifies lockout check happens before password verification
func TestLogin_AccountLockout_BeforePasswordCheck(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	service := createTestService(mockStore, mockEmail)

	// Create a locked user with correct password
	testUser := createTestUser(t, "CorrectPassword123")
	lockUntil := time.Now().Add(10 * time.Minute)
	testUser.LockedUntil = &lockUntil
	testUser.FailedLoginAttempts = MaxFailedLoginAttempts

	mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
	// UpdateUser should NOT be called since we fail on lockout check before password verification
	// No password verification happens when account is locked

	req := LoginRequest{
		Email:    "test@example.com",
		Password: "CorrectPassword123", // Even with correct password
	}

	resp, err := service.Login(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "invalid email or password") // Generic error to prevent user enumeration

	mockStore.AssertExpectations(t)
	// Verify UpdateUser was NOT called - lockout check happens first
	mockStore.AssertNotCalled(t, "UpdateUser", ctx, mock.Anything)
}

// TestLogin_AccountLockout_FailedAttempts verifies lockout occurs after max failed attempts
func TestLogin_AccountLockout_FailedAttempts(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	service := createTestService(mockStore, mockEmail)

	testUser := createTestUser(t, "CorrectPassword123")
	testUser.FailedLoginAttempts = 4 // One more attempt will trigger lockout

	// Track calls to UpdateUser
	var updatedUser *User
	mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).
		Run(func(args mock.Arguments) {
			updatedUser = args.Get(1).(*User)
		}).
		Return(nil).Once()

	req := LoginRequest{
		Email:    "test@example.com",
		Password: "WrongPassword", // Wrong password triggers failed attempt
	}

	resp, err := service.Login(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, resp)

	// Verify user was locked
	require.NotNil(t, updatedUser)
	assert.Equal(t, MaxFailedLoginAttempts, updatedUser.FailedLoginAttempts)
	assert.NotNil(t, updatedUser.LockedUntil)
	assert.True(t, updatedUser.LockedUntil.After(time.Now()))

	mockStore.AssertExpectations(t)
}

// TestLogin_AccountLockout_Duration verifies lockout duration is correct
func TestLogin_AccountLockout_Duration(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	service := createTestService(mockStore, mockEmail)

	testUser := createTestUser(t, "CorrectPassword123")
	testUser.FailedLoginAttempts = MaxFailedLoginAttempts - 1

	var updatedUser *User
	mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).
		Run(func(args mock.Arguments) {
			updatedUser = args.Get(1).(*User)
		}).
		Return(nil).Once()

	req := LoginRequest{
		Email:    "test@example.com",
		Password: "WrongPassword",
	}

	_, err := service.Login(ctx, req)
	assert.Error(t, err)

	// Verify lockout duration is AccountLockoutDuration (15 minutes)
	require.NotNil(t, updatedUser)
	require.NotNil(t, updatedUser.LockedUntil)

	expectedLockout := time.Now().Add(AccountLockoutDuration)
	// Allow 1 second tolerance for test execution time
	assert.WithinDuration(t, expectedLockout, *updatedUser.LockedUntil, time.Second)

	mockStore.AssertExpectations(t)
}

// TestLogin_AccountLockout_ExpiredLock verifies expired lockouts allow login
func TestLogin_AccountLockout_ExpiredLock(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	service := createTestService(mockStore, mockEmail)

	testUser := createTestUser(t, "CorrectPassword123")
	// Lockout expired 1 minute ago
	lockUntil := time.Now().Add(-1 * time.Minute)
	testUser.LockedUntil = &lockUntil
	testUser.FailedLoginAttempts = MaxFailedLoginAttempts

	mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
	mockStore.On("CreateSession", ctx, mock.AnythingOfType("*auth.Session")).Return(nil).Once()
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

	req := LoginRequest{
		Email:    "test@example.com",
		Password: "CorrectPassword123",
	}

	resp, err := service.Login(ctx, req)
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.Token)

	mockStore.AssertExpectations(t)
}

// TestLogin_AccountLockout_ResetOnSuccess verifies successful login resets failed attempts
func TestLogin_AccountLockout_ResetOnSuccess(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	service := createTestService(mockStore, mockEmail)

	testUser := createTestUser(t, "CorrectPassword123")
	testUser.FailedLoginAttempts = 3 // Some failed attempts, but not locked

	var updatedUser *User
	mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
	mockStore.On("CreateSession", ctx, mock.AnythingOfType("*auth.Session")).Return(nil).Once()
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).
		Run(func(args mock.Arguments) {
			updatedUser = args.Get(1).(*User)
		}).
		Return(nil).Once()

	req := LoginRequest{
		Email:    "test@example.com",
		Password: "CorrectPassword123",
	}

	resp, err := service.Login(ctx, req)
	require.NoError(t, err)
	assert.NotNil(t, resp)

	// Verify failed attempts were reset
	require.NotNil(t, updatedUser)
	assert.Equal(t, 0, updatedUser.FailedLoginAttempts)
	assert.Nil(t, updatedUser.LockedUntil)

	mockStore.AssertExpectations(t)
}

// TestLogin_AccountLockout_IncrementalFailures verifies each failure increments counter
func TestLogin_AccountLockout_IncrementalFailures(t *testing.T) {
	ctx := context.Background()

	for attempt := 0; attempt < MaxFailedLoginAttempts; attempt++ {
		t.Run(fmt.Sprintf("Attempt_%d", attempt+1), func(t *testing.T) {
			mockStore := new(MockStore)
			mockEmail := new(MockEmailSender)
			service := createTestService(mockStore, mockEmail)

			testUser := createTestUser(t, "CorrectPassword123")
			testUser.FailedLoginAttempts = attempt

			var updatedUser *User
			mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
			mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).
				Run(func(args mock.Arguments) {
					updatedUser = args.Get(1).(*User)
				}).
				Return(nil).Once()

			req := LoginRequest{
				Email:    "test@example.com",
				Password: "WrongPassword",
			}

			_, err := service.Login(ctx, req)
			assert.Error(t, err)

			// Verify attempt counter incremented
			require.NotNil(t, updatedUser)
			assert.Equal(t, attempt+1, updatedUser.FailedLoginAttempts)

			// Verify lockout only happens at MaxFailedLoginAttempts
			if attempt+1 >= MaxFailedLoginAttempts {
				assert.NotNil(t, updatedUser.LockedUntil)
			} else {
				assert.Nil(t, updatedUser.LockedUntil)
			}

			mockStore.AssertExpectations(t)
		})
	}
}

// TestLogin_AccountLockout_MFAFailure verifies MFA failures count toward lockout
func TestLogin_AccountLockout_MFAFailure(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	service := createTestService(mockStore, mockEmail)

	s := &Service{}
	hash, _ := s.hashPassword("CorrectPassword123")

	testUser := &User{
		ID:                  "user-123",
		Email:               "test@example.com",
		PasswordHash:        hash,
		Active:              true,
		MFAEnabled:          true,
		MFASecret:           "JBSWY3DPEHPK3PXP",
		Role:                RoleUser,
		FailedLoginAttempts: MaxFailedLoginAttempts - 1,
	}

	var updatedUser *User
	mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).
		Run(func(args mock.Arguments) {
			updatedUser = args.Get(1).(*User)
		}).
		Return(nil).Once()

	req := LoginRequest{
		Email:    "test@example.com",
		Password: "CorrectPassword123", // Correct password
		MFACode:  "000000",             // Wrong MFA code
	}

	_, err := service.Login(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid MFA code")

	// Verify MFA failure incremented counter and locked account
	require.NotNil(t, updatedUser)
	assert.Equal(t, MaxFailedLoginAttempts, updatedUser.FailedLoginAttempts)
	assert.NotNil(t, updatedUser.LockedUntil)

	mockStore.AssertExpectations(t)
}

// TestLogin_AccountLockout_GenericErrorMessage verifies no information leakage
func TestLogin_AccountLockout_GenericErrorMessage(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	service := createTestService(mockStore, mockEmail)

	testUser := createTestUser(t, "CorrectPassword123")
	lockUntil := time.Now().Add(10 * time.Minute)
	testUser.LockedUntil = &lockUntil

	mockStore.On("GetUserByEmail", ctx, "test@example.com").Return(testUser, nil).Once()

	req := LoginRequest{
		Email:    "test@example.com",
		Password: "CorrectPassword123",
	}

	_, err := service.Login(ctx, req)
	assert.Error(t, err)

	// Error message should be generic to prevent user enumeration
	// Should NOT reveal that account is locked
	assert.Equal(t, "invalid email or password", err.Error())

	mockStore.AssertExpectations(t)
}

// TestRecordFailedLogin verifies recordFailedLogin function behavior
func TestRecordFailedLogin(t *testing.T) {
	ctx := context.Background()

	t.Run("increments counter below threshold", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "password")
		testUser.FailedLoginAttempts = 2

		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		service.recordFailedLogin(ctx, testUser)

		assert.Equal(t, 3, testUser.FailedLoginAttempts)
		assert.Nil(t, testUser.LockedUntil)

		mockStore.AssertExpectations(t)
	})

	t.Run("locks account at threshold", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "password")
		testUser.FailedLoginAttempts = MaxFailedLoginAttempts - 1

		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		service.recordFailedLogin(ctx, testUser)

		assert.Equal(t, MaxFailedLoginAttempts, testUser.FailedLoginAttempts)
		assert.NotNil(t, testUser.LockedUntil)
		assert.True(t, testUser.LockedUntil.After(time.Now()))

		mockStore.AssertExpectations(t)
	})

	t.Run("handles update error gracefully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "password")

		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(assert.AnError).Once()

		// Should not panic even if update fails
		service.recordFailedLogin(ctx, testUser)

		mockStore.AssertExpectations(t)
	})
}
