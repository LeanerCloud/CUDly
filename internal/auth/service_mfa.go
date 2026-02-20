package auth

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"fmt"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
)

// verifyTOTP validates a TOTP code against the secret
// Implements RFC 6238 TOTP with 30-second time steps
// Uses constant-time comparison to prevent timing attacks
func verifyTOTP(secret, code string) bool {
	// Allow for time skew by checking current and adjacent time windows
	currentTime := time.Now().Unix()
	timeStep := int64(config.MFATimeStep)

	// Check current time step and one step before/after for clock skew tolerance
	// Use constant-time comparison and avoid early returns to prevent timing attacks
	valid := 0
	for _, offset := range []int64{-1, 0, 1} {
		counter := (currentTime / timeStep) + offset
		expected := generateTOTP(secret, counter)
		// Use constant-time comparison - bitwise OR accumulates matches
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			valid = 1
		}
	}
	return valid == 1
}

// generateTOTP generates a TOTP code for the given counter
func generateTOTP(secret string, counter int64) string {
	// Decode base32 secret
	secretBytes, err := base32Decode(secret)
	if err != nil {
		return ""
	}

	// Convert counter to 8-byte big-endian
	counterBytes := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		counterBytes[i] = byte(counter & 0xff)
		counter >>= 8
	}

	// Compute HMAC-SHA1
	h := hmac.New(sha1.New, secretBytes)
	h.Write(counterBytes)
	hash := h.Sum(nil)

	// Dynamic truncation (RFC 4226)
	offset := hash[len(hash)-1] & 0x0f
	binary := (int32(hash[offset])&0x7f)<<24 |
		(int32(hash[offset+1])&0xff)<<16 |
		(int32(hash[offset+2])&0xff)<<8 |
		(int32(hash[offset+3]) & 0xff)

	// Generate 6-digit code
	otp := binary % 1000000
	return fmt.Sprintf("%06d", otp)
}

// base32Decode decodes a base32 string (RFC 4648)
func base32Decode(s string) ([]byte, error) {
	// Remove padding and convert to uppercase
	s = strings.TrimRight(strings.ToUpper(s), "=")

	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	var bits uint64
	var bitCount uint

	result := make([]byte, 0, len(s)*5/8)

	for _, c := range s {
		idx := strings.IndexRune(alphabet, c)
		if idx < 0 {
			return nil, fmt.Errorf("invalid base32 character: %c", c)
		}

		bits = (bits << 5) | uint64(idx)
		bitCount += 5

		if bitCount >= 8 {
			bitCount -= 8
			result = append(result, byte(bits>>bitCount))
			bits &= (1 << bitCount) - 1
		}
	}

	return result, nil
}
