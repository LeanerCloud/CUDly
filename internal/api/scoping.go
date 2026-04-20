package api

import (
	"context"
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
	if err != nil {
		return fmt.Errorf("failed to get execution: %w", err)
	}
	if execution == nil {
		return errNotFound
	}
	return h.requirePlanAccess(ctx, session, execution.PlanID)
}

// resolveAccountNamesByID fetches the complete account list once and returns
// a map from account ID → display name. Used by handlers that filter records
// (recommendations, purchases, history) by account ID but need name-based
// matching in auth.MatchesAccount.
//
// Returns an empty map on error; callers fall through to ID-only matching,
// which is still safe (MatchesAccount falls back to ID comparison).
func (h *Handler) resolveAccountNamesByID(ctx context.Context) map[string]string {
	accounts, err := h.config.ListCloudAccounts(ctx, config.CloudAccountFilter{})
	if err != nil {
		return map[string]string{}
	}
	nameByID := make(map[string]string, len(accounts))
	for _, a := range accounts {
		nameByID[a.ID] = a.Name
	}
	return nameByID
}
