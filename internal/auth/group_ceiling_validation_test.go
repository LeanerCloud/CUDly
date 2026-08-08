package auth

// Malformed-permission rejection (issue #1730).
//
// Separate from the ceiling tests because this guard runs BEFORE the ceiling
// and is actor-independent: a blank resource is refused whoever asks, which
// the tests assert positively rather than by implication.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestGrantCeiling_RejectsBlankActionOrResource covers the #1730 widening
// path. A blank resource is NOT caught by the ceiling: the admin:* branch
// grants any pair that is not carved out, and ("view", "") is not carved out,
// so before validateRequestedPermissions an admin wrote it straight through
// and it round-tripped through the group-edit form as the "*" wildcard.
func TestGrantCeiling_RejectsBlankActionOrResource(t *testing.T) {
	ctx := context.Background()

	blank := []struct {
		name string
		perm APIPermission
	}{
		// The escalating case: blank resource widens to "*".
		{"empty resource", APIPermission{Action: ActionView, Resource: ""}},
		{"whitespace resource", APIPermission{Action: ActionView, Resource: "   "}},
		// The non-escalating but still malformed case: blank action is
		// silently dropped by the form.
		{"empty action", APIPermission{Action: "", Resource: ResourcePurchases}},
		{"whitespace action", APIPermission{Action: "  ", Resource: ResourcePurchases}},
		{"both blank", APIPermission{Action: "", Resource: ""}},
	}

	for _, tc := range blank {
		t.Run("update/"+tc.name, func(t *testing.T) {
			mockStore := new(MockStore)
			t.Cleanup(func() { mockStore.AssertExpectations(t) })
			svc := newCeilingService(t, mockStore)

			stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Team"})
			// Permissive downstream stubs so removing validateRequestedPermissions
			// cannot kill this test by panicking on a missing stub. With them,
			// the blank permission would be accepted (admin:* grants view on
			// any resource, including "") and the assertions below catch it.
			stubActorPermissionsMaybe(ctx, mockStore, adminOnly)
			mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Maybe()

			result, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID, updateReqWith(tc.perm))

			require.Error(t, err)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, ErrInvalidPermission)
			// The refusal names the offending entry.
			assert.Contains(t, err.Error(), "entry 0")
			mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
			// Validation runs BEFORE the actor lookup, so a malformed entry is
			// refused whoever the caller is -- including a full admin, whose
			// admin:* branch would otherwise grant ("view", "") outright.
			mockStore.AssertNotCalled(t, "GetUserByID", mock.Anything, mock.Anything)
		})

		t.Run("create/"+tc.name, func(t *testing.T) {
			mockStore := new(MockStore)
			t.Cleanup(func() { mockStore.AssertExpectations(t) })
			svc := newCeilingService(t, mockStore)

			stubActorPermissionsMaybe(ctx, mockStore, adminOnly)
			mockStore.On("CreateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Maybe()

			_, err := svc.CreateGroupAPI(ctx, ceilingActorID, APICreateGroupRequest{
				Name:        "Team",
				Permissions: []APIPermission{tc.perm},
			})

			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidPermission)
			mockStore.AssertNotCalled(t, "CreateGroup", mock.Anything, mock.Anything)
			mockStore.AssertNotCalled(t, "GetUserByID", mock.Anything, mock.Anything)
		})
	}

	// A blank entry anywhere in the list refuses the WHOLE write; the valid
	// entries are not saved without it (never silently narrow).
	t.Run("a blank entry beside valid ones refuses the whole list", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Team"})

		_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID, updateReqWith(
			APIPermission{Action: ActionView, Resource: ResourcePlans},
			APIPermission{Action: ActionView, Resource: ""},
		))

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidPermission)
		assert.Contains(t, err.Error(), "entry 1")
		mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
	})

	// Negative control: a well-formed permission is still accepted, so the
	// validator is not simply refusing everything.
	t.Run("a well-formed permission is still accepted", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		stubActorPermissions(ctx, mockStore, adminOnly)
		stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Team"})

		var saved *Group
		mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).
			Run(func(args mock.Arguments) {
				g, ok := args.Get(1).(*Group)
				require.True(t, ok)
				saved = g
			}).Return(nil).Once()

		_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
			updateReqWith(APIPermission{Action: ActionView, Resource: ResourcePlans}))

		require.NoError(t, err)
		require.NotNil(t, saved)
		assert.Equal(t, []Permission{{Action: ActionView, Resource: ResourcePlans}}, saved.Permissions)
	})

	// An explicit "*" is a legitimate value and must NOT be confused with a
	// blank one; it is gated by the ceiling instead.
	t.Run("an explicit wildcard is not treated as blank", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		stubActorPermissions(ctx, mockStore, adminOnly)
		stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Team"})
		mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Once()

		_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
			updateReqWith(APIPermission{Action: ActionView, Resource: ResourceAll}))

		require.NoError(t, err, "an admin holding admin:* may grant view:*; only a BLANK resource is malformed")
	})
}
