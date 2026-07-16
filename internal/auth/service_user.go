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

	if _, parseErr := mail.ParseAddress(req.Email); parseErr != nil {
		return nil, ErrInvalidEmail
	}

	err = s.validatePassword(req.Password)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPasswordPolicy, err)
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
			ID:     user.ID,
			Email:  user.Email,
			Groups: user.GroupIDs,
		},
		CSRFToken: session.CSRFToken,
	}, nil
}

// mapStoreCreateUserError maps a Store.CreateUser error into the auth
// package's sentinel set so the API handler can surface 4xx instead of
// 500 for known recoverable failures. Defense-in-depth: the validator
// pre-checks email-in-use, but two callers can race past it and hit the
// users_email_key unique constraint. Extracted from CreateUser /
// SetupAdmin call sites to keep both functions under gocyclo's
// complexity threshold. Issue #349.
func mapStoreCreateUserError(err error) error {
	if err == nil {
		return nil
	}
	if isEmailDuplicateError(err) {
		return ErrEmailInUse
	}
	return fmt.Errorf("failed to create user: %w", err)
}

// CheckAdminExists returns whether an admin user exists.
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
	// Authorization derives entirely from group membership, so a user with no
	// groups can do nothing. Reject zero-group creation as a 400 (issue #907).
	if len(req.GroupIDs) == 0 {
		return ErrNoGroups
	}
	if req.Password == "" {
		return nil
	}
	if err := s.validatePassword(req.Password); err != nil {
		// validatePassword returns specific messages ("must be at least N
		// characters", "common password", etc.); wrap so the handler can
		// detect the category while keeping the message detail.
		return fmt.Errorf("%w: %w", ErrPasswordPolicy, err)
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

	if err := mapStoreCreateUserError(s.store.CreateUser(ctx, user)); err != nil {
		return nil, err
	}

	result := &CreateUserResult{User: user}

	if invite {
		s.sendInviteEmail(ctx, user, inviteToken, result)
	} else {
		logging.Infof("User created: id=%s, groups=%d", user.ID, len(user.GroupIDs))
	}

	return result, nil
}

// sendInviteEmail constructs the setup URL and dispatches the invite,
// recording the outcome on result. Extracted from CreateUser so the
// caller stays under gocyclo's complexity threshold.
//
// When dashboardURL is empty the send is skipped and InviteEmailError
// carries a clear "fix DASHBOARD_URL" hint back to the admin — the
// user row is already persisted, the operator can re-invite once the
// env var is set rather than silently mailing out a broken relative
// link. Issue #355.
func (s *Service) sendInviteEmail(ctx context.Context, user *User, inviteToken string, result *CreateUserResult) {
	if s.dashboardURL == "" {
		notSent := false
		result.InviteEmailSent = &notSent
		result.InviteEmailError = "DashboardURL is not configured on the server; the invite link cannot be generated. Ask the operator to set DASHBOARD_URL before re-inviting."
		logging.Errorf("CreateUser invite: skipping send for user_id=%s — DashboardURL empty", user.ID)
		return
	}
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
	logging.Infof("User invited: id=%s, groups=%d, email_sent=%t", user.ID, len(user.GroupIDs), sent)
}

// loadUser fetches a user by ID, normalising a missing row (either pgx.ErrNoRows
// or a nil user) into a uniform "user not found" error. Extracted so the mutating
// service methods share one lookup-or-not-found path and stay under gocyclo's
// complexity threshold.
func (s *Service) loadUser(ctx context.Context, userID string) (*User, error) {
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
	return user, nil
}

// UpdateUser updates user details (requires manage-users permission).
//
// actorUserID is the authenticated user performing the change (from the
// session, never client-supplied). It is used to enforce the self-escalation
// guard: a user may not add a group they are not already a member of unless
// they hold the manage-users permission. Pass "" for trusted internal callers
// (e.g. the stateless admin API key) that have already been authorized.
func (s *Service) UpdateUser(ctx context.Context, actorUserID, userID string, req UpdateUserRequest) (*User, error) {
	user, err := s.loadUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Snapshot the prior membership/active state before mutating so the guards
	// below can reason about what is being added/removed/deactivated.
	priorGroups := append([]string(nil), user.GroupIDs...)
	priorActive := user.Active

	applyUpdateUserRequest(user, req)

	if req.GroupIDs != nil {
		if err := s.guardGroupChange(ctx, actorUserID, userID, priorGroups, req.GroupIDs); err != nil {
			return nil, err
		}
	}

	if err := s.guardDeactivation(ctx, user, priorActive, req.Active); err != nil {
		return nil, err
	}

	// Email is mutated through updateUserEmail rather than applyUpdateUserRequest
	// because it requires a DB lookup (uniqueness check) and format validation
	// (same rules as the self-edit profile path; see updateUserEmail and #868
	// for the TLD constraint). Issue #892.
	if req.Email != nil {
		if err := s.updateUserEmail(ctx, user, *req.Email); err != nil {
			return nil, err
		}
	}

	if err := s.store.UpdateUser(ctx, user); err != nil {
		// The deferred DB trigger (migration 000065) fires at commit time and
		// can reject writes that the application-level soft check missed due to
		// concurrent requests. Surface the trigger violation as ErrLastAdmin so
		// callers receive the same sentinel regardless of which guard caught it.
		if isLastAdminConstraintViolation(err) {
			return nil, ErrLastAdmin
		}
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	return user, nil
}

// guardDeactivation rejects deactivating the last active Administrators-group
// member, which would lock the deployment out of admin functionality, the same
// hazard as removing the group. AdminExists and the 000065 trigger both count
// only active members, so this soft check mirrors that invariant and rejects
// early with a friendly 409. The deferred trigger is the race-free backstop
// when concurrent requests slip past this read-then-write check. Extracted from
// UpdateUser to keep that function under gocyclo's complexity threshold.
//
// user carries the post-applyUpdateUserRequest group membership; priorActive is
// the active state before the update and reqActive the requested change (nil
// when the request does not touch Active).
func (s *Service) guardDeactivation(ctx context.Context, user *User, priorActive bool, reqActive *bool) error {
	deactivating := priorActive && reqActive != nil && !*reqActive
	if deactivating && containsGroup(user.GroupIDs, DefaultAdminGroupID) {
		return s.checkLastAdminConstraint(ctx)
	}
	return nil
}

// guardGroupChange enforces the issue #907 invariants for a group-membership
// change: at least one group remains, the last Administrators-group member is
// not removed, and a non-privileged actor cannot escalate their own access.
func (s *Service) guardGroupChange(ctx context.Context, actorUserID, targetUserID string, prior, next []string) error {
	if len(next) == 0 {
		return ErrNoGroups
	}

	// Last-admin protection: if this change removes the Administrators group
	// from a user who currently has it, ensure at least one other member
	// keeps it.
	if containsGroup(prior, DefaultAdminGroupID) && !containsGroup(next, DefaultAdminGroupID) {
		if err := s.checkLastAdminConstraint(ctx); err != nil {
			return err
		}
	}

	// Self-escalation guard: when the actor is editing their own membership,
	// any group being ADDED that they did not already have requires the actor
	// to hold the manage-users permission. A non-privileged user therefore
	// cannot grant themselves a more powerful group. Internal callers
	// (actorUserID == "") are already trusted and skip this check.
	if actorUserID != "" && actorUserID == targetUserID && addsNewGroup(prior, next) {
		canManage, err := s.HasPermission(ctx, actorUserID, ActionUpdate, ResourceUsers, nil)
		if err != nil {
			return fmt.Errorf("failed to verify manage-users permission: %w", err)
		}
		if !canManage {
			return ErrSelfEscalation
		}
	}
	return nil
}

// checkLastAdminConstraint returns ErrLastAdmin if removing or deactivating
// the Administrators group's current sole holder would leave the group with
// no other member to fall back on. Pulled out of guardGroupChange to keep that
// function under the cyclomatic limit and reused by the deactivation guard.
// This is a best-effort early reject; the 000065 deferred trigger is the
// race-free backstop that also accounts for already-inactive members.
func (s *Service) checkLastAdminConstraint(ctx context.Context) error {
	count, err := s.store.CountGroupMembers(ctx, DefaultAdminGroupID)
	if err != nil {
		return fmt.Errorf("failed to count administrators: %w", err)
	}
	if count <= 1 {
		return ErrLastAdmin
	}
	return nil
}

func containsGroup(groups []string, target string) bool {
	for _, g := range groups {
		if g == target {
			return true
		}
	}
	return false
}

// addsNewGroup reports whether next contains any group not present in prior.
func addsNewGroup(prior, next []string) bool {
	for _, g := range next {
		if !containsGroup(prior, g) {
			return true
		}
	}
	return false
}

// applyUpdateUserRequest applies the non-nil fields of req to user. GroupID
// membership changes are validated separately by the caller via
// guardGroupChange; Active is a bool with no per-field validation.
func applyUpdateUserRequest(user *User, req UpdateUserRequest) {
	if req.GroupIDs != nil {
		user.GroupIDs = req.GroupIDs
	}
	if req.Active != nil {
		user.Active = *req.Active
	}
}

// DeleteUser removes a user (requires manage-users permission). Refuses to
// delete the last remaining Administrators-group member so the deployment can
// never be locked out of admin functionality (issue #907).
func (s *Service) DeleteUser(ctx context.Context, userID string) error {
	user, err := s.loadUser(ctx, userID)
	if err != nil {
		return err
	}

	if containsGroup(user.GroupIDs, DefaultAdminGroupID) {
		count, err := s.store.CountGroupMembers(ctx, DefaultAdminGroupID)
		if err != nil {
			return fmt.Errorf("failed to count administrators: %w", err)
		}
		if count <= 1 {
			return ErrLastAdmin
		}
	}

	// Delete all user sessions
	if err := s.store.DeleteUserSessions(ctx, userID); err != nil {
		logging.Warnf("Failed to delete user sessions: %v", err)
	}

	if err := s.store.DeleteUser(ctx, userID); err != nil {
		// The deferred DB trigger (migration 000065) fires at commit time and
		// can reject deletes that the application-level soft check missed due
		// to concurrent requests. Surface as ErrLastAdmin so the handler maps
		// it to the same 409 regardless of which guard caught it.
		if isLastAdminConstraintViolation(err) {
			return ErrLastAdmin
		}
		return err
	}
	return nil
}

// GetUser returns user info. Returns (nil, pgx.ErrNoRows) if the user does not exist.
func (s *Service) GetUser(ctx context.Context, userID string) (*User, error) {
	return s.store.GetUserByID(ctx, userID)
}

// UpdateUserProfile allows a user to update their own email and password.
func (s *Service) UpdateUserProfile(ctx context.Context, userID, email, currentPassword, newPassword string) error {
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
		return ErrCurrentPasswordIncorrect
	}

	err = s.updateUserEmail(ctx, user, email)
	if err != nil {
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
			return ErrEmailInUse
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

// ListUsers returns all users (admin only).
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	return s.store.ListUsers(ctx)
}

// recordFailedLogin increments failed login attempts and locks the account if necessary.
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
