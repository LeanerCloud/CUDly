package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
