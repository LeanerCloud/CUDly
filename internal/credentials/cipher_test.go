package credentials

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testKey = make([]byte, 32) // 32-byte zero key for testing

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		plaintext string
	}{
		{"empty string", ""},
		{"short text", "hello"},
		{"json payload", `{"access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_access_key":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}`},
		{"unicode", "こんにちは世界"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blob, err := Encrypt(testKey, []byte(tt.plaintext))
			require.NoError(t, err)
			assert.Contains(t, blob, ".", "blob should contain nonce.ciphertext separator")

			plaintext, err := Decrypt(testKey, blob)
			require.NoError(t, err)
			assert.Equal(t, tt.plaintext, string(plaintext))
		})
	}
}

func TestEncrypt_ProducesUniqueCiphertexts(t *testing.T) {
	// Each encryption should produce a different blob (random nonce).
	plaintext := []byte("same input")
	blob1, err := Encrypt(testKey, plaintext)
	require.NoError(t, err)
	blob2, err := Encrypt(testKey, plaintext)
	require.NoError(t, err)
	assert.NotEqual(t, blob1, blob2, "repeated encryptions should produce different blobs")
}

func TestDecrypt_WrongKey(t *testing.T) {
	blob, err := Encrypt(testKey, []byte("secret"))
	require.NoError(t, err)

	wrongKey := make([]byte, 32)
	wrongKey[0] = 0xFF
	_, err = Decrypt(wrongKey, blob)
	assert.Error(t, err, "decryption with wrong key should fail")
}

func TestDecrypt_MalformedBlob(t *testing.T) {
	cases := []string{
		"",
		"no-dot",
		"bad base64!!!.more bad",
	}
	for _, blob := range cases {
		_, err := Decrypt(testKey, blob)
		assert.Error(t, err, "expected error for malformed blob: %q", blob)
	}
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	blob, err := Encrypt(testKey, []byte("secret"))
	require.NoError(t, err)

	// Flip a bit in the ciphertext part.
	parts := strings.SplitN(blob, ".", 2)
	require.Len(t, parts, 2)
	ct := []byte(parts[1])
	if len(ct) > 0 {
		ct[0] ^= 0x01
	}
	tampered := parts[0] + "." + string(ct)

	_, err = Decrypt(testKey, tampered)
	assert.Error(t, err, "tampered ciphertext should not decrypt")
}

func TestDecodeHexKey_Valid(t *testing.T) {
	hexKey := strings.Repeat("ab", 32) // 64 hex chars = 32 bytes
	key, err := decodeHexKey(hexKey)
	require.NoError(t, err)
	assert.Len(t, key, 32)
}

func TestDecodeHexKey_TooShort(t *testing.T) {
	_, err := decodeHexKey("deadbeef")
	assert.Error(t, err)
}

func TestDecodeHexKey_Invalid(t *testing.T) {
	_, err := decodeHexKey("not-hex-at-all!!")
	assert.Error(t, err)
}

func TestDecodeHexKey_WithWhitespace(t *testing.T) {
	hexKey := "  " + strings.Repeat("ab", 32) + "\n"
	key, err := decodeHexKey(hexKey)
	require.NoError(t, err)
	assert.Len(t, key, 32)
}
