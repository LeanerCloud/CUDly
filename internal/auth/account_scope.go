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

// ScopeForAccounts returns a scope limited to the given accounts, treating
// every entry as a LITERAL account identifier.
//
// Two asymmetries with the legacy []string convention, and both invert its
// meaning rather than merely differing from it:
//
//   - An empty list grants NOTHING here; under the legacy convention empty
//     meant "all accounts".
//   - "*" is a literal identifier here, NOT a wildcard. ScopeForAccounts(["*"])
//     matches an account whose ID or name is the single character "*" and
//     denies everything else, where ScopeFromLegacyList(["*"]) is unrestricted.
//
// The second matters for the obvious future call. Passing a group's stored
// list straight through -- ScopeForAccounts(group.AllowedAccounts) -- silently
// produces a scope that DENIES EVERYTHING for any wildcard-carrying group, and
// all seven seeded groups ship allowed_accounts = ARRAY['*']. Use
// ScopeFromLegacyList for a value that came out of the resolver, and this
// constructor only for a list you have already decided is literal.
func ScopeForAccounts(accounts []string) AccountScope {
	return AccountScope{Accounts: accounts}
}

// ScopeFromLegacyList converts a resolved []string using the historical
// convention where empty or a "*" entry means unrestricted.
//
// ScopeFromLegacyList(nil) and ScopeFromLegacyList([]) both return
// UnrestrictedScope(). That is deliberate and it is also the dangerous part,
// so be precise about what makes it safe.
//
// This function CANNOT distinguish "no restriction configured" from "we could
// not work out this principal's scope" -- they are the same empty value, which
// is issue #1748 in its entirety. It therefore carries no safety of its own.
// It is safe only because Service.ResolveAllowedAccounts has ALREADY refused
// every input that would make empty mean the wrong thing:
//
//   - a store error, or a missing user row -> error, never reaches here
//   - every group unresolvable -> error
//   - SOME group unresolvable AND the surviving union empty -> error
//
// That third condition is the one to keep in view. An earlier version of the
// resolver refused only total failure, and under it this function WOULD have
// converted a widened empty union into UnrestrictedScope() -- carrying the bug
// forward inside the new type, with the compiler blessing it. The guarantee
// this depends on is not "the resolver returns successfully"; it is "the
// resolver refuses any empty union that a skipped group could have caused".
//
// Do not call this on a []string from any other source. If a new producer
// appears, give it the same guarantee first or build the AccountScope
// directly with ScopeForAccounts / UnrestrictedScope.
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
