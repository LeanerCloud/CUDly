package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateGroup creates a new permission group
func (s *Service) CreateGroup(ctx context.Context, group *Group, createdBy string) error {
	now := time.Now()
	// Generate a plain UUID. The previous "group-<uuid>" prefix produced
	// an invalid UUID string that failed the Postgres UUID column insert
	// with an "invalid input syntax for type uuid" error, which surfaced
	// as a 500 on every POST /api/groups call.
	group.ID = uuid.New().String()
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

// GetUserPermissions returns all permissions for a user. Authorization is
// derived purely from the union of the user's groups' permissions: there is
// no role-based fallback. A user with no groups therefore has no permissions
// and is denied everything (fail closed).
func (s *Service) GetUserPermissions(ctx context.Context, userID string) ([]Permission, error) {
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

	var permissions []Permission

	// Permissions come exclusively from group memberships.
	for _, groupID := range user.GroupIDs {
		group, err := s.store.GetGroup(ctx, groupID)
		if err != nil {
			logging.Warnf("Failed to fetch group %s: %v", groupID, err)
			continue
		}
		if group == nil {
			continue
		}
		permissions = append(permissions, group.Permissions...)
	}

	return permissions, nil
}

// BuildAuthContext builds a complete authorization context for a user.
// Permissions and allowed accounts are derived purely from the union of the
// user's group memberships; a user with no groups gets an empty context and
// is denied everything (fail closed).
func (s *Service) BuildAuthContext(ctx context.Context, userID string) (*AuthContext, error) {
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

	authCtx := &AuthContext{
		User:            user,
		Groups:          make([]*Group, 0),
		AllowedAccounts: make([]string, 0),
		Permissions:     make([]Permission, 0),
	}

	s.collectGroupsAndAccounts(ctx, authCtx, user.GroupIDs)

	return authCtx, nil
}

func (s *Service) collectGroupsAndAccounts(ctx context.Context, authCtx *AuthContext, groupIDs []string) {
	accountSet := make(map[string]bool)

	for _, groupID := range groupIDs {
		group, err := s.store.GetGroup(ctx, groupID)
		if err != nil {
			logging.Warnf("Failed to fetch group %s: %v", groupID, err)
			continue
		}
		if group == nil {
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

// UserHasAdminCapability reports whether the user's effective (group-derived)
// permissions include the full-access {admin, *} capability, i.e. the user is
// a member of the Administrators group (or any group granted equivalent
// permission). This is the group-membership replacement for the old
// role == "admin" short-circuit. Fail closed: any lookup error returns
// (false, err) and callers must deny.
func (s *Service) UserHasAdminCapability(ctx context.Context, userID string) (bool, error) {
	perms, err := s.GetUserPermissions(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, perm := range perms {
		if checkAdminPermission(perm) {
			return true, nil
		}
	}
	return false, nil
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
