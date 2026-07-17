package purchase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newReaperManager builds a Manager wired with just enough mocks for the
// reaper sweep. Other deps (email, providers) are unused by ReapStuck
// and intentionally left nil — the reaper is local-status-only by design.
func newReaperManager(store *MockConfigStore) *Manager {
	return &Manager{
		config: store,
	}
}

// stuckExec builds a representative stuck execution with no recommendations.
// allRecsSafeToRedrive returns false for empty recs, so no "safe to retry"
// is appended to the reaper's canonical error message.
func stuckExec(id, status string) config.PurchaseExecution {
	return config.PurchaseExecution{
		PlanID:        "plan-1",
		ExecutionID:   id,
		Status:        status,
		ApprovalToken: "tok-" + id,
		ScheduledDate: time.Now().Add(-1 * time.Hour),
	}
}

// stuckExecWithAWSRecs builds a stuck execution with a single AWS EC2 rec.
// allRecsSafeToRedrive returns true for pure-AWS recs, so the reaper's
// canonical error includes "safe to retry" (F4 fix).
func stuckExecWithAWSRecs(id, status string) config.PurchaseExecution {
	e := stuckExec(id, status)
	e.Recommendations = []config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Count: 1},
	}
	return e
}

func TestReapStuckExecutions_StaleApprovedFlippedToFailed(t *testing.T) {
	ctx := context.Background()
	store := new(MockConfigStore)
	reapAfter := 10 * time.Minute

	// AWS recs are idempotent (ClientToken / tag-guard), so "safe to retry"
	// must appear in the canonical error for operator guidance (F4 fix).
	row := stuckExecWithAWSRecs("exec-A", "approved")
	transitioned := row
	transitioned.Status = failedStatus

	store.On("ListStuckExecutions", ctx, stuckStatuses, reapAfter).
		Return([]config.PurchaseExecution{row}, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-A", stuckStatuses, failedStatus, (*string)(nil)).
		Return(&transitioned, nil)
	store.On("SavePurchaseExecution", ctx, mock.MatchedBy(func(e *config.PurchaseExecution) bool {
		// Canonical error message + previous-status attribution.
		return e.ExecutionID == "exec-A" &&
			e.Status == failedStatus &&
			strings.Contains(e.Error, "reaped after") &&
			strings.Contains(e.Error, "approved") &&
			strings.Contains(e.Error, "safe to retry")
	})).Return(nil)

	mgr := newReaperManager(store)
	result, err := mgr.ReapStuckExecutions(ctx, reapAfter)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Found)
	assert.Equal(t, 1, result.Reaped)
	assert.Equal(t, 0, result.RaceLost)
	assert.Equal(t, 0, result.Errored)
	store.AssertExpectations(t)
}

func TestReapStuckExecutions_StaleRunningFlippedToFailed(t *testing.T) {
	ctx := context.Background()
	store := new(MockConfigStore)
	reapAfter := 10 * time.Minute

	row := stuckExec("exec-B", "running")
	transitioned := row
	transitioned.Status = failedStatus

	store.On("ListStuckExecutions", ctx, stuckStatuses, reapAfter).
		Return([]config.PurchaseExecution{row}, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-B", stuckStatuses, failedStatus, (*string)(nil)).
		Return(&transitioned, nil)
	store.On("SavePurchaseExecution", ctx, mock.MatchedBy(func(e *config.PurchaseExecution) bool {
		// "running" attribution path
		return e.ExecutionID == "exec-B" &&
			e.Status == failedStatus &&
			strings.Contains(e.Error, "running")
	})).Return(nil)

	mgr := newReaperManager(store)
	result, err := mgr.ReapStuckExecutions(ctx, reapAfter)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Reaped)
	store.AssertExpectations(t)
}

func TestReapStuckExecutions_YoungerThanThresholdNotTouched(t *testing.T) {
	// The reaper's age filtering is enforced by the SELECT in
	// ListStuckExecutions — so "younger than reapAfter" means "the store
	// returns an empty slice". The reaper must then return early with
	// Found=0 and never call TransitionExecutionStatus or
	// SavePurchaseExecution.
	ctx := context.Background()
	store := new(MockConfigStore)
	reapAfter := 10 * time.Minute

	store.On("ListStuckExecutions", ctx, stuckStatuses, reapAfter).
		Return([]config.PurchaseExecution{}, nil)
	// No TransitionExecutionStatus / SavePurchaseExecution expectations
	// — mock.AssertExpectations will fail if either is called.

	mgr := newReaperManager(store)
	result, err := mgr.ReapStuckExecutions(ctx, reapAfter)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Found)
	assert.Equal(t, 0, result.Reaped)
	store.AssertExpectations(t)
	store.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	store.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

func TestReapStuckExecutions_TerminalStatusNotTouched(t *testing.T) {
	// The store filters out terminal statuses via the WHERE clause; the
	// reaper never sees completed/failed/canceled rows. Verify the
	// reaper passes only stuckStatuses (approved/running) to the store
	// — never the terminal set. This regression-guards a future change
	// that accidentally widens stuckStatuses.
	ctx := context.Background()
	store := new(MockConfigStore)
	reapAfter := 10 * time.Minute

	store.On("ListStuckExecutions", ctx, mock.MatchedBy(func(statuses []string) bool {
		// Must contain "approved" and "running" and NOT contain any
		// terminal status.
		seen := map[string]bool{}
		for _, s := range statuses {
			seen[s] = true
		}
		return seen["approved"] && seen["running"] &&
			!seen["completed"] && !seen["failed"] && !seen["canceled"] && !seen["pending"] && !seen["notified"]
	}), reapAfter).
		Return([]config.PurchaseExecution{}, nil)

	mgr := newReaperManager(store)
	_, err := mgr.ReapStuckExecutions(ctx, reapAfter)
	require.NoError(t, err)
	store.AssertExpectations(t)
}

func TestReapStuckExecutions_CASRaceLostNoError(t *testing.T) {
	// CAS race: the SELECT returns a stuck row, but between SELECT and
	// CAS, the real executor flips the row to completed. The store wraps
	// the rejection in ErrExecutionNotInExpectedStatus so the reaper can
	// use errors.Is to recognize the race outcome and move on without
	// erroring the sweep — this regression-guards the A1 CR finding
	// (must not classify all CAS errors as race-lost).
	ctx := context.Background()
	store := new(MockConfigStore)
	reapAfter := 10 * time.Minute

	row := stuckExec("exec-race", "approved")
	store.On("ListStuckExecutions", ctx, stuckStatuses, reapAfter).
		Return([]config.PurchaseExecution{row}, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-race", stuckStatuses, failedStatus, (*string)(nil)).
		Return(nil, fmt.Errorf("%w: execution exec-race cannot transition from %q to %q",
			config.ErrExecutionNotInExpectedStatus, "completed", "failed"))
	// No SavePurchaseExecution expectation — we lost the race, the real
	// executor's status flip stands.

	mgr := newReaperManager(store)
	result, err := mgr.ReapStuckExecutions(ctx, reapAfter)
	require.NoError(t, err) // sweep itself succeeds even on per-row race
	assert.Equal(t, 1, result.Found)
	assert.Equal(t, 0, result.Reaped)
	assert.Equal(t, 1, result.RaceLost)
	assert.Equal(t, 0, result.Errored)
	store.AssertExpectations(t)
	store.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

func TestReapStuckExecutions_RowVanishedTreatedAsRaceLost(t *testing.T) {
	// Defensive: between SELECT and CAS the row might be deleted (e.g.
	// manual DBA intervention). The store wraps that case in
	// config.ErrNotFound; the reaper must treat it as a race-lost (the
	// row is no longer in approved/running, nothing for the reaper to do)
	// rather than as a real DB error. Regression-guards the second half
	// of the A1 CR finding's sentinel handling.
	ctx := context.Background()
	store := new(MockConfigStore)
	reapAfter := 10 * time.Minute

	row := stuckExec("exec-gone", "approved")
	store.On("ListStuckExecutions", ctx, stuckStatuses, reapAfter).
		Return([]config.PurchaseExecution{row}, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-gone", stuckStatuses, failedStatus, (*string)(nil)).
		Return(nil, fmt.Errorf("%w: execution exec-gone", config.ErrNotFound))

	mgr := newReaperManager(store)
	result, err := mgr.ReapStuckExecutions(ctx, reapAfter)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Found)
	assert.Equal(t, 0, result.Reaped)
	assert.Equal(t, 1, result.RaceLost)
	assert.Equal(t, 0, result.Errored)
	store.AssertExpectations(t)
	store.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

func TestReapStuckExecutions_HardDBErrorClassifiedAsErrored(t *testing.T) {
	// The A1 fix's whole point: a non-sentinel error from
	// TransitionExecutionStatus (DB connection dropped, query syntax
	// error, etc.) is NOT a race-loss — it must bump Errored so the ops
	// signal is visible. Before A1, this was silently absorbed as
	// RaceLost; this test is the regression guard.
	ctx := context.Background()
	store := new(MockConfigStore)
	reapAfter := 10 * time.Minute

	row := stuckExec("exec-dbflake", "running")
	store.On("ListStuckExecutions", ctx, stuckStatuses, reapAfter).
		Return([]config.PurchaseExecution{row}, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-dbflake", stuckStatuses, failedStatus, (*string)(nil)).
		Return(nil, errors.New("connection refused"))

	mgr := newReaperManager(store)
	result, err := mgr.ReapStuckExecutions(ctx, reapAfter)
	require.NoError(t, err) // sweep itself still succeeds; per-row errors don't propagate
	assert.Equal(t, 1, result.Found)
	assert.Equal(t, 0, result.Reaped)
	assert.Equal(t, 0, result.RaceLost, "real DB errors must NOT be classified as race-lost")
	assert.Equal(t, 1, result.Errored, "real DB errors must bump Errored so ops can see the outage")
	store.AssertExpectations(t)
	store.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

func TestReapStuckExecutions_CASReturnsNilNilTreatedAsRaceLost(t *testing.T) {
	// Defensive coverage: if the store contract is violated and returns
	// (nil, nil), the reaper treats it as a race-lost rather than NPEing
	// on the follow-up save.
	ctx := context.Background()
	store := new(MockConfigStore)
	reapAfter := 10 * time.Minute

	row := stuckExec("exec-nilnil", "running")
	store.On("ListStuckExecutions", ctx, stuckStatuses, reapAfter).
		Return([]config.PurchaseExecution{row}, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-nilnil", stuckStatuses, failedStatus, (*string)(nil)).
		Return(nil, nil)

	mgr := newReaperManager(store)
	result, err := mgr.ReapStuckExecutions(ctx, reapAfter)
	require.NoError(t, err)
	assert.Equal(t, 1, result.RaceLost)
	assert.Equal(t, 0, result.Reaped)
	store.AssertExpectations(t)
}

func TestReapStuckExecutions_ThreeStuckRowsAllReaped(t *testing.T) {
	// Integration-style: 3 stuck rows → 3 CAS calls → 3 SavePurchaseExecution
	// writes with the correct execution_id passed through each path.
	ctx := context.Background()
	store := new(MockConfigStore)
	reapAfter := 15 * time.Minute

	rows := []config.PurchaseExecution{
		stuckExec("exec-1", "approved"),
		stuckExec("exec-2", "running"),
		stuckExec("exec-3", "approved"),
	}
	store.On("ListStuckExecutions", ctx, stuckStatuses, reapAfter).Return(rows, nil)

	for _, r := range rows {
		flipped := r
		flipped.Status = failedStatus
		store.On("TransitionExecutionStatus", ctx, r.ExecutionID, stuckStatuses, failedStatus, (*string)(nil)).
			Return(&flipped, nil).Once()
		store.On("SavePurchaseExecution", ctx, mock.MatchedBy(func(e *config.PurchaseExecution) bool {
			return e.ExecutionID == r.ExecutionID && e.Status == failedStatus
		})).Return(nil).Once()
	}

	mgr := newReaperManager(store)
	result, err := mgr.ReapStuckExecutions(ctx, reapAfter)
	require.NoError(t, err)
	assert.Equal(t, 3, result.Found)
	assert.Equal(t, 3, result.Reaped)
	store.AssertExpectations(t)
}

func TestReapStuckExecutions_ListStoreError(t *testing.T) {
	// A store-level failure on the SELECT propagates as a sweep-level
	// error so the caller knows to log + alert. Per-row errors do NOT
	// propagate (see CASRaceLost test) — this is the wholesale-failure
	// path.
	ctx := context.Background()
	store := new(MockConfigStore)
	reapAfter := 10 * time.Minute

	store.On("ListStuckExecutions", ctx, stuckStatuses, reapAfter).
		Return(nil, errors.New("db down"))

	mgr := newReaperManager(store)
	_, err := mgr.ReapStuckExecutions(ctx, reapAfter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list stuck executions")
	store.AssertExpectations(t)
}

func TestReapStuckExecutions_SaveErrorAfterCASStillCountsAsReaped(t *testing.T) {
	// The CAS already flipped the row to failed; the follow-up
	// canonical-error save is best-effort. A save failure must not
	// double-count as a race-lost (the row IS reaped, status-wise),
	// but Errored should bump so ops can spot the persistence flake.
	ctx := context.Background()
	store := new(MockConfigStore)
	reapAfter := 10 * time.Minute

	row := stuckExec("exec-saveflake", "approved")
	flipped := row
	flipped.Status = failedStatus
	store.On("ListStuckExecutions", ctx, stuckStatuses, reapAfter).
		Return([]config.PurchaseExecution{row}, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-saveflake", stuckStatuses, failedStatus, (*string)(nil)).
		Return(&flipped, nil)
	store.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).
		Return(errors.New("write conflict"))

	mgr := newReaperManager(store)
	result, err := mgr.ReapStuckExecutions(ctx, reapAfter)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Reaped)
	assert.Equal(t, 1, result.Errored)
	store.AssertExpectations(t)
}

func TestParseReapAfterFromEnv_DefaultWhenUnset(t *testing.T) {
	t.Setenv(reapAfterEnvVar, "")
	got := ParseReapAfterFromEnv()
	assert.Equal(t, DefaultReapAfter, got)
}

func TestParseReapAfterFromEnv_ValidDuration(t *testing.T) {
	t.Setenv(reapAfterEnvVar, "7m")
	got := ParseReapAfterFromEnv()
	assert.Equal(t, 7*time.Minute, got)
}

func TestParseReapAfterFromEnv_InvalidFallsBackToDefault(t *testing.T) {
	t.Setenv(reapAfterEnvVar, "not-a-duration")
	got := ParseReapAfterFromEnv()
	assert.Equal(t, DefaultReapAfter, got, "malformed env value must fall back to default, not crash")
}

func TestParseReapAfterFromEnv_GarbageFallsBackToDefault(t *testing.T) {
	// Explicit garbage-input case named per the A4 CR finding so the
	// regression intent is searchable. Complements
	// _InvalidFallsBackToDefault above.
	t.Setenv(reapAfterEnvVar, "garbage")
	got := ParseReapAfterFromEnv()
	assert.Equal(t, DefaultReapAfter, got)
}

func TestParseReapAfterFromEnv_ZeroFallsBackToDefault(t *testing.T) {
	// "0s" is a syntactically valid Go duration but would make the SELECT
	// match every approved/running row regardless of age — the reaper
	// would flip in-flight executions. A2 rejects non-positive durations
	// at parse time; this test is the regression guard.
	t.Setenv(reapAfterEnvVar, "0s")
	got := ParseReapAfterFromEnv()
	assert.Equal(t, DefaultReapAfter, got, "0s must fall back to default — would otherwise reap fresh executions")
}

func TestParseReapAfterFromEnv_NegativeFallsBackToDefault(t *testing.T) {
	// "-5m" parses cleanly as a negative duration. Passed unchanged into
	// the store, it would invert the cutoff: "updated_at < NOW() - (-5m)"
	// == "updated_at < NOW() + 5m" — reaping rows from 5 minutes in the
	// future, i.e. effectively every row. A2 rejects this at parse time.
	t.Setenv(reapAfterEnvVar, "-5m")
	got := ParseReapAfterFromEnv()
	assert.Equal(t, DefaultReapAfter, got, "negative duration must fall back to default — would otherwise reap fresh executions")
}

func TestParseReapAfterFromEnv_NonStandardButValidGoDuration(t *testing.T) {
	// Sanity check Go's parser accepts non-minute units — ops may
	// reasonably set e.g. "2h" for a relaxed cadence.
	t.Setenv(reapAfterEnvVar, "2h30m")
	got := ParseReapAfterFromEnv()
	assert.Equal(t, 2*time.Hour+30*time.Minute, got)
}

// ─── F4 regression: safe-to-retry gate (adversarial review follow-up) ────────

// TestReapStuckExecutions_AzureSPNotSafeToRetry is the regression test for F4:
// a stranded Azure savings-plans execution must NOT receive "safe to retry" in
// its canonical error message. Azure SP purchases use a timestamp-based alias
// name with no server-side idempotency key, so an operator retry would create
// a duplicate savings plan.
func TestReapStuckExecutions_AzureSPNotSafeToRetry(t *testing.T) {
	ctx := context.Background()
	store := new(MockConfigStore)
	reapAfter := 10 * time.Minute

	row := stuckExec("exec-azsp", "approved")
	row.Recommendations = []config.RecommendationRecord{
		{Provider: "azure", Service: "savingsplans", Count: 1},
	}
	transitioned := row
	transitioned.Status = failedStatus

	store.On("ListStuckExecutions", ctx, stuckStatuses, reapAfter).
		Return([]config.PurchaseExecution{row}, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-azsp", stuckStatuses, failedStatus, (*string)(nil)).
		Return(&transitioned, nil)
	store.On("SavePurchaseExecution", ctx, mock.MatchedBy(func(e *config.PurchaseExecution) bool {
		// "safe to retry" MUST NOT appear — Azure SP is not idempotent.
		return e.ExecutionID == "exec-azsp" &&
			e.Status == failedStatus &&
			strings.Contains(e.Error, "reaped after") &&
			!strings.Contains(e.Error, "safe to retry")
	})).Return(nil)

	mgr := newReaperManager(store)
	result, err := mgr.ReapStuckExecutions(ctx, reapAfter)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Reaped)
	store.AssertExpectations(t)
}

// TestReapStuckExecutions_EmptyRecsNotSafeToRetry verifies that executions
// with no recommendations are not stamped "safe to retry": an empty rec-set
// means allRecsSafeToRedrive returns false (unknown purchase state).
func TestReapStuckExecutions_EmptyRecsNotSafeToRetry(t *testing.T) {
	ctx := context.Background()
	store := new(MockConfigStore)
	reapAfter := 10 * time.Minute

	row := stuckExec("exec-empty", "running") // stuckExec has no recs
	transitioned := row
	transitioned.Status = failedStatus

	store.On("ListStuckExecutions", ctx, stuckStatuses, reapAfter).
		Return([]config.PurchaseExecution{row}, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-empty", stuckStatuses, failedStatus, (*string)(nil)).
		Return(&transitioned, nil)
	store.On("SavePurchaseExecution", ctx, mock.MatchedBy(func(e *config.PurchaseExecution) bool {
		return e.ExecutionID == "exec-empty" &&
			e.Status == failedStatus &&
			!strings.Contains(e.Error, "safe to retry")
	})).Return(nil)

	mgr := newReaperManager(store)
	result, err := mgr.ReapStuckExecutions(ctx, reapAfter)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Reaped)
	store.AssertExpectations(t)
}
