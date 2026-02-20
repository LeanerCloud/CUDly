package auth

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestConversionHelpers(t *testing.T) {
	t.Run("userToAPIUser with nil", func(t *testing.T) {
		result := userToAPIUser(nil)
		assert.Nil(t, result)
	})

	t.Run("userToAPIUser with user", func(t *testing.T) {
		now := time.Now()
		user := &User{
			ID:         "user-123",
			Email:      "test@example.com",
			Role:       RoleUser,
			GroupIDs:   []string{"group-1", "group-2"},
			MFAEnabled: true,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		result := userToAPIUser(user)
		assert.NotNil(t, result)
		assert.Equal(t, "user-123", result.ID)
		assert.Equal(t, "test@example.com", result.Email)
		assert.Equal(t, RoleUser, result.Role)
		assert.Equal(t, []string{"group-1", "group-2"}, result.Groups)
		assert.True(t, result.MFAEnabled)
		assert.NotEmpty(t, result.CreatedAt)
		assert.NotEmpty(t, result.UpdatedAt)
	})

	t.Run("groupToAPIGroup with nil", func(t *testing.T) {
		result := groupToAPIGroup(nil)
		assert.Nil(t, result)
	})

	t.Run("groupToAPIGroup with group", func(t *testing.T) {
		now := time.Now()
		group := &Group{
			ID:          "group-123",
			Name:        "Test Group",
			Description: "Test description",
			Permissions: []Permission{
				{
					Action:   ActionView,
					Resource: ResourceRecommendations,
					Constraints: &PermissionConstraints{
						AccountIDs:        []string{"account-1"},
						Providers:         []string{"aws"},
						Services:          []string{"ec2"},
						Regions:           []string{"us-east-1"},
						MaxPurchaseAmount: 10000.00,
					},
				},
			},
			CreatedAt: now,
			UpdatedAt: now,
		}
		result := groupToAPIGroup(group)
		assert.NotNil(t, result)
		assert.Equal(t, "group-123", result.ID)
		assert.Equal(t, "Test Group", result.Name)
		assert.Equal(t, "Test description", result.Description)
		assert.Len(t, result.Permissions, 1)
		assert.Equal(t, ActionView, result.Permissions[0].Action)
		assert.Equal(t, ResourceRecommendations, result.Permissions[0].Resource)
		assert.NotNil(t, result.Permissions[0].Constraints)
		assert.Equal(t, []string{"account-1"}, result.Permissions[0].Constraints.Accounts)
		assert.Equal(t, []string{"aws"}, result.Permissions[0].Constraints.Providers)
		assert.Equal(t, []string{"ec2"}, result.Permissions[0].Constraints.Services)
		assert.Equal(t, []string{"us-east-1"}, result.Permissions[0].Constraints.Regions)
		assert.Equal(t, 10000.00, result.Permissions[0].Constraints.MaxAmount)
	})

	t.Run("permissionToAPIPermission without constraints", func(t *testing.T) {
		perm := Permission{
			Action:   ActionPurchase,
			Resource: ResourcePlans,
		}
		result := permissionToAPIPermission(perm)
		assert.Equal(t, ActionPurchase, result.Action)
		assert.Equal(t, ResourcePlans, result.Resource)
		assert.Nil(t, result.Constraints)
	})

	t.Run("apiPermissionToPermission with constraints", func(t *testing.T) {
		apiPerm := APIPermission{
			Action:   ActionConfigure,
			Resource: ResourceConfig,
			Constraints: &APIPermissionConstraint{
				Accounts:  []string{"account-2"},
				Providers: []string{"azure"},
				Services:  []string{"vm"},
				Regions:   []string{"eastus"},
				MaxAmount: 5000.00,
			},
		}
		result := apiPermissionToPermission(apiPerm)
		assert.Equal(t, ActionConfigure, result.Action)
		assert.Equal(t, ResourceConfig, result.Resource)
		assert.NotNil(t, result.Constraints)
		assert.Equal(t, []string{"account-2"}, result.Constraints.AccountIDs)
		assert.Equal(t, []string{"azure"}, result.Constraints.Providers)
		assert.Equal(t, []string{"vm"}, result.Constraints.Services)
		assert.Equal(t, []string{"eastus"}, result.Constraints.Regions)
		assert.Equal(t, 5000.00, result.Constraints.MaxPurchaseAmount)
	})

	t.Run("apiPermissionToPermission without constraints", func(t *testing.T) {
		apiPerm := APIPermission{
			Action:   ActionView,
			Resource: ResourceHistory,
		}
		result := apiPermissionToPermission(apiPerm)
		assert.Equal(t, ActionView, result.Action)
		assert.Equal(t, ResourceHistory, result.Resource)
		assert.Nil(t, result.Constraints)
	})
}

// Test API adapter methods
func TestService_CreateUserAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("successful user creation", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetUserByEmail", ctx, "newuser@example.com").Return(nil, nil).Once()
		mockStore.On("CreateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		req := APICreateUserRequest{
			Email:    "newuser@example.com",
			Password: "SecurePass@123",
			Role:     RoleUser,
			Groups:   []string{"group-1"},
		}

		result, err := service.CreateUserAPI(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, result)

		apiUser, ok := result.(*APIUser)
		assert.True(t, ok)
		assert.Equal(t, "newuser@example.com", apiUser.Email)
		assert.Equal(t, RoleUser, apiUser.Role)
		assert.Equal(t, []string{"group-1"}, apiUser.Groups)

		mockStore.AssertExpectations(t)
	})

	t.Run("invalid request type", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		result, err := service.CreateUserAPI(ctx, "invalid")
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid request type")
	})
}

func TestService_UpdateUserAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("successful user update", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		existingUser := &User{
			ID:    "user-123",
			Email: "test@example.com",
			Role:  RoleUser,
		}

		mockStore.On("GetUserByID", ctx, "user-123").Return(existingUser, nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		req := APIUpdateUserRequest{
			Role:   RoleAdmin,
			Groups: []string{"group-2"},
		}

		result, err := service.UpdateUserAPI(ctx, "user-123", req)
		require.NoError(t, err)
		assert.NotNil(t, result)

		apiUser, ok := result.(*APIUser)
		assert.True(t, ok)
		assert.Equal(t, RoleAdmin, apiUser.Role)

		mockStore.AssertExpectations(t)
	})

	t.Run("invalid request type", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		result, err := service.UpdateUserAPI(ctx, "user-123", "invalid")
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid request type")
	})
}

func TestService_ListUsersAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("list users successfully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		users := []User{
			{ID: "user-1", Email: "user1@example.com", Role: RoleUser, CreatedAt: time.Now(), UpdatedAt: time.Now()},
			{ID: "user-2", Email: "user2@example.com", Role: RoleAdmin, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		}

		mockStore.On("ListUsers", ctx).Return(users, nil).Once()

		result, err := service.ListUsersAPI(ctx)
		require.NoError(t, err)
		assert.NotNil(t, result)

		apiUsers, ok := result.([]*APIUser)
		assert.True(t, ok)
		assert.Len(t, apiUsers, 2)
		assert.Equal(t, "user1@example.com", apiUsers[0].Email)
		assert.Equal(t, "user2@example.com", apiUsers[1].Email)

		mockStore.AssertExpectations(t)
	})

	t.Run("list users with error", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("ListUsers", ctx).Return(nil, fmt.Errorf("database error")).Once()

		result, err := service.ListUsersAPI(ctx)
		assert.Error(t, err)
		assert.Nil(t, result)

		mockStore.AssertExpectations(t)
	})
}

func TestService_ChangePasswordAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("successful password change", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testUser := createTestUser(t, "OldPassword123")

		mockStore.On("GetUserByID", ctx, "user-123").Return(testUser, nil).Once()
		mockStore.On("DeleteUserSessions", ctx, "user-123").Return(nil).Once()
		mockStore.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil).Once()

		err := service.ChangePasswordAPI(ctx, "user-123", "OldPassword123", "SecureTest@456")
		require.NoError(t, err)

		mockStore.AssertExpectations(t)
	})
}

func TestService_CreateGroupAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("successful group creation", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("CreateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Once()

		req := APICreateGroupRequest{
			Name:        "Test Group",
			Description: "Test description",
			Permissions: []APIPermission{
				{
					Action:   ActionView,
					Resource: ResourceRecommendations,
				},
			},
		}

		result, err := service.CreateGroupAPI(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, result)

		apiGroup, ok := result.(*APIGroup)
		assert.True(t, ok)
		assert.Equal(t, "Test Group", apiGroup.Name)
		assert.Equal(t, "Test description", apiGroup.Description)
		assert.Len(t, apiGroup.Permissions, 1)

		mockStore.AssertExpectations(t)
	})

	t.Run("invalid request type", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		result, err := service.CreateGroupAPI(ctx, "invalid")
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid request type")
	})
}

func TestService_UpdateGroupAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("successful group update", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		existingGroup := &Group{
			ID:          "group-123",
			Name:        "Old Name",
			Description: "Old description",
			Permissions: []Permission{},
		}

		mockStore.On("GetGroup", ctx, "group-123").Return(existingGroup, nil).Once()
		mockStore.On("UpdateGroup", ctx, mock.AnythingOfType("*auth.Group")).Return(nil).Once()

		req := APIUpdateGroupRequest{
			Name:        "New Name",
			Description: "New description",
			Permissions: []APIPermission{
				{
					Action:   ActionPurchase,
					Resource: ResourcePlans,
				},
			},
		}

		result, err := service.UpdateGroupAPI(ctx, "group-123", req)
		require.NoError(t, err)
		assert.NotNil(t, result)

		apiGroup, ok := result.(*APIGroup)
		assert.True(t, ok)
		assert.Equal(t, "New Name", apiGroup.Name)
		assert.Equal(t, "New description", apiGroup.Description)
		assert.Len(t, apiGroup.Permissions, 1)

		mockStore.AssertExpectations(t)
	})

	t.Run("invalid request type", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		result, err := service.UpdateGroupAPI(ctx, "group-123", "invalid")
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid request type")
	})

	t.Run("group not found", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetGroup", ctx, "group-123").Return(nil, nil).Once()

		req := APIUpdateGroupRequest{
			Name: "New Name",
		}

		result, err := service.UpdateGroupAPI(ctx, "group-123", req)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "group not found")

		mockStore.AssertExpectations(t)
	})
}

func TestService_GetGroupAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("get group successfully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		testGroup := &Group{
			ID:          "group-123",
			Name:        "Test Group",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
			Permissions: []Permission{},
		}

		mockStore.On("GetGroup", ctx, "group-123").Return(testGroup, nil).Once()

		result, err := service.GetGroupAPI(ctx, "group-123")
		require.NoError(t, err)
		assert.NotNil(t, result)

		apiGroup, ok := result.(*APIGroup)
		assert.True(t, ok)
		assert.Equal(t, "group-123", apiGroup.ID)
		assert.Equal(t, "Test Group", apiGroup.Name)

		mockStore.AssertExpectations(t)
	})

	t.Run("group not found", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetGroup", ctx, "group-123").Return(nil, nil).Once()

		result, err := service.GetGroupAPI(ctx, "group-123")
		require.NoError(t, err)
		assert.Nil(t, result)

		mockStore.AssertExpectations(t)
	})

	t.Run("return error when GetGroup fails", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("GetGroup", ctx, "group-123").Return(nil, assert.AnError).Once()

		result, err := service.GetGroupAPI(ctx, "group-123")
		assert.Error(t, err)
		assert.Nil(t, result)

		mockStore.AssertExpectations(t)
	})
}

func TestService_ListGroupsAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("list groups successfully", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		groups := []Group{
			{ID: "group-1", Name: "AWS Team", CreatedAt: time.Now(), UpdatedAt: time.Now(), Permissions: []Permission{}},
			{ID: "group-2", Name: "Azure Team", CreatedAt: time.Now(), UpdatedAt: time.Now(), Permissions: []Permission{}},
		}

		mockStore.On("ListGroups", ctx).Return(groups, nil).Once()

		result, err := service.ListGroupsAPI(ctx)
		require.NoError(t, err)
		assert.NotNil(t, result)

		apiGroups, ok := result.([]*APIGroup)
		assert.True(t, ok)
		assert.Len(t, apiGroups, 2)
		assert.Equal(t, "AWS Team", apiGroups[0].Name)
		assert.Equal(t, "Azure Team", apiGroups[1].Name)

		mockStore.AssertExpectations(t)
	})

	t.Run("list groups with error", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		mockStore.On("ListGroups", ctx).Return(nil, fmt.Errorf("database error")).Once()

		result, err := service.ListGroupsAPI(ctx)
		assert.Error(t, err)
		assert.Nil(t, result)

		mockStore.AssertExpectations(t)
	})
}

func TestService_HasPermissionAPI(t *testing.T) {
	ctx := context.Background()

	t.Run("admin has permission", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		adminUser := &User{
			ID:   "admin-123",
			Role: RoleAdmin,
		}

		mockStore.On("GetUserByID", ctx, "admin-123").Return(adminUser, nil).Once()

		has, err := service.HasPermissionAPI(ctx, "admin-123", ActionPurchase, ResourcePlans)
		require.NoError(t, err)
		assert.True(t, has)

		mockStore.AssertExpectations(t)
	})

	t.Run("regular user lacks permission", func(t *testing.T) {
		mockStore := new(MockStore)
		mockEmail := new(MockEmailSender)
		service := createTestService(mockStore, mockEmail)

		readonlyUser := &User{
			ID:   "readonly-123",
			Role: RoleReadOnly,
		}

		mockStore.On("GetUserByID", ctx, "readonly-123").Return(readonlyUser, nil).Once()

		has, err := service.HasPermissionAPI(ctx, "readonly-123", ActionPurchase, ResourcePlans)
		require.NoError(t, err)
		assert.False(t, has)

		mockStore.AssertExpectations(t)
	})
}

// Test error paths and edge cases
