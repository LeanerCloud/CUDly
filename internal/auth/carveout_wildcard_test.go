package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Issue #1901 (audit A03-001..003): the carve-out set is keyed on exact
// (action, resource) pairs, while enforcement treats a stored resource of
// "*" as matching every resource. So {execute, *} was not carved out at
// grant time but granted execute:purchases at check time. Every guard that
// consults the carve-out must see the wildcard form as carved out.
//
// As elsewhere in this package, refusal cases stub NO write expectation on
// the mock store: testify panics if the write lands, so the assertions
// cannot be vacuous.

// wildcardMoneyVerbs is the attack payload from the audit reproduction.
var wildcardMoneyVerbs = []APIPermission{
	{Action: ActionExecute, Resource: ResourceAll},
	{Action: ActionApproveAny, Resource: ResourceAll},
	{Action: ActionRetryAny, Resource: ResourceAll},
}

// A03-001, the grant ceiling: an admin creating a group carrying the
// wildcard form of a money verb.
func TestGrantCeiling_WildcardMoneyVerbNotGrantable(t *testing.T) {
	ctx := context.Background()

	for _, perm := range wildcardMoneyVerbs {
		t.Run("create "+perm.Action+":"+perm.Resource, func(t *testing.T) {
			mockStore := new(MockStore)
			t.Cleanup(func() { mockStore.AssertExpectations(t) })
			svc := newCeilingService(t, mockStore)

			stubActorPermissions(ctx, mockStore, adminOnly)

			result, err := svc.CreateGroupAPI(ctx, ceilingActorID, APICreateGroupRequest{
				Name:        "Spenders",
				Permissions: []APIPermission{perm},
			})

			require.Error(t, err)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, ErrPermissionNotGrantable)
			assert.Contains(t, err.Error(), perm.Action+":"+perm.Resource)
			mockStore.AssertNotCalled(t, "CreateGroup", mock.Anything, mock.Anything)
		})
	}

	t.Run("update execute:*", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		stubActorPermissions(ctx, mockStore, adminOnly)
		stubTargetGroup(ctx, mockStore, &Group{ID: ceilingTargetID, Name: "Administrators"})

		_, err := svc.UpdateGroupAPI(ctx, ceilingActorID, ceilingTargetID,
			updateReqWith(APIPermission{Action: ActionExecute, Resource: ResourceAll}))

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPermissionNotGrantable)
		mockStore.AssertNotCalled(t, "UpdateGroup", mock.Anything, mock.Anything)
	})

	// A default-deployment admin explicitly holds the concrete money verbs
	// (migrations 000059/000064). Holding execute:purchases is not holding
	// execute:*, and the carve-out refuses the wildcard regardless.
	t.Run("purchaser admin cannot grant execute:*", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		svc := newCeilingService(t, mockStore)

		stubActorPermissions(ctx, mockStore, append(
			[]Permission{{Action: ActionAdmin, Resource: ResourceAll}},
			DefaultPurchaserPermissions()...))

		_, err := svc.CreateGroupAPI(ctx, ceilingActorID, APICreateGroupRequest{
			Name:        "Spenders",
			Permissions: []APIPermission{{Action: ActionExecute, Resource: ResourceAll}},
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPermissionNotGrantable)
		mockStore.AssertNotCalled(t, "CreateGroup", mock.Anything, mock.Anything)
	})
}

// A03-003, the self-membership guard: an admin joining a group that carries
// the wildcard form.
func TestSelfCarvedOutGrant_WildcardGroupBlocked(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	svc := createTestService(mockStore, new(MockEmailSender))

	wildcard := &Group{
		ID:          "88888888-8888-4888-8888-888888888888",
		Name:        "Wildcard Spenders",
		Permissions: []Permission{{Action: ActionExecute, Resource: ResourceAll}},
	}
	stubSelfActor(ctx, mockStore, []string{adminGroupID}, adminGroupRow(), wildcard)

	_, err := svc.UpdateUser(ctx, selfActorID, selfActorID, UpdateUserRequest{
		GroupIDs: []string{adminGroupID, wildcard.ID},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSelfEscalation)
	assert.Contains(t, err.Error(), ActionExecute+":"+ResourceAll)
	mockStore.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
}

// A03-002, API-key creation: an admin minting a key that carries the
// wildcard form, through the real CreateAPIKey path.
func TestCreateAPIKey_AdminCannotMintWildcardMoneyVerb(t *testing.T) {
	ctx := context.Background()
	adminGrp := &Group{ID: DefaultAdminGroupID, Permissions: adminOnly}

	for _, perm := range wildcardMoneyVerbs {
		t.Run(perm.Action+":"+perm.Resource, func(t *testing.T) {
			mockStore := new(MockStore)
			t.Cleanup(func() { mockStore.AssertExpectations(t) })
			service := &Service{store: mockStore}

			user := &User{ID: "user-123", Active: true, GroupIDs: []string{DefaultAdminGroupID}}
			mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
			mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(adminGrp, nil)

			_, _, err := service.CreateAPIKey(ctx, "user-123", "spender",
				[]Permission{{Action: perm.Action, Resource: perm.Resource}}, nil)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "user does not have permission")
			mockStore.AssertNotCalled(t, "CreateAPIKey", mock.Anything, mock.Anything)
		})
	}

	// Negative control: an owner whose group explicitly holds the wildcard
	// form may mint it. The refusal keys on what the owner holds, not on
	// the wildcard itself.
	t.Run("explicit execute:* holder may mint execute:*", func(t *testing.T) {
		mockStore := new(MockStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		service := &Service{store: mockStore}

		grpID := "99999999-9999-4999-8999-999999999999"
		user := &User{ID: "user-123", Active: true, GroupIDs: []string{grpID}}
		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
		mockStore.On("GetGroup", ctx, grpID).Return(&Group{
			ID:          grpID,
			Permissions: []Permission{{Action: ActionExecute, Resource: ResourceAll}},
		}, nil)
		mockStore.On("CreateAPIKey", ctx, mock.AnythingOfType("*auth.UserAPIKey")).Return(nil).Once()

		_, _, err := service.CreateAPIKey(ctx, "user-123", "spender",
			[]Permission{{Action: ActionExecute, Resource: ResourceAll}}, nil)
		require.NoError(t, err)
	})
}

// A03-002 at use time: a key that already stores the wildcard form (minted
// before the fix) must not spend for an admin owner.
func TestComputeEffectivePermissions_AdminKeyDropsWildcardMoneyVerb(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	service := &Service{store: mockStore}

	user := &User{ID: "user-123", Active: true, GroupIDs: []string{DefaultAdminGroupID}}
	mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil)
	mockStore.On("GetGroup", ctx, DefaultAdminGroupID).
		Return(&Group{ID: DefaultAdminGroupID, Permissions: adminOnly}, nil)

	key := &UserAPIKey{ID: "key-1", UserID: "user-123", Permissions: []Permission{
		{Action: ActionExecute, Resource: ResourceAll},
		{Action: ActionView, Resource: ResourcePlans},
	}}

	effective, err := service.ComputeEffectivePermissions(ctx, key, user)
	require.NoError(t, err)
	assert.Equal(t, []Permission{{Action: ActionView, Resource: ResourcePlans}}, effective)

	keyCtx := &AuthContext{User: user, Permissions: effective}
	assert.False(t, keyCtx.HasPermission(ActionExecute, ResourcePurchases))
	assert.False(t, keyCtx.HasPermission(ActionExecute, ResourceRIExchange))
}

// The two enforcement matchers, asked for the wildcard form directly.
func TestAdminWildcardCarveOuts_WildcardResource(t *testing.T) {
	adminCtx := &AuthContext{User: &User{}, Permissions: adminOnly}

	assert.False(t, adminCtx.HasPermission(ActionExecute, ResourceAll))
	assert.False(t, adminCtx.HasPermission(ActionApproveAny, ResourceAll))
	assert.False(t, adminCtx.HasPermission(ActionRetryAny, ResourceAll))
	assert.False(t, permissionsAllow(adminOnly, ActionExecute, ResourceAll, nil))
	assert.False(t, permissionsAllow(adminOnly, ActionApproveAny, ResourceAll, nil))
	assert.False(t, permissionsAllow(adminOnly, ActionRetryAny, ResourceAll, nil))

	// admin:* still covers wildcard requests for verbs that are not carved
	// out, and an explicit wildcard holder is still matched.
	assert.True(t, adminCtx.HasPermission(ActionView, ResourceAll))
	assert.True(t, permissionsAllow(adminOnly, ActionCancelAny, ResourceAll, nil))
	explicit := []Permission{{Action: ActionExecute, Resource: ResourceAll}}
	assert.True(t, (&AuthContext{User: &User{}, Permissions: explicit}).HasPermission(ActionExecute, ResourceAll))
	assert.True(t, permissionsAllow(explicit, ActionExecute, ResourcePurchases, nil))
}
