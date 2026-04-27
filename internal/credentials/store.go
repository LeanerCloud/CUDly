package credentials

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CredentialStore handles encrypted credential blobs backed by the
// account_credentials table. The interface is defined here (not in config/)
// to avoid circular imports: credentials→config is fine; config→credentials
// would be circular.
type CredentialStore interface {
	// SaveCredential encrypts payload and persists it for accountID/credType.
	// If a record for the same (accountID, credType) already exists it is replaced.
	SaveCredential(ctx context.Context, accountID, credType string, payload []byte) error

	// LoadRaw decrypts and returns the raw credential bytes.
	// Returns nil, nil when no credential is stored for the given (accountID, credType).
	LoadRaw(ctx context.Context, accountID, credType string) ([]byte, error)

	// DeleteCredential removes the stored credential for accountID/credType.
	DeleteCredential(ctx context.Context, accountID, credType string) error

	// HasCredential reports whether any credential exists for accountID/credType.
	HasCredential(ctx context.Context, accountID, credType string) (bool, error)

	// EncryptPayload encrypts plaintext using the store's AES-256-GCM key.
	// Used to encrypt credential data in the account_registrations table.
	EncryptPayload(plaintext []byte) (string, error)

	// DecryptPayload decrypts a ciphertext blob encrypted by EncryptPayload.
	DecryptPayload(ciphertext string) ([]byte, error)
}

// NewCredentialStore creates a CredentialStore backed by PostgreSQL.
// encKey must be the 32-byte AES-256 key (obtain via LoadKey).
func NewCredentialStore(pool *pgxpool.Pool, encKey []byte) CredentialStore {
	return &pgCredentialStore{pool: pool, key: encKey}
}

type pgCredentialStore struct {
	pool *pgxpool.Pool
	key  []byte
}

func (s *pgCredentialStore) SaveCredential(ctx context.Context, accountID, credType string, payload []byte) error {
	blob, err := Encrypt(s.key, payload)
	if err != nil {
		return fmt.Errorf("credentials: encrypt: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO account_credentials (id, account_id, credential_type, encrypted_blob)
		VALUES (uuid_generate_v4(), $1, $2, $3)
		ON CONFLICT (account_id, credential_type) DO UPDATE SET
			encrypted_blob = $3,
			updated_at = NOW()
	`, accountID, credType, blob)
	if err != nil {
		return fmt.Errorf("credentials: save credential: %w", err)
	}
	return nil
}

func (s *pgCredentialStore) LoadRaw(ctx context.Context, accountID, credType string) ([]byte, error) {
	var blob string
	err := s.pool.QueryRow(ctx,
		`SELECT encrypted_blob FROM account_credentials WHERE account_id = $1 AND credential_type = $2`,
		accountID, credType,
	).Scan(&blob)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("credentials: load credential: %w", err)
	}
	plaintext, err := Decrypt(s.key, blob)
	if err != nil {
		return nil, fmt.Errorf("credentials: decrypt: %w", err)
	}
	return plaintext, nil
}

func (s *pgCredentialStore) DeleteCredential(ctx context.Context, accountID, credType string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM account_credentials WHERE account_id = $1 AND credential_type = $2`,
		accountID, credType,
	)
	if err != nil {
		return fmt.Errorf("credentials: delete credential: %w", err)
	}
	return nil
}

func (s *pgCredentialStore) EncryptPayload(plaintext []byte) (string, error) {
	return Encrypt(s.key, plaintext)
}

func (s *pgCredentialStore) DecryptPayload(ciphertext string) ([]byte, error) {
	return Decrypt(s.key, ciphertext)
}

func (s *pgCredentialStore) HasCredential(ctx context.Context, accountID, credType string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM account_credentials WHERE account_id = $1 AND credential_type = $2)`,
		accountID, credType,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("credentials: check credential: %w", err)
	}
	return exists, nil
}
