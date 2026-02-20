package purchase

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestManager_ProcessMessage(t *testing.T) {
	ctx := context.Background()

	t.Run("invalid JSON message", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockEmail := new(MockEmailSender)
		manager := &Manager{
			config:       mockStore,
			email:        mockEmail,
			dashboardURL: "https://dashboard.example.com",
		}
		// Invalid JSON should be skipped without error
		err := manager.ProcessMessage(ctx, "plain text")
		assert.NoError(t, err)
	})

	t.Run("unknown message type", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockEmail := new(MockEmailSender)
		manager := &Manager{
			config:       mockStore,
			email:        mockEmail,
			dashboardURL: "https://dashboard.example.com",
		}
		err := manager.ProcessMessage(ctx, `{"type": "unknown_type"}`)
		assert.NoError(t, err)
	})

	t.Run("execute_purchase missing execution_id", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockEmail := new(MockEmailSender)
		manager := &Manager{
			config:       mockStore,
			email:        mockEmail,
			dashboardURL: "https://dashboard.example.com",
		}
		err := manager.ProcessMessage(ctx, `{"type": "execute_purchase"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "execution_id required")
	})

	t.Run("approve missing token", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockEmail := new(MockEmailSender)
		manager := &Manager{
			config:       mockStore,
			email:        mockEmail,
			dashboardURL: "https://dashboard.example.com",
		}
		err := manager.ProcessMessage(ctx, `{"type": "approve", "execution_id": "exec-123"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "token required")
	})

	t.Run("cancel missing token", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockEmail := new(MockEmailSender)
		manager := &Manager{
			config:       mockStore,
			email:        mockEmail,
			dashboardURL: "https://dashboard.example.com",
		}
		err := manager.ProcessMessage(ctx, `{"type": "cancel", "execution_id": "exec-123"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "token required")
	})

	t.Run("send_notification success", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockEmail := new(MockEmailSender)
		manager := &Manager{
			config:       mockStore,
			email:        mockEmail,
			dashboardURL: "https://dashboard.example.com",
		}
		mockStore.On("ListPurchasePlans", ctx).Return([]config.PurchasePlan{}, nil)

		err := manager.ProcessMessage(ctx, `{"type": "send_notification"}`)
		assert.NoError(t, err)
		mockStore.AssertExpectations(t)
	})

	t.Run("execute_purchase execution not found", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockEmail := new(MockEmailSender)
		manager := &Manager{
			config:       mockStore,
			email:        mockEmail,
			dashboardURL: "https://dashboard.example.com",
		}
		mockStore.On("GetExecutionByID", ctx, "exec-notfound").Return(nil, nil)

		err := manager.ProcessMessage(ctx, `{"type": "execute_purchase", "execution_id": "exec-notfound"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "execution not found")
	})

	t.Run("execute_purchase wrong status", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockEmail := new(MockEmailSender)
		manager := &Manager{
			config:       mockStore,
			email:        mockEmail,
			dashboardURL: "https://dashboard.example.com",
		}
		execution := &config.PurchaseExecution{
			ExecutionID: "exec-123",
			Status:      "cancelled",
		}
		mockStore.On("GetExecutionByID", ctx, "exec-123").Return(execution, nil)

		err := manager.ProcessMessage(ctx, `{"type": "execute_purchase", "execution_id": "exec-123"}`)
		// Should skip without error when status is not executable
		assert.NoError(t, err)
	})
}
