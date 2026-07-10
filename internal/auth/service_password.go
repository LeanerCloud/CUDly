package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is the work factor for bcrypt password hashing.
// OWASP recommends at least 10, we use 12 for stronger protection against brute-force attacks.
// This adds about 4x more computational cost compared to DefaultCost (10).
const bcryptCost = 12

// dummyPasswordHash is a pre-computed bcrypt hash of a random constant string.
// It is used by Login to run a timing-equalizing bcrypt.CompareHashAndPassword
// when the requested email does not exist, so an attacker cannot distinguish a
// missing account from a wrong-password attempt via response time (issue #416).
// The plain-text "dummy" value is intentionally unguessable and never stored.
//
// Generated once at compile time with cost bcryptCost (12).
//
//nolint:gosec // this is a public sentinel hash, not a credential
var dummyPasswordHash = "$2a$12$iAMeexq41AwZ2Dj9oAvGfeVHQxK5ffLPPTNxwPB8bsf7olA730dxO"

// Password validation constants following NIST guidelines.
const (
	minPasswordLength     = 12  // Minimum password length
	maxPasswordLength     = 128 // Maximum password length to prevent bcrypt DoS
	passwordHistorySize   = 5   // Number of previous passwords to remember
	repeatedCharThreshold = 3   // Number of identical consecutive characters to reject

	// PasswordResetRateLimit is the minimum interval between password reset requests
	// for the same email address. A second request within this window is silently
	// dropped (the existing token remains valid) to prevent a griefing vector where
	// an attacker repeatedly requests resets to perpetually invalidate the victim's
	// legitimate link.
	PasswordResetRateLimit = 1 * time.Minute
)

// commonPasswords is a list of commonly used weak passwords to reject
// Based on NIST guidelines and common password lists.
var commonPasswords = []string{
	"password", "123456", "qwerty", "admin", "welcome", "letmein",
	"monkey", "dragon", "master", "login", "abc123", "starwars",
	"trustno1", "password1", "password123", "admin123", "root",
	"qwerty123", "welcome1", "password!", "admin@123", "test1234",
	"user123", "letmein1", "changeme", "default", "superman",
	"iloveyou", "princess", "football", "baseball", "sunshine",
}

// hashPassword hashes a password using bcrypt.
// Bcrypt handles salting internally, so no external salt is needed.
func (s *Service) hashPassword(password string) (string, error) {
	cost := bcryptCost
	if s.bcryptCostOverride > 0 {
		cost = s.bcryptCostOverride
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}

	return string(hash), nil
}

// verifyPassword verifies a password against a bcrypt hash.
func (s *Service) verifyPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// containsRepeatedChars checks if password contains n or more identical consecutive characters.
// For example, "aaa", "111", "###" would be caught with n=3.
// Note: this checks for repeated (identical) chars, not sequential runs (abc, 123) --
// the name "Sequential" in the original was misleading; renamed for accuracy (03-L3).
func containsRepeatedChars(password string, n int) bool {
	if len(password) < n || n < 2 {
		return false
	}

	count := 1
	lastChar := rune(0)

	for _, char := range password {
		if char == lastChar {
			count++
			if count >= n {
				return true
			}
		} else {
			count = 1
			lastChar = char
		}
	}

	return false
}

// checkPasswordHistory verifies that the new password hasn't been used recently.
// Checks the current password hash and the prior-password history separately so
// the caller can render a more useful message for the dominant case (user
// re-typing their existing password on the reset form): see issue #459.
func (s *Service) checkPasswordHistory(newPassword string, currentHash string, passwordHistory []string) error {
	// Check against current password first; distinct message so the user
	// can tell "I typed my current one" from "this matches an old one".
	if currentHash != "" && s.verifyPassword(newPassword, currentHash) {
		return fmt.Errorf("this is your current password, choose a different one")
	}

	// Check against all stored password hashes in history
	for _, oldHash := range passwordHistory {
		if s.verifyPassword(newPassword, oldHash) {
			return fmt.Errorf("password has been used recently, please choose a different password")
		}
	}
	return nil
}

// addToPasswordHistory adds a new password hash to the history and maintains the limit.
func addToPasswordHistory(currentHash string, existingHistory []string) []string {
	// Create new history array with current password at the beginning
	newHistory := []string{currentHash}

	// Add existing passwords up to the limit (keeping most recent)
	for i := 0; i < len(existingHistory) && len(newHistory) < passwordHistorySize; i++ {
		newHistory = append(newHistory, existingHistory[i])
	}

	return newHistory
}

// validatePassword validates password requirements following NIST guidelines.
func (s *Service) validatePassword(password string) error {
	// Check minimum length
	if len(password) < minPasswordLength {
		return fmt.Errorf("password must be at least %d characters", minPasswordLength)
	}

	// Check maximum length to prevent bcrypt DoS attacks
	// bcrypt has a 72-byte limit, but we enforce a lower limit for practical reasons
	if len(password) > maxPasswordLength {
		return fmt.Errorf("password must not exceed %d characters", maxPasswordLength)
	}

	// Check for complexity requirements
	if err := s.validatePasswordComplexity(password); err != nil {
		return err
	}

	// Check for sequential identical characters
	if containsRepeatedChars(password, repeatedCharThreshold) {
		return fmt.Errorf("password must not contain %d or more identical consecutive characters", repeatedCharThreshold)
	}

	// Check against common passwords
	if err := s.checkCommonPasswords(password); err != nil {
		return err
	}

	return nil
}

// validatePasswordComplexity checks that password meets complexity requirements.
func (s *Service) validatePasswordComplexity(password string) error {
	hasUpper, hasLower, hasNumber, hasSpecial := checkCharacterTypes(password)
	return validateCharacterRequirements(hasUpper, hasLower, hasNumber, hasSpecial)
}

func checkCharacterTypes(password string) (hasUpper, hasLower, hasNumber, hasSpecial bool) {
	for _, c := range password {
		switch {
		case unicode.IsUpper(c):
			hasUpper = true
		case unicode.IsLower(c):
			hasLower = true
		case unicode.IsNumber(c):
			hasNumber = true
		case unicode.IsPunct(c) || unicode.IsSymbol(c):
			hasSpecial = true
		}
	}
	return
}

func validateCharacterRequirements(hasUpper, hasLower, hasNumber, hasSpecial bool) error {
	if !hasUpper || !hasLower || !hasNumber || !hasSpecial {
		return fmt.Errorf("password must contain at least one uppercase letter, one lowercase letter, one number, and one special character")
	}
	return nil
}

// checkCommonPasswords verifies password is not in common password list.
func (s *Service) checkCommonPasswords(password string) error {
	lowerPass := strings.ToLower(password)
	for _, common := range commonPasswords {
		if lowerPass == common {
			return fmt.Errorf("password is too common, please choose a stronger password")
		}
	}
	return nil
}

// ChangePassword allows a user to change their password.
func (s *Service) ChangePassword(ctx context.Context, userID string, req ChangePasswordRequest) error {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("user not found")
		}
		return err
	}
	if user == nil {
		return fmt.Errorf("user not found")
	}

	// Verify current password
	if !s.verifyPassword(req.CurrentPassword, user.PasswordHash) {
		return fmt.Errorf("current password is incorrect")
	}

	// Validate new password against requirements
	if err := s.validatePassword(req.NewPassword); err != nil {
		return err
	}

	// Check password history to prevent reuse (includes current password)
	if err := s.checkPasswordHistory(req.NewPassword, user.PasswordHash, user.PasswordHistory); err != nil {
		return err
	}

	// Hash the new password
	passwordHash, err := s.hashPassword(req.NewPassword)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// Update password history (add current password to history)
	user.PasswordHistory = addToPasswordHistory(user.PasswordHash, user.PasswordHistory)

	// Set new password
	user.Salt = "" // Not used anymore
	user.PasswordHash = passwordHash

	// Invalidate all sessions (non-critical, log error but continue)
	if err := s.store.DeleteUserSessions(ctx, userID); err != nil {
		logging.Warnf("Failed to delete sessions for user %s during password change: %v", userID, err)
	}

	if err := s.store.UpdateUser(ctx, user); err != nil {
		return err
	}
	s.notifyPasswordChange(ctx, userID, req.NewPassword)
	return nil
}

// RequestPasswordReset initiates a password reset.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	user, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Don't reveal if email exists
			logging.Debugf("Password reset requested for non-existent email: %s", redactEmail(email))
			return nil
		}
		return err
	}
	if user == nil {
		// Don't reveal if email exists
		logging.Debugf("Password reset requested for non-existent email: %s", redactEmail(email))
		return nil
	}

	// Rate-limit: if a reset token was issued recently (within PasswordResetRateLimit),
	// silently return so the existing token stays valid. This prevents a griefing
	// attack where an adversary who knows the victim's email repeatedly requests
	// resets to perpetually invalidate the victim's legitimate link.
	if user.PasswordResetExpiry != nil {
		tokenAge := PasswordResetExpiry - time.Until(*user.PasswordResetExpiry)
		if tokenAge < PasswordResetRateLimit {
			logging.Debugf("Password reset rate-limited for %s (token age %s < %s)",
				redactEmail(email), tokenAge.Round(time.Second), PasswordResetRateLimit)
			return nil
		}
	}

	// Generate reset token
	token, err := generateToken()
	if err != nil {
		return fmt.Errorf("failed to generate reset token: %w", err)
	}

	// Hash the token before storing (security best practice)
	tokenHash := hashSessionToken(token)

	// Set expiry based on configured duration
	expiry := time.Now().Add(PasswordResetExpiry)
	user.PasswordResetToken = tokenHash
	user.PasswordResetExpiry = &expiry

	if err := s.store.UpdateUser(ctx, user); err != nil {
		return fmt.Errorf("failed to save reset token: %w", err)
	}

	// Skip the email entirely if dashboardURL is unconfigured — a broken
	// relative link in an inbox is worse than no email; the operator's
	// startup-time WARN already names the missing env var. Don't return an
	// error to the caller to preserve the email-enumeration protection
	// (RequestPasswordReset must look identical whether or not the email
	// exists, and that includes whether or not a send happened). Issue #355.
	if s.dashboardURL == "" {
		logging.Errorf("RequestPasswordReset: skipping send — DashboardURL empty would produce a broken relative link (set DASHBOARD_URL).")
		return nil
	}

	// Send reset email (use unhashed token in URL)
	resetURL := fmt.Sprintf("%s/reset-password?token=%s", s.dashboardURL, token)
	if err := s.emailSender.SendPasswordResetEmail(ctx, user.Email, resetURL); err != nil {
		logging.Errorf("Failed to send password reset email: %v", err)
		// Don't return error to prevent email enumeration
	}

	return nil
}

// ConfirmPasswordReset completes a password reset.
func (s *Service) ConfirmPasswordReset(ctx context.Context, req PasswordResetConfirm) error {
	user, err := s.validateResetToken(ctx, req.Token)
	if err != nil {
		return err
	}

	// Invalidate the reset token before processing to ensure one-time use
	user.PasswordResetToken = ""
	user.PasswordResetExpiry = nil

	if err := s.processPasswordReset(user, req.NewPassword); err != nil {
		// Token is consumed even on validation failure (one-time use)
		if updateErr := s.store.UpdateUser(ctx, user); updateErr != nil {
			logging.Warnf("Failed to invalidate reset token after password validation failure: %v", updateErr)
		}
		return err
	}

	// Activate user on first password set (admin bootstrap flow)
	if !user.Active {
		user.Active = true
	}

	if err := s.store.DeleteUserSessions(ctx, user.ID); err != nil {
		logging.Warnf("Failed to delete sessions for user %s during password reset: %v", user.ID, err)
	}

	if err := s.store.UpdateUser(ctx, user); err != nil {
		return err
	}
	s.notifyPasswordChange(ctx, user.ID, req.NewPassword)
	return nil
}

// ResetTokenState describes the runtime state of a password-reset token.
// One of "valid", "expired", "used". "used" doubles as the fallback for
// tokens that never existed: the row is wiped on consumption (one-time
// use), so the store cannot reliably distinguish "consumed" from
// "never issued". Surfacing both as "used" matches the dominant
// real-world case (stale link from an old email) and lets the frontend
// branch on a single state.
type ResetTokenState string

const (
	// ResetTokenStateValid means the token matches an issued, unexpired row.
	ResetTokenStateValid ResetTokenState = "valid"
	// ResetTokenStateExpired means the token matches but its expiry has passed.
	ResetTokenStateExpired ResetTokenState = "expired"
	// ResetTokenStateUsed covers both consumed and never-issued tokens.
	ResetTokenStateUsed ResetTokenState = "used"
)

// ResetTokenFlow describes whether the matched token belongs to an
// invite flow (user had Active = false at issue time, still false now)
// or a normal password-reset flow. The frontend uses this to swap
// "Set your password" vs "Reset your password" wording (issue #461).
type ResetTokenFlow string

const (
	// ResetTokenFlowReset is the default flow for active users.
	ResetTokenFlowReset ResetTokenFlow = "reset"
	// ResetTokenFlowInvite is the bootstrap flow for not-yet-active users.
	ResetTokenFlowInvite ResetTokenFlow = "invite"
)

// ResetTokenStatus returns the state of a reset token without consuming
// it. The frontend calls this before rendering the reset-password form
// so it can show an "expired" or "already used" view instead of a form
// the user can never submit (issues #460, #461). For "used" / never-
// issued, flow defaults to "reset" since there is no user to inspect.
func (s *Service) ResetTokenStatus(ctx context.Context, token string) (ResetTokenState, ResetTokenFlow, error) {
	if token == "" {
		return ResetTokenStateUsed, ResetTokenFlowReset, nil
	}

	tokenHash := hashSessionToken(token)
	user, err := s.store.GetUserByResetToken(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ResetTokenStateUsed, ResetTokenFlowReset, nil
		}
		return "", "", err
	}
	if user == nil {
		return ResetTokenStateUsed, ResetTokenFlowReset, nil
	}

	if user.PasswordResetExpiry == nil || time.Now().After(*user.PasswordResetExpiry) {
		flow := ResetTokenFlowReset
		if !user.Active {
			flow = ResetTokenFlowInvite
		}
		return ResetTokenStateExpired, flow, nil
	}

	flow := ResetTokenFlowReset
	if !user.Active {
		flow = ResetTokenFlowInvite
	}
	return ResetTokenStateValid, flow, nil
}

func (s *Service) validateResetToken(ctx context.Context, token string) (*User, error) {
	tokenHash := hashSessionToken(token)

	user, err := s.store.GetUserByResetToken(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("invalid or expired reset token")
		}
		return nil, err
	}
	if user == nil {
		return nil, fmt.Errorf("invalid or expired reset token")
	}

	if user.PasswordResetExpiry == nil || time.Now().After(*user.PasswordResetExpiry) {
		return nil, fmt.Errorf("reset token has expired")
	}

	return user, nil
}

func (s *Service) processPasswordReset(user *User, newPassword string) error {
	if err := s.validatePassword(newPassword); err != nil {
		return err
	}

	if err := s.checkPasswordHistory(newPassword, user.PasswordHash, user.PasswordHistory); err != nil {
		return err
	}

	passwordHash, err := s.hashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	if user.PasswordHash != "" {
		user.PasswordHistory = addToPasswordHistory(user.PasswordHash, user.PasswordHistory)
	}

	user.Salt = ""
	user.PasswordHash = passwordHash
	return nil
}

// redactEmail returns a redacted version of an email address safe for debug logs.
// Both the local part and the domain are partially masked to reduce PII exposure
// for low-entropy addresses (03-L1, see also feedback_pii_in_logs).
// Example: "user@example.com" -> "us***@ex***.com".
func redactEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return "***"
	}
	local := email[:at]
	domain := email[at+1:] // without '@'
	if len(local) <= 2 {
		local = "***"
	} else {
		local = local[:2] + "***"
	}

	// Mask the domain: keep the TLD but redact the hostname.
	// e.g. "example.com" -> "ex***.com"
	dot := strings.LastIndex(domain, ".")
	if dot < 0 || dot == 0 {
		domain = "***"
	} else {
		tld := domain[dot:]  // ".com"
		host := domain[:dot] // "example"
		if len(host) <= 2 {
			domain = "***" + tld
		} else {
			domain = host[:2] + "***" + tld
		}
	}
	return local + "@" + domain
}
