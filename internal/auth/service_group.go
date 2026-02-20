package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CreateGroup creates a new permission group
func (s *Service) CreateGroup(ctx context.Context, group *Group, createdBy string) error {
	now := time.Now()
	group.ID = "group-" + uuid.New().String()
	group.CreatedAt = now
	group.UpdatedAt = now
	group.CreatedBy = createdBy

	return s.store.CreateGroup(ctx, group)
}

// UpdateGroup updates a permission group
func (s *Service) UpdateGroup(ctx context.Context, group *Group) error {
	return s.store.UpdateGroup(ctx, group)
}

// DeleteGroup removes a permission group
func (s *Service) DeleteGroup(ctx context.Context, groupID string) error {
	return s.store.DeleteGroup(ctx, groupID)
}

// GetGroup returns a group by ID
func (s *Service) GetGroup(ctx context.Context, groupID string) (*Group, error) {
	return s.store.GetGroup(ctx, groupID)
}

// ListGroups returns all groups
func (s *Service) ListGroups(ctx context.Context) ([]Group, error) {
	return s.store.ListGroups(ctx)
}

// GetUserPermissions returns all permissions for a user (from role + groups)
func (s *Service) GetUserPermissions(ctx context.Context, userID string) ([]Permission, error) {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}

	var permissions []Permission

	// Add role-based permissions
	switch user.Role {
	case RoleAdmin:
		permissions = append(permissions, DefaultAdminPermissions()...)
	case RoleUser:
		permissions = append(permissions, DefaultUserPermissions()...)
	case RoleReadOnly:
		permissions = append(permissions, DefaultReadOnlyPermissions()...)
	}

	// Add group permissions
	for _, groupID := range user.GroupIDs {
		group, err := s.store.GetGroup(ctx, groupID)
		if err != nil || group == nil {
			continue
		}
		permissions = append(permissions, group.Permissions...)
	}

	return permissions, nil
}

// BuildAuthContext builds a complete authorization context for a user
// This includes permissions and allowed accounts from the user's role and groups
func (s *Service) BuildAuthContext(ctx context.Context, userID string) (*AuthContext, error) {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}

	authCtx := &AuthContext{
		User:            user,
		Groups:          make([]*Group, 0),
		AllowedAccounts: make([]string, 0),
		Permissions:     make([]Permission, 0),
	}

	addRolePermissions(authCtx, user.Role)
	s.collectGroupsAndAccounts(ctx, authCtx, user.GroupIDs)

	return authCtx, nil
}

func addRolePermissions(authCtx *AuthContext, role string) {
	switch role {
	case RoleAdmin:
		authCtx.Permissions = append(authCtx.Permissions, DefaultAdminPermissions()...)
	case RoleUser:
		authCtx.Permissions = append(authCtx.Permissions, DefaultUserPermissions()...)
	case RoleReadOnly:
		authCtx.Permissions = append(authCtx.Permissions, DefaultReadOnlyPermissions()...)
	}
}

func (s *Service) collectGroupsAndAccounts(ctx context.Context, authCtx *AuthContext, groupIDs []string) {
	accountSet := make(map[string]bool)

	for _, groupID := range groupIDs {
		group, err := s.store.GetGroup(ctx, groupID)
		if err != nil || group == nil {
			continue
		}

		authCtx.Groups = append(authCtx.Groups, group)
		authCtx.Permissions = append(authCtx.Permissions, group.Permissions...)

		for _, accountID := range group.AllowedAccounts {
			accountSet[accountID] = true
		}
	}

	for accountID := range accountSet {
		authCtx.AllowedAccounts = append(authCtx.AllowedAccounts, accountID)
	}
}

// GetAuthContext is an alias for BuildAuthContext for backward compatibility
func (s *Service) GetAuthContext(ctx context.Context, userID string) (*AuthContext, error) {
	return s.BuildAuthContext(ctx, userID)
}

// HasPermission checks if a user has a specific permission
func (s *Service) HasPermission(ctx context.Context, userID, action, resource string, constraints *PermissionConstraints) (bool, error) {
	permissions, err := s.GetUserPermissions(ctx, userID)
	if err != nil {
		return false, err
	}

	for _, perm := range permissions {
		if checkAdminPermission(perm) {
			return true, nil
		}

		if !checkPermissionMatch(perm, action, resource) {
			continue
		}

		if !checkPermissionConstraints(s, perm, constraints) {
			continue
		}

		return true, nil
	}

	return false, nil
}

func checkAdminPermission(perm Permission) bool {
	return perm.Action == ActionAdmin && perm.Resource == ResourceAll
}

func checkPermissionMatch(perm Permission, action, resource string) bool {
	if perm.Action != action {
		return false
	}
	if perm.Resource != resource && perm.Resource != ResourceAll {
		return false
	}
	return true
}

func checkPermissionConstraints(s *Service, perm Permission, constraints *PermissionConstraints) bool {
	if constraints != nil && perm.Constraints != nil {
		return s.matchConstraints(perm.Constraints, constraints)
	}
	return true
}

// matchConstraints checks if permission constraints match request constraints
func (s *Service) matchConstraints(permConstraints, reqConstraints *PermissionConstraints) bool {
	return s.matchStringListConstraints(permConstraints.AccountIDs, reqConstraints.AccountIDs) &&
		s.matchStringListConstraints(permConstraints.Providers, reqConstraints.Providers) &&
		s.matchStringListConstraints(permConstraints.Services, reqConstraints.Services) &&
		s.matchStringListConstraints(permConstraints.Regions, reqConstraints.Regions) &&
		s.matchPurchaseAmountConstraint(permConstraints.MaxPurchaseAmount, reqConstraints.MaxPurchaseAmount)
}

// matchStringListConstraints checks if two string lists have any overlap
func (s *Service) matchStringListConstraints(permList, reqList []string) bool {
	if len(permList) > 0 && len(reqList) > 0 {
		return containsAny(permList, reqList)
	}
	return true
}

// matchPurchaseAmountConstraint checks if requested amount is within permitted limit
func (s *Service) matchPurchaseAmountConstraint(permMax, reqMax float64) bool {
	if permMax > 0 && reqMax > permMax {
		return false
	}
	return true
}
