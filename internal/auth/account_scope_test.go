package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The defining property of AccountScope (issue #1748): THE ZERO VALUE DENIES.
//
// This is the reason the type exists. The untyped []string it replaces used
// the same value -- empty -- for "no restriction configured" and for "we could
// not establish your scope", so every resolution failure silently granted
// access to every account.
//
// If someone later inverts the flag to `Restricted bool` because it reads
// better, the zero value starts granting everything and the bug returns with
// the compiler's blessing. These tests are the barrier.

func TestAccountScope_ZeroValueDeniesEverything(t *testing.T) {
	var zero AccountScope

	assert.False(t, zero.AllowsAll(), "the zero value must NOT be unrestricted")
	assert.False(t, zero.Allows("any-account-id", ""), "the zero value must allow no account")
	assert.False(t, zero.Allows("any-account-id", "any-name"))
	assert.Empty(t, zero.FilterIDs([]string{"a", "b", "c"}),
		"the zero value must filter every account out")
	assert.Equal(t, "no accounts", zero.String())
}

// A struct returned on an error path, or one whose fields a later change fails
// to populate, must deny. Same property, stated the way it will actually be
// encountered.
func TestAccountScope_UninitialisedOnErrorPathDenies(t *testing.T) {
	failing := func() (AccountScope, error) {
		var s AccountScope // deliberately not populated
		return s, assert.AnError
	}
	s, err := failing()
	require.Error(t, err)
	assert.False(t, s.Allows("acct-A", ""), "a scope returned alongside an error must not grant access")
}

func TestAccountScope_UnrestrictedMustBeSetPositively(t *testing.T) {
	assert.True(t, UnrestrictedScope().AllowsAll())
	assert.True(t, UnrestrictedScope().Allows("literally-anything", ""))
	assert.Equal(t, []string{"a", "b"}, UnrestrictedScope().FilterIDs([]string{"a", "b"}))
	assert.Equal(t, "all accounts", UnrestrictedScope().String())
}

// ScopeForAccounts with an EMPTY list grants nothing -- the opposite of the
// legacy []string convention. Pinned because this inversion is the whole point
// and is exactly what a careless migration would get backwards.
func TestAccountScope_EmptyExplicitListGrantsNothing(t *testing.T) {
	s := ScopeForAccounts(nil)
	assert.False(t, s.AllowsAll(), "an empty explicit list is NOT unrestricted")
	assert.False(t, s.Allows("acct-A", ""))

	s2 := ScopeForAccounts([]string{})
	assert.False(t, s2.AllowsAll())
	assert.False(t, s2.Allows("acct-A", ""))
}

func TestAccountScope_RestrictedAllowsOnlyItsOwn(t *testing.T) {
	s := ScopeForAccounts([]string{"acct-A", "prod-name"})

	assert.True(t, s.Allows("acct-A", ""), "matches by ID")
	assert.True(t, s.Allows("some-uuid", "prod-name"), "matches by display name")
	assert.False(t, s.Allows("acct-B", ""), "an account outside the list is denied")
	assert.False(t, s.Allows("acct-B", "other-name"))
	assert.False(t, s.AllowsAll())
	assert.Equal(t, []string{"acct-A"}, s.FilterIDs([]string{"acct-A", "acct-B"}))
}

// ScopeFromLegacyList preserves the historical convention, and is safe only
// for an already-successful resolution. Both spellings of unrestricted must
// convert to a positively-set flag rather than to an empty restricted scope.
func TestAccountScope_FromLegacyList(t *testing.T) {
	for _, tc := range []struct {
		name             string
		in               []string
		wantUnrestricted bool
	}{
		{"nil means unrestricted under the legacy convention", nil, true},
		{"empty means unrestricted under the legacy convention", []string{}, true},
		{"wildcard means unrestricted", []string{"*"}, true},
		{"wildcard among others still means unrestricted", []string{"acct-A", "*"}, true},
		{"a real list stays restricted", []string{"acct-A"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := ScopeFromLegacyList(tc.in)
			assert.Equal(t, tc.wantUnrestricted, s.AllowsAll())
			if tc.wantUnrestricted {
				assert.True(t, s.Allows("anything", ""))
			} else {
				assert.True(t, s.Allows("acct-A", ""))
				assert.False(t, s.Allows("acct-B", ""))
			}
		})
	}
}

// The migration must not accidentally promote a scoped principal to
// unrestricted -- the control the team lead asked for explicitly.
func TestAccountScope_ScopedPrincipalStaysLimited(t *testing.T) {
	s := ScopeFromLegacyList([]string{"acct-A", "acct-B"})

	require.False(t, s.AllowsAll(), "a genuinely scoped principal must not become unrestricted")
	assert.True(t, s.Allows("acct-A", ""))
	assert.True(t, s.Allows("acct-B", ""))
	assert.False(t, s.Allows("acct-C", ""))
	assert.Equal(t, []string{"acct-A"}, s.FilterIDs([]string{"acct-A", "acct-C"}))
}

// Behavioral parity with the helpers it replaces, so the migration cannot
// silently change who can reach what.
func TestAccountScope_ParityWithLegacyHelpers(t *testing.T) {
	for _, legacy := range [][]string{
		nil, {}, {"*"}, {"acct-A"}, {"acct-A", "acct-B"}, {"prod-name"},
	} {
		s := ScopeFromLegacyList(legacy)
		assert.Equal(t, IsUnrestrictedAccess(legacy), s.AllowsAll(),
			"AllowsAll must agree with IsUnrestrictedAccess for %v", legacy)
		for _, probe := range []struct{ id, name string }{
			{"acct-A", ""}, {"acct-B", ""}, {"some-uuid", "prod-name"}, {"acct-C", "nope"},
		} {
			assert.Equal(t, MatchesAccount(legacy, probe.id, probe.name), s.Allows(probe.id, probe.name),
				"Allows must agree with MatchesAccount for legacy=%v probe=%v", legacy, probe)
		}
	}
}
