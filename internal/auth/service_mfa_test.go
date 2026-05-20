package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBase32Decode(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		hasError bool
	}{
		{
			name:     "valid base32",
			input:    "JBSWY3DPEHPK3PXP",
			hasError: false,
		},
		{
			name:     "empty string",
			input:    "",
			hasError: false,
		},
		{
			name:     "lowercase converted to uppercase",
			input:    "jbswy3dpehpk3pxp",
			hasError: false,
		},
		{
			name:     "with padding",
			input:    "GEZDGNBVGY3TQOJQ", // "12345678"
			hasError: false,
		},
		{
			name:     "invalid character",
			input:    "JBSWY3DPEHPK3PXP!",
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := base32Decode(tt.input)
			if tt.hasError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				// Just verify it returns bytes without error
				assert.NotNil(t, result)
			}
		})
	}
}

func TestGenerateTOTP(t *testing.T) {
	// Test with a known secret and counter
	// Using RFC 6238 test vectors would be ideal but we just want coverage
	secret := "JBSWY3DPEHPK3PXP"

	tests := []struct {
		name    string
		counter int64
	}{
		{"counter 0", 0},
		{"counter 1", 1},
		{"counter 1000000", 1000000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := generateTOTP(secret, tt.counter)
			// Should be a 6-digit code
			assert.Len(t, code, 6)
			// Should contain only digits
			for _, c := range code {
				assert.True(t, c >= '0' && c <= '9', "expected digit, got %c", c)
			}
		})
	}
}

func TestGenerateTOTP_InvalidSecret(t *testing.T) {
	// Invalid base32 secret should return empty string
	code := generateTOTP("INVALID!SECRET", 0)
	assert.Equal(t, "", code)
}

func TestVerifyTOTP(t *testing.T) {
	// Generate a code for the current time
	secret := "JBSWY3DPEHPK3PXP"
	currentTime := time.Now().Unix()
	timeStep := int64(30)
	counter := currentTime / timeStep

	// Generate the expected code
	expectedCode := generateTOTP(secret, counter)

	tests := []struct {
		name     string
		secret   string
		code     string
		expected bool
	}{
		{
			name:     "valid code for current time",
			secret:   secret,
			code:     expectedCode,
			expected: true,
		},
		{
			name:     "invalid code",
			secret:   secret,
			code:     "000000",
			expected: false,
		},
		{
			name:     "wrong secret",
			secret:   "DIFFERENTSECRETZ",
			code:     expectedCode,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := verifyTOTP(tt.secret, tt.code)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestVerifyTOTP_TimeWindow(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	currentTime := time.Now().Unix()
	timeStep := int64(30)

	// Test that codes from adjacent time windows are accepted
	for _, offset := range []int64{-1, 0, 1} {
		counter := (currentTime / timeStep) + offset
		code := generateTOTP(secret, counter)

		result := verifyTOTP(secret, code)
		assert.True(t, result, "code from time window offset %d should be valid", offset)
	}
}

// ---------------------------------------------------------------
// Tests for the MFA enrollment / disable / regenerate lifecycle
// added in issue #497.
// ---------------------------------------------------------------

func TestGenerateMFASecret(t *testing.T) {
	a, err := generateMFASecret()
	require.NoError(t, err)
	b, err := generateMFASecret()
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "two successive secrets must differ")
	// Unpadded base32 of 20 bytes = ceil(20 * 8 / 5) = 32 chars.
	assert.Len(t, a, 32)
}

func TestBuildProvisioningURI(t *testing.T) {
	uri := buildProvisioningURI("user@example.com", "JBSWY3DPEHPK3PXP")
	assert.True(t, strings.HasPrefix(uri, "otpauth://totp/"))
	assert.Contains(t, uri, "secret=JBSWY3DPEHPK3PXP")
	assert.Contains(t, uri, "issuer=CUDly")
	assert.Contains(t, uri, "algorithm=SHA1")
	assert.Contains(t, uri, "digits=6")
	assert.Contains(t, uri, "period=30")
}

func TestGenerateRecoveryCodes(t *testing.T) {
	codes, err := generateRecoveryCodes(10)
	require.NoError(t, err)
	assert.Len(t, codes, 10)
	seen := map[string]struct{}{}
	for _, c := range codes {
		// Format: XXXX-XXXX (8 chars + dash)
		assert.Len(t, c, recoveryCodeLength+1)
		assert.Equal(t, byte('-'), c[recoveryCodeLength/2])
		assert.NotContains(t, seen, c, "recovery code collision is astronomically unlikely")
		seen[c] = struct{}{}
	}
}

func TestNormalizeRecoveryCode(t *testing.T) {
	assert.Equal(t, "ABCD2345", normalizeRecoveryCode("abcd-2345"))
	assert.Equal(t, "ABCD2345", normalizeRecoveryCode(" abcd 2345 "))
	assert.Equal(t, "ABCD2345", normalizeRecoveryCode("ABCD-2345"))
}

// totpFor returns a valid TOTP code for the given secret at the
// current time step. Helper for the service tests below.
func totpFor(secret string) string {
	timeStep := int64(30)
	return generateTOTP(secret, time.Now().Unix()/timeStep)
}

func TestMFASetup_HappyPath(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	service := createTestService(mockStore, new(MockEmailSender))

	user := createTestUser(t, "SecurePass@123")
	user.MFAEnabled = false
	mockStore.On("GetUserByID", ctx, user.ID).Return(user, nil)
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Run(func(args mock.Arguments) {
		u := args.Get(1).(*User)
		assert.NotEmpty(t, u.MFAPendingSecret, "setup should persist a pending secret")
		assert.NotNil(t, u.MFAPendingSecretExpiresAt)
		assert.False(t, u.MFAEnabled, "setup must not flip MFAEnabled")
	}).Return(nil).Once()

	res, err := service.MFASetup(ctx, user.ID, "SecurePass@123")
	require.NoError(t, err)
	assert.NotEmpty(t, res.Secret)
	assert.True(t, strings.HasPrefix(res.ProvisioningURI, "otpauth://totp/"))
	mockStore.AssertExpectations(t)
}

func TestMFASetup_WrongPassword(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	service := createTestService(mockStore, new(MockEmailSender))

	user := createTestUser(t, "SecurePass@123")
	mockStore.On("GetUserByID", ctx, user.ID).Return(user, nil)

	_, err := service.MFASetup(ctx, user.ID, "WrongPassword!@#")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid password")
	mockStore.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
}

func TestMFAEnable_HappyPath(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	service := createTestService(mockStore, new(MockEmailSender))

	secret := "JBSWY3DPEHPK3PXP"
	expiresAt := time.Now().Add(mfaPendingExpiry)
	user := createTestUser(t, "SecurePass@123")
	user.MFAPendingSecret = secret
	user.MFAPendingSecretExpiresAt = &expiresAt

	mockStore.On("GetUserByID", ctx, user.ID).Return(user, nil)
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Run(func(args mock.Arguments) {
		u := args.Get(1).(*User)
		assert.True(t, u.MFAEnabled)
		assert.Equal(t, secret, u.MFASecret)
		assert.Empty(t, u.MFAPendingSecret, "pending secret must be cleared on enable")
		assert.Nil(t, u.MFAPendingSecretExpiresAt)
		assert.Len(t, u.MFARecoveryCodes, recoveryCodeCount)
	}).Return(nil).Once()

	codes, err := service.MFAEnable(ctx, user.ID, totpFor(secret))
	require.NoError(t, err)
	assert.Len(t, codes, recoveryCodeCount)
	mockStore.AssertExpectations(t)
}

func TestMFAEnable_ExpiredPending(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	service := createTestService(mockStore, new(MockEmailSender))

	secret := "JBSWY3DPEHPK3PXP"
	expiresAt := time.Now().Add(-1 * time.Minute) // already expired
	user := createTestUser(t, "SecurePass@123")
	user.MFAPendingSecret = secret
	user.MFAPendingSecretExpiresAt = &expiresAt
	mockStore.On("GetUserByID", ctx, user.ID).Return(user, nil)
	// Stale pending fields get wiped on expiry; allow but don't require.
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Maybe()

	_, err := service.MFAEnable(ctx, user.ID, totpFor(secret))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestMFAEnable_NoPending(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	service := createTestService(mockStore, new(MockEmailSender))

	user := createTestUser(t, "SecurePass@123")
	mockStore.On("GetUserByID", ctx, user.ID).Return(user, nil)

	_, err := service.MFAEnable(ctx, user.ID, "000000")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no MFA enrollment in progress")
}

func TestMFAEnable_WrongCode(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	service := createTestService(mockStore, new(MockEmailSender))

	secret := "JBSWY3DPEHPK3PXP"
	expiresAt := time.Now().Add(mfaPendingExpiry)
	user := createTestUser(t, "SecurePass@123")
	user.MFAPendingSecret = secret
	user.MFAPendingSecretExpiresAt = &expiresAt
	mockStore.On("GetUserByID", ctx, user.ID).Return(user, nil)

	_, err := service.MFAEnable(ctx, user.ID, "000000")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid MFA code")
}

func TestMFADisable_WithTOTP(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	service := createTestService(mockStore, new(MockEmailSender))

	secret := "JBSWY3DPEHPK3PXP"
	user := createTestUser(t, "SecurePass@123")
	user.MFAEnabled = true
	user.MFASecret = secret
	user.MFARecoveryCodes = []string{"$2a$04$hashedstub"} // doesn't matter, won't be tested

	mockStore.On("GetUserByID", ctx, user.ID).Return(user, nil)
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Run(func(args mock.Arguments) {
		u := args.Get(1).(*User)
		assert.False(t, u.MFAEnabled)
		assert.Empty(t, u.MFASecret)
		assert.Empty(t, u.MFARecoveryCodes)
	}).Return(nil).Once()

	err := service.MFADisable(ctx, user.ID, "SecurePass@123", totpFor(secret))
	require.NoError(t, err)
	mockStore.AssertExpectations(t)
}

func TestMFADisable_WithRecoveryCode(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	service := createTestService(mockStore, new(MockEmailSender))

	secret := "JBSWY3DPEHPK3PXP"
	user := createTestUser(t, "SecurePass@123")
	user.MFAEnabled = true
	user.MFASecret = secret

	plaintextCode := "ABCD-2345"
	hash, err := service.hashRecoveryCode(plaintextCode)
	require.NoError(t, err)
	user.MFARecoveryCodes = []string{hash}

	mockStore.On("GetUserByID", ctx, user.ID).Return(user, nil)
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

	err = service.MFADisable(ctx, user.ID, "SecurePass@123", plaintextCode)
	require.NoError(t, err)
}

func TestMFADisable_WrongPassword(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	service := createTestService(mockStore, new(MockEmailSender))

	user := createTestUser(t, "SecurePass@123")
	user.MFAEnabled = true
	user.MFASecret = "JBSWY3DPEHPK3PXP"
	mockStore.On("GetUserByID", ctx, user.ID).Return(user, nil)

	err := service.MFADisable(ctx, user.ID, "wrong", "000000")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid password")
	mockStore.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
}

func TestMFADisable_IdempotentWhenAlreadyDisabled(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	service := createTestService(mockStore, new(MockEmailSender))

	user := createTestUser(t, "SecurePass@123")
	user.MFAEnabled = false
	mockStore.On("GetUserByID", ctx, user.ID).Return(user, nil)
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

	err := service.MFADisable(ctx, user.ID, "SecurePass@123", "")
	require.NoError(t, err)
}

func TestMFARegenerateRecoveryCodes_HappyPath(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	service := createTestService(mockStore, new(MockEmailSender))

	secret := "JBSWY3DPEHPK3PXP"
	user := createTestUser(t, "SecurePass@123")
	user.MFAEnabled = true
	user.MFASecret = secret
	// pre-existing hash that should NOT survive regeneration
	user.MFARecoveryCodes = []string{"$2a$04$preexistingstub"}

	mockStore.On("GetUserByID", ctx, user.ID).Return(user, nil)
	var captured []string
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Run(func(args mock.Arguments) {
		u := args.Get(1).(*User)
		captured = append([]string{}, u.MFARecoveryCodes...)
	}).Return(nil).Once()

	codes, err := service.MFARegenerateRecoveryCodes(ctx, user.ID, totpFor(secret))
	require.NoError(t, err)
	assert.Len(t, codes, recoveryCodeCount)
	assert.Len(t, captured, recoveryCodeCount, "regenerate must replace the entire slice")
	// The pre-existing stub must NOT be present.
	for _, h := range captured {
		assert.NotEqual(t, "$2a$04$preexistingstub", h)
	}
}

func TestMFARegenerateRecoveryCodes_WrongTOTP(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	service := createTestService(mockStore, new(MockEmailSender))

	user := createTestUser(t, "SecurePass@123")
	user.MFAEnabled = true
	user.MFASecret = "JBSWY3DPEHPK3PXP"
	mockStore.On("GetUserByID", ctx, user.ID).Return(user, nil)

	_, err := service.MFARegenerateRecoveryCodes(ctx, user.ID, "000000")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid MFA code")
}

func TestLogin_WithMFA_RecoveryCode_ConsumedOnce(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	service := createTestService(mockStore, new(MockEmailSender))

	plaintextCode := "ABCD-2345"
	hash, err := service.hashRecoveryCode(plaintextCode)
	require.NoError(t, err)

	user := createTestUser(t, "SecurePass@123")
	user.MFAEnabled = true
	user.MFASecret = "JBSWY3DPEHPK3PXP"
	user.MFARecoveryCodes = []string{hash}

	mockStore.On("GetUserByEmail", ctx, user.Email).Return(user, nil)
	// recovery-code consumption persists user, then completeSuccessfulLogin
	// persists again. Both must succeed.
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil)
	mockStore.On("CreateSession", ctx, mock.AnythingOfType("*auth.Session")).Return(nil)

	resp, err := service.Login(ctx, LoginRequest{
		Email:    user.Email,
		Password: "SecurePass@123",
		MFACode:  plaintextCode,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Empty(t, user.MFARecoveryCodes, "consumed recovery code must be removed from the slice")
}
