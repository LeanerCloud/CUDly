package auth

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestService_HasPermission(t *testing.T) {
	ctx := context.Background()

	t.Run("admin has all permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		adminUser := &User{
			ID:       "admin-123",
			GroupIDs: []string{DefaultAdminGroupID},
		}
		adminGrp := &Group{
			ID:          DefaultAdminGroupID,
			Name:        "Administrators",
			Permissions: []Permission{{Action: ActionAdmin, Resource: ResourceAll}},
		}

		mockStore.On("GetUserByID", ctx, "admin-123").Return(adminUser, nil).Once()
		mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(adminGrp, nil).Once()

		has, err := service.HasPermission(ctx, "admin-123", ActionExecute, "aws/ec2", nil)
		require.NoError(t, err)
		assert.True(t, has)

		mockStore.AssertExpectations(t)
	})

	t.Run("user with group permission", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		regularUser := &User{
			ID:       "user-123",
			GroupIDs: []string{"group-1"},
		}

		testGroup := &Group{
			ID:   "group-1",
			Name: "AWS Team",
			Permissions: []Permission{
				{
					Action:   ActionExecute,
					Resource: ResourceAll,
					Constraints: &PermissionConstraints{
						Providers:  []string{"aws"},
						AccountIDs: []string{"123456789012"},
					},
				},
			},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(regularUser, nil).Once()
		mockStore.On("GetGroup", ctx, "group-1").Return(testGroup, nil).Once()

		constraints := &PermissionConstraints{
			Providers:  []string{"aws"},
			AccountIDs: []string{"123456789012"},
		}
		has, err := service.HasPermission(ctx, "user-123", ActionExecute, "aws/ec2", constraints)
		require.NoError(t, err)
		assert.True(t, has)

		mockStore.AssertExpectations(t)
	})

	t.Run("readonly user cannot purchase", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		readonlyUser := &User{
			ID:       "readonly-123",
			GroupIDs: []string{"readonly-group"},
		}
		readonlyGrp := &Group{
			ID:          "readonly-group",
			Name:        "Read-Only Users",
			Permissions: DefaultReadOnlyPermissions(),
		}

		mockStore.On("GetUserByID", ctx, "readonly-123").Return(readonlyUser, nil).Once()
		mockStore.On("GetGroup", ctx, "readonly-group").Return(readonlyGrp, nil).Once()

		has, err := service.HasPermission(ctx, "readonly-123", ActionExecute, "aws/ec2", nil)
		require.NoError(t, err)
		assert.False(t, has)

		mockStore.AssertExpectations(t)
	})
}

func TestService_ListGroups(t *testing.T) {
	ctx := context.Background()

	t.Run("list groups successfully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		groups := []Group{
			{ID: "group-1", Name: "AWS Team"},
			{ID: "group-2", Name: "Azure Team"},
		}

		mockStore.On("ListGroups", ctx).Return(groups, nil).Once()

		result, err := service.ListGroups(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)

		mockStore.AssertExpectations(t)
	})
}

func TestService_CreateGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("successful group creation", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		group := &Group{
			Name: "New Team",
			Permissions: []Permission{
				{Action: ActionView, Resource: ResourceRecommendations},
			},
		}

		mockStore.On("CreateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Once()

		err := service.CreateGroup(ctx, group, "admin-123")
		require.NoError(t, err)
		assert.NotEmpty(t, group.ID)
		assert.Equal(t, "admin-123", group.CreatedBy)

		mockStore.AssertExpectations(t)
	})
}

func TestService_UpdateGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("successful group update", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		group := &Group{
			ID:   "group-123",
			Name: "Updated Team",
		}

		mockStore.On("UpdateGroup", ctx, group).Return(nil).Once()

		err := service.UpdateGroup(ctx, group)
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})
}

func TestService_DeleteGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("successful group deletion", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("DeleteGroup", ctx, "group-123").Return(nil).Once()

		err := service.DeleteGroup(ctx, "group-123")
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})
}

func TestService_GetGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("get group successfully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testGroup := &Group{
			ID:   "group-123",
			Name: "Test Team",
		}

		mockStore.On("GetGroup", ctx, "group-123").Return(testGroup, nil).Once()

		group, err := service.GetGroup(ctx, "group-123")
		require.NoError(t, err)
		assert.Equal(t, "group-123", group.ID)
		assert.Equal(t, "Test Team", group.Name)

		mockStore.AssertExpectations(t)
	})

	t.Run("group not found", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetGroup", ctx, "nonexistent").Return(nil, nil).Once()

		group, err := service.GetGroup(ctx, "nonexistent")
		require.NoError(t, err)
		assert.Nil(t, group)

		mockStore.AssertExpectations(t)
	})
}

func TestService_GetUserPermissions(t *testing.T) {
	ctx := context.Background()

	t.Run("admin user gets admin permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		adminUser := &User{
			ID:       "admin-123",
			GroupIDs: []string{DefaultAdminGroupID},
		}
		adminGrp := &Group{
			ID:          DefaultAdminGroupID,
			Name:        "Administrators",
			Permissions: DefaultAdminPermissions(),
		}

		mockStore.On("GetUserByID", ctx, "admin-123").Return(adminUser, nil).Once()
		mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(adminGrp, nil).Once()

		permissions, err := service.GetUserPermissions(ctx, "admin-123")
		require.NoError(t, err)
		assert.Len(t, permissions, 1)
		assert.Equal(t, ActionAdmin, permissions[0].Action)
		assert.Equal(t, ResourceAll, permissions[0].Resource)

		mockStore.AssertExpectations(t)
	})

	t.Run("regular user gets user permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		regularUser := &User{
			ID:       "user-123",
			GroupIDs: []string{"standard-group"},
		}
		standardGrp := &Group{
			ID:          "standard-group",
			Name:        "Standard Users",
			Permissions: DefaultUserPermissions(),
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(regularUser, nil).Once()
		mockStore.On("GetGroup", ctx, "standard-group").Return(standardGrp, nil).Once()

		permissions, err := service.GetUserPermissions(ctx, "user-123")
		require.NoError(t, err)
		// 11 = 6 read/plan-author + delete:plans (PR-A #660)
		// + update:purchases (PR-A #660)
		// + cancel-own:purchases (issue #46)
		// + retry-own:purchases (issue #47)
		// + revoke-own:purchases (issue #290).
		// NOTE: approve-own removed (issue #1407, four-eyes).
		assert.Len(t, permissions, 11)

		mockStore.AssertExpectations(t)
	})

	t.Run("readonly user gets readonly permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		readonlyUser := &User{
			ID:       "readonly-123",
			GroupIDs: []string{"readonly-group"},
		}
		readonlyGrp := &Group{
			ID:          "readonly-group",
			Name:        "Read-Only Users",
			Permissions: DefaultReadOnlyPermissions(),
		}

		mockStore.On("GetUserByID", ctx, "readonly-123").Return(readonlyUser, nil).Once()
		mockStore.On("GetGroup", ctx, "readonly-group").Return(readonlyGrp, nil).Once()

		permissions, err := service.GetUserPermissions(ctx, "readonly-123")
		require.NoError(t, err)
		assert.Len(t, permissions, 3) // 3 readonly permissions

		mockStore.AssertExpectations(t)
	})

	t.Run("user with groups gets combined permissions", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		userWithGroups := &User{
			ID:       "user-123",
			GroupIDs: []string{"standard-group", "group-1", "group-2"},
		}

		standardGrp := &Group{
			ID:          "standard-group",
			Name:        "Standard Users",
			Permissions: DefaultUserPermissions(),
		}

		group1 := &Group{
			ID:   "group-1",
			Name: "AWS Team",
			Permissions: []Permission{
				{Action: ActionExecute, Resource: ResourcePlans},
			},
		}

		group2 := &Group{
			ID:   "group-2",
			Name: "Config Team",
			Permissions: []Permission{
				{Action: ActionUpdate, Resource: ResourceConfig},
			},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(userWithGroups, nil).Once()
		mockStore.On("GetGroup", ctx, "standard-group").Return(standardGrp, nil).Once()
		mockStore.On("GetGroup", ctx, "group-1").Return(group1, nil).Once()
		mockStore.On("GetGroup", ctx, "group-2").Return(group2, nil).Once()

		permissions, err := service.GetUserPermissions(ctx, "user-123")
		require.NoError(t, err)
		// 11 standard-group (incl. delete:plans (PR-A #660) + update:purchases (PR-A #660)
		// + cancel-own (#46) + retry-own (#47)
		// + revoke-own (#290):purchases; approve-own removed per #1407 four-eyes)
		// + 1 group1 + 1 group2 = 13
		assert.Len(t, permissions, 13)

		mockStore.AssertExpectations(t)
	})

	t.Run("user not found", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByID", ctx, "nonexistent").Return(nil, nil).Once()

		permissions, err := service.GetUserPermissions(ctx, "nonexistent")
		assert.Error(t, err)
		assert.Nil(t, permissions)
		assert.Contains(t, err.Error(), "user not found")

		mockStore.AssertExpectations(t)
	})

	t.Run("handles missing groups gracefully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		userWithMissingGroup := &User{
			ID:       "user-123",
			GroupIDs: []string{"standard-group", "missing-group"},
		}
		standardGrp := &Group{
			ID:          "standard-group",
			Name:        "Standard Users",
			Permissions: DefaultUserPermissions(),
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(userWithMissingGroup, nil).Once()
		mockStore.On("GetGroup", ctx, "standard-group").Return(standardGrp, nil).Once()
		mockStore.On("GetGroup", ctx, "missing-group").Return(nil, nil).Once()

		permissions, err := service.GetUserPermissions(ctx, "user-123")
		require.NoError(t, err)
		// Should have only the resolvable group's permissions; the missing
		// group is skipped.
		// 11 = 6 read/plan-author + delete:plans (PR-A #660)
		// + update:purchases (PR-A #660)
		// + cancel-own:purchases (issue #46)
		// + retry-own:purchases (issue #47)
		// + revoke-own:purchases (issue #290).
		// NOTE: approve-own removed (issue #1407, four-eyes).
		assert.Len(t, permissions, 11)

		mockStore.AssertExpectations(t)
	})

	t.Run("propagates per-group fetch error instead of returning partial permissions", func(t *testing.T) {
		// Regression test for issue #918: a transient store error on one group
		// must propagate as an error, not silently compute a partial union.
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"group-ok", "group-err"},
		}
		okGroup := &Group{
			ID:          "group-ok",
			Name:        "OK Group",
			Permissions: DefaultUserPermissions(),
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "group-ok").Return(okGroup, nil).Once()
		mockStore.On("GetGroup", ctx, "group-err").Return(nil, assert.AnError).Once()

		permissions, err := service.GetUserPermissions(ctx, "user-123")
		require.Error(t, err, "a per-group fetch error must propagate")
		assert.Nil(t, permissions, "no partial permission set must be returned")

		mockStore.AssertExpectations(t)
	})

	t.Run("skips deleted group (pgx.ErrNoRows) without error", func(t *testing.T) {
		// pgx.ErrNoRows from the store means the group row was deleted between
		// the user-load and the group-load; treat it as "missing" and skip.
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"standard-group", "deleted-group"},
		}
		standardGrp := &Group{
			ID:          "standard-group",
			Name:        "Standard Users",
			Permissions: DefaultUserPermissions(),
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "standard-group").Return(standardGrp, nil).Once()
		mockStore.On("GetGroup", ctx, "deleted-group").Return(nil, pgx.ErrNoRows).Once()

		permissions, err := service.GetUserPermissions(ctx, "user-123")
		require.NoError(t, err, "a pgx.ErrNoRows group must be skipped, not propagated")
		// Only the resolvable group's permissions; deleted group excluded.
		assert.Len(t, permissions, len(DefaultUserPermissions()))

		mockStore.AssertExpectations(t)
	})
}

func TestService_BuildAuthContext(t *testing.T) {
	ctx := context.Background()

	t.Run("admin user context", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		adminUser := &User{
			ID:       "admin-123",
			Email:    "admin@example.com",
			GroupIDs: []string{DefaultAdminGroupID},
		}
		adminGrp := &Group{
			ID:          DefaultAdminGroupID,
			Name:        "Administrators",
			Permissions: DefaultAdminPermissions(),
		}

		mockStore.On("GetUserByID", ctx, "admin-123").Return(adminUser, nil).Once()
		mockStore.On("GetGroup", ctx, DefaultAdminGroupID).Return(adminGrp, nil).Once()

		authCtx, err := service.BuildAuthContext(ctx, "admin-123")
		require.NoError(t, err)
		assert.NotNil(t, authCtx)
		assert.Equal(t, adminUser, authCtx.User)
		assert.Len(t, authCtx.Permissions, 1)
		assert.Equal(t, ActionAdmin, authCtx.Permissions[0].Action)
		assert.Empty(t, authCtx.AllowedAccounts) // No group account restrictions

		mockStore.AssertExpectations(t)
	})

	t.Run("user with group allowed accounts", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			Email:    "user@example.com",
			GroupIDs: []string{"group-1", "group-2"},
		}

		group1 := &Group{
			ID:              "group-1",
			Name:            "AWS Account 1",
			AllowedAccounts: []string{"111111111111", "222222222222"},
			Permissions: []Permission{
				{Action: ActionView, Resource: ResourceRecommendations},
			},
		}

		group2 := &Group{
			ID:              "group-2",
			Name:            "AWS Account 2",
			AllowedAccounts: []string{"222222222222", "333333333333"},
			Permissions: []Permission{
				{Action: ActionExecute, Resource: ResourcePlans},
			},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "group-1").Return(group1, nil).Once()
		mockStore.On("GetGroup", ctx, "group-2").Return(group2, nil).Once()

		authCtx, err := service.BuildAuthContext(ctx, "user-123")
		require.NoError(t, err)
		assert.NotNil(t, authCtx)
		assert.Equal(t, user, authCtx.User)
		assert.Len(t, authCtx.Groups, 2)
		// Union of accounts: 111111111111, 222222222222, 333333333333
		assert.Len(t, authCtx.AllowedAccounts, 3)
		assert.Contains(t, authCtx.AllowedAccounts, "111111111111")
		assert.Contains(t, authCtx.AllowedAccounts, "222222222222")
		assert.Contains(t, authCtx.AllowedAccounts, "333333333333")
		// Permissions derive purely from group membership: 1 group1 + 1 group2 = 2.
		assert.Len(t, authCtx.Permissions, 2)

		mockStore.AssertExpectations(t)
	})

	t.Run("user without groups has no account restrictions", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:    "user-123",
			Email: "user@example.com",
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()

		authCtx, err := service.BuildAuthContext(ctx, "user-123")
		require.NoError(t, err)
		assert.NotNil(t, authCtx)
		assert.Empty(t, authCtx.AllowedAccounts)
		// A user with no groups holds no permissions (fail closed): authz is
		// group-membership-only (issue #907).
		assert.Empty(t, authCtx.Permissions)

		mockStore.AssertExpectations(t)
	})

	t.Run("user not found returns error", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByID", ctx, "nonexistent").Return(nil, nil).Once()

		authCtx, err := service.BuildAuthContext(ctx, "nonexistent")
		assert.Error(t, err)
		assert.Nil(t, authCtx)
		assert.Contains(t, err.Error(), "user not found")

		mockStore.AssertExpectations(t)
	})

	t.Run("handles missing groups gracefully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"valid-group", "missing-group"},
		}

		validGroup := &Group{
			ID:              "valid-group",
			Name:            "Valid Group",
			AllowedAccounts: []string{"111111111111"},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "valid-group").Return(validGroup, nil).Once()
		mockStore.On("GetGroup", ctx, "missing-group").Return(nil, nil).Once()

		authCtx, err := service.BuildAuthContext(ctx, "user-123")
		require.NoError(t, err)
		assert.NotNil(t, authCtx)
		assert.Len(t, authCtx.Groups, 1) // Only valid group
		assert.Len(t, authCtx.AllowedAccounts, 1)
		assert.Contains(t, authCtx.AllowedAccounts, "111111111111")

		mockStore.AssertExpectations(t)
	})

	t.Run("propagates per-group fetch error instead of returning partial context", func(t *testing.T) {
		// Regression test for issue #918: a transient store error on one group
		// must propagate, not silently compute a partial auth context.
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"group-ok", "group-err"},
		}
		okGroup := &Group{
			ID:              "group-ok",
			Name:            "OK Group",
			AllowedAccounts: []string{"111111111111"},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "group-ok").Return(okGroup, nil).Once()
		mockStore.On("GetGroup", ctx, "group-err").Return(nil, assert.AnError).Once()

		authCtx, err := service.BuildAuthContext(ctx, "user-123")
		require.Error(t, err, "a per-group fetch error must propagate")
		assert.Nil(t, authCtx, "no partial auth context must be returned")

		mockStore.AssertExpectations(t)
	})

	t.Run("skips deleted group (pgx.ErrNoRows) without error", func(t *testing.T) {
		// pgx.ErrNoRows from the store means the group row was deleted between
		// the user-load and the group-load; treat it as "missing" and skip.
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"valid-group", "deleted-group"},
		}
		validGroup := &Group{
			ID:              "valid-group",
			Name:            "Valid Group",
			AllowedAccounts: []string{"111111111111"},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "valid-group").Return(validGroup, nil).Once()
		mockStore.On("GetGroup", ctx, "deleted-group").Return(nil, pgx.ErrNoRows).Once()

		authCtx, err := service.BuildAuthContext(ctx, "user-123")
		require.NoError(t, err, "a pgx.ErrNoRows group must be skipped, not propagated")
		assert.NotNil(t, authCtx)
		assert.Len(t, authCtx.Groups, 1, "only the present group appears")
		assert.Len(t, authCtx.AllowedAccounts, 1)
		assert.Contains(t, authCtx.AllowedAccounts, "111111111111")

		mockStore.AssertExpectations(t)
	})
}

func TestAuthContext_HasPermission(t *testing.T) {
	t.Run("admin has all permissions", func(t *testing.T) {
		// Admin capability now derives from holding the {admin, *} permission
		// (via Administrators-group membership), not a role.
		authCtx := &AuthContext{
			User:        &User{GroupIDs: []string{DefaultAdminGroupID}},
			Permissions: []Permission{{Action: ActionAdmin, Resource: ResourceAll}},
		}
		assert.True(t, authCtx.HasPermission(ActionExecute, ResourcePlans))
		assert.True(t, authCtx.HasPermission(ActionView, ResourceRecommendations))
		assert.True(t, authCtx.HasPermission(ActionAdmin, ResourceUsers))
	})

	t.Run("user with specific permission", func(t *testing.T) {
		authCtx := &AuthContext{
			User: &User{},
			Permissions: []Permission{
				{Action: ActionView, Resource: ResourceRecommendations},
				{Action: ActionExecute, Resource: ResourcePlans},
			},
		}
		assert.True(t, authCtx.HasPermission(ActionView, ResourceRecommendations))
		assert.True(t, authCtx.HasPermission(ActionExecute, ResourcePlans))
		assert.False(t, authCtx.HasPermission(ActionAdmin, ResourceUsers))
	})

	t.Run("wildcard resource permission", func(t *testing.T) {
		authCtx := &AuthContext{
			User: &User{},
			Permissions: []Permission{
				{Action: ActionView, Resource: ResourceAll},
			},
		}
		assert.True(t, authCtx.HasPermission(ActionView, ResourceRecommendations))
		assert.True(t, authCtx.HasPermission(ActionView, ResourcePlans))
		assert.True(t, authCtx.HasPermission(ActionView, ResourceHistory))
		assert.False(t, authCtx.HasPermission(ActionExecute, ResourcePlans))
	})

	t.Run("admin permission grants all", func(t *testing.T) {
		authCtx := &AuthContext{
			User: &User{},
			Permissions: []Permission{
				{Action: ActionAdmin, Resource: ResourceAll},
			},
		}
		assert.True(t, authCtx.HasPermission(ActionView, ResourceRecommendations))
		assert.True(t, authCtx.HasPermission(ActionExecute, ResourcePlans))
		assert.True(t, authCtx.HasPermission(ActionAdmin, ResourceUsers))
	})
}

func TestAuthContext_CanAccessAccount(t *testing.T) {
	t.Run("admin can access any account", func(t *testing.T) {
		authCtx := &AuthContext{
			User:            &User{},
			AllowedAccounts: []string{},
		}
		assert.True(t, authCtx.CanAccessAccount("111111111111", ""))
		assert.True(t, authCtx.CanAccessAccount("999999999999", ""))
	})

	t.Run("empty allowed accounts means all access", func(t *testing.T) {
		authCtx := &AuthContext{
			User:            &User{},
			AllowedAccounts: []string{},
		}
		assert.True(t, authCtx.CanAccessAccount("111111111111", ""))
		assert.True(t, authCtx.CanAccessAccount("999999999999", ""))
	})

	t.Run("wildcard in allowed accounts", func(t *testing.T) {
		authCtx := &AuthContext{
			User:            &User{},
			AllowedAccounts: []string{"*"},
		}
		assert.True(t, authCtx.CanAccessAccount("111111111111", ""))
		assert.True(t, authCtx.CanAccessAccount("999999999999", ""))
	})

	t.Run("specific accounts only", func(t *testing.T) {
		authCtx := &AuthContext{
			User:            &User{},
			AllowedAccounts: []string{"111111111111", "222222222222"},
		}
		assert.True(t, authCtx.CanAccessAccount("111111111111", ""))
		assert.True(t, authCtx.CanAccessAccount("222222222222", ""))
		assert.False(t, authCtx.CanAccessAccount("333333333333", ""))
		assert.False(t, authCtx.CanAccessAccount("999999999999", ""))
	})

	t.Run("readonly user with account restrictions", func(t *testing.T) {
		authCtx := &AuthContext{
			User:            &User{},
			AllowedAccounts: []string{"111111111111"},
		}
		assert.True(t, authCtx.CanAccessAccount("111111111111", ""))
		assert.False(t, authCtx.CanAccessAccount("222222222222", ""))
	})

	t.Run("match by account name", func(t *testing.T) {
		authCtx := &AuthContext{
			User:            &User{},
			AllowedAccounts: []string{"Production", "Staging"},
		}
		// UUID doesn't match, name does
		assert.True(t, authCtx.CanAccessAccount("uuid-1", "Production"))
		assert.True(t, authCtx.CanAccessAccount("uuid-2", "Staging"))
		// Neither matches
		assert.False(t, authCtx.CanAccessAccount("uuid-3", "Development"))
		// Name empty — falls back to ID-only, no match
		assert.False(t, authCtx.CanAccessAccount("uuid-4", ""))
	})

	t.Run("mixed UUID and name entries", func(t *testing.T) {
		authCtx := &AuthContext{
			User:            &User{},
			AllowedAccounts: []string{"uuid-prod", "Staging"},
		}
		assert.True(t, authCtx.CanAccessAccount("uuid-prod", "Production"))
		assert.True(t, authCtx.CanAccessAccount("uuid-stg", "Staging"))
		assert.False(t, authCtx.CanAccessAccount("uuid-dev", "Development"))
	})

	t.Run("wildcard combined with specific names", func(t *testing.T) {
		authCtx := &AuthContext{
			User:            &User{},
			AllowedAccounts: []string{"*", "Production"},
		}
		// Wildcard wins — everything matches
		assert.True(t, authCtx.CanAccessAccount("any-uuid", "Anything"))
	})
}

func TestIsUnrestrictedAccess(t *testing.T) {
	assert.True(t, IsUnrestrictedAccess(nil), "nil slice is unrestricted")
	assert.True(t, IsUnrestrictedAccess([]string{}), "empty slice is unrestricted")
	assert.True(t, IsUnrestrictedAccess([]string{"*"}), "just wildcard is unrestricted")
	assert.True(t, IsUnrestrictedAccess([]string{"foo", "*", "bar"}), "wildcard anywhere is unrestricted")
	assert.False(t, IsUnrestrictedAccess([]string{"foo"}), "specific entry is restricted")
	assert.False(t, IsUnrestrictedAccess([]string{"foo", "bar"}), "multiple specific entries are restricted")
}

func TestMatchesAccount(t *testing.T) {
	t.Run("unrestricted matches anything", func(t *testing.T) {
		assert.True(t, MatchesAccount(nil, "any", "any"))
		assert.True(t, MatchesAccount([]string{}, "any", "any"))
		assert.True(t, MatchesAccount([]string{"*"}, "any", "any"))
	})

	t.Run("match by ID", func(t *testing.T) {
		assert.True(t, MatchesAccount([]string{"uuid-1"}, "uuid-1", "SomeName"))
		assert.False(t, MatchesAccount([]string{"uuid-1"}, "uuid-2", "SomeName"))
	})

	t.Run("match by name", func(t *testing.T) {
		assert.True(t, MatchesAccount([]string{"Production"}, "uuid-1", "Production"))
		assert.False(t, MatchesAccount([]string{"Production"}, "uuid-1", "Staging"))
	})

	t.Run("empty name falls back to ID-only", func(t *testing.T) {
		assert.True(t, MatchesAccount([]string{"uuid-1"}, "uuid-1", ""))
		assert.False(t, MatchesAccount([]string{"Production"}, "uuid-1", ""))
	})

	t.Run("empty name does not match empty entry", func(t *testing.T) {
		// Edge case: allowed entry "" should not match an account with no name
		// (otherwise any accountless entry would grant access).
		assert.False(t, MatchesAccount([]string{""}, "uuid-1", ""))
	})
}

func TestService_HasPermission_Constraints(t *testing.T) {
	ctx := context.Background()

	t.Run("match account constraints", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"group-1"},
		}

		group := &Group{
			ID:   "group-1",
			Name: "AWS Account Team",
			Permissions: []Permission{
				{
					Action:   ActionExecute,
					Resource: ResourceAll,
					Constraints: &PermissionConstraints{
						AccountIDs: []string{"123456789012", "987654321098"},
					},
				},
			},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "group-1").Return(group, nil).Once()

		// Should have permission with matching account
		has, err := service.HasPermission(ctx, "user-123", ActionExecute, "aws/ec2", &PermissionConstraints{
			AccountIDs: []string{"123456789012"},
		})
		require.NoError(t, err)
		assert.True(t, has)

		mockStore.AssertExpectations(t)
	})

	t.Run("reject non-matching account constraints", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"group-1"},
		}

		group := &Group{
			ID:   "group-1",
			Name: "AWS Account Team",
			Permissions: []Permission{
				{
					Action:   ActionExecute,
					Resource: ResourceAll,
					Constraints: &PermissionConstraints{
						AccountIDs: []string{"123456789012"},
					},
				},
			},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "group-1").Return(group, nil).Once()

		// Should not have permission with non-matching account
		has, err := service.HasPermission(ctx, "user-123", ActionExecute, "aws/ec2", &PermissionConstraints{
			AccountIDs: []string{"different-account"},
		})
		require.NoError(t, err)
		assert.False(t, has)

		mockStore.AssertExpectations(t)
	})

	t.Run("match provider constraints", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"group-1"},
		}

		group := &Group{
			ID:   "group-1",
			Name: "AWS Team",
			Permissions: []Permission{
				{
					Action:   ActionExecute,
					Resource: ResourceAll,
					Constraints: &PermissionConstraints{
						Providers: []string{"aws", "azure"},
					},
				},
			},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "group-1").Return(group, nil).Once()

		has, err := service.HasPermission(ctx, "user-123", ActionExecute, "aws/ec2", &PermissionConstraints{
			Providers: []string{"aws"},
		})
		require.NoError(t, err)
		assert.True(t, has)

		mockStore.AssertExpectations(t)
	})

	t.Run("reject non-matching provider constraints", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"group-1"},
		}

		group := &Group{
			ID:   "group-1",
			Name: "AWS Only Team",
			Permissions: []Permission{
				{
					Action:   ActionExecute,
					Resource: ResourceAll,
					Constraints: &PermissionConstraints{
						Providers: []string{"aws"},
					},
				},
			},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "group-1").Return(group, nil).Once()

		has, err := service.HasPermission(ctx, "user-123", ActionExecute, "gcp/compute", &PermissionConstraints{
			Providers: []string{"gcp"},
		})
		require.NoError(t, err)
		assert.False(t, has)

		mockStore.AssertExpectations(t)
	})

	t.Run("match service constraints", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"group-1"},
		}

		group := &Group{
			ID:   "group-1",
			Name: "EC2 Team",
			Permissions: []Permission{
				{
					Action:   ActionExecute,
					Resource: ResourceAll,
					Constraints: &PermissionConstraints{
						Services: []string{"ec2", "rds"},
					},
				},
			},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "group-1").Return(group, nil).Once()

		has, err := service.HasPermission(ctx, "user-123", ActionExecute, "aws/ec2", &PermissionConstraints{
			Services: []string{"ec2"},
		})
		require.NoError(t, err)
		assert.True(t, has)

		mockStore.AssertExpectations(t)
	})

	t.Run("match region constraints", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"group-1"},
		}

		group := &Group{
			ID:   "group-1",
			Name: "US Regions Team",
			Permissions: []Permission{
				{
					Action:   ActionExecute,
					Resource: ResourceAll,
					Constraints: &PermissionConstraints{
						Regions: []string{"us-east-1", "us-west-2"},
					},
				},
			},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "group-1").Return(group, nil).Once()

		has, err := service.HasPermission(ctx, "user-123", ActionExecute, "aws/ec2", &PermissionConstraints{
			Regions: []string{"us-east-1"},
		})
		require.NoError(t, err)
		assert.True(t, has)

		mockStore.AssertExpectations(t)
	})

	t.Run("match max purchase amount constraints", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"group-1"},
		}

		group := &Group{
			ID:   "group-1",
			Name: "Budget Team",
			Permissions: []Permission{
				{
					Action:   ActionExecute,
					Resource: ResourceAll,
					Constraints: &PermissionConstraints{
						MaxPurchaseAmount: 10000.00,
					},
				},
			},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "group-1").Return(group, nil).Once()

		// Under limit should pass
		has, err := service.HasPermission(ctx, "user-123", ActionExecute, "aws/ec2", &PermissionConstraints{
			MaxPurchaseAmount: 5000.00,
		})
		require.NoError(t, err)
		assert.True(t, has)

		mockStore.AssertExpectations(t)
	})

	t.Run("reject over max purchase amount", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"group-1"},
		}

		group := &Group{
			ID:   "group-1",
			Name: "Budget Team",
			Permissions: []Permission{
				{
					Action:   ActionExecute,
					Resource: ResourceAll,
					Constraints: &PermissionConstraints{
						MaxPurchaseAmount: 10000.00,
					},
				},
			},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "group-1").Return(group, nil).Once()

		// Over limit should fail
		has, err := service.HasPermission(ctx, "user-123", ActionExecute, "aws/ec2", &PermissionConstraints{
			MaxPurchaseAmount: 15000.00,
		})
		require.NoError(t, err)
		assert.False(t, has)

		mockStore.AssertExpectations(t)
	})

	t.Run("permission with no constraints matches any request", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"group-1"},
		}

		group := &Group{
			ID:   "group-1",
			Name: "Unrestricted Team",
			Permissions: []Permission{
				{
					Action:   ActionExecute,
					Resource: ResourceAll,
					// No constraints
				},
			},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "group-1").Return(group, nil).Once()

		has, err := service.HasPermission(ctx, "user-123", ActionExecute, "any/resource", &PermissionConstraints{
			AccountIDs: []string{"any-account"},
			Providers:  []string{"any-provider"},
		})
		require.NoError(t, err)
		assert.True(t, has)

		mockStore.AssertExpectations(t)
	})

	t.Run("action mismatch returns false", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID: "user-123",
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()

		// Readonly user can view but not purchase
		has, err := service.HasPermission(ctx, "user-123", ActionExecute, ResourcePlans, nil)
		require.NoError(t, err)
		assert.False(t, has)

		mockStore.AssertExpectations(t)
	})

	t.Run("resource mismatch returns false", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		user := &User{
			ID:       "user-123",
			GroupIDs: []string{"group-1"},
		}

		group := &Group{
			ID:   "group-1",
			Name: "Plans Team",
			Permissions: []Permission{
				{
					Action:   ActionUpdate,
					Resource: ResourcePlans,
				},
			},
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(user, nil).Once()
		mockStore.On("GetGroup", ctx, "group-1").Return(group, nil).Once()

		// User can configure plans but not users
		has, err := service.HasPermission(ctx, "user-123", ActionUpdate, ResourceUsers, nil)
		require.NoError(t, err)
		assert.False(t, has)

		mockStore.AssertExpectations(t)
	})
}

func TestMatchConstraints(t *testing.T) {
	service := &Service{}

	t.Run("all empty constraints match", func(t *testing.T) {
		permConstraints := &PermissionConstraints{}
		reqConstraints := &PermissionConstraints{}
		assert.True(t, service.matchConstraints(permConstraints, reqConstraints))
	})

	t.Run("account IDs match when intersection exists", func(t *testing.T) {
		permConstraints := &PermissionConstraints{
			AccountIDs: []string{"account-1", "account-2"},
		}
		reqConstraints := &PermissionConstraints{
			AccountIDs: []string{"account-2"},
		}
		assert.True(t, service.matchConstraints(permConstraints, reqConstraints))
	})

	t.Run("account IDs don't match when no intersection", func(t *testing.T) {
		permConstraints := &PermissionConstraints{
			AccountIDs: []string{"account-1", "account-2"},
		}
		reqConstraints := &PermissionConstraints{
			AccountIDs: []string{"account-3"},
		}
		assert.False(t, service.matchConstraints(permConstraints, reqConstraints))
	})

	t.Run("providers match when intersection exists", func(t *testing.T) {
		permConstraints := &PermissionConstraints{
			Providers: []string{"aws", "azure"},
		}
		reqConstraints := &PermissionConstraints{
			Providers: []string{"azure"},
		}
		assert.True(t, service.matchConstraints(permConstraints, reqConstraints))
	})

	t.Run("services match when intersection exists", func(t *testing.T) {
		permConstraints := &PermissionConstraints{
			Services: []string{"ec2", "rds"},
		}
		reqConstraints := &PermissionConstraints{
			Services: []string{"ec2"},
		}
		assert.True(t, service.matchConstraints(permConstraints, reqConstraints))
	})

	t.Run("regions match when intersection exists", func(t *testing.T) {
		permConstraints := &PermissionConstraints{
			Regions: []string{"us-east-1", "eu-west-1"},
		}
		reqConstraints := &PermissionConstraints{
			Regions: []string{"us-east-1"},
		}
		assert.True(t, service.matchConstraints(permConstraints, reqConstraints))
	})

	t.Run("max purchase amount under limit", func(t *testing.T) {
		permConstraints := &PermissionConstraints{
			MaxPurchaseAmount: 10000.00,
		}
		reqConstraints := &PermissionConstraints{
			MaxPurchaseAmount: 5000.00,
		}
		assert.True(t, service.matchConstraints(permConstraints, reqConstraints))
	})

	t.Run("max purchase amount over limit", func(t *testing.T) {
		permConstraints := &PermissionConstraints{
			MaxPurchaseAmount: 10000.00,
		}
		reqConstraints := &PermissionConstraints{
			MaxPurchaseAmount: 15000.00,
		}
		assert.False(t, service.matchConstraints(permConstraints, reqConstraints))
	})

	t.Run("max purchase amount at exact limit", func(t *testing.T) {
		permConstraints := &PermissionConstraints{
			MaxPurchaseAmount: 10000.00,
		}
		reqConstraints := &PermissionConstraints{
			MaxPurchaseAmount: 10000.00,
		}
		assert.True(t, service.matchConstraints(permConstraints, reqConstraints))
	})

	t.Run("multiple constraint types combined", func(t *testing.T) {
		permConstraints := &PermissionConstraints{
			AccountIDs:        []string{"account-1"},
			Providers:         []string{"aws"},
			Services:          []string{"ec2"},
			Regions:           []string{"us-east-1"},
			MaxPurchaseAmount: 10000.00,
		}
		reqConstraints := &PermissionConstraints{
			AccountIDs:        []string{"account-1"},
			Providers:         []string{"aws"},
			Services:          []string{"ec2"},
			Regions:           []string{"us-east-1"},
			MaxPurchaseAmount: 5000.00,
		}
		assert.True(t, service.matchConstraints(permConstraints, reqConstraints))
	})

	t.Run("one non-matching constraint fails all", func(t *testing.T) {
		permConstraints := &PermissionConstraints{
			AccountIDs:        []string{"account-1"},
			Providers:         []string{"aws"},
			Services:          []string{"ec2"},
			Regions:           []string{"us-east-1"},
			MaxPurchaseAmount: 10000.00,
		}
		reqConstraints := &PermissionConstraints{
			AccountIDs:        []string{"account-1"},
			Providers:         []string{"aws"},
			Services:          []string{"rds"}, // Different service
			Regions:           []string{"us-east-1"},
			MaxPurchaseAmount: 5000.00,
		}
		assert.False(t, service.matchConstraints(permConstraints, reqConstraints))
	})
}
