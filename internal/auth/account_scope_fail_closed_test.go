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

// A partially-resolvable membership keeps working and reports only what
// resolved. Under-reporting scope makes access STRICTER, so it is safe; only
// total failure had to be refused.
func TestResolveAllowedAccounts_PartialResolutionIsAllowedAndNarrower(t *testing.T) {
	ctx := context.Background()
	ms, svc := scopeStore(t)
	t.Cleanup(func() { ms.AssertExpectations(t) })

	ms.On("GetUserByID", ctx, scopeUserID).
		Return(&User{ID: scopeUserID, GroupIDs: []string{"g1", "g2"}}, nil)
	ms.On("GetGroup", ctx, "g1").Return(&Group{ID: "g1", AllowedAccounts: []string{"acct-A"}}, nil)
	ms.On("GetGroup", ctx, "g2").Return(nil, pgx.ErrNoRows)

	got, err := svc.ResolveAllowedAccounts(ctx, scopeUserID)

	require.NoError(t, err)
	assert.Equal(t, []string{"acct-A"}, got)
	assert.False(t, IsUnrestrictedAccess(got), "a partial resolution must not widen to all accounts")
}
