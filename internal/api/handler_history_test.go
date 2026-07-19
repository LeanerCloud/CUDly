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
	}, nil)
	mockAuth.grantAdmin()
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

	// account_id "123456789012" is not a known cloud_accounts UUID, so it is
	// folded into the dual-column filter as an external account number and the
	// request routes through GetPurchaseHistoryFiltered (issue #701/#498/#866).
	mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{ExternalIDsByProvider: map[string][]string{"": {"123456789012"}}, Limit: 100}).Return(history, nil)
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

// TestHandler_getHistory_ExpireIfStale exercises the async approval-window
// expiry sweep (issue #1032). The History read fires a best-effort background
// goroutine to transition stale pending/notified executions to "expired" so
// the GET handler itself is a pure read. The goroutine runs AFTER the response
// is assembled, so the response shows the pre-transition status ("pending");
// the next History load sees "expired" from the DB.
//
// Each sub-test wires a done channel into the mock's Run callback to
// synchronize with the async goroutine — no time.Sleep per project memory.
// The fresh-row sub-test asserts TransitionExecutionStatus is never called for
// a non-stale execution (the staleness guard short-circuits before enqueueing
// the ID).
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
	// trigger TransitionExecutionStatus once with the row's ID (asynchronously).
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

	// waitForCall returns a Run option that signals done when the mock is
	// called. Use it to synchronize with the background goroutine without
	// sleeping.
	waitForCall := func(done chan<- struct{}) func(mock.Arguments) {
		return func(_ mock.Arguments) { close(done) }
	}

	t.Run("async expire fires for stale pending row; response shows pre-transition status", func(t *testing.T) {
		ctx := context.Background()
		mockStore := new(MockConfigStore)

		expired := staleExec("pending")
		expired.Status = "expired"
		done := make(chan struct{})

		mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).
			Return([]config.PurchaseExecution{freshExec(), staleExec("pending")}, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)
		// The goroutine uses context.Background(); context.Background() == ctx in
		// this test, so the matcher fires correctly. The trailing mock.Anything
		// matches the actor *string (nil for the system-initiated async expire).
		mockStore.On("TransitionExecutionStatus", mock.Anything, staleID, []string{"pending", "notified"}, "expired", mock.Anything).
			Run(waitForCall(done)).
			Return(&expired, nil).Once()

		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		result, err := handler.getHistory(ctx, req, map[string]string{})
		require.NoError(t, err)

		// Wait for the background goroutine to complete before asserting the
		// call count. No sleep: we block on the done channel.
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("TransitionExecutionStatus goroutine did not fire within 5s")
		}

		// Exactly one Transition call, only for the stale row.
		mockStore.AssertNumberOfCalls(t, "TransitionExecutionStatus", 1)
		mockStore.AssertCalled(t, "TransitionExecutionStatus", mock.Anything, staleID, []string{"pending", "notified"}, "expired", mock.Anything)

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

		// The GET is now a pure read: the response reflects the pre-transition
		// status ("pending"). The background goroutine fires the transition so
		// the NEXT History load sees "expired". This is the post-fix invariant.
		assert.Equal(t, "pending", staleRow.Status,
			"issue #1032: GET must not mutate state — stale row shows pre-transition status in this response")
		assert.Equal(t, "pending", freshRow.Status, "fresh row must stay pending — staleness guard short-circuits enqueue")
	})

	t.Run("async expire fires for stale notified row; response shows pre-transition status", func(t *testing.T) {
		ctx := context.Background()
		mockStore := new(MockConfigStore)

		expired := staleExec("notified")
		expired.Status = "expired"
		done := make(chan struct{})

		mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).
			Return([]config.PurchaseExecution{staleExec("notified")}, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)
		mockStore.On("TransitionExecutionStatus", mock.Anything, staleID, []string{"pending", "notified"}, "expired", mock.Anything).
			Run(waitForCall(done)).
			Return(&expired, nil).Once()

		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		result, err := handler.getHistory(ctx, req, map[string]string{})
		require.NoError(t, err)

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("TransitionExecutionStatus goroutine did not fire within 5s")
		}
		mockStore.AssertNumberOfCalls(t, "TransitionExecutionStatus", 1)

		historyResp := result.(HistoryResponse)
		require.Len(t, historyResp.Purchases, 1)
		// Response shows pre-transition status; the DB is updated asynchronously.
		assert.Equal(t, "notified", historyResp.Purchases[0].Status,
			"issue #1032: GET must not mutate — stale notified row shows pre-transition status in this response")
		assert.Equal(t, 1, historyResp.Summary.TotalPending,
			"pre-transition stale notified row counts as pending in this response")
		assert.Equal(t, 0, historyResp.Summary.TotalExpired,
			"the expired state is not visible in this response; next load will reflect it")
	})

	t.Run("async expire error is non-fatal; response and row visibility are unaffected", func(t *testing.T) {
		ctx := context.Background()
		mockStore := new(MockConfigStore)
		done := make(chan struct{})

		mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).
			Return([]config.PurchaseExecution{staleExec("pending")}, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)
		mockStore.On("TransitionExecutionStatus", mock.Anything, staleID, []string{"pending", "notified"}, "expired", mock.Anything).
			Run(waitForCall(done)).
			Return(nil, errors.New("simulated store failure")).Once()

		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		// A Transition error must be non-fatal. No panic, no dropped row —
		// the stale row still renders (with its pre-transition status) so the
		// user can see the pending approval even when the background expire fails.
		result, err := handler.getHistory(ctx, req, map[string]string{})
		require.NoError(t, err)

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("TransitionExecutionStatus goroutine did not fire within 5s")
		}
		mockStore.AssertNumberOfCalls(t, "TransitionExecutionStatus", 1)

		historyResp := result.(HistoryResponse)
		require.Len(t, historyResp.Purchases, 1, "transition error must not drop the row")
		assert.Equal(t, staleID, historyResp.Purchases[0].PurchaseID)
		assert.Equal(t, "pending", historyResp.Purchases[0].Status,
			"row is visible with pre-transition status; the transition failed so next load may retry it")
		assert.Equal(t, 1, historyResp.Summary.TotalPending, "stale row counts as pending since transition failed")
		assert.Equal(t, 0, historyResp.Summary.TotalExpired)
	})

	t.Run("fresh row does not trigger TransitionExecutionStatus", func(t *testing.T) {
		ctx := context.Background()
		mockStore := new(MockConfigStore)

		mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).
			Return([]config.PurchaseExecution{freshExec()}, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)

		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		result, err := handler.getHistory(ctx, req, map[string]string{})
		require.NoError(t, err)

		// No goroutine should have been spawned — assert zero calls directly.
		// There is no done channel because the goroutine must not exist.
		mockStore.AssertNumberOfCalls(t, "TransitionExecutionStatus", 0)

		historyResp := result.(HistoryResponse)
		require.Len(t, historyResp.Purchases, 1)
		assert.Equal(t, freshID, historyResp.Purchases[0].PurchaseID)
		assert.Equal(t, "pending", historyResp.Purchases[0].Status)
	})
}

// TestHandler_getHistory_GetIsReadOnly is the issue #1032 defect-1 regression
// guard. The GET /api/history handler must not call TransitionExecutionStatus
// synchronously — it must not block on or return from a write. Pre-fix the
// transition was inlined in fetchExecutionsAsHistory, so the handler wrote DB
// state on every call for any stale pending row, racing concurrent loads.
//
// This test seeds one stale pending execution. It wires the
// TransitionExecutionStatus mock behind a channel gate and asserts that
// getHistory returns BEFORE the goroutine releases the gate — i.e. the
// response is produced without waiting for the write to complete.
//
// Fails pre-fix: the old inline expireIfStale runs synchronously so the
// handler blocks until TransitionExecutionStatus returns; opening the gate
// AFTER getHistory returns would mean the mock was never called in the first
// place (0 calls, but the mock has Once() and the close() never fires, so
// AssertNumberOfCalls panics on the missed expectation).
func TestHandler_getHistory_GetIsReadOnly(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	approverEmail := "ops@example.com"

	stale := config.PurchaseExecution{
		ExecutionID:   "stale-ro-exec",
		Status:        "pending",
		ScheduledDate: time.Now().Add(-8 * 24 * time.Hour),
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", Region: "us-east-1"},
		},
	}

	// gate blocks the TransitionExecutionStatus mock until the test releases it.
	// Pre-fix: the handler blocks on this gate and returns only after it is
	// closed — proving the write was synchronous. Post-fix: the handler returns
	// immediately and the goroutine blocks on the gate independently.
	gate := make(chan struct{})
	transitionCalled := make(chan struct{})
	mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{stale}, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)
	mockStore.On("TransitionExecutionStatus", mock.Anything, "stale-ro-exec", []string{"pending", "notified"}, "expired", mock.Anything).
		Run(func(_ mock.Arguments) {
			close(transitionCalled) // signal that the goroutine reached the transition
			<-gate                  // block until the test releases it
		}).
		Return(nil, nil).Maybe()

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	// getHistory must return without waiting for TransitionExecutionStatus.
	result, err := handler.getHistory(ctx, req, map[string]string{})
	require.NoError(t, err, "handler must not error even when the background expire has not run yet")

	// At this point the handler has returned. The transition goroutine may or
	// may not have started yet — we do NOT assert its call count here. We assert
	// the response shape (the read-only view): the row is present with its
	// pre-transition status, not dropped.
	resp := result.(HistoryResponse)
	require.Len(t, resp.Purchases, 1, "stale execution must appear in response (pre-transition status)")
	assert.Equal(t, "pending", resp.Purchases[0].Status,
		"issue #1032: GET must return current DB state without waiting for the expire write")

	// Now wait for the goroutine to call the transition, then release the gate
	// so the test teardown can complete cleanly.
	select {
	case <-transitionCalled:
	case <-time.After(5 * time.Second):
		// If we timed out it means the goroutine never fired — that would be a
		// bug (the expire must still happen, just not synchronously).
		t.Fatal("expire goroutine did not call TransitionExecutionStatus within 5s")
	}
	close(gate) // unblock the goroutine so it can finish
}

// TestHandler_getHistory_ScopedUserSeesEmptyAccountRows is the issue #1032
// defect-2 / #621 regression guard. An execution with CloudAccountID == nil
// and no common per-rec account (accountID == "") must remain visible to a
// scoped (non-admin) user. Pre-fix, filterPurchaseHistoryByAllowedAccounts
// called auth.MatchesAccount(allowed, "", "") == false for any non-empty
// allowed list, silently dropping the row — re-introducing the #621
// disappearance bug for non-admins. Post-fix the filter passes empty-AccountID
// rows through unconditionally (they carry no account-specific data that could
// violate cross-tenant isolation).
//
// Fails pre-fix: the empty-account execution row is dropped and
// resp.Purchases is empty, causing the require.Len assertion to fail.
func TestHandler_getHistory_ScopedUserSeesEmptyAccountRows(t *testing.T) {
	ctx := context.Background()

	const scopedUserID = "scoped-user-id"

	// Ambient execution created by the scoped user: CloudAccountID == nil and
	// recommendations carry no common account. CreatedByUserID must match the
	// requesting session so the ownership gate (adversarial-review F1) passes.
	creatorID := scopedUserID
	ambientExec := config.PurchaseExecution{
		ExecutionID:     "ambient-exec-1",
		Status:          "pending",
		ScheduledDate:   time.Now(),
		CreatedByUserID: &creatorID,
		Recommendations: []config.RecommendationRecord{
			// No CloudAccountID on either rec → collapseRecommendationAccount
			// returns "" → executionToHistoryRow sets AccountID = "".
			{Provider: "aws", Service: "ec2", Region: "us-east-1"},
			{Provider: "aws", Service: "rds", Region: "us-east-1"},
		},
	}

	mockStore := new(MockConfigStore)
	approverEmail := "ops@example.com"
	mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{ambientExec}, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)
	// resolveAccountNamesByID must not error — return an empty accounts list
	// so MatchesAccount falls back to ID-only matching (safe for our assertions).
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return nil, nil
	}

	// Scoped user: only allowed to access one specific account UUID.
	scopedUser := &Session{
		UserID: scopedUserID,
		Email:  "scoped@example.com",
	}
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "scoped-token").Return(scopedUser, nil)
	mockAuth.On("HasPermissionAPI", ctx, scopedUserID, "view", "purchases").Return(true, nil)
	// allowed_accounts is non-empty → user is scoped (not unrestricted).
	mockAuth.On("GetAllowedAccountsAPI", ctx, scopedUserID).Return([]string{"aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa"}, nil)
	// resolveUserEmails resolves creator emails; stub for the scoped user's own ID.
	mockAuth.On("GetUser", ctx, scopedUserID).Return(&User{Email: "scoped@example.com"}, nil).Maybe()

	handler := &Handler{
		auth:   mockAuth,
		config: mockStore,
	}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer scoped-token"},
	}
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	result, err := handler.getHistory(ctx, req, map[string]string{})
	require.NoError(t, err)

	resp := result.(HistoryResponse)
	require.Len(t, resp.Purchases, 1,
		"issue #1032 / #621 regression: a scoped user must see their own in-flight ambient-account executions; dropping them re-creates the financial-action-vanishes bug")
	assert.Equal(t, "ambient-exec-1", resp.Purchases[0].PurchaseID)
	assert.Equal(t, "", resp.Purchases[0].AccountID,
		"the AccountID is empty (ambient execution) and must remain visible to scoped users who created the row")
	assert.Equal(t, "pending", resp.Purchases[0].Status)
	assert.Equal(t, 1, resp.Summary.TotalPending)
}

// TestHandler_getHistory_ScopedUserCannotSeeOtherUsersEmptyAccountRows is the
// adversarial-review F1 regression test: a scoped user must NOT see in-flight
// ambient-account rows (AccountID == "") created by OTHER users. Before the
// fix, filterPurchaseHistoryByAllowedAccounts passed ALL empty-AccountID rows
// through unconditionally, leaking other users' CreatedByUserEmail (PII) and
// dollar amounts to any scoped user who had view:purchases.
//
// Fails pre-fix: user B's ambient row passes through the empty-AccountID
// exemption and resp.Purchases has length 1 instead of 0.
func TestHandler_getHistory_ScopedUserCannotSeeOtherUsersEmptyAccountRows(t *testing.T) {
	ctx := context.Background()

	const userAID = "user-a-id"
	const userBID = "user-b-id"

	// Ambient execution created by user B with PII-carrying fields.
	creatorID := userBID
	otherUsersExec := config.PurchaseExecution{
		ExecutionID:     "other-user-ambient-exec",
		Status:          "pending",
		ScheduledDate:   time.Now(),
		CreatedByUserID: &creatorID,
		Recommendations: []config.RecommendationRecord{
			// No cloud account → AccountID will be "".
			{Provider: "aws", Service: "ec2", Region: "us-east-1"},
		},
	}

	mockStore := new(MockConfigStore)
	approverEmail := "ops@example.com"
	mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{otherUsersExec}, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return nil, nil
	}

	// User A: scoped to one account, different from user B.
	userASession := &Session{UserID: userAID, Email: "a@example.com"}
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "user-a-token").Return(userASession, nil)
	mockAuth.On("HasPermissionAPI", ctx, userAID, "view", "purchases").Return(true, nil)
	mockAuth.On("GetAllowedAccountsAPI", ctx, userAID).Return([]string{"aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa"}, nil)
	// resolveUserEmails may try to look up user B's email; stub it to return a
	// value so the test exercises the actual row filtering, not an email lookup error.
	mockAuth.On("GetUser", ctx, userBID).Return(&User{Email: "b@example.com"}, nil).Maybe()

	handler := &Handler{auth: mockAuth, config: mockStore}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer user-a-token"},
	}
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	result, err := handler.getHistory(ctx, req, map[string]string{})
	require.NoError(t, err)

	resp := result.(HistoryResponse)
	assert.Empty(t, resp.Purchases,
		"adversarial-review F1: scoped user A must NOT see another user's empty-account in-flight row (PII/financial leak)")
}

// TestHandler_expireStaleExecutionsAsync_SystemActorIsNil asserts that the
// async stale-expire sweep passes nil as the actor param to
// TransitionExecutionStatus. Expiry is a system-initiated path (no human
// session), so transitioned_by must be NULL on the affected row (issue #1009).
// The transition fires in a background goroutine (issue #1032: GET is a pure
// read), so the test blocks on a done channel rather than asserting
// synchronously.
func TestHandler_expireStaleExecutionsAsync_SystemActorIsNil(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	approverEmail := "ops@example.com"
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	staleID := "actor-nil-stale-exec"
	expired := config.PurchaseExecution{ExecutionID: staleID, Status: "expired"}
	done := make(chan struct{})

	mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).
		Return([]config.PurchaseExecution{{
			ExecutionID:   staleID,
			Status:        "pending",
			ScheduledDate: time.Now().Add(-8 * 24 * time.Hour),
		}}, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)
	// System path: the async expire must pass nil actor so transitioned_by =
	// NULL. The (*string)(nil) literal is the contract under test. The
	// goroutine uses context.Background(); use mock.Anything for ctx.
	mockStore.On("TransitionExecutionStatus", mock.Anything, staleID, []string{"pending", "notified"}, "expired",
		(*string)(nil),
	).Run(func(_ mock.Arguments) { close(done) }).Return(&expired, nil).Once()

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}
	_, err := handler.getHistory(ctx, req, map[string]string{})
	require.NoError(t, err)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("expire goroutine did not call TransitionExecutionStatus within 5s")
	}
}

// TestHandler_getHistory_PermissionDenied asserts that a non-admin user without
// view:purchases gets 403 and never reaches the store.
func TestHandler_getHistory_PermissionDenied(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(&Session{
		UserID: "viewer-1",
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
			require.NotNil(t, row.MonthlyCost, "monthly cost must come from the rec")
			assert.InDelta(t, 2.117, *row.MonthlyCost, 1e-9, "monthly cost must come from the rec")
			assert.Equal(t, "no-upfront", row.Payment, "payment must come from the rec, not be left blank (#733)")
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
	assert.True(t, row.IsAuditGap, "synthesized audit-gap row must carry the explicit IsAuditGap marker")
	assert.Equal(t, 1, resp.Summary.TotalCompleted, "money was committed, so it counts as completed")
	// Double-count guard: the synthesized audit-gap row is an audit flag, not a
	// money source. A partially-saved multi-rec execution can have BOTH some
	// purchase_history rows AND this synthesized row, so its execution-level
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

// TestHandler_getHistory_FilterParams is the issue #701 primary regression
// guard. /api/history must honor the provider / account_ids / start / end
// query params the frontend sends — both on the SQL path (purchase_history
// rows in fetchPurchaseHistory) and on the in-memory path (synthesized
// execution rows in fetchExecutionsAsHistory). The filters were previously
// dropped silently; visible filter affordances were no-ops.
//
// Each sub-test seeds enough rows on both halves that an unfiltered
// response would include extras, then asserts the filter prunes both halves
// consistently and that the SQL path receives the right store call.
//
// Each filter is exercised independently first, then combined.
func TestHandler_getHistory_FilterParams(t *testing.T) {
	approver := "ops@example.com"

	// Helper to build a fresh handler/store/req triple per sub-test so mock
	// expectations don't bleed across cases.
	newHandler := func(t *testing.T) (*MockConfigStore, *Handler, *events.LambdaFunctionURLRequest, context.Context) {
		t.Helper()
		ctx := context.Background()
		mockStore := new(MockConfigStore)
		mockAuth, req := adminHistoryReq(ctx)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approver}, nil).Maybe()
		// resolveHistoryAccountFilter loads cloud_accounts to resolve UUIDs to
		// their external ids. Default to an empty list (no resolvable external
		// ids) unless a sub-test overrides ListCloudAccountsFn.
		mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
			return nil, nil
		}
		return mockStore, &Handler{auth: mockAuth, config: mockStore}, req, ctx
	}

	t.Run("provider filter is pushed to SQL and prunes executions", func(t *testing.T) {
		mockStore, handler, req, ctx := newHandler(t)

		// SQL path: must be called via GetPurchaseHistoryFiltered with
		// provider="aws" and no other filters set. Return only the aws row.
		filtered := []config.PurchaseHistoryRecord{
			{AccountID: "acc-aws", PurchaseID: "p-aws", Provider: "aws", UpfrontCost: 100.0},
		}
		mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{Provider: "aws", Limit: config.DefaultListLimit}).
			Return(filtered, nil).Once()

		// Executions path: two execs, one aws-rec and one azure-rec. Only the
		// aws-rec exec must survive the in-memory provider filter.
		execs := []config.PurchaseExecution{
			{
				ExecutionID:     "exec-aws",
				Status:          "pending",
				ScheduledDate:   time.Now(),
				Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Region: "us-east-1"}},
			},
			{
				ExecutionID:     "exec-azure",
				Status:          "pending",
				ScheduledDate:   time.Now(),
				Recommendations: []config.RecommendationRecord{{Provider: "azure", Service: "vm", Region: "westeurope"}},
			},
		}
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return(execs, nil)

		result, err := handler.getHistory(ctx, req, map[string]string{"provider": "aws"})
		require.NoError(t, err)
		resp := result.(HistoryResponse)

		// 1 DB row + 1 surviving exec.
		require.Len(t, resp.Purchases, 2)
		for _, p := range resp.Purchases {
			assert.NotEqual(t, "exec-azure", p.PurchaseID, "azure-only execution must be filtered out when provider=aws")
		}
		mockStore.AssertCalled(t, "GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{Provider: "aws", Limit: config.DefaultListLimit})
	})

	t.Run("provider=all is treated as no filter (legacy SQL path)", func(t *testing.T) {
		mockStore, handler, req, ctx := newHandler(t)
		mockStore.On("GetAllPurchaseHistory", ctx, config.DefaultListLimit).Return([]config.PurchaseHistoryRecord{}, nil).Once()
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{}, nil)

		_, err := handler.getHistory(ctx, req, map[string]string{"provider": "all"})
		require.NoError(t, err)
		mockStore.AssertCalled(t, "GetAllPurchaseHistory", ctx, config.DefaultListLimit)
		mockStore.AssertNotCalled(t, "GetPurchaseHistoryFiltered", mock.Anything, mock.Anything)
	})

	t.Run("account_ids filter is pushed to SQL and prunes executions", func(t *testing.T) {
		mockStore, handler, req, ctx := newHandler(t)

		uuidA := "11111111-1111-1111-1111-111111111111"
		uuidB := "22222222-2222-2222-2222-222222222222"

		mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{AccountIDs: []string{uuidA, uuidB}, Limit: config.DefaultListLimit}).
			Return([]config.PurchaseHistoryRecord{{AccountID: "acc-A", PurchaseID: "p-A"}}, nil).Once()

		execs := []config.PurchaseExecution{
			{
				ExecutionID:     "exec-in-A",
				Status:          "pending",
				ScheduledDate:   time.Now(),
				CloudAccountID:  strPtr(uuidA),
				Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2"}},
			},
			{
				ExecutionID:     "exec-in-C",
				Status:          "pending",
				ScheduledDate:   time.Now(),
				CloudAccountID:  strPtr("33333333-3333-3333-3333-333333333333"),
				Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2"}},
			},
			{
				ExecutionID:     "exec-nil-account",
				Status:          "pending",
				ScheduledDate:   time.Now(),
				Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2"}},
			},
		}
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return(execs, nil)

		result, err := handler.getHistory(ctx, req, map[string]string{"account_ids": uuidA + "," + uuidB})
		require.NoError(t, err)
		resp := result.(HistoryResponse)

		// 1 DB row + only the exec whose CloudAccountID is in the filter list.
		// NULL CloudAccountID execs must be excluded (mirrors SQL semantics).
		require.Len(t, resp.Purchases, 2)
		ids := map[string]bool{}
		for _, p := range resp.Purchases {
			ids[p.PurchaseID] = true
		}
		assert.True(t, ids["exec-in-A"], "execution in account A must survive the filter")
		assert.False(t, ids["exec-in-C"], "execution in unrelated account C must be filtered out")
		assert.False(t, ids["exec-nil-account"], "execution with NULL exec.CloudAccountID AND no rec-level CloudAccountID must still be excluded (effective account is empty)")
	})

	t.Run("legacy singular account_id is folded into the dual-column filter", func(t *testing.T) {
		// account_id (singular) is an AWS-style external account number (not a
		// known cloud_accounts UUID in this fixture). resolveHistoryAccountFilter
		// folds it into the ExternalIDs half of the dual-column predicate so it
		// is matched against purchase_history.account_id alongside any other
		// filters — fixing the prior bug where the legacy param was silently
		// dropped when combined with provider/account_ids (issue #701/#498/#866).
		mockStore, handler, req, ctx := newHandler(t)

		mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{Provider: "aws", ExternalIDsByProvider: map[string][]string{"": {"123456789012"}}, Limit: config.DefaultListLimit}).
			Return([]config.PurchaseHistoryRecord{}, nil).Once()
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{}, nil)

		_, err := handler.getHistory(ctx, req, map[string]string{"provider": "aws", "account_id": "123456789012"})
		require.NoError(t, err)
		mockStore.AssertExpectations(t)
		mockStore.AssertNotCalled(t, "GetPurchaseHistory", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("legacy singular account_id alone routes through the dual-column filter", func(t *testing.T) {
		// A bare external account number is now matched on account_id via the
		// dual-column filter rather than the single-column GetPurchaseHistory
		// fast path, so its rows surface regardless of cloud_account_id.
		mockStore, handler, req, ctx := newHandler(t)
		mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{ExternalIDsByProvider: map[string][]string{"": {"123456789012"}}, Limit: config.DefaultListLimit}).
			Return([]config.PurchaseHistoryRecord{{AccountID: "123456789012", PurchaseID: "p-legacy"}}, nil).Once()
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{}, nil)

		_, err := handler.getHistory(ctx, req, map[string]string{"account_id": "123456789012"})
		require.NoError(t, err)
		mockStore.AssertExpectations(t)
		mockStore.AssertNotCalled(t, "GetPurchaseHistory", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("start/end date filter is pushed to SQL and prunes executions", func(t *testing.T) {
		mockStore, handler, req, ctx := newHandler(t)

		// Build start/end relative to "now" so the in-range execution's
		// ScheduledDate stays younger than the approvalExpiryWindow (7 days).
		// Older ScheduledDates would trigger the lazy expireIfStale path and
		// require mocking TransitionExecutionStatus — orthogonal to what
		// this test covers.
		now := time.Now().UTC()
		startDay := now.AddDate(0, 0, -3).Format("2006-01-02")
		endDay := now.Format("2006-01-02")
		outOfRangeDay := now.AddDate(0, 0, -10) // older than the window AND outside the requested range

		mockStore.On("GetPurchaseHistoryFiltered", ctx,
			mock.MatchedBy(func(f config.PurchaseHistoryFilter) bool {
				return f.Provider == "" && len(f.AccountIDs) == 0 && len(f.ExternalIDsByProvider) == 0 &&
					f.Start != nil && f.Start.Format("2006-01-02") == startDay &&
					f.End != nil && f.End.Format("2006-01-02") == endDay &&
					f.Limit == config.DefaultListLimit
			}),
		).Return([]config.PurchaseHistoryRecord{{AccountID: "in-range", PurchaseID: "p-in-range"}}, nil).Once()

		execs := []config.PurchaseExecution{
			{
				ExecutionID:     "exec-in-range",
				Status:          "approved", // approved -> expireIfStale short-circuits before transition
				ScheduledDate:   now.Add(-12 * time.Hour),
				Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2"}},
			},
			{
				ExecutionID:     "exec-out-of-range",
				Status:          "approved",
				ScheduledDate:   outOfRangeDay,
				Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2"}},
			},
		}
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return(execs, nil)

		result, err := handler.getHistory(ctx, req, map[string]string{"start": startDay, "end": endDay})
		require.NoError(t, err)
		resp := result.(HistoryResponse)

		require.Len(t, resp.Purchases, 2, "1 DB row + 1 in-range exec")
		for _, p := range resp.Purchases {
			assert.NotEqual(t, "exec-out-of-range", p.PurchaseID, "out-of-range execution must be filtered out")
		}
	})

	t.Run("combined provider+account_ids+date filter is applied on both halves", func(t *testing.T) {
		mockStore, handler, req, ctx := newHandler(t)

		uuidA := "55555555-5555-5555-5555-555555555555"
		mockStore.On("GetPurchaseHistoryFiltered", ctx,
			mock.MatchedBy(func(f config.PurchaseHistoryFilter) bool {
				return f.Provider == "aws" && len(f.AccountIDs) == 1 && f.AccountIDs[0] == uuidA &&
					f.Start != nil && f.End != nil && f.Limit == config.DefaultListLimit
			}),
		).Return([]config.PurchaseHistoryRecord{{AccountID: "match", PurchaseID: "p-match"}}, nil).Once()

		now := time.Now().UTC()
		startDay := now.AddDate(0, 0, -3).Format("2006-01-02")
		endDay := now.Format("2006-01-02")
		// Use "approved" status so expireIfStale short-circuits regardless of
		// ScheduledDate (the date predicate itself is what we're asserting).
		inRange := now.Add(-12 * time.Hour)
		execs := []config.PurchaseExecution{
			{
				ExecutionID:     "exec-match",
				Status:          "approved",
				ScheduledDate:   inRange,
				CloudAccountID:  strPtr(uuidA),
				Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2"}},
			},
			{
				ExecutionID:     "exec-wrong-provider",
				Status:          "approved",
				ScheduledDate:   inRange,
				CloudAccountID:  strPtr(uuidA),
				Recommendations: []config.RecommendationRecord{{Provider: "azure", Service: "vm"}},
			},
			{
				ExecutionID:     "exec-wrong-account",
				Status:          "approved",
				ScheduledDate:   inRange,
				CloudAccountID:  strPtr("66666666-6666-6666-6666-666666666666"),
				Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2"}},
			},
		}
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return(execs, nil)

		result, err := handler.getHistory(ctx, req, map[string]string{
			"provider":    "aws",
			"account_ids": uuidA,
			"start":       startDay,
			"end":         endDay,
		})
		require.NoError(t, err)
		resp := result.(HistoryResponse)

		require.Len(t, resp.Purchases, 2, "1 DB row + only the exec that matches ALL three filters")
		ids := map[string]bool{}
		for _, p := range resp.Purchases {
			ids[p.PurchaseID] = true
		}
		assert.True(t, ids["exec-match"])
		assert.False(t, ids["exec-wrong-provider"])
		assert.False(t, ids["exec-wrong-account"])
	})
}

// TestHandler_getHistory_FilterValidation covers the 400-on-malformed-input
// paths for issue #701: an invalid provider, a non-UUID account_id, a date
// that doesn't parse as YYYY-MM-DD, an inverted range, and a range that
// exceeds MaxHistoryDateRangeDays (the DoS guard mirroring PR #529 / issue
// #414). Each must return a 400 ClientError; none must reach the store.
func TestHandler_getHistory_FilterValidation(t *testing.T) {
	cases := []struct {
		params      map[string]string
		name        string
		wantContain string
		wantCode    int
	}{
		{
			name:        "invalid provider",
			params:      map[string]string{"provider": "unknown"},
			wantCode:    400,
			wantContain: "invalid provider",
		},
		{
			name:        "non-UUID account_ids",
			params:      map[string]string{"account_ids": "not-a-uuid"},
			wantCode:    400,
			wantContain: "invalid account_ids",
		},
		{
			name:        "malformed start date (not YYYY-MM-DD)",
			params:      map[string]string{"start": "01/02/2024"},
			wantCode:    400,
			wantContain: "invalid start date format",
		},
		{
			name:        "malformed end date",
			params:      map[string]string{"end": "2024-13-40"},
			wantCode:    400,
			wantContain: "invalid end date format",
		},
		{
			name:        "inverted range (start after end)",
			params:      map[string]string{"start": "2024-12-31", "end": "2024-01-01"},
			wantCode:    400,
			wantContain: "before or equal to end",
		},
		{
			name:        "range exceeds 366 days",
			params:      map[string]string{"start": "2024-01-01", "end": "2025-12-31"},
			wantCode:    400,
			wantContain: "date range too large",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mockStore := new(MockConfigStore)
			mockAuth, req := adminHistoryReq(ctx)
			handler := &Handler{auth: mockAuth, config: mockStore}

			_, err := handler.getHistory(ctx, req, tc.params)
			require.Error(t, err)
			ce, ok := IsClientError(err)
			require.True(t, ok, "validation failure must surface as a ClientError, got %T: %v", err, err)
			assert.Equal(t, tc.wantCode, ce.code)
			assert.Contains(t, err.Error(), tc.wantContain)

			// Sanity guards: no store calls leaked through on a 400.
			mockStore.AssertNotCalled(t, "GetPurchaseHistory", mock.Anything, mock.Anything, mock.Anything)
			mockStore.AssertNotCalled(t, "GetAllPurchaseHistory", mock.Anything, mock.Anything)
			mockStore.AssertNotCalled(t, "GetPurchaseHistoryFiltered", mock.Anything, mock.Anything)
			mockStore.AssertNotCalled(t, "GetExecutionsByStatuses", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

// TestHandler_getHistory_DateRangeBoundary asserts the inclusive boundaries
// of the 366-day window: a range of exactly 366 days is accepted; 367 is
// rejected. Mirrors the analytics handler's range cap (PR #529).
func TestHandler_getHistory_DateRangeBoundary(t *testing.T) {
	ctx := context.Background()

	t.Run("range of exactly 366 days is accepted", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}
		mockStore.On("GetPurchaseHistoryFiltered", ctx, mock.Anything).
			Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{}, nil)

		// 2024 is a leap year — Jan 1 to Dec 31 inclusive is 366 days.
		_, err := handler.getHistory(ctx, req, map[string]string{"start": "2024-01-01", "end": "2024-12-31"})
		require.NoError(t, err, "366-day range must be accepted (boundary)")
	})

	t.Run("range of 367 days is rejected", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		_, err := handler.getHistory(ctx, req, map[string]string{"start": "2024-01-01", "end": "2025-01-02"})
		require.Error(t, err)
		ce, ok := IsClientError(err)
		require.True(t, ok)
		assert.Equal(t, 400, ce.code)
	})
}

// TestParseHistoryDateRange covers the YYYY-MM-DD parser in isolation: empty
// inputs disable the filter (hasDate=false), one-sided inputs default the
// other bound open, and end is rolled forward to end-of-day so date inputs
// behave inclusively on the user's chosen day.
func TestParseHistoryDateRange(t *testing.T) {
	t.Run("both empty -> no date filter", func(t *testing.T) {
		s, e, has, err := parseHistoryDateRange("", "")
		require.NoError(t, err)
		assert.False(t, has, "empty inputs must yield hasDate=false so the SQL clause is skipped")
		assert.True(t, s.IsZero())
		assert.True(t, e.IsZero())
	})

	t.Run("only end -> start defaults to end - MaxHistoryDateRangeDays", func(t *testing.T) {
		s, e, has, err := parseHistoryDateRange("", "2024-06-15")
		require.NoError(t, err)
		assert.True(t, has)
		assert.Equal(t, "2024-06-15", e.Format("2006-01-02"))
		// The absent lower bound must be MaxHistoryDateRangeDays before end
		// (not epoch) so the open-side default still satisfies the DoS cap.
		expectedStart := e.Add(-MaxHistoryDateRangeDays * 24 * time.Hour)
		assert.Equal(t, expectedStart.Format("2006-01-02"), s.Format("2006-01-02"))
	})

	t.Run("only start -> end defaults to start + MaxHistoryDateRangeDays", func(t *testing.T) {
		s, e, has, err := parseHistoryDateRange("2024-06-15", "")
		require.NoError(t, err)
		assert.True(t, has)
		assert.Equal(t, "2024-06-15", s.Format("2006-01-02"))
		expectedEnd := s.Add(MaxHistoryDateRangeDays * 24 * time.Hour)
		assert.Equal(t, expectedEnd.Format("2006-01-02"), e.Format("2006-01-02"))
	})

	t.Run("end is inclusive of the requested day", func(t *testing.T) {
		_, e, _, err := parseHistoryDateRange("2024-01-01", "2024-01-15")
		require.NoError(t, err)
		// End must be 2024-01-15 23:59:59 UTC so a row stamped at any time on
		// that day is included.
		assert.Equal(t, 2024, e.Year())
		assert.Equal(t, time.January, e.Month())
		assert.Equal(t, 15, e.Day())
		assert.Equal(t, 23, e.Hour())
	})
}

// TestHandler_getHistory_CreatedByUserEmailResolved verifies that when a
// pending execution carries a non-nil CreatedByUserID, the returned history row
// includes the resolved email address in CreatedByUserEmail so the Approval
// Queue can show a name instead of a UUID. A lookup failure must degrade
// gracefully (email field stays empty, row still renders, no panic).
func TestHandler_getHistory_CreatedByUserEmailResolved(t *testing.T) {
	creatorID := "user-uuid-1234"
	creatorEmail := "alice@example.com"
	approverEmail := "ops@example.com"

	t.Run("email resolved from auth service", func(t *testing.T) {
		ctx := context.Background()
		mockStore := new(MockConfigStore)

		exec := config.PurchaseExecution{
			ExecutionID:     "pend-with-creator",
			Status:          "pending",
			ScheduledDate:   time.Now(),
			CreatedByUserID: &creatorID,
			Recommendations: []config.RecommendationRecord{
				{Provider: "aws", Service: "ec2", Region: "us-east-1"},
			},
		}
		mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{exec}, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)

		mockAuth, req := adminHistoryReq(ctx)
		mockAuth.On("GetUser", ctx, creatorID).Return(&User{ID: creatorID, Email: creatorEmail}, nil)
		handler := &Handler{auth: mockAuth, config: mockStore}

		result, err := handler.getHistory(ctx, req, map[string]string{})
		require.NoError(t, err)
		resp := result.(HistoryResponse)

		require.Len(t, resp.Purchases, 1)
		row := resp.Purchases[0]
		assert.Equal(t, creatorID, row.CreatedByUserID, "raw UUID must still be present for cancel-own gate")
		assert.Equal(t, creatorEmail, row.CreatedByUserEmail, "email must be resolved for Approval Queue display")
	})

	t.Run("lookup failure degrades gracefully", func(t *testing.T) {
		ctx := context.Background()
		mockStore := new(MockConfigStore)

		exec := config.PurchaseExecution{
			ExecutionID:     "pend-bad-user",
			Status:          "pending",
			ScheduledDate:   time.Now(),
			CreatedByUserID: &creatorID,
			Recommendations: []config.RecommendationRecord{
				{Provider: "aws", Service: "ec2", Region: "us-east-1"},
			},
		}
		mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{exec}, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)

		mockAuth, req := adminHistoryReq(ctx)
		mockAuth.On("GetUser", ctx, creatorID).Return(nil, errors.New("user not found"))
		handler := &Handler{auth: mockAuth, config: mockStore}

		result, err := handler.getHistory(ctx, req, map[string]string{})
		require.NoError(t, err, "a user-lookup failure must not abort the history response")
		resp := result.(HistoryResponse)

		require.Len(t, resp.Purchases, 1, "row must still render when email lookup fails")
		row := resp.Purchases[0]
		assert.Equal(t, creatorID, row.CreatedByUserID)
		assert.Empty(t, row.CreatedByUserEmail, "email must be empty when lookup fails — UI falls back to UUID")
	})
}

// TestHandler_getHistory_ApprovalQueueColumnsPopulated is the issue #733
// regression guard. PR #713 added the Approval queue's Account, Term, Payment,
// and Monthly Cost columns to the frontend; the backend handler was missing the
// data plumbing for Account (web-initiated pending executions never set
// exec.CloudAccountID — the rec carries it), Payment (never copied off the rec
// in projectRecommendationFields), and multi-rec MonthlyCost (only the single-
// rec branch was mapped). Without these, every Approval queue cell rendered as
// the "-" fallback. Three sub-tests pin the contract:
//   - single-rec: Payment is copied from the rec; Account falls back to the
//     rec's CloudAccountID when exec.CloudAccountID is nil.
//   - multi-rec uniform: Payment + Account collapse to the shared value;
//     MonthlyCost sums across recs.
//   - multi-rec mixed Payment: Payment collapses to "" (the frontend's "-"
//     fallback) rather than silently picking a single value for a basket
//     that genuinely mixes payment options.
func TestHandler_getHistory_ApprovalQueueColumnsPopulated(t *testing.T) {
	approverEmail := "ops@example.com"

	t.Run("single-rec pending row carries Account + Payment + MonthlyCost", func(t *testing.T) {
		ctx := context.Background()
		mockStore := new(MockConfigStore)
		monthly := 7.5
		accountID := "123456789012"
		pending := []config.PurchaseExecution{
			{
				ExecutionID:   "pend-single",
				Status:        "pending",
				ScheduledDate: time.Now(),
				// exec.CloudAccountID intentionally nil — matches the
				// real-world web bulk-purchase flow which only populates
				// the per-rec CloudAccountID.
				Recommendations: []config.RecommendationRecord{
					{
						Provider:       "aws",
						Service:        "ec2",
						Region:         "us-east-1",
						ResourceType:   "t4g.nano",
						Term:           1,
						Payment:        "all-upfront",
						Count:          1,
						UpfrontCost:    100.0,
						MonthlyCost:    &monthly,
						Savings:        2.5,
						CloudAccountID: &accountID,
					},
				},
			},
		}
		mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return(pending, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)

		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		result, err := handler.getHistory(ctx, req, map[string]string{})
		require.NoError(t, err)
		resp := result.(HistoryResponse)
		require.Len(t, resp.Purchases, 1)
		row := resp.Purchases[0]

		assert.Equal(t, accountID, row.AccountID, "Account must fall back to rec.CloudAccountID when exec.CloudAccountID is nil (#733)")
		assert.Equal(t, "all-upfront", row.Payment, "Payment must be copied from the single rec (#733)")
		require.NotNil(t, row.MonthlyCost, "MonthlyCost must come from the rec")
		assert.InDelta(t, 7.5, *row.MonthlyCost, 1e-9, "MonthlyCost must come from the rec")
	})

	t.Run("multi-rec uniform pending row collapses Account + Payment, sums MonthlyCost", func(t *testing.T) {
		ctx := context.Background()
		mockStore := new(MockConfigStore)
		monthlyA := 3.0
		monthlyB := 4.5
		accountID := "987654321098"
		pending := []config.PurchaseExecution{
			{
				ExecutionID:      "pend-multi-uniform",
				Status:           "pending",
				ScheduledDate:    time.Now(),
				TotalUpfrontCost: 250.0,
				EstimatedSavings: 12.0,
				Recommendations: []config.RecommendationRecord{
					{Provider: "aws", Service: "ec2", Region: "us-east-1", Payment: "no-upfront", MonthlyCost: &monthlyA, CloudAccountID: &accountID},
					{Provider: "aws", Service: "ec2", Region: "us-east-1", Payment: "no-upfront", MonthlyCost: &monthlyB, CloudAccountID: &accountID},
				},
			},
		}
		mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return(pending, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)

		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		result, err := handler.getHistory(ctx, req, map[string]string{})
		require.NoError(t, err)
		resp := result.(HistoryResponse)
		require.Len(t, resp.Purchases, 1)
		row := resp.Purchases[0]

		assert.Equal(t, accountID, row.AccountID, "Account must collapse to the shared rec value (#733)")
		assert.Equal(t, "no-upfront", row.Payment, "Payment must collapse to the shared rec value (#733)")
		require.NotNil(t, row.MonthlyCost, "MonthlyCost must sum across recs (#733)")
		assert.InDelta(t, 7.5, *row.MonthlyCost, 1e-9, "MonthlyCost must sum across recs (#733)")
	})

	t.Run("multi-rec heterogeneous Payment collapses to empty for honest dash fallback", func(t *testing.T) {
		ctx := context.Background()
		mockStore := new(MockConfigStore)
		pending := []config.PurchaseExecution{
			{
				ExecutionID:   "pend-multi-mixed",
				Status:        "pending",
				ScheduledDate: time.Now(),
				Recommendations: []config.RecommendationRecord{
					{Provider: "aws", Service: "ec2", Region: "us-east-1", Payment: "all-upfront"},
					{Provider: "aws", Service: "ec2", Region: "us-east-1", Payment: "no-upfront"},
				},
			},
		}
		mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return(pending, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)

		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		result, err := handler.getHistory(ctx, req, map[string]string{})
		require.NoError(t, err)
		resp := result.(HistoryResponse)
		require.Len(t, resp.Purchases, 1)
		row := resp.Purchases[0]

		assert.Empty(t, row.Payment, "Payment must collapse to empty when recs disagree - the dash fallback is more honest than a single arbitrary value (#733)")
	})

	t.Run("account_ids filter matches pending row via rec-level fallback when exec.CloudAccountID is nil", func(t *testing.T) {
		// Regression: matchesExecution previously rejected executions with a nil
		// exec.CloudAccountID even when the rec carried the matching UUID. Web
		// bulk-purchase flows only populate the per-rec CloudAccountID, so an
		// account-filtered approval queue would silently drop all web-initiated
		// pending rows. The fix makes matchesExecution apply the same two-level
		// fallback as executionToHistoryRow (issue #704, CR #738).
		ctx := context.Background()
		mockStore := new(MockConfigStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		recAccountID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		monthly := 9.0
		pending := []config.PurchaseExecution{
			{
				ExecutionID:   "pend-nil-exec-account",
				Status:        "pending",
				ScheduledDate: time.Now(),
				// exec.CloudAccountID intentionally nil - mirrors web bulk-purchase
				// flow. The rec carries the canonical account UUID.
				Recommendations: []config.RecommendationRecord{
					{
						Provider:       "aws",
						Service:        "ec2",
						Region:         "us-east-1",
						ResourceType:   "m5.large",
						Term:           1,
						Payment:        "no-upfront",
						Count:          1,
						UpfrontCost:    0.0,
						MonthlyCost:    &monthly,
						CloudAccountID: &recAccountID,
					},
				},
			},
		}
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return(pending, nil)
		// account_ids is set, so fetchPurchaseHistory routes to GetPurchaseHistoryFiltered
		// (not GetAllPurchaseHistory). No DB rows needed; we only care about the
		// execution path.
		mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{AccountIDs: []string{recAccountID}, Limit: 100}).
			Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)

		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		result, err := handler.getHistory(ctx, req, map[string]string{"account_ids": recAccountID})
		require.NoError(t, err)
		resp := result.(HistoryResponse)
		require.Len(t, resp.Purchases, 1, "pending row must survive the account_ids filter via rec-level CloudAccountID fallback")
		row := resp.Purchases[0]

		assert.Equal(t, recAccountID, row.AccountID, "AccountID must be resolved from the rec when exec.CloudAccountID is nil (#704)")
	})
}

// TestHandler_getHistory_ExternalIDOnlyAccount is the end-to-end keystone
// regression test for issue #701/#498/#866: an account whose purchase_history
// rows were written by a direct-execute/ambient/legacy path carry only the
// external account_id (cloud_account_id IS NULL). When the user selects that
// account by its cloud_accounts UUID (the top-bar chip value), the handler
// must resolve the UUID to its external id and pass BOTH to the store so the
// dual-column predicate matches the external-id-only rows. Before the fix the
// handler filtered cloud_account_id only and the user saw "No purchase history
// yet" for the account.
func TestHandler_getHistory_ExternalIDOnlyAccount(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	accountBUUID := "bbbbbbbb-1111-2222-3333-444444444444"
	accountBExternal := "999988887777"

	// cloud_accounts knows account B's external id; the resolver maps the
	// requested UUID to it.
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return []config.CloudAccount{
			{ID: accountBUUID, Name: "Account B", Provider: "aws", ExternalID: accountBExternal},
		}, nil
	}

	// The store returns account B's row only when the dual-column filter carries
	// BOTH the UUID and the resolved external id. The row itself has the external
	// AccountID and (implicitly) a NULL cloud_account_id.
	bRow := []config.PurchaseHistoryRecord{
		{AccountID: accountBExternal, PurchaseID: "p-B", Provider: "aws", Service: "ec2", UpfrontCost: 100.0, EstimatedSavings: 25.0},
	}
	mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{
		AccountIDs:            []string{accountBUUID},
		ExternalIDsByProvider: map[string][]string{"aws": {accountBExternal}},
		Limit:                 config.DefaultListLimit,
	}).Return(bRow, nil).Once()
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{}, nil)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.getHistory(ctx, req, map[string]string{"account_ids": accountBUUID})
	require.NoError(t, err)
	resp := result.(HistoryResponse)

	require.Len(t, resp.Purchases, 1, "external-id-only row must surface when its account UUID is requested")
	assert.Equal(t, accountBExternal, resp.Purchases[0].AccountID)
}

// TestMatchesExecution_ExternalIDOnlyPending asserts the in-memory mirror of
// the dual-column filter: a pending execution attributed only by an external
// account number (no UUID anywhere) must survive when the request resolves to
// that external id, so external-id-only pending rows aren't dropped from the
// approval queue (issue #701/#498/#866).
func TestMatchesExecution_ExternalIDOnlyPending(t *testing.T) {
	external := "999988887777"
	uuid := "bbbbbbbb-1111-2222-3333-444444444444"

	// Execution carries the external number as its effective account id.
	exec := config.PurchaseExecution{
		ExecutionID:     "pend-external",
		Status:          "pending",
		ScheduledDate:   time.Now(),
		CloudAccountID:  strPtr(external),
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2"}},
	}

	// Filtering by the UUID alone drops it (no UUID match)...
	uuidOnly := historyFilters{AccountIDs: []string{uuid}}
	assert.False(t, uuidOnly.matchesExecution(exec), "UUID-only filter must not match an external-id-only execution")

	// ...but once the external id is resolved into the filter it is retained.
	dual := historyFilters{AccountIDs: []string{uuid}, ExternalIDsByProvider: map[string][]string{"aws": {external}}}
	assert.True(t, dual.matchesExecution(exec), "external-id-only pending execution must survive when its external id is in the filter")
}

// TestHandler_getHistory_CompletedExecutionNotDuplicated guards the dedup path.
// The store loads "completed" executions now (so audit-gap rows can surface),
// but a NORMAL completed execution (Error=="") is already represented by its
// purchase_history rows and must NOT be synthesized a second time. The History
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
	// synthesized) because they are assumed already represented by their
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

// TestSummarizePurchaseHistory_CancelledExcludedFromKPIs is the regression
// test for issues #625 and #736. Canceling a pending purchase must not add its upfront
// cost or savings to the KPI totals. Specifically:
//   - TotalUpfront, TotalMonthlySavings, TotalAnnualSavings must reflect only
//     the approved/completed rows.
//   - TotalCompleted must not include canceled rows.
//   - A pre-existing canceled row in the dataset must also be excluded.
func TestSummarizePurchaseHistory_CancelledExcludedFromKPIs(t *testing.T) {
	purchases := []config.PurchaseHistoryRecord{
		// Three completed rows that should contribute to the KPI totals.
		{Status: "completed", UpfrontCost: 100.0, EstimatedSavings: 10.0},
		{Status: "completed", UpfrontCost: 200.0, EstimatedSavings: 20.0},
		{Status: "", UpfrontCost: 50.0, EstimatedSavings: 5.0}, // legacy row, no status
		// One pending row that should be counted as pending, not completed.
		{Status: "pending", UpfrontCost: 999.0, EstimatedSavings: 99.0},
		// Two canceled rows — the regression case from issue #736. Neither must
		// appear in the dollar KPIs or TotalCompleted. One uses the new US
		// spelling (config.StatusCanceled) and one the legacy British spelling
		// (config.LegacyStatusCanceled): during the expand-contract rename
		// (migration 000089) a mixed fleet still emits the legacy spelling, so
		// the dual-spelling read path must exclude BOTH. The constant carries
		// the legacy value without a literal the US-locale misspell linter would
		// flag (and without a nolint). Drop the legacy fixture once the contract
		// migration (#1278) normalizes the data.
		{Status: config.StatusCanceled, UpfrontCost: 500.0, EstimatedSavings: 50.0},
		{Status: config.LegacyStatusCanceled, UpfrontCost: 750.0, EstimatedSavings: 75.0},
	}

	summary := summarizePurchaseHistory(purchases)

	assert.Equal(t, 6, summary.TotalPurchases, "all rows count toward TotalPurchases")
	assert.Equal(t, 3, summary.TotalCompleted, "canceled rows must not inflate TotalCompleted")
	assert.Equal(t, 1, summary.TotalPending)

	assert.InDelta(t, 350.0, summary.TotalUpfront, 0.001,
		"canceled upfront cost must not be included in TotalUpfront (issues #625, #736)")
	assert.InDelta(t, 35.0, summary.TotalMonthlySavings, 0.001,
		"canceled savings must not be included in TotalMonthlySavings (issues #625, #736)")
	assert.InDelta(t, 420.0, summary.TotalAnnualSavings, 0.001,
		"TotalAnnualSavings = TotalMonthlySavings * 12 and must exclude canceled (issues #625, #736)")
}

// TestSummarizePurchaseHistory_CancelPendingDoesNotChangeKPIs mirrors the
// QA reproduction scenario from issues #625 and #736: start with N approved
// purchases, observe KPI totals, then add a canceled execution and assert the
// totals are unchanged.
// TestHandler_getHistory_LimitParsing is the 01-M1 regression guard.
// Prior to the fix, parseHistoryFilters used fmt.Sscanf to parse the limit
// query param, which silently swallows non-integer input (callers get the
// default with no feedback) and never returns a 400. strconv.Atoi now
// rejects non-integer values, and limit < 1 is clamped to default rather
// than being treated as an override (issue #1061).
func TestHandler_getHistory_LimitParsing(t *testing.T) {
	cases := []struct {
		name       string
		limitParam string
		wantCode   int
		wantLimit  int // only checked on success paths (wantCode == 0)
	}{
		{
			name:       "non-integer limit returns 400",
			limitParam: "abc",
			wantCode:   400,
		},
		{
			name:       "float string limit returns 400",
			limitParam: "1.5",
			wantCode:   400,
		},
		{
			name:       "negative limit clamps to default",
			limitParam: "-1",
			wantCode:   0,
			wantLimit:  config.DefaultListLimit,
		},
		{
			name:       "zero limit clamps to default",
			limitParam: "0",
			wantCode:   0,
			wantLimit:  config.DefaultListLimit,
		},
		{
			name:       "over-max limit clamps to MaxListLimit",
			limitParam: "9999",
			wantCode:   0,
			wantLimit:  config.MaxListLimit,
		},
		{
			name:       "valid limit accepted",
			limitParam: "50",
			wantCode:   0,
			wantLimit:  50,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mockStore := new(MockConfigStore)
			mockAuth, req := adminHistoryReq(ctx)
			handler := &Handler{auth: mockAuth, config: mockStore}

			if tc.wantCode != 0 {
				// Error path: expect a ClientError with wantCode; no store calls.
				_, err := handler.getHistory(ctx, req, map[string]string{"limit": tc.limitParam})
				require.Error(t, err, "non-integer limit must return an error")
				ce, ok := IsClientError(err)
				require.True(t, ok, "error must be a ClientError, got %T: %v", err, err)
				assert.Equal(t, tc.wantCode, ce.code)
				mockStore.AssertNotCalled(t, "GetAllPurchaseHistory", mock.Anything, mock.Anything)
				mockStore.AssertNotCalled(t, "GetPurchaseHistoryFiltered", mock.Anything, mock.Anything)
			} else {
				// Success path: store receives the clamped limit.
				mockStore.On("GetAllPurchaseHistory", ctx, tc.wantLimit).
					Return([]config.PurchaseHistoryRecord{}, nil)
				mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).
					Return([]config.PurchaseExecution{}, nil)

				_, err := handler.getHistory(ctx, req, map[string]string{"limit": tc.limitParam})
				require.NoError(t, err)
				mockStore.AssertCalled(t, "GetAllPurchaseHistory", ctx, tc.wantLimit)
			}
		})
	}
}
func TestSummarizePurchaseHistory_CancelPendingDoesNotChangeKPIs(t *testing.T) {
	// Baseline: three approved (completed) rows.
	baseline := make([]config.PurchaseHistoryRecord, 0, 4)
	baseline = append(baseline,
		config.PurchaseHistoryRecord{Status: "completed", UpfrontCost: 100.0, EstimatedSavings: 10.0},
		config.PurchaseHistoryRecord{Status: "completed", UpfrontCost: 200.0, EstimatedSavings: 20.0},
		config.PurchaseHistoryRecord{Status: "completed", UpfrontCost: 300.0, EstimatedSavings: 30.0},
	)
	before := summarizePurchaseHistory(baseline)

	// After: same rows plus one canceled execution (the pending that got canceled).
	withCancelled := append(baseline, config.PurchaseHistoryRecord{ //nolint:gocritic
		Status:           "cancelled", //nolint:misspell // DB schema value 'cancelled' -- see migration 000001_initial_schema.up.sql
		UpfrontCost:      999.0,
		EstimatedSavings: 99.0,
	})
	after := summarizePurchaseHistory(withCancelled)

	assert.Equal(t, before.TotalUpfront, after.TotalUpfront,
		"canceling a pending purchase must not change TotalUpfront (issues #625, #736)")
	assert.Equal(t, before.TotalMonthlySavings, after.TotalMonthlySavings,
		"canceling a pending purchase must not change TotalMonthlySavings (issues #625, #736)")
	assert.Equal(t, before.TotalAnnualSavings, after.TotalAnnualSavings,
		"canceling a pending purchase must not change TotalAnnualSavings (issues #625, #736)")
	assert.Equal(t, before.TotalCompleted, after.TotalCompleted,
		"canceling a pending purchase must not change TotalCompleted (issues #625, #736)")
}

// TestHandler_getHistory_ExpireIfStale_LambdaGuard covers the Lambda guard on
// the stale-approval expiry sweep (issue #1170, COR-06). On Lambda the
// execution environment freezes as soon as the response is returned, so the
// sweep must complete synchronously before getHistory returns; on long-running
// servers it must stay asynchronous so the read response is never blocked on
// the transitions. Detection reuses runtime.IsLambda (AWS_LAMBDA_RUNTIME_API),
// the same helper the SWR cache goroutine gate uses, so the sub-tests flip
// that env var via t.Setenv.
func TestHandler_getHistory_ExpireIfStale_LambdaGuard(t *testing.T) {
	approverEmail := "ops@example.com"
	staleID := "stale-exec-lambda-guard"
	staleExec := func() config.PurchaseExecution {
		return config.PurchaseExecution{
			ExecutionID:      staleID,
			Status:           "pending",
			ScheduledDate:    time.Now().Add(-8 * 24 * time.Hour),
			TotalUpfrontCost: 200.0,
			EstimatedSavings: 20.0,
			Recommendations: []config.RecommendationRecord{
				{Provider: "aws", Service: "rds", Region: "us-east-1"},
			},
		}
	}

	t.Run("Lambda: sweep completes synchronously before the handler returns", func(t *testing.T) {
		// Any non-empty value marks the process as running on Lambda
		// (runtime.IsLambda checks presence, not content).
		t.Setenv("AWS_LAMBDA_RUNTIME_API", "127.0.0.1:9001")

		ctx := context.Background()
		mockStore := new(MockConfigStore)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })
		expired := staleExec()
		expired.Status = "expired"

		mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).
			Return([]config.PurchaseExecution{staleExec()}, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)
		mockStore.On("TransitionExecutionStatus", mock.Anything, staleID, []string{"pending", "notified"}, "expired",
			(*string)(nil),
		).Return(&expired, nil).Once()

		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		result, err := handler.getHistory(ctx, req, map[string]string{})
		require.NoError(t, err)

		// No channel wait: on Lambda the transition must already have fired
		// by the time getHistory returns. Pre-fix this fails because the
		// sweep ran in a goroutine the frozen sandbox never resumes.
		mockStore.AssertNumberOfCalls(t, "TransitionExecutionStatus", 1)

		historyResp := result.(HistoryResponse)
		require.Len(t, historyResp.Purchases, 1)
		assert.Equal(t, "pending", historyResp.Purchases[0].Status,
			"GET stays a pure read on Lambda too: response carries the pre-transition status")
	})

	t.Run("non-Lambda: sweep stays asynchronous and never blocks the response", func(t *testing.T) {
		// Explicitly clear the marker so the test is deterministic even if
		// the outer environment sets it; t.Setenv restores it afterwards.
		t.Setenv("AWS_LAMBDA_RUNTIME_API", "")

		ctx := context.Background()
		mockStore := new(MockConfigStore)
		expired := staleExec()
		expired.Status = "expired"

		// The transition blocks until released. If the sweep ran
		// synchronously off-Lambda (a regression of issue #1032's pure-read
		// guarantee), getHistory would block on it and the watchdog below
		// would fire.
		release := make(chan struct{})
		swept := make(chan struct{})
		mockStore.On("GetAllPurchaseHistory", ctx, 100).Return([]config.PurchaseHistoryRecord{}, nil)
		mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).
			Return([]config.PurchaseExecution{staleExec()}, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)
		mockStore.On("TransitionExecutionStatus", mock.Anything, staleID, []string{"pending", "notified"}, "expired",
			(*string)(nil),
		).Run(func(_ mock.Arguments) {
			<-release
			close(swept)
		}).Return(&expired, nil).Once()

		mockAuth, req := adminHistoryReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		done := make(chan struct{})
		var result any
		var err error
		go func() {
			result, err = handler.getHistory(ctx, req, map[string]string{})
			close(done)
		}()

		select {
		case <-done:
			// getHistory returned while the transition was still blocked:
			// the sweep is asynchronous, as required off-Lambda.
		case <-time.After(5 * time.Second):
			t.Fatal("getHistory blocked on the expiry sweep off-Lambda - sweep must stay asynchronous")
		}

		close(release)
		select {
		case <-swept:
		case <-time.After(5 * time.Second):
			t.Fatal("background expiry sweep did not complete within 5s")
		}

		require.NoError(t, err)
		mockStore.AssertNumberOfCalls(t, "TransitionExecutionStatus", 1)
		mockStore.AssertExpectations(t)
		historyResp := result.(HistoryResponse)
		require.Len(t, historyResp.Purchases, 1)
		assert.Equal(t, "pending", historyResp.Purchases[0].Status)
	})
}

// TestSummarizePurchaseHistory_RevokedExcludedFromKPIs is the defect-#2
// regression guard. A revoked commitment (RevokedAt != nil) must count toward
// TotalPurchases and TotalRevoked, but must be excluded from TotalCompleted and
// all dollar KPIs. Pre-fix, summarizePurchaseHistory had no revoked case, so
// revoked rows fell through to the completed-dollar-total path and inflated
// TotalUpfront and TotalMonthlySavings.
func TestSummarizePurchaseHistory_RevokedExcludedFromKPIs(t *testing.T) {
	now := time.Now()
	revokedAt := now.AddDate(0, -1, 0) // revoked 1 month ago

	purchases := []config.PurchaseHistoryRecord{
		// Completed, non-revoked: must contribute to dollar totals.
		{Status: "completed", UpfrontCost: 100.0, EstimatedSavings: 10.0},
		// Revoked: term still open but revoked_at is set.
		// Pre-fix: fell through to the completed branch, inflating TotalUpfront
		// by 500 and TotalMonthlySavings by 50.
		{Status: "completed", UpfrontCost: 500.0, EstimatedSavings: 50.0, RevokedAt: &revokedAt},
		// Revoked with empty status (legacy DB row): same guard must apply.
		{Status: "", UpfrontCost: 750.0, EstimatedSavings: 75.0, RevokedAt: &revokedAt},
	}

	summary := summarizePurchaseHistory(purchases)

	assert.Equal(t, 3, summary.TotalPurchases, "all rows count toward TotalPurchases")
	assert.Equal(t, 2, summary.TotalRevoked,
		"revoked rows must be counted in TotalRevoked")
	assert.Equal(t, 1, summary.TotalCompleted,
		"revoked rows must not inflate TotalCompleted")

	assert.InDelta(t, 100.0, summary.TotalUpfront, 0.001,
		"revoked upfront cost must not appear in TotalUpfront")
	assert.InDelta(t, 10.0, summary.TotalMonthlySavings, 0.001,
		"revoked savings must not appear in TotalMonthlySavings")
	assert.InDelta(t, 120.0, summary.TotalAnnualSavings, 0.001,
		"TotalAnnualSavings must exclude revoked rows")
}
