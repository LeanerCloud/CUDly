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

// TestHandler_getHistory_IncludesInFlightApprovals is the issue #621 primary-
// path regression guard. An execution stuck in "approved" (Lambda timeout /
// crash mid-execute) and one in "running" must BOTH appear in the History
// list, rendered with their real status and counted as in-progress — never
// silently dropped, never folded into the committed dollar totals. Pre-fix
// the statuses list excluded approved/running so these rows vanished.
func TestHandler_getHistory_IncludesInFlightApprovals(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	completed := []config.PurchaseHistoryRecord{
		{AccountID: "acc-1", PurchaseID: "done-1", UpfrontCost: 500.0, EstimatedSavings: 50.0},
	}
	approver := "ops@example.com"
	inFlight := []config.PurchaseExecution{
		{
			ExecutionID:      "appr-1",
			Status:           "approved",
			ScheduledDate:    time.Now(),
			TotalUpfrontCost: 999.0,
			EstimatedSavings: 99.0,
			ApprovedBy:       &approver,
			Recommendations:  []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Region: "us-east-1"}},
		},
		{
			ExecutionID:      "run-1",
			Status:           "running",
			ScheduledDate:    time.Now(),
			TotalUpfrontCost: 777.0,
			EstimatedSavings: 77.0,
			Recommendations:  []config.RecommendationRecord{{Provider: "aws", Service: "rds", Region: "us-east-1"}},
		},
	}

	mockStore.On("GetAllPurchaseHistory", ctx, 100).Return(completed, nil)
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return(inFlight, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approver}, nil)

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.getHistory(ctx, req, map[string]string{})
	require.NoError(t, err)
	resp := result.(HistoryResponse)

	byID := map[string]config.PurchaseHistoryRecord{}
	for _, p := range resp.Purchases {
		byID[p.PurchaseID] = p
	}
	apprRow, ok := byID["appr-1"]
	require.True(t, ok, "approved (stuck) execution must stay visible in History (issue #621)")
	assert.Equal(t, "approved", apprRow.Status)
	assert.Contains(t, apprRow.StatusDescription, "in progress", "approved row must render as in-progress, not as completed")
	runRow, ok := byID["run-1"]
	require.True(t, ok, "running execution must stay visible in History")
	assert.Equal(t, "running", runRow.Status)

	assert.Equal(t, 3, resp.Summary.TotalPurchases)
	assert.Equal(t, 1, resp.Summary.TotalCompleted)
	assert.Equal(t, 2, resp.Summary.TotalInProgress, "approved+running must count as in-progress")
	assert.Equal(t, 500.0, resp.Summary.TotalUpfront, "in-flight spend must not inflate committed totals")
	assert.Equal(t, 50.0, resp.Summary.TotalMonthlySavings)
}

// TestHandler_getHistory_InProgressRowMapsRecFields is the issue #631
// regression guard. A single-rec in-progress (approved/running/paused)
// execution must project the recommendation's real service / resource_type /
// region / term / count / costs onto its synthetic History row — the SAME
// shape a completed purchase_history row carries. Before the fix the row left
// Term=0 ("0 Years"), ResourceType="N commitment(s)" (rendered "multiple"-ish
// and never the real type), and pulled costs from the execution aggregate, so
// a valid 1yr t4g.nano RI rendered as "0 Years / 1 commitment(s) / $0" — the
// user could not tell what they had approved on a financial action.
func TestHandler_getHistory_InProgressRowMapsRecFields(t *testing.T) {
	monthly := 2.117
	rec := config.RecommendationRecord{
		Provider:     "aws",
		Service:      "ec2",
		Region:       "eu-west-1",
		ResourceType: "t4g.nano",
		Term:         1,
		Payment:      "no-upfront",
		Count:        1,
		UpfrontCost:  0,
		MonthlyCost:  &monthly,
		Savings:      1.2333,
	}

	for _, status := range []string{"approved", "running", "paused"} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			mockStore := new(MockConfigStore)

			inProgress := []config.PurchaseExecution{
				{
					ExecutionID:   "ip-1",
					Status:        status,
					ScheduledDate: time.Now(),
					// Execution aggregates intentionally differ from the rec so
					// the test proves the row is sourced from the rec, not these.
					TotalUpfrontCost: 12345.0,
					EstimatedSavings: 99.0,
					Recommendations:  []config.RecommendationRecord{rec},
				},
			}
			approver := "ops@example.com"
			mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
			mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return(inProgress, nil)
			mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approver}, nil)

			mockAuth, req := adminHistoryReq(ctx)
			handler := &Handler{auth: mockAuth, config: mockStore}

			result, err := handler.getHistory(ctx, req, map[string]string{})
			require.NoError(t, err)
			resp := result.(HistoryResponse)
			require.Len(t, resp.Purchases, 1)
			row := resp.Purchases[0]

			assert.Equal(t, status, row.Status)
			assert.Equal(t, "ec2", row.Service, "service must come from the rec, not be left blank")
			assert.Equal(t, "t4g.nano", row.ResourceType, "resource type must be the rec's, not 'N commitment(s)'/multiple")
			assert.Equal(t, "eu-west-1", row.Region, "region must be the rec's, not 'multiple'")
			assert.Equal(t, 1, row.Term, "term must be the rec's 1yr, not 0 ('0 Years')")
			assert.Equal(t, 1, row.Count)
			assert.Equal(t, 0.0, row.UpfrontCost, "upfront must come from the rec")
			assert.Equal(t, 1.2333, row.EstimatedSavings, "savings must come from the rec")
			assert.Equal(t, 2.117, row.MonthlyCost, "monthly cost must come from the rec")
			assert.NotEmpty(t, row.StatusDescription,
				"in-progress rows must carry a human-readable status description, not render as a finished purchase")
		})
	}
}

// TestHandler_getHistory_AuditGapCompletedVisible is the issue #621 secondary-
// path regression guard. A "completed" execution whose purchase_history write
// failed (carries a non-empty Error) MUST surface in the History list — the
// purchase happened, the money was committed, and silently dropping it is the
// worst failure mode for a financial action. It is rendered completed (the
// purchase succeeded) with a StatusDescription that surfaces the audit gap.
func TestHandler_getHistory_AuditGapCompletedVisible(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	auditGap := []config.PurchaseExecution{
		{
			ExecutionID:      "gap-1",
			Status:           "completed",
			Error:            "commitment ri-abc purchased but its history record failed to save: insert failed",
			ScheduledDate:    time.Now(),
			TotalUpfrontCost: 300.0,
			EstimatedSavings: 30.0,
			Recommendations:  []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Region: "us-east-1"}},
		},
	}
	approver := "ops@example.com"
	mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return(auditGap, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approver}, nil)

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.getHistory(ctx, req, map[string]string{})
	require.NoError(t, err)
	resp := result.(HistoryResponse)

	require.Len(t, resp.Purchases, 1, "audit-gap completed execution must not be silently dropped (issue #621)")
	row := resp.Purchases[0]
	assert.Equal(t, "gap-1", row.PurchaseID)
	assert.Equal(t, "completed", row.Status)
	assert.Contains(t, row.StatusDescription, "history record could not be saved", "the audit gap must be surfaced to the user")
	assert.True(t, row.IsAuditGap, "synthesised audit-gap row must carry the explicit IsAuditGap marker")
	assert.Equal(t, 1, resp.Summary.TotalCompleted, "money was committed, so it counts as completed")
	// Double-count guard: the synthesised audit-gap row is an audit flag, not a
	// money source. A partially-saved multi-rec execution can have BOTH some
	// purchase_history rows AND this synthesised row, so its execution-level
	// dollars must NOT be added to the committed totals (those come from the
	// purchase_history rows that actually saved).
	assert.Equal(t, 0.0, resp.Summary.TotalUpfront, "audit-gap row must not contribute execution-level dollars (double-count risk)")
	assert.Equal(t, 0.0, resp.Summary.TotalMonthlySavings)
}

// TestHandler_getHistory_PartiallyCompletedVisible is the issue #642 regression
// guard. A "partially_completed" execution (some recs committed, some failed)
// must be surfaced in History as a non-failed row carrying a clear description,
// count as completed (money was committed), and have its execution-level
// dollars excluded via IsAuditGap (the committed dollars are counted on the
// per-rec purchase_history rows that actually saved, not here).
func TestHandler_getHistory_PartiallyCompletedVisible(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	partial := []config.PurchaseExecution{
		{
			ExecutionID:      "partial-1",
			Status:           "partially_completed",
			Error:            "some purchases failed (partial success): c5.xlarge: offering not available",
			ScheduledDate:    time.Now(),
			TotalUpfrontCost: 900.0,
			EstimatedSavings: 140.0,
			Recommendations:  []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Region: "us-east-1"}},
		},
	}
	approver := "ops@example.com"
	mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return(partial, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approver}, nil)

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.getHistory(ctx, req, map[string]string{})
	require.NoError(t, err)
	resp := result.(HistoryResponse)

	require.Len(t, resp.Purchases, 1, "partially_completed execution must be surfaced in History (issue #642)")
	row := resp.Purchases[0]
	assert.Equal(t, "partial-1", row.PurchaseID)
	assert.Equal(t, "partially_completed", row.Status)
	assert.NotEqual(t, "failed", row.Status, "a partial run with real commitments must never read as failed (double-spend hazard)")
	assert.Contains(t, row.StatusDescription, "partially completed", "the partial outcome must be surfaced to the user")
	assert.True(t, row.IsAuditGap, "partial row must carry IsAuditGap so its execution-level dollars are excluded")
	assert.Equal(t, 1, resp.Summary.TotalCompleted, "money was committed, so it counts as completed")
	assert.Equal(t, 0.0, resp.Summary.TotalUpfront, "partial row must not contribute execution-level dollars (committed dollars come from purchase_history rows)")
	assert.Equal(t, 0.0, resp.Summary.TotalMonthlySavings)
}

// TestHandler_getHistory_CompletedDBRowWithDescriptionStillCounts guards the
// financial invariant that dollar exclusion keys off the explicit IsAuditGap
// marker, NOT off StatusDescription being set. A real purchase_history row
// loaded from the DB always has IsAuditGap=false, so even if some future code
// path annotates it with a StatusDescription, its committed dollars must still
// flow into the totals; undercounting committed spend is as wrong as
// double-counting it (issue #621).
func TestHandler_getHistory_CompletedDBRowWithDescriptionStillCounts(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	// A genuine completed purchase_history row that happens to carry a
	// human-readable StatusDescription (IsAuditGap stays false, as it is for
	// every DB-loaded row). Its dollars are committed and must be counted.
	completed := []config.PurchaseHistoryRecord{
		{
			AccountID:         "acc-1",
			PurchaseID:        "ri-commitment-1",
			Status:            "completed",
			StatusDescription: "approved by ops@example.com",
			UpfrontCost:       700.0,
			EstimatedSavings:  70.0,
		},
	}
	mockStore.On("GetAllPurchaseHistory", ctx, 100).Return(completed, nil)
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{}, nil)
	approver := "ops@example.com"
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approver}, nil)

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.getHistory(ctx, req, map[string]string{})
	require.NoError(t, err)
	resp := result.(HistoryResponse)

	require.Len(t, resp.Purchases, 1)
	assert.False(t, resp.Purchases[0].IsAuditGap, "DB-loaded completed rows are never audit gaps")
	assert.Equal(t, 1, resp.Summary.TotalCompleted)
	assert.Equal(t, 700.0, resp.Summary.TotalUpfront, "a completed DB row with a StatusDescription must still count its committed dollars")
	assert.Equal(t, 70.0, resp.Summary.TotalMonthlySavings)
}

// TestHandler_getHistory_CompletedExecutionNotDuplicated guards the dedup path.
// The store loads "completed" executions now (so audit-gap rows can surface),
// but a NORMAL completed execution (Error=="") is already represented by its
// purchase_history rows and must NOT be synthesised a second time. The History
// list must contain exactly one row for that purchase.
func TestHandler_getHistory_CompletedExecutionNotDuplicated(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	completed := []config.PurchaseHistoryRecord{
		{AccountID: "acc-1", PurchaseID: "ri-commitment-1", UpfrontCost: 400.0, EstimatedSavings: 40.0},
	}
	// A clean completed execution exists alongside a purchase_history row. The
	// execution (exec-clean-1) and the purchase_history row (ri-commitment-1)
	// are separate records with different IDs; the test does not assert they
	// match. It asserts that ALL clean completed executions are skipped (not
	// synthesised) because they are assumed already represented by their
	// purchase_history rows, so the surviving row is the purchase_history one.
	cleanCompletedExec := []config.PurchaseExecution{
		{
			ExecutionID:     "exec-clean-1",
			Status:          "completed",
			Error:           "",
			ScheduledDate:   time.Now(),
			Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Region: "us-east-1"}},
		},
	}
	approver := "ops@example.com"
	mockStore.On("GetAllPurchaseHistory", ctx, 100).Return(completed, nil)
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return(cleanCompletedExec, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approver}, nil)

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.getHistory(ctx, req, map[string]string{})
	require.NoError(t, err)
	resp := result.(HistoryResponse)

	require.Len(t, resp.Purchases, 1, "a clean completed execution must not duplicate its purchase_history row")
	assert.Equal(t, "ri-commitment-1", resp.Purchases[0].PurchaseID, "the surviving row is the purchase_history row")
	assert.Equal(t, 1, resp.Summary.TotalCompleted)
	assert.Equal(t, 400.0, resp.Summary.TotalUpfront)
}
