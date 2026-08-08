package auth

// system_managed immutability (issue #1629).
//
// The seeded groups are owned by migrations; no API verb may reshape or drop
// them. Covers both the update and the delete path -- the delete path is a
// third write path to a group's permissions that neither #1550 nor #1629
// named.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestSystemManagedGroup_Immutable covers the second half of #1629: the
// seeded groups are owned by migrations and no API verb may reshape them.
func TestSystemManagedGroup_Immutable(t *testing.T) {
	ctx := context.Background()

	seeded := func() *Group {
		return &Group{
			ID:            DefaultPurchaserGroupID,
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

	t.Run("update is refused", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		mockStore.On("GetGroup", ctx, DefaultPurchaserGroupID).Return(seeded(), nil)
		// Permissive actor stubs, deliberately. Without them, removing the
		// system_managed guard kills this test by panicking on an unstubbed
		// GetUserByID -- a kill that evaporates the moment someone adds a
		// stub while tidying fixtures. With them, the guard's removal is
		// caught by the assertions below instead, which is the property.
		mockStore.On("GetUserByID", ctx, ceilingActorID).
			Return(&User{ID: ceilingActorID, GroupIDs: []string{ceilingActorGroupID}}, nil).Maybe()
		mockStore.On("GetGroup", ctx, ceilingActorGroupID).
			Return(&Group{ID: ceilingActorGroupID, Permissions: adminOnly}, nil).Maybe()
		mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Maybe()

		// The exact #1629 payload: approve-any/retry-any dropped, view
		// widened to the wildcard.
		result, err := svc.UpdateGroupAPI(ctx, ceilingActorID, DefaultPurchaserGroupID,
			APIUpdateGroupRequest{
				Description: "cosmetic edit",
				Permissions: []APIPermission{
					{Action: ActionExecute, Resource: ResourcePurchases},
					{Action: ActionView, Resource: ResourceAll},
				},
			})

		require.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrSystemManagedGroup)
		assert.Contains(t, err.Error(), GroupPurchaser)
		mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
	})

	t.Run("delete is refused", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		mockStore.On("GetGroup", ctx, DefaultPurchaserGroupID).Return(seeded(), nil)

		err := svc.DeleteGroup(ctx, DefaultPurchaserGroupID)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSystemManagedGroup)
		mockStore.AssertNotCalled(t, "DeleteGroup", mock.Anything, mock.Anything)
	})

	t.Run("an ordinary group is still deletable", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Team"})
		mockStore.On("DeleteGroup", ctx, ceilingTargetID).Return(nil).Once()

		require.NoError(t, svc.DeleteGroup(ctx, ceilingTargetID))
	})
}
