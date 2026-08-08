package auth

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Account-scope ceiling on group writes (issue #1738, folded into #1737).
//
// EVERY refusal case here sends allowed_accounts with NO "permissions" key.
// That shape is the whole finding: checkGrantCeiling returns early when no
// permissions are sent, so a test that includes permissions alongside would
// PASS WITH THE BUG PRESENT, because the permission ceiling then runs and
// refuses for an unrelated reason.

const scopedAccountA = "acct-A"

// scopedActorService wires an actor holding only update:groups, scoped to
// exactly one cloud account, editing a group scoped to that same account.
func scopedActorService(t *testing.T, ctx context.Context, mockStore *MockStore) *Service {
	t.Helper()
	mockStore.On("GetUserByID", ctx, ceilingActorID).
		Return(&User{ID: ceilingActorID, GroupIDs: []string{ceilingActorGroupID}}, nil)
	mockStore.On("GetGroup", ctx, ceilingActorGroupID).Return(&Group{
		ID:              ceilingActorGroupID,
		Name:            "Scoped Group Managers",
		Permissions:     []Permission{{Action: ActionUpdate, Resource: ResourceGroups}},
		AllowedAccounts: []string{scopedAccountA},
	}, nil)
	mockStore.On("GetGroup", ctx, ceilingTargetID).Return(&Group{
		ID:              ceilingTargetID,
		Name:            "Team",
		AllowedAccounts: []string{scopedAccountA},
	}, nil)
	return createTestService(mockStore, new(MockEmailSender))
}

func TestAccountCeiling_RefusesWideningWithNoPermissionsSent(t *testing.T) {
	ctx := context.Background()

	widenings := []struct {
		name     string
		accounts []string
		wantIn   string
	}{
		{"to additional accounts", []string{scopedAccountA, "acct-B"}, `"acct-B"`},
		// The two unrestricted spellings. An empty list is NOT a narrowing:
		// IsUnrestrictedAccess reads it as "all accounts".
		{"to the empty list (unrestricted)", []string{}, "unrestricted"},
		{"to the wildcard", []string{"*"}, "unrestricted"},
	}

	for _, w := range widenings {
		t.Run(w.name, func(t *testing.T) {
			mockStore := new(MockStore)
			t.Cleanup(func() { mockStore.AssertExpectations(t) })
			svc := scopedActorService(t, ctx, mockStore)

			// NOTE: no Permissions field. This is the request shape the bug
			// lives in; adding permissions here would mask it entirely.
			result, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
				APIUpdateGroupRequest{AllowedAccounts: w.accounts})

			require.Error(t, err)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, ErrPermissionCeiling)
			assert.Contains(t, err.Error(), w.wantIn)
			mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
		})
	}
}

// Negative control 1: a write that WIDENS the group but stays inside the
// actor's own scope succeeds.
//
// This deliberately widens rather than repeating the group's current scope. A
// same-value write returns on the not-a-widening branch without ever
// resolving the actor, so it would prove only that the early return works --
// not that the actor-scope branch actually grants anything.
func TestAccountCeiling_AllowsInScopeWidening(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	// Actor may reach A and B; the group currently reaches only A.
	mockStore.On("GetUserByID", ctx, ceilingActorID).
		Return(&User{ID: ceilingActorID, GroupIDs: []string{ceilingActorGroupID}}, nil)
	mockStore.On("GetGroup", ctx, ceilingActorGroupID).Return(&Group{
		ID:              ceilingActorGroupID,
		Permissions:     []Permission{{Action: ActionUpdate, Resource: ResourceGroups}},
		AllowedAccounts: []string{scopedAccountA, "acct-B"},
	}, nil)
	mockStore.On("GetGroup", ctx, ceilingTargetID).Return(&Group{
		ID: ceilingTargetID, Name: "Team", AllowedAccounts: []string{scopedAccountA},
	}, nil)

	var saved *Group
	mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).
		Run(func(a mock.Arguments) {
			g, ok := a.Get(1).(*Group)
			require.True(t, ok)
			saved = g
		}).Return(nil).Once()

	_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
		APIUpdateGroupRequest{AllowedAccounts: []string{scopedAccountA, "acct-B"}})

	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, []string{scopedAccountA, "acct-B"}, saved.AllowedAccounts)
}

// Negative control 1b: repeating the group's current scope is not a widening
// and must not require an authorization round-trip at all.
func TestAccountCeiling_UnchangedScopeSkipsActorLookup(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	mockStore.On("GetGroup", ctx, ceilingTargetID).Return(&Group{
		ID: ceilingTargetID, Name: "Team", AllowedAccounts: []string{scopedAccountA},
	}, nil)
	mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Once()

	_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
		APIUpdateGroupRequest{AllowedAccounts: []string{scopedAccountA}})

	require.NoError(t, err)
	mockStore.AssertNotCalled(t, "GetUserByID", mock.Anything, mock.Anything)
}

// Negative control 2: an UNRESTRICTED actor may still widen a group, so the
// refusals above come from the actor's scope and not from the check refusing
// all widenings outright.
func TestAccountCeiling_UnrestrictedActorMayWiden(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	stubActorPermissions(ctx, mockStore, adminOnly) // no AllowedAccounts -> unrestricted
	stubTargetGroup(ctx, mockStore, &Group{
		ID: ceilingTargetID, Name: "Team", AllowedAccounts: []string{scopedAccountA},
	})
	mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Once()

	_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
		APIUpdateGroupRequest{AllowedAccounts: []string{"*"}})
	require.NoError(t, err)
}

// Negative control 3: omitting allowed_accounts entirely (nil, "not sent")
// must not be treated as a write, or every name-only edit would be refused.
func TestAccountCeiling_NotSentIsNotAWrite(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	// Only the target group is stubbed. Neither ceiling resolves the actor
	// for a request that sends nothing they gate, and asserting that here
	// (via AssertExpectations on an actor-free mock) is the point: a
	// name-only edit must not become an authorization round-trip.
	mockStore.On("GetGroup", ctx, ceilingTargetID).Return(&Group{
		ID: ceilingTargetID, Name: "Team", AllowedAccounts: []string{scopedAccountA},
	}, nil)

	var saved *Group
	mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).
		Run(func(a mock.Arguments) {
			g, ok := a.Get(1).(*Group)
			require.True(t, ok)
			saved = g
		}).Return(nil).Once()

	_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
		APIUpdateGroupRequest{Name: "Renamed"})

	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, "Renamed", saved.Name)
	assert.Equal(t, []string{scopedAccountA}, saved.AllowedAccounts, "scope must be untouched")
}

// Narrowing is always allowed, whoever the actor is: it cannot widen anything.
func TestAccountCeiling_NarrowingIsAllowed(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	mockStore.On("GetUserByID", ctx, ceilingActorID).
		Return(&User{ID: ceilingActorID, GroupIDs: []string{ceilingActorGroupID}}, nil).Maybe()
	mockStore.On("GetGroup", ctx, ceilingActorGroupID).Return(&Group{
		ID:              ceilingActorGroupID,
		Permissions:     []Permission{{Action: ActionUpdate, Resource: ResourceGroups}},
		AllowedAccounts: []string{"acct-Z"},
	}, nil).Maybe()
	mockStore.On("GetGroup", ctx, ceilingTargetID).Return(&Group{
		ID: ceilingTargetID, Name: "Team", AllowedAccounts: []string{scopedAccountA, "acct-B"},
	}, nil)
	mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Once()

	_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
		APIUpdateGroupRequest{AllowedAccounts: []string{scopedAccountA}})
	require.NoError(t, err, "narrowing the group's own scope is never a widening")
}

// CreateGroupAPI has no existing scope to compare against, so any
// allowed_accounts value must sit inside the creator's own.
func TestAccountCeiling_CreateIsBounded(t *testing.T) {
	ctx := context.Background()

	t.Run("out of scope is refused", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := createTestService(mockStore, new(MockEmailSender))

		mockStore.On("GetUserByID", ctx, ceilingActorID).
			Return(&User{ID: ceilingActorID, GroupIDs: []string{ceilingActorGroupID}}, nil)
		mockStore.On("GetGroup", ctx, ceilingActorGroupID).Return(&Group{
			ID:              ceilingActorGroupID,
			Permissions:     []Permission{{Action: ActionCreate, Resource: ResourceGroups}},
			AllowedAccounts: []string{scopedAccountA},
		}, nil)

		_, err := svc.CreateGroupAPI(ctx, ceilingActorID, APICreateGroupRequest{
			Name:            "Wider",
			AllowedAccounts: []string{"acct-B"},
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPermissionCeiling)
		mockStore.AssertNotCalled(t, "CreateGroup", mock.Anything, mock.Anything)
	})

	t.Run("in scope is allowed", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := createTestService(mockStore, new(MockEmailSender))

		mockStore.On("GetUserByID", ctx, ceilingActorID).
			Return(&User{ID: ceilingActorID, GroupIDs: []string{ceilingActorGroupID}}, nil)
		mockStore.On("GetGroup", ctx, ceilingActorGroupID).Return(&Group{
			ID:              ceilingActorGroupID,
			Permissions:     []Permission{{Action: ActionCreate, Resource: ResourceGroups}},
			AllowedAccounts: []string{scopedAccountA},
		}, nil)
		mockStore.On("CreateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Once()

		_, err := svc.CreateGroupAPI(ctx, ceilingActorID, APICreateGroupRequest{
			Name:            "Narrower",
			AllowedAccounts: []string{scopedAccountA},
		})
		require.NoError(t, err)
	})
}

// Fail closed: an actor whose scope cannot be resolved cannot widen.
func TestAccountCeiling_FailsClosedOnUnidentifiedActor(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	stubTargetGroup(ctx, mockStore, &Group{
		ID: ceilingTargetID, Name: "Team", AllowedAccounts: []string{scopedAccountA},
	})

	_, err := svc.UpdateGroupAPI(ctx, "", ceilingTargetID,
		APIUpdateGroupRequest{AllowedAccounts: []string{"*"}})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPermissionCeiling)
	mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
}

// An actor whose groups do not resolve has an UNKNOWN scope, not an
// unrestricted one. collectGroupsAndAccounts skips a missing or deleted group
// silently, so before this guard a total resolution failure produced an empty
// list -- read as "all accounts" -- and the ceiling became a no-op on exactly
// the path it guards.
//
// The permission ceiling already failed closed on the same input (an empty
// permission set grants nothing); this closes the asymmetry.
func TestAccountCeiling_FailsClosedWhenActorScopeUnresolvable(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name      string
		groupResp func(*MockStore)
	}{
		{"actor's only group is missing", func(ms *MockStore) {
			ms.On("GetGroup", ctx, ceilingActorGroupID).Return(nil, pgx.ErrNoRows)
		}},
		{"actor's only group resolves to nil", func(ms *MockStore) {
			ms.On("GetGroup", ctx, ceilingActorGroupID).Return(nil, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockStore := new(MockStore)
			t.Cleanup(func() { mockStore.AssertExpectations(t) })
			svc := createTestService(mockStore, new(MockEmailSender))

			mockStore.On("GetUserByID", ctx, ceilingActorID).
				Return(&User{ID: ceilingActorID, GroupIDs: []string{ceilingActorGroupID}}, nil)
			tc.groupResp(mockStore)
			mockStore.On("GetGroup", ctx, ceilingTargetID).Return(&Group{
				ID: ceilingTargetID, Name: "Team", AllowedAccounts: []string{scopedAccountA},
			}, nil)

			_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
				APIUpdateGroupRequest{AllowedAccounts: []string{"*"}})

			require.Error(t, err, "an unresolvable scope must refuse the widening, not permit it")
			assert.ErrorIs(t, err, ErrPermissionCeiling)
			// Total failure is the degenerate case of a partial one -- every
			// group skipped -- so it is caught by the skipped-group guard and
			// carries that message. Assert the sentinel plus the shared
			// substring rather than one guard's exact wording.
			assert.Contains(t, err.Error(), "could not be resolved")
			mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
		})
	}
}

// A PARTIAL group resolution can WIDEN the actor's own scope, letting them
// grant what their real configuration forbids (#1737 A1, same defect as the
// read path in #1752).
//
// Needs TWO groups: one granting update:groups with no allowed_accounts
// (unrestricted alone), one carrying the restriction. The union is
// restricted; lose the restricting group and it collapses to empty = every
// account, and the ceiling then permits widening a group to ["*"].
func TestAccountCeiling_PartialActorResolutionThatWidensIsRefused(t *testing.T) {
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
			mockStore := new(MockStore)
			t.Cleanup(func() { mockStore.AssertExpectations(t) })
			svc := createTestService(mockStore, new(MockEmailSender))

			mockStore.On("GetUserByID", ctx, ceilingActorID).
				Return(&User{ID: ceilingActorID, GroupIDs: []string{permGroup, scopeGroup}}, nil)
			mockStore.On("GetGroup", ctx, permGroup).Return(&Group{
				ID:          permGroup,
				Permissions: []Permission{{Action: ActionUpdate, Resource: ResourceGroups}},
			}, nil)
			tc.scopeResp(mockStore)
			mockStore.On("GetGroup", ctx, ceilingTargetID).Return(&Group{
				ID: ceilingTargetID, Name: "Team", AllowedAccounts: []string{scopedAccountA},
			}, nil)

			_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
				APIUpdateGroupRequest{AllowedAccounts: []string{"*"}})

			require.Error(t, err, "a widened actor scope must not authorize widening a group")
			assert.ErrorIs(t, err, ErrPermissionCeiling)
			mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
		})
	}
}

// Baseline control: both groups resolving, the same actor is correctly
// refused for a DIFFERENT reason (their real scope does not cover "*"), which
// is what makes the partial case a genuine bypass rather than a no-op.
func TestAccountCeiling_PartialActorBaselineIsRefusedOnRealScope(t *testing.T) {
	ctx := context.Background()
	const permGroup, scopeGroup = "g-perm", "g-scope"

	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	mockStore.On("GetUserByID", ctx, ceilingActorID).
		Return(&User{ID: ceilingActorID, GroupIDs: []string{permGroup, scopeGroup}}, nil)
	mockStore.On("GetGroup", ctx, permGroup).Return(&Group{
		ID: permGroup, Permissions: []Permission{{Action: ActionUpdate, Resource: ResourceGroups}},
	}, nil)
	mockStore.On("GetGroup", ctx, scopeGroup).Return(&Group{
		ID: scopeGroup, AllowedAccounts: []string{scopedAccountA},
	}, nil)
	mockStore.On("GetGroup", ctx, ceilingTargetID).Return(&Group{
		ID: ceilingTargetID, Name: "Team", AllowedAccounts: []string{scopedAccountA},
	}, nil)

	_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
		APIUpdateGroupRequest{AllowedAccounts: []string{"*"}})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPermissionCeiling)
}

// An actor whose surviving union carries "*" was already maximally wide, so a
// lost group cannot widen them. Refusing them would be pure availability cost
// -- and all seven seeded groups ship allowed_accounts = ARRAY['*'].
func TestAccountCeiling_WildcardActorToleratesSkippedGroup(t *testing.T) {
	ctx := context.Background()
	const seededGroup, scopeGroup = "g-seeded", "g-scope"

	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	mockStore.On("GetUserByID", ctx, ceilingActorID).
		Return(&User{ID: ceilingActorID, GroupIDs: []string{seededGroup, scopeGroup}}, nil)
	mockStore.On("GetGroup", ctx, seededGroup).Return(&Group{
		ID:              seededGroup,
		Permissions:     []Permission{{Action: ActionUpdate, Resource: ResourceGroups}},
		AllowedAccounts: []string{"*"},
	}, nil)
	mockStore.On("GetGroup", ctx, scopeGroup).Return(nil, pgx.ErrNoRows)
	mockStore.On("GetGroup", ctx, ceilingTargetID).Return(&Group{
		ID: ceilingTargetID, Name: "Team", AllowedAccounts: []string{scopedAccountA},
	}, nil)
	mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Once()

	_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
		APIUpdateGroupRequest{AllowedAccounts: []string{"*"}})

	require.NoError(t, err,
		"an actor already unrestricted at baseline must not be refused for a lost group")
}
