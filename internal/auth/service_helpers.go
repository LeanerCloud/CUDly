package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/hkdf"
)

// csrfKeyHKDFInfo is the HKDF "info" (domain-separation) label used when
// deriving the CSRF key from a master secret. It binds the derived key to this
// specific purpose so the CSRF key can never collide with a key derived from
// the same master secret for any other use.
const csrfKeyHKDFInfo = "CUDly:csrf-key:v1"

// DeriveCSRFKey derives a stable 32-byte CSRF key from a master secret using
// HKDF-SHA256 with a fixed domain-separation label.
//
// Production wiring passes the (already stable, deploy-provided) credential
// encryption key as the master secret, so every process and every Lambda
// cold-start derives the SAME CSRF key. This is what makes a CSRF token minted
// by one instance validate on another instance: ValidateCSRFToken recomputes
// HMAC-SHA256(csrfKey, rawSessionToken), and the csrfKey is now identical
// across the fleet instead of a per-process random value.
//
// HKDF domain separation (csrfKeyHKDFInfo) ensures the derived CSRF key is
// cryptographically independent of the master secret, so leaking one does not
// reveal the other. The master secret must be non-empty.
func DeriveCSRFKey(masterSecret []byte) ([]byte, error) {
	if len(masterSecret) == 0 {
		return nil, fmt.Errorf("auth: DeriveCSRFKey requires a non-empty master secret")
	}
	r := hkdf.New(sha256.New, masterSecret, nil /* salt */, []byte(csrfKeyHKDFInfo))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("auth: derive CSRF key: %w", err)
	}
	return key, nil
}

// hashSessionToken creates a SHA-256 hash of a session token for secure storage.
// This prevents session hijacking if the database is compromised, as attackers
// would only have hashes, not usable session tokens.
func hashSessionToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// deriveCSRFToken derives a CSRF token as HMAC-SHA256(csrfKey, rawSessionToken).
// The token is cryptographically bound to the session token and the server-side
// key: an attacker who reads the database cannot forge a CSRF token without the
// key, and cannot replay a CSRF token from another session.
func deriveCSRFToken(csrfKey []byte, rawSessionToken string) string {
	mac := hmac.New(sha256.New, csrfKey)
	mac.Write([]byte(rawSessionToken))
	return hex.EncodeToString(mac.Sum(nil))
}

// createSession creates a new session for a user
func (s *Service) createSession(ctx context.Context, user *User, userAgent, ipAddress string) (*Session, error) {
	// Generate a cryptographically random session token
	rawToken, err := generateToken()
	if err != nil {
		return nil, err
	}

	// Hash the token for secure storage.
	// The raw token is returned to the client; only the hash is stored.
	hashedToken := hashSessionToken(rawToken)

	// Derive the CSRF token as HMAC-SHA256(csrfKey, rawToken).
	// The token is bound to this session's raw token and the server-side key,
	// so it cannot be forged from a DB read alone. Not stored in the DB
	// (the stored value is the MAC itself, kept only for diagnostics/compat).
	csrfToken := deriveCSRFToken(s.csrfKey, rawToken)

	// Create session with hashed token for storage.
	// The csrf_token column holds the MAC for observability; validation
	// always recomputes it from the raw session token rather than trusting
	// the stored value, so DB exposure does not yield a usable CSRF token.
	storedSession := &Session{
		Token:     hashedToken, // Store the hash, not the raw token
		UserID:    user.ID,
		Email:     user.Email,
		ExpiresAt: time.Now().Add(s.sessionDuration),
		CreatedAt: time.Now(),
		UserAgent: userAgent,
		IPAddress: ipAddress,
		CSRFToken: csrfToken, // MAC stored for diagnostics only; not used in validation
	}

	if err := s.store.CreateSession(ctx, storedSession); err != nil {
		return nil, err
	}

	// Return session with raw token (for client)
	clientSession := &Session{
		Token:     rawToken, // Client gets the raw token
		UserID:    user.ID,
		Email:     user.Email,
		ExpiresAt: storedSession.ExpiresAt,
		CreatedAt: storedSession.CreatedAt,
		UserAgent: userAgent,
		IPAddress: ipAddress,
		CSRFToken: csrfToken, // Client receives the MAC as the CSRF token
	}

	return clientSession, nil
}

// generateToken generates a cryptographically secure random token by encoding
// 32 random bytes as base64url. Hashing CSPRNG output through SHA-256 adds
// nothing -- the raw bytes already carry 256 bits of entropy (03-N4).
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// containsAny checks if any element from requested is in allowed
func containsAny(allowed, requested []string) bool {
	allowedSet := make(map[string]bool)
	for _, a := range allowed {
		allowedSet[a] = true
	}
	for _, r := range requested {
		if allowedSet[r] {
			return true
		}
	}
	return false
}
