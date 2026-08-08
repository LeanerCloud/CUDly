package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Account scope must fail CLOSED when it cannot be established (issue #1748).
//
// The trap this guards: an empty result means UNRESTRICTED
// (IsUnrestrictedAccess), a deliberate backward-compat default. But
// collectGroupsAndAccounts silently skips a group it cannot load, so a
// resolution failure produced the SAME empty value and granted access to every
// cloud account. Absence and unrestricted shared a representation.
//
// Both halves are asserted here. A refusal-only suite would be passed by an
// implementation that refuses everyone, so every failure case is paired with a
// control proving a legitimate unrestricted principal still gets through.

const scopeUserID = "99999999-9999-4999-8999-999999999999"

func scopeStore(t *testing.T) (*MockStore, *Service) {
	t.Helper()
	ms := new(MockStore)
	return ms, createTestService(ms, new(MockEmailSender))
}

// ---------- the failure modes: each must REFUSE ----------

func TestResolveAllowedAccounts_FailsClosed(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name  string
		setup func(*MockStore)
	}{
		{"group missing (pgx.ErrNoRows)", func(ms *MockStore) {
			ms.On("GetUserByID", ctx, scopeUserID).
				Return(&User{ID: scopeUserID, GroupIDs: []string{"g1"}}, nil)
			ms.On("GetGroup", ctx, "g1").Return(nil, pgx.ErrNoRows)
		}},
		{"group resolves to (nil, nil)", func(ms *MockStore) {
			ms.On("GetUserByID", ctx, scopeUserID).
				Return(&User{ID: scopeUserID, GroupIDs: []string{"g1"}}, nil)
			ms.On("GetGroup", ctx, "g1").Return(nil, nil)
		}},
		{"several groups, none resolve", func(ms *MockStore) {
			ms.On("GetUserByID", ctx, scopeUserID).
				Return(&User{ID: scopeUserID, GroupIDs: []string{"g1", "g2"}}, nil)
			ms.On("GetGroup", ctx, "g1").Return(nil, pgx.ErrNoRows)
			ms.On("GetGroup", ctx, "g2").Return(nil, nil)
		}},
		{"user has no groups at all", func(ms *MockStore) {
			ms.On("GetUserByID", ctx, scopeUserID).Return(&User{ID: scopeUserID}, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ms, svc := scopeStore(t)
			t.Cleanup(func() { ms.AssertExpectations(t) })
			tc.setup(ms)

			got, err := svc.ResolveAllowedAccounts(ctx, scopeUserID)

			require.Error(t, err, "an unestablishable scope must be refused, not treated as unrestricted")
			assert.Nil(t, got)
			assert.Contains(t, err.Error(), "could not be established")
			// The property in its own terms: whatever comes back must not be
			// readable as "all accounts".
			assert.False(t, IsUnrestrictedAccess(got) && err == nil,
				"a failed resolution must never yield an unrestricted scope")
		})
	}
}

// A store error still propagates rather than being converted into a scope.
func TestResolveAllowedAccounts_PropagatesStoreError(t *testing.T) {
	ctx := context.Background()
	ms, svc := scopeStore(t)
	t.Cleanup(func() { ms.AssertExpectations(t) })

	boom := errors.New("db unavailable")
	ms.On("GetUserByID", ctx, scopeUserID).Return(nil, boom)

	got, err := svc.ResolveAllowedAccounts(ctx, scopeUserID)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.ErrorIs(t, err, boom)
}

// ---------- the controls: legitimate principals must still PASS ----------

// Three distinct principals are legitimately unrestricted, and the third is
// the one that collides with the failure modes: a group with no
// allowed_accounts configured resolves to the SAME empty list. Making absence
// fail closed without this control would have broken every legacy group.
func TestResolveAllowedAccounts_LegitimatePrincipalsStillResolve(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name             string
		group            *Group
		wantAccounts     []string
		wantUnrestricted bool
	}{
		{
			name:             "group carrying the * wildcard (Administrators)",
			group:            &Group{ID: "g1", AllowedAccounts: []string{"*"}},
			wantAccounts:     []string{"*"},
			wantUnrestricted: true,
		},
		{
			name:             "group with NO allowed_accounts configured (legacy default)",
			group:            &Group{ID: "g1"},
			wantAccounts:     []string{},
			wantUnrestricted: true,
		},
		{
			name:             "group scoped to one account stays scoped",
			group:            &Group{ID: "g1", AllowedAccounts: []string{"acct-A"}},
			wantAccounts:     []string{"acct-A"},
			wantUnrestricted: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ms, svc := scopeStore(t)
			t.Cleanup(func() { ms.AssertExpectations(t) })
			ms.On("GetUserByID", ctx, scopeUserID).
				Return(&User{ID: scopeUserID, GroupIDs: []string{"g1"}}, nil)
			ms.On("GetGroup", ctx, "g1").Return(tc.group, nil)

			got, err := svc.ResolveAllowedAccounts(ctx, scopeUserID)

			require.NoError(t, err, "a resolvable scope must not be refused")
			assert.ElementsMatch(t, tc.wantAccounts, got)
			assert.Equal(t, tc.wantUnrestricted, IsUnrestrictedAccess(got))
		})
	}
}

// THE MULTI-GROUP WIDENING (issue #1748, the case an earlier version of this
// guard missed).
//
// This test replaces one that asserted the opposite -- that partial resolution
// is "allowed and narrower". That invariant is FALSE and the old test passed
// with the bug present, because its surviving group carried the restriction.
// A test encoding a false invariant is worse than no test: it tells the next
// reader the case is covered.
//
// The configuration needs TWO groups, which is why a six-case single-group
// verification could not find it: one granting a permission with NO
// allowed_accounts (unrestricted on its own), one carrying the restriction.
// The union is restricted; lose the restricting group and it collapses to
// empty, which reads as EVERY account. Dropping a group WIDENS.
func TestResolveAllowedAccounts_PartialResolutionThatWidensIsRefused(t *testing.T) {
	ctx := context.Background()
	const permGroup, scopeGroup = "g-perm", "g-scope"

	for _, tc := range []struct {
		name      string
		scopeResp func(*MockStore)
	}{
		{"restricting group missing (ErrNoRows)", func(ms *MockStore) {
			ms.On("GetGroup", ctx, scopeGroup).Return(nil, pgx.ErrNoRows)
		}},
		{"restricting group resolves to (nil, nil)", func(ms *MockStore) {
			ms.On("GetGroup", ctx, scopeGroup).Return(nil, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ms, svc := scopeStore(t)
			t.Cleanup(func() { ms.AssertExpectations(t) })
			ms.On("GetUserByID", ctx, scopeUserID).
				Return(&User{ID: scopeUserID, GroupIDs: []string{permGroup, scopeGroup}}, nil)
			// Survives, and contributes NO accounts -- unrestricted alone.
			ms.On("GetGroup", ctx, permGroup).Return(&Group{
				ID:          permGroup,
				Permissions: []Permission{{Action: ActionUpdate, Resource: ResourceGroups}},
			}, nil)
			tc.scopeResp(ms)

			got, err := svc.ResolveAllowedAccounts(ctx, scopeUserID)

			require.Error(t, err,
				"losing the restricting group collapses the union to empty = ALL accounts; that must be refused")
			assert.Nil(t, got)
			assert.Contains(t, err.Error(), "could not be established")
			assert.False(t, IsUnrestrictedAccess(got) && err == nil)
		})
	}
}

// The baseline control for the case above: with BOTH groups resolving, the
// same actor is correctly restricted. Without this, a guard that refused the
// two-group shape outright would pass the test above.
func TestResolveAllowedAccounts_MultiGroupBaselineStaysRestricted(t *testing.T) {
	ctx := context.Background()
	const permGroup, scopeGroup = "g-perm", "g-scope"

	ms, svc := scopeStore(t)
	t.Cleanup(func() { ms.AssertExpectations(t) })
	ms.On("GetUserByID", ctx, scopeUserID).
		Return(&User{ID: scopeUserID, GroupIDs: []string{permGroup, scopeGroup}}, nil)
	ms.On("GetGroup", ctx, permGroup).Return(&Group{
		ID:          permGroup,
		Permissions: []Permission{{Action: ActionUpdate, Resource: ResourceGroups}},
	}, nil)
	ms.On("GetGroup", ctx, scopeGroup).Return(&Group{
		ID: scopeGroup, AllowedAccounts: []string{"acct-A"},
	}, nil)

	got, err := svc.ResolveAllowedAccounts(ctx, scopeUserID)

	require.NoError(t, err)
	assert.Equal(t, []string{"acct-A"}, got)
	assert.False(t, IsUnrestrictedAccess(got))
}

// Deleted-group tolerance is PRESERVED for a restricted principal: a
// non-empty union cannot have been widened by the loss, so the skip is
// absorbed rather than refused. This is the behavior option 1 ("refuse on any
// unresolved group") would have destroyed, locking out every user with one
// stale membership.
func TestResolveAllowedAccounts_SkippedGroupToleratedWhenUnionStaysRestricted(t *testing.T) {
	ctx := context.Background()
	ms, svc := scopeStore(t)
	t.Cleanup(func() { ms.AssertExpectations(t) })

	ms.On("GetUserByID", ctx, scopeUserID).
		Return(&User{ID: scopeUserID, GroupIDs: []string{"g1", "g2"}}, nil)
	ms.On("GetGroup", ctx, "g1").Return(&Group{ID: "g1", AllowedAccounts: []string{"acct-A"}}, nil)
	ms.On("GetGroup", ctx, "g2").Return(nil, pgx.ErrNoRows)

	got, err := svc.ResolveAllowedAccounts(ctx, scopeUserID)

	require.NoError(t, err, "a stale membership must not lock out a restricted principal")
	assert.Equal(t, []string{"acct-A"}, got)
	assert.False(t, IsUnrestrictedAccess(got))
}

// A duplicated membership resolving to an unrestricted group is allowed.
//
// This was named DuplicateGroupIDsAreNotSkips and claimed to exclude a
// len(Groups) vs len(GroupIDs) skip count. It cannot: Groups is appended once
// per ID with no dedup, so duplicates produce duplicate entries and the two
// implementations agree on every shape -- swapping one for the other leaves
// this test passing. Renamed to what it does guard, which is over-blocking:
// a guard that refused any multi-entry membership would fail here.
func TestResolveAllowedAccounts_DuplicateMembershipIsAllowed(t *testing.T) {
	ctx := context.Background()
	ms, svc := scopeStore(t)
	t.Cleanup(func() { ms.AssertExpectations(t) })

	ms.On("GetUserByID", ctx, scopeUserID).
		Return(&User{ID: scopeUserID, GroupIDs: []string{"g1", "g1"}}, nil)
	ms.On("GetGroup", ctx, "g1").Return(&Group{ID: "g1"}, nil) // no allowed_accounts

	got, err := svc.ResolveAllowedAccounts(ctx, scopeUserID)

	require.NoError(t, err, "a duplicated membership must not be refused")
	assert.True(t, IsUnrestrictedAccess(got))
}

// A survivor carrying "*" was ALREADY maximally wide at baseline, so no lost
// group can widen it. Refusing it is zero security benefit and pure
// availability cost -- and it is the shape a default deployment produces,
// because all seven seeded groups ship allowed_accounts = ARRAY['*'].
//
// This is why the guard tests len(AllowedAccounts) == 0 rather than
// IsUnrestrictedAccess: the latter is true for "*" too, and using it 500'd
// every account-scoped endpoint for any member of a seeded group who also had
// one stale membership.
func TestResolveAllowedAccounts_WildcardSurvivorToleratesSkippedGroup(t *testing.T) {
	ctx := context.Background()
	const seeded, scoped = "g-seeded", "g-scoped"

	for _, tc := range []struct {
		name      string
		scopeResp func(*MockStore)
	}{
		{"stale membership missing (ErrNoRows)", func(ms *MockStore) {
			ms.On("GetGroup", ctx, scoped).Return(nil, pgx.ErrNoRows)
		}},
		{"stale membership resolves to (nil, nil)", func(ms *MockStore) {
			ms.On("GetGroup", ctx, scoped).Return(nil, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ms, svc := scopeStore(t)
			t.Cleanup(func() { ms.AssertExpectations(t) })
			ms.On("GetUserByID", ctx, scopeUserID).
				Return(&User{ID: scopeUserID, GroupIDs: []string{seeded, scoped}}, nil)
			ms.On("GetGroup", ctx, seeded).Return(&Group{
				ID: seeded, AllowedAccounts: []string{"*"},
			}, nil)
			tc.scopeResp(ms)

			got, err := svc.ResolveAllowedAccounts(ctx, scopeUserID)

			require.NoError(t, err,
				"a principal already unrestricted at baseline must not be refused for a lost group")
			assert.True(t, IsUnrestrictedAccess(got))
		})
	}
}

// The baseline control: the same principal, both groups resolving, is
// unrestricted anyway -- which is what makes the refusal above pointless.
func TestResolveAllowedAccounts_WildcardSurvivorBaselineIsAlreadyUnrestricted(t *testing.T) {
	ctx := context.Background()
	ms, svc := scopeStore(t)
	t.Cleanup(func() { ms.AssertExpectations(t) })

	ms.On("GetUserByID", ctx, scopeUserID).
		Return(&User{ID: scopeUserID, GroupIDs: []string{"g-seeded", "g-scoped"}}, nil)
	ms.On("GetGroup", ctx, "g-seeded").Return(&Group{ID: "g-seeded", AllowedAccounts: []string{"*"}}, nil)
	ms.On("GetGroup", ctx, "g-scoped").Return(&Group{ID: "g-scoped", AllowedAccounts: []string{"acct-A"}}, nil)

	got, err := svc.ResolveAllowedAccounts(ctx, scopeUserID)

	require.NoError(t, err)
	assert.True(t, IsUnrestrictedAccess(got),
		"the wildcard makes this principal unrestricted before any group is lost")
}
