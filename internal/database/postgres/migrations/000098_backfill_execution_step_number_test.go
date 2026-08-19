//go:build integration
// +build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedStepNumberPlan inserts a purchase_plans row sitting on ramp step
// currentStep and returns its id.
func seedStepNumberPlan(ctx context.Context, t *testing.T, pool *pgxpool.Pool, name string, currentStep int) string {
	t.Helper()
	var planID string
	err := pool.QueryRow(ctx, `
		INSERT INTO purchase_plans (name, ramp_schedule)
		VALUES ($1, jsonb_build_object('type', 'weekly', 'percent_per_step', 25,
		                               'step_interval_days', 7, 'total_steps', 4,
		                               'current_step', $2::int))
		RETURNING id
	`, name, currentStep).Scan(&planID)
	require.NoError(t, err)
	return planID
}

// seedStepNumberExecution inserts a purchase_executions row and returns its id.
func seedStepNumberExecution(ctx context.Context, t *testing.T, pool *pgxpool.Pool, planID, status string, stepNumber int) string {
	t.Helper()
	var execID string
	err := pool.QueryRow(ctx, `
		INSERT INTO purchase_executions (plan_id, status, step_number, scheduled_date)
		VALUES ($1, $2, $3, NOW())
		RETURNING execution_id
	`, planID, status, stepNumber).Scan(&execID)
	require.NoError(t, err)
	return execID
}

// stepNumberOf reads back one execution's step_number.
func stepNumberOf(ctx context.Context, t *testing.T, pool *pgxpool.Pool, execID string) int {
	t.Helper()
	var step int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT step_number FROM purchase_executions WHERE execution_id = $1`, execID).Scan(&step))
	return step
}

// TestMigration_BackfillExecutionStepNumber locks down migration 000098: rows
// carrying the pre-#1669 convention (step_number = the COUNT of completed ramp
// steps) are retargeted at the step they will actually complete, while
// correctly-stamped rows and terminal rows are left alone.
func TestMigration_BackfillExecutionStepNumber(t *testing.T) {
	ctx := context.Background()
	migrationsPath := getMigrationsPath()

	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)
	pool := container.DB.Pool()

	// Pin just below 000098 so the assertions exercise this migration's direct
	// effect. The number below head is read from disk because migration numbers
	// in this repo are not contiguous.
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, previousMigrationVersion(t, 98)))

	// A fresh plan (nothing bought yet) whose pending row was stamped 0 by the
	// old writer: the shape that would hit the "no ramp step" refusal.
	freshPlan := seedStepNumberPlan(ctx, t, pool, "Fresh Plan", 0)
	freshPending := seedStepNumberExecution(ctx, t, pool, freshPlan, "pending", 0)

	// A plan two steps in whose approved row was stamped 2 by the old writer:
	// the shape that would silently no-op forever.
	midPlan := seedStepNumberPlan(ctx, t, pool, "Mid Plan", 2)
	midApproved := seedStepNumberExecution(ctx, t, pool, midPlan, "approved", 2)
	// Correctly stamped rows on the same plan must not move.
	midCorrect := seedStepNumberExecution(ctx, t, pool, midPlan, "pending", 3)
	midFuture := seedStepNumberExecution(ctx, t, pool, midPlan, "notified", 4)
	// The old-convention row that advanced this plan 1 -> 2, stamped 1 by the
	// old writer. It must not be rewritten, but note WHICH clause spares it: a
	// completed row is its own completed sibling, so NOT EXISTS excludes it
	// whether or not the status allowlist is there. Its job in this fixture is
	// to establish that no completed row carries step 2, which is what lets
	// midApproved and midFailed below isolate the other two clauses.
	midTerminal := seedStepNumberExecution(ctx, t, pool, midPlan, "completed", 1)
	// The row that isolates the status allowlist. It is terminal but NOT
	// completed, so NOT EXISTS is true for it (no completed row carries step 2)
	// and step_number <= current_step holds: line 57 of the migration is the
	// only thing excluding it. Rewriting it would not be cosmetic, because
	// persistRetryExecution propagates a failed row's StepNumber onto its retry
	// successor, so moving it changes which ramp step a later operator retry
	// completes.
	midFailed := seedStepNumberExecution(ctx, t, pool, midPlan, "failed", 2)
	// Every other status the plan flow can still execute from. Missing one is
	// how 'paused' survived three review passes, so cover the set explicitly.
	midExecutable := map[string]string{}
	for _, status := range []string{"paused", "running", "scheduled", "notified"} {
		midExecutable[status] = seedStepNumberExecution(ctx, t, pool, midPlan, status, 2)
	}

	// A plan whose ramp_schedule carries no current_step key at all: the
	// COALESCE must read it as 0, so its step-0 row is still retargeted. Without
	// the COALESCE the comparison is NULL and the row is silently skipped.
	var keylessPlan string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO purchase_plans (name, ramp_schedule)
		VALUES ('Keyless Plan', jsonb_build_object('type', 'weekly', 'total_steps', 4))
		RETURNING id
	`).Scan(&keylessPlan))
	keylessPending := seedStepNumberExecution(ctx, t, pool, keylessPlan, "pending", 0)

	// The shape the NOT EXISTS guard protects: a multi-account step 3 that
	// partially failed, where the first per-account retry already advanced the
	// ramp to 3 and a sibling successor for the SAME step is still pending. Its
	// step_number is correct; retargeting it to 4 would make it buy step 3's
	// tranche while advancing the ramp past step 4.
	partialPlan := seedStepNumberPlan(ctx, t, pool, "Partial Step Plan", 3)
	seedStepNumberExecution(ctx, t, pool, partialPlan, "completed", 3)
	partialSibling := seedStepNumberExecution(ctx, t, pool, partialPlan, "pending", 3)

	// Same shape, but the sibling that advanced the ramp landed
	// partially_completed rather than completed. That is the LIKELIER form of
	// this scenario, not a corner: applyAccountOutcome stamps
	// partially_completed when some of an account's recs committed and others
	// failed, which is what a partial fan-out produces. It isolates the second
	// entry of the sibling list, which nothing else in this fixture exercises.
	partialRecPlan := seedStepNumberPlan(ctx, t, pool, "Partially Completed Sibling Plan", 3)
	seedStepNumberExecution(ctx, t, pool, partialRecPlan, "partially_completed", 3)
	partialRecSibling := seedStepNumberExecution(ctx, t, pool, partialRecPlan, "pending", 3)

	require.Equal(t, 0, stepNumberOf(ctx, t, pool, freshPending), "seed must start on the old convention")
	require.Equal(t, 2, stepNumberOf(ctx, t, pool, midApproved), "seed must start on the old convention")

	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, 98))

	assert.Equal(t, 1, stepNumberOf(ctx, t, pool, freshPending),
		"a step-0 executable row on a plan at step 0 must be retargeted at step 1")
	assert.Equal(t, 3, stepNumberOf(ctx, t, pool, midApproved),
		"an old-convention executable row must be retargeted at current_step + 1")
	assert.Equal(t, 3, stepNumberOf(ctx, t, pool, midCorrect),
		"a correctly-stamped row for the next step must not move")
	assert.Equal(t, 4, stepNumberOf(ctx, t, pool, midFuture),
		"a correctly-stamped row for a later step must not move")
	assert.Equal(t, 1, stepNumberOf(ctx, t, pool, midTerminal),
		"a completed row is audit trail and must not be rewritten (excluded by NOT EXISTS, which finds itself)")
	assert.Equal(t, 2, stepNumberOf(ctx, t, pool, midFailed),
		"a failed row is audit trail and a retry successor's step source; only the status allowlist excludes it")
	for status, execID := range midExecutable {
		assert.Equal(t, 3, stepNumberOf(ctx, t, pool, execID),
			"a %q row can still execute and must be retargeted", status)
	}
	assert.Equal(t, 1, stepNumberOf(ctx, t, pool, keylessPending),
		"a plan with no current_step key must be read as step 0, not skipped")
	assert.Equal(t, 3, stepNumberOf(ctx, t, pool, partialSibling),
		"a pending sibling of an already-completed step is correctly stamped and must not move")
	assert.Equal(t, 3, stepNumberOf(ctx, t, pool, partialRecSibling),
		"a pending sibling of a partially_completed step must not move either; only that entry of the sibling list excludes it")

	// The down migration is a documented no-op: rolling back the version must
	// succeed and must leave the corrected values in place.
	require.NoError(t, migrations.MigrateToVersion(ctx, pool, migrationsPath, previousMigrationVersion(t, 98)))
	assert.Equal(t, 3, stepNumberOf(ctx, t, pool, midApproved),
		"the no-op down must leave the corrected value, which the pre-#1669 code ignores")
}
