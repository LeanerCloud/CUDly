package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/internal/credentials"
)

// TestRekeyOne_RoundTrip verifies the encrypt/decrypt loop without DB.
// It exercises the helper used by rekeyAccountCredentials per row. We run
// the production credentials.Encrypt/Decrypt directly to confirm:
//   - decrypt(zeroKey, zeroBlob) → plaintext (the rekey path triggers)
//   - decrypt(zeroKey, realBlob) → error (the skip path triggers)
//   - encrypt(realKey, plaintext) round-trips
//
// The transaction wrapper inside rekeyOne is exercised by an integration
// test against testcontainers Postgres in CI; that's heavier than warranted
// for a unit test, so we cover the crypto half here.
func TestRekey_DecryptionRouting(t *testing.T) {
	zeroKey := credentials.DevKey()
	realKey := decodeHex(t, strings.Repeat("ab", 32))

	plaintext := []byte(`{"access_key_id":"AKIA","secret_access_key":"abc"}`)

	zeroBlob, err := credentials.Encrypt(zeroKey, plaintext)
	require.NoError(t, err)

	// Zero-key decrypt of a zero-encrypted blob → plaintext recovered.
	got, err := credentials.Decrypt(zeroKey, zeroBlob)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)

	// Re-encrypt with real key, then prove zero-key decrypt fails on the
	// new blob (proves the skip-path detection works).
	realBlob, err := credentials.Encrypt(realKey, plaintext)
	require.NoError(t, err)
	_, err = credentials.Decrypt(zeroKey, realBlob)
	require.Error(t, err, "zero key must NOT decrypt real-key ciphertext (this is how rekey detects already-real rows)")

	// And the real key still recovers the plaintext from the new blob.
	got, err = credentials.Decrypt(realKey, realBlob)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestIsEqual_KeyComparison(t *testing.T) {
	a := credentials.DevKey()
	b := credentials.DevKey()
	assert.True(t, isEqual(a, b))

	c := decodeHex(t, strings.Repeat("ff", 32))
	assert.False(t, isEqual(a, c))
	assert.False(t, isEqual(a, []byte{0, 0, 0})) // length mismatch
}

func decodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		var hi, lo byte
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			hi = c - '0'
		case c >= 'a' && c <= 'f':
			hi = c - 'a' + 10
		default:
			t.Fatalf("bad hex char %q", c)
		}
		switch c := s[i+1]; {
		case c >= '0' && c <= '9':
			lo = c - '0'
		case c >= 'a' && c <= 'f':
			lo = c - 'a' + 10
		default:
			t.Fatalf("bad hex char %q", c)
		}
		b[i/2] = hi<<4 | lo
	}
	return b
}
