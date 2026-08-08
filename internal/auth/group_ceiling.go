package auth

import (
	"context"
	"fmt"
	"strings"
)

// Grant ceiling for group-permission writes (issues #1550, #1629).
//
// Before this, CreateGroupAPI / UpdateGroupAPI wrote the client-supplied
// permission list onto a group verbatim. Because update:groups is NOT one of
// the pairs carved out of the admin:* wildcard, any admin could, in a single
// request, write execute:purchases / approve-any:purchases /
// retry-any:purchases onto their own group and void the #923 money
// separation-of-duties control tenant-wide.
//
// Two rules close that, and both are enforced here:
//
//  1. Ceiling: a caller may only grant permissions their own effective set
//     already holds, at constraints no broader than their own.
//  2. Non-grantable: the money verbs in adminCarvedOuts may never be ADDED to
//     a group at all, whoever the caller is. This second rule is load-bearing
//     rather than belt-and-braces: migrations 000059/000064 backfill every
//     Administrators member into the Purchaser group, so in a default
//     deployment a typical admin DOES explicitly hold the money verbs and
//     rule 1 alone would let them relay those verbs onto the Administrators
//     group. The only group granting these verbs today is seeded by SQL and
//     is system-managed, so nothing legitimate needs the API to grant them.

// AdminAPIKeyActorID is the sentinel actor identifier carried by the
// stateless admin API-key session. The key is an infrastructure credential
// with no backing user row, so a group-derived permission lookup cannot
// resolve it. The ceiling treats it as holding exactly {admin, *} -- what it
// is authorized as everywhere else -- which subjects it to the same
// carve-out as a human admin: it can seed ordinary groups but cannot grant
// the money-spending verbs (issue #1550's third vector).
//
// internal/api's apiKeyAdminUserID aliases this constant; keep them equal.
const AdminAPIKeyActorID = "admin-api-key"

// grantCeilingPermissions returns the permission set a group write is
// measured against. Fails closed: an unidentified actor, or any error
// resolving the actor's groups, refuses the write rather than falling
// through to "allow".
func (s *Service) grantCeilingPermissions(ctx context.Context, actorUserID string) ([]Permission, error) {
	if actorUserID == "" {
		return nil, fmt.Errorf("%w: the acting user could not be identified", ErrPermissionCeiling)
	}
	if actorUserID == AdminAPIKeyActorID {
		return []Permission{{Action: ActionAdmin, Resource: ResourceAll}}, nil
	}
	perms, err := s.GetUserPermissions(ctx, actorUserID)
	if err != nil {
		return nil, fmt.Errorf("%w: could not resolve the acting user's permissions: %w", ErrPermissionCeiling, err)
	}
	return perms, nil
}

// checkGrantCeiling validates a requested group-permission list against the
// two rules above.
//
// existing is the target group's CURRENT permission list (nil on create). A
// carved-out permission already on the group may be carried through an
// unrelated edit -- a rename must not be forced to strip it -- but only at
// constraints no broader than the ones already stored, so an edit cannot
// raise an existing MaxPurchaseAmount either.
//
// A refusal names the offending permission and returns an error: the list is
// never silently narrowed to the subset that would have been allowed, which
// is exactly the silent-corruption failure mode #1629 reports on the
// frontend side.
func (s *Service) checkGrantCeiling(ctx context.Context, actorUserID string, requested, existing []Permission) error {
	if len(requested) == 0 {
		return nil
	}
	if err := validateRequestedPermissions(requested); err != nil {
		return err
	}
	actorPerms, err := s.grantCeilingPermissions(ctx, actorUserID)
	if err != nil {
		return err
	}
	for _, req := range requested {
		if adminCarvedOuts[[2]string{req.Action, req.Resource}] {
			if permissionCoveredBy(existing, req) {
				continue
			}
			return fmt.Errorf(
				"%w: %s:%s is reserved for separation of duties (issue #923) and cannot be granted through the API",
				ErrPermissionNotGrantable, req.Action, req.Resource)
		}
		if !grantCeilingAllows(actorPerms, req) {
			return fmt.Errorf(
				"%w: cannot grant %s:%s because your own permissions do not include it (or not at the requested scope)",
				ErrPermissionCeiling, req.Action, req.Resource)
		}
	}
	return nil
}

// validateRequestedPermissions rejects malformed entries before the ceiling
// runs (issue #1730).
//
// This is NOT redundant with the ceiling. The ceiling's admin:* branch grants
// any (action, resource) pair that is not carved out, and ("view", "") is not
// carved out, so before this check an admin could write a blank resource
// straight through. It then round-trips as the "*" WILDCARD: the group-edit
// form picks its `<option>` with `isDefault = !currentValue && resource ===
// '*'`, so an empty stored resource renders as the selected "All (*)" entry
// and saves back as view:*. Nothing validated the list on the way in, so the
// same widening is reachable from any API client with no form involved.
//
// The two blank fields fail differently in the form -- a blank action is
// silently DROPPED (its index 0 is an empty placeholder) while a blank
// resource is silently WIDENED (its index 0 is the wildcard) -- which is why
// the defect is in the defaulting rather than the parsing. Only the resource
// side escalates; both are refused here, because a silently dropped
// permission is a different bug rather than an acceptable one.
//
// Blank means empty or whitespace-only. Unknown-but-non-blank values are
// deliberately NOT rejected: vocabulary validation is a separate concern
// (#1629) and rejecting values this endpoint can legitimately already have
// stored would break edits of existing groups.
func validateRequestedPermissions(requested []Permission) error {
	for i, p := range requested {
		if strings.TrimSpace(p.Action) == "" {
			return fmt.Errorf(
				"%w: entry %d has a blank action (resource %q); a blank action is malformed input, not a permission to drop",
				ErrInvalidPermission, i, p.Resource)
		}
		if strings.TrimSpace(p.Resource) == "" {
			return fmt.Errorf(
				"%w: entry %d has a blank resource (action %q); a blank resource is malformed input, not a request for the %q wildcard",
				ErrInvalidPermission, i, p.Action, ResourceAll)
		}
	}
	return nil
}

// grantCeilingAccounts returns the account scope a group write is measured
// against: the union of the acting principal's groups' AllowedAccounts.
// Fails closed on an unidentified actor or a resolution error.
//
// The stateless admin API key has no user row and is unrestricted everywhere
// else (see getAccountScope in internal/api), so it is unrestricted here.
func (s *Service) grantCeilingAccounts(ctx context.Context, actorUserID string) ([]string, error) {
	if actorUserID == "" {
		return nil, fmt.Errorf("%w: the acting user could not be identified", ErrPermissionCeiling)
	}
	if actorUserID == AdminAPIKeyActorID {
		return nil, nil
	}
	authCtx, err := s.BuildAuthContext(ctx, actorUserID)
	if err != nil {
		return nil, fmt.Errorf("%w: could not resolve the acting user's account scope: %w", ErrPermissionCeiling, err)
	}
	// A PARTIAL resolution can WIDEN the actor's scope, which is the case that
	// matters and the one an earlier version of this guard missed.
	//
	// AllowedAccounts is a union in which the empty set means EVERYTHING, so
	// dropping a contributing group does not narrow it. The union of [] and
	// ["acct-A"] is restricted; lose the group carrying ["acct-A"] and it
	// collapses to [] = unrestricted, and the actor can then widen any group
	// to ["*"]. Verified by execution against the write path: baseline
	// REFUSED, one skipped group ACCEPTED.
	//
	// The test is len(AllowedAccounts) == 0, NOT IsUnrestrictedAccess. A union
	// containing "*" was already maximally wide at baseline, so no loss can
	// widen it; refusing it would be zero security benefit and pure
	// availability cost, and all seven seeded groups ship ARRAY['*'].
	if len(authCtx.AllowedAccounts) == 0 && authCtx.SkippedGroups > 0 {
		return nil, fmt.Errorf(
			"%w: %d group(s) could not be resolved and the acting user's remaining scope is unrestricted",
			ErrPermissionCeiling, authCtx.SkippedGroups)
	}
	// An unresolved scope is UNKNOWN, not unrestricted.
	//
	// collectGroupsAndAccounts skips a missing or deleted group silently
	// (pgx.ErrNoRows, or a nil group), so an actor whose groups all fail to
	// load yields an EMPTY account list -- which IsUnrestrictedAccess reads as
	// "all accounts". That turns this ceiling into a no-op on exactly the
	// path it guards, so require that at least one group actually resolved.
	//
	// Note the asymmetry this closes: the permission ceiling already fails
	// closed on the same input, because an empty permission set grants
	// nothing. Without this the account ceiling failed OPEN on it.
	if len(authCtx.Groups) == 0 {
		return nil, fmt.Errorf(
			"%w: the acting user's account scope could not be established (no group resolved)",
			ErrPermissionCeiling)
	}
	return authCtx.AllowedAccounts, nil
}

// checkAccountCeiling bounds the OTHER account dimension of a group write.
//
// This is deliberately a separate call from checkGrantCeiling rather than a
// branch inside it. checkGrantCeiling returns early when no permissions are
// sent, and APIUpdateGroupRequest's "empty means not sent" contract makes an
// allowed_accounts-only PUT the natural shape -- so folding this in would
// leave exactly the request that widens account scope unchecked. That was the
// live gap: widening AllowedAccounts to more accounts, to [] or to ["*"] was
// accepted, while widening Permissions[].Constraints.AccountIDs on the SAME
// call was correctly refused.
//
// requested == nil means "not sent" and is left alone. A write is in ceiling
// if it does not widen the group's existing scope, or if it stays within the
// acting principal's own scope.
func (s *Service) checkAccountCeiling(ctx context.Context, actorUserID string, requested, existing []string) error {
	if requested == nil {
		return nil
	}
	// Not a widening of what the group already had -- safe whoever the actor
	// is. Only reachable on update: a new group has no prior scope, and nil
	// existing would read as UNRESTRICTED here and swallow every check, which
	// is why CreateGroupAPI calls checkAccountGrant directly instead.
	if accountScopeGap(existing, requested) == "" {
		return nil
	}
	return s.checkAccountGrant(ctx, actorUserID, requested)
}

// checkAccountGrant requires requested to sit inside the acting principal's
// own account scope. This is the create-path entry point, where there is no
// prior scope to compare against, so every value is a grant.
//
// A nil requested is checked too, and deliberately: on create, omitting
// allowed_accounts produces an UNRESTRICTED group (IsUnrestrictedAccess reads
// empty as "all accounts"), so for a scoped actor that is the widest possible
// grant rather than a no-op.
func (s *Service) checkAccountGrant(ctx context.Context, actorUserID string, requested []string) error {
	actorAccounts, err := s.grantCeilingAccounts(ctx, actorUserID)
	if err != nil {
		return err
	}
	if gap := accountScopeGap(actorAccounts, requested); gap != "" {
		return fmt.Errorf(
			"%w: cannot grant %s because your own account scope does not include it",
			ErrPermissionCeiling, gap)
	}
	return nil
}

// accountScopeGap returns a description of the first way requested reaches
// beyond outer, or "" when outer covers it entirely.
//
// Empty and "*" both mean UNRESTRICTED on either side (IsUnrestrictedAccess),
// which is why this cannot be a plain subset test: the empty set is a subset
// of everything but means the opposite of narrow. An unrestricted request
// against a restricted holder is the widening this exists to catch.
//
// Comparison is EXACT -- no trimming, no case folding -- because that is what
// MatchesAccount does at enforcement (`a == accountID`). Normalising here
// would make the ceiling MORE permissive than enforcement: an actor whose
// stored scope is " acct-A" matches nothing when access is actually checked,
// yet a trimming ceiling would treat them as holding "acct-A" and let them
// grant it.
//
// The rule to preserve: a ceiling may be STRICTER than enforcement (a false
// refusal fails closed and is visible), never looser.
func accountScopeGap(outer, requested []string) string {
	if IsUnrestrictedAccess(outer) {
		return ""
	}
	if IsUnrestrictedAccess(requested) {
		return "unrestricted access to all cloud accounts"
	}
	permitted := make(map[string]bool, len(outer))
	for _, a := range outer {
		permitted[a] = true
	}
	for _, a := range requested {
		if !permitted[a] {
			return fmt.Sprintf("access to cloud account %q", a)
		}
	}
	return ""
}

// grantCeilingAllows reports whether actorPerms holds req in full. Action and
// resource matching mirrors permissionsAllow exactly, including the admin:*
// carve-out, so the ceiling can never be looser than enforcement. It adds one
// requirement enforcement does not need: a constrained holder cannot hand out
// an unconstrained (or differently scoped) copy of their own permission.
func grantCeilingAllows(actorPerms []Permission, req Permission) bool {
	for _, held := range actorPerms {
		if checkAdminPermission(held) {
			// admin:* carries no constraints, so it covers any requested
			// constraint set. Carved-out pairs never reach here (the caller
			// rejects them first), but mirror permissionsAllow anyway so the
			// two stay in lockstep if the carve-out set grows (#1644).
			if adminCarvedOuts[[2]string{req.Action, req.Resource}] {
				continue
			}
			return true
		}
		if !checkPermissionMatch(held, req.Action, req.Resource) {
			continue
		}
		if !constraintsCover(held.Constraints, req.Constraints) {
			continue
		}
		return true
	}
	return false
}

// permissionCoveredBy reports whether set already contains a permission with
// req's exact action and resource, at constraints no narrower than req's. It
// distinguishes "this write KEEPS what the group already had" from "this
// write grants or widens it".
func permissionCoveredBy(set []Permission, req Permission) bool {
	for _, p := range set {
		if p.Action != req.Action || p.Resource != req.Resource {
			continue
		}
		if constraintsCover(p.Constraints, req.Constraints) {
			return true
		}
	}
	return false
}

// constraintsCover reports whether a permission constrained by held is broad
// enough to cover a grant constrained by req.
//
// Mirrors the enforcement-time semantics in matchConstraints: an empty list
// or a zero MaxPurchaseAmount means "no restriction on this dimension". So an
// unconstrained holder covers anything, while a constrained one requires the
// grant to name a subset of the same values and a cap no higher than its own.
// A nil req against a constrained held is a widening and is refused.
func constraintsCover(held, req *PermissionConstraints) bool {
	if held == nil {
		return true
	}
	if req == nil {
		req = &PermissionConstraints{}
	}
	return listCovers(held.AccountIDs, req.AccountIDs) &&
		listCovers(held.Providers, req.Providers) &&
		listCovers(held.Services, req.Services) &&
		listCovers(held.Regions, req.Regions) &&
		amountCovers(held.MaxPurchaseAmount, req.MaxPurchaseAmount)
}

// listCovers reports whether every value in req is permitted by held. An
// empty held list is "no restriction on this dimension" and covers anything;
// a non-empty held list requires req to be a NON-EMPTY subset, so a grant can
// never drop the restriction.
//
// Comparison is EXACT for every dimension. An earlier version normalised
// (trim + lower-case) and justified it as "matching matchAllRegionsConstraint"
// -- true for Regions only. AccountIDs, Providers and Services are enforced by
// containsAny, an exact case-sensitive lookup, so normalising made the ceiling
// LOOSER than enforcement there: a holder constrained to ["ACCT-1"] could
// grant ["acct-1"], a value they cannot themselves use.
//
// Being exact on Regions too is stricter than its enforcement, not looser, so
// it fails closed. A ceiling may exceed enforcement in strictness; it must
// never fall short.
func listCovers(held, req []string) bool {
	if len(held) == 0 {
		return true
	}
	if len(req) == 0 {
		return false
	}
	permitted := make(map[string]bool, len(held))
	for _, v := range held {
		permitted[v] = true
	}
	for _, v := range req {
		if !permitted[v] {
			return false
		}
	}
	return true
}

// amountCovers reports whether a grant capped at reqMax stays within a holder
// capped at heldMax. Zero means "no cap" on both sides (mirroring
// matchPurchaseAmountConstraint), so a capped holder cannot hand out an
// uncapped grant.
func amountCovers(heldMax, reqMax float64) bool {
	if heldMax <= 0 {
		return true
	}
	return reqMax > 0 && reqMax <= heldMax
}
