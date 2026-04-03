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
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// devKey is a fixed 32-byte key used only in local development when no
// CREDENTIAL_ENCRYPTION_KEY or CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN is set.
// It is intentionally NOT secret — it exists solely to allow local dev
// without a Secrets Manager dependency.
const devKeyHex = "0000000000000000000000000000000000000000000000000000000000000000"

var (
	cachedKey     []byte
	cachedKeyOnce sync.Once
	cachedKeyErr  error
)

// KeyFromEnv loads the 32-byte AES-256 key using the following priority order:
//  1. CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN set → retrieve secret value from AWS Secrets
//     Manager and decode as 64-char hex string; result is cached for Lambda lifetime.
//  2. CREDENTIAL_ENCRYPTION_KEY set → decode as 64-char hex string directly.
//  3. Neither set → use the hardcoded dev key; logs a warning.
func KeyFromEnv() ([]byte, error) {
	cachedKeyOnce.Do(func() {
		cachedKey, cachedKeyErr = loadKey()
	})
	return cachedKey, cachedKeyErr
}

func loadKey() ([]byte, error) {
	if arn := os.Getenv("CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN"); arn != "" {
		return loadKeyFromSecretsManager(arn)
	}
	if hexKey := os.Getenv("CREDENTIAL_ENCRYPTION_KEY"); hexKey != "" {
		return decodeHexKey(hexKey)
	}
	log.Println("WARN: CREDENTIAL_ENCRYPTION_KEY not set — using insecure dev credential key")
	return decodeHexKey(devKeyHex)
}

func loadKeyFromSecretsManager(arn string) ([]byte, error) {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("credentials: load AWS config for Secrets Manager: %w", err)
	}
	client := secretsmanager.NewFromConfig(cfg)
	out, err := client.GetSecretValue(context.Background(), &secretsmanager.GetSecretValueInput{
		SecretId: &arn,
	})
	if err != nil {
		return nil, fmt.Errorf("credentials: GetSecretValue(%s): %w", arn, err)
	}
	if out.SecretString == nil {
		return nil, fmt.Errorf("credentials: secret %s has no string value", arn)
	}
	return decodeHexKey(*out.SecretString)
}

func decodeHexKey(hexKey string) ([]byte, error) {
	hexKey = strings.TrimSpace(hexKey)
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("credentials: invalid hex key: %w", err)
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
