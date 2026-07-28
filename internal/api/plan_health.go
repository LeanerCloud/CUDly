// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
)

// PlanHealthFactorCode identifies a single scoring factor in a plan's
// health-score breakdown (issue #340 follow-up). Typed so the frontend
// tooltip renders deterministic wording per factor instead of parsing free
// text out of Note.
type PlanHealthFactorCode string

// Health-factor codes, in the same order computePlanHealth evaluates them.
const (
	HealthFactorOverdue            PlanHealthFactorCode = "overdue"
	HealthFactorFailedExecutions   PlanHealthFactorCode = "failed_executions"
	HealthFactorCanceledExecutions PlanHealthFactorCode = "canceled_executions"
	HealthFactorStalled            PlanHealthFactorCode = "stalled"
	HealthFactorBehindSchedule     PlanHealthFactorCode = "behind_schedule"
	HealthFactorDisabledMidway     PlanHealthFactorCode = "disabled_midway"
)

// PlanHealthFactor is one penalty applied when computing a plan's health
// score. Returned alongside the score so a bad score is actionable instead
// of opaque: the frontend tooltip enumerates every factor and its penalty.
type PlanHealthFactor struct {
	Code    PlanHealthFactorCode `json:"code"`
	Penalty int                  `json:"penalty"`
	Note    string               `json:"note"`
}

// Score bounds and per-factor weights for computePlanHealth. Named
// constants rather than inline magic numbers so the scoring table in the
// package doc / issue #340 has a single source of truth.
const (
	planHealthScoreMax = 100
	planHealthScoreMin = 0

	penaltyOverdue         = 30
	penaltyPerFailedExec   = 10
	penaltyPerCanceledExec = 5
	penaltyStalled         = 15
	penaltyBehindSchedule  = 20
	penaltyDisabledMidway  = 25

	// maxCountedFailedExecs / maxCountedCanceledExecs cap how many rows
	// count toward their respective penalty, so a plan with a long failure
	// history can't score below the documented worst case for that factor
	// (-40 / -20 respectively).
	maxCountedFailedExecs   = 4
	maxCountedCanceledExecs = 4

	// planHealthLookbackDays bounds how far back the execution factors
	// count. It is deliberately the SAME constant the execution-retention
	// sweep uses (config.DefaultExecutionTTLDays, applied by
	// CleanupOldExecutions via internal/server/handler.go), because the two
	// have to agree for the score to mean anything:
	//
	// That sweep deletes canceled rows past the retention horizon but never
	// deletes failed ones. Counting over any longer window would therefore
	// measure the two factors over different spans -- canceled over the
	// surviving retention period, failed over all of history -- and a plan
	// that failed a few times years ago would stay penalized forever, with
	// no number of subsequent clean runs able to clear it. Health is a
	// statement about the plan now, so both factors count the same recent
	// period, the one retention guarantees is fully populated.
	//
	// Agreeing on the length is necessary but not sufficient: the sweep has
	// to measure it from the same column. CountExecutionsByPlanAndStatus
	// windows on updated_at, so CleanupOldExecutions retains canceled rows
	// on updated_at too (its branch 2, and the canceled exclusion on its
	// expires_at branch). If either side is moved back onto scheduled_date,
	// the sweep starts deleting rows this score is still counting and a
	// plan's score jumps by up to 20 points overnight with nothing having
	// changed about the plan.
	//
	// Every factor note names the window, so the number on screen is never
	// ambiguous about what it covers.
	planHealthLookbackDays = config.DefaultExecutionTTLDays
)

// planHealthStatusFailed mirrors the "failed" execution-status literal used
// throughout internal/api (e.g. handler_purchases.go finalizePurchaseStatus).
// No config.Status* constant exists for it today -- only StatusCanceled and
// LegacyStatusCanceled do -- so this local constant is the typed handle for
// this file.
const planHealthStatusFailed = "failed"

// planHealthExecutionStatuses selects the execution statuses
// computePlanHealth needs (failed + both spellings of canceled). Passed to
// config.StoreInterface.CountExecutionsByPlanAndStatus, which aggregates in
// SQL: the History handler's GetExecutionsByStatuses is capped at
// config.DefaultListLimit across ALL plans, so counting rows out of it
// would silently understate any plan whose executions fall outside the
// newest page and render an unhealthy plan as healthy.
var planHealthExecutionStatuses = []string{
	planHealthStatusFailed,
	config.StatusCanceled,
	config.LegacyStatusCanceled,
}

// computePlanHealth derives a 0-100 health score for a single purchase plan
// from its ramp-schedule attributes and its own execution counts (the caller
// passes the counts for this plan's ID only). Starts at 100 and subtracts
// weighted penalties, clamped to [planHealthScoreMin, planHealthScoreMax].
// The returned factors document exactly which penalties applied so a bad
// score is actionable instead of opaque (issue #340 follow-up, PR #376).
//
// A completed plan (RampSchedule.IsComplete(), guarded against the
// degenerate TotalSteps == 0 case) short-circuits to a perfect score:
// historical failure/cancellation rows and a disabled toggle-off after
// completion are expected end-of-life noise, not signals an operator
// should chase.
func computePlanHealth(plan config.PurchasePlan, now time.Time, counts config.ExecutionStatusCounts) (int, []PlanHealthFactor) {
	if plan.RampSchedule.TotalSteps > 0 && plan.RampSchedule.IsComplete() {
		return planHealthScoreMax, nil
	}

	factors := collectPlanHealthFactors(plan, now, counts)

	score := planHealthScoreMax
	for _, f := range factors {
		score -= f.Penalty
	}
	if score < planHealthScoreMin {
		score = planHealthScoreMin
	}
	return score, factors
}

// collectPlanHealthFactors runs every per-factor check and returns the
// subset that applies to plan, in table order (overdue, failed_executions,
// canceled_executions, then the mutually-exclusive stalled/behind_schedule
// pair, then disabled_midway). Split out from computePlanHealth so each
// factor stays an independent, individually testable function.
func collectPlanHealthFactors(plan config.PurchasePlan, now time.Time, counts config.ExecutionStatusCounts) []PlanHealthFactor {
	var factors []PlanHealthFactor
	if f, ok := overdueFactor(plan, now); ok {
		factors = append(factors, f)
	}
	if f, ok := failedExecutionsFactor(counts); ok {
		factors = append(factors, f)
	}
	if f, ok := canceledExecutionsFactor(counts); ok {
		factors = append(factors, f)
	}
	if f, ok := scheduleFactor(plan, now); ok {
		factors = append(factors, f)
	}
	if f, ok := disabledMidwayFactor(plan); ok {
		factors = append(factors, f)
	}
	return factors
}

// overdueFactor: enabled AND next_execution_date < now.
func overdueFactor(plan config.PurchasePlan, now time.Time) (PlanHealthFactor, bool) {
	if !plan.Enabled || plan.NextExecutionDate == nil || !plan.NextExecutionDate.Before(now) {
		return PlanHealthFactor{}, false
	}
	return PlanHealthFactor{
		Code:    HealthFactorOverdue,
		Penalty: penaltyOverdue,
		Note:    "next purchase date has passed",
	}, true
}

// failedExecutionsFactor: -10 per failed execution in the last
// planHealthLookbackDays, capped at 4 (-40 max). The note reports the true
// (uncapped) count and the window it covers, so an operator can see the full
// extent even when the penalty itself is capped.
func failedExecutionsFactor(counts config.ExecutionStatusCounts) (PlanHealthFactor, bool) {
	count := counts[planHealthStatusFailed]
	if count == 0 {
		return PlanHealthFactor{}, false
	}
	counted := count
	if counted > maxCountedFailedExecs {
		counted = maxCountedFailedExecs
	}
	return PlanHealthFactor{
		Code:    HealthFactorFailedExecutions,
		Penalty: counted * penaltyPerFailedExec,
		Note:    fmt.Sprintf("%d failed execution(s) in the last %d days", count, planHealthLookbackDays),
	}, true
}

// canceledExecutionsFactor: -5 per canceled execution in the last
// planHealthLookbackDays, capped at 4 (-20 max). Counts both spellings of
// "canceled" (config.StatusCanceled and the legacy
// config.LegacyStatusCanceled) so pre-#1278 rows aren't invisible to scoring.
func canceledExecutionsFactor(counts config.ExecutionStatusCounts) (PlanHealthFactor, bool) {
	count := counts[config.StatusCanceled] + counts[config.LegacyStatusCanceled]
	if count == 0 {
		return PlanHealthFactor{}, false
	}
	counted := count
	if counted > maxCountedCanceledExecs {
		counted = maxCountedCanceledExecs
	}
	return PlanHealthFactor{
		Code:    HealthFactorCanceledExecutions,
		Penalty: counted * penaltyPerCanceledExec,
		Note:    fmt.Sprintf("%d canceled execution(s) in the last %d days", count, planHealthLookbackDays),
	}, true
}

// scheduleFactor evaluates the ramp schedule's progress against wall-clock
// time and returns at most one of two mutually exclusive factors:
//
//   - stalled (-15): enabled, past the first scheduled interval, but no
//     step has executed yet (CurrentStep == 0).
//   - behind_schedule (-20): CurrentStep lags the step implied by
//     StartDate + StepIntervalDays and capped at TotalSteps, in any other
//     case (including a disabled plan that was never started).
//
// Plans with no positive StepIntervalDays (e.g. "immediate" ramp schedules)
// have no schedule position to fall behind on and are skipped entirely --
// there is no divide-by-zero guard needed elsewhere because this is the
// only place StepIntervalDays is used as a divisor.
//
// Plans with no StartDate are skipped for the same reason: the schedule
// position is unknown, and measuring elapsed time from a zero time.Time
// would report every such plan as ~740000 days behind schedule -- a
// fabricated penalty derived from a missing input rather than a real one.
// RampSchedule.GetNextPurchaseDate guards the same field the same way.
func scheduleFactor(plan config.PurchasePlan, now time.Time) (PlanHealthFactor, bool) {
	ramp := plan.RampSchedule
	if ramp.StepIntervalDays <= 0 || ramp.StartDate.IsZero() {
		return PlanHealthFactor{}, false
	}

	daysSinceStart := int(now.Sub(ramp.StartDate).Hours() / 24)
	if daysSinceStart < ramp.StepIntervalDays {
		return PlanHealthFactor{}, false
	}

	if plan.Enabled && ramp.CurrentStep == 0 {
		return PlanHealthFactor{
			Code:    HealthFactorStalled,
			Penalty: penaltyStalled,
			Note:    "enabled but no purchases have executed since the first scheduled step",
		}, true
	}

	// Clamp to the steps the plan actually has. Elapsed time keeps growing
	// after the ramp's full duration has passed, so the raw quotient runs
	// past TotalSteps: a 4-step weekly ramp started 90 days ago yields 12.
	// Reporting "expected step 12 by now" for a 4-step plan quotes a step
	// the plan does not have, and an operator checking the plan against it
	// finds nothing to reconcile. The comparison and the note are clamped
	// together on purpose -- clamping only the note would let the factor
	// fire on a step number it never displays, and clamping only the
	// comparison would leave the fabricated number on screen.
	expectedStep := daysSinceStart / ramp.StepIntervalDays
	if ramp.TotalSteps > 0 && expectedStep > ramp.TotalSteps {
		expectedStep = ramp.TotalSteps
	}
	if ramp.CurrentStep >= expectedStep {
		return PlanHealthFactor{}, false
	}
	return PlanHealthFactor{
		Code:    HealthFactorBehindSchedule,
		Penalty: penaltyBehindSchedule,
		Note:    fmt.Sprintf("on step %d, expected step %d by now", ramp.CurrentStep, expectedStep),
	}, true
}

// disabledMidwayFactor: disabled with 0 < current_step < total_steps.
func disabledMidwayFactor(plan config.PurchasePlan) (PlanHealthFactor, bool) {
	ramp := plan.RampSchedule
	if plan.Enabled || ramp.CurrentStep <= 0 || ramp.TotalSteps <= 0 || ramp.CurrentStep >= ramp.TotalSteps {
		return PlanHealthFactor{}, false
	}
	return PlanHealthFactor{
		Code:    HealthFactorDisabledMidway,
		Penalty: penaltyDisabledMidway,
		Note:    "disabled before completing its ramp schedule",
	}, true
}
