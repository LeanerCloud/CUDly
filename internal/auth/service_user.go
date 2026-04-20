package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SetupAdmin creates the first admin user using API key authentication
func (s *Service) SetupAdmin(ctx context.Context, req SetupAdminRequest) (*LoginResponse, error) {
	// Check if admin already exists
	exists, err := s.store.AdminExists(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check admin: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("admin user already exists")
	}

	// Validate email
	if _, err := mail.ParseAddress(req.Email); err != nil {
		return nil, fmt.Errorf("invalid email format")
	}

	// Validate password
	if err := s.validatePassword(req.Password); err != nil {
		return nil, err
	}

	// Hash password directly with bcrypt (no custom salt needed)
	passwordHash, err := s.hashPassword(req.Password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	// Create admin user, auto-assigned to the Administrators group seeded
	// by migration 000024 so the Administrators group card shows members
	// immediately on a fresh install.
	now := time.Now()
	user := &User{
		ID:           uuid.New().String(),
		Email:        req.Email,
		PasswordHash: passwordHash,
		Salt:         "", // Not used anymore, but kept for backward compatibility
		Role:         RoleAdmin,
		GroupIDs:     []string{DefaultAdminGroupID},
		CreatedAt:    now,
		UpdatedAt:    now,
		Active:       true,
	}

	if err := s.store.CreateUser(ctx, user); err != nil {
		// Two callers reaching CreateUser concurrently after both passed the
		// AdminExists() check is caught by the users_one_admin partial unique
		// index (migration 000025). Map that duplicate-key error to the same
		// "admin already exists" semantic the existence check returned above.
		if isDuplicateKeyError(err) {
			return nil, fmt.Errorf("admin user already exists")
		}
		return nil, fmt.Errorf("failed to create admin: %w", err)
	}

	// Create session
	session, err := s.createSession(ctx, user, "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	logging.Infof("Admin user created: id=%s", user.ID)

	return &LoginResponse{
		Token:     session.Token,
		ExpiresAt: session.ExpiresAt,
		User: &UserInfo{
			ID:    user.ID,
			Email: user.Email,
			Role:  user.Role,
		},
		CSRFToken: session.CSRFToken,
	}, nil
}

// CheckAdminExists returns whether an admin user exists
func (s *Service) CheckAdminExists(ctx context.Context) (bool, error) {
	return s.store.AdminExists(ctx)
}

// validateCreateUserRequest validates the fields of a CreateUserRequest before creating
// the user record. Returns an error if any field is invalid.
func (s *Service) validateCreateUserRequest(ctx context.Context, req CreateUserRequest) error {
	if _, err := mail.ParseAddress(req.Email); err != nil {
		return fmt.Errorf("invalid email format")
	}
	existing, err := s.store.GetUserByEmail(ctx, req.Email)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if existing != nil {
		return fmt.Errorf("email already in use")
	}
	if req.Role != RoleAdmin && req.Role != RoleUser && req.Role != RoleReadOnly {
		return fmt.Errorf("invalid role: %s", req.Role)
	}
	return s.validatePassword(req.Password)
}

// CreateUser creates a new user (admin only)
func (s *Service) CreateUser(ctx context.Context, req CreateUserRequest) (*User, error) {
	if err := s.validateCreateUserRequest(ctx, req); err != nil {
		return nil, err
	}

	// Hash password directly with bcrypt (no custom salt needed)
	passwordHash, err := s.hashPassword(req.Password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	// Create user
	now := time.Now()
	user := &User{
		ID:           uuid.New().String(),
		Email:        req.Email,
		PasswordHash: passwordHash,
		Salt:         "", // Not used anymore, but kept for backward compatibility
		Role:         req.Role,
		GroupIDs:     req.GroupIDs,
		CreatedAt:    now,
		UpdatedAt:    now,
		Active:       true,
	}

	if err := s.store.CreateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	logging.Infof("User created: id=%s, role=%s", user.ID, user.Role)

	return user, nil
}

// UpdateUser updates user details (admin only)
func (s *Service) UpdateUser(ctx context.Context, userID string, req UpdateUserRequest) (*User, error) {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user not found")
		}
		return nil, err
	}
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}

	if err := applyUpdateUserRequest(user, req); err != nil {
		return nil, err
	}

	if err := s.store.UpdateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	return user, nil
}

// applyUpdateUserRequest applies the non-nil fields of req to user, validating as needed.
func applyUpdateUserRequest(user *User, req UpdateUserRequest) error {
	if req.Role != nil {
		if *req.Role != RoleAdmin && *req.Role != RoleUser && *req.Role != RoleReadOnly {
			return fmt.Errorf("invalid role: %s", *req.Role)
		}
		user.Role = *req.Role
	}
	if req.GroupIDs != nil {
		user.GroupIDs = req.GroupIDs
	}
	if req.Active != nil {
		user.Active = *req.Active
	}
	return nil
}

// DeleteUser removes a user (admin only)
func (s *Service) DeleteUser(ctx context.Context, userID string) error {
	// Delete all user sessions
	if err := s.store.DeleteUserSessions(ctx, userID); err != nil {
		logging.Warnf("Failed to delete user sessions: %v", err)
	}

	return s.store.DeleteUser(ctx, userID)
}

// GetUser returns user info. Returns (nil, pgx.ErrNoRows) if the user does not exist.
func (s *Service) GetUser(ctx context.Context, userID string) (*User, error) {
	return s.store.GetUserByID(ctx, userID)
}

// UpdateUserProfile allows a user to update their own email and password
func (s *Service) UpdateUserProfile(ctx context.Context, userID string, email string, currentPassword string, newPassword string) error {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("user not found")
		}
		return fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return fmt.Errorf("user not found")
	}

	if !s.verifyPassword(currentPassword, user.PasswordHash) {
		return fmt.Errorf("current password is incorrect")
	}

	if err := s.updateUserEmail(ctx, user, email); err != nil {
		return err
	}

	passwordChanged, err := s.updateUserPassword(user, newPassword)
	if err != nil {
		return err
	}

	user.UpdatedAt = time.Now()
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	// Invalidate sessions when password changes
	if passwordChanged {
		if err := s.store.DeleteUserSessions(ctx, userID); err != nil {
			logging.Warnf("Failed to delete sessions for user %s during profile update: %v", userID, err)
		}
		s.notifyPasswordChange(ctx, userID, newPassword)
	}

	logging.Infof("User profile updated: id=%s", user.ID)
	return nil
}

func (s *Service) updateUserEmail(ctx context.Context, user *User, email string) error {
	if email != "" && email != user.Email {
		if _, err := mail.ParseAddress(email); err != nil {
			return fmt.Errorf("invalid email format")
		}
		// Check email uniqueness
		existing, err := s.store.GetUserByEmail(ctx, email)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if existing != nil {
			return fmt.Errorf("email already in use")
		}
		user.Email = email
	}
	return nil
}

func (s *Service) updateUserPassword(user *User, newPassword string) (bool, error) {
	if newPassword == "" {
		return false, nil
	}

	if err := s.validatePassword(newPassword); err != nil {
		return false, err
	}

	// Check password history to prevent reuse
	if err := s.checkPasswordHistory(newPassword, user.PasswordHash, user.PasswordHistory); err != nil {
		return false, err
	}

	hash, err := s.hashPassword(newPassword)
	if err != nil {
		return false, fmt.Errorf("failed to hash password: %w", err)
	}

	// Update password history
	if user.PasswordHash != "" {
		user.PasswordHistory = addToPasswordHistory(user.PasswordHash, user.PasswordHistory)
	}

	user.Salt = ""
	user.PasswordHash = hash
	return true, nil
}

// ListUsers returns all users (admin only)
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	return s.store.ListUsers(ctx)
}

// recordFailedLogin increments failed login attempts and locks the account if necessary
func (s *Service) recordFailedLogin(ctx context.Context, user *User) {
	user.FailedLoginAttempts++
	now := time.Now()
	user.UpdatedAt = now

	if user.FailedLoginAttempts >= MaxFailedLoginAttempts {
		lockUntil := now.Add(AccountLockoutDuration)
		user.LockedUntil = &lockUntil
		logging.Warnf("Account locked due to %d failed login attempts: id=%s (locked until %v)",
			user.FailedLoginAttempts, user.ID, lockUntil)
	}

	if err := s.store.UpdateUser(ctx, user); err != nil {
		logging.Errorf("Failed to record failed login attempt for user %s: %v", user.ID, err)
	}
}
