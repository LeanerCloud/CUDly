package purchase

import (
	"context"
	"fmt"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
		mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return([]config.PurchasePlan{}, nil)

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
		mockStore.On("GetExecutionByID", ctx, "exec-notfound").Return(nil, fmt.Errorf("%w: execution exec-notfound", config.ErrNotFound))

		err := manager.ProcessMessage(ctx, `{"type": "execute_purchase", "execution_id": "exec-notfound"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "execution not found")
	})

	// ─── F1 regression: AutoPurchase gate on SQS execute_purchase path ────────

	t.Run("execute_purchase pending web-source rejected (F1)", func(t *testing.T) {
		// A web-submitted pending row must not be auto-executed via SQS;
		// it must wait for the token-link approval path.
		mockStore := new(MockConfigStore)
		manager := &Manager{config: mockStore, dashboardURL: "https://example.com"}
		execution := &config.PurchaseExecution{
			ExecutionID: "exec-web",
			PlanID:      "plan-1",
			Status:      "pending",
			Source:      "web",
		}
		mockStore.On("GetExecutionByID", ctx, "exec-web").Return(execution, nil)
		// GetPurchasePlan must NOT be called (Source=web short-circuits)
		// TransitionExecutionStatus must NOT be called

		err := manager.ProcessMessage(ctx, `{"type": "execute_purchase", "execution_id": "exec-web"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not eligible for auto-execution")
		mockStore.AssertNotCalled(t, "GetPurchasePlan", mock.Anything, mock.Anything)
		mockStore.AssertNotCalled(t, "TransitionExecutionStatus",
			mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("execute_purchase pending AutoPurchase=false rejected (F1)", func(t *testing.T) {
		// A pending row under a plan with AutoPurchase=false must not be
		// auto-executed via SQS.
		mockStore := new(MockConfigStore)
		manager := &Manager{config: mockStore, dashboardURL: "https://example.com"}
		execution := &config.PurchaseExecution{
			ExecutionID: "exec-noauto",
			PlanID:      "plan-noauto",
			Status:      "pending",
		}
		plan := &config.PurchasePlan{ID: "plan-noauto", AutoPurchase: false}
		mockStore.On("GetExecutionByID", ctx, "exec-noauto").Return(execution, nil)
		mockStore.On("GetPurchasePlan", ctx, "plan-noauto").Return(plan, nil)
		// TransitionExecutionStatus must NOT be called

		err := manager.ProcessMessage(ctx, `{"type": "execute_purchase", "execution_id": "exec-noauto"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not eligible for auto-execution")
		mockStore.AssertNotCalled(t, "TransitionExecutionStatus",
			mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		mockStore.AssertExpectations(t)
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
			Status:      "canceled",
		}
		mockStore.On("GetExecutionByID", ctx, "exec-123").Return(execution, nil)
		// The claim CAS rejects a non-executable status (issue #1013): the row
		// is "canceled", not in [approved,pending,notified], so the CAS loses
		// and returns ErrExecutionNotInExpectedStatus — a benign skip that the
		// handler acks without error.
		mockStore.On("TransitionExecutionStatus", ctx, "exec-123",
			[]string{"approved", "pending", "notified"}, "running", (*string)(nil)).
			Return(nil, fmt.Errorf("%w: canceled", config.ErrExecutionNotInExpectedStatus))

		err := manager.ProcessMessage(ctx, `{"type": "execute_purchase", "execution_id": "exec-123"}`)
		// Should skip without error when status is not executable
		assert.NoError(t, err)
	})
}
