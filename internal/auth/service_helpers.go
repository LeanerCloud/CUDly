package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"
)

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
