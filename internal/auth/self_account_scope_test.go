package auth

// Account-scope ceiling on self-membership changes (issue #1756).
//
// The bug had two routes running in OPPOSITE directions, and a guard that only
// inspects what is being ADDED sees exactly one of them:
//
//	join a wider group   -> Administrators ships allowed_accounts = ARRAY['*']
//	leave the scoping group -> the union collapses to [], which
//	                           IsUnrestrictedAccess reads as every account
//
// Both refusals below are therefore paired with a control that must still be
// ALLOWED, because a guard that refused every self-membership edit would pass
// the refusals on its own. The controls are the load-bearing half of this file:
//
//	drop the group that carries NO restriction -> allowed (T3)
//	join a group scoped to a SUBSET of your own accounts -> allowed (T4)
//
// T3 is deliberately the same shape as the T2 refusal (a removal by the same
// actor from the same two groups), so it pins that the guard distinguishes
// WHICH group carried the restriction rather than refusing removals wholesale.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	scopedActorID        = "88888888-8888-4888-8888-888888888888"
	regionalAdminGroupID = "99999999-9999-4999-8999-999999999999"
	acctAViewersGroupID  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	deletedGroupID       = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

// regionalAdminGroup is the realistic shape of a scoped administrator: the
// full {admin, *} capability (which is what PUT /api/users/{id} gates on), but
// limited to two cloud accounts. Everything this file tests starts here.
func regionalAdminGroup() *Group {
	return &Group{
		ID:              regionalAdminGroupID,
		Name:            "Regional Administrators",
		Permissions:     []Permission{{Action: ActionAdmin, Resource: ResourceAll}},
		AllowedAccounts: []string{"acct-A", "acct-B"},
	}
}

// acctAViewersGroup is scoped to a strict SUBSET of regionalAdminGroup's
// accounts, so joining it grants nothing new on the account dimension.
func acctAViewersGroup() *Group {
	return &Group{
		ID:              acctAViewersGroupID,
		Name:            "Acct-A Viewers",
		Permissions:     []Permission{{Action: ActionView, Resource: ResourceRecommendations}},
		AllowedAccounts: []string{"acct-A"},
	}
}

// stubScopedActor wires the actor's user row and the groups the change touches.
//
// Refusal cases add a permissive UpdateUser stub on purpose: without one, a
// guard removal kills the test by PANICKING on an unstubbed write, and that
// kill evaporates the moment someone adds a stub while tidying fixtures. With
// it, the removal is caught by an assertion instead.
//
// The GetGroup stubs are .Maybe() for the mirror-image reason. They are the
// guard's own reads, so a guard removal leaves them unmet and every test in
// this file fails on the unmet expectation -- including the CONTROLS, which
// exist precisely to pass when the guard is disabled and fail when it refuses
// everything. Asserting them would collapse that distinction into "all eight
// tests turn red", which measures nothing. Each test asserts its outcome
// directly instead.
func stubScopedActor(ctx context.Context, mockStore *MockStore, priorGroups []string, groups ...*Group) {
	mockStore.On("GetUserByID", ctx, scopedActorID).
		Return(&User{ID: scopedActorID, Active: true, GroupIDs: priorGroups}, nil)
	for _, g := range groups {
		mockStore.On("GetGroup", ctx, g.ID).Return(g, nil).Maybe()
	}
}

// T1 -- Route 1: joining a wider group. The seeded Administrators group ships
// allowed_accounts = ARRAY['*'] (migrations 000024/000057), so a default
// deployment always has one group to launder scope through. The pre-existing
// guards pass this: update:users is what they ask for and admin:* grants it,
// and {admin, *} is not a carved-out money verb.
func TestSelfAccountScope_JoiningWiderGroupRefused(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	stubScopedActor(ctx, mockStore, []string{regionalAdminGroupID},
		regionalAdminGroup(), adminGroup())
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Maybe()

	_, err := svc.UpdateUser(ctx, scopedActorID, scopedActorID, UpdateUserRequest{
		GroupIDs: []string{regionalAdminGroupID, DefaultAdminGroupID},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSelfEscalation)
	assert.Contains(t, err.Error(), "all cloud accounts")
	mockStore.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
}

// T2 -- Route 2: leaving the group that carries the restriction. Nothing is
// added, so every add-oriented check is a no-op; the union collapses from
// [acct-A acct-B] to [] and empty means EVERY account. Removing the
// restriction grants the restriction.
func TestSelfAccountScope_LeavingScopingGroupRefused(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	// viewerGroup carries no allowed_accounts, so it contributes nothing to
	// the union: dropping regionalAdminGroup leaves the actor unrestricted.
	stubScopedActor(ctx, mockStore, []string{regionalAdminGroupID, viewerGroup().ID},
		regionalAdminGroup(), viewerGroup())
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Maybe()

	_, err := svc.UpdateUser(ctx, scopedActorID, scopedActorID, UpdateUserRequest{
		GroupIDs: []string{viewerGroup().ID},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSelfEscalation)
	assert.Contains(t, err.Error(), "all cloud accounts")
	mockStore.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
}

// T3 -- Control for T2: the SAME actor removing the OTHER group. viewerGroup
// contributes no accounts, so the surviving union is unchanged and the change
// must go through. A guard that refused removals wholesale, or one that only
// checked "is the resulting scope non-empty", fails here.
func TestSelfAccountScope_NonWideningRemovalAllowed(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	stubScopedActor(ctx, mockStore, []string{regionalAdminGroupID, viewerGroup().ID},
		regionalAdminGroup(), viewerGroup())
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

	updated, err := svc.UpdateUser(ctx, scopedActorID, scopedActorID, UpdateUserRequest{
		GroupIDs: []string{regionalAdminGroupID},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{regionalAdminGroupID}, updated.GroupIDs)
}

// T4 -- Control for T1: joining a group whose scope is a strict SUBSET of the
// actor's own grants nothing, so it must still be allowed. Ordinary membership
// administration inside one's own scope keeps working.
func TestSelfAccountScope_JoiningSubsetScopedGroupAllowed(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	stubScopedActor(ctx, mockStore, []string{regionalAdminGroupID},
		regionalAdminGroup(), acctAViewersGroup())
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

	updated, err := svc.UpdateUser(ctx, scopedActorID, scopedActorID, UpdateUserRequest{
		GroupIDs: []string{regionalAdminGroupID, acctAViewersGroupID},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{regionalAdminGroupID, acctAViewersGroupID}, updated.GroupIDs)
}

// T5 -- Fail closed when a PRIOR group cannot be resolved (issue #1748's
// mechanism, on this path). A silently skipped group leaves an empty prior
// union, which reads as unrestricted and turns the ceiling into a no-op on
// exactly the change it guards: this actor's real scope is [acct-A acct-B] and
// swallowing the skip would compute "unrestricted" and wave the removal
// through. The removal route is the one that matters here, because nothing is
// added and no other guard runs at all.
func TestSelfAccountScope_FailsClosedOnUnresolvablePriorGroup(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	mockStore.On("GetUserByID", ctx, scopedActorID).
		Return(&User{ID: scopedActorID, Active: true,
			GroupIDs: []string{deletedGroupID, viewerGroup().ID}}, nil)
	// The store returns (nil, nil) for a deleted group.
	mockStore.On("GetGroup", ctx, deletedGroupID).Return(nil, nil)
	mockStore.On("GetGroup", ctx, viewerGroup().ID).Return(viewerGroup(), nil)
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Maybe()

	_, err := svc.UpdateUser(ctx, scopedActorID, scopedActorID, UpdateUserRequest{
		GroupIDs: []string{viewerGroup().ID},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be loaded")
	mockStore.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
}
