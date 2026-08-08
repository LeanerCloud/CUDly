package auth

import (
	"context"
	"testing"
)

// Shared fixtures for the group-ceiling test files (issues #1550, #1629,
// #1730). Split out of group_ceiling_test.go, which exceeded the repo's
// 500-line guideline.
//
// Every refusal case here deliberately stubs NO CreateGroup / UpdateGroup /
// DeleteGroup expectation on the mock store. testify's mock panics on an
// un-stubbed call, so if the guard under test were removed the write would
// reach the store and the test would fail loudly rather than silently pass.
// That is the mutation check: these assertions are not vacuous.

const (
	ceilingActorID      = "11111111-1111-4111-8111-111111111111"
	ceilingActorGroupID = "22222222-2222-4222-8222-222222222222"
	ceilingTargetID     = "33333333-3333-4333-8333-333333333333"
)

// stubActorPermissions wires the two store lookups GetUserPermissions makes
// for the acting user: the user row, then each of its groups.
func stubActorPermissions(ctx context.Context, mockStore *MockStore, perms []Permission) {
	mockStore.On("GetUserByID", ctx, ceilingActorID).
		Return(&User{ID: ceilingActorID, GroupIDs: []string{ceilingActorGroupID}}, nil)
	mockStore.On("GetGroup", ctx, ceilingActorGroupID).
		Return(&Group{ID: ceilingActorGroupID, Name: "Actor Group", Permissions: perms}, nil)
}

// stubActorPermissionsMaybe is the .Maybe() form, for tests where the guard
// under test must return BEFORE the actor is resolved. Registering the reads
// permissively means a removed guard is caught by the test's own assertions
// rather than by an incidental panic on a missing stub -- a kill that a later
// fixture tidy-up would silently delete.
func stubActorPermissionsMaybe(ctx context.Context, mockStore *MockStore, perms []Permission) {
	mockStore.On("GetUserByID", ctx, ceilingActorID).
		Return(&User{ID: ceilingActorID, GroupIDs: []string{ceilingActorGroupID}}, nil).Maybe()
	mockStore.On("GetGroup", ctx, ceilingActorGroupID).
		Return(&Group{ID: ceilingActorGroupID, Name: "Actor Group", Permissions: perms}, nil).Maybe()
}

func stubTargetGroup(ctx context.Context, mockStore *MockStore, group *Group) {
	mockStore.On("GetGroup", ctx, ceilingTargetID).Return(group, nil)
}

func newCeilingService(t *testing.T, mockStore *MockStore) *Service {
	t.Helper()
	return createTestService(mockStore, new(MockEmailSender))
}

func updateReqWith(perms ...APIPermission) APIUpdateGroupRequest {
	return APIUpdateGroupRequest{Name: "Renamed", Permissions: perms}
}

var adminOnly = []Permission{{Action: ActionAdmin, Resource: ResourceAll}}
