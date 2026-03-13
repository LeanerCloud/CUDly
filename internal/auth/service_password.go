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

// Password validation constants following NIST guidelines
const (
	minPasswordLength       = 12  // Minimum password length
	maxPasswordLength       = 128 // Maximum password length to prevent bcrypt DoS
	passwordHistorySize     = 5   // Number of previous passwords to remember
	sequentialCharThreshold = 3   // Number of identical consecutive characters to reject
)

// commonPasswords is a list of commonly used weak passwords to reject
// Based on NIST guidelines and common password lists
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

// containsSequentialChars checks if password contains n or more identical consecutive characters
// For example, "aaa", "111", "###" would be caught with n=3
func containsSequentialChars(password string, n int) bool {
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

// checkPasswordHistory verifies that the new password hasn't been used recently
// Checks both the current password hash and the password history
func (s *Service) checkPasswordHistory(newPassword string, currentHash string, passwordHistory []string) error {
	// Check against current password first
	if currentHash != "" && s.verifyPassword(newPassword, currentHash) {
		return fmt.Errorf("password has been used recently, please choose a different password")
	}

	// Check against all stored password hashes in history
	for _, oldHash := range passwordHistory {
		if s.verifyPassword(newPassword, oldHash) {
			return fmt.Errorf("password has been used recently, please choose a different password")
		}
	}
	return nil
}

// addToPasswordHistory adds a new password hash to the history and maintains the limit
func addToPasswordHistory(currentHash string, existingHistory []string) []string {
	// Create new history array with current password at the beginning
	newHistory := []string{currentHash}

	// Add existing passwords up to the limit (keeping most recent)
	for i := 0; i < len(existingHistory) && len(newHistory) < passwordHistorySize; i++ {
		newHistory = append(newHistory, existingHistory[i])
	}

	return newHistory
}

// validatePassword validates password requirements following NIST guidelines
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
	if containsSequentialChars(password, sequentialCharThreshold) {
		return fmt.Errorf("password must not contain %d or more identical consecutive characters", sequentialCharThreshold)
	}

	// Check against common passwords
	if err := s.checkCommonPasswords(password); err != nil {
		return err
	}

	return nil
}

// validatePasswordComplexity checks that password meets complexity requirements
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

// checkCommonPasswords verifies password is not in common password list
func (s *Service) checkCommonPasswords(password string) error {
	lowerPass := strings.ToLower(password)
	for _, common := range commonPasswords {
		if lowerPass == common {
			return fmt.Errorf("password is too common, please choose a stronger password")
		}
	}
	return nil
}

// ChangePassword allows a user to change their password
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

// RequestPasswordReset initiates a password reset
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	user, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Don't reveal if email exists
			logging.Debugf("Password reset requested for non-existent email: %s", email)
			return nil
		}
		return err
	}
	if user == nil {
		// Don't reveal if email exists
		logging.Debugf("Password reset requested for non-existent email: %s", email)
		return nil
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

	// Send reset email (use unhashed token in URL)
	resetURL := fmt.Sprintf("%s/reset-password?token=%s", s.dashboardURL, token)
	if err := s.emailSender.SendPasswordResetEmail(ctx, user.Email, resetURL); err != nil {
		logging.Errorf("Failed to send password reset email: %v", err)
		// Don't return error to prevent email enumeration
	}

	return nil
}

// ConfirmPasswordReset completes a password reset
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
