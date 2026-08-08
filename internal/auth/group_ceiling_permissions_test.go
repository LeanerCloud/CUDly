package auth

// Grant-ceiling tests (issues #1550, #1629): what a caller may hand out.
//
// Shared fixtures live in group_ceiling_fixtures_test.go. Every refusal case
// asserts that no write reached the store, with per-parameter matchers --
// a name-only AssertNotCalled can never fail (issue #1595).

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestGrantCeiling_CarvedOutNotGrantable is the #1550 attack itself: an admin
// writing the money verbs onto a group in a single request.
func TestGrantCeiling_CarvedOutNotGrantable(t *testing.T) {
	ctx := context.Background()

	carvedOut := []APIPermission{
		{Action: ActionExecute, Resource: ResourcePurchases},
		{Action: ActionApproveAny, Resource: ResourcePurchases},
		{Action: ActionRetryAny, Resource: ResourcePurchases},
	}

	for _, perm := range carvedOut {
		t.Run(perm.Action+":"+perm.Resource, func(t *testing.T) {
			mockStore := new(MockStore)
			t.Cleanup(func() { mockStore.AssertExpectations(t) })
			svc := newCeilingService(t, mockStore)

			stubActorPermissions(ctx, mockStore, adminOnly)
			stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Administrators"})

			result, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID, updateReqWith(perm))

			require.Error(t, err)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, ErrPermissionNotGrantable)
			// The refusal must name the exact permission (#1629: never
			// silently narrow the list).
			assert.Contains(t, err.Error(), perm.Action+":"+perm.Resource)
			mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
		})
	}
}

// TestGrantCeiling_CarvedOutNotGrantableByPurchaserAdmin is the load-bearing
// case. Migrations 000059/000064 backfill every Administrators member into
// the Purchaser group, so a default-deployment admin explicitly HOLDS the
// money verbs. A ceiling that only asked "do you hold it?" would let that
// admin relay the verbs onto the Administrators group and #1550 would stay
// open in the default configuration.
func TestGrantCeiling_CarvedOutNotGrantableByPurchaserAdmin(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := newCeilingService(t, mockStore)

	stubActorPermissions(ctx, mockStore, []Permission{
		{Action: ActionAdmin, Resource: ResourceAll},
		{Action: ActionExecute, Resource: ResourcePurchases},
		{Action: ActionApproveAny, Resource: ResourcePurchases},
		{Action: ActionRetryAny, Resource: ResourcePurchases},
	})
	stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Administrators"})

	_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
		updateReqWith(APIPermission{Action: ActionExecute, Resource: ResourcePurchases}))

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPermissionNotGrantable)
	mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
}

// TestGrantCeiling_CarvedOutMayBeKeptNotWidened: an unrelated edit to a group
// that already holds a money verb must not be forced to strip it, but must
// not be able to raise its cap either.
func TestGrantCeiling_CarvedOutMayBeKeptNotWidened(t *testing.T) {
	ctx := context.Background()

	t.Run("kept unchanged is allowed", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		stubActorPermissions(ctx, mockStore, adminOnly)
		stubTargetGroup(ctx, mockStore, &Group{
			ID:          ceilingTargetID,
			Name:        "Custom Purchasers",
			Permissions: []Permission{{Action: ActionExecute, Resource: ResourcePurchases}},
		})
		mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Once()

		_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID, updateReqWith(
			APIPermission{Action: ActionExecute, Resource: ResourcePurchases},
			APIPermission{Action: ActionView, Resource: ResourcePlans},
		))
		require.NoError(t, err)
	})

	t.Run("widening an existing cap is refused", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		stubActorPermissions(ctx, mockStore, adminOnly)
		stubTargetGroup(ctx, mockStore, &Group{
			ID:   ceilingTargetID,
			Name: "Custom Purchasers",
			Permissions: []Permission{{
				Action:      ActionExecute,
				Resource:    ResourcePurchases,
				Constraints: &PermissionConstraints{MaxPurchaseAmount: 100},
			}},
		})

		// Same action/resource, cap removed entirely.
		_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
			updateReqWith(APIPermission{Action: ActionExecute, Resource: ResourcePurchases}))

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPermissionNotGrantable)
		mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
	})
}

// TestGrantCeiling_CannotGrantUnheldPermission covers rule 1: you cannot hand
// out what you do not hold.
func TestGrantCeiling_CannotGrantUnheldPermission(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := newCeilingService(t, mockStore)

	// A group manager: can administer groups, but holds nothing on accounts.
	stubActorPermissions(ctx, mockStore, []Permission{
		{Action: ActionUpdate, Resource: ResourceGroups},
		{Action: ActionView, Resource: ResourcePlans},
	})
	stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Team"})

	_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
		updateReqWith(APIPermission{Action: ActionDelete, Resource: ResourceAccounts}))

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPermissionCeiling)
	assert.Contains(t, err.Error(), ActionDelete+":"+ResourceAccounts)
	mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
}

// TestGrantCeiling_CannotWidenResourceWildcard: holding view on one resource
// does not authorize granting view on every resource.
func TestGrantCeiling_CannotWidenResourceWildcard(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := newCeilingService(t, mockStore)

	stubActorPermissions(ctx, mockStore, []Permission{
		{Action: ActionUpdate, Resource: ResourceGroups},
		{Action: ActionView, Resource: ResourcePlans},
	})
	stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Team"})

	_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
		updateReqWith(APIPermission{Action: ActionView, Resource: ResourceAll}))

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPermissionCeiling)
	assert.Contains(t, err.Error(), ActionView+":"+ResourceAll)
	mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
}

// TestGrantCeiling_InCeilingUpdateSucceeds is the negative control. Without
// it, a guard that refused every write would pass every test above.
func TestGrantCeiling_InCeilingUpdateSucceeds(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := newCeilingService(t, mockStore)

	stubActorPermissions(ctx, mockStore, []Permission{
		{Action: ActionUpdate, Resource: ResourceGroups},
		{Action: ActionView, Resource: ResourcePlans},
		{Action: ActionView, Resource: ResourceRecommendations},
	})
	stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Team"})

	var saved *Group
	mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).
		Run(func(args mock.Arguments) {
			g, ok := args.Get(1).(*Group)
			require.True(t, ok)
			saved = g
		}).Return(nil).Once()

	result, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID, updateReqWith(
		APIPermission{Action: ActionView, Resource: ResourcePlans},
		APIPermission{Action: ActionView, Resource: ResourceRecommendations},
	))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, saved)
	// The full requested list is persisted; nothing was quietly dropped.
	assert.Equal(t, []Permission{
		{Action: ActionView, Resource: ResourcePlans},
		{Action: ActionView, Resource: ResourceRecommendations},
	}, saved.Permissions)
}

// TestGrantCeiling_ConstraintContainment: a constrained holder may hand out a
// narrower grant but not a broader one.
func TestGrantCeiling_ConstraintContainment(t *testing.T) {
	ctx := context.Background()

	held := []Permission{
		{Action: ActionUpdate, Resource: ResourceGroups},
		{
			Action:   ActionExecute,
			Resource: ResourceRIExchange,
			Constraints: &PermissionConstraints{
				Providers:         []string{"aws"},
				MaxPurchaseAmount: 100,
			},
		},
	}

	t.Run("narrower grant is allowed", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		stubActorPermissions(ctx, mockStore, held)
		stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Team"})
		mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Once()

		_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
			updateReqWith(APIPermission{
				Action:   ActionExecute,
				Resource: ResourceRIExchange,
				Constraints: &APIPermissionConstraint{
					Providers: []string{"aws"},
					MaxAmount: 50,
				},
			}))
		require.NoError(t, err)
	})

	t.Run("dropping the cap is refused", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		stubActorPermissions(ctx, mockStore, held)
		stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Team"})

		_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
			updateReqWith(APIPermission{Action: ActionExecute, Resource: ResourceRIExchange}))

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPermissionCeiling)
		mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
	})

	t.Run("adding an unheld provider is refused", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		stubActorPermissions(ctx, mockStore, held)
		stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Team"})

		_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
			updateReqWith(APIPermission{
				Action:   ActionExecute,
				Resource: ResourceRIExchange,
				Constraints: &APIPermissionConstraint{
					Providers: []string{"aws", "azure"},
					MaxAmount: 50,
				},
			}))

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPermissionCeiling)
		mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
	})
}

// TestGrantCeiling_FailsClosed: if the acting principal cannot be identified
// or their permissions cannot be resolved, the write is refused rather than
// falling through to "allow".
func TestGrantCeiling_FailsClosed(t *testing.T) {
	ctx := context.Background()

	t.Run("unidentified actor", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Team"})

		_, err := svc.UpdateGroupAPI(ctx, "", ceilingTargetID,
			updateReqWith(APIPermission{Action: ActionView, Resource: ResourcePlans}))

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPermissionCeiling)
		mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
	})

	t.Run("actor permission lookup fails", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		lookupErr := errors.New("database unavailable")
		mockStore.On("GetUserByID", ctx, ceilingActorID).Return(nil, lookupErr)
		stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Team"})

		_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
			updateReqWith(APIPermission{Action: ActionView, Resource: ResourcePlans}))

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPermissionCeiling)
		mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
	})
}

// TestGrantCeiling_AdminAPIKeyActor: the stateless admin API key has no user
// row. It is measured as a bare {admin, *} holder, so it can seed ordinary
// groups but is subject to the same money carve-out as a human admin
// (#1550's third vector).
func TestGrantCeiling_AdminAPIKeyActor(t *testing.T) {
	ctx := context.Background()

	t.Run("may create an ordinary group", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		mockStore.On("CreateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Once()

		_, err := svc.CreateGroupAPI(ctx, AdminAPIKeyActorID, APICreateGroupRequest{
			Name:        "Viewers",
			Permissions: []APIPermission{{Action: ActionView, Resource: ResourcePlans}},
		})
		require.NoError(t, err)
		// No user lookup is attempted for the key.
		mockStore.AssertNotCalled(t, "GetUserByID", mock.Anything, mock.Anything)
	})

	t.Run("may not grant a carved-out permission", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		_, err := svc.CreateGroupAPI(ctx, AdminAPIKeyActorID, APICreateGroupRequest{
			Name:        "Shadow Purchasers",
			Permissions: []APIPermission{{Action: ActionExecute, Resource: ResourcePurchases}},
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPermissionNotGrantable)
		mockStore.AssertNotCalled(t, "CreateGroup", mock.Anything, mock.Anything)
	})
}

// TestGrantCeiling_CreateGroupAPI covers the create path's ceiling directly.
func TestGrantCeiling_CreateGroupAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("refuses an unheld permission", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		stubActorPermissions(ctx, mockStore, []Permission{
			{Action: ActionCreate, Resource: ResourceGroups},
			{Action: ActionView, Resource: ResourcePlans},
		})

		_, err := svc.CreateGroupAPI(ctx, ceilingActorID, APICreateGroupRequest{
			Name:        "Escalated",
			Permissions: []APIPermission{{Action: ActionAdmin, Resource: ResourceAll}},
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPermissionCeiling)
		assert.Contains(t, err.Error(), ActionAdmin+":"+ResourceAll)
		mockStore.AssertNotCalled(t, "CreateGroup", mock.Anything, mock.Anything)
	})

	t.Run("allows an in-ceiling permission", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		stubActorPermissions(ctx, mockStore, []Permission{
			{Action: ActionCreate, Resource: ResourceGroups},
			{Action: ActionView, Resource: ResourcePlans},
		})
		mockStore.On("CreateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Once()

		_, err := svc.CreateGroupAPI(ctx, ceilingActorID, APICreateGroupRequest{
			Name:        "Plan Viewers",
			Permissions: []APIPermission{{Action: ActionView, Resource: ResourcePlans}},
		})
		require.NoError(t, err)
	})
}
