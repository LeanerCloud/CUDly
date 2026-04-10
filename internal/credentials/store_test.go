package credentials

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// NewCredentialStore
// ---------------------------------------------------------------------------

// TestNewCredentialStore_ReturnsNonNil verifies the constructor returns a
// non-nil CredentialStore.  We pass a nil pool intentionally — it is only
// dereferenced on actual DB operations, which we do not invoke here.
func TestNewCredentialStore_ReturnsNonNil(t *testing.T) {
	store := NewCredentialStore(nil, make([]byte, 32))
	assert.NotNil(t, store)
}

// ---------------------------------------------------------------------------
// pgCredentialStore.SaveCredential — encrypt-error path
// ---------------------------------------------------------------------------

// TestSaveCredential_EncryptError verifies that SaveCredential returns an
// error when encryption fails (e.g. invalid key length), without ever
// reaching the DB pool.
func TestSaveCredential_EncryptError(t *testing.T) {
	s := &pgCredentialStore{
		pool: nil,             // never reached: error occurs in Encrypt
		key:  []byte("short"), // not 16/24/32 bytes → aes.NewCipher fails
	}
	err := s.SaveCredential(context.Background(), "acct1", "aws_access_keys", []byte("payload"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypt")
}

// ---------------------------------------------------------------------------
// pgCredentialStore.LoadRaw — decrypt-error path
// ---------------------------------------------------------------------------

// TestLoadRaw_EncryptedWithDifferentKey checks that LoadRaw returns a
// "decrypt" error when the stored blob was encrypted with a different key.
// We inject the blob directly into a mockCredentialStore, then decode
// through the pgCredentialStore's Decrypt logic.
//
// Because pgCredentialStore.pool is *pgxpool.Pool (concrete), we cannot
// inject a mock pool without modifying production code.  We therefore test
// the Decrypt path via a stand-alone call that mirrors what LoadRaw does.
func TestLoadRaw_DecryptPathViaHelpers(t *testing.T) {
	key := make([]byte, 32)

	// Encrypt with a different key so Decrypt fails.
	wrongKey := make([]byte, 32)
	wrongKey[0] = 0xFF
	blob, err := Encrypt(wrongKey, []byte("secret"))
	require.NoError(t, err)

	_, err = Decrypt(key, blob)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decrypt")
}
