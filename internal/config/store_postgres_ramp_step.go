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
// of the same CTE below so they cannot disagree about which plans are frozen.
//
// WHICH ACCOUNTS COUNT
// The accounts the step actually targeted, taken from the execution rows the
// fan-out wrote at the time, not from the plan's account list today. A plan's
// account set changes between steps; those rows do not. Detaching an account
// from a plan therefore does not release a step that account is holding open --
// deleting the account itself does, because the cloud_account_id FK is ON
// DELETE SET NULL (migration 000011) and its rows drop out of the fan-out
// scope. Short of that, the operator's exits are the ordinary ones: retry the
// account until it buys, or cancel its row.
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

// The ramp_step CTE names the (plan_id, step_number) pairs a query is about.
// rampStepLatestAttemptCTE joins against it by that name, so every query below
// opens with exactly one of these two definitions.
const (
	// rampStepScopeOne is the single (plan, step) a completion is about.
	rampStepScopeOne = `
	ramp_step AS (SELECT $1::uuid AS plan_id, $2::int AS step_number)`

	// rampStepScopeNextPerPlan is every plan's next uncounted ramp step.
	rampStepScopeNextPerPlan = `
	ramp_step AS (
		SELECT p.id AS plan_id,
		       COALESCE((p.ramp_schedule ->> 'current_step')::int, 0) + 1 AS step_number
		  FROM purchase_plans p)`
)

// rampStepLatestAttemptCTE reduces a ramp step's execution rows to one row per
// fan-out unit: the attempt that currently speaks for that unit.
//
// Scope. A fanned-out step writes one row per cloud account plus a root row
// carrying no account. The root row's status is an aggregate of its children
// (partially_completed when some bought, failed when none did), so counting it
// alongside them would let an all-accounts-failed step stay blocked by its own
// container forever. When any row for the step names an account, only the
// account rows count; otherwise the single root row is the unit.
//
// Latest attempt. Within a unit the ordering picks, in order: a row no retry
// has superseded (retry_execution_id IS NULL) over one that has, then the most
// recently written row, then execution_id so the pick is total and stable.
// Both keys are load-bearing and neither subsumes the other. A retry successor
// and the predecessor it supersedes are written in one transaction and so
// share an updated_at, which only the supersession key separates; a re-drive
// of a failed ROOT row re-fans-out and writes fresh account rows that supersede
// nothing, which only updated_at separates. Missing either lets a dead attempt
// speak for an account that has since bought, and freeze the ramp on it.
const rampStepLatestAttemptCTE = `
	latest_attempt AS (
		SELECT DISTINCT ON (e.plan_id, e.cloud_account_id)
		       e.plan_id, e.step_number, e.status
		  FROM purchase_executions e
		  JOIN ramp_step s
		    ON s.plan_id = e.plan_id AND s.step_number = e.step_number
		 WHERE e.cloud_account_id IS NOT NULL
		    OR NOT EXISTS (
		         SELECT 1
		           FROM purchase_executions f
		          WHERE f.plan_id = e.plan_id
		            AND f.step_number = e.step_number
		            AND f.cloud_account_id IS NOT NULL)
		 ORDER BY e.plan_id, e.cloud_account_id,
		          (e.retry_execution_id IS NULL) DESC,
		          e.updated_at DESC,
		          e.execution_id
	)`

// rampStepFanOutQuery counts, over one step's fan-out units, how many bought
// and how many are still holding the step open.
const rampStepFanOutQuery = `WITH` + rampStepScopeOne + `,` + rampStepLatestAttemptCTE + `
	SELECT count(*) FILTER (WHERE status = ANY($3)),
	       count(*) FILTER (WHERE status <> ALL($4))
	  FROM latest_attempt`

// stuckRampStepQuery reports every plan whose next ramp step has a fan-out unit
// sitting in a terminal-but-unsuccessful status with no retry in flight.
const stuckRampStepQuery = `WITH` + rampStepScopeNextPerPlan + `,` + rampStepLatestAttemptCTE + `
	SELECT plan_id, step_number, count(*)
	  FROM latest_attempt
	 WHERE status = ANY($1)
	 GROUP BY plan_id, step_number`

// requireRampStepBought reports whether ramp step stepNumber of planID may be
// counted, using the transaction the caller already holds the plan row locked
// in. It returns a wrapped ErrRampStepIncomplete when the step is not fully
// bought, and that includes the case where no unit bought at all: a step with
// no successful execution has bought nothing, and an empty result set must not
// pass a guard whose whole job is to prove a purchase happened.
func requireRampStepBought(ctx context.Context, tx pgx.Tx, planID string, stepNumber int) error {
	var bought, outstanding int
	if err := tx.QueryRow(ctx, rampStepFanOutQuery,
		planID, stepNumber, RampStepSucceededStatuses, RampStepSettledStatuses,
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

// GetStuckRampSteps returns, keyed by plan ID, the plan's next ramp step and
// how many of that step's executions are stuck on it -- their latest
// attempt reached a terminal status without buying and no retry is in flight.
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
	rows, err := s.db.Query(ctx, stuckRampStepQuery, RampStepStuckStatuses)
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
