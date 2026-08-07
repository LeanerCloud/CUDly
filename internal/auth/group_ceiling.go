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
// never drop the restriction. Values are trimmed and lower-cased, matching
// matchAllRegionsConstraint's comparison.
func listCovers(held, req []string) bool {
	if len(held) == 0 {
		return true
	}
	if len(req) == 0 {
		return false
	}
	permitted := make(map[string]bool, len(held))
	for _, v := range held {
		permitted[normalizeConstraintValue(v)] = true
	}
	for _, v := range req {
		if !permitted[normalizeConstraintValue(v)] {
			return false
		}
	}
	return true
}

func normalizeConstraintValue(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
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
