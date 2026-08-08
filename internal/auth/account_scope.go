package auth

import "strings"

// AccountScope is the set of cloud accounts a principal may access.
//
// # The zero value MUST deny. This is a requirement, not an observation.
//
// AccountScope{} is {Unrestricted: false, Accounts: nil} -- restricted to
// nothing, granting access to no account at all. That is deliberate and
// load-bearing: a forgotten initialisation, a value returned on an error path,
// or a field a later change fails to populate all fail CLOSED.
//
// Do NOT invert the flag to `Restricted bool` for readability. That reads
// marginally better and is the entire bug this type exists to remove: it would
// make the zero value grant everything, so absence would once again mean
// unrestricted -- with a compiler now blessing it. Issue #1748 is precisely
// that failure in its untyped form: `[]string` used both for "no restriction
// configured" and for "we could not work out your scope", so every resolution
// failure silently granted access to every account.
//
// Unrestricted must therefore always be set POSITIVELY by a caller that has
// established the principal genuinely has unlimited access -- the stateless
// admin API key, or a group carrying the "*" wildcard.
type AccountScope struct {
	// Accounts is the explicit allow-list, meaningful only when
	// Unrestricted is false. Entries match by cloud account ID or by
	// display name, mirroring MatchesAccount.
	Accounts []string

	// Unrestricted grants access to every account. Set it only where
	// unlimited access has been positively established.
	Unrestricted bool
}

// UnrestrictedScope returns a scope granting every account. Use it only at a
// site that has positively established unlimited access; never as a fallback
// or a default.
func UnrestrictedScope() AccountScope {
	return AccountScope{Unrestricted: true}
}

// ScopeForAccounts returns a scope limited to the given accounts.
//
// An empty list yields a scope that grants NOTHING, which is the opposite of
// the legacy []string convention where empty meant "all accounts". Callers
// converting a legacy list must decide which they mean rather than passing it
// through; ScopeFromLegacyList exists for the one place that still has to.
func ScopeForAccounts(accounts []string) AccountScope {
	return AccountScope{Accounts: accounts}
}

// ScopeFromLegacyList converts a resolved []string using the historical
// convention where empty or a "*" entry means unrestricted.
//
// It is safe ONLY for a list that came from a SUCCESSFUL resolution, because
// it cannot tell "no restriction configured" from "resolution failed" -- they
// are the same value, which is issue #1748. Callers must have already failed
// closed on the failure case (see Service.ResolveAllowedAccounts) before
// calling this.
func ScopeFromLegacyList(accounts []string) AccountScope {
	if IsUnrestrictedAccess(accounts) {
		return UnrestrictedScope()
	}
	return ScopeForAccounts(accounts)
}

// Allows reports whether the scope permits the given cloud account, matched by
// internal ID or display name. Pass "" for the name when unavailable.
//
// A restricted scope with no accounts allows nothing, which is what makes the
// zero value deny.
func (s AccountScope) Allows(accountID, accountName string) bool {
	if s.Unrestricted {
		return true
	}
	for _, a := range s.Accounts {
		if a == accountID {
			return true
		}
		if accountName != "" && a == accountName {
			return true
		}
	}
	return false
}

// AllowsAll reports whether the scope is unrestricted. Prefer Allows; this
// exists for the few sites that must branch on unrestricted-ness itself, such
// as skipping a filter entirely.
func (s AccountScope) AllowsAll() bool {
	return s.Unrestricted
}

// FilterIDs returns the subset of ids the scope permits. An unrestricted scope
// returns ids unchanged.
func (s AccountScope) FilterIDs(ids []string) []string {
	if s.Unrestricted {
		return ids
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if s.Allows(id, "") {
			out = append(out, id)
		}
	}
	return out
}

// String renders the scope for error messages and logs.
func (s AccountScope) String() string {
	if s.Unrestricted {
		return "all accounts"
	}
	if len(s.Accounts) == 0 {
		return "no accounts"
	}
	return strings.Join(s.Accounts, ", ")
}
