package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// Configuration constants
const (
	// PasswordResetExpiry is how long password reset tokens are valid
	PasswordResetExpiry = 1 * time.Hour

	// DefaultSessionDurationHours is the default session duration in hours
	DefaultSessionDurationHours = 24

	// Account lockout settings for brute-force protection
	// MaxFailedLoginAttempts is the number of failed attempts before lockout
	MaxFailedLoginAttempts = 5

	// AccountLockoutDuration is how long an account is locked after max failed attempts
	AccountLockoutDuration = 15 * time.Minute
)

// Service handles authentication and authorization
type Service struct {
	store              StoreInterface
	emailSender        EmailSenderInterface
	sessionDuration    time.Duration
	dashboardURL       string
	bcryptCostOverride int // if > 0, overrides bcryptCost const (used by tests for speed)
	onPasswordChange   func(ctx context.Context, userID, newPassword string)
}

// ServiceConfig holds configuration for the auth service
type ServiceConfig struct {
	Store            StoreInterface
	EmailSender      EmailSenderInterface
	SessionDuration  time.Duration
	DashboardURL     string
	OnPasswordChange func(ctx context.Context, userID, newPassword string)
}

// NewService creates a new auth service
func NewService(cfg ServiceConfig) *Service {
	if cfg.SessionDuration == 0 {
		cfg.SessionDuration = time.Duration(DefaultSessionDurationHours) * time.Hour
	}

	// SECURITY: Validate that dashboard URL uses HTTPS in production
	// This prevents password reset tokens from being leaked over HTTP
	if cfg.DashboardURL != "" && !strings.HasPrefix(cfg.DashboardURL, "https://") {
		// Allow http for localhost development only
		if !strings.HasPrefix(cfg.DashboardURL, "http://localhost") &&
			!strings.HasPrefix(cfg.DashboardURL, "http://127.0.0.1") {
			// Strip query string before logging to avoid leaking tokens in log output
			safeURL := cfg.DashboardURL
			if idx := strings.IndexByte(safeURL, '?'); idx >= 0 {
				safeURL = safeURL[:idx]
			}
			logging.Warnf("SECURITY WARNING: Dashboard URL does not use HTTPS: %s. Password reset links may be insecure.", safeURL)
		}
	}

	return &Service{
		store:            cfg.Store,
		emailSender:      cfg.EmailSender,
		sessionDuration:  cfg.SessionDuration,
		dashboardURL:     cfg.DashboardURL,
		onPasswordChange: cfg.OnPasswordChange,
	}
}

// notifyPasswordChange calls the password change callback if configured
func (s *Service) notifyPasswordChange(ctx context.Context, userID, newPassword string) {
	if s.onPasswordChange != nil {
		s.onPasswordChange(ctx, userID, newPassword)
	}
}

// ensureStore returns an error if the auth store is not initialized
func (s *Service) ensureStore() error {
	if s.store == nil {
		return fmt.Errorf("auth store not initialized")
	}
	return nil
}

// Login authenticates a user and creates a session
func (s *Service) Login(ctx context.Context, req LoginRequest) (*LoginResponse, error) {
	if err := s.ensureStore(); err != nil {
		return nil, err
	}

	// Use only the address portion to prevent RFC 5322 display-name attacks
	// e.g. `"Attacker" <user@host>` must not be forwarded verbatim to the store
	parsed, err := mail.ParseAddress(req.Email)
	if err != nil {
		return nil, fmt.Errorf("invalid email format")
	}
	req.Email = parsed.Address

	user, err := s.getUserAndValidateStatus(ctx, req.Email)
	if err != nil {
		return nil, err
	}

	if err := s.verifyPasswordAndMFA(ctx, user, req); err != nil {
		return nil, err
	}

	return s.completeSuccessfulLogin(ctx, user)
}

// getUserAndValidateStatus retrieves user and checks if account is active and unlocked
func (s *Service) getUserAndValidateStatus(ctx context.Context, email string) (*User, error) {
	user, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("authentication failed")
	}
	if user == nil {
		return nil, fmt.Errorf("invalid email or password")
	}

	if !user.Active {
		return nil, fmt.Errorf("invalid email or password")
	}

	if user.LockedUntil != nil && time.Now().Before(*user.LockedUntil) {
		remainingTime := time.Until(*user.LockedUntil).Round(time.Minute)
		// Omit user.ID from log to avoid leaking internal identifiers to log
		logging.Warnf("Login attempt for locked account (locked for %v more)", remainingTime)
		return nil, fmt.Errorf("invalid email or password")
	}

	return user, nil
}

// verifyPasswordAndMFA verifies password and MFA code if enabled
func (s *Service) verifyPasswordAndMFA(ctx context.Context, user *User, req LoginRequest) error {
	// Use a generic error for missing password hash to avoid leaking account state
	// (a distinct message would reveal that the account exists but has no password set)
	if user.PasswordHash == "" {
		return fmt.Errorf("invalid email or password")
	}

	if !s.verifyPassword(req.Password, user.PasswordHash) {
		s.recordFailedLogin(ctx, user)
		return fmt.Errorf("invalid email or password")
	}

	if user.MFAEnabled {
		if req.MFACode == "" {
			return fmt.Errorf("MFA code required")
		}
		if user.MFASecret == "" {
			return fmt.Errorf("MFA is enabled but not configured")
		}
		if !verifyTOTP(user.MFASecret, req.MFACode) {
			s.recordFailedLogin(ctx, user)
			return fmt.Errorf("invalid MFA code")
		}
	}

	return nil
}

// completeSuccessfulLogin creates session and updates user login info
func (s *Service) completeSuccessfulLogin(ctx context.Context, user *User) (*LoginResponse, error) {
	session, err := s.createSession(ctx, user, "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	now := time.Now()
	user.LastLoginAt = &now
	user.FailedLoginAttempts = 0
	user.LockedUntil = nil
	if err := s.store.UpdateUser(ctx, user); err != nil {
		logging.Warnf("Failed to update login info for user %s: %v", user.ID, err)
	}

	return &LoginResponse{
		Token:     session.Token,
		ExpiresAt: session.ExpiresAt,
		User: &UserInfo{
			ID:         user.ID,
			Email:      user.Email,
			Role:       user.Role,
			Groups:     user.GroupIDs,
			MFAEnabled: user.MFAEnabled,
		},
		CSRFToken: session.CSRFToken,
	}, nil
}

// Logout invalidates a session
func (s *Service) Logout(ctx context.Context, token string) error {
	if err := s.ensureStore(); err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("token is required")
	}
	hashedToken := hashSessionToken(token)
	return s.store.DeleteSession(ctx, hashedToken)
}

// ValidateSession checks if a session is valid and returns user info
func (s *Service) ValidateSession(ctx context.Context, token string) (*Session, error) {
	if err := s.ensureStore(); err != nil {
		return nil, err
	}

	hashedToken := hashSessionToken(token)

	session, err := s.store.GetSession(ctx, hashedToken)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("session not found")
	}

	if session.ExpiresAt.IsZero() {
		return nil, fmt.Errorf("session has no expiry: data integrity error")
	}

	if time.Now().After(session.ExpiresAt) {
		if err := s.store.DeleteSession(ctx, hashedToken); err != nil {
			logging.Warnf("Failed to delete expired session: %v", err)
		}
		return nil, fmt.Errorf("session expired")
	}

	// Return a copy of the session with the original token (not the hash) for client use.
	// Copying avoids mutating a potentially-shared store-cached pointer.
	result := *session
	result.Token = token
	return &result, nil
}

// ValidateCSRFToken validates the CSRF token for a session
func (s *Service) ValidateCSRFToken(ctx context.Context, sessionToken, csrfToken string) error {
	if csrfToken == "" {
		return fmt.Errorf("CSRF token is required")
	}

	session, err := s.ValidateSession(ctx, sessionToken)
	if err != nil {
		return fmt.Errorf("invalid session: %w", err)
	}

	if session.CSRFToken == "" {
		// Security: Reject sessions without CSRF tokens instead of allowing bypass
		// Legacy sessions must re-authenticate to get a proper CSRF token
		logging.Warnf("Rejecting session without CSRF token")
		return fmt.Errorf("session requires re-authentication for CSRF protection")
	}

	// Use constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(session.CSRFToken), []byte(csrfToken)) != 1 {
		return fmt.Errorf("invalid CSRF token")
	}

	return nil
}

// CleanupExpiredSessions removes expired sessions from the store
func (s *Service) CleanupExpiredSessions(ctx context.Context) error {
	if err := s.ensureStore(); err != nil {
		return err
	}
	return s.store.CleanupExpiredSessions(ctx)
}

// Ping checks the health of the auth store database connection
func (s *Service) Ping(ctx context.Context) error {
	if err := s.ensureStore(); err != nil {
		return err
	}
	return s.store.Ping(ctx)
}
