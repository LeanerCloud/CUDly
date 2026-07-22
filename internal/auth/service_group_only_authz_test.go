package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// adminGroup returns the seeded Administrators group ({admin, *}, allowed=['*']).
func adminGroup() *Group {
	return &Group{
		ID:              DefaultAdminGroupID,
		Name:            "Administrators",
		Permissions:     []Permission{{Action: ActionAdmin, Resource: ResourceAll}},
		AllowedAccounts: []string{"*"},
	}
}

// viewerGroup returns a read-only group with no admin capability.
func viewerGroup() *Group {
	return &Group{
		ID:          "00000000-0000-5000-8000-000000000004",
		Name:        "Viewers",
		Permissions: []Permission{{Action: ActionView, Resource: ResourceRecommendations}},
	}
}

// TestGroupOnlyAuthz_AdminEquivalence proves an Administrators-group member
// retains every capability the old role == "admin" path allowed, EXCEPT the
// money-spending verbs carved out for separation of duties (issue #923):
// HasPermission returns true for any other action/resource, and
// UserHasAdminCapability is true. See TestAdminWildcardCarveOuts in
// types_test.go for the equivalent AuthContext.HasPermission coverage.
func TestGroupOnlyAuthz_AdminEquivalence(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	svc := createTestService(mockStore, mockEmail)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	admin := &User{ID: "admin-1", GroupIDs: []string{DefaultAdminGroupID}, Active: true}
	mockStore.On("GetUserByID", ctx, "admin-1").Return(admin, nil)
	mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(adminGroup(), nil)

	for _, tc := range []struct{ action, resource string }{
		{ActionDelete, ResourceUsers},
		{ActionUpdate, ResourceConfig},
	} {
		has, err := svc.HasPermission(ctx, "admin-1", tc.action, tc.resource, nil)
		require.NoError(t, err)
		assert.Truef(t, has, "admin must hold %s on %s", tc.action, tc.resource)
	}

	// admin:* must NOT cover the carved-out money-spending verbs (issue #923).
	for _, tc := range []struct{ action, resource string }{
		{ActionExecute, ResourcePurchases},
		{ActionApproveAny, ResourcePurchases},
	} {
		has, err := svc.HasPermission(ctx, "admin-1", tc.action, tc.resource, nil)
		require.NoError(t, err)
		assert.Falsef(t, has, "admin-only must NOT hold %s on %s (issue #923)", tc.action, tc.resource)
	}

	isAdmin, err := svc.UserHasAdminCapability(ctx, "admin-1")
	require.NoError(t, err)
	assert.True(t, isAdmin)
}

// TestGroupOnlyAuthz_NonAdminDenied proves a non-admin (viewer-only) is denied
// the privileged actions the admin path allowed, and is not flagged as admin.
func TestGroupOnlyAuthz_NonAdminDenied(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	svc := createTestService(mockStore, mockEmail)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	viewer := &User{ID: "viewer-1", GroupIDs: []string{viewerGroup().ID}, Active: true}
	mockStore.On("GetUserByID", ctx, "viewer-1").Return(viewer, nil)
	mockStore.On("GetGroup", ctx, viewerGroup().ID).Return(viewerGroup(), nil)

	has, err := svc.HasPermission(ctx, "viewer-1", ActionApproveAny, ResourcePurchases, nil)
	require.NoError(t, err)
	assert.False(t, has, "viewer must NOT hold approve-any on purchases")

	isAdmin, err := svc.UserHasAdminCapability(ctx, "viewer-1")
	require.NoError(t, err)
	assert.False(t, isAdmin)
}

// TestGroupOnlyAuthz_ZeroGroupFailClosed proves a user with no groups is denied
// everything (fail closed) — no role fallback grants them anything.
func TestGroupOnlyAuthz_ZeroGroupFailClosed(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	svc := createTestService(mockStore, mockEmail)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	orphan := &User{ID: "orphan-1", GroupIDs: nil, Active: true}
	mockStore.On("GetUserByID", ctx, "orphan-1").Return(orphan, nil)

	has, err := svc.HasPermission(ctx, "orphan-1", ActionView, ResourceRecommendations, nil)
	require.NoError(t, err)
	assert.False(t, has, "a zero-group user must be denied everything")

	isAdmin, err := svc.UserHasAdminCapability(ctx, "orphan-1")
	require.NoError(t, err)
	assert.False(t, isAdmin)
}

// TestGroupOnlyAuthz_LookupErrorDenies proves a store error fails closed:
// HasPermission propagates the error (callers deny) rather than allowing.
func TestGroupOnlyAuthz_LookupErrorDenies(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	svc := createTestService(mockStore, mockEmail)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	mockStore.On("GetUserByID", ctx, "err-1").Return(nil, errors.New("db down"))

	has, err := svc.HasPermission(ctx, "err-1", ActionView, ResourceRecommendations, nil)
	require.Error(t, err)
	assert.False(t, has)
}

// TestCreateUser_RejectsZeroGroups proves user creation requires >= 1 group.
func TestCreateUser_RejectsZeroGroups(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	svc := createTestService(mockStore, mockEmail)

	// The email-uniqueness pre-check runs before the group check.
	mockStore.On("GetUserByEmail", ctx, "new@example.com").Return(nil, nil)

	_, err := svc.CreateUser(ctx, CreateUserRequest{
		Email:    "new@example.com",
		Password: "Sup3rSecretP@ssw0rd!",
		GroupIDs: nil,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoGroups)
	// CreateUser must NOT have been called on the store.
	mockStore.AssertNotCalled(t, "CreateUser", mock.Anything, mock.Anything)
}

// TestUpdateUser_RejectsZeroGroups proves an update cannot empty group_ids.
func TestUpdateUser_RejectsZeroGroups(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	svc := createTestService(mockStore, mockEmail)

	existing := &User{ID: "u1", GroupIDs: []string{viewerGroup().ID}, Active: true}
	mockStore.On("GetUserByID", ctx, "u1").Return(existing, nil)

	empty := []string{}
	_, err := svc.UpdateUser(ctx, "actor", "u1", UpdateUserRequest{GroupIDs: empty})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoGroups)
	mockStore.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
}

// TestUpdateUser_LastAdminProtection proves the last Administrators-group
// member cannot be demoted out of the group.
func TestUpdateUser_LastAdminProtection(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	svc := createTestService(mockStore, mockEmail)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	admin := &User{ID: "a1", GroupIDs: []string{DefaultAdminGroupID}, Active: true}
	mockStore.On("GetUserByID", ctx, "a1").Return(admin, nil)
	mockStore.On("CountGroupMembers", ctx, DefaultAdminGroupID).Return(1, nil)

	// Demote the only admin to a viewer group: must be refused.
	_, err := svc.UpdateUser(ctx, "", "a1", UpdateUserRequest{GroupIDs: []string{viewerGroup().ID}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLastAdmin)
	mockStore.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
}

// TestUpdateUser_DemoteAdminWhenOthersExist allows demotion when another admin
// remains, proving the guard is a last-member guard, not a blanket block.
func TestUpdateUser_DemoteAdminWhenOthersExist(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	svc := createTestService(mockStore, mockEmail)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	admin := &User{ID: "a1", GroupIDs: []string{DefaultAdminGroupID}, Active: true}
	mockStore.On("GetUserByID", ctx, "a1").Return(admin, nil)
	mockStore.On("CountGroupMembers", ctx, DefaultAdminGroupID).Return(2, nil)
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil)

	updated, err := svc.UpdateUser(ctx, "", "a1", UpdateUserRequest{GroupIDs: []string{viewerGroup().ID}})
	require.NoError(t, err)
	assert.Equal(t, []string{viewerGroup().ID}, updated.GroupIDs)
}

// TestDeleteUser_LastAdminProtection proves the last admin cannot be deleted.
func TestDeleteUser_LastAdminProtection(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	svc := createTestService(mockStore, mockEmail)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	admin := &User{ID: "a1", GroupIDs: []string{DefaultAdminGroupID}, Active: true}
	mockStore.On("GetUserByID", ctx, "a1").Return(admin, nil)
	mockStore.On("CountGroupMembers", ctx, DefaultAdminGroupID).Return(1, nil)

	err := svc.DeleteUser(ctx, "a1")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLastAdmin)
	mockStore.AssertNotCalled(t, "DeleteUser", mock.Anything, mock.Anything)
}

// TestUpdateUser_SelfEscalationDenied proves a non-privileged user cannot add a
// new group to their own membership.
func TestUpdateUser_SelfEscalationDenied(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	svc := createTestService(mockStore, mockEmail)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	// Actor == target, currently only a viewer, attempts to add the
	// Administrators group to themselves. UpdateUser fetches the target first
	// (this object is mutated in place by applyUpdateUserRequest), then the
	// self-escalation guard re-resolves the actor's *persisted* permissions
	// via a fresh GetUserByID; return a distinct, unmutated viewer copy for
	// that second read so it mirrors a real DB round-trip rather than aliasing
	// the just-mutated object.
	target := &User{ID: "self-1", GroupIDs: []string{viewerGroup().ID}, Active: true}
	actor := &User{ID: "self-1", GroupIDs: []string{viewerGroup().ID}, Active: true}
	mockStore.On("GetUserByID", ctx, "self-1").Return(target, nil).Once()
	mockStore.On("GetUserByID", ctx, "self-1").Return(actor, nil).Once()
	mockStore.On("GetGroup", ctx, viewerGroup().ID).Return(viewerGroup(), nil)
	// The change ADDS Administrators (does not remove it), so the last-admin
	// branch is skipped; the self-escalation branch then evaluates
	// HasPermission(update, users), which a viewer lacks.

	_, err := svc.UpdateUser(ctx, "self-1", "self-1",
		UpdateUserRequest{GroupIDs: []string{viewerGroup().ID, DefaultAdminGroupID}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSelfEscalation)
	mockStore.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
}

// TestUpdateUser_AdminEditingSelfAllowed proves a privileged actor (manage-users)
// can change their own groups (no self-escalation block).
func TestUpdateUser_AdminEditingSelfAllowed(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	mockEmail := new(MockEmailSender)
	svc := createTestService(mockStore, mockEmail)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	admin := &User{ID: "adm", GroupIDs: []string{DefaultAdminGroupID}, Active: true}
	// GetUserByID is hit by UpdateUser (target) and by HasPermission (actor).
	mockStore.On("GetUserByID", ctx, "adm").Return(admin, nil)
	mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(adminGroup(), nil)
	mockStore.On("GetGroup", ctx, viewerGroup().ID).Return(viewerGroup(), nil)
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil)

	// Admin adds a viewer group to themselves while KEEPING Administrators, so
	// the last-admin branch is not triggered and the self-escalation guard
	// passes because the actor holds {admin, *} (i.e. manage-users).
	updated, err := svc.UpdateUser(ctx, "adm", "adm",
		UpdateUserRequest{GroupIDs: []string{DefaultAdminGroupID, viewerGroup().ID}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{DefaultAdminGroupID, viewerGroup().ID}, updated.GroupIDs)
}
