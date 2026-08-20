package auth

// Malformed-permission rejection (issue #1730).
//
// Separate from the ceiling tests because this guard runs BEFORE the ceiling
// and is actor-independent: a blank resource is refused whoever asks, which
// the tests assert positively rather than by implication.

import (
	"context"
	"fmt"
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

// TestGrantCeiling_RejectsBlankConstraintEntries covers the constraint-list
// half of the same rule (issue #1629).
//
// A blank entry and an absent one mean opposite things at enforcement --
// matchStringListConstraints reads an empty list as "no restriction"
// (allow everything) while a list holding only "" matches nothing (deny
// everything) -- and the group-edit form's comma-joined text encoding
// cannot represent the difference. The ceiling does not catch this either:
// the admin:* branch of grantCeilingAllows covers any requested constraint
// set, so before this guard a blank entry was written straight through.
func TestGrantCeiling_RejectsBlankConstraintEntries(t *testing.T) {
	ctx := context.Background()

	blank := []struct {
		name       string
		dimension  string
		constraint APIPermissionConstraint
		position   int
	}{
		{"empty account id", "accounts", APIPermissionConstraint{Accounts: []string{""}}, 0},
		{"whitespace account id beside a real one", "accounts", APIPermissionConstraint{Accounts: []string{"acct-1", "  "}}, 1},
		{"empty provider", "providers", APIPermissionConstraint{Providers: []string{""}}, 0},
		{"tab-only service", "services", APIPermissionConstraint{Services: []string{"\t"}}, 0},
		{"empty region beside a real one", "regions", APIPermissionConstraint{Regions: []string{"us-east-1", ""}}, 1},
	}

	for _, tc := range blank {
		perm := APIPermission{Action: ActionView, Resource: ResourcePlans, Constraints: &tc.constraint}

		t.Run("update/"+tc.name, func(t *testing.T) {
			mockStore := new(MockStore)
			t.Cleanup(func() { mockStore.AssertExpectations(t) })
			svc := newCeilingService(t, mockStore)

			stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Team"})
			// Permissive downstream stubs, same reasoning as the blank
			// action/resource test above: with them, removing the guard makes
			// the write SUCCEED (admin:* covers any constraint set) and the
			// assertions below catch it, rather than the test passing by
			// accident on a missing-stub panic.
			stubActorPermissionsMaybe(ctx, mockStore, adminOnly)
			mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Maybe()

			result, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID, updateReqWith(perm))

			require.Error(t, err)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, ErrInvalidPermission)
			// The refusal names the offending entry, the dimension AND the
			// position within it, so the client can find the value without
			// guessing. Asserting the position matters because two of these
			// cases put the blank at index 1: reporting the permission index
			// where the value index belongs would otherwise go unnoticed.
			assert.Contains(t, err.Error(), "entry 0")
			assert.Contains(t, err.Error(), tc.dimension)
			assert.Contains(t, err.Error(), fmt.Sprintf("at position %d", tc.position))
			mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
		})

		t.Run("create/"+tc.name, func(t *testing.T) {
			mockStore := new(MockStore)
			t.Cleanup(func() { mockStore.AssertExpectations(t) })
			svc := newCeilingService(t, mockStore)

			stubActorPermissionsMaybe(ctx, mockStore, adminOnly)
			mockStore.On("CreateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Maybe()

			_, err := svc.CreateGroupAPI(ctx, ceilingActorID, APICreateGroupRequest{
				Name:        "Team",
				Permissions: []APIPermission{perm},
			})

			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidPermission)
			assert.Contains(t, err.Error(), tc.dimension)
			mockStore.AssertNotCalled(t, "CreateGroup", mock.Anything, mock.Anything)
		})
	}

	// Negative controls: the guard must not refuse the ordinary constrained
	// permissions this endpoint exists to write.
	accepted := []struct {
		name       string
		constraint APIPermissionConstraint
		expect     PermissionConstraints
	}{
		{
			"a populated multi-value list on every dimension",
			APIPermissionConstraint{
				Accounts:  []string{"acct-1", "acct-2"},
				Providers: []string{"aws", "azure"},
				Services:  []string{"ec2"},
				Regions:   []string{"us-east-1", "us-west-2"},
				MaxAmount: 5000,
			},
			PermissionConstraints{
				AccountIDs:        []string{"acct-1", "acct-2"},
				Providers:         []string{"aws", "azure"},
				Services:          []string{"ec2"},
				Regions:           []string{"us-east-1", "us-west-2"},
				MaxPurchaseAmount: 5000,
			},
		},
		{
			// An EMPTY list is "no restriction on this dimension", a
			// legitimate state, and must not be confused with a blank entry.
			"an empty list is not a blank entry",
			APIPermissionConstraint{Accounts: []string{}, Providers: []string{"aws"}},
			PermissionConstraints{AccountIDs: []string{}, Providers: []string{"aws"}},
		},
		{
			// Blank means empty-after-trimming, not "contains whitespace".
			// Enforcement compares these exactly, so trimming or refusing
			// them here would diverge from what the value actually does.
			"a value containing spaces is not blank",
			APIPermissionConstraint{Accounts: []string{" acct A "}},
			PermissionConstraints{AccountIDs: []string{" acct A "}},
		},
	}

	for _, tc := range accepted {
		t.Run("accepted/"+tc.name, func(t *testing.T) {
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

			constraint := tc.constraint
			_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID, updateReqWith(
				APIPermission{Action: ActionView, Resource: ResourcePlans, Constraints: &constraint}))

			require.NoError(t, err)
			require.NotNil(t, saved)
			require.Len(t, saved.Permissions, 1)
			require.NotNil(t, saved.Permissions[0].Constraints)
			// The constraint reaches the store unchanged: the guard refuses or
			// passes through, it never normalizes.
			assert.Equal(t, tc.expect, *saved.Permissions[0].Constraints)
		})
	}
}
