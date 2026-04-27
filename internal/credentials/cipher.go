// Package credentials provides AES-256-GCM encryption for cloud account credentials
// and helpers for resolving provider-specific credential structs.
package credentials

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/LeanerCloud/CUDly/internal/secrets"
)

// devKeyHex is the all-zero 32-byte AES-256 dev key, used ONLY when
// CREDENTIAL_ENCRYPTION_ALLOW_DEV_KEY=1 is explicitly set. It is intentionally
// not secret — it exists for local dev without a Secrets Manager dependency.
const devKeyHex = "0000000000000000000000000000000000000000000000000000000000000000"

// ErrNoKey is returned by LoadKey when no encryption key is configured and
// CREDENTIAL_ENCRYPTION_ALLOW_DEV_KEY is not set.
var ErrNoKey = errors.New("credentials: no credential encryption key configured")

// Env var names used by LoadKey, in priority order.
const (
	EnvSecretARN  = "CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN"  // AWS Secrets Manager ARN
	EnvSecretName = "CREDENTIAL_ENCRYPTION_KEY_SECRET_NAME" // Azure Key Vault secret name
	EnvSecretID   = "CREDENTIAL_ENCRYPTION_KEY_SECRET_ID"   // GCP Secret Manager secret ID
	EnvRawKey     = "CREDENTIAL_ENCRYPTION_KEY"             // Raw 64-char hex (ops/dev)
	EnvAllowDev   = "CREDENTIAL_ENCRYPTION_ALLOW_DEV_KEY"   // 1 = permit zero-key fallback
)

var (
	cachedKey       []byte
	cachedKeySource string
	cachedKeyOnce   sync.Once
	cachedKeyErr    error
)

// LoadKey loads the 32-byte AES-256 key for tenant credential encryption.
// First non-empty wins, in this order:
//
//  1. CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN  → resolver.GetSecret(arn)   (AWS)
//  2. CREDENTIAL_ENCRYPTION_KEY_SECRET_NAME → resolver.GetSecret(name)  (Azure)
//  3. CREDENTIAL_ENCRYPTION_KEY_SECRET_ID   → resolver.GetSecret(id)    (GCP)
//  4. CREDENTIAL_ENCRYPTION_KEY              → raw 64-char hex
//
// If none are set, returns ErrNoKey unless CREDENTIAL_ENCRYPTION_ALLOW_DEV_KEY=1
// is set (in which case the all-zero dev key is returned with a loud warning).
//
// The returned source string is the env var name that resolved (for logging).
// Callers MUST NOT log the key value.
//
// Result is memoized via sync.Once for the process lifetime; tests reset the
// cache via resetKeyCacheForTest() in this package's test files.
func LoadKey(ctx context.Context, resolver secrets.Resolver) (key []byte, source string, err error) {
	cachedKeyOnce.Do(func() {
		cachedKey, cachedKeySource, cachedKeyErr = loadKey(ctx, resolver)
	})
	return cachedKey, cachedKeySource, cachedKeyErr
}

// DevKey returns the all-zero 32-byte dev key. Exposed for the rekey
// migration command (cmd/rekey) which needs to detect rows encrypted under
// it without going through the LoadKey env-var path.
func DevKey() []byte {
	k, _ := decodeHexKey(devKeyHex)
	return k
}

func loadKey(ctx context.Context, resolver secrets.Resolver) ([]byte, string, error) {
	// Detect multiple-set misconfiguration upfront.
	var set []string
	for _, name := range []string{EnvSecretARN, EnvSecretName, EnvSecretID, EnvRawKey} {
		if os.Getenv(name) != "" {
			set = append(set, name)
		}
	}
	if len(set) > 1 {
		log.Printf("WARN: credentials: multiple encryption-key env vars set (%s); using first in priority order",
			strings.Join(set, ", "))
	}

	if arn := os.Getenv(EnvSecretARN); arn != "" {
		k, err := loadFromResolver(ctx, resolver, arn, EnvSecretARN)
		return k, EnvSecretARN, err
	}
	if name := os.Getenv(EnvSecretName); name != "" {
		k, err := loadFromResolver(ctx, resolver, name, EnvSecretName)
		return k, EnvSecretName, err
	}
	if id := os.Getenv(EnvSecretID); id != "" {
		k, err := loadFromResolver(ctx, resolver, id, EnvSecretID)
		return k, EnvSecretID, err
	}
	if hexKey := os.Getenv(EnvRawKey); hexKey != "" {
		k, err := decodeHexKey(hexKey)
		return k, EnvRawKey, err
	}

	if os.Getenv(EnvAllowDev) == "1" {
		log.Printf("WARN: credentials: %s=1 — using insecure all-zero dev key. NEVER use in production.", EnvAllowDev)
		k, err := decodeHexKey(devKeyHex)
		return k, EnvAllowDev, err
	}

	return nil, "", fmt.Errorf("%w (set one of %s, %s, %s, %s, or %s=1 for local dev)",
		ErrNoKey, EnvSecretARN, EnvSecretName, EnvSecretID, EnvRawKey, EnvAllowDev)
}

func loadFromResolver(ctx context.Context, resolver secrets.Resolver, secretID, sourceVar string) ([]byte, error) {
	if resolver == nil {
		return nil, fmt.Errorf("credentials: %s set but no secret resolver provided", sourceVar)
	}
	raw, err := resolver.GetSecret(ctx, secretID)
	if err != nil {
		return nil, fmt.Errorf("credentials: fetch secret via %s: %w", sourceVar, err)
	}
	return decodeHexKey(raw)
}

func decodeHexKey(hexKey string) ([]byte, error) {
	hexKey = strings.TrimSpace(hexKey)
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		// Don't leak the offending byte from hex.InvalidByteError into logs.
		return nil, fmt.Errorf("credentials: invalid hex key (length=%d)", len(hexKey))
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("credentials: key must be 32 bytes (64 hex chars), got %d bytes", len(key))
	}
	return key, nil
}

// Encrypt encrypts plaintext using AES-256-GCM.
// Returns a string in the format "<base64url(nonce)>.<base64url(ciphertext)>".
func Encrypt(key, plaintext []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("credentials: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("credentials: create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("credentials: generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	nonceB64 := base64.RawURLEncoding.EncodeToString(nonce)
	ctB64 := base64.RawURLEncoding.EncodeToString(ciphertext)
	return nonceB64 + "." + ctB64, nil
}

// Decrypt reverses Encrypt. Returns the original plaintext.
func Decrypt(key []byte, blob string) ([]byte, error) {
	parts := strings.SplitN(blob, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("credentials: malformed blob (expected nonce.ciphertext)")
	}

	nonce, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("credentials: decode nonce: %w", err)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("credentials: decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("credentials: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("credentials: create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("credentials: decrypt: %w", err)
	}
	return plaintext, nil
}
