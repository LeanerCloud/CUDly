package purchase

import (
	"context"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// approveFromStatuses mirrors the value Manager.ApproveAndExecute passes to
// TransitionExecutionStatus. Centralized so a future change to the
// allowed-from set updates every test in one place.
var approveFromStatuses = []string{"pending", "notified"}

// newApproveManager wires a Manager backed by a fresh MockConfigStore +
// MockEmailSender. Returns both mocks so tests can register expectations
// per scenario without re-stating the boilerplate.
func newApproveManager(t *testing.T) (*Manager, *MockConfigStore, *MockEmailSender) {
	t.Helper()
	store := new(MockConfigStore)
	sender := new(MockEmailSender)
	return &Manager{
		config:       store,
		email:        sender,
		dashboardURL: "https://dashboard.example.com",
	}, store, sender
}

// stubExecuteChain wires the mocks that Manager.executeAndFinalize touches
// when the execution has no work to do (empty Recommendations, no plan
// accounts). GetPlanAccounts uses the Fn-override pattern in this mock,
// not testify's .On; left at its nil default it returns (nil, nil) which
// drops us into the single-account branch with no recs.
func stubExecuteChain(t *testing.T, store *MockConfigStore, sender *MockEmailSender, planID string) {
	t.Helper()
	plan := &config.PurchasePlan{ID: planID, Name: "test-plan"}
	store.On("GetPurchasePlan", mock.Anything, planID).Return(plan, nil)
	// Empty Recommendations means processPurchaseRecommendations is a
	// no-op; totals stay zero but sendPurchaseNotification still fires.
	sender.On("SendPurchaseConfirmation", mock.Anything, mock.Anything).Return(nil)
	// finalizeExecution writes status=completed via SavePurchaseExecution.
	store.On("SavePurchaseExecution", mock.Anything, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	// updatePlanProgress fetches+updates the plan.
	store.On("UpdatePurchasePlan", mock.Anything, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)
}

func TestManager_ApproveExecution_Success(t *testing.T) {
	ctx := context.Background()
	manager, store, sender := newApproveManager(t)

	execution := &config.PurchaseExecution{
		ExecutionID:   "exec-123",
		PlanID:        "plan-456",
		Status:        "pending",
		ApprovalToken: "valid-token",
	}
	updated := &config.PurchaseExecution{
		ExecutionID:   "exec-123",
		PlanID:        "plan-456",
		Status:        "approved",
		ApprovalToken: "valid-token",
	}

	store.On("GetExecutionByID", ctx, "exec-123").Return(execution, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-123", approveFromStatuses, "approved").Return(updated, nil)
	stubExecuteChain(t, store, sender, "plan-456")

	err := manager.ApproveExecution(ctx, "exec-123", "valid-token", "")
	require.NoError(t, err)
	store.AssertExpectations(t)
	sender.AssertExpectations(t)
}

func TestManager_ApproveExecution_StampsApprovedBy(t *testing.T) {
	ctx := context.Background()
	manager, store, sender := newApproveManager(t)

	execution := &config.PurchaseExecution{
		ExecutionID:   "exec-123",
		PlanID:        "plan-456",
		Status:        "pending",
		ApprovalToken: "valid-token",
	}
	updated := &config.PurchaseExecution{
		ExecutionID:   "exec-123",
		PlanID:        "plan-456",
		Status:        "approved",
		ApprovalToken: "valid-token",
	}

	store.On("GetExecutionByID", ctx, "exec-123").Return(execution, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-123", approveFromStatuses, "approved").Return(updated, nil)
	stubExecuteChain(t, store, sender, "plan-456")

	err := manager.ApproveExecution(ctx, "exec-123", "valid-token", "operator@example.com")
	require.NoError(t, err)

	// Two SavePurchaseExecution calls land on the same pointer (the
	// transitioned row): once for the ApprovedBy stamp, once at the end
	// of executeAndFinalize. Both should reflect the actor.
	require.NotNil(t, updated.ApprovedBy)
	assert.Equal(t, "operator@example.com", *updated.ApprovedBy)
	store.AssertExpectations(t)
	sender.AssertExpectations(t)
}

func TestManager_ApproveExecution_NotifiedStatus(t *testing.T) {
	ctx := context.Background()
	manager, store, sender := newApproveManager(t)

	execution := &config.PurchaseExecution{
		ExecutionID:   "exec-123",
		PlanID:        "plan-456",
		Status:        "notified",
		ApprovalToken: "valid-token",
	}
	updated := &config.PurchaseExecution{
		ExecutionID:   "exec-123",
		PlanID:        "plan-456",
		Status:        "approved",
		ApprovalToken: "valid-token",
	}

	store.On("GetExecutionByID", ctx, "exec-123").Return(execution, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-123", approveFromStatuses, "approved").Return(updated, nil)
	stubExecuteChain(t, store, sender, "plan-456")

	err := manager.ApproveExecution(ctx, "exec-123", "valid-token", "")
	require.NoError(t, err)
	store.AssertExpectations(t)
	sender.AssertExpectations(t)
}

func TestManager_ApproveExecution_InvalidToken(t *testing.T) {
	ctx := context.Background()
	manager, store, _ := newApproveManager(t)

	execution := &config.PurchaseExecution{
		ExecutionID:   "exec-123",
		PlanID:        "plan-456",
		Status:        "pending",
		ApprovalToken: "valid-token",
	}

	store.On("GetExecutionByID", ctx, "exec-123").Return(execution, nil)

	err := manager.ApproveExecution(ctx, "exec-123", "invalid-token", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid approval token")
	// Critically: TransitionExecutionStatus and SavePurchaseExecution must
	// NOT have been called. A token-validation bypass is exactly what the
	// constant-time comparison guards against.
	store.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	store.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
	store.AssertExpectations(t)
}

func TestManager_ApproveExecution_EmptyToken(t *testing.T) {
	ctx := context.Background()
	manager, store, _ := newApproveManager(t)

	execution := &config.PurchaseExecution{
		ExecutionID:   "exec-123",
		Status:        "pending",
		ApprovalToken: "valid-token",
	}
	store.On("GetExecutionByID", ctx, "exec-123").Return(execution, nil)

	err := manager.ApproveExecution(ctx, "exec-123", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid approval token")
	store.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestManager_ApproveExecution_NotFound(t *testing.T) {
	ctx := context.Background()
	manager, store, _ := newApproveManager(t)

	store.On("GetExecutionByID", ctx, "exec-123").Return(nil, nil)

	err := manager.ApproveExecution(ctx, "exec-123", "token", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "execution not found")
	store.AssertExpectations(t)
}

func TestManager_ApproveExecution_GetError(t *testing.T) {
	ctx := context.Background()
	manager, store, _ := newApproveManager(t)

	store.On("GetExecutionByID", ctx, "exec-123").Return(nil, errors.New("database error"))

	err := manager.ApproveExecution(ctx, "exec-123", "token", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get execution")
	store.AssertExpectations(t)
}

func TestManager_ApproveExecution_TransitionFails(t *testing.T) {
	// Closes the race-with-cancel/expire path: token is valid but the row
	// drifted out of pending/notified between fetch and atomic UPDATE.
	// TransitionExecutionStatus 0-rows; the caller surfaces a clean error
	// and never enters the execute chain.
	ctx := context.Background()
	manager, store, _ := newApproveManager(t)

	execution := &config.PurchaseExecution{
		ExecutionID:   "exec-123",
		PlanID:        "plan-456",
		Status:        "pending",
		ApprovalToken: "valid-token",
	}
	store.On("GetExecutionByID", ctx, "exec-123").Return(execution, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-123", approveFromStatuses, "approved").
		Return(nil, errors.New(`execution exec-123 cannot transition from "cancelled" to "approved"`))

	err := manager.ApproveExecution(ctx, "exec-123", "valid-token", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot transition")
	// Execute chain must not run after a failed transition.
	store.AssertNotCalled(t, "GetPurchasePlan", mock.Anything, mock.Anything)
	store.AssertExpectations(t)
}

func TestManager_ApproveAndExecute_SkipsTokenCheck(t *testing.T) {
	// Session-authed path: ApproveAndExecute is called directly without a
	// token, after the caller has run RBAC. Verifies the entry point works
	// independently of the token branch.
	ctx := context.Background()
	manager, store, sender := newApproveManager(t)

	updated := &config.PurchaseExecution{
		ExecutionID:   "exec-456",
		PlanID:        "plan-789",
		Status:        "approved",
		ApprovalToken: "tok",
	}
	store.On("TransitionExecutionStatus", ctx, "exec-456", approveFromStatuses, "approved").Return(updated, nil)
	stubExecuteChain(t, store, sender, "plan-789")

	err := manager.ApproveAndExecute(ctx, "exec-456", "session-user@example.com")
	require.NoError(t, err)
	require.NotNil(t, updated.ApprovedBy)
	assert.Equal(t, "session-user@example.com", *updated.ApprovedBy)
	store.AssertExpectations(t)
	sender.AssertExpectations(t)
}

func TestManager_CancelExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	execution := &config.PurchaseExecution{
		ExecutionID:   "exec-123",
		PlanID:        "plan-456",
		Status:        "pending",
		ApprovalToken: "valid-token",
	}

	mockStore.On("GetExecutionByID", ctx, "exec-123").Return(execution, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.CancelExecution(ctx, "exec-123", "valid-token", "")
	require.NoError(t, err)

	mockStore.AssertExpectations(t)
}

func TestManager_CancelExecution_InvalidToken(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	execution := &config.PurchaseExecution{
		ExecutionID:   "exec-123",
		PlanID:        "plan-456",
		Status:        "pending",
		ApprovalToken: "valid-token",
	}

	mockStore.On("GetExecutionByID", ctx, "exec-123").Return(execution, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.CancelExecution(ctx, "exec-123", "invalid-token", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid approval token")

	mockStore.AssertExpectations(t)
}

func TestManager_CancelExecution_AlreadyCompleted(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	execution := &config.PurchaseExecution{
		ExecutionID:   "exec-123",
		PlanID:        "plan-456",
		Status:        "completed",
		ApprovalToken: "valid-token",
	}

	mockStore.On("GetExecutionByID", ctx, "exec-123").Return(execution, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.CancelExecution(ctx, "exec-123", "valid-token", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "execution cannot be cancelled")

	mockStore.AssertExpectations(t)
}

func TestManager_CancelExecution_NotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	mockStore.On("GetExecutionByID", ctx, "exec-123").Return(nil, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.CancelExecution(ctx, "exec-123", "token", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "execution not found")

	mockStore.AssertExpectations(t)
}

func TestManager_CancelExecution_GetError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	mockStore.On("GetExecutionByID", ctx, "exec-123").Return(nil, errors.New("database error"))

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.CancelExecution(ctx, "exec-123", "token", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get execution")

	mockStore.AssertExpectations(t)
}
