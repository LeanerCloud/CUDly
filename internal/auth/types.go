// Package auth provides user authentication and authorization.
package auth

import (
	"time"
)

// User represents a user account.
type User struct {
	ID                  string     `json:"id" dynamodbav:"PK"`
	Email               string     `json:"email" dynamodbav:"Email"`
	PasswordHash        string     `json:"-" dynamodbav:"PasswordHash"`
	Salt                string     `json:"-" dynamodbav:"Salt"`
	GroupIDs            []string   `json:"group_ids,omitempty" dynamodbav:"GroupIDs"`
	CreatedAt           time.Time  `json:"created_at" dynamodbav:"CreatedAt"`
	UpdatedAt           time.Time  `json:"updated_at" dynamodbav:"UpdatedAt"`
	LastLoginAt         *time.Time `json:"last_login_at,omitempty" dynamodbav:"LastLoginAt"`
	PasswordResetToken  string     `json:"-" dynamodbav:"PasswordResetToken,omitempty"`
	PasswordResetExpiry *time.Time `json:"-" dynamodbav:"PasswordResetExpiry,omitempty"`
	Active              bool       `json:"active" dynamodbav:"Active"`
	MFAEnabled          bool       `json:"mfa_enabled" dynamodbav:"MFAEnabled"`
	MFASecret           string     `json:"-" dynamodbav:"MFASecret,omitempty"`
	// MFA enrollment carrier fields (issue #497). Populated by
	// MFASetup and consumed by MFAEnable; both cleared on successful
	// enable / disable. Persisting the pending secret here (instead
	// of in a signed token returned to the client) keeps the wire
	// shape simple and avoids introducing a new HMAC signing key.
	// An abandoned enrollment expires harmlessly because the active
	// MFASecret + MFAEnabled fields stay untouched until enable
	// succeeds.
	MFAPendingSecret          string     `json:"-" dynamodbav:"MFAPendingSecret,omitempty"`
	MFAPendingSecretExpiresAt *time.Time `json:"-" dynamodbav:"MFAPendingSecretExpiresAt,omitempty"`
	// MFARecoveryCodes holds bcrypt hashes of single-use recovery
	// codes generated at enable / regenerate time. The matching hash
	// is removed from the slice when consumed during login or disable.
	MFARecoveryCodes []string `json:"-" dynamodbav:"MFARecoveryCodes,omitempty"`
	// Account lockout fields for brute-force protection
	FailedLoginAttempts int        `json:"-" dynamodbav:"FailedLoginAttempts,omitempty"`
	LockedUntil         *time.Time `json:"-" dynamodbav:"LockedUntil,omitempty"`
	// Password history for preventing reuse (stores up to 5 previous password hashes)
	PasswordHistory []string `json:"-" dynamodbav:"PasswordHistory,omitempty"`
}

// Group represents a permission group.
type Group struct {
	ID              string       `json:"id" dynamodbav:"PK"`
	Name            string       `json:"name" dynamodbav:"Name"`
	Description     string       `json:"description,omitempty" dynamodbav:"Description"`
	Permissions     []Permission `json:"permissions" dynamodbav:"Permissions"`
	AllowedAccounts []string     `json:"allowed_accounts,omitempty" dynamodbav:"AllowedAccounts"`
	// SystemManaged marks groups that are seeded by migrations and
	// should not be renamed or deleted via the API. Only membership
	// can change for system-managed groups.
	SystemManaged bool      `json:"system_managed,omitempty" dynamodbav:"SystemManaged"`
	CreatedAt     time.Time `json:"created_at" dynamodbav:"CreatedAt"`
	UpdatedAt     time.Time `json:"updated_at" dynamodbav:"UpdatedAt"`
	CreatedBy     string    `json:"created_by" dynamodbav:"CreatedBy"`
}

// Permission defines what actions a group can perform.
type Permission struct {
	// Action: view, purchase, configure, admin
	Action string `json:"action" dynamodbav:"Action"`

	// Resource type: recommendations, plans, history, config, users
	Resource string `json:"resource" dynamodbav:"Resource"`

	// Constraints limit the permission to specific contexts
	Constraints *PermissionConstraints `json:"constraints,omitempty" dynamodbav:"Constraints"`
}

// PermissionConstraints limit permissions to specific accounts, providers, or services.
type PermissionConstraints struct {
	// AccountIDs limits to specific AWS/Azure/GCP accounts
	AccountIDs []string `json:"account_ids,omitempty" dynamodbav:"AccountIDs"`

	// Providers limits to specific cloud providers (aws, azure, gcp)
	Providers []string `json:"providers,omitempty" dynamodbav:"Providers"`

	// Services limits to specific services (ec2, rds, elasticache, etc.)
	Services []string `json:"services,omitempty" dynamodbav:"Services"`

	// Regions limits to specific regions
	Regions []string `json:"regions,omitempty" dynamodbav:"Regions"`

	// MaxPurchaseAmount limits the maximum purchase amount
	MaxPurchaseAmount float64 `json:"max_purchase_amount,omitempty" dynamodbav:"MaxPurchaseAmount"`
}

// UserAPIKey represents a personal API key for a user with scoped permissions.
type UserAPIKey struct {
	ID          string       `json:"id" dynamodbav:"PK"`                             // UUID string
	UserID      string       `json:"user_id" dynamodbav:"UserID"`                    // User who owns this key
	Name        string       `json:"name" dynamodbav:"Name"`                         // Human-readable name
	KeyPrefix   string       `json:"key_prefix" dynamodbav:"KeyPrefix"`              // First 8 chars for display
	KeyHash     string       `json:"-" dynamodbav:"KeyHash"`                         // SHA-256 hash of the full key
	Permissions []Permission `json:"permissions,omitempty" dynamodbav:"Permissions"` // Scoped permissions
	ExpiresAt   *time.Time   `json:"expires_at,omitempty" dynamodbav:"ExpiresAt"`
	CreatedAt   time.Time    `json:"created_at" dynamodbav:"CreatedAt"`
	LastUsedAt  *time.Time   `json:"last_used_at,omitempty" dynamodbav:"LastUsedAt"`
	IsActive    bool         `json:"is_active" dynamodbav:"IsActive"`
}

// AuthContext represents the complete authorization context for a user
// It combines group memberships and the permissions computed from them.
type AuthContext struct {
	User            *User
	Groups          []*Group
	AllowedAccounts []string     // Computed from all groups (union)
	Permissions     []Permission // Computed from group memberships
}

// adminCarvedOuts is the set of (action, resource) pairs that the admin:*
// wildcard does NOT cover. Each pair requires explicit membership in a group
// that holds the matching permission (e.g. the Purchaser group). This
// implements separation-of-duties for money-spending operations (issue #923):
// a compromised admin account alone cannot drain commitments.
var adminCarvedOuts = map[[2]string]bool{
	{ActionExecute, ResourcePurchases}:    true,
	{ActionApproveAny, ResourcePurchases}: true,
	{ActionRetryAny, ResourcePurchases}:   true,
}

// HasPermission checks if the auth context has a specific permission.
// Authorization is derived purely from group-granted permissions: a user
// who is a member of the Administrators group holds {ActionAdmin, ResourceAll}
// and therefore passes any check; a user with no groups holds no permissions
// and is denied everything (fail closed).
//
// The admin:* wildcard is intentionally narrow for the three carved-out
// money-spending verbs (execute:purchases, approve-any:purchases,
// retry-any:purchases). Those require explicit membership in a group that
// grants them directly (e.g. the Purchaser group seeded by migration 000054).
func (ctx *AuthContext) HasPermission(action, resource string) bool {
	for _, perm := range ctx.Permissions {
		// Admin permission grants all access EXCEPT the carved-out
		// money-spending verbs (separation of duties, issue #923).
		if perm.Action == ActionAdmin && perm.Resource == ResourceAll {
			if adminCarvedOuts[[2]string{action, resource}] {
				// Fall through to explicit-permission check below.
				continue
			}
			return true
		}

		// Check action and resource match
		if perm.Action != action {
			continue
		}
		if perm.Resource != resource && perm.Resource != ResourceAll {
			continue
		}

		return true
	}

	return false
}

// IsUnrestrictedAccess returns true if the allowed list grants access to all
// accounts — either because it's empty (backward-compat default) or contains
// a "*" wildcard entry. Handlers can use this to short-circuit their filter
// loops without iterating accounts when access is unrestricted.
//
// WARNING — fail-open default (03-L5): an empty allowed list means "all
// accounts", not "no accounts". This is a deliberate backward-compatibility
// default so existing groups without an AllowedAccounts configuration grant
// full access. New callers that intend to express "no access" must represent
// that with an explicit sentinel (e.g. a list containing only a non-existent
// account ID) and must NOT rely on an empty list for the "deny all" case.
func IsUnrestrictedAccess(allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == "*" {
			return true
		}
	}
	return false
}

// MatchesAccount returns true if the allowed list matches an account by its
// internal ID or display name. Exact string match against either field. The
// name is optional — pass "" when unavailable; the match then falls back to
// ID-only. Empty allowed list or a "*" entry matches any account.
func MatchesAccount(allowed []string, accountID, accountName string) bool {
	if IsUnrestrictedAccess(allowed) {
		return true
	}
	for _, a := range allowed {
		if a == accountID {
			return true
		}
		if accountName != "" && a == accountName {
			return true
		}
	}
	return false
}

// CanAccessAccount checks if the user can access a specific account by its
// ID or display name. Access is derived from the union of the user's groups'
// AllowedAccounts via MatchesAccount. Administrators-group members carry the
// "*" wildcard (seeded with allowed_accounts=['*']) and so match any account;
// a user with no groups has an empty AllowedAccounts and, combined with the
// permission check at the call site, is denied (fail closed).
func (ctx *AuthContext) CanAccessAccount(accountID, accountName string) bool {
	return MatchesAccount(ctx.AllowedAccounts, accountID, accountName)
}

// Session represents an active user session.
type Session struct {
	Token     string    `json:"token" dynamodbav:"PK"`
	UserID    string    `json:"user_id" dynamodbav:"UserID"`
	Email     string    `json:"email" dynamodbav:"Email"`
	ExpiresAt time.Time `json:"expires_at" dynamodbav:"ExpiresAt"`
	CreatedAt time.Time `json:"created_at" dynamodbav:"CreatedAt"`
	UserAgent string    `json:"user_agent,omitempty" dynamodbav:"UserAgent"`
	IPAddress string    `json:"ip_address,omitempty" dynamodbav:"IPAddress"`
	CSRFToken string    `json:"csrf_token,omitempty" dynamodbav:"CSRFToken"`
}

// LoginRequest represents a login attempt.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	MFACode  string `json:"mfa_code,omitempty"`
}

// LoginResponse is returned after successful login.
type LoginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      *UserInfo `json:"user"`
	CSRFToken string    `json:"csrf_token,omitempty"`
}

// UserInfo is the public user info returned to clients.
type UserInfo struct {
	ID         string   `json:"id"`
	Email      string   `json:"email"`
	Groups     []string `json:"groups,omitempty"`
	MFAEnabled bool     `json:"mfa_enabled"`
}

// PasswordResetRequest initiates a password reset.
type PasswordResetRequest struct {
	Email string `json:"email"`
}

// PasswordResetConfirm completes a password reset.
type PasswordResetConfirm struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// CreateUserRequest for admin creating users. GroupIDs must contain at least
// one group: authorization derives entirely from group membership (issue #907).
type CreateUserRequest struct {
	Email    string   `json:"email"`
	Password string   `json:"password"`
	GroupIDs []string `json:"group_ids,omitempty"`
}

// UpdateUserRequest for updating user details.
//
// Email is a pointer so callers can distinguish "not sending email" (nil)
// from "explicitly setting email to a new value". This matters because the
// service layer applies email changes via updateUserEmail, which performs
// format validation and uniqueness checks that must NOT run on no-op
// updates that only touch groups/active.
//
// GroupIDs is nil when the caller is not changing group membership; a non-nil
// (including empty) slice replaces the membership and must be non-empty.
type UpdateUserRequest struct {
	Email    *string  `json:"email,omitempty"`
	GroupIDs []string `json:"group_ids,omitempty"`
	Active   *bool    `json:"active,omitempty"`
}

// ChangePasswordRequest for users changing their own password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// SetupAdminRequest for first-time admin setup with API key.
type SetupAdminRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// CreateAPIKeyRequest for creating a new user API key.
type CreateAPIKeyRequest struct {
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions,omitempty"`
	ExpiresAt   *time.Time   `json:"expires_at,omitempty"`
}

// CreateAPIKeyResponse returns the newly created API key (only shown once).
type CreateAPIKeyResponse struct {
	APIKey string      `json:"api_key"` // Full key - only returned on creation
	KeyID  string      `json:"key_id"`
	Info   *UserAPIKey `json:"info"`
}

// Predefined roles.
const (
	RoleAdmin    = "admin"
	RoleUser     = "user"
	RoleReadOnly = "readonly"
)

// DefaultAdminGroupID is the fixed UUID of the Administrators group seeded
// by migration 000024. SetupAdmin auto-assigns new admin users to this
// group so the group card shows members on a fresh install.
const DefaultAdminGroupID = "00000000-0000-5000-8000-000000000001"

// DefaultPurchaserGroupID is the fixed UUID of the Purchaser group, relocated
// by migration 000064 to resolve the UUID collision with "Standard Users"
// (issue #942). It holds the three money-spending verbs carved out of the
// admin:* wildcard (issue #923).
const DefaultPurchaserGroupID = "00000000-0000-5000-8000-000000000007"

// GroupPurchaser is the canonical name of the system-managed Purchaser
// group. MUST match the literal name inserted by migration
// 000059_seed_purchaser_group.up.sql so name-based lookups agree with
// the seeded row.
const GroupPurchaser = "Purchaser"

// Predefined actions.
const (
	ActionView    = "view"
	ActionCreate  = "create"
	ActionUpdate  = "update"
	ActionDelete  = "delete"
	ActionExecute = "execute"
	ActionApprove = "approve"
	ActionAdmin   = "admin"
	// ActionCancelOwn / ActionCancelAny gate the session-authed Cancel
	// button on pending Purchase History rows (issue #46).
	//
	// Default grants:
	//   * RoleAdmin — implicit via {ActionAdmin, ResourceAll}; covers
	//     both verbs.
	//   * RoleUser — DefaultUserPermissions() adds cancel-own:purchases.
	//     Allows canceling pending executions whose created_by_user_id
	//     matches the session user. Legacy rows with NULL creator are
	//     out of reach for non-admins via this verb; admins still cancel
	//     them via cancel-any.
	//   * RoleReadOnly — neither verb. Read-only users cannot cancel.
	//
	// cancel-any has no default non-admin grant; the constant exists so
	// future operator roles can be granted broad cancel rights without
	// escalating to admin. Add it to a custom group's Permissions to
	// enable that path.
	//
	// The legacy email-token cancel path stays unchanged as an escape
	// hatch and is gated by token possession, not these verbs.
	ActionCancelOwn = "cancel-own"
	ActionCancelAny = "cancel-any"
	// ActionRetryOwn / ActionRetryAny gate the session-authed Retry
	// button on failed Purchase History rows (issue #47). Mirror image
	// of the cancel verbs above:
	//
	//   * RoleAdmin — implicit via {ActionAdmin, ResourceAll}; covers
	//     both verbs.
	//   * RoleUser — DefaultUserPermissions() adds retry-own:purchases.
	//     Allows retrying failed executions whose created_by_user_id
	//     matches the session user. Legacy rows with NULL creator are
	//     out of reach for non-admins via this verb; admins still
	//     retry them via retry-any.
	//   * RoleReadOnly — neither verb. Read-only users cannot retry.
	//
	// retry-any has no default non-admin grant; the constant exists so
	// future operator roles can be granted broad retry rights without
	// escalating to admin.
	//
	// Retry creates a NEW purchase execution from the failed row's
	// stored Recommendations slice; it is NOT a status mutation of the
	// original row (the original keeps its `failed` status as a
	// historical record and gains a retry_execution_id pointer to the
	// successor). The "execute purchases" action is therefore the
	// natural permission to require, but the retry verbs let us gate
	// the *source* — a user without retry-own can still trigger fresh
	// purchases via the Recommendations page; they just can't act on
	// somebody else's failed row.
	ActionRetryOwn = "retry-own"
	ActionRetryAny = "retry-any"
	// ActionApproveOwn / ActionApproveAny gate the session-authed Approve
	// button on pending Purchase History rows (issue #286). Mirror image
	// of the cancel-{own,any} verbs above:
	//
	//   * RoleAdmin — implicit via {ActionAdmin, ResourceAll}; covers
	//     both verbs.
	//   * RoleUser — DefaultUserPermissions() adds approve-own:purchases.
	//     Allows approving pending executions whose created_by_user_id
	//     matches the session user. Legacy rows with NULL creator are
	//     out of reach for non-admins via this verb; admins still
	//     approve them via approve-any.
	//   * RoleReadOnly — neither verb. Read-only users cannot approve.
	//
	// approve-any has no default non-admin grant; the constant exists so
	// future operator roles can be granted broad approve rights without
	// escalating to admin. Add it to a custom group's Permissions to
	// enable that path.
	//
	// The legacy email-token approve path stays unchanged as an escape
	// hatch and is gated by token possession + the per-account
	// contact_email gate (PR #101), not these verbs.
	ActionApproveOwn = "approve-own"
	ActionApproveAny = "approve-any"
	// ActionExecuteOwn / ActionExecuteAny gate the direct-execute shortcut
	// on the Recommendations page (issue #289). A holder skips the approval
	// email and immediately commits the purchase, with audit fields
	// (executed_by_user_id, executed_at, pre_approval_skip_reason) stamped
	// on the execution row.
	//
	//   * RoleAdmin — implicit via {ActionAdmin, ResourceAll}; covers
	//     both verbs.
	//   * RoleUser — NO default grant. This is a finance-impacting permission
	//     that must be explicitly granted per-user/per-role. Even trusted
	//     users submit via the approval flow by default; only deliberately
	//     privileged accounts should hold this verb.
	//   * RoleReadOnly — neither verb.
	//
	// execute-own: allows direct-execute only for executions where
	//   created_by_user_id == session user (the user drafted the purchase
	//   themselves). Like approve-own, legacy rows with NULL creator are
	//   unreachable for non-admins via this verb.
	// execute-any: allows direct-execute regardless of creator; no ownership
	//   check. No default non-admin grant; add to a custom operator group.
	ActionExecuteOwn = "execute-own"
	ActionExecuteAny = "execute-any"
	// ActionUpdateAny is the privileged escape that lets a holder manage
	// (pause / resume / run / delete) a SCHEDULED purchase execution
	// regardless of who created it (issue #950). It complements the base
	// update:purchases verb every authenticated user already holds: that
	// base verb authorizes managing only your OWN scheduled purchases
	// (created_by_user_id == session.UserID), while update-any drops the
	// per-record ownership check.
	//
	//   * RoleAdmin — implicit via {ActionAdmin, ResourceAll}; update-any is
	//     NOT in adminCarvedOuts, so admins manage every scheduled purchase.
	//   * RoleUser — NO default grant. A standard user manages only the
	//     scheduled purchases they created (base update:purchases + creator
	//     match). Legacy rows with NULL created_by_user_id are out of reach
	//     for non-admins (they hold neither update-any nor a creator match).
	//   * Custom operator groups — add update-any:purchases to let a role
	//     manage everyone's scheduled purchases without escalating to admin.
	//
	// There is no separate update-own verb: the existing update:purchases
	// grant already plays that role, mirroring how cancel-own/approve-own
	// gate History rows. The creator match is enforced in the handler
	// (authorizeExecutionManagement), not in HasPermission.
	ActionUpdateAny = "update-any"
	// ActionRevokeOwn / ActionRevokeAny gate the in-app Revoke button on
	// completed purchase_history rows while still within the provider's
	// free-cancel window (issue #290).
	//
	// Default grants:
	//   * RoleAdmin   -- implicit via {ActionAdmin, ResourceAll}.
	//   * RoleUser    -- DefaultUserPermissions() adds revoke-own:purchases.
	//     "Own" is currently enforced at ACCOUNT scope, not creator scope:
	//     a user may revoke a completed purchase in any cloud account they
	//     are allowed to access (the check in
	//     api.checkRevokeOwnAccountAccess via GetAllowedAccountsAPI), because
	//     purchase_history rows pre-date created_by_user_id and have no
	//     reliable per-creator attribution. Rows with no account association
	//     (CloudAccountID NULL) are out of reach for non-admins (fail-closed);
	//     admins still revoke them via revoke-any.
	//     NOTE: whether revoke-own should instead be creator-scoped is a
	//     product decision tracked in issue #950; do not tighten this to
	//     created_by_user_id without resolving that issue first.
	//   * RoleReadOnly -- neither verb.
	//
	// revoke-any has no default non-admin grant; the constant exists so
	// future operator roles can be granted broad revoke rights without
	// escalating to admin.
	ActionRevokeOwn = "revoke-own"
	ActionRevokeAny = "revoke-any"
)

// Predefined resources.
const (
	ResourceRecommendations = "recommendations"
	ResourcePlans           = "plans"
	ResourcePurchases       = "purchases"
	ResourceHistory         = "history"
	ResourceConfig          = "config"
	ResourceAccounts        = "accounts"
	ResourceUsers           = "users"
	ResourceGroups          = "groups"
	ResourceAPIKeys         = "api-keys"
	// ResourceRIExchange gates RI-exchange-specific operations. The execute
	// verb on this resource is deliberately separate from execute:purchases
	// because RI exchanges are financially irreversible (the AWS API does
	// not have a rollback path once an exchange is submitted). Admins carry
	// implicit access via {ActionAdmin, ResourceAll}. Non-admin roles must
	// be explicitly granted execute:ri-exchange by a custom group; there is
	// no default user-role grant.
	ResourceRIExchange = "ri-exchange"
	ResourceAll        = "*"
)

// DefaultAdminPermissions returns full admin permissions.
func DefaultAdminPermissions() []Permission {
	return []Permission{
		{Action: ActionAdmin, Resource: ResourceAll},
	}
}

// DefaultUserPermissions returns standard user permissions.
func DefaultUserPermissions() []Permission {
	return []Permission{
		{Action: ActionView, Resource: ResourceRecommendations},
		{Action: ActionView, Resource: ResourcePlans},
		{Action: ActionView, Resource: ResourcePurchases},
		{Action: ActionView, Resource: ResourceHistory},
		{Action: ActionCreate, Resource: ResourcePlans},
		{Action: ActionUpdate, Resource: ResourcePlans},
		// delete:plans — every authenticated user can delete plans they
		// have access to (PR-A of #660). The handler still requires
		// requirePermission("delete", "plans") and the plan-access scope
		// check, so only plans in the user's allowed accounts are
		// reachable.
		{Action: ActionDelete, Resource: ResourcePlans},
		// update:purchases — every authenticated user can pause, resume,
		// and update planned purchase executions (PR-A of #660). The
		// handler still requires requirePermission("update", "purchases")
		// and the execution-access scope check.
		{Action: ActionUpdate, Resource: ResourcePurchases},
		// cancel-own:purchases — every authenticated user can cancel
		// pending purchase executions they created themselves (issue #46).
		// The handler still requires the execution to be in a cancellable
		// state (pending/notified) and the creator UUID to match the
		// session UserID before honoring the request.
		{Action: ActionCancelOwn, Resource: ResourcePurchases},
		// retry-own:purchases — every authenticated user can retry
		// failed purchase executions they created themselves (issue #47).
		// The handler still requires the execution to be in `failed`
		// state, the creator UUID to match the session UserID, the
		// failure reason not to match the persistent-misconfig list,
		// and the retry-attempt counter on the chain to be below the
		// soft-block threshold (overridable with ?force=true).
		{Action: ActionRetryOwn, Resource: ResourcePurchases},
		// approve-own:purchases — every authenticated user can approve
		// pending purchase executions they created themselves (issue #286).
		// The handler still requires the execution to be in an approvable
		// state (pending/notified) and the creator UUID to match the
		// session UserID before honoring the request. The legacy email-
		// token approve path stays as an escape hatch for non-session
		// approvers.
		{Action: ActionApproveOwn, Resource: ResourcePurchases},
		// revoke-own:purchases — every authenticated user can revoke completed
		// purchases they created themselves while still within the provider's
		// free-cancel window (issue #290). The handler verifies the window has
		// not closed, the provider supports a direct revocation API, and the
		// creator UUID matches. Legacy rows with NULL creator are out of reach
		// for non-admins (email-token paths have no revocation escape hatch).
		{Action: ActionRevokeOwn, Resource: ResourcePurchases},
	}
}

// DefaultReadOnlyPermissions returns read-only permissions.
func DefaultReadOnlyPermissions() []Permission {
	return []Permission{
		{Action: ActionView, Resource: ResourceRecommendations},
		{Action: ActionView, Resource: ResourcePlans},
		{Action: ActionView, Resource: ResourceHistory},
	}
}

// DefaultPurchaserPermissions returns the permissions for the system-managed
// Purchaser group (issue #923). The three execute/approve-any/retry-any verbs
// are carved out of the admin:* wildcard; a user must hold them explicitly
// (via this group or a custom group that includes them) to spend money.
func DefaultPurchaserPermissions() []Permission {
	return []Permission{
		// Money-spending verbs (carved out of admin:* wildcard).
		{Action: ActionExecute, Resource: ResourcePurchases},
		{Action: ActionApproveAny, Resource: ResourcePurchases},
		{Action: ActionRetryAny, Resource: ResourcePurchases},
		// Read access so Purchaser members can navigate to the relevant pages.
		{Action: ActionView, Resource: ResourceRecommendations},
		{Action: ActionView, Resource: ResourcePlans},
		{Action: ActionView, Resource: ResourcePurchases},
		{Action: ActionView, Resource: ResourceHistory},
	}
}
