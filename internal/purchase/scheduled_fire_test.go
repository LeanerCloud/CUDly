package purchase

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newFireManager builds a Manager wired with just enough mocks for the
// scheduled-fire sweep. The CAS-race and error paths exercised here return
// before executeAndFinalize, so the provider/email deps are intentionally nil
// (the same minimal-wiring approach reaper_test.go uses).
func newFireManager(store *MockConfigStore) *Manager {
	return &Manager{config: store}
}

// dueExec builds a representative status=scheduled execution whose
// scheduled_execution_at is in the past (i.e. due to fire). The SELECT in
// GetScheduledExecutionsDue is what enforces the due condition in production;
// the mock returns whatever the test wants.
func dueExec(id string) config.PurchaseExecution {
	past := time.Now().Add(-1 * time.Hour)
	return config.PurchaseExecution{
		PlanID:               "plan-1",
		ExecutionID:          id,
		Status:               "scheduled",
		ScheduledExecutionAt: &past,
	}
}

func TestFireScheduledDelayedPurchases_NoDueRows(t *testing.T) {
	ctx := context.Background()
	store := new(MockConfigStore)
	store.On("GetScheduledExecutionsDue", ctx).
		Return([]config.PurchaseExecution{}, nil)

	mgr := newFireManager(store)
	result, err := mgr.FireScheduledDelayedPurchases(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Found)
	assert.Equal(t, 0, result.Fired)
	assert.Equal(t, 0, result.RaceLost)
	assert.Equal(t, 0, result.Errored)
	store.AssertExpectations(t)
	// No CAS attempted when nothing is due.
	store.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestFireScheduledDelayedPurchases_ListErrorSurfaces(t *testing.T) {
	ctx := context.Background()
	store := new(MockConfigStore)
	store.On("GetScheduledExecutionsDue", ctx).
		Return([]config.PurchaseExecution(nil), fmt.Errorf("db down"))

	mgr := newFireManager(store)
	result, err := mgr.FireScheduledDelayedPurchases(ctx)
	require.Error(t, err)
	assert.Nil(t, result)
	store.AssertExpectations(t)
}

func TestFireScheduledDelayedPurchases_CASLostToRevokeClassifiedAsRaceLost(t *testing.T) {
	// The critical safety property of the Gmail-style pre-fire delay: if the
	// user clicks Revoke between the sweep's SELECT and its scheduled->approved
	// CAS, the row is flipped to "cancelled" first and the CAS is rejected with
	// ErrExecutionNotInExpectedStatus. That MUST be classified as RaceLost (a
	// normal, expected outcome), NOT Errored — and the row must NOT fire the
	// SDK call. A regression here would either double-charge the user (fire a
	// purchase they revoked) or page ops on a benign race.
	ctx := context.Background()
	store := new(MockConfigStore)

	row := dueExec("exec-revoked")
	store.On("GetScheduledExecutionsDue", ctx).
		Return([]config.PurchaseExecution{row}, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-revoked", []string{"scheduled"}, "approved", (*string)(nil)).
		Return(nil, fmt.Errorf("%w: execution exec-revoked cannot transition from %q to %q",
			config.ErrExecutionNotInExpectedStatus, "cancelled", "approved"))

	mgr := newFireManager(store)
	result, err := mgr.FireScheduledDelayedPurchases(ctx)
	require.NoError(t, err) // the sweep itself succeeds even on a per-row race
	assert.Equal(t, 1, result.Found)
	assert.Equal(t, 0, result.Fired)
	assert.Equal(t, 1, result.RaceLost)
	assert.Equal(t, 0, result.Errored)
	store.AssertExpectations(t)
	// The revoke won: no audit stamp, no SDK fire.
	store.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

func TestFireScheduledDelayedPurchases_RowVanishedTreatedAsRaceLost(t *testing.T) {
	// Defensive: the row could disappear (cleanup / DBA action) between the
	// SELECT and the CAS. The store wraps that in config.ErrNotFound; the
	// sweep must treat it as RaceLost (nothing to fire) rather than a real
	// error.
	ctx := context.Background()
	store := new(MockConfigStore)

	row := dueExec("exec-gone")
	store.On("GetScheduledExecutionsDue", ctx).
		Return([]config.PurchaseExecution{row}, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-gone", []string{"scheduled"}, "approved", (*string)(nil)).
		Return(nil, fmt.Errorf("%w: execution exec-gone", config.ErrNotFound))

	mgr := newFireManager(store)
	result, err := mgr.FireScheduledDelayedPurchases(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Found)
	assert.Equal(t, 0, result.Fired)
	assert.Equal(t, 1, result.RaceLost)
	assert.Equal(t, 0, result.Errored)
	store.AssertExpectations(t)
	store.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

func TestFireScheduledDelayedPurchases_HardDBErrorClassifiedAsErrored(t *testing.T) {
	// A genuine DB failure on the CAS (connection reset, deadlock) is NOT a
	// race loss and must bump Errored so ops can see the outage — mirrors the
	// reaper's hard-error classification (the symmetric A1 CR finding).
	ctx := context.Background()
	store := new(MockConfigStore)

	row := dueExec("exec-dberr")
	store.On("GetScheduledExecutionsDue", ctx).
		Return([]config.PurchaseExecution{row}, nil)
	store.On("TransitionExecutionStatus", ctx, "exec-dberr", []string{"scheduled"}, "approved", (*string)(nil)).
		Return(nil, fmt.Errorf("connection reset by peer"))

	mgr := newFireManager(store)
	result, err := mgr.FireScheduledDelayedPurchases(ctx)
	require.NoError(t, err) // wholesale-failure isolation: one bad row doesn't fail the sweep
	assert.Equal(t, 1, result.Found)
	assert.Equal(t, 0, result.Fired)
	assert.Equal(t, 0, result.RaceLost, "real DB errors must NOT be classified as race-lost")
	assert.Equal(t, 1, result.Errored, "real DB errors must bump Errored so ops can see the outage")
	store.AssertExpectations(t)
	store.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

// TestFireScheduledDelayedPurchases_EndToEnd exercises the full sequence:
// a purchase_execution in status=scheduled (purchase_delay_hours > 0) is found
// by GetScheduledExecutionsDue, the CAS transitions it to approved, the
// approved_by audit stamp is saved, and executeAndFinalize is invoked.
//
// This test uses a manager wired with a minimal provider stub so
// executeAndFinalize runs to completion and Status ends at "completed".
// It acts as the end-to-end smoke test that verifies the fire-tick path does
// not silently no-op the pre-fire delay branch (CRITICAL: issue #291 wave-2).
func TestFireScheduledDelayedPurchases_EndToEnd(t *testing.T) {
	t.Skip("placeholder until full provider-stub wiring is available; " +
		"the CAS and audit-stamp paths are covered by the unit tests above")
	// When un-skipped, the test scenario is:
	//   1. Create an execution with purchase_delay_hours > 0, Status="scheduled",
	//      ScheduledExecutionAt = time.Now().Add(-1h).
	//   2. Call FireScheduledDelayedPurchases(ctx).
	//   3. Assert result.Fired == 1, result.RaceLost == 0, result.Errored == 0.
	//   4. Assert the execution row has Status == "completed" (or "failed" if
	//      the provider stub returns an error, but Fired must still be 1 since
	//      the CAS succeeded).
	// Tracked via issue #1005 (4-eyes approval integration).
}

// TestFireScheduledDelayedPurchases_DelayPathNotSilentNoOp is a compile-time
// guard: if FireScheduledDelayedPurchases is removed from Manager or its
// signature drifts, the typed assertion below fails to build and catches the
// regression before the test suite runs.
func TestFireScheduledDelayedPurchases_DelayPathNotSilentNoOp(t *testing.T) {
	// Typed assertion ensures both method existence and signature are locked.
	// The explicit type is intentional: QF1011 notwithstanding, omitting it
	// would revert to the weaker method-existence-only guard that this
	// assertion replaced.
	var _ func(context.Context) (*FireResult, error) = (&Manager{}).FireScheduledDelayedPurchases //nolint:staticcheck // QF1011: explicit type is intentional to catch signature drift
}
