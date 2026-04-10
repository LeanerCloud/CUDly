package credentials

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadKey_FromEnvVar exercises the CREDENTIAL_ENCRYPTION_KEY path.
func TestLoadKey_FromEnvVar(t *testing.T) {
	validHex := strings.Repeat("ab", 32) // 64 hex chars = 32 bytes
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", validHex)
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN", "")

	key, err := loadKey()
	require.NoError(t, err)
	assert.Len(t, key, 32)
}

// TestLoadKey_FallbackDevKey exercises the "neither env var set" fallback.
func TestLoadKey_FallbackDevKey(t *testing.T) {
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN", "")

	key, err := loadKey()
	require.NoError(t, err)
	assert.Len(t, key, 32)
	// The dev key is all zeros.
	for _, b := range key {
		assert.Equal(t, byte(0), b)
	}
}

// TestLoadKey_InvalidHexInEnv exercises an invalid hex key in the env var.
func TestLoadKey_InvalidHexInEnv(t *testing.T) {
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "this-is-not-hex!!")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN", "")

	_, err := loadKey()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid hex key")
}

// TestLoadKey_ShortHexInEnv exercises a hex string with wrong byte length.
func TestLoadKey_ShortHexInEnv(t *testing.T) {
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "deadbeef") // only 4 bytes
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN", "")

	_, err := loadKey()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key must be 32 bytes")
}

// TestKeyFromEnv_CachesResult verifies that repeated calls return the same
// slice (the sync.Once cache is exercised via a reset — we test loadKey
// directly above; here we just verify KeyFromEnv doesn't panic and returns
// something consistent).
//
// Note: cachedKeyOnce is package-level and executes once per test binary.
// We cannot easily reset it without modifying production code, so this test
// exercises the cached-hit path.
func TestKeyFromEnv_ConsistentResult(t *testing.T) {
	// The first call may have already happened; we just verify it's stable.
	k1, err1 := KeyFromEnv()
	k2, err2 := KeyFromEnv()

	assert.Equal(t, err1, err2)
	assert.Equal(t, k1, k2)
}

// TestEncrypt_InvalidKey verifies Encrypt returns an error for a bad key length.
func TestEncrypt_InvalidKeyLength(t *testing.T) {
	shortKey := []byte("tooshort") // not 16/24/32 bytes
	_, err := Encrypt(shortKey, []byte("payload"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create cipher")
}

// TestDecrypt_BadBase64Ciphertext verifies that bad base64 in the ciphertext part errors.
func TestDecrypt_BadBase64Ciphertext(t *testing.T) {
	// valid nonce part, invalid ciphertext part
	blob, err := Encrypt(testKey, []byte("hello"))
	require.NoError(t, err)
	parts := strings.SplitN(blob, ".", 2)
	require.Len(t, parts, 2)

	badBlob := parts[0] + ".!!!bad base64!!!"
	_, err = Decrypt(testKey, badBlob)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decode ciphertext")
}

// TestDecrypt_InvalidKeyLength verifies Decrypt returns an error for a bad key.
func TestDecrypt_InvalidKeyLength(t *testing.T) {
	blob, err := Encrypt(testKey, []byte("hello"))
	require.NoError(t, err)

	shortKey := []byte("tooshort")
	_, err = Decrypt(shortKey, blob)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create cipher")
}

// TestDecodeHexKey_Exactly32Bytes verifies exactly 32 bytes is accepted.
func TestDecodeHexKey_ExactlyCorrect(t *testing.T) {
	key, err := decodeHexKey(strings.Repeat("ff", 32))
	require.NoError(t, err)
	assert.Len(t, key, 32)
	for _, b := range key {
		assert.Equal(t, byte(0xff), b)
	}
}

// TestDecodeHexKey_TooLong verifies > 32 bytes is rejected.
func TestDecodeHexKey_TooLong(t *testing.T) {
	_, err := decodeHexKey(strings.Repeat("ab", 33)) // 33 bytes
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key must be 32 bytes")
}

// TestLoadKey_SecretARNPath exercises the ARN branch of loadKey.
// loadKeyFromSecretsManager will fail without real AWS credentials, but
// calling loadKey with the ARN env var set exercises the branch statement.
func TestLoadKey_SecretARNPath(t *testing.T) {
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN", "arn:aws:secretsmanager:us-east-1:123456789012:secret:test")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "")

	_, err := loadKey()
	// loadKeyFromSecretsManager will fail (no real AWS creds), but the call
	// itself is what we need to cover — the ARN branch in loadKey.
	assert.Error(t, err)
}
