package auth

import (
	"context"
	"fmt"
	"time"
)

// API adapter types - these match the types in internal/api/handler.go

// APIUser is the user type for API responses.
//
// Groups deliberately has NO omitempty: the frontend's TS type
// (frontend/src/api/types.ts) declares groups as a required string[],
// and renderers read user.groups.length / iterate user.groups without
// a guard. Omitting the field on empty slices breaks that contract
// and crashes the admin users page with "TypeError: Cannot read
// properties of undefined (reading 'length')" — see issue #350.
type APIUser struct {
	ID         string   `json:"id"`
	Email      string   `json:"email"`
	CreatedAt  string   `json:"created_at,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
	LastLogin  string   `json:"last_login,omitempty"`
	Groups     []string `json:"groups"`
	MFAEnabled bool     `json:"mfa_enabled"`
}

// APIGroup is the group type for API responses.
//
// AllowedAccounts has NO omitempty for the same reason Groups doesn't on
// APIUser — the frontend treats it as always present. See issue #350.
type APIGroup struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	CreatedAt       string          `json:"created_at,omitempty"`
	UpdatedAt       string          `json:"updated_at,omitempty"`
	Permissions     []APIPermission `json:"permissions"`
	AllowedAccounts []string        `json:"allowed_accounts"`
}

// APIPermission is the permission type for API responses.
type APIPermission struct {
	Constraints *APIPermissionConstraint `json:"constraints,omitempty"`
	Action      string                   `json:"action"`
	Resource    string                   `json:"resource"`
}

// APIPermissionConstraint is the permission constraint type for API responses.
type APIPermissionConstraint struct {
	Accounts  []string `json:"accounts,omitempty"`
	Providers []string `json:"providers,omitempty"`
	Services  []string `json:"services,omitempty"`
	Regions   []string `json:"regions,omitempty"`
	MaxAmount float64  `json:"max_amount,omitempty"`
}

// APICreateUserRequest is the request type for creating users via API.
// Groups must be non-empty: authorization is group-membership-only (issue #907).
type APICreateUserRequest struct {
	Email    string   `json:"email"`
	Password string   `json:"password"`
	Groups   []string `json:"groups,omitempty"`
}

// APICreateUserResponse is the response type for POST /api/users. It
// embeds APIUser so existing consumers keep reading the flat
// {id, email, role, ...} fields and only callers that need the new
// invite-status information have to look at the extra optional fields.
//
// InviteEmailSent is non-nil only when the request created an invited
// (passwordless) user. true means the invite email was handed to the
// configured sender; false means the user row exists but the recipient
// hasn't been told how to activate it and the admin should re-mail the
// setup link via Forgot Password.
type APICreateUserResponse struct {
	*APIUser
	InviteEmailSent  *bool  `json:"invite_email_sent,omitempty"`
	InviteEmailError string `json:"invite_email_error,omitempty"`
}

// APIUpdateUserRequest is the request type for updating users via API.
//
// Groups is decoded from JSON, so the handler cannot use a nil slice to mean
// "not sent". A non-empty Groups replaces the user's membership; an empty/nil
// Groups means "leave membership unchanged" (callers that intend to change
// groups always send at least one, since zero-group users are forbidden).
type APIUpdateUserRequest struct {
	Email  string   `json:"email,omitempty"`
	Groups []string `json:"groups,omitempty"`
}

// APICreateGroupRequest is the request type for creating groups via API.
type APICreateGroupRequest struct {
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	Permissions     []APIPermission `json:"permissions"`
	AllowedAccounts []string        `json:"allowed_accounts,omitempty"`
}

// APIUpdateGroupRequest is the request type for updating groups via API.
// AllowedAccounts has no omitempty: clients must be able to send an explicit
// empty slice to clear account restrictions. Nil means "not sent".
type APIUpdateGroupRequest struct {
	Name            string          `json:"name,omitempty"`
	Description     string          `json:"description,omitempty"`
	Permissions     []APIPermission `json:"permissions,omitempty"`
	AllowedAccounts []string        `json:"allowed_accounts"`
}

// Conversion helpers

func userToAPIUser(u *User) *APIUser {
	if u == nil {
		return nil
	}
	var lastLogin string
	if u.LastLoginAt != nil {
		lastLogin = u.LastLoginAt.Format(time.RFC3339)
	}
	// Substitute an empty slice for nil so the JSON encoder emits "[]"
	// rather than "null", matching the TS contract that declares
	// APIUser.groups as a required string[] (issue #350).
	groups := u.GroupIDs
	if groups == nil {
		groups = []string{}
	}
	return &APIUser{
		ID:         u.ID,
		Email:      u.Email,
		Groups:     groups,
		MFAEnabled: u.MFAEnabled,
		CreatedAt:  u.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  u.UpdatedAt.Format(time.RFC3339),
		LastLogin:  lastLogin,
	}
}

func groupToAPIGroup(g *Group) *APIGroup {
	if g == nil {
		return nil
	}
	apiPerms := make([]APIPermission, len(g.Permissions))
	for i, p := range g.Permissions {
		apiPerms[i] = permissionToAPIPermission(p)
	}
	// Empty-slice substitution: see userToAPIUser. Issue #350.
	allowedAccounts := g.AllowedAccounts
	if allowedAccounts == nil {
		allowedAccounts = []string{}
	}
	return &APIGroup{
		ID:              g.ID,
		Name:            g.Name,
		Description:     g.Description,
		Permissions:     apiPerms,
		AllowedAccounts: allowedAccounts,
		CreatedAt:       g.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       g.UpdatedAt.Format(time.RFC3339),
	}
}

func permissionToAPIPermission(p Permission) APIPermission {
	ap := APIPermission{
		Action:   p.Action,
		Resource: p.Resource,
	}
	if p.Constraints != nil {
		ap.Constraints = &APIPermissionConstraint{
			Accounts:  p.Constraints.AccountIDs,
			Providers: p.Constraints.Providers,
			Services:  p.Constraints.Services,
			Regions:   p.Constraints.Regions,
			MaxAmount: p.Constraints.MaxPurchaseAmount,
		}
	}
	return ap
}

func apiPermissionToPermission(ap APIPermission) Permission {
	p := Permission{
		Action:   ap.Action,
		Resource: ap.Resource,
	}
	if ap.Constraints != nil {
		p.Constraints = &PermissionConstraints{
			AccountIDs:        ap.Constraints.Accounts,
			Providers:         ap.Constraints.Providers,
			Services:          ap.Constraints.Services,
			Regions:           ap.Constraints.Regions,
			MaxPurchaseAmount: ap.Constraints.MaxAmount,
		}
	}
	return p
}

// API adapter methods - these implement the AuthServiceInterface from handler.go
// They use any to avoid import cycles with the api package

// CreateUserAPI creates a new user via the API.
func (s *Service) CreateUserAPI(ctx context.Context, reqInterface any) (any, error) {
	req, ok := reqInterface.(APICreateUserRequest)
	if !ok {
		return nil, fmt.Errorf("invalid request type")
	}
	authReq := CreateUserRequest{
		Email:    req.Email,
		Password: req.Password,
		GroupIDs: req.Groups,
	}
	result, err := s.CreateUser(ctx, authReq)
	if err != nil {
		return nil, err
	}
	return &APICreateUserResponse{
		APIUser:          userToAPIUser(result.User),
		InviteEmailSent:  result.InviteEmailSent,
		InviteEmailError: result.InviteEmailError,
	}, nil
}

// UpdateUserAPI updates a user via the API. actorUserID is the authenticated
// caller performing the change (from the session, never the request body); it
// is used by the service layer to enforce the self-escalation guard (#907).
func (s *Service) UpdateUserAPI(ctx context.Context, actorUserID, userID string, reqInterface any) (any, error) {
	req, ok := reqInterface.(APIUpdateUserRequest)
	if !ok {
		return nil, fmt.Errorf("invalid request type")
	}
	authReq := UpdateUserRequest{
		GroupIDs: req.Groups,
	}
	// Wire email through so admins can edit other users' email addresses.
	// Before #892 this field was silently dropped: the API accepted email
	// in the JSON, the handler returned 200, and the user's email column
	// was never touched, producing a false-positive success toast and
	// (worst case) a sign-in lockout once the user lost the old address.
	if req.Email != "" {
		authReq.Email = &req.Email
	}
	user, err := s.UpdateUser(ctx, actorUserID, userID, authReq)
	if err != nil {
		return nil, err
	}
	return userToAPIUser(user), nil
}

// ListUsersAPI returns all users via the API.
func (s *Service) ListUsersAPI(ctx context.Context) (any, error) {
	users, err := s.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*APIUser, len(users))
	for i := range users {
		result[i] = userToAPIUser(&users[i])
	}
	return result, nil
}

// ChangePasswordAPI changes a user's password via the API.
func (s *Service) ChangePasswordAPI(ctx context.Context, userID, currentPassword, newPassword string) error {
	req := ChangePasswordRequest{
		CurrentPassword: currentPassword,
		NewPassword:     newPassword,
	}
	return s.ChangePassword(ctx, userID, req)
}

// CreateGroupAPI creates a new group via the API.
func (s *Service) CreateGroupAPI(ctx context.Context, reqInterface any) (any, error) {
	req, ok := reqInterface.(APICreateGroupRequest)
	if !ok {
		return nil, fmt.Errorf("invalid request type")
	}
	perms := make([]Permission, len(req.Permissions))
	for i, p := range req.Permissions {
		perms[i] = apiPermissionToPermission(p)
	}
	group := &Group{
		Name:            req.Name,
		Description:     req.Description,
		Permissions:     perms,
		AllowedAccounts: req.AllowedAccounts,
	}
	// Use empty string for createdBy since we don't have user context here
	if err := s.CreateGroup(ctx, group, ""); err != nil {
		return nil, err
	}
	return groupToAPIGroup(group), nil
}

// UpdateGroupAPI updates a group via the API.
func (s *Service) UpdateGroupAPI(ctx context.Context, groupID string, reqInterface any) (any, error) {
	req, ok := reqInterface.(APIUpdateGroupRequest)
	if !ok {
		return nil, fmt.Errorf("invalid request type")
	}
	group, err := s.GetGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, fmt.Errorf("group not found")
	}

	if req.Name != "" {
		group.Name = req.Name
	}
	if req.Description != "" {
		group.Description = req.Description
	}
	if len(req.Permissions) > 0 {
		perms := make([]Permission, len(req.Permissions))
		for i, p := range req.Permissions {
			perms[i] = apiPermissionToPermission(p)
		}
		group.Permissions = perms
	}
	if req.AllowedAccounts != nil {
		group.AllowedAccounts = req.AllowedAccounts
	}

	group.UpdatedAt = time.Now()
	if err := s.UpdateGroup(ctx, group); err != nil {
		return nil, err
	}
	return groupToAPIGroup(group), nil
}

// GetGroupAPI returns a group by ID via the API.
func (s *Service) GetGroupAPI(ctx context.Context, groupID string) (any, error) {
	group, err := s.GetGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, fmt.Errorf("group not found")
	}
	return groupToAPIGroup(group), nil
}

// ListGroupsAPI returns all groups via the API.
func (s *Service) ListGroupsAPI(ctx context.Context) (any, error) {
	groups, err := s.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*APIGroup, len(groups))
	for i := range groups {
		result[i] = groupToAPIGroup(&groups[i])
	}
	return result, nil
}

// HasPermissionAPI checks if a user has a specific permission via the API.
func (s *Service) HasPermissionAPI(ctx context.Context, userID, action, resource string) (bool, error) {
	return s.HasPermission(ctx, userID, action, resource, nil)
}

// GetUserPermissionsAPI returns the effective permission set for a user via
// the API. Calls GetUserPermissions (the same union path the server enforces
// with) and converts each Permission to an APIPermission for the wire format.
// The handler asserts the return value to []APIPermission.
func (s *Service) GetUserPermissionsAPI(ctx context.Context, userID string) (any, error) {
	perms, err := s.GetUserPermissions(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := make([]APIPermission, len(perms))
	for i, p := range perms {
		result[i] = permissionToAPIPermission(p)
	}
	return result, nil
}

// MFASetupAPI starts an MFA enrollment via the API. Returns the
// freshly-generated secret + provisioning URI (the otpauth:// URI
// the frontend renders as a QR code). Wraps MFASetup; thin shim
// exists so the api package can refer to a stable signature without
// importing the auth package's internal MFASetupResult type.
func (s *Service) MFASetupAPI(ctx context.Context, userID, password string) (string, string, error) {
	result, err := s.MFASetup(ctx, userID, password)
	if err != nil {
		return "", "", err
	}
	return result.Secret, result.ProvisioningURI, nil
}

// MFAEnableAPI finalizes an enrollment via the API.
func (s *Service) MFAEnableAPI(ctx context.Context, userID, code string) ([]string, error) {
	return s.MFAEnable(ctx, userID, code)
}

// MFADisableAPI turns off MFA via the API.
func (s *Service) MFADisableAPI(ctx context.Context, userID, password, codeOrRecovery string) error {
	return s.MFADisable(ctx, userID, password, codeOrRecovery)
}

// MFARegenerateRecoveryCodesAPI replaces stored recovery codes via the API.
func (s *Service) MFARegenerateRecoveryCodesAPI(ctx context.Context, userID, code string) ([]string, error) {
	return s.MFARegenerateRecoveryCodes(ctx, userID, code)
}
