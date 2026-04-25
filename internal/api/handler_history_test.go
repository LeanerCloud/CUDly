package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// adminHistoryReq builds an admin-authed request and wires the auth mock so
// requirePermission short-circuits. Returns the mocked auth so tests can add
// extra expectations.
func adminHistoryReq(ctx context.Context) (*MockAuthService, *events.LambdaFunctionURLRequest) {
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}, nil)
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	return mockAuth, req
}

func TestHandler_getHistory(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	history := []config.PurchaseHistoryRecord{
		{AccountID: "123456789012", PurchaseID: "purchase-1", UpfrontCost: 100.0, EstimatedSavings: 10.0},
	}

	mockStore.On("GetPurchaseHistory", ctx, "123456789012", 100).Return(history, nil)
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{}, nil)

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	params := map[string]string{
		"account_id": "123456789012",
	}

	result, err := handler.getHistory(ctx, req, params)
	require.NoError(t, err)

	historyResp := result.(HistoryResponse)
	assert.Len(t, historyResp.Purchases, 1)
	assert.Equal(t, "completed", historyResp.Purchases[0].Status, "DB-backed rows must be tagged completed")
	assert.Equal(t, 1, historyResp.Summary.TotalPurchases)
	assert.Equal(t, 1, historyResp.Summary.TotalCompleted)
	assert.Equal(t, 0, historyResp.Summary.TotalPending)
	assert.Equal(t, 100.0, historyResp.Summary.TotalUpfront)
	assert.Equal(t, 10.0, historyResp.Summary.TotalMonthlySavings)
	assert.Equal(t, 120.0, historyResp.Summary.TotalAnnualSavings)
}

func TestHandler_getHistory_AllAccounts(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	history := []config.PurchaseHistoryRecord{
		{AccountID: "111111111111", PurchaseID: "purchase-1", UpfrontCost: 100.0, EstimatedSavings: 10.0},
		{AccountID: "222222222222", PurchaseID: "purchase-2", UpfrontCost: 200.0, EstimatedSavings: 20.0},
	}

	mockStore.On("GetAllPurchaseHistory", ctx, 100).Return(history, nil)
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{}, nil)

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	params := map[string]string{}

	result, err := handler.getHistory(ctx, req, params)
	require.NoError(t, err)

	historyResp := result.(HistoryResponse)
	assert.Len(t, historyResp.Purchases, 2)
	assert.Equal(t, 2, historyResp.Summary.TotalPurchases)
	assert.Equal(t, 2, historyResp.Summary.TotalCompleted)
	assert.Equal(t, 300.0, historyResp.Summary.TotalUpfront)
	assert.Equal(t, 30.0, historyResp.Summary.TotalMonthlySavings)
}

// TestHandler_getHistory_IncludesPending verifies a pending PurchaseExecution
// shows up in the response as a synthetic history row with status=pending,
// counted toward TotalPending, and explicitly NOT folded into the dollar
// totals (TotalUpfront stays at the completed row's value).
func TestHandler_getHistory_IncludesPending(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	completed := []config.PurchaseHistoryRecord{
		{AccountID: "acc-1", PurchaseID: "done-1", UpfrontCost: 500.0, EstimatedSavings: 50.0},
	}
	pending := []config.PurchaseExecution{
		{
			ExecutionID: "pend-1",
			Status:      "pending",
			// Fresh scheduled_date keeps expireIfStale from transitioning
			// this row to "expired" during the test. The stale-approval
			// path is covered by its own test below.
			ScheduledDate:    time.Now(),
			TotalUpfrontCost: 999.0,
			EstimatedSavings: 99.0,
			Recommendations: []config.RecommendationRecord{
				{Provider: "aws", Service: "ec2", Region: "us-east-1"},
				{Provider: "aws", Service: "rds", Region: "us-east-1"},
			},
		},
	}

	approverEmail := "ops@example.com"
	mockStore.On("GetAllPurchaseHistory", ctx, 100).Return(completed, nil)
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return(pending, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.getHistory(ctx, req, map[string]string{})
	require.NoError(t, err)

	historyResp := result.(HistoryResponse)
	assert.Len(t, historyResp.Purchases, 2)
	assert.Equal(t, 2, historyResp.Summary.TotalPurchases)
	assert.Equal(t, 1, historyResp.Summary.TotalCompleted)
	assert.Equal(t, 1, historyResp.Summary.TotalPending)
	assert.Equal(t, 500.0, historyResp.Summary.TotalUpfront, "pending spend must not inflate committed totals")
	assert.Equal(t, 50.0, historyResp.Summary.TotalMonthlySavings)

	// Locate the pending row by PurchaseID and assert its shape.
	var pendingRow *config.PurchaseHistoryRecord
	for i := range historyResp.Purchases {
		if historyResp.Purchases[i].PurchaseID == "pend-1" {
			pendingRow = &historyResp.Purchases[i]
			break
		}
	}
	require.NotNil(t, pendingRow, "pending execution must render as a history row")
	assert.Equal(t, "pending", pendingRow.Status)
	assert.Equal(t, "aws", pendingRow.Provider, "single-provider execution must collapse to that provider")
	assert.Equal(t, 999.0, pendingRow.UpfrontCost)
	assert.Equal(t, 2, pendingRow.Count)
	assert.Equal(t, "2 commitment(s)", pendingRow.ResourceType)
	assert.Equal(t, approverEmail, pendingRow.Approver, "pending rows must expose the approver email so the UI can tell the user whose inbox to check")
}

func TestHandler_getHistory_CustomLimit(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	mockStore.On("GetAllPurchaseHistory", ctx, 50).Return([]config.PurchaseHistoryRecord{}, nil)
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{}, nil)

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	params := map[string]string{
		"limit": "50",
	}

	_, err := handler.getHistory(ctx, req, params)
	require.NoError(t, err)
}

// TestHandler_getHistory_ExpireIfStale exercises the lazy approval-window
// transition in expireIfStale. The History render seeds two non-completed
// executions: one fresh (left untouched) and one with ScheduledDate older than
// the 7-day approvalExpiryWindow. The stale row must be transitioned to
// "expired" via a single TransitionExecutionStatus call against its ID, and
// the rendered row must reflect the new status + the canonical
// "approval link expired …" StatusDescription. The fresh row stays pending
// and proves expireIfStale's age guard short-circuits before touching the
// store for non-stale rows. A second sub-test covers the error fallback:
// when TransitionExecutionStatus returns an error the original pending row
// must still render (no panic, no row dropped, status stays pending).
//
// Closes the gap flagged in known_issues/32_history_lazy_expire_test.md.
func TestHandler_getHistory_ExpireIfStale(t *testing.T) {
	freshID := "fresh-exec"
	staleID := "stale-exec"
	approverEmail := "ops@example.com"

	// Fresh: ScheduledDate = now → must NOT trigger TransitionExecutionStatus.
	freshExec := func() config.PurchaseExecution {
		return config.PurchaseExecution{
			ExecutionID:      freshID,
			Status:           "pending",
			ScheduledDate:    time.Now(),
			TotalUpfrontCost: 100.0,
			EstimatedSavings: 10.0,
			Recommendations: []config.RecommendationRecord{
				{Provider: "aws", Service: "ec2", Region: "us-east-1"},
			},
		}
	}

	// Stale: ScheduledDate older than approvalExpiryWindow (7 days) → must
	// trigger TransitionExecutionStatus once with the row's ID.
	staleExec := func(status string) config.PurchaseExecution {
		return config.PurchaseExecution{
			ExecutionID:      staleID,
			Status:           status,
			ScheduledDate:    time.Now().Add(-8 * 24 * time.Hour),
			TotalUpfrontCost: 200.0,
			EstimatedSavings: 20.0,
			Recommendations: []config.RecommendationRecord{
				{Provider: "aws", Service: "rds", Region: "us-east-1"},
			},
		}
	}

	t.Run("transitions stale pending row to expired", func(t *testing.T) {
		ctx := context.Background()
		mockStore := new(MockConfigStore)

		expired := staleExec("pending")
		expired.Status = "expired"

		mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).
			Return([]config.PurchaseExecution{freshExec(), staleExec("pending")}, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)
		mockStore.On("TransitionExecutionStatus", ctx, staleID, []string{"pending", "notified"}, "expired").
			Return(&expired, nil).Once()

		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		result, err := handler.getHistory(ctx, req, map[string]string{})
		require.NoError(t, err)

		// Single Transition call, only for the stale row.
		mockStore.AssertNumberOfCalls(t, "TransitionExecutionStatus", 1)
		mockStore.AssertCalled(t, "TransitionExecutionStatus", ctx, staleID, []string{"pending", "notified"}, "expired")

		historyResp := result.(HistoryResponse)
		require.Len(t, historyResp.Purchases, 2, "both executions must render as history rows")

		var staleRow, freshRow *config.PurchaseHistoryRecord
		for i := range historyResp.Purchases {
			switch historyResp.Purchases[i].PurchaseID {
			case staleID:
				staleRow = &historyResp.Purchases[i]
			case freshID:
				freshRow = &historyResp.Purchases[i]
			}
		}
		require.NotNil(t, staleRow, "stale execution must render as a history row")
		require.NotNil(t, freshRow, "fresh execution must render as a history row")

		assert.Equal(t, "expired", staleRow.Status, "stale row must reflect the expired transition")
		assert.NotEmpty(t, staleRow.StatusDescription, "expired row must carry an explanatory description")
		assert.Contains(t, staleRow.StatusDescription, "approval link expired",
			"expired description must use the canonical user-facing wording")

		assert.Equal(t, "pending", freshRow.Status, "fresh row must stay pending — age guard short-circuits the transition")
	})

	t.Run("transitions stale notified row to expired", func(t *testing.T) {
		ctx := context.Background()
		mockStore := new(MockConfigStore)

		expired := staleExec("notified")
		expired.Status = "expired"

		mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).
			Return([]config.PurchaseExecution{staleExec("notified")}, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)
		mockStore.On("TransitionExecutionStatus", ctx, staleID, []string{"pending", "notified"}, "expired").
			Return(&expired, nil).Once()

		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		result, err := handler.getHistory(ctx, req, map[string]string{})
		require.NoError(t, err)

		mockStore.AssertNumberOfCalls(t, "TransitionExecutionStatus", 1)

		historyResp := result.(HistoryResponse)
		require.Len(t, historyResp.Purchases, 1)
		assert.Equal(t, "expired", historyResp.Purchases[0].Status,
			"stale notified rows must transition the same as stale pending rows")
		assert.Equal(t, 1, historyResp.Summary.TotalExpired,
			"expired transition must be reflected in the summary bucket")
		assert.Equal(t, 0, historyResp.Summary.TotalPending)
	})

	t.Run("transition error falls back to pending row without panic", func(t *testing.T) {
		ctx := context.Background()
		mockStore := new(MockConfigStore)

		mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).
			Return([]config.PurchaseExecution{staleExec("pending")}, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)
		mockStore.On("TransitionExecutionStatus", ctx, staleID, []string{"pending", "notified"}, "expired").
			Return(nil, errors.New("simulated store failure")).Once()

		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		// The whole point: a Transition error is non-fatal. No panic, no
		// dropped row, status stays pending so the user still sees the
		// approval in the history view.
		result, err := handler.getHistory(ctx, req, map[string]string{})
		require.NoError(t, err)

		mockStore.AssertNumberOfCalls(t, "TransitionExecutionStatus", 1)

		historyResp := result.(HistoryResponse)
		require.Len(t, historyResp.Purchases, 1, "fallback must render the original row, not drop it")
		assert.Equal(t, staleID, historyResp.Purchases[0].PurchaseID)
		assert.Equal(t, "pending", historyResp.Purchases[0].Status,
			"transition failure must leave the row in its original status")
		assert.Equal(t, 1, historyResp.Summary.TotalPending, "summary must reflect the un-transitioned status")
		assert.Equal(t, 0, historyResp.Summary.TotalExpired)
	})
}

// TestHandler_getHistory_PermissionDenied asserts that a non-admin user without
// view:purchases gets 403 and never reaches the store.
func TestHandler_getHistory_PermissionDenied(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(&Session{
		UserID: "viewer-1",
		Role:   "user",
	}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "view", "purchases").Return(false, nil)

	handler := &Handler{auth: mockAuth, config: mockStore}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer viewer-token"},
	}
	_, err := handler.getHistory(ctx, req, map[string]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
	mockStore.AssertNotCalled(t, "GetPurchaseHistory")
	mockStore.AssertNotCalled(t, "GetAllPurchaseHistory")
}
