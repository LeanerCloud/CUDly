package config

// store_postgres_increment_step_test.go -- pgxmock regression tests for
// IncrementPlanCurrentStep, the atomic ramp-step advance added for issue #1071.
//
// The bug: updatePlanProgress previously did a plain GetPurchasePlan ->
// CurrentStep++ -> UpdatePurchasePlan read-modify-write with no row lock, so
// two overlapping Lambda invocations could both read CurrentStep=N and both
// write N+1, skipping a ramp step (a lost update on a money path).
//
// These tests pin the fix's contract:
//   - the read happens inside a transaction (Begin) and carries FOR UPDATE,
//   - CurrentStep is advanced by exactly one and that value is persisted,
//   - a plan deleted mid-race is tolerated (returns nil, no spurious error),
//   - a completed ramp clears next_execution_date instead of advancing.
//
// pgxmock uses regexp query matching (see newMock), so an expectation whose
// pattern requires "FOR UPDATE" only matches if the production query actually
// emits it -- that is the atomicity guard. Dropping FOR UPDATE from the store
// makes TestPGXMock_IncrementPlanCurrentStep_LocksAndAdvances fail.

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

// incrementUpdateArgs builds the WithArgs matcher list for the UPDATE issued by
// IncrementPlanCurrentStep, asserting the persisted CurrentStep at $7, a
// refreshed updated_at at $8 (> staleUpdatedAt), and the next_execution_date
// presence at $9 while leaving the rest as AnyArg.
func incrementUpdateArgs(wantStep int, staleUpdatedAt time.Time, wantNextNil bool) []interface{} {
	args := anyArgsCfg(purchasePlanUpdateArgs)
	args[6] = rampStepArg{want: wantStep}         // ramp_schedule = $7
	args[7] = afterArg{notBefore: staleUpdatedAt} // updated_at = $8
	args[8] = nullTimeArg{wantNil: wantNextNil}   // next_execution_date = $9
	return args
}

func TestPGXMock_IncrementPlanCurrentStep_LocksAndAdvances(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	svcJSON, err := json.Marshal(map[string]ServiceConfig{})
	require.NoError(t, err)
	ramp := RampSchedule{
		Type:             "weekly",
		PercentPerStep:   25,
		StepIntervalDays: 7,
		CurrentStep:      1,
		TotalSteps:       4,
		StartDate:        now,
	}
	rampJSON, err := json.Marshal(ramp)
	require.NoError(t, err)

	// Seed updated_at with an old timestamp so the test can assert the store
	// refreshes it on increment rather than persisting the stale value.
	stale := now.AddDate(0, 0, -30)
	rows := pgxmock.NewRows(purchasePlanRowCols).AddRow(
		"plan-123", "Ramp Plan", true, true, 3,
		svcJSON, rampJSON, now, stale,
		sql.NullTime{Valid: false}, sql.NullTime{Valid: false}, sql.NullTime{Valid: false},
	)

	// Begin -> SELECT ... FOR UPDATE -> UPDATE (CurrentStep advanced 1 -> 2,
	// updated_at refreshed, next_execution_date still set since ramp not
	// complete) -> Commit.
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT[\s\S]*FROM purchase_plans[\s\S]*WHERE id = \$1 FOR UPDATE`).
		WithArgs("plan-123").
		WillReturnRows(rows)
	mock.ExpectExec(`UPDATE purchase_plans`).
		WithArgs(incrementUpdateArgs(2, stale, false)...).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	err = store.IncrementPlanCurrentStep(ctx, "plan-123")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_IncrementPlanCurrentStep_PlanDeletedMidRace(t *testing.T) {
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
	err := store.IncrementPlanCurrentStep(ctx, "gone")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_IncrementPlanCurrentStep_CompletedRampClearsNextDate(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	svcJSON, err := json.Marshal(map[string]ServiceConfig{})
	require.NoError(t, err)
	// Already at the last step: CurrentStep == TotalSteps means IsComplete, so
	// the step is not advanced and next_execution_date is cleared.
	ramp := RampSchedule{Type: "weekly", PercentPerStep: 25, StepIntervalDays: 7, CurrentStep: 4, TotalSteps: 4, StartDate: now}
	rampJSON, err := json.Marshal(ramp)
	require.NoError(t, err)

	stale := now.AddDate(0, 0, -30)
	rows := pgxmock.NewRows(purchasePlanRowCols).AddRow(
		"plan-done", "Done Plan", true, true, 3,
		svcJSON, rampJSON, now, stale,
		sql.NullTime{Valid: true, Time: now}, sql.NullTime{Valid: false}, sql.NullTime{Valid: false},
	)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT[\s\S]*FROM purchase_plans[\s\S]*WHERE id = \$1 FOR UPDATE`).
		WithArgs("plan-done").
		WillReturnRows(rows)
	// Step stays at 4 (already complete), updated_at refreshed, and
	// next_execution_date is cleared.
	mock.ExpectExec(`UPDATE purchase_plans`).
		WithArgs(incrementUpdateArgs(4, stale, true)...).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	err = store.IncrementPlanCurrentStep(ctx, "plan-done")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_IncrementPlanCurrentStep_LockErrorRollsBack(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT[\s\S]*FROM purchase_plans[\s\S]*WHERE id = \$1 FOR UPDATE`).
		WithArgs("plan-err").
		WillReturnError(assert.AnError)
	mock.ExpectRollback()

	err := store.IncrementPlanCurrentStep(ctx, "plan-err")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
