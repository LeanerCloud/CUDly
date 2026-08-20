package config

// store_postgres_complete_step_test.go -- pgxmock regression tests for
// CompletePlanStep, the atomic ramp-step advance added for issue #1071 and made
// idempotent per step for issue #1669.
//
// The #1071 bug: updatePlanProgress did a plain GetPurchasePlan ->
// CurrentStep++ -> UpdatePurchasePlan read-modify-write with no row lock, so
// two overlapping Lambda invocations could both read CurrentStep=N and both
// write N+1, skipping a ramp step (a lost update on a money path).
//
// The #1669 bug: even under the lock, the advance was a blind ++ that carried
// no notion of WHICH step completed. A multi-account plan produces one
// execution per account per ramp step, so retrying two separately-failed
// accounts of one step advanced the ramp twice and the plan reported itself a
// step further along than the commitment it had bought.
//
// The #1861 bug: even counted once, a step counted as complete as soon as ONE
// of its accounts bought, so an operator repairing a multi-account step one
// account at a time moved the plan to "step N done" while the others had
// bought nothing for step N.
//
// These tests pin the fix's contract:
//   - the read happens inside a transaction (Begin) and carries FOR UPDATE,
//   - completing step N from CurrentStep N-1 persists CurrentStep = N,
//   - completing the step the ramp is sitting on writes nothing and reports
//     ErrRampStepCountedBySibling, while completing a step it has moved past
//     reports ErrRampStepAlreadyCounted -- only the latter is an anomaly,
//   - completing a step whose fan-out still has an outstanding account, or
//     which nothing bought, writes nothing and reports ErrRampStepIncomplete,
//   - completing a step more than one beyond CurrentStep is refused,
//   - a non-positive step is refused before the transaction is opened,
//   - a plan deleted mid-race is tolerated (returns nil, no spurious error),
//   - a completed ramp clears next_execution_date instead of advancing.
//
// pgxmock uses regexp query matching (see newMock), so an expectation whose
// pattern requires "FOR UPDATE" only matches if the production query actually
// emits it -- that is the atomicity guard. Dropping FOR UPDATE from the store
// makes TestPGXMock_CompletePlanStep_LocksAndAdvances fail.

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// purchasePlanRowCols mirrors the SELECT column list in purchasePlanSelectCols.
var purchasePlanRowCols = []string{
	"id", "name", "enabled", "auto_purchase", "notification_days_before",
	"services", "ramp_schedule", "created_at", "updated_at",
	"next_execution_date", "last_execution_date", "last_notification_sent",
}

// rampStepArg is a pgxmock.Argument that unmarshals the ramp_schedule JSONB
// passed to the UPDATE and asserts its CurrentStep, proving the advance landed
// in the persisted row rather than only in memory.
type rampStepArg struct{ want int }

func (a rampStepArg) Match(v interface{}) bool {
	b, ok := v.([]byte)
	if !ok {
		return false
	}
	var rs RampSchedule
	if err := json.Unmarshal(b, &rs); err != nil {
		return false
	}
	return rs.CurrentStep == a.want
}

// nullTimeArg matches a *time.Time UPDATE argument by presence (nil vs set),
// used to assert next_execution_date is cleared on a completed ramp.
type nullTimeArg struct{ wantNil bool }

func (a nullTimeArg) Match(v interface{}) bool {
	tp, ok := v.(*time.Time)
	if !ok {
		return false
	}
	return (tp == nil) == a.wantNil
}

// afterArg matches a time.Time UPDATE argument that is strictly after the given
// instant, used to assert updated_at is refreshed rather than persisted stale.
type afterArg struct{ notBefore time.Time }

func (a afterArg) Match(v interface{}) bool {
	ts, ok := v.(time.Time)
	if !ok {
		return false
	}
	return ts.After(a.notBefore)
}

const purchasePlanUpdateArgs = 11

// completeStepUpdateArgs builds the WithArgs matcher list for the UPDATE issued
// by CompletePlanStep, asserting the persisted CurrentStep at $7, a refreshed
// updated_at at $8 (> staleUpdatedAt), and the next_execution_date presence at
// $9 while leaving the rest as AnyArg.
func completeStepUpdateArgs(wantStep int, staleUpdatedAt time.Time, wantNextNil bool) []interface{} {
	args := anyArgsCfg(purchasePlanUpdateArgs)
	args[6] = rampStepArg{want: wantStep}         // ramp_schedule = $7
	args[7] = afterArg{notBefore: staleUpdatedAt} // updated_at = $8
	args[8] = nullTimeArg{wantNil: wantNextNil}   // next_execution_date = $9
	return args
}

// expectFanOut registers the ramp step's fan-out tally that CompletePlanStep
// consults before advancing (issue #1861): how many of the step's units have
// bought and how many are still holding it open.
//
// The pattern names each clause of that query which, if dropped, still yields a
// query that runs and returns two numbers -- silently wrong ones. Deleting the
// plan_accounts test removes the gate's only release valve; deleting the
// root-row exclusion lets an all-accounts-failed step stay blocked by its own
// container; deleting DISTINCT ON or the updated_at ordering lets a dead
// attempt speak for a unit; dropping step_number from the reduction keys
// collapses an account across steps; deleting ever_bought re-opens a step an
// account has already bought. The behavior of each is measured against a real database in
// store_postgres_ramp_step_integration_test.go; these are the cheap tripwires
// that fire in the default test run.
func expectFanOut(mock pgxmock.PgxPoolIface, planID string, stepNumber, bought, outstanding int) {
	mock.ExpectQuery(`WITH[\s\S]*FROM plan_accounts pa[\s\S]*`+
		`DISTINCT ON \(e\.plan_id, e\.step_number, e\.cloud_account_id\)[\s\S]*`+
		`bool_or\(e\.status = ANY\(\$1\)\)[\s\S]*`+
		`NOT EXISTS[\s\S]*FROM eligible f[\s\S]*`+
		`e\.updated_at DESC[\s\S]*FROM unit`).
		WithArgs(RampStepSucceededStatuses, RampStepSettledStatuses, planID, stepNumber).
		WillReturnRows(pgxmock.NewRows([]string{"bought", "outstanding"}).AddRow(bought, outstanding))
}

// rampPlanRows builds a single purchase_plans row carrying ramp, with
// updated_at seeded to stale so a test can assert the store refreshes it.
func rampPlanRows(t *testing.T, planID string, ramp RampSchedule, now, stale time.Time, nextExec sql.NullTime) *pgxmock.Rows {
	t.Helper()
	svcJSON, err := json.Marshal(map[string]ServiceConfig{})
	require.NoError(t, err)
	rampJSON, err := json.Marshal(ramp)
	require.NoError(t, err)

	return pgxmock.NewRows(purchasePlanRowCols).AddRow(
		planID, "Ramp Plan", true, true, 3,
		svcJSON, rampJSON, now, stale,
		nextExec, sql.NullTime{Valid: false}, sql.NullTime{Valid: false},
	)
}

func TestPGXMock_CompletePlanStep_LocksAndAdvances(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	stale := now.AddDate(0, 0, -30)
	ramp := RampSchedule{
		Type:             "weekly",
		PercentPerStep:   25,
		StepIntervalDays: 7,
		CurrentStep:      1,
		TotalSteps:       4,
		StartDate:        now,
	}

	// Begin -> SELECT ... FOR UPDATE -> UPDATE (CurrentStep advanced 1 -> 2,
	// updated_at refreshed, next_execution_date still set since ramp not
	// complete) -> Commit.
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT[\s\S]*FROM purchase_plans[\s\S]*WHERE id = \$1 FOR UPDATE`).
		WithArgs("plan-123").
		WillReturnRows(rampPlanRows(t, "plan-123", ramp, now, stale, sql.NullTime{Valid: false}))
	expectFanOut(mock, "plan-123", 2, 3, 0)
	mock.ExpectExec(`UPDATE purchase_plans`).
		WithArgs(completeStepUpdateArgs(2, stale, false)...).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, store.CompletePlanStep(ctx, "plan-123", 2))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_CompletePlanStep_OutstandingAccountBlocksAdvance is the issue
// #1861 guard at the store boundary: the fan-out tally reports an account that
// has not bought, so the plan row must not be written at all. pgxmock fails the
// test if an UPDATE is issued, because no ExpectExec is registered.
func TestPGXMock_CompletePlanStep_OutstandingAccountBlocksAdvance(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	stale := now.AddDate(0, 0, -30)
	ramp := RampSchedule{Type: "weekly", PercentPerStep: 25, StepIntervalDays: 7, CurrentStep: 1, TotalSteps: 4, StartDate: now}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT[\s\S]*FROM purchase_plans[\s\S]*WHERE id = \$1 FOR UPDATE`).
		WithArgs("plan-multi").
		WillReturnRows(rampPlanRows(t, "plan-multi", ramp, now, stale, sql.NullTime{Valid: false}))
	expectFanOut(mock, "plan-multi", 2, 2, 1)
	mock.ExpectRollback()

	err := store.CompletePlanStep(ctx, "plan-multi", 2)
	require.ErrorIs(t, err, ErrRampStepIncomplete,
		"one account of a 3-account step has not bought, so the step is not complete")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_CompletePlanStep_StepNothingBoughtIsRefused pins the fail-closed
// half of the tally. An empty or all-unsuccessful fan-out satisfies "nothing is
// outstanding" vacuously, so a guard that only checked outstanding would wave
// through a step no purchase ever landed for.
func TestPGXMock_CompletePlanStep_StepNothingBoughtIsRefused(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	stale := now.AddDate(0, 0, -30)
	ramp := RampSchedule{Type: "weekly", PercentPerStep: 25, StepIntervalDays: 7, CurrentStep: 1, TotalSteps: 4, StartDate: now}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT[\s\S]*FROM purchase_plans[\s\S]*WHERE id = \$1 FOR UPDATE`).
		WithArgs("plan-empty").
		WillReturnRows(rampPlanRows(t, "plan-empty", ramp, now, stale, sql.NullTime{Valid: false}))
	expectFanOut(mock, "plan-empty", 2, 0, 0)
	mock.ExpectRollback()

	err := store.CompletePlanStep(ctx, "plan-empty", 2)
	require.ErrorIs(t, err, ErrRampStepIncomplete)
	assert.Contains(t, err.Error(), "bought anything")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_CompletePlanStep_SiblingCountedStepWritesNothing is the issue
// #1669 guard at the store boundary: the second per-account retry of one ramp
// step completes a step a sibling just counted, and must leave the row exactly
// as it found it. pgxmock fails the test if any UPDATE is issued, because no
// ExpectExec is registered.
//
// The sentinel is the sibling one, not the already-counted one, and the
// distinction is load-bearing (issue #1861): only the latter is stamped onto
// the execution row, and stamping this routine case would flip a cleanly
// completed purchase into History's audit-gap rendering.
func TestPGXMock_CompletePlanStep_SiblingCountedStepWritesNothing(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	stale := now.AddDate(0, 0, -30)
	// The ramp advanced to 3 when the first retry of step 3 landed moments ago.
	ramp := RampSchedule{Type: "weekly", PercentPerStep: 25, StepIntervalDays: 7, CurrentStep: 3, TotalSteps: 4, StartDate: now}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT[\s\S]*FROM purchase_plans[\s\S]*WHERE id = \$1 FOR UPDATE`).
		WithArgs("plan-123").
		WillReturnRows(rampPlanRows(t, "plan-123", ramp, now, stale, sql.NullTime{Valid: true, Time: now}))
	mock.ExpectRollback()

	err := store.CompletePlanStep(ctx, "plan-123", 3)
	require.ErrorIs(t, err, ErrRampStepCountedBySibling,
		"a step the plan is sitting on was counted by a sibling, which is routine")
	require.NotErrorIs(t, err, ErrRampStepAlreadyCounted,
		"the two must stay distinguishable: only the passed-step case is stamped on the row")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_CompletePlanStep_PassedStepIsReportedAsAnomalous covers the other
// half of that split: a row completing a step the ramp moved PAST bought
// commitment the plan counted earlier and will never count again. That is the
// mis-stamped-row case issue #1861 asks to be recorded, so it must reach the
// caller under its own sentinel.
func TestPGXMock_CompletePlanStep_PassedStepIsReportedAsAnomalous(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	stale := now.AddDate(0, 0, -30)
	ramp := RampSchedule{Type: "weekly", PercentPerStep: 25, StepIntervalDays: 7, CurrentStep: 3, TotalSteps: 6, StartDate: now}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT[\s\S]*FROM purchase_plans[\s\S]*WHERE id = \$1 FOR UPDATE`).
		WithArgs("plan-past").
		WillReturnRows(rampPlanRows(t, "plan-past", ramp, now, stale, sql.NullTime{Valid: true, Time: now}))
	mock.ExpectRollback()

	err := store.CompletePlanStep(ctx, "plan-past", 2)
	require.ErrorIs(t, err, ErrRampStepAlreadyCounted)
	require.NotErrorIs(t, err, ErrRampStepCountedBySibling)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_CompletePlanStep_SkippedPredecessorIsRefused pins the other half
// of the idempotency contract: advancing over steps that never completed would
// overstate how much commitment the plan has bought, so it errors rather than
// jumping. No UPDATE is expected.
func TestPGXMock_CompletePlanStep_SkippedPredecessorIsRefused(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	stale := now.AddDate(0, 0, -30)
	ramp := RampSchedule{Type: "weekly", PercentPerStep: 25, StepIntervalDays: 7, CurrentStep: 1, TotalSteps: 6, StartDate: now}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT[\s\S]*FROM purchase_plans[\s\S]*WHERE id = \$1 FOR UPDATE`).
		WithArgs("plan-gap").
		WillReturnRows(rampPlanRows(t, "plan-gap", ramp, now, stale, sql.NullTime{Valid: false}))
	mock.ExpectRollback()

	err := store.CompletePlanStep(ctx, "plan-gap", 4)
	require.Error(t, err, "steps 2-3 never completed, so step 4 must not silently advance the ramp")
	assert.Contains(t, err.Error(), "never completed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_CompletePlanStep_NonPositiveStepIsRefused asserts the argument is
// validated before any transaction opens: a plan-attributed execution that
// carries no ramp step must not advance the ramp by guesswork.
func TestPGXMock_CompletePlanStep_NonPositiveStepIsRefused(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// No Begin is expected: the guard must short-circuit before the round trip.
	err := store.CompletePlanStep(ctx, "plan-123", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1-based")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_CompletePlanStep_PlanDeletedMidRace(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT[\s\S]*FROM purchase_plans[\s\S]*WHERE id = \$1 FOR UPDATE`).
		WithArgs("gone").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	// A plan deleted between execution and progress update must not error: the
	// caller cannot control that race and should not be penalized for it.
	require.NoError(t, store.CompletePlanStep(ctx, "gone", 2))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_CompletePlanStep_CompletedRampClearsNextDate(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	stale := now.AddDate(0, 0, -30)
	// Already at the last step: CurrentStep == TotalSteps means IsComplete, so
	// the step is not advanced and next_execution_date is cleared.
	ramp := RampSchedule{Type: "weekly", PercentPerStep: 25, StepIntervalDays: 7, CurrentStep: 4, TotalSteps: 4, StartDate: now}

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT[\s\S]*FROM purchase_plans[\s\S]*WHERE id = \$1 FOR UPDATE`).
		WithArgs("plan-done").
		WillReturnRows(rampPlanRows(t, "plan-done", ramp, now, stale, sql.NullTime{Valid: true, Time: now}))
	// Step stays at 4 (already complete), updated_at refreshed, and
	// next_execution_date is cleared.
	mock.ExpectExec(`UPDATE purchase_plans`).
		WithArgs(completeStepUpdateArgs(4, stale, true)...).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, store.CompletePlanStep(ctx, "plan-done", 5))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_CompletePlanStep_LockErrorRollsBack(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT[\s\S]*FROM purchase_plans[\s\S]*WHERE id = \$1 FOR UPDATE`).
		WithArgs("plan-err").
		WillReturnError(assert.AnError)
	mock.ExpectRollback()

	err := store.CompletePlanStep(ctx, "plan-err", 2)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
