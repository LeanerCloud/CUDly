package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateGroup creates a new permission group.
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

// UpdateGroup updates a permission group.
func (s *Service) UpdateGroup(ctx context.Context, group *Group) error {
	return s.store.UpdateGroup(ctx, group)
}

// DeleteGroup removes a permission group.
func (s *Service) DeleteGroup(ctx context.Context, groupID string) error {
	return s.store.DeleteGroup(ctx, groupID)
}

// GetGroup returns a group by ID.
func (s *Service) GetGroup(ctx context.Context, groupID string) (*Group, error) {
	return s.store.GetGroup(ctx, groupID)
}

// ListGroups returns all groups.
func (s *Service) ListGroups(ctx context.Context) ([]Group, error) {
	return s.store.ListGroups(ctx)
}

// GetUserPermissions returns all permissions for a user. Authorization is
// derived purely from the union of the user's groups' permissions: there is
// no role-based fallback. A user with no groups therefore has no permissions
// and is denied everything (fail closed).
//
// Any transient store error fetching a group is propagated immediately so
// callers fail closed with an error rather than silently receiving a partial
// permission set. A nil group (the store returns nil, nil for a deleted/
// missing group) is skipped without error.
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
			if errors.Is(err, pgx.ErrNoRows) {
				// Group was deleted; skip it rather than failing the entire request.
				continue
			}
			return nil, fmt.Errorf("fetching group %s: %w", groupID, err)
		}
		if group == nil {
			// Group was deleted; skip it rather than failing the entire request.
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

	if err := s.collectGroupsAndAccounts(ctx, authCtx, user.GroupIDs); err != nil {
		return nil, err
	}

	return authCtx, nil
}

func (s *Service) collectGroupsAndAccounts(ctx context.Context, authCtx *AuthContext, groupIDs []string) error {
	accountSet := make(map[string]bool)

	for _, groupID := range groupIDs {
		group, err := s.store.GetGroup(ctx, groupID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Group was deleted; skip it rather than failing the entire request.
				continue
			}
			return fmt.Errorf("fetching group %s: %w", groupID, err)
		}
		if group == nil {
			// Group was deleted; skip it rather than failing the entire request.
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
	return nil
}

// ResolveAllowedAccounts returns the cloud accounts a user may access, and
// FAILS CLOSED when that scope cannot be established (issue #1748).
//
// The subtlety this exists for: an empty result means UNRESTRICTED
// (IsUnrestrictedAccess), a deliberate backward-compat default so a group with
// no allowed_accounts configured grants full access. But
// collectGroupsAndAccounts also skips a group it cannot load -- pgx.ErrNoRows,
// or a store returning (nil, nil) -- so a user whose groups all fail to
// resolve produced the SAME empty value. Absence and unrestricted shared a
// representation, and every failure silently granted access to every account.
//
// Requiring at least one group to have actually resolved separates the two
// cleanly. Verified across all six cases: a group with allowed_accounts=["*"],
// a group with none configured (the legacy default), and a scoped group all
// resolve at least one group and keep working; a missing group, a (nil, nil)
// group, and a user with no groups at all resolve none and are refused.
//
// A user with no groups holds no permissions either, so refusing them here
// costs nothing and closes the same hole from the other side.
func (s *Service) ResolveAllowedAccounts(ctx context.Context, userID string) ([]string, error) {
	authCtx, err := s.BuildAuthContext(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(authCtx.Groups) == 0 {
		return nil, fmt.Errorf(
			"account scope could not be established for user %s: no group resolved", userID)
	}
	return authCtx.AllowedAccounts, nil
}

// GetAuthContext is an alias for BuildAuthContext for backward compatibility.
func (s *Service) GetAuthContext(ctx context.Context, userID string) (*AuthContext, error) {
	return s.BuildAuthContext(ctx, userID)
}

// HasPermission checks if a user has a specific permission.
func (s *Service) HasPermission(ctx context.Context, userID, action, resource string, constraints *PermissionConstraints) (bool, error) {
	permissions, err := s.GetUserPermissions(ctx, userID)
	if err != nil {
		return false, err
	}

	return s.permissionsAllow(permissions, action, resource, constraints), nil
}

// permissionsAllow reports whether any permission in the effective set grants
// action on resource under the given request-side constraints. Extracted from
// HasPermission so batch callers (HasPermissionForConstraintsAPI) can evaluate
// several constraint sets against a single permission fetch.
//
// Mirrors AuthContext.HasPermission's carve-out handling (types.go): the
// admin:* wildcard grants everything EXCEPT the money-spending verbs in
// adminCarvedOuts (separation of duties, issue #923). For those, admin falls
// through to the explicit-permission check below instead of short-circuiting.
func (s *Service) permissionsAllow(permissions []Permission, action, resource string, constraints *PermissionConstraints) bool {
	for _, perm := range permissions {
		if checkAdminPermission(perm) {
			if adminCarvedOuts[[2]string{action, resource}] {
				continue
			}
			return true
		}

		if !checkPermissionMatch(perm, action, resource) {
			continue
		}

		if !checkPermissionConstraints(s, perm, constraints) {
			continue
		}

		return true
	}

	return false
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

// matchConstraints checks if permission constraints match request constraints.
func (s *Service) matchConstraints(permConstraints, reqConstraints *PermissionConstraints) bool {
	return s.matchStringListConstraints(permConstraints.AccountIDs, reqConstraints.AccountIDs) &&
		s.matchStringListConstraints(permConstraints.Providers, reqConstraints.Providers) &&
		s.matchStringListConstraints(permConstraints.Services, reqConstraints.Services) &&
		s.matchAllRegionsConstraint(permConstraints.Regions, reqConstraints.Regions) &&
		s.matchPurchaseAmountConstraint(permConstraints.MaxPurchaseAmount, reqConstraints.MaxPurchaseAmount)
}

// matchStringListConstraints checks if two string lists have any overlap.
//
// Semantics (03-L6): if either list is empty the constraint is treated as
// satisfied ("no constraint specified = no restriction"). Concretely:
//   - empty permList: the permission has no constraint on this dimension
//   - empty reqList: the request does not specify this dimension
//
// Both cases return true (match). Only when both lists are non-empty is
// containsAny used to require at least one common element.
//
// Callers that need "a constrained permission must only match an explicit
// request value" should verify reqList is non-empty before calling, or add
// a separate dimension-specific check.
func (s *Service) matchStringListConstraints(permList, reqList []string) bool {
	if len(permList) > 0 && len(reqList) > 0 {
		return containsAny(permList, reqList)
	}
	return true
}

// matchAllRegionsConstraint is the Regions dimension's matcher: EVERY
// requested region must be permitted, not merely one of them.
//
// Regions is the one dimension where a single request legitimately spans
// several values: an Azure RI exchange takes a list of targets, and
// handler_ri_exchange.go's targetLocations feeds all of their locations in
// at once. Under the generic containsAny rule that made the region
// constraint bypassable -- a caller permitted only in eastus could attach a
// westus target, containsAny would find eastus in the permitted set, return
// true, and the exchange would execute for BOTH regions. "Regions limits to
// specific regions" (types.go) cannot mean "limits to requests that mention
// at least one permitted region".
//
// Every other dimension keeps containsAny: a request names one provider,
// one service, one account, so for them any-match and all-match coincide.
//
// Comparison is case-insensitive so a permission stored as "EastUS" still
// matches the canonical lower-case form callers are normalized to; the
// empty-list semantics are unchanged from matchStringListConstraints (an
// unconstrained permission, or a request that does not name a region, still
// matches).
func (s *Service) matchAllRegionsConstraint(permRegions, reqRegions []string) bool {
	if len(permRegions) == 0 || len(reqRegions) == 0 {
		return true
	}
	permitted := make(map[string]bool, len(permRegions))
	for _, r := range permRegions {
		permitted[strings.ToLower(strings.TrimSpace(r))] = true
	}
	for _, r := range reqRegions {
		if !permitted[strings.ToLower(strings.TrimSpace(r))] {
			return false
		}
	}
	return true
}

// matchPurchaseAmountConstraint checks if requested amount is within permitted limit.
func (s *Service) matchPurchaseAmountConstraint(permMax, reqMax float64) bool {
	if permMax > 0 && reqMax > permMax {
		return false
	}
	return true
}
