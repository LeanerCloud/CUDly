// Package auth provides user authentication and authorization.
package auth

import (
	"time"
)

// User represents a user account
type User struct {
	ID                  string     `json:"id" dynamodbav:"PK"`
	Email               string     `json:"email" dynamodbav:"Email"`
	PasswordHash        string     `json:"-" dynamodbav:"PasswordHash"`
	Salt                string     `json:"-" dynamodbav:"Salt"`
	Role                string     `json:"role" dynamodbav:"Role"` // admin, user, readonly
	GroupIDs            []string   `json:"group_ids,omitempty" dynamodbav:"GroupIDs"`
	CreatedAt           time.Time  `json:"created_at" dynamodbav:"CreatedAt"`
	UpdatedAt           time.Time  `json:"updated_at" dynamodbav:"UpdatedAt"`
	LastLoginAt         *time.Time `json:"last_login_at,omitempty" dynamodbav:"LastLoginAt"`
	PasswordResetToken  string     `json:"-" dynamodbav:"PasswordResetToken,omitempty"`
	PasswordResetExpiry *time.Time `json:"-" dynamodbav:"PasswordResetExpiry,omitempty"`
	Active              bool       `json:"active" dynamodbav:"Active"`
	MFAEnabled          bool       `json:"mfa_enabled" dynamodbav:"MFAEnabled"`
	MFASecret           string     `json:"-" dynamodbav:"MFASecret,omitempty"`
	// Account lockout fields for brute-force protection
	FailedLoginAttempts int        `json:"-" dynamodbav:"FailedLoginAttempts,omitempty"`
	LockedUntil         *time.Time `json:"-" dynamodbav:"LockedUntil,omitempty"`
	// Password history for preventing reuse (stores up to 5 previous password hashes)
	PasswordHistory []string `json:"-" dynamodbav:"PasswordHistory,omitempty"`
}

// Group represents a permission group
type Group struct {
	ID              string       `json:"id" dynamodbav:"PK"`
	Name            string       `json:"name" dynamodbav:"Name"`
	Description     string       `json:"description,omitempty" dynamodbav:"Description"`
	Permissions     []Permission `json:"permissions" dynamodbav:"Permissions"`
	AllowedAccounts []string     `json:"allowed_accounts,omitempty" dynamodbav:"AllowedAccounts"`
	CreatedAt       time.Time    `json:"created_at" dynamodbav:"CreatedAt"`
	UpdatedAt       time.Time    `json:"updated_at" dynamodbav:"UpdatedAt"`
	CreatedBy       string       `json:"created_by" dynamodbav:"CreatedBy"`
}

// Permission defines what actions a group can perform
type Permission struct {
	// Action: view, purchase, configure, admin
	Action string `json:"action" dynamodbav:"Action"`

	// Resource type: recommendations, plans, history, config, users
	Resource string `json:"resource" dynamodbav:"Resource"`

	// Constraints limit the permission to specific contexts
	Constraints *PermissionConstraints `json:"constraints,omitempty" dynamodbav:"Constraints"`
}

// PermissionConstraints limit permissions to specific accounts, providers, or services
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

// UserAPIKey represents a personal API key for a user with scoped permissions
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
// It combines user role, group memberships, and computed permissions
type AuthContext struct {
	User            *User
	Groups          []*Group
	AllowedAccounts []string     // Computed from all groups (union)
	Permissions     []Permission // Computed from role + groups
}

// HasPermission checks if the auth context has a specific permission
func (ctx *AuthContext) HasPermission(action, resource string) bool {
	// Admin has all permissions
	if ctx.User.Role == RoleAdmin {
		return true
	}

	for _, perm := range ctx.Permissions {
		// Admin permission grants all access
		if perm.Action == ActionAdmin && perm.Resource == ResourceAll {
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

// CanAccessAccount checks if the user can access a specific account ID
func (ctx *AuthContext) CanAccessAccount(accountID string) bool {
	// Admin users have access to all accounts
	if ctx.User.Role == RoleAdmin {
		return true
	}

	// Empty AllowedAccounts means all access
	if len(ctx.AllowedAccounts) == 0 {
		return true
	}

	// Check for wildcard
	for _, allowed := range ctx.AllowedAccounts {
		if allowed == "*" {
			return true
		}
		if allowed == accountID {
			return true
		}
	}

	return false
}

// Session represents an active user session
type Session struct {
	Token     string    `json:"token" dynamodbav:"PK"`
	UserID    string    `json:"user_id" dynamodbav:"UserID"`
	Email     string    `json:"email" dynamodbav:"Email"`
	Role      string    `json:"role" dynamodbav:"Role"`
	ExpiresAt time.Time `json:"expires_at" dynamodbav:"ExpiresAt"`
	CreatedAt time.Time `json:"created_at" dynamodbav:"CreatedAt"`
	UserAgent string    `json:"user_agent,omitempty" dynamodbav:"UserAgent"`
	IPAddress string    `json:"ip_address,omitempty" dynamodbav:"IPAddress"`
	CSRFToken string    `json:"csrf_token,omitempty" dynamodbav:"CSRFToken"`
}

// LoginRequest represents a login attempt
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	MFACode  string `json:"mfa_code,omitempty"`
}

// LoginResponse is returned after successful login
type LoginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      *UserInfo `json:"user"`
	CSRFToken string    `json:"csrf_token,omitempty"`
}

// UserInfo is the public user info returned to clients
type UserInfo struct {
	ID         string   `json:"id"`
	Email      string   `json:"email"`
	Role       string   `json:"role"`
	Groups     []string `json:"groups,omitempty"`
	MFAEnabled bool     `json:"mfa_enabled"`
}

// PasswordResetRequest initiates a password reset
type PasswordResetRequest struct {
	Email string `json:"email"`
}

// PasswordResetConfirm completes a password reset
type PasswordResetConfirm struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// CreateUserRequest for admin creating users
type CreateUserRequest struct {
	Email    string   `json:"email"`
	Password string   `json:"password"`
	Role     string   `json:"role"`
	GroupIDs []string `json:"group_ids,omitempty"`
}

// UpdateUserRequest for updating user details
type UpdateUserRequest struct {
	Role     *string  `json:"role,omitempty"`
	GroupIDs []string `json:"group_ids,omitempty"`
	Active   *bool    `json:"active,omitempty"`
}

// ChangePasswordRequest for users changing their own password
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// SetupAdminRequest for first-time admin setup with API key
type SetupAdminRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// CreateAPIKeyRequest for creating a new user API key
type CreateAPIKeyRequest struct {
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions,omitempty"`
	ExpiresAt   *time.Time   `json:"expires_at,omitempty"`
}

// CreateAPIKeyResponse returns the newly created API key (only shown once)
type CreateAPIKeyResponse struct {
	APIKey string      `json:"api_key"` // Full key - only returned on creation
	KeyID  string      `json:"key_id"`
	Info   *UserAPIKey `json:"info"`
}

// Predefined roles
const (
	RoleAdmin    = "admin"
	RoleUser     = "user"
	RoleReadOnly = "readonly"
)

// Predefined actions
const (
	ActionView    = "view"
	ActionCreate  = "create"
	ActionUpdate  = "update"
	ActionDelete  = "delete"
	ActionExecute = "execute"
	ActionApprove = "approve"
	ActionAdmin   = "admin"
)

// Predefined resources
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
	ResourceAll             = "*"
)

// DefaultAdminPermissions returns full admin permissions
func DefaultAdminPermissions() []Permission {
	return []Permission{
		{Action: ActionAdmin, Resource: ResourceAll},
	}
}

// DefaultUserPermissions returns standard user permissions
func DefaultUserPermissions() []Permission {
	return []Permission{
		{Action: ActionView, Resource: ResourceRecommendations},
		{Action: ActionView, Resource: ResourcePlans},
		{Action: ActionView, Resource: ResourcePurchases},
		{Action: ActionView, Resource: ResourceHistory},
		{Action: ActionCreate, Resource: ResourcePlans},
		{Action: ActionUpdate, Resource: ResourcePlans},
	}
}

// DefaultReadOnlyPermissions returns read-only permissions
func DefaultReadOnlyPermissions() []Permission {
	return []Permission{
		{Action: ActionView, Resource: ResourceRecommendations},
		{Action: ActionView, Resource: ResourcePlans},
		{Action: ActionView, Resource: ResourceHistory},
	}
}
