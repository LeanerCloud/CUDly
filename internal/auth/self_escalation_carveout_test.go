package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The membership route to the #923 bypass (issue #1550's "one-request
// alternative"): an admin adding themselves to the Purchaser group.
//
// The pre-existing #907 guard gates self-added groups on update:users, which
// admin:* grants, so it passed. Closing only the group-permission write path
// would have left this open and #1550 would have auto-closed over it.
//
// As elsewhere in this package, refusal cases stub NO UpdateUser expectation:
// testify panics if the write lands, so the assertions cannot be vacuous.

const (
	selfActorID    = "44444444-4444-4444-8444-444444444444"
	otherUserID    = "55555555-5555-4555-8555-555555555555"
	adminGroupID   = DefaultAdminGroupID
	purchaserGroup = DefaultPurchaserGroupID
)

func purchaserGroupRow() *Group {
	return &Group{
		ID:            purchaserGroup,
		Name:          GroupPurchaser,
		SystemManaged: true,
		Permissions: []Permission{
			{Action: ActionExecute, Resource: ResourcePurchases},
			{Action: ActionApproveAny, Resource: ResourcePurchases},
			{Action: ActionRetryAny, Resource: ResourcePurchases},
			{Action: ActionView, Resource: ResourceHistory},
		},
	}
}

func adminGroupRow() *Group {
	return &Group{
		ID:          adminGroupID,
		Name:        "Administrators",
		Permissions: []Permission{{Action: ActionAdmin, Resource: ResourceAll}},
	}
}

// stubSelfActor wires the user row plus the groups it belongs to, so both
// HasPermission(update:users) and GetUserPermissions resolve.
func stubSelfActor(ctx context.Context, mockStore *MockStore, groupIDs []string, groups ...*Group) {
	mockStore.On("GetUserByID", ctx, selfActorID).
		Return(&User{ID: selfActorID, Active: true, GroupIDs: groupIDs}, nil)
	for _, g := range groups {
		mockStore.On("GetGroup", ctx, g.ID).Return(g, nil)
	}
}

func TestSelfCarvedOutGrant_AdminCannotJoinPurchaser(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	// A full admin, currently NOT a purchaser (the strict-SoD posture #923
	// tells admins to adopt).
	stubSelfActor(ctx, mockStore, []string{adminGroupID}, adminGroupRow(), purchaserGroupRow())

	_, err := svc.UpdateUser(ctx, selfActorID, selfActorID, UpdateUserRequest{
		GroupIDs: []string{adminGroupID, purchaserGroup},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSelfEscalation)
	// Names the group and the specific verb.
	assert.Contains(t, err.Error(), GroupPurchaser)
	assert.Contains(t, err.Error(), ActionExecute+":"+ResourcePurchases)
	mockStore.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
}

// A custom (non-seeded) group carrying a carved-out verb is blocked too: the
// guard keys off the permission, not off DefaultPurchaserGroupID.
func TestSelfCarvedOutGrant_CustomGroupWithMoneyVerbAlsoBlocked(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	custom := &Group{
		ID:          "66666666-6666-4666-8666-666666666666",
		Name:        "Ops Spenders",
		Permissions: []Permission{{Action: ActionRetryAny, Resource: ResourcePurchases}},
	}
	stubSelfActor(ctx, mockStore, []string{adminGroupID}, adminGroupRow(), custom)

	_, err := svc.UpdateUser(ctx, selfActorID, selfActorID, UpdateUserRequest{
		GroupIDs: []string{adminGroupID, custom.ID},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSelfEscalation)
	assert.Contains(t, err.Error(), ActionRetryAny+":"+ResourcePurchases)
	mockStore.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
}

// Negative control 1: an admin may still add themselves to a group that
// carries NO carved-out verb. Without this a guard that refused every
// self-addition would pass the tests above.
func TestSelfCarvedOutGrant_OrdinaryGroupStillAllowed(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	readOnly := &Group{
		ID:          "77777777-7777-4777-8777-777777777777",
		Name:        "Read-Only Users",
		Permissions: []Permission{{Action: ActionView, Resource: ResourcePlans}},
	}
	stubSelfActor(ctx, mockStore, []string{adminGroupID}, adminGroupRow(), readOnly)

	var saved *User
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).
		Run(func(args mock.Arguments) {
			u, ok := args.Get(1).(*User)
			require.True(t, ok)
			saved = u
		}).Return(nil).Once()

	_, err := svc.UpdateUser(ctx, selfActorID, selfActorID, UpdateUserRequest{
		GroupIDs: []string{adminGroupID, readOnly.ID},
	})

	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, []string{adminGroupID, readOnly.ID}, saved.GroupIDs)
}

// Negative control 2: a user who ALREADY holds the money verbs is not blocked
// from ordinary membership changes -- adding a second group carrying a verb
// they already have is not an escalation.
func TestSelfCarvedOutGrant_ExistingPurchaserNotBlocked(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	second := &Group{
		ID:          "88888888-8888-4888-8888-888888888888",
		Name:        "Second Purchasers",
		Permissions: []Permission{{Action: ActionExecute, Resource: ResourcePurchases}},
	}
	// Actor is already in Purchaser, so execute:purchases is already held.
	stubSelfActor(ctx, mockStore,
		[]string{adminGroupID, purchaserGroup},
		adminGroupRow(), purchaserGroupRow(), second)

	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

	_, err := svc.UpdateUser(ctx, selfActorID, selfActorID, UpdateUserRequest{
		GroupIDs: []string{adminGroupID, purchaserGroup, second.ID},
	})
	require.NoError(t, err)
}

// Negative control 3: the two-person control is preserved. An admin may add
// ANOTHER user to the Purchaser group; only self-edits are gated.
func TestSelfCarvedOutGrant_AdminMayAddAnotherUserToPurchaser(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	mockStore.On("GetUserByID", ctx, otherUserID).
		Return(&User{ID: otherUserID, Active: true, GroupIDs: []string{adminGroupID}}, nil)
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

	_, err := svc.UpdateUser(ctx, selfActorID, otherUserID, UpdateUserRequest{
		GroupIDs: []string{adminGroupID, purchaserGroup},
	})
	require.NoError(t, err)
	// The self-guard never runs for a cross-user edit, so the actor's own
	// permissions are never even fetched.
	mockStore.AssertNotCalled(t, "GetUserByID", ctx, selfActorID)
}

// Trusted internal callers (actorUserID == "") are unaffected, so bootstrap
// and seeding paths keep working.
func TestSelfCarvedOutGrant_InternalCallerUnaffected(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	mockStore.On("GetUserByID", ctx, selfActorID).
		Return(&User{ID: selfActorID, Active: true, GroupIDs: []string{adminGroupID}}, nil)
	mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

	_, err := svc.UpdateUser(ctx, "", selfActorID, UpdateUserRequest{
		GroupIDs: []string{adminGroupID, purchaserGroup},
	})
	require.NoError(t, err)
}

// Fail closed: an error loading a group being joined refuses the change
// rather than falling through to "allow".
func TestSelfCarvedOutGrant_FailsClosedOnGroupLoadError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	loadErr := errors.New("database unavailable")
	mockStore.On("GetUserByID", ctx, selfActorID).
		Return(&User{ID: selfActorID, Active: true, GroupIDs: []string{adminGroupID}}, nil)
	mockStore.On("GetGroup", ctx, adminGroupID).Return(adminGroupRow(), nil)
	mockStore.On("GetGroup", ctx, purchaserGroup).Return(nil, loadErr)

	_, err := svc.UpdateUser(ctx, selfActorID, selfActorID, UpdateUserRequest{
		GroupIDs: []string{adminGroupID, purchaserGroup},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load group")
	mockStore.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
}
