//go:build integration
// +build integration

package config

// Real-DB tests for the ramp-step completeness gate and the stuck-ramp report
// (issue #1861). They build exact execution-row shapes and ask the SQL what it
// decides, because every clause the gate depends on is a predicate over rows
// that a mock cannot evaluate: whether an account is still attached to the
// plan, whether a unit ever bought as opposed to how its latest attempt ended,
// which row speaks for a unit, and whether the root row is a unit at all.
//
// The end-to-end fixtures in internal/purchase drive the executor and so can
// only produce the row shapes that flow naturally out of it. Several of the
// shapes below are reachable in production but not from that fixture (an
// all-accounts-failed root, a superseded root, a bought account with a later
// failed attempt), which is exactly why they had no coverage.

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupRampStepStore(ctx context.Context, t *testing.T) *PostgresStore {
	t.Helper()
	container := testhelpers.RequirePostgresContainer(ctx, t)
	t.Cleanup(func() { container.Cleanup(context.Background()) })

	require.NoError(t,
		migrations.RunMigrations(ctx, container.DB.Pool(), getTestMigrationsPath(), "", ""),
		"migrations failed to apply to a fresh database")
	return NewPostgresStore(container.DB)
}

// rampFixture is a plan on step 2 of 4 with a set of attached cloud accounts.
type rampFixture struct {
	store    *PostgresStore
	planID   string
	accounts []string
}

// newRampFixture creates the plan and attaches accountCount cloud accounts.
// totalSteps drives the ramp shape so a test can build a non-ramp plan too.
func newRampFixture(ctx context.Context, t *testing.T, accountCount, currentStep, totalSteps int) *rampFixture {
	t.Helper()
	store := setupRampStepStore(ctx, t)

	plan := &PurchasePlan{
		Name:     fmt.Sprintf("Ramp Fixture %s", uuid.New().String()[:8]),
		Services: map[string]ServiceConfig{},
		RampSchedule: RampSchedule{
			Type: "weekly", PercentPerStep: 25, StepIntervalDays: 7,
			CurrentStep: currentStep, TotalSteps: totalSteps,
			StartDate: time.Now().AddDate(0, 0, -14),
		},
	}
	require.NoError(t, store.CreatePurchasePlan(ctx, plan))

	f := &rampFixture{store: store, planID: plan.ID}
	for i := 0; i < accountCount; i++ {
		acct := &CloudAccount{
			Name:       fmt.Sprintf("acct-%c", 'a'+i),
			Provider:   "aws",
			ExternalID: fmt.Sprintf("22222222222%d", i),
			Enabled:    true,
		}
		require.NoError(t, store.CreateCloudAccount(ctx, acct))
		f.accounts = append(f.accounts, acct.ID)
	}
	require.NoError(t, store.SetPlanAccounts(ctx, f.planID, f.accounts))
	return f
}

// row persists one execution for the fixture's plan. account may be nil for a
// root row. Rows are written oldest-first by the caller, and each write bumps
// updated_at, so call order is what the latest-attempt ordering sees.
func (f *rampFixture) row(ctx context.Context, t *testing.T, step int, account *string, status string) *PurchaseExecution {
	t.Helper()
	return f.rowWithID(ctx, t, uuid.New().String(), step, account, status)
}

// rowWithID is row with the execution_id chosen by the caller, so a test can
// put the ordering keys in conflict on purpose: execution_id is the ordering's
// last resort, and a test that lets it agree with the keys above it cannot tell
// whether those keys are doing anything.
func (f *rampFixture) rowWithID(ctx context.Context, t *testing.T, execID string, step int, account *string, status string) *PurchaseExecution {
	t.Helper()
	exec := &PurchaseExecution{
		ExecutionID:    execID,
		IdempotencyKey: uuid.New().String(),
		PlanID:         f.planID,
		CloudAccountID: account,
		Status:         status,
		StepNumber:     step,
		ScheduledDate:  time.Now(),
	}
	require.NoError(t, f.store.SavePurchaseExecution(ctx, exec))
	return exec
}

// updatedAt reads a row's updated_at, so a test can prove the precondition its
// ordering assertion depends on instead of assuming it.
func (f *rampFixture) updatedAt(ctx context.Context, t *testing.T, execID string) time.Time {
	t.Helper()
	var ts time.Time
	require.NoError(t, f.store.db.QueryRow(ctx,
		`SELECT updated_at FROM purchase_executions WHERE execution_id = $1`, execID).Scan(&ts))
	return ts
}

// supersede points predecessor at successor, the link persistRetryExecution
// writes onto a retried row.
func (f *rampFixture) supersede(ctx context.Context, t *testing.T, predecessor, successor *PurchaseExecution) {
	t.Helper()
	predecessor.RetryExecutionID = &successor.ExecutionID
	require.NoError(t, f.store.SavePurchaseExecution(ctx, predecessor))
}

// retryInOneTx inserts a successor for predecessor and stamps the supersession
// link, both inside a single transaction -- the shape api.persistRetryExecution
// writes. It matters that this is one transaction: now() is transaction-scoped
// in Postgres, so both rows end up with the SAME updated_at, and the ordering
// that decides which of them speaks for the account cannot fall back on
// recency. Writing them separately would hand the test a discriminator
// production does not have.
func (f *rampFixture) retryInOneTx(ctx context.Context, t *testing.T, predecessor *PurchaseExecution, successorID, status string) *PurchaseExecution {
	t.Helper()
	successor := &PurchaseExecution{
		ExecutionID:    successorID,
		IdempotencyKey: predecessor.IdempotencyKey,
		PlanID:         f.planID,
		CloudAccountID: predecessor.CloudAccountID,
		Status:         status,
		StepNumber:     predecessor.StepNumber,
		ScheduledDate:  time.Now(),
		RetryAttemptN:  predecessor.RetryAttemptN + 1,
	}
	updated := *predecessor
	updated.RetryExecutionID = &successorID

	require.NoError(t, f.store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := f.store.SavePurchaseExecutionTx(ctx, tx, successor); err != nil {
			return err
		}
		return f.store.SavePurchaseExecutionTx(ctx, tx, &updated)
	}))
	return successor
}

// gate runs the advance gate for the plan's next step and reports its verdict.
func (f *rampFixture) gate(ctx context.Context, t *testing.T) error {
	t.Helper()
	return f.store.CompletePlanStep(ctx, f.planID, f.currentStep(ctx, t)+1)
}

// currentStep re-reads the plan's ramp position.
func (f *rampFixture) currentStep(ctx context.Context, t *testing.T) int {
	t.Helper()
	plan, err := f.store.GetPurchasePlan(ctx, f.planID)
	require.NoError(t, err)
	return plan.RampSchedule.CurrentStep
}

func (f *rampFixture) acct(i int) *string { return &f.accounts[i] }

// occupied runs the create-side probe the way its caller must: inside a
// transaction that already holds the plan's ramp lock.
func (f *rampFixture) occupied(ctx context.Context, t *testing.T, from, to int) ([]int, error) {
	t.Helper()
	var steps []int
	err := f.store.WithTx(ctx, func(tx pgx.Tx) error {
		locked, lockErr := f.store.LockPurchasePlanTx(ctx, tx, f.planID)
		if lockErr != nil {
			return lockErr
		}
		require.NotNil(t, locked, "the fixture plan must exist")
		var qErr error
		steps, qErr = f.store.OccupiedRampStepsInRangeTx(ctx, tx, f.planID, from, to)
		return qErr
	})
	return steps, err
}

// TestRampGate_DetachingAnAccountReleasesTheStep is the release valve issue
// #1861 requires option 2 to have, and the one the first cut of this fix
// documented but did not implement.
//
// Account C's row is `failed`. It cannot be retried when the provider refuses
// re-drive (Azure savings plans), and it cannot be canceled at all --
// IsCancelable and CancelExecutionAtomic both admit only pending/notified/
// scheduled. Without an exit the step is held open forever and the plan never
// buys the rest of its ramp. Detaching the account from the plan is the exit.
func TestRampGate_DetachingAnAccountReleasesTheStep(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 3, 2, 4)

	f.row(ctx, t, 3, f.acct(0), StatusCompleted)
	f.row(ctx, t, 3, f.acct(1), StatusCompleted)
	f.row(ctx, t, 3, f.acct(2), StatusFailed)

	require.ErrorIs(t, f.gate(ctx, t), ErrRampStepIncomplete,
		"an attached account that has not bought must hold the step open")
	require.Equal(t, 2, f.currentStep(ctx, t))

	// The operator removes the unrecoverable account from the plan. It is no
	// longer a target, so it no longer has a share of this step to buy.
	require.NoError(t, f.store.SetPlanAccounts(ctx, f.planID, f.accounts[:2]))

	require.NoError(t, f.gate(ctx, t),
		"detaching the account the plan no longer targets must release the ramp")
	assert.Equal(t, 3, f.currentStep(ctx, t))
}

// TestRampGate_DetachedAccountIsNotReportedStuck pins the report to the same
// predicate as the gate. A plan nobody can unstick must be reported stuck; a
// plan that has been released must stop being reported, or the badge keeps
// pointing an operator at an account the plan no longer buys for.
func TestRampGate_DetachedAccountIsNotReportedStuck(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 3, 2, 4)

	f.row(ctx, t, 3, f.acct(0), StatusCompleted)
	f.row(ctx, t, 3, f.acct(1), StatusCompleted)
	f.row(ctx, t, 3, f.acct(2), StatusFailed)

	stuck, err := f.store.GetStuckRampSteps(ctx)
	require.NoError(t, err)
	require.Equal(t, RampStepBlock{StepNumber: 3, StuckExecutions: 1}, stuck[f.planID])

	require.NoError(t, f.store.SetPlanAccounts(ctx, f.planID, f.accounts[:2]))

	stuck, err = f.store.GetStuckRampSteps(ctx)
	require.NoError(t, err)
	assert.NotContains(t, stuck, f.planID)
}

// TestRampGate_AnAccountThatBoughtStaysBought is the monotonicity guard. A
// purchase cannot be un-made, so a later failed attempt for an account that
// already bought must not re-open the step. If it did, the stuck report would
// name that account and an operator following it would retry a purchase that
// already landed, buying the same commitment twice.
func TestRampGate_AnAccountThatBoughtStaysBought(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 2, 2, 4)

	f.row(ctx, t, 3, f.acct(0), StatusCompleted)
	f.row(ctx, t, 3, f.acct(1), StatusCompleted)

	// A later, unsuperseded attempt for account A ends failed: a re-execution
	// of a step that was already bought.
	f.row(ctx, t, 3, f.acct(0), StatusFailed)

	stuck, err := f.store.GetStuckRampSteps(ctx)
	require.NoError(t, err)
	assert.NotContains(t, stuck, f.planID,
		"an account that already bought must never be reported stuck: following that report double-buys")

	require.NoError(t, f.gate(ctx, t),
		"a failed re-attempt cannot un-buy a step every account already bought")
	assert.Equal(t, 3, f.currentStep(ctx, t))
}

// TestRampGate_AllAccountsFailedRootDoesNotBlockAfterRetries pins the
// root-exclusion clause. When every account fails, executeMultiAccount returns
// errAllAccountsFailed and finalizeExecution stamps the ROOT row `failed`. That
// row is a container, not a purchase unit: once each account has been retried
// to success the step is bought, and counting the root alongside its children
// would hold it open forever with nothing left to retry.
//
// The end-to-end fixtures never produce this shape, because a partially-failed
// fan-out stamps the root `partially_completed`, which is a succeeded status.
func TestRampGate_AllAccountsFailedRootDoesNotBlockAfterRetries(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 2, 2, 4)

	root := f.row(ctx, t, 3, nil, StatusFailed)
	failedA := f.row(ctx, t, 3, f.acct(0), StatusFailed)
	failedB := f.row(ctx, t, 3, f.acct(1), StatusFailed)

	require.ErrorIs(t, f.gate(ctx, t), ErrRampStepIncomplete,
		"nothing bought this step yet")

	retryA := f.row(ctx, t, 3, f.acct(0), StatusCompleted)
	f.supersede(ctx, t, failedA, retryA)
	retryB := f.row(ctx, t, 3, f.acct(1), StatusCompleted)
	f.supersede(ctx, t, failedB, retryB)

	require.NoError(t, f.gate(ctx, t),
		"every account bought; the failed root row must not hold the step open")
	assert.Equal(t, 3, f.currentStep(ctx, t))
	require.NotNil(t, root)
}

// TestRampGate_LatestAttemptDecidesAUnitThatNeverBought pins `updated_at DESC`.
//
// The key only bites on a unit that never bought (once a unit has bought,
// ever_bought answers the gate's other question regardless of which row
// represents it) and that has no supersession link to fall back on -- the shape
// a re-drive produces, since re-fanned rows supersede nothing. Account B fails,
// then a later attempt for B is canceled, which is the operator saying
// that unit will not buy this step, so the step is released.
//
// The two execution_ids are chosen so the FAILED row sorts first by
// execution_id, the ordering's last resort. If updated_at DESC is dropped, that
// dead attempt speaks for B, B reads as outstanding and the ramp freezes. The
// test asserts the updated_at precondition it depends on rather than assuming
// two consecutive writes differ.
func TestRampGate_LatestAttemptDecidesAUnitThatNeverBought(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 2, 2, 4)

	f.row(ctx, t, 3, f.acct(0), StatusCompleted)

	const earlyID = "00000000-0000-4000-8000-000000000001"
	const lateID = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	f.rowWithID(ctx, t, earlyID, 3, f.acct(1), StatusFailed)
	f.rowWithID(ctx, t, lateID, 3, f.acct(1), StatusCanceled)

	require.True(t, f.updatedAt(ctx, t, lateID).After(f.updatedAt(ctx, t, earlyID)),
		"the canceled row must be strictly newer, or this test measures nothing")
	require.Less(t, earlyID, lateID,
		"the dead attempt must sort first by execution_id, or the mutant survives")

	stuck, err := f.store.GetStuckRampSteps(ctx)
	require.NoError(t, err)
	assert.NotContains(t, stuck, f.planID,
		"B's live attempt was canceled, not failed: the plan is released, not frozen")

	require.NoError(t, f.gate(ctx, t),
		"B's latest attempt settled the unit; the older failed row must not still speak")
	assert.Equal(t, 3, f.currentStep(ctx, t))
}

// TestRampGate_ASupersededAttemptNeverSpeaks pins the other ordering key, the
// one updated_at cannot cover. persistRetryExecution writes the successor and
// stamps the predecessor in ONE transaction, so both rows carry the same
// transaction timestamp and only the supersession link separates them.
//
// Measured on a unit that has NOT bought, because that is the only state where
// which row speaks changes an answer: account B failed and its retry is still
// pending, so B is recovering, not stuck. Drop the supersession key and the
// ordering falls through to execution_id, which is chosen here so the dead
// predecessor sorts first -- B would then be reported stuck and an operator
// would be sent to retry a row that already has a retry in flight.
func TestRampGate_ASupersededAttemptNeverSpeaks(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 2, 2, 4)

	f.row(ctx, t, 3, f.acct(0), StatusCompleted)

	const predecessorID = "00000000-0000-4000-8000-00000000000a"
	const successorID = "ffffffff-ffff-4fff-8fff-fffffffffffa"
	failed := f.rowWithID(ctx, t, predecessorID, 3, f.acct(1), StatusFailed)
	f.retryInOneTx(ctx, t, failed, successorID, "pending")

	require.Less(t, predecessorID, successorID,
		"the superseded row must sort first by execution_id, or the mutant survives")
	require.Equal(t, f.updatedAt(ctx, t, predecessorID), f.updatedAt(ctx, t, successorID),
		"one transaction must give both rows the same updated_at, or recency would decide instead")

	stuck, err := f.store.GetStuckRampSteps(ctx)
	require.NoError(t, err)
	assert.NotContains(t, stuck, f.planID,
		"the superseded predecessor must not speak: B has a retry in flight, it is not stuck")

	require.ErrorIs(t, f.gate(ctx, t), ErrRampStepIncomplete,
		"the pending retry still holds the step open")
}

// TestStuckRampSteps_IgnoresPlansWithoutARamp pins the report's scope. An
// "immediate" plan has total_steps 1 and its single execution is stamped step 1
// by the schema default, so an ordinary failed purchase on it looked exactly
// like a blocked ramp step: a -25 health penalty, quoting a ramp step, on a plan
// that has no ramp, for the same row failed_executions already counts.
func TestStuckRampSteps_IgnoresPlansWithoutARamp(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 1, 0, 1)

	f.row(ctx, t, 1, f.acct(0), StatusFailed)

	stuck, err := f.store.GetStuckRampSteps(ctx)
	require.NoError(t, err)
	assert.NotContains(t, stuck, f.planID,
		"a plan with a single step has no ramp to block")
}

// TestStuckRampSteps_IgnoresFinishedRamps: a ramp that has bought every step
// has no next step, so nothing can be blocking it.
func TestStuckRampSteps_IgnoresFinishedRamps(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 1, 4, 4)

	f.row(ctx, t, 5, f.acct(0), StatusFailed)

	stuck, err := f.store.GetStuckRampSteps(ctx)
	require.NoError(t, err)
	assert.NotContains(t, stuck, f.planID)
}

// TestStuckRampSteps_ReportsAMultiStepRamp is the positive control for the two
// scope tests above: without it, a filter that excluded everything would still
// let them pass.
func TestStuckRampSteps_ReportsAMultiStepRamp(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 2, 2, 4)

	f.row(ctx, t, 3, f.acct(0), StatusCompleted)
	f.row(ctx, t, 3, f.acct(1), StatusFailed)

	stuck, err := f.store.GetStuckRampSteps(ctx)
	require.NoError(t, err)
	assert.Equal(t, RampStepBlock{StepNumber: 3, StuckExecutions: 1}, stuck[f.planID])
}

// TestStuckRampSteps_RetryInFlightIsNotStuck separates the gate's question from
// the report's. A pending retry holds the step open, so the gate refuses, but
// the plan is recovering rather than frozen and must not be flagged.
func TestStuckRampSteps_RetryInFlightIsNotStuck(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 2, 2, 4)

	f.row(ctx, t, 3, f.acct(0), StatusCompleted)
	failed := f.row(ctx, t, 3, f.acct(1), StatusFailed)
	retry := f.row(ctx, t, 3, f.acct(1), "pending")
	f.supersede(ctx, t, failed, retry)

	stuck, err := f.store.GetStuckRampSteps(ctx)
	require.NoError(t, err)
	assert.NotContains(t, stuck, f.planID, "a retry in flight is recovery, not a frozen ramp")

	require.ErrorIs(t, f.gate(ctx, t), ErrRampStepIncomplete,
		"the gate still waits for the retry to land")
}

// TestOccupiedRampSteps_SeparatesStepsOfTheSameAccount pins the step key in the
// unit reduction. The range query is the only caller whose scope spans more
// than one step, so without step_number in the DISTINCT ON and the PARTITION
// BY, one account collapses to a single row across the whole range and the two
// steps become one unit.
//
// Account A bought step 3; its only step-4 row was canceled, which is settled
// without buying and therefore leaves step 4 free to schedule. Only step 3 is
// occupied. Collapse the two steps into one unit and the account's ever_bought
// leaks across, reporting step 4 as occupied and refusing a legitimate create.
func TestOccupiedRampSteps_SeparatesStepsOfTheSameAccount(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 1, 2, 6)

	// The step-4 row is written second and sorts last by execution_id, so it
	// wins every tie-break the reduction has if the step key is missing.
	f.rowWithID(ctx, t, "00000000-0000-4000-8000-000000000003", 3, f.acct(0), StatusCompleted)
	f.rowWithID(ctx, t, "ffffffff-ffff-4fff-8fff-fffffffffff4", 4, f.acct(0), StatusCanceled)

	occupied, err := f.occupied(ctx, t, 3, 5)
	require.NoError(t, err)
	assert.Equal(t, []int{3}, occupied,
		"step 3 bought, step 4 canceled: only step 3 is occupied")
}

// TestOccupiedRampSteps_LiveRowOccupiesTheStep is the half of the predicate the
// concurrent-create guard depends on. A pending row bought nothing, so the
// bought test alone does not see it, yet a second create for that step mints a
// second root row and approving both double-buys.
func TestOccupiedRampSteps_LiveRowOccupiesTheStep(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 1, 2, 6)

	f.row(ctx, t, 3, nil, "pending")

	occupied, err := f.occupied(ctx, t, 3, 4)
	require.NoError(t, err)
	assert.Equal(t, []int{3}, occupied,
		"a step already scheduled must not be scheduled again")
}

// TestOccupiedRampSteps_CanceledStepIsFree keeps the guard from turning a
// cancel into a dead end: a step whose units all settled without buying has
// nothing bought and nothing in flight, so rescheduling it is the intended
// recovery.
func TestOccupiedRampSteps_CanceledStepIsFree(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 1, 2, 6)

	f.row(ctx, t, 3, f.acct(0), StatusCanceled)

	occupied, err := f.occupied(ctx, t, 3, 4)
	require.NoError(t, err)
	assert.Empty(t, occupied)
}

// TestOccupiedRampSteps_RejectsAnInvertedRange: generate_series over an
// inverted range yields no rows, so a silent empty answer would read as "no
// step is occupied" and wave the create through. A guard on a money path must
// not fail open on a nonsense argument.
func TestOccupiedRampSteps_RejectsAnInvertedRange(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 1, 2, 6)
	f.row(ctx, t, 3, f.acct(0), StatusCompleted)

	_, err := f.occupied(ctx, t, 5, 3)
	require.Error(t, err, "an inverted range must error, not report nothing occupied")
}

// TestOccupiedRampSteps_IgnoresDetachedAccounts keeps the create guard on the
// same predicate as the gate: once the plan stops targeting an account, its
// rows stop speaking for the plan in both places.
func TestOccupiedRampSteps_IgnoresDetachedAccounts(t *testing.T) {
	ctx := context.Background()
	f := newRampFixture(ctx, t, 2, 2, 4)

	f.row(ctx, t, 3, f.acct(0), StatusCompleted)
	require.NoError(t, f.store.SetPlanAccounts(ctx, f.planID, f.accounts[1:]))

	occupied, err := f.occupied(ctx, t, 3, 5)
	require.NoError(t, err)
	assert.Empty(t, occupied,
		"the only row that bought belongs to an account the plan no longer targets")
}

// TestRampStepStatusListsPartitionEveryWrittenStatus guards the classification
// itself. Every status the product writes to purchase_executions has to land in
// exactly one of: succeeded (bought), excused (settled without buying), stuck
// (terminal, reported), or in-flight (holds the step open, not reported). A
// status in none of them silently blocks a ramp AND is invisible to the health
// report, which is the one combination an operator cannot diagnose.
func TestRampStepStatusListsPartitionEveryWrittenStatus(t *testing.T) {
	// The statuses the History view loads, which is the product's own
	// enumeration of what a purchase_executions row can be.
	written := []string{
		"pending", "notified", "scheduled", "approved", "running", "paused",
		StatusCompleted, StatusPartiallyCompleted, StatusFailed, StatusExpired,
		StatusCanceled, LegacyStatusCanceled,
	}
	// In-flight statuses hold a step open on purpose and are deliberately not
	// reported as stuck; they are listed here so adding a status to the schema
	// forces a decision rather than defaulting into invisibility.
	inFlight := map[string]bool{
		"pending": true, "notified": true, "scheduled": true,
		"approved": true, "running": true, "paused": true,
	}

	classify := func(status string) []string {
		var in []string
		if slices.Contains(RampStepSucceededStatuses, status) {
			in = append(in, "succeeded")
		}
		if slices.Contains(RampStepSettledStatuses, status) && !slices.Contains(RampStepSucceededStatuses, status) {
			in = append(in, "excused")
		}
		if slices.Contains(RampStepStuckStatuses, status) {
			in = append(in, "stuck")
		}
		if inFlight[status] {
			in = append(in, "in-flight")
		}
		return in
	}

	require.NotEmpty(t, written)
	for _, status := range written {
		classes := classify(status)
		assert.Len(t, classes, 1,
			"status %q must fall in exactly one ramp-step class, got %v", status, classes)
	}
}
