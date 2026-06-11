package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultPermissions(t *testing.T) {
	t.Run("DefaultAdminPermissions returns admin access", func(t *testing.T) {
		perms := DefaultAdminPermissions()
		assert.Len(t, perms, 1)
		assert.Equal(t, ActionAdmin, perms[0].Action)
		assert.Equal(t, ResourceAll, perms[0].Resource)
	})

	t.Run("DefaultUserPermissions returns user access", func(t *testing.T) {
		perms := DefaultUserPermissions()
		// 6 read/plan-author perms + delete:plans (PR-A #660)
		// + update:purchases (PR-A #660)
		// + cancel-own:purchases (issue #46)
		// + retry-own:purchases (issue #47)
		// + approve-own:purchases (issue #286)
		// + revoke-own:purchases (issue #290)
		// + sell-own:purchases (issue #292) = 13.
		assert.Len(t, perms, 13)

		actions := make(map[string]bool)
		for _, p := range perms {
			actions[p.Action+":"+p.Resource] = true
		}

		assert.True(t, actions[ActionView+":"+ResourceRecommendations])
		assert.True(t, actions[ActionView+":"+ResourcePlans])
		assert.True(t, actions[ActionView+":"+ResourcePurchases])
		assert.True(t, actions[ActionView+":"+ResourceHistory])
		assert.True(t, actions[ActionCreate+":"+ResourcePlans])
		assert.True(t, actions[ActionUpdate+":"+ResourcePlans])
		assert.True(t, actions[ActionDelete+":"+ResourcePlans])
		assert.True(t, actions[ActionUpdate+":"+ResourcePurchases])
		assert.True(t, actions[ActionCancelOwn+":"+ResourcePurchases])
		assert.True(t, actions[ActionRetryOwn+":"+ResourcePurchases])
		assert.True(t, actions[ActionApproveOwn+":"+ResourcePurchases])
		assert.True(t, actions[ActionRevokeOwn+":"+ResourcePurchases])
		assert.True(t, actions[ActionSellOwn+":"+ResourcePurchases])
	})

	t.Run("DefaultReadOnlyPermissions returns readonly access", func(t *testing.T) {
		perms := DefaultReadOnlyPermissions()
		assert.Len(t, perms, 3)

		// All should be view actions
		for _, p := range perms {
			assert.Equal(t, ActionView, p.Action)
		}
	})

	t.Run("DefaultPurchaserPermissions contains carved verbs and view grants", func(t *testing.T) {
		perms := DefaultPurchaserPermissions()
		// 3 money-spending verbs + 4 view grants = 7.
		assert.Len(t, perms, 7)

		actions := make(map[string]bool)
		for _, p := range perms {
			actions[p.Action+":"+p.Resource] = true
		}

		assert.True(t, actions[ActionExecute+":"+ResourcePurchases])
		assert.True(t, actions[ActionApproveAny+":"+ResourcePurchases])
		assert.True(t, actions[ActionRetryAny+":"+ResourcePurchases])
		assert.True(t, actions[ActionView+":"+ResourceRecommendations])
		assert.True(t, actions[ActionView+":"+ResourcePlans])
		assert.True(t, actions[ActionView+":"+ResourcePurchases])
		assert.True(t, actions[ActionView+":"+ResourceHistory])
	})
}

// TestAdminWildcardCarveOuts verifies that the admin:* permission does NOT
// cover the three money-spending verbs carved out for separation of duties
// (issue #923).
func TestAdminWildcardCarveOuts(t *testing.T) {
	adminCtx := &AuthContext{
		User: &User{},
		Permissions: []Permission{
			{Action: ActionAdmin, Resource: ResourceAll},
		},
	}

	// Admin wildcard must NOT cover the three carved-out verbs.
	assert.False(t, adminCtx.HasPermission(ActionExecute, ResourcePurchases),
		"admin:* must not cover execute:purchases (issue #923)")
	assert.False(t, adminCtx.HasPermission(ActionApproveAny, ResourcePurchases),
		"admin:* must not cover approve-any:purchases (issue #923)")
	assert.False(t, adminCtx.HasPermission(ActionRetryAny, ResourcePurchases),
		"admin:* must not cover retry-any:purchases (issue #923)")

	// Admin wildcard MUST still cover everything else.
	assert.True(t, adminCtx.HasPermission(ActionView, ResourcePurchases))
	assert.True(t, adminCtx.HasPermission(ActionCreate, ResourcePlans))
	assert.True(t, adminCtx.HasPermission(ActionDelete, ResourceUsers))
	assert.True(t, adminCtx.HasPermission(ActionCancelAny, ResourcePurchases),
		"cancel-any stays on admin (cleanup, not money-out)")
}

// TestPurchaserGroupCoversExecutePurchases verifies that a user who holds
// the Purchaser group permissions (but not admin:*) can execute, approve-any,
// and retry-any purchases.
func TestPurchaserGroupCoversExecutePurchases(t *testing.T) {
	purchaserCtx := &AuthContext{
		User:        &User{},
		Permissions: DefaultPurchaserPermissions(),
	}

	assert.True(t, purchaserCtx.HasPermission(ActionExecute, ResourcePurchases))
	assert.True(t, purchaserCtx.HasPermission(ActionApproveAny, ResourcePurchases))
	assert.True(t, purchaserCtx.HasPermission(ActionRetryAny, ResourcePurchases))

	// But not admin-only operations.
	assert.False(t, purchaserCtx.HasPermission(ActionDelete, ResourceUsers))
	assert.False(t, purchaserCtx.HasPermission(ActionAdmin, ResourceAll))
}

// TestAdminWithoutPurchaserCannotExecutePurchases verifies that admin:* alone
// (without the Purchaser group permissions) is denied execute:purchases.
func TestAdminWithoutPurchaserCannotExecutePurchases(t *testing.T) {
	adminOnlyCtx := &AuthContext{
		User: &User{},
		Permissions: []Permission{
			{Action: ActionAdmin, Resource: ResourceAll},
		},
	}

	assert.False(t, adminOnlyCtx.HasPermission(ActionExecute, ResourcePurchases),
		"admin-only context must be denied execute:purchases")
}

// TestAdminAndPurchaserCanExecutePurchases verifies that a user in both the
// Administrators group and the Purchaser group can execute purchases.
func TestAdminAndPurchaserCanExecutePurchases(t *testing.T) {
	combinedPerms := append(
		[]Permission{{Action: ActionAdmin, Resource: ResourceAll}},
		DefaultPurchaserPermissions()...,
	)
	ctx := &AuthContext{
		User:        &User{},
		Permissions: combinedPerms,
	}

	assert.True(t, ctx.HasPermission(ActionExecute, ResourcePurchases))
	assert.True(t, ctx.HasPermission(ActionApproveAny, ResourcePurchases))
	assert.True(t, ctx.HasPermission(ActionRetryAny, ResourcePurchases))
	assert.True(t, ctx.HasPermission(ActionDelete, ResourceUsers))
}
