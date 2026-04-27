package credentials

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/internal/secrets"
)

// resetKeyCacheForTest clears the LoadKey memoization cache so each test
// gets a fresh sync.Once. Test-only — never called from production code.
func resetKeyCacheForTest() {
	cachedKey = nil
	cachedKeySource = ""
	cachedKeyErr = nil
	cachedKeyOnce = sync.Once{}
}

// fakeResolver is a test double for secrets.Resolver. It returns the
// configured value for GetSecret and records the secret IDs it was asked for.
type fakeResolver struct {
	value string
	err   error
	asked []string
}

func (f *fakeResolver) GetSecret(_ context.Context, secretID string) (string, error) {
	f.asked = append(f.asked, secretID)
	return f.value, f.err
}
func (f *fakeResolver) GetSecretJSON(_ context.Context, _ string) (map[string]any, error) {
	return nil, errors.New("not used in tests")
}
func (f *fakeResolver) PutSecret(_ context.Context, _ string, _ string) error {
	return errors.New("not used in tests")
}
func (f *fakeResolver) ListSecrets(_ context.Context, _ string) ([]string, error) {
	return nil, errors.New("not used in tests")
}
func (f *fakeResolver) Close() error { return nil }

// Compile-time assertion that fakeResolver satisfies secrets.Resolver.
var _ secrets.Resolver = (*fakeResolver)(nil)

// validHexKey is a 64-char hex string (32 bytes), distinct from the dev key.
const validHexKey = "abababababababababababababababababababababababababababababababab"

func clearAllKeyEnvs(t *testing.T) {
	t.Helper()
	for _, name := range []string{EnvSecretARN, EnvSecretName, EnvSecretID, EnvRawKey, EnvAllowDev} {
		t.Setenv(name, "")
	}
}

// TestLoadKey_NoKey_Fails confirms that the silent zero-key fallback is GONE.
// Before this change a missing key silently returned the all-zero dev key.
func TestLoadKey_NoKey_Fails(t *testing.T) {
	resetKeyCacheForTest()
	clearAllKeyEnvs(t)

	key, source, err := LoadKey(context.Background(), nil)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoKey), "expected ErrNoKey sentinel, got %v", err)
	assert.Nil(t, key)
	assert.Empty(t, source)
}

// TestLoadKey_DevKey_OnlyWithFlag confirms the zero-key path requires explicit opt-in.
func TestLoadKey_DevKey_OnlyWithFlag(t *testing.T) {
	resetKeyCacheForTest()
	clearAllKeyEnvs(t)
	t.Setenv(EnvAllowDev, "1")

	key, source, err := LoadKey(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, key, 32)
	for i, b := range key {
		assert.Equalf(t, byte(0), b, "byte %d should be zero in dev key", i)
	}
	assert.Equal(t, EnvAllowDev, source)
}

// TestLoadKey_ARNPath uses a fake resolver to exercise the AWS branch.
// This is also the AWS regression test — protects the currently-working path.
func TestLoadKey_ARNPath(t *testing.T) {
	resetKeyCacheForTest()
	clearAllKeyEnvs(t)
	t.Setenv(EnvSecretARN, "arn:aws:secretsmanager:us-east-1:123:secret:enc-key-AbC")

	r := &fakeResolver{value: validHexKey}
	key, source, err := LoadKey(context.Background(), r)
	require.NoError(t, err)
	assert.Len(t, key, 32)
	assert.Equal(t, EnvSecretARN, source)
	require.Len(t, r.asked, 1)
	assert.Equal(t, "arn:aws:secretsmanager:us-east-1:123:secret:enc-key-AbC", r.asked[0])
}

// TestLoadKey_NamePath exercises the Azure branch.
func TestLoadKey_NamePath(t *testing.T) {
	resetKeyCacheForTest()
	clearAllKeyEnvs(t)
	t.Setenv(EnvSecretName, "credential-encryption-key")

	r := &fakeResolver{value: validHexKey}
	key, source, err := LoadKey(context.Background(), r)
	require.NoError(t, err)
	assert.Len(t, key, 32)
	assert.Equal(t, EnvSecretName, source)
	require.Len(t, r.asked, 1)
	assert.Equal(t, "credential-encryption-key", r.asked[0])
}

// TestLoadKey_IDPath exercises the GCP branch.
func TestLoadKey_IDPath(t *testing.T) {
	resetKeyCacheForTest()
	clearAllKeyEnvs(t)
	t.Setenv(EnvSecretID, "cudly-credential-encryption-key")

	r := &fakeResolver{value: validHexKey}
	key, source, err := LoadKey(context.Background(), r)
	require.NoError(t, err)
	assert.Len(t, key, 32)
	assert.Equal(t, EnvSecretID, source)
	require.Len(t, r.asked, 1)
	assert.Equal(t, "cudly-credential-encryption-key", r.asked[0])
}

// TestLoadKey_Precedence confirms ARN beats NAME beats ID beats raw KEY when
// multiple are set. A WARN is logged but the first in priority order wins.
func TestLoadKey_Precedence(t *testing.T) {
	resetKeyCacheForTest()
	clearAllKeyEnvs(t)
	t.Setenv(EnvSecretARN, "the-arn")
	t.Setenv(EnvSecretName, "the-name")
	t.Setenv(EnvSecretID, "the-id")
	t.Setenv(EnvRawKey, validHexKey)

	r := &fakeResolver{value: validHexKey}
	_, source, err := LoadKey(context.Background(), r)
	require.NoError(t, err)
	assert.Equal(t, EnvSecretARN, source, "ARN should win precedence")
	require.Len(t, r.asked, 1)
	assert.Equal(t, "the-arn", r.asked[0])
}

// TestLoadKey_RawHexKey exercises the bare CREDENTIAL_ENCRYPTION_KEY path
// (still supported for ops/dev).
func TestLoadKey_RawHexKey(t *testing.T) {
	resetKeyCacheForTest()
	clearAllKeyEnvs(t)
	t.Setenv(EnvRawKey, validHexKey)

	key, source, err := LoadKey(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, key, 32)
	assert.Equal(t, EnvRawKey, source)
}

// TestLoadKey_ResolverError surfaces resolver errors with context.
func TestLoadKey_ResolverError(t *testing.T) {
	resetKeyCacheForTest()
	clearAllKeyEnvs(t)
	t.Setenv(EnvSecretName, "missing-secret")

	r := &fakeResolver{err: errors.New("vault unreachable")}
	_, _, err := LoadKey(context.Background(), r)
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvSecretName)
	assert.Contains(t, err.Error(), "vault unreachable")
}

// TestLoadKey_ARNWithoutResolver returns a clear error rather than a panic.
func TestLoadKey_ARNWithoutResolver(t *testing.T) {
	resetKeyCacheForTest()
	clearAllKeyEnvs(t)
	t.Setenv(EnvSecretARN, "some-arn")

	_, _, err := LoadKey(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvSecretARN)
	assert.Contains(t, err.Error(), "no secret resolver provided")
}

// TestLoadKey_RoundTrip verifies a fetched-via-resolver key actually works for
// encrypt/decrypt — protects against silent format mismatches (hex vs base64,
// trailing whitespace, etc.).
func TestLoadKey_RoundTrip(t *testing.T) {
	resetKeyCacheForTest()
	clearAllKeyEnvs(t)
	t.Setenv(EnvSecretARN, "test-arn")

	r := &fakeResolver{value: "  " + validHexKey + "\n"} // exercise whitespace trim
	key, _, err := LoadKey(context.Background(), r)
	require.NoError(t, err)

	blob, err := Encrypt(key, []byte("payload"))
	require.NoError(t, err)
	plaintext, err := Decrypt(key, blob)
	require.NoError(t, err)
	assert.Equal(t, "payload", string(plaintext))
}

// TestDecodeHexKey_DoesNotLeakBadByte ensures the error message does not
// embed the offending byte from hex.InvalidByteError.
func TestDecodeHexKey_DoesNotLeakBadByte(t *testing.T) {
	_, err := decodeHexKey("zz" + strings.Repeat("ab", 31))
	require.Error(t, err)
	// Old wrapping included the offending byte; new wrapping reports only length.
	assert.NotContains(t, err.Error(), "'z'")
	assert.NotContains(t, err.Error(), "byte 0x")
	assert.Contains(t, err.Error(), "length=")
}

// TestEncrypt_InvalidKeyLength preserves the existing behavior coverage.
func TestEncrypt_InvalidKeyLength(t *testing.T) {
	shortKey := []byte("tooshort")
	_, err := Encrypt(shortKey, []byte("payload"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create cipher")
}

// TestDecrypt_BadBase64Ciphertext preserves existing coverage.
func TestDecrypt_BadBase64Ciphertext(t *testing.T) {
	blob, err := Encrypt(testKey, []byte("hello"))
	require.NoError(t, err)
	parts := strings.SplitN(blob, ".", 2)
	require.Len(t, parts, 2)

	badBlob := parts[0] + ".!!!bad base64!!!"
	_, err = Decrypt(testKey, badBlob)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decode ciphertext")
}

// TestDecrypt_InvalidKeyLength preserves existing coverage.
func TestDecrypt_InvalidKeyLength(t *testing.T) {
	blob, err := Encrypt(testKey, []byte("hello"))
	require.NoError(t, err)

	shortKey := []byte("tooshort")
	_, err = Decrypt(shortKey, blob)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create cipher")
}

// TestDecodeHexKey_ExactlyCorrect preserves existing coverage.
func TestDecodeHexKey_ExactlyCorrect(t *testing.T) {
	key, err := decodeHexKey(strings.Repeat("ff", 32))
	require.NoError(t, err)
	assert.Len(t, key, 32)
	for _, b := range key {
		assert.Equal(t, byte(0xff), b)
	}
}

// TestDecodeHexKey_TooLong preserves existing coverage.
func TestDecodeHexKey_TooLong(t *testing.T) {
	_, err := decodeHexKey(strings.Repeat("ab", 33))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key must be 32 bytes")
}

// TestDevKey_AllZero confirms the exported DevKey() helper returns the all-zero key
// (used by the rekey migration command).
func TestDevKey_AllZero(t *testing.T) {
	key := DevKey()
	require.Len(t, key, 32)
	for _, b := range key {
		assert.Equal(t, byte(0), b)
	}
}
