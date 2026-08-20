package config

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Ramp-step accounting for issue #1861.
//
// A ramp step of a multi-account plan is not one purchase, it is one purchase
// per cloud account, and each of those succeeds or fails on its own. The plan
// has bought the step only when every one of them has. Both the advance gate
// (CompletePlanStep) and the plan-health stuck-ramp report read that fact out
// of the same CTEs below so they cannot disagree about which plans are frozen.
//
// WHICH ACCOUNTS COUNT
// The execution rows the fan-out wrote at the time, intersected with the
// accounts the plan still targets. The rows decide WHICH units exist, so an
// account added later is not retroactively required to have bought an earlier
// step; the plan's current attachments decide whether a unit still MATTERS, so
// an account the plan no longer targets stops holding a step open.
//
// That intersection is the gate's release valve, and it has to exist. An
// account can become permanently unable to buy (revoked credentials, closed
// account, an Azure savings-plans row that purchase.RedriveRefusalReason
// refuses to retry by design), and a failed row cannot be canceled either --
// IsCancelable and CancelExecutionAtomic both admit only pending/notified/
// scheduled. Without the attachment test such a unit would hold its step open
// forever and the plan would never buy the rest of its ramp: the exact
// "permanently-failed account freezes the ramp" caveat issue #1861 attached to
// this design. Detaching the account from the plan (SetPlanAccounts, reachable
// from the plan-accounts endpoint) is a non-destructive, reversible operator
// action that says "this plan no longer buys for that account", and it releases
// the step. Deleting the cloud account works too, because the cloud_account_id
// FK is ON DELETE SET NULL (migration 000011), but that is plan-wide and
// destructive.
//
// ONCE BOUGHT, ALWAYS BOUGHT
// A purchase cannot be un-made, so "has this unit bought?" is answered by
// whether ANY row for it succeeded, never by its latest attempt. Reading it off
// the latest attempt would let a later failed attempt for an account that
// already bought re-open the step, and the stuck report would then tell an
// operator to retry an account that has already bought -- turning a reporting
// artifact into a duplicate commitment. Latest-attempt semantics apply only to
// the in-flight / stuck question, which is genuinely about the present.
//
// KNOWN GAP 1: the unit set is derived from rows that exist. If
// executeForAccount buys for an account and then fails to persist its row (the
// AUDIT LOSS path), that account has no unit and the step can be counted
// complete without it. Closing it needs the fan-out width recorded durably at
// fan-out time, which needs a migration; tracked separately. Note the failure
// needs an audit-loss event, and the same event equally hides a purchase that
// DID land.
//
// KNOWN GAP 2: root and per-account rows are told apart by cloud_account_id,
// which issue #1537 showed is not reliable for rows written before it landed --
// purchase.reattachAccountScope exists because per-account rows with no scope
// are real, and it recognizes them by a colon in the idempotency lineage key,
// which this has no equivalent of. A step whose only rows are legacy scopeless
// per-account rows collapses them into one unit and the latest speaks for all
// of them, which is #1861's own bug for those rows. Such plans have almost
// always advanced past the affected steps already, so this is documented rather
// than fixed.
//
// WHY NOT DERIVE current_step INSTEAD
// The alternative in the issue was to stop storing CurrentStep and compute it
// as the highest fully-bought step. CleanupOldExecutions deletes completed
// executions past the retention horizon, so a derived position would fall back
// toward zero as the rows aged out and the plan would buy its whole ramp a
// second time. Deriving it also does not avoid the freeze it was meant to
// avoid: contiguously it stalls on the same incomplete step, and
// non-contiguously it jumps over it, which is the overstatement the
// skipped-predecessor refusal exists to prevent.

// PARAMETER CONVENTION for the composed queries below: $1 is always
// RampStepSucceededStatuses, because the shared rampStepUnitCTE reads it. Each
// query binds its own parameters from $2 on. Changing this means changing every
// query in this file together.

// The ramp_step CTE names the (plan_id, step_number) pairs a query is about.
// rampStepUnitCTE joins against it by that name, so every query below opens
// with exactly one of these two definitions.
const (
	// rampStepScopeOne is the single (plan, step) a completion is about.
	rampStepScopeOne = `
	ramp_step AS (SELECT $3::uuid AS plan_id, $4::int AS step_number)`

	// rampStepScopeNextPerPlan is the next uncounted step of every plan that
	// has a ramp still running.
	//
	// The total_steps > 1 test is not an optimization. Without it every plan
	// is in scope, including "immediate" ones (total_steps 1) whose single
	// execution is stamped step 1 by default; one ordinary failed purchase on
	// such a plan would be reported as a blocked ramp step on a plan that has
	// no ramp, penalizing it for a row the failed_executions factor is already
	// counting. The current_step < total_steps test drops finished ramps,
	// which have no next step to block.
	rampStepScopeNextPerPlan = `
	ramp_step AS (
		SELECT p.id AS plan_id,
		       COALESCE((p.ramp_schedule ->> 'current_step')::int, 0) + 1 AS step_number
		  FROM purchase_plans p
		 WHERE COALESCE((p.ramp_schedule ->> 'total_steps')::int, 1) > 1
		   AND COALESCE((p.ramp_schedule ->> 'current_step')::int, 0)
		     < COALESCE((p.ramp_schedule ->> 'total_steps')::int, 1))`
)

// rampStepUnitCTE reduces a ramp step's execution rows to one row per fan-out
// unit, carrying that unit's current status and whether it has ever bought.
//
// `eligible` drops rows whose cloud account the plan no longer targets (see the
// release-valve note above). Rows with no account survive: they are root rows,
// judged below.
//
// `unit` picks the scope and the representative row, once per (step, account).
// Keying on the step as well as the account is only visibly load-bearing for a
// caller whose ramp_step spans a RANGE of steps -- OccupiedRampStepsInRangeTx
// does -- but it is the correct key regardless: without it one account collapses
// into a single row across every step in scope, so a step it bought and a step
// it has merely scheduled become indistinguishable.
//
// Scope: a fanned-out step writes one row per cloud account plus a root row
// carrying no account. The root row's status is an aggregate of its children
// (partially_completed when some bought, failed when none did), so counting it
// alongside them would let an all-accounts-failed step stay blocked by its own
// container forever, even after every account was retried to success. When any
// eligible row for the step names an account, only account rows count;
// otherwise the single root row is the unit. Deriving that test from `eligible`
// rather than from the raw table matters: a step whose account rows are all
// detached must fall back to its root row, not end up with no units at all.
//
// Representative row: the ordering prefers a row no retry has superseded
// (retry_execution_id IS NULL), then the most recently written, then
// execution_id so the pick is total and stable (execution_id is unique, so no
// tie survives). Both leading keys are load-bearing and neither subsumes the
// other. A retry successor and the predecessor it supersedes are written in one
// transaction and so share an updated_at, which only the supersession key
// separates; a re-drive of a failed ROOT row re-fans-out and writes fresh rows
// that supersede nothing, which only updated_at separates. Missing either lets
// a dead attempt speak for a unit that has moved on.
//
// ever_bought is a window aggregate over the WHOLE unit, deliberately not the
// representative row: see the "once bought, always bought" note above.
const rampStepUnitCTE = `
	eligible AS (
		SELECT e.plan_id, e.step_number, e.status, e.cloud_account_id,
		       e.retry_execution_id, e.updated_at, e.execution_id
		  FROM purchase_executions e
		  JOIN ramp_step s
		    ON s.plan_id = e.plan_id AND s.step_number = e.step_number
		 WHERE e.cloud_account_id IS NULL
		    OR EXISTS (
		         SELECT 1
		           FROM plan_accounts pa
		          WHERE pa.plan_id = e.plan_id
		            AND pa.account_id = e.cloud_account_id)
	),
	unit AS (
		SELECT DISTINCT ON (e.plan_id, e.step_number, e.cloud_account_id)
		       e.plan_id, e.step_number, e.status,
		       bool_or(e.status = ANY($1))
		         OVER (PARTITION BY e.plan_id, e.step_number, e.cloud_account_id) AS ever_bought
		  FROM eligible e
		 WHERE e.cloud_account_id IS NOT NULL
		    OR NOT EXISTS (
		         SELECT 1
		           FROM eligible f
		          WHERE f.plan_id = e.plan_id
		            AND f.step_number = e.step_number
		            AND f.cloud_account_id IS NOT NULL)
		 ORDER BY e.plan_id, e.step_number, e.cloud_account_id,
		          (e.retry_execution_id IS NULL) DESC,
		          e.updated_at DESC,
		          e.execution_id
	)`

// rampStepFanOutQuery counts, over one step's fan-out units, how many have
// bought and how many are still holding the step open. A unit that has bought
// can never be outstanding, however its latest attempt ended.
const rampStepFanOutQuery = `WITH` + rampStepScopeOne + `,` + rampStepUnitCTE + `
	SELECT count(*) FILTER (WHERE ever_bought),
	       count(*) FILTER (WHERE NOT ever_bought AND status <> ALL($2))
	  FROM unit`

// stuckRampStepQuery reports every running ramp whose next step has a unit that
// has never bought and whose latest attempt ended terminally unsuccessful.
const stuckRampStepQuery = `WITH` + rampStepScopeNextPerPlan + `,` + rampStepUnitCTE + `
	SELECT plan_id, step_number, count(*)
	  FROM unit
	 WHERE NOT ever_bought AND status = ANY($2)
	 GROUP BY plan_id, step_number`

// occupiedRampStepsQuery lists the steps in [$4, $5] of plan $3 that already
// have a unit which bought or is still working. Used to keep a new fan-out off
// a step that is already covered (issue #1861); see
// OccupiedRampStepsInRangeTx.
const occupiedRampStepsQuery = `WITH
	ramp_step AS (
		SELECT $3::uuid AS plan_id, generate_series($4::int, $5::int) AS step_number),` +
	rampStepUnitCTE + `
	SELECT step_number
	  FROM unit
	 GROUP BY step_number
	HAVING count(*) FILTER (WHERE ever_bought) > 0
	    OR count(*) FILTER (WHERE NOT ever_bought AND status <> ALL($2)) > 0
	 ORDER BY step_number`

// requireRampStepBought reports whether ramp step stepNumber of planID may be
// counted, using the transaction the caller already holds the plan row locked
// in. It returns a wrapped ErrRampStepIncomplete when the step is not fully
// bought, and that includes the case where no unit bought at all: a step with
// no successful execution has bought nothing, and an empty result set must not
// pass a guard whose whole job is to prove a purchase happened.
func requireRampStepBought(ctx context.Context, tx pgx.Tx, planID string, stepNumber int) error {
	var bought, outstanding int
	if err := tx.QueryRow(ctx, rampStepFanOutQuery,
		RampStepSucceededStatuses, RampStepSettledStatuses, planID, stepNumber,
	).Scan(&bought, &outstanding); err != nil {
		return fmt.Errorf("failed to read plan %s ramp step %d fan-out: %w", planID, stepNumber, err)
	}
	if bought == 0 {
		return fmt.Errorf("%w: no execution of plan %s ramp step %d bought anything",
			ErrRampStepIncomplete, planID, stepNumber)
	}
	if outstanding > 0 {
		return fmt.Errorf("%w: %d account(s) of plan %s ramp step %d have not bought",
			ErrRampStepIncomplete, outstanding, planID, stepNumber)
	}
	return nil
}

// OccupiedRampStepsInRangeTx returns the steps between from and to (inclusive)
// of planID that already have a fan-out unit which bought or is still working,
// ascending. It runs in the caller's transaction so the answer can be acted on
// atomically; see the lock requirement below.
//
// It exists so a caller about to mint executions for a range of steps can
// refuse to target one that is already covered. Both halves of the predicate
// are load-bearing and neither subsumes the other:
//
//   - A step some account BOUGHT must not get a fresh root row. The completeness
//     gate holds CurrentStep still while an account is outstanding, so the
//     plan-scoped create endpoint keeps stamping CurrentStep+1, the same step.
//     Approving that row re-fans-out across every account including the ones
//     that already bought, under a fresh idempotency lineage whose derived
//     tokens miss the provider-side dedupe entirely: a genuine duplicate
//     commitment, not a no-op.
//   - A step that already has a LIVE unit must not get a second one either. Two
//     concurrent creates both find nothing bought, and each mints its own root
//     row for the same step; approving both double-buys exactly as above. The
//     first create's own pending row is what the second must see, and it is not
//     "bought", so the bought half alone cannot stop it.
//
// A step whose units all settled without buying (canceled) is NOT occupied: the
// operator abandoned it and rescheduling it is the intended recovery.
//
// CALLER CONTRACT: hold LockPurchasePlanTx on planID for the same transaction
// before calling this and until the resulting inserts commit. Without that lock
// the answer is advisory -- two callers read it concurrently, both see the step
// free and both insert.
func (s *PostgresStore) OccupiedRampStepsInRangeTx(ctx context.Context, tx pgx.Tx, planID string, from, to int) ([]int, error) {
	if from > to {
		return nil, fmt.Errorf("occupied ramp steps of plan %s: range %d-%d is empty", planID, from, to)
	}
	rows, err := tx.Query(ctx, occupiedRampStepsQuery,
		RampStepSucceededStatuses, RampStepSettledStatuses, planID, from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to query occupied ramp steps of plan %s: %w", planID, err)
	}
	defer rows.Close()

	var steps []int
	for rows.Next() {
		var step int
		if err := rows.Scan(&step); err != nil {
			return nil, fmt.Errorf("failed to scan occupied ramp step: %w", err)
		}
		steps = append(steps, step)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read occupied ramp steps of plan %s: %w", planID, err)
	}
	return steps, nil
}

// GetStuckRampSteps returns, keyed by plan ID, the plan's next ramp step and
// how many of that step's units are stuck on it -- never bought, latest attempt
// terminal and unsuccessful, no retry in flight.
//
// Derived on every read rather than stamped on a row when the advance was
// refused, which is what makes it safe to score a plan's health on: a stamped
// refusal is true only at the instant it is written and nothing clears it, so a
// plan that recovered would stay marked unhealthy until an operator noticed.
// Recomputing from the same rows the advance gate reads means the report
// disappears exactly when the ramp unfreezes, and cannot drift from the gate.
//
// Plans with nothing stuck are absent from the map rather than present with a
// zero, so a caller cannot confuse "healthy" with "not reported".
func (s *PostgresStore) GetStuckRampSteps(ctx context.Context) (map[string]RampStepBlock, error) {
	rows, err := s.db.Query(ctx, stuckRampStepQuery, RampStepSucceededStatuses, RampStepStuckStatuses)
	if err != nil {
		return nil, fmt.Errorf("failed to query stuck ramp steps: %w", err)
	}
	defer rows.Close()

	blocks := make(map[string]RampStepBlock)
	for rows.Next() {
		var planID string
		var block RampStepBlock
		if err := rows.Scan(&planID, &block.StepNumber, &block.StuckExecutions); err != nil {
			return nil, fmt.Errorf("failed to scan stuck ramp step: %w", err)
		}
		blocks[planID] = block
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read stuck ramp steps: %w", err)
	}
	return blocks, nil
}
