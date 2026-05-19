package purchase

import (
	"context"
	"errors"
	"testing"
	"time"

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

// TestManager_ApproveAndExecute_EmptyPlanID is the regression guard for
// issue #395. Direct-execute purchases (Opportunities "Purchase" button)
// arrive with PlanID = "". Before the fix, executePurchase called
// GetPurchasePlan(ctx, "") which crashed with SQLSTATE 22P02 because the
// Postgres purchase_plans.id column is UUID. This test asserts the
// approve+execute path now short-circuits the plan/accounts fetch and the
// plan-progress update, lands the execution in a terminal state, and
// never calls GetPurchasePlan / GetPlanAccounts / UpdatePurchasePlan.
func TestManager_ApproveAndExecute_EmptyPlanID(t *testing.T) {
	ctx := context.Background()
	manager, store, sender := newApproveManager(t)

	updated := &config.PurchaseExecution{
		ExecutionID:   "exec-direct-1",
		PlanID:        "", // direct-execute shape — no associated plan
		Status:        "approved",
		ApprovalToken: "tok",
	}
	store.On("TransitionExecutionStatus", ctx, "exec-direct-1", approveFromStatuses, "approved").Return(updated, nil)
	// No GetPurchasePlan / GetPlanAccounts / UpdatePurchasePlan calls — see AssertNotCalled below.
	sender.On("SendPurchaseConfirmation", mock.Anything, mock.Anything).Return(nil)
	store.On("SavePurchaseExecution", mock.Anything, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)

	err := manager.ApproveAndExecute(ctx, "exec-direct-1", "operator@example.com")
	require.NoError(t, err)

	// Crucially, the empty PlanID must never reach the UUID-typed store columns.
	store.AssertNotCalled(t, "GetPurchasePlan", mock.Anything, mock.Anything)
	store.AssertNotCalled(t, "UpdatePurchasePlan", mock.Anything, mock.Anything)
	// GetPlanAccounts uses the Fn-override pattern in MockConfigStore; leaving
	// GetPlanAccountsFn nil means the production code path is the one under
	// test. The early-return on empty PlanID skips the call entirely.
	assert.Nil(t, store.GetPlanAccountsFn, "test sanity: empty-PlanID branch must not depend on GetPlanAccounts being stubbed")

	// Status reaches a terminal state — completed because no recs failed.
	assert.Equal(t, "completed", updated.Status)
	require.NotNil(t, updated.ApprovedBy)
	assert.Equal(t, "operator@example.com", *updated.ApprovedBy)
	store.AssertExpectations(t)
	sender.AssertExpectations(t)
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

// --- Regression tests for issue #397 (token TTL enforcement) ---

// TestManager_ApproveExecution_ExpiredToken is the regression guard for
// issue #397: an approval token whose ApprovalTokenExpiresAt deadline has
// passed must be rejected with "expired", not silently accepted. This
// prevents a phished or log-leaked token from authorising a purchase weeks
// after the approval window closed.
func TestManager_ApproveExecution_ExpiredToken(t *testing.T) {
	ctx := context.Background()
	manager, store, _ := newApproveManager(t)

	past := time.Now().Add(-1 * time.Hour) // expired 1 hour ago
	execution := &config.PurchaseExecution{
		ExecutionID:            "exec-expired",
		Status:                 "pending",
		ApprovalToken:          "valid-token",
		ApprovalTokenExpiresAt: &past,
	}
	store.On("GetExecutionByID", ctx, "exec-expired").Return(execution, nil)

	err := manager.ApproveExecution(ctx, "exec-expired", "valid-token", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
	// Token validation passes but the expiry check fires — TransitionExecutionStatus
	// must never be called on an expired token.
	store.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	store.AssertExpectations(t)
}

// TestManager_ApproveExecution_ValidTokenWithinTTL confirms that a
// non-expired token still works (regression guard for the inverse case).
func TestManager_ApproveExecution_ValidTokenWithinTTL(t *testing.T) {
	ctx := context.Background()
	manager, store, sender := newApproveManager(t)

	future := time.Now().Add(7 * 24 * time.Hour)
	execution := &config.PurchaseExecution{
		ExecutionID:            "exec-live",
		PlanID:                 "plan-live",
		Status:                 "pending",
		ApprovalToken:          "valid-token",
		ApprovalTokenExpiresAt: &future,
	}
	updated := &config.PurchaseExecution{
		ExecutionID:   "exec-live",
		PlanID:        "plan-live",
		Status:        "approved",
		ApprovalToken: "valid-token",
	}
	store.On("GetExecutionByID", ctx, "exec-live").Return(execution, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-live", approveFromStatuses, "approved").Return(updated, nil)
	stubExecuteChain(t, store, sender, "plan-live")

	err := manager.ApproveExecution(ctx, "exec-live", "valid-token", "")
	require.NoError(t, err)
	store.AssertExpectations(t)
}

// TestManager_ApproveExecution_NilExpiresAt_LegacyRow verifies backward
// compatibility: rows created before migration 000051 have a nil
// ApprovalTokenExpiresAt and must not be rejected (issue #397 explicitly
// carves out legacy rows for compatibility).
func TestManager_ApproveExecution_NilExpiresAt_LegacyRow(t *testing.T) {
	ctx := context.Background()
	manager, store, sender := newApproveManager(t)

	execution := &config.PurchaseExecution{
		ExecutionID:            "exec-legacy",
		PlanID:                 "plan-legacy",
		Status:                 "pending",
		ApprovalToken:          "valid-token",
		ApprovalTokenExpiresAt: nil, // pre-migration row
	}
	updated := &config.PurchaseExecution{
		ExecutionID: "exec-legacy",
		PlanID:      "plan-legacy",
		Status:      "approved",
	}
	store.On("GetExecutionByID", ctx, "exec-legacy").Return(execution, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-legacy", approveFromStatuses, "approved").Return(updated, nil)
	stubExecuteChain(t, store, sender, "plan-legacy")

	err := manager.ApproveExecution(ctx, "exec-legacy", "valid-token", "")
	require.NoError(t, err)
	store.AssertExpectations(t)
}

// TestManager_CancelExecution_ExpiredToken is the cancel-path regression
// guard for issue #397.
func TestManager_CancelExecution_ExpiredToken(t *testing.T) {
	ctx := context.Background()
	manager, store, _ := newApproveManager(t)

	past := time.Now().Add(-2 * 24 * time.Hour)
	execution := &config.PurchaseExecution{
		ExecutionID:            "exec-cancel-expired",
		Status:                 "pending",
		ApprovalToken:          "valid-token",
		ApprovalTokenExpiresAt: &past,
	}
	store.On("GetExecutionByID", ctx, "exec-cancel-expired").Return(execution, nil)

	err := manager.CancelExecution(ctx, "exec-cancel-expired", "valid-token", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
	store.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
	store.AssertExpectations(t)
}
