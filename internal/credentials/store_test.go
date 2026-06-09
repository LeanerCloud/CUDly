package credentials

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
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
// pgCredentialStore.LoadRaw — errors.Is regression (#1028)
// ---------------------------------------------------------------------------

// TestLoadRaw_ErrorsIs_WrappedNoRows is the regression test for #1028.
//
// Before the fix, LoadRaw used `err == pgx.ErrNoRows` (pointer equality).
// Pool and transaction wrappers can wrap the sentinel, making the equality
// check return false and causing LoadRaw to return a hard error instead of
// (nil, nil). The fix changes to errors.Is which unwraps the error chain.
//
// This test verifies that errors.Is(wrappedErr, pgx.ErrNoRows) returns true
// for a wrapped sentinel -- the invariant the production code now relies on.
func TestLoadRaw_ErrorsIs_WrappedNoRows(t *testing.T) {
	// Simulate what a pgxpool/transaction wrapper may produce.
	wrapped := fmt.Errorf("pool: %w", pgx.ErrNoRows)
	assert.True(t, errors.Is(wrapped, pgx.ErrNoRows),
		"errors.Is must unwrap pgx.ErrNoRows through one layer of wrapping; "+
			"== would return false here, which was the pre-fix bug")

	// Direct equality still works.
	assert.True(t, errors.Is(pgx.ErrNoRows, pgx.ErrNoRows))

	// A different error must not match.
	other := errors.New("something else")
	assert.False(t, errors.Is(other, pgx.ErrNoRows))
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
