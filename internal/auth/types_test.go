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
		assert.Len(t, perms, 6)

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
	})

	t.Run("DefaultReadOnlyPermissions returns readonly access", func(t *testing.T) {
		perms := DefaultReadOnlyPermissions()
		assert.Len(t, perms, 3)

		// All should be view actions
		for _, p := range perms {
			assert.Equal(t, ActionView, p.Action)
		}
	})
}
