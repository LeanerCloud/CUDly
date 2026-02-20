package auth

import (
	"context"
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

// createSession creates a new session for a user
func (s *Service) createSession(ctx context.Context, user *User, userAgent, ipAddress string) (*Session, error) {
	// Generate a cryptographically random session token
	rawToken, err := generateToken()
	if err != nil {
		return nil, err
	}

	// Hash the token for secure storage in DynamoDB
	// The raw token is returned to the client; only the hash is stored
	hashedToken := hashSessionToken(rawToken)

	// Generate CSRF token for state-changing request protection
	csrfToken, err := generateToken()
	if err != nil {
		return nil, err
	}

	// Create session with hashed token for storage
	storedSession := &Session{
		Token:     hashedToken, // Store the hash, not the raw token
		UserID:    user.ID,
		Email:     user.Email,
		Role:      user.Role,
		ExpiresAt: time.Now().Add(s.sessionDuration),
		CreatedAt: time.Now(),
		UserAgent: userAgent,
		IPAddress: ipAddress,
		CSRFToken: csrfToken,
	}

	if err := s.store.CreateSession(ctx, storedSession); err != nil {
		return nil, err
	}

	// Return session with raw token (for client)
	clientSession := &Session{
		Token:     rawToken, // Client gets the raw token
		UserID:    user.ID,
		Email:     user.Email,
		Role:      user.Role,
		ExpiresAt: storedSession.ExpiresAt,
		CreatedAt: storedSession.CreatedAt,
		UserAgent: userAgent,
		IPAddress: ipAddress,
		CSRFToken: csrfToken,
	}

	return clientSession, nil
}

// generateSalt generates a cryptographically secure random salt
func generateSalt() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(bytes), nil
}

// generateToken generates a cryptographically secure random token
func generateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	hash := sha256.Sum256(bytes)
	return hex.EncodeToString(hash[:]), nil
}

// hashToken creates a SHA-256 hash of a token for secure storage
func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
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
