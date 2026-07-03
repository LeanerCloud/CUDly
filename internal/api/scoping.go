package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
)

// requireAccountAccess fetches the account by ID, then verifies the session's
// allowed_accounts list grants access via auth.MatchesAccount. Returns the
// fetched account on success so callers can avoid a second GetCloudAccount
// call.
//
// Returns errNotFound when either the account does not exist OR the user is
// restricted to a subset of accounts and the target is outside their scope.
// The 404-not-403 choice is deliberate: a user with view:accounts permission
// who can see account A shouldn't be able to infer that account B exists by
// probing a single-account endpoint. IDOR-via-enumeration is cheap with
// UUIDv4, but logs, URLs, and copy-paste still leak IDs.
//
// requirePermission must be called before this helper so the verb gate fires
// first; callers pass the session it returned.
func (h *Handler) requireAccountAccess(ctx context.Context, session *Session, accountID string) (*config.CloudAccount, error) {
	account, err := h.config.GetCloudAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}
	if account == nil {
		return nil, errNotFound
	}

	allowed, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if auth.IsUnrestrictedAccess(allowed) {
		return account, nil
	}
	if !auth.MatchesAccount(allowed, account.ID, account.Name) {
		return nil, errNotFound
	}
	return account, nil
}

// canAccessAccountID is the lightweight, no-DB variant of requireAccountAccess
// for callers that already have the account's ID AND name (e.g. when iterating
// a list that was already fetched). Returns true when the session is
// unrestricted or the allowed_accounts list matches.
func (h *Handler) canAccessAccountID(ctx context.Context, session *Session, accountID, accountName string) (bool, error) {
	allowed, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return false, fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if auth.IsUnrestrictedAccess(allowed) {
		return true, nil
	}
	return auth.MatchesAccount(allowed, accountID, accountName), nil
}

// requirePlanAccess fetches the plan's associated accounts and rejects with
// errNotFound when the session's allowed_accounts list doesn't intersect
// with any of them. Admin / unrestricted sessions pass through unchanged.
// Plans with no account assignments are hidden from scoped users — the safe
// default when we can't attribute the plan to a specific account.
//
// requirePermission must fire first; the session it returns is what the
// caller passes here. This is the plan-level analogue of requireAccountAccess
// and is used by the plans/purchases/ri-exchange per-record scoping.
func (h *Handler) requirePlanAccess(ctx context.Context, session *Session, planID string) error {
	allowed, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if auth.IsUnrestrictedAccess(allowed) {
		return nil
	}
	accounts, err := h.config.GetPlanAccounts(ctx, planID)
	if err != nil {
		return fmt.Errorf("failed to get plan accounts: %w", err)
	}
	for _, acct := range accounts {
		if auth.MatchesAccount(allowed, acct.ID, acct.Name) {
			return nil
		}
	}
	return errNotFound
}

// validatePurchaseRecommendationScope returns a 400 client error when any
// recommendation in the batch targets an account the session can't access.
// Admin / unrestricted sessions pass through. Recommendations with a nil
// CloudAccountID are rejected when the session is scoped — we can't
// attribute them, and silently letting them through would bypass the filter.
func (h *Handler) validatePurchaseRecommendationScope(ctx context.Context, session *Session, recs []config.RecommendationRecord) error {
	allowed, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if auth.IsUnrestrictedAccess(allowed) {
		return nil
	}
	nameByID := h.resolveAccountNamesByID(ctx)
	for i, rec := range recs {
		if rec.CloudAccountID == nil {
			return NewClientError(400, fmt.Sprintf("recommendation %d has no cloud_account_id; scoped users cannot execute unattributed recommendations", i))
		}
		id := *rec.CloudAccountID
		if !auth.MatchesAccount(allowed, id, nameByID[id]) {
			return NewClientError(403, fmt.Sprintf("recommendation %d targets account %s which is outside your allowed_accounts", i, id))
		}
	}
	return nil
}

// requireExecutionAccess rejects with errNotFound when the execution's plan's
// associated accounts don't intersect with the session's allowed_accounts.
// Convenience wrapper around requirePlanAccess for the pause/resume/run/
// delete/details handlers that key on executionID. Returns errNotFound when
// the execution itself doesn't exist, so unauthenticated probing can't
// distinguish "no such execution" from "you can't see it".
//
// Short-circuits for admin / unrestricted sessions BEFORE the
// GetExecutionByID fetch to keep those happy-paths free of the extra store
// round-trip (and to keep existing unit-test fixtures for admin operations
// working without adding execution mocks).
func (h *Handler) requireExecutionAccess(ctx context.Context, session *Session, executionID string) error {
	allowed, err := h.getAllowedAccounts(ctx, session)
	if err != nil {
		return fmt.Errorf("failed to get allowed accounts: %w", err)
	}
	if auth.IsUnrestrictedAccess(allowed) {
		return nil
	}
	execution, err := h.config.GetExecutionByID(ctx, executionID)
	if errors.Is(err, config.ErrNotFound) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to get execution: %w", err)
	}
	return h.requirePlanAccess(ctx, session, execution.PlanID)
}

// resolveAccountFilterIDs maps requested cloud_accounts UUIDs to the
// (uuid, provider, external_id) data needed for the dual-column purchase-history
// filter. purchase_history rows carry either the UUID FK (cloud_account_id) or
// the cloud-provider external number (account_id) and frequently only one of
// them, so a UUID-only WHERE silently drops the external-only rows. This
// resolver loads cloud_accounts once (reusing ListCloudAccounts, the same source
// as resolveAccountNamesByID), then returns the UUIDs unchanged plus the matched
// accounts' external ids grouped by provider.
//
// The external ids are grouped by provider so the downstream predicate compares
// each external number only against rows of its own provider
// ((provider = p AND account_id = ANY(...))). Without this, an external number
// reused across providers (aws/123 vs azure/123) would leak the wrong rows when
// the filter omits provider.
//
// Only UUIDs that resolve to a known cloud_accounts row contribute an external
// id, so a caller cannot inject an arbitrary external id. Unknown UUIDs are
// still returned in the uuid set so the cloud_account_id half of the predicate
// matches any rows that happen to carry them.
//
// Returns (uuids, nil) when uuids is empty or the account load fails — the
// dual-column predicate then degrades to UUID-only matching, no worse than the
// pre-fix behaviour.
func (h *Handler) resolveAccountFilterIDs(ctx context.Context, uuids []string) (resolvedUUIDs []string, externalIDsByProvider map[string][]string) {
	if len(uuids) == 0 {
		return uuids, nil
	}
	accounts, err := h.config.ListCloudAccounts(ctx, config.CloudAccountFilter{})
	if err != nil {
		return uuids, nil
	}
	type provExt struct{ provider, externalID string }
	byUUID := make(map[string]provExt, len(accounts))
	for _, a := range accounts {
		if a.ExternalID != "" {
			byUUID[a.ID] = provExt{provider: a.Provider, externalID: a.ExternalID}
		}
	}
	for _, u := range uuids {
		pe, ok := byUUID[u]
		if !ok {
			continue
		}
		externalIDsByProvider = addExternalIDForProvider(externalIDsByProvider, pe.provider, pe.externalID)
	}
	return uuids, externalIDsByProvider
}

// addExternalIDForProvider appends externalID to the provider's slice in m,
// allocating m and the slice lazily and skipping duplicates so each provider's
// ANY() bind has no repeated values. Returns the (possibly newly-allocated) map.
func addExternalIDForProvider(m map[string][]string, provider, externalID string) map[string][]string {
	if m == nil {
		m = make(map[string][]string)
	}
	for _, e := range m[provider] {
		if e == externalID {
			return m
		}
	}
	m[provider] = append(m[provider], externalID)
	return m
}

// resolveSingleAccountFilterIDs resolves a single account identifier (the
// legacy singular `account_id` param, which the frontend populates with the
// top-bar chip's cloud_accounts UUID) into the dual-column filter inputs:
//
//   - Known account UUID: returned in the uuid set, plus its external id (if
//     the account has one) grouped under the account's provider. Both columns
//     are then matched, with the external half scoped to that provider.
//   - Unknown value: treated as an opaque external account number (the pre-UUID
//     call shape, e.g. a raw AWS account number) so legacy single-account
//     callers keep working; it is NOT placed in the uuid set so a raw external
//     number is never compared against cloud_account_id UUIDs. Its provider is
//     unknown, so it is grouped under the "" key, which the predicate treats as
//     an unconstrained-provider match (legacy behaviour preserved).
//
// Empty input returns nil maps (no account filter). A cloud_accounts load
// failure falls back to treating the value as an external id (no worse than the
// pre-fix behaviour); per-record allowed_accounts scoping still applies
// downstream.
func (h *Handler) resolveSingleAccountFilterIDs(ctx context.Context, accountID string) (uuids []string, externalIDsByProvider map[string][]string) {
	if accountID == "" {
		return nil, nil
	}
	accounts, err := h.config.ListCloudAccounts(ctx, config.CloudAccountFilter{})
	if err != nil {
		return nil, map[string][]string{"": {accountID}}
	}
	for _, a := range accounts {
		if a.ID == accountID {
			// Known UUID: match cloud_account_id by UUID and, when present,
			// account_id by the resolved external number scoped to its provider.
			if a.ExternalID != "" {
				return []string{accountID}, map[string][]string{a.Provider: {a.ExternalID}}
			}
			return []string{accountID}, nil
		}
	}
	// Not a known UUID: treat the value as an external account number with an
	// unknown provider ("" key = unconstrained provider match).
	return nil, map[string][]string{"": {accountID}}
}

// a map from account identifier → display name. The map is keyed by BOTH
// the internal UUID (CloudAccount.ID) and the cloud-provider external ID
// (CloudAccount.ExternalID, e.g. an AWS account number or Azure subscription
// ID). Dual-keying is necessary because different record types use different
// identifiers: recommendation records carry the UUID (cloud_account_id FK)
// while legacy purchase_history rows carry the external ID (account_id
// VARCHAR). Without both keys, the name lookup always misses for one path.
//
// Returns an empty map on error; callers fall through to ID-only matching,
// which is still safe (MatchesAccount falls back to ID comparison).
func (h *Handler) resolveAccountNamesByID(ctx context.Context) map[string]string {
	accounts, err := h.config.ListCloudAccounts(ctx, config.CloudAccountFilter{})
	if err != nil {
		return map[string]string{}
	}
	// Allocate 2x capacity since each account contributes up to two keys.
	nameByID := make(map[string]string, len(accounts)*2)
	for _, a := range accounts {
		nameByID[a.ID] = a.Name
		if a.ExternalID != "" {
			nameByID[a.ExternalID] = a.Name
		}
	}
	return nameByID
}
