package api

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateRegistrationRequest_AccountNameSanitized is the defense-in-depth
// regression test for #544 / #401: account_name from the unauthenticated
// POST /api/register endpoint must be CR/LF-stripped and length-capped at the
// data source before it is persisted or interpolated into any email header.
func TestValidateRegistrationRequest_AccountNameSanitized(t *testing.T) {
	t.Run("strips CRLF", func(t *testing.T) {
		req := RegistrationRequest{
			Provider:     "aws",
			ExternalID:   "ext-123",
			AccountName:  "Acme\r\nBcc: attacker@evil.example.com",
			ContactEmail: "user@example.com",
		}
		require.NoError(t, validateRegistrationRequest(&req))
		assert.NotContains(t, req.AccountName, "\r")
		assert.NotContains(t, req.AccountName, "\n")
	})

	t.Run("length-capped (rune-safe)", func(t *testing.T) {
		// Use a multibyte rune so the cap is exercised by rune count, not byte
		// length, and confirm truncation never splits a rune (valid UTF-8).
		req := RegistrationRequest{
			Provider:     "aws",
			ExternalID:   "ext-123",
			AccountName:  strings.Repeat("é", maxAccountNameLen+50),
			ContactEmail: "user@example.com",
		}
		require.NoError(t, validateRegistrationRequest(&req))
		assert.LessOrEqual(t, utf8.RuneCountInString(req.AccountName), maxAccountNameLen)
		assert.True(t, utf8.ValidString(req.AccountName), "cap must not split a multibyte rune")
	})

	t.Run("CRLF-only name is rejected as empty", func(t *testing.T) {
		req := RegistrationRequest{
			Provider:     "aws",
			ExternalID:   "ext-123",
			AccountName:  "\r\n",
			ContactEmail: "user@example.com",
		}
		err := validateRegistrationRequest(&req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "account_name is required")
	})

	t.Run("whitespace-only name is rejected as empty", func(t *testing.T) {
		req := RegistrationRequest{
			Provider:     "aws",
			ExternalID:   "ext-123",
			AccountName:  "   \t  ",
			ContactEmail: "user@example.com",
		}
		err := validateRegistrationRequest(&req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "account_name is required")
	})
}
