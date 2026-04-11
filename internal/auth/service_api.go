package auth

import (
	"context"
	"fmt"
	"time"
)

// API adapter types - these match the types in internal/api/handler.go

// APIUser is the user type for API responses
type APIUser struct {
	ID         string   `json:"id"`
	Email      string   `json:"email"`
	Role       string   `json:"role"`
	Groups     []string `json:"groups,omitempty"`
	MFAEnabled bool     `json:"mfa_enabled"`
	CreatedAt  string   `json:"created_at,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
}

// APIGroup is the group type for API responses
type APIGroup struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	Permissions     []APIPermission `json:"permissions"`
	AllowedAccounts []string        `json:"allowed_accounts,omitempty"`
	CreatedAt       string          `json:"created_at,omitempty"`
	UpdatedAt       string          `json:"updated_at,omitempty"`
}

// APIPermission is the permission type for API responses
type APIPermission struct {
	Action      string                   `json:"action"`
	Resource    string                   `json:"resource"`
	Constraints *APIPermissionConstraint `json:"constraints,omitempty"`
}

// APIPermissionConstraint is the permission constraint type for API responses
type APIPermissionConstraint struct {
	Accounts  []string `json:"accounts,omitempty"`
	Providers []string `json:"providers,omitempty"`
	Services  []string `json:"services,omitempty"`
	Regions   []string `json:"regions,omitempty"`
	MaxAmount float64  `json:"max_amount,omitempty"`
}

// APICreateUserRequest is the request type for creating users via API
type APICreateUserRequest struct {
	Email    string   `json:"email"`
	Password string   `json:"password"`
	Role     string   `json:"role"`
	Groups   []string `json:"groups,omitempty"`
}

// APIUpdateUserRequest is the request type for updating users via API
type APIUpdateUserRequest struct {
	Email  string   `json:"email,omitempty"`
	Role   string   `json:"role,omitempty"`
	Groups []string `json:"groups,omitempty"`
}

// APICreateGroupRequest is the request type for creating groups via API
type APICreateGroupRequest struct {
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	Permissions     []APIPermission `json:"permissions"`
	AllowedAccounts []string        `json:"allowed_accounts,omitempty"`
}

// APIUpdateGroupRequest is the request type for updating groups via API
type APIUpdateGroupRequest struct {
	Name            string          `json:"name,omitempty"`
	Description     string          `json:"description,omitempty"`
	Permissions     []APIPermission `json:"permissions,omitempty"`
	AllowedAccounts []string        `json:"allowed_accounts,omitempty"`
}

// Conversion helpers

func userToAPIUser(u *User) *APIUser {
	if u == nil {
		return nil
	}
	return &APIUser{
		ID:         u.ID,
		Email:      u.Email,
		Role:       u.Role,
		Groups:     u.GroupIDs,
		MFAEnabled: u.MFAEnabled,
		CreatedAt:  u.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  u.UpdatedAt.Format(time.RFC3339),
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
	return &APIGroup{
		ID:              g.ID,
		Name:            g.Name,
		Description:     g.Description,
		Permissions:     apiPerms,
		AllowedAccounts: g.AllowedAccounts,
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

// CreateUserAPI creates a new user via the API
func (s *Service) CreateUserAPI(ctx context.Context, reqInterface any) (any, error) {
	req, ok := reqInterface.(APICreateUserRequest)
	if !ok {
		return nil, fmt.Errorf("invalid request type")
	}
	authReq := CreateUserRequest{
		Email:    req.Email,
		Password: req.Password,
		Role:     req.Role,
		GroupIDs: req.Groups,
	}
	user, err := s.CreateUser(ctx, authReq)
	if err != nil {
		return nil, err
	}
	return userToAPIUser(user), nil
}

// UpdateUserAPI updates a user via the API
func (s *Service) UpdateUserAPI(ctx context.Context, userID string, reqInterface any) (any, error) {
	req, ok := reqInterface.(APIUpdateUserRequest)
	if !ok {
		return nil, fmt.Errorf("invalid request type")
	}
	authReq := UpdateUserRequest{
		GroupIDs: req.Groups,
	}
	if req.Role != "" {
		authReq.Role = &req.Role
	}
	user, err := s.UpdateUser(ctx, userID, authReq)
	if err != nil {
		return nil, err
	}
	return userToAPIUser(user), nil
}

// ListUsersAPI returns all users via the API
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

// ChangePasswordAPI changes a user's password via the API
func (s *Service) ChangePasswordAPI(ctx context.Context, userID, currentPassword, newPassword string) error {
	req := ChangePasswordRequest{
		CurrentPassword: currentPassword,
		NewPassword:     newPassword,
	}
	return s.ChangePassword(ctx, userID, req)
}

// CreateGroupAPI creates a new group via the API
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

// UpdateGroupAPI updates a group via the API
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

// GetGroupAPI returns a group by ID via the API
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

// ListGroupsAPI returns all groups via the API
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

// HasPermissionAPI checks if a user has a specific permission via the API
func (s *Service) HasPermissionAPI(ctx context.Context, userID, action, resource string) (bool, error) {
	return s.HasPermission(ctx, userID, action, resource, nil)
}
