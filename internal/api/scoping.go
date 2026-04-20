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
