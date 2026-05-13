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

// SetupAdmin creates the first admin user using API key authentication.
// The bootstrap is race-safe in two layers: an upfront AdminExists() check
// (common case — fast path, no insert when an admin already exists) and an
// atomic CreateAdminIfNone() conditional insert (closes the TOCTOU window
// where two concurrent bootstrap callers both passed the existence check).
func (s *Service) SetupAdmin(ctx context.Context, req SetupAdminRequest) (*LoginResponse, error) {
	exists, err := s.store.AdminExists(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check admin: %w", err)
	}
	if exists {
		return nil, ErrAdminExists
	}

	if _, err := mail.ParseAddress(req.Email); err != nil {
		return nil, ErrInvalidEmail
	}

	if err := s.validatePassword(req.Password); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPasswordPolicy, err)
	}

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

	inserted, err := s.store.CreateAdminIfNone(ctx, user)
	if err != nil {
		// ErrEmailInUse (email collision with an existing non-admin user)
		// surfaces unwrapped so the handler maps it to 409. Other errors
		// stay wrapped for diagnosis.
		if errors.Is(err, ErrEmailInUse) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to create admin: %w", err)
	}
	if !inserted {
		// Another bootstrap caller raced ahead between AdminExists and the
		// conditional insert. Returns the same sentinel as the fast-path
		// check above so the handler maps both branches to 409.
		return nil, ErrAdminExists
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
//
// When req.Password is empty the user is invited and will set their own
// password via the welcome email link, so the password validator is skipped.
func (s *Service) validateCreateUserRequest(ctx context.Context, req CreateUserRequest) error {
	if _, err := mail.ParseAddress(req.Email); err != nil {
		return ErrInvalidEmail
	}
	existing, err := s.store.GetUserByEmail(ctx, req.Email)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if existing != nil {
		return ErrEmailInUse
	}
	if req.Role != RoleAdmin && req.Role != RoleUser && req.Role != RoleReadOnly {
		// %w lets the API handler detect the category via errors.Is(ErrInvalidRole)
		// while preserving the specific role name in the user-facing message.
		return fmt.Errorf("%w: %s", ErrInvalidRole, req.Role)
	}
	if req.Password == "" {
		return nil
	}
	if err := s.validatePassword(req.Password); err != nil {
		// validatePassword returns specific messages ("must be at least N
		// characters", "common password", etc.); wrap so the handler can
		// detect the category while keeping the message detail.
		return fmt.Errorf("%w: %v", ErrPasswordPolicy, err)
	}
	return nil
}

// CreateUserResult bundles the created user with optional invite-email
// delivery status. InviteEmailSent is nil unless the request triggered
// an invite (req.Password == ""). When non-nil it reflects whether the
// invite email actually reached the configured sender — false means the
// account exists but the recipient has no way to activate it yet and the
// admin should re-mail the setup link via the Forgot Password flow until
// a dedicated Resend Invite endpoint exists. InviteEmailError carries
// the underlying send error in the false case so callers can surface it.
type CreateUserResult struct {
	User             *User
	InviteEmailSent  *bool
	InviteEmailError string
}

// CreateUser creates a new user (admin only).
//
// If req.Password is empty the user is created in the "invited" state:
// inactive, with an unguessable placeholder password hash that no client
// input can match, and a setup token mailed to req.Email. The recipient
// activates the account and chooses their own password by following the
// link, which lands on the existing ConfirmPasswordReset flow.
//
// On an invite request the returned CreateUserResult always carries a
// non-nil InviteEmailSent so callers can distinguish "delivered" from
// "stored, but the user is currently unreachable". An invite-email send
// failure is reported via the result (not as an error) so the user row
// is still surfaced and the admin can react instead of seeing a 5xx.
func (s *Service) CreateUser(ctx context.Context, req CreateUserRequest) (*CreateUserResult, error) {
	if err := s.validateCreateUserRequest(ctx, req); err != nil {
		return nil, err
	}

	invite := req.Password == ""

	passwordSource := req.Password
	if invite {
		// Hash a fresh random token rather than the empty string so the
		// resulting bcrypt hash is unguessable. Login already short-circuits
		// on !user.Active, but this guards against any future code path that
		// reaches the bcrypt compare with this account.
		placeholder, err := generateToken()
		if err != nil {
			return nil, fmt.Errorf("failed to generate placeholder password: %w", err)
		}
		passwordSource = placeholder
	}

	passwordHash, err := s.hashPassword(passwordSource)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

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
		Active:       !invite,
	}

	var inviteToken string
	if invite {
		token, err := generateToken()
		if err != nil {
			return nil, fmt.Errorf("failed to generate invite token: %w", err)
		}
		expiry := now.Add(PasswordSetupExpiry)
		user.PasswordResetToken = hashSessionToken(token)
		user.PasswordResetExpiry = &expiry
		inviteToken = token
	}

	if err := s.store.CreateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	result := &CreateUserResult{User: user}

	if invite {
		setupURL := fmt.Sprintf("%s/reset-password?token=%s", s.dashboardURL, inviteToken)
		err := s.emailSender.SendUserInviteEmail(ctx, user.Email, setupURL)
		sent := err == nil
		result.InviteEmailSent = &sent
		if err != nil {
			// Don't fail the API call: the user row was created
			// successfully and a 5xx here would imply the whole
			// operation rolled back. Surface the failure via the
			// result instead so the caller can show the admin a
			// warning toast and point them at the Forgot Password
			// flow as the recovery path.
			result.InviteEmailError = err.Error()
			logging.Errorf("Failed to send user invite email: %v", err)
		}
		logging.Infof("User invited: id=%s, role=%s, email_sent=%t", user.ID, user.Role, sent)
	} else {
		logging.Infof("User created: id=%s, role=%s", user.ID, user.Role)
	}

	return result, nil
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
