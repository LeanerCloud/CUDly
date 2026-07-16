package purchase

import (
	"context"
	"errors"
	"fmt"
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
	// updatePlanProgress calls IncrementPlanCurrentStep (atomic, issue #1071).
	store.On("IncrementPlanCurrentStep", mock.Anything, planID).Return(nil)
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
	store.On("TransitionExecutionStatus", ctx, "exec-123", approveFromStatuses, "approved", (*string)(nil)).Return(updated, nil)
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
	store.On("TransitionExecutionStatus", ctx, "exec-123", approveFromStatuses, "approved", (*string)(nil)).Return(updated, nil)
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
	store.On("TransitionExecutionStatus", ctx, "exec-123", approveFromStatuses, "approved", (*string)(nil)).Return(updated, nil)
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
	store.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
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
	store.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestManager_ApproveExecution_NotFound(t *testing.T) {
	ctx := context.Background()
	manager, store, _ := newApproveManager(t)

	store.On("GetExecutionByID", ctx, "exec-123").Return(nil, fmt.Errorf("%w: execution exec-123", config.ErrNotFound))

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
	store.On("TransitionExecutionStatus", ctx, "exec-123", approveFromStatuses, "approved", (*string)(nil)).
		Return(nil, errors.New(`execution exec-123 cannot transition from "canceled" to "approved"`))

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
	store.On("TransitionExecutionStatus", ctx, "exec-direct-1", approveFromStatuses, "approved", (*string)(nil)).Return(updated, nil)
	// No GetPurchasePlan / GetPlanAccounts / UpdatePurchasePlan calls — see AssertNotCalled below.
	sender.On("SendPurchaseConfirmation", mock.Anything, mock.Anything).Return(nil)
	store.On("SavePurchaseExecution", mock.Anything, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)

	err := manager.ApproveAndExecute(ctx, "exec-direct-1", "operator@example.com", nil)
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
	//
	// Also the manager-level regression guard for issue #1009: a non-nil
	// transitionedBy (the session user's UUID) must be threaded into
	// TransitionExecutionStatus so transitioned_by is stamped for human
	// session approvals. Pre-fix ApproveAndExecute always passed nil here.
	ctx := context.Background()
	manager, store, sender := newApproveManager(t)

	actorUUID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	updated := &config.PurchaseExecution{
		ExecutionID:   "exec-456",
		PlanID:        "plan-789",
		Status:        "approved",
		ApprovalToken: "tok",
	}
	store.On("TransitionExecutionStatus", ctx, "exec-456", approveFromStatuses, "approved",
		mock.MatchedBy(func(actor *string) bool { return actor != nil && *actor == actorUUID })).Return(updated, nil)
	stubExecuteChain(t, store, sender, "plan-789")

	err := manager.ApproveAndExecute(ctx, "exec-456", "session-user@example.com", &actorUUID)
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
	// WithTx passes nil as the tx sentinel in tests; empty actor -> nil cancelledBy.
	mockStore.On("CancelExecutionAtomic", ctx, mock.Anything, "exec-123", (*string)(nil)).
		Return(true, "canceled", nil)
	mockStore.On("DeleteSuppressionsByExecutionTx", ctx, mock.Anything, "exec-123").
		Return(nil)
	// Token rotation: SavePurchaseExecution is called after a successful cancel.
	mockStore.On("SavePurchaseExecution", ctx, mock.MatchedBy(func(e *config.PurchaseExecution) bool {
		return e.ExecutionID == "exec-123" && e.ApprovalToken == ""
	})).Return(nil)

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
	assert.Contains(t, err.Error(), "execution cannot be canceled")

	mockStore.AssertExpectations(t)
}

// TestManager_CancelExecution_RejectsNonCancelableStatus is the regression
// guard for issue #645: the token/email cancel path previously rejected only
// completed/canceled, so an email-link holder could cancel an
// approved/running/paused/failed/expired execution that the dashboard
// (session) path refuses. Each non-cancelable status must now be rejected with
// no write to the store — approved/running rows in particular are mid-execution
// and canceling them would desync the DB from the cloud. Pending, notified, and
// scheduled are cancelable; all other states are not.
func TestManager_CancelExecution_RejectsNonCancelableStatus(t *testing.T) {
	rejected := []string{"approved", "running", "paused", "failed", "expired", "completed", "canceled"}
	for _, status := range rejected {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			mockStore := new(MockConfigStore)
			mockEmail := new(MockEmailSender)

			execution := &config.PurchaseExecution{
				ExecutionID:   "exec-123",
				PlanID:        "plan-456",
				Status:        status,
				ApprovalToken: "valid-token",
			}
			mockStore.On("GetExecutionByID", ctx, "exec-123").Return(execution, nil)

			manager := &Manager{
				config:       mockStore,
				email:        mockEmail,
				dashboardURL: "https://dashboard.example.com",
			}

			err := manager.CancelExecution(ctx, "exec-123", "valid-token", status)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "execution cannot be canceled")
			assert.Contains(t, err.Error(), status)
			// Status guard fires before the atomic UPDATE — a rejected
			// cancel must never reach CancelExecutionAtomic.
			mockStore.AssertNotCalled(t, "CancelExecutionAtomic", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			mockStore.AssertExpectations(t)
		})
	}
}

// TestManager_CancelExecution_AllowsCancelableStatus confirms the inverse of
// the #645 guard: pending, notified, and scheduled rows are all cancelable on
// the token path, and the cancel commits (status flip + suppression cleanup)
// in a single tx. Without this the alignment fix could silently over-restrict
// and break the legitimate email-link cancel of a row awaiting approval.
// "scheduled" is included because the cloud SDK has not been called yet (issue
// #291 wave-2) and IsCancelable already permits it.
func TestManager_CancelExecution_AllowsCancelableStatus(t *testing.T) {
	allowed := []string{"pending", "notified", "scheduled"}
	for _, status := range allowed {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			mockStore := new(MockConfigStore)
			mockEmail := new(MockEmailSender)

			execution := &config.PurchaseExecution{
				ExecutionID:   "exec-123",
				PlanID:        "plan-456",
				Status:        status,
				ApprovalToken: "valid-token",
			}
			mockStore.On("GetExecutionByID", ctx, "exec-123").Return(execution, nil)
			// CancelExecutionAtomic is called inside WithTx (nil tx sentinel in tests).
			mockStore.On("CancelExecutionAtomic", ctx, mock.Anything, "exec-123", (*string)(nil)).
				Return(true, "canceled", nil)
			// Suppression cleanup must follow a successful atomic cancel.
			mockStore.On("DeleteSuppressionsByExecutionTx", ctx, mock.Anything, "exec-123").
				Return(nil)
			// Token rotation after successful cancel.
			mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)

			manager := &Manager{
				config:       mockStore,
				email:        mockEmail,
				dashboardURL: "https://dashboard.example.com",
			}

			err := manager.CancelExecution(ctx, "exec-123", "valid-token", "")
			require.NoError(t, err)
			mockStore.AssertCalled(t, "CancelExecutionAtomic", ctx, mock.Anything, "exec-123", (*string)(nil))
			mockStore.AssertCalled(t, "DeleteSuppressionsByExecutionTx", ctx, mock.Anything, "exec-123")
			mockStore.AssertExpectations(t)
		})
	}
}

func TestManager_CancelExecution_NotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	mockStore.On("GetExecutionByID", ctx, "exec-123").Return(nil, fmt.Errorf("%w: execution exec-123", config.ErrNotFound))

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
// prevents a phished or log-leaked token from authorizing a purchase weeks
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
	store.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
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
	store.On("TransitionExecutionStatus", ctx, "exec-live", approveFromStatuses, "approved", (*string)(nil)).Return(updated, nil)
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
	store.On("TransitionExecutionStatus", ctx, "exec-legacy", approveFromStatuses, "approved", (*string)(nil)).Return(updated, nil)
	stubExecuteChain(t, store, sender, "plan-legacy")

	err := manager.ApproveExecution(ctx, "exec-legacy", "valid-token", "")
	require.NoError(t, err)
	store.AssertExpectations(t)
}

// --- Regression tests for issue #609 (orphan-account guard, ApproveExecution path) ---

// TestManager_ApproveExecution_NonAWSOrphanReturnsError is the regression
// guard for issue #609 on the token-authenticated ApproveExecution path (used
// by the SQS approve worker and the legacy email-link flow). An execution
// whose CloudAccountID is nil and whose provider is non-AWS must return a
// descriptive error before the execute chain is entered, instead of surfacing
// an opaque cloud-SDK credential error at runtime.
func TestManager_ApproveExecution_NonAWSOrphanReturnsError(t *testing.T) {
	ctx := context.Background()
	manager, store, _ := newApproveManager(t)

	execution := &config.PurchaseExecution{
		ExecutionID:   "exec-orphan",
		Status:        "pending",
		ApprovalToken: "valid-token",
		// CloudAccountID nil — account was deleted.
		Recommendations: []config.RecommendationRecord{{ID: "r1", Provider: "azure"}},
	}
	store.On("GetExecutionByID", ctx, "exec-orphan").Return(execution, nil)

	err := manager.ApproveExecution(ctx, "exec-orphan", "valid-token", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no longer exists")
	assert.Contains(t, err.Error(), "azure")
	// Execute chain must not run after the orphan guard fires.
	store.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	store.AssertExpectations(t)
}

// TestManager_ApproveExecution_AWSOrphanFallsThrough confirms the AWS
// ambient-fallback carve-out: nil CloudAccountID + AWS provider must NOT be
// blocked by the issue-#609 guard. The execution proceeds normally.
func TestManager_ApproveExecution_AWSOrphanFallsThrough(t *testing.T) {
	ctx := context.Background()
	manager, store, sender := newApproveManager(t)

	execution := &config.PurchaseExecution{
		ExecutionID:   "exec-aws-ambient",
		PlanID:        "plan-aws",
		Status:        "pending",
		ApprovalToken: "valid-token",
		// CloudAccountID nil but provider is AWS — ambient fallback applies.
		Recommendations: []config.RecommendationRecord{{ID: "r1", Provider: "aws"}},
	}
	updated := &config.PurchaseExecution{
		ExecutionID:   "exec-aws-ambient",
		PlanID:        "plan-aws",
		Status:        "approved",
		ApprovalToken: "valid-token",
	}
	store.On("GetExecutionByID", ctx, "exec-aws-ambient").Return(execution, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-aws-ambient", approveFromStatuses, "approved", (*string)(nil)).Return(updated, nil)
	stubExecuteChain(t, store, sender, "plan-aws")

	err := manager.ApproveExecution(ctx, "exec-aws-ambient", "valid-token", "")
	require.NoError(t, err)
	store.AssertExpectations(t)
	sender.AssertExpectations(t)
}

// TestOrphanExecutionError_RecLevelAccountIDPreventsOrphan is the regression
// guard for CR pass-1 actionable 2: an execution with a nil execution-level
// CloudAccountID and a non-AWS provider must NOT be treated as an orphan when
// at least one recommendation carries its own non-nil CloudAccountID. The
// multi-rec fan-out path stores account affinity at the rec level and leaves
// the execution-level field nil.
func TestOrphanExecutionError_RecLevelAccountIDPreventsOrphan(t *testing.T) {
	accountID := "acct-azure-multi-001"
	execution := &config.PurchaseExecution{
		ExecutionID: "exec-multi-rec",
		// Execution-level CloudAccountID is nil — fan-out shape.
		Recommendations: []config.RecommendationRecord{
			{ID: "r1", Provider: "azure", CloudAccountID: &accountID},
			{ID: "r2", Provider: "azure", CloudAccountID: &accountID},
		},
	}
	// Despite execution-level nil + non-AWS provider, the rec-level account
	// ID means the execution is NOT orphaned. OrphanExecutionError must return nil.
	err := OrphanExecutionError(execution)
	assert.NoError(t, err, "rec-level CloudAccountID must prevent orphan classification")
}

// TestManager_CancelExecution_RaceWithApprove is the regression guard for
// issue #671: when a concurrent approve wins the race and transitions the
// execution to 'approved' before the cancel's conditional UPDATE runs,
// CancelExecutionAtomic returns (false, "approved", nil). CancelExecution
// must surface a clean error containing the racing status rather than
// silently overwriting the approved row.
func TestManager_CancelExecution_RaceWithApprove(t *testing.T) {
	ctx := context.Background()
	manager, store, _ := newApproveManager(t)

	execution := &config.PurchaseExecution{
		ExecutionID:   "exec-raced",
		Status:        "pending", // status at load time
		ApprovalToken: "valid-token",
	}
	store.On("GetExecutionByID", ctx, "exec-raced").Return(execution, nil)
	// Simulate: concurrent approve won between our IsCancelable check and
	// the atomic UPDATE. The DB row is now 'approved' so zero rows affected.
	store.On("CancelExecutionAtomic", ctx, mock.Anything, "exec-raced", (*string)(nil)).
		Return(false, "approved", nil)

	err := manager.CancelExecution(ctx, "exec-raced", "valid-token", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "approved", "error must surface the racing status")
	assert.Contains(t, err.Error(), "concurrent", "error must mention the concurrent operation")
	// The approve is already in flight — the DB row must not have been
	// overwritten by the cancel. AssertExpectations verifies that
	// CancelExecutionAtomic was called (confirming the guard reached the DB)
	// and that no further writes landed.
	store.AssertExpectations(t)
	// When the CAS misses, suppression cleanup must never fire.
	store.AssertNotCalled(t, "DeleteSuppressionsByExecutionTx", mock.Anything, mock.Anything, mock.Anything)
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
	store.AssertNotCalled(t, "CancelExecutionAtomic", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	store.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Finding #1: Token rotation on approve/cancel
// ---------------------------------------------------------------------------

// TestApproveExecution_RotatesApprovalToken verifies that after a successful
// approve the ApprovalToken is cleared to prevent the same token from being
// used to trigger a revoke (or any other action) later.
func TestApproveExecution_MintsRevocationToken(t *testing.T) {
	ctx := context.Background()
	manager, store, sender := newApproveManager(t)

	execution := &config.PurchaseExecution{
		ExecutionID:   "exec-rotate",
		PlanID:        "plan-rotate",
		Status:        "pending",
		ApprovalToken: "pre-rotate-token",
	}
	updated := &config.PurchaseExecution{
		ExecutionID:   "exec-rotate",
		PlanID:        "plan-rotate",
		Status:        "approved",
		ApprovalToken: "pre-rotate-token",
	}

	// GetExecutionByID is called twice: once in ApproveExecution itself and
	// once inside mintRevocationToken (the rotation step). Both return the
	// same pointer so mintRevocationToken's field mutations are visible after
	// the call.
	store.On("GetExecutionByID", ctx, "exec-rotate").Return(execution, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-rotate", approveFromStatuses, "approved", (*string)(nil)).Return(updated, nil)
	stubExecuteChain(t, store, sender, "plan-rotate")

	err := manager.ApproveExecution(ctx, "exec-rotate", "pre-rotate-token", "")
	require.NoError(t, err)

	// After approve, mintRevocationToken must have stamped a fresh token onto
	// the execution row. The old approval token must be gone (proving the
	// rotation happened), and the expiry must be set to the 24-hour
	// revocation window.
	// stubExecuteChain registers SavePurchaseExecution with AnythingOfType
	// so it absorbs both the finalize write and the mintRevocationToken write.
	assert.NotEmpty(t, execution.ApprovalToken,
		"a fresh revocation token must be minted after successful approve (not cleared)")
	assert.NotEqual(t, "pre-rotate-token", execution.ApprovalToken,
		"fresh revocation token must differ from the consumed approval token")
	assert.NotNil(t, execution.ApprovalTokenExpiresAt,
		"revocation token expiry must be set to the 24-hour window")
}

// TestClearApprovalToken_PersistsEmpty is a unit test for clearApprovalToken
// (the cancel path) that verifies the helper clears both ApprovalToken and
// ApprovalTokenExpiresAt on the persisted row. A canceled execution has no
// valid follow-up action, so emptying the token is correct.
func TestClearApprovalToken_PersistsEmpty(t *testing.T) {
	ctx := context.Background()
	manager, store, _ := newApproveManager(t)

	future := time.Now().Add(24 * time.Hour)
	execution := &config.PurchaseExecution{
		ExecutionID:            "exec-tok-clear",
		Status:                 "canceled",
		ApprovalToken:          "some-token",
		ApprovalTokenExpiresAt: &future,
	}
	store.On("GetExecutionByID", ctx, "exec-tok-clear").Return(execution, nil)
	store.On("SavePurchaseExecution", ctx, mock.MatchedBy(func(e *config.PurchaseExecution) bool {
		return e.ExecutionID == "exec-tok-clear" && e.ApprovalToken == "" && e.ApprovalTokenExpiresAt == nil
	})).Return(nil)

	err := manager.clearApprovalToken(ctx, "exec-tok-clear")
	require.NoError(t, err)
	store.AssertExpectations(t)
}

// TestMintRevocationToken_PersistsFreshToken is a unit test for
// mintRevocationToken that verifies the helper writes a non-empty, changed
// token with a future expiry on the persisted row.
func TestMintRevocationToken_PersistsFreshToken(t *testing.T) {
	ctx := context.Background()
	manager, store, _ := newApproveManager(t)

	execution := &config.PurchaseExecution{
		ExecutionID:   "exec-mint",
		Status:        "completed",
		ApprovalToken: "old-approval-token",
	}
	store.On("GetExecutionByID", ctx, "exec-mint").Return(execution, nil)
	store.On("SavePurchaseExecution", ctx, mock.MatchedBy(func(e *config.PurchaseExecution) bool {
		// Fresh token must be non-empty, different from old, and expiry set.
		return e.ExecutionID == "exec-mint" &&
			e.ApprovalToken != "" &&
			e.ApprovalToken != "old-approval-token" &&
			e.ApprovalTokenExpiresAt != nil
	})).Return(nil)

	err := manager.mintRevocationToken(ctx, "exec-mint")
	require.NoError(t, err)
	store.AssertExpectations(t)
	// Confirm the in-memory struct was mutated (used by the handler's re-fetch).
	assert.NotEmpty(t, execution.ApprovalToken)
	assert.NotEqual(t, "old-approval-token", execution.ApprovalToken)
	assert.NotNil(t, execution.ApprovalTokenExpiresAt)
}
