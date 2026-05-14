package api

import (
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
)

// Plan health score
//
// Surfaces a quick 0-100 "is this plan executing as expected" glance on
// each plan card. Pure function so the formula can be regression-tested
// without HTTP / DB plumbing, and so the response-side data is computed
// once per list call instead of leaking into persistence.
//
// Score starts at 100 and subtracts weighted penalties. Each penalty
// produces a HealthFactor entry so the UI tooltip can explain why the
// score isn't 100. Completed plans (current_step >= total_steps) short-
// circuit to (100, nil) — they finished, there is nothing to diagnose.
//
// Penalty table (keep in sync with the plan file at
// ~/.claude/projects/.../plans-health-score.md):
//
//	overdue              -30   plan enabled AND next_execution_date < today
//	failed_executions    -10×n (n capped at 4 -> max -40)
//	cancelled_executions  -5×n (n capped at 4 -> max -20)
//	behind_schedule      -20   current_step < expected step from start_date
//	stalled              -15   enabled, start_date past, current_step == 0
//	                            for > 1 step_interval_days
//	disabled_midway      -25   disabled with 0 < current_step < total_steps
//
// Note on the "completed plan" short-circuit: a 4/4 plan can still be
// toggled off after completion (Enabled == false, CurrentStep ==
// TotalSteps). Without the short-circuit, disabled_midway would NOT fire
// (current_step is not < total_steps), but the plan would still show
// arbitrary score noise from any historical failed/cancelled rows. The
// short-circuit makes finished plans always read as 100, which matches
// user intuition for "this plan is done".

// HealthFactor is one bullet in the per-plan health-score breakdown.
// Surfaced via the badge tooltip so a low score is actionable, not just
// a number. Kind is a stable machine slug (so the frontend can colour /
// link / filter on it later); Note is the human-readable explanation.
type HealthFactor struct {
	Kind   string `json:"kind"`
	Weight int    `json:"weight"`
	Note   string `json:"note"`
}

// PlanWithHealth wraps a config.PurchasePlan with the response-only
// HealthScore + HealthFactors. The wrapper keeps the score fields out
// of the persisted struct so they can't accidentally drift into
// DynamoDB / Postgres columns. The embedded value (not pointer) keeps
// JSON serialisation flat — clients see the same fields as before plus
// the new ones, no extra "plan" envelope nesting.
type PlanWithHealth struct {
	config.PurchasePlan
	HealthScore   int            `json:"health_score"`
	HealthFactors []HealthFactor `json:"health_factors,omitempty"`
}

// computePlanHealth returns the 0-100 health score and the slice of
// HealthFactor entries explaining every penalty applied. Pure: takes
// only the plan, its execution slice, and "now" so tests can fix time.
//
// executions should be the subset of PurchaseExecution rows whose
// PlanID matches plan.ID — callers are expected to pre-filter (the
// caller already groups all executions by plan_id in a single sweep, so
// re-filtering inside this function would be a duplicate pass).
//
// Implementation runs each penalty check via a small per-factor helper
// so the cyclomatic complexity stays well under the project's gocyclo
// budget (10) and each rule is independently readable / unit-testable
// without dragging the rest of the formula into the test setup.
func computePlanHealth(plan *config.PurchasePlan, executions []config.PurchaseExecution, now time.Time) (int, []HealthFactor) {
	// Completed-plan short-circuit. See package comment for the
	// rationale on why this lives outside the penalty loop.
	if plan.RampSchedule.TotalSteps > 0 && plan.RampSchedule.CurrentStep >= plan.RampSchedule.TotalSteps {
		return 100, nil
	}

	score := 100
	var factors []HealthFactor

	// Penalty checks are run in a fixed order so the resulting
	// HealthFactor slice has a stable iteration sequence (the frontend
	// uses it as-is for the tooltip text — reordering would change the
	// rendered string and the test fixtures).
	checks := []func(*config.PurchasePlan, []config.PurchaseExecution, time.Time) *HealthFactor{
		overduePenalty,
		failedExecutionsPenalty,
		cancelledExecutionsPenalty,
		behindSchedulePenalty,
		stalledPenalty,
		disabledMidwayPenalty,
	}
	for _, check := range checks {
		if f := check(plan, executions, now); f != nil {
			score -= f.Weight
			factors = append(factors, *f)
		}
	}

	// Clamp. The cumulative weight ceiling is 30+40+20+20+15+25 = 150,
	// so without clamping the worst case would render -50.
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score, factors
}

// overduePenalty fires for enabled plans whose next_execution_date is
// strictly before today. A disabled plan with a stale next_execution_date
// isn't an issue — it isn't running. Day-granularity comparison so a
// plan scheduled for "today" doesn't flip to overdue at 00:00:01.
func overduePenalty(plan *config.PurchasePlan, _ []config.PurchaseExecution, now time.Time) *HealthFactor {
	if !plan.Enabled || plan.NextExecutionDate == nil {
		return nil
	}
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if !plan.NextExecutionDate.Before(startOfToday) {
		return nil
	}
	return &HealthFactor{
		Kind:   "overdue",
		Weight: 30,
		Note:   "Next execution date is in the past",
	}
}

// failedExecutionsPenalty applies -10 per failed execution, capped at
// 4 so a single bad week doesn't permanently floor every future score.
func failedExecutionsPenalty(_ *config.PurchasePlan, executions []config.PurchaseExecution, _ time.Time) *HealthFactor {
	n := countExecutionsByStatus(executions, "failed")
	if n == 0 {
		return nil
	}
	capped := n
	if capped > 4 {
		capped = 4
	}
	return &HealthFactor{
		Kind:   "failed_executions",
		Weight: 10 * capped,
		Note:   fmt.Sprintf("%d failed execution(s)", n),
	}
}

// cancelledExecutionsPenalty applies -5 per cancellation, capped at 4.
// The per-event weight is lower than failures because cancellations
// are usually user-initiated rather than execution-time errors.
func cancelledExecutionsPenalty(_ *config.PurchasePlan, executions []config.PurchaseExecution, _ time.Time) *HealthFactor {
	n := countExecutionsByStatus(executions, "cancelled")
	if n == 0 {
		return nil
	}
	capped := n
	if capped > 4 {
		capped = 4
	}
	return &HealthFactor{
		Kind:   "cancelled_executions",
		Weight: 5 * capped,
		Note:   fmt.Sprintf("%d cancelled execution(s)", n),
	}
}

// behindSchedulePenalty fires when current_step lags the step we'd
// expect from start_date + step_interval_days. Only meaningful for
// ramped plans with both a non-zero interval and a start_date in the
// past.
func behindSchedulePenalty(plan *config.PurchasePlan, _ []config.PurchaseExecution, now time.Time) *HealthFactor {
	if plan.RampSchedule.StepIntervalDays == 0 || plan.RampSchedule.StartDate.IsZero() {
		return nil
	}
	daysSinceStart := int(now.Sub(plan.RampSchedule.StartDate).Hours() / 24)
	if daysSinceStart <= 0 {
		return nil
	}
	expectedStep := daysSinceStart / plan.RampSchedule.StepIntervalDays
	if expectedStep > plan.RampSchedule.TotalSteps {
		expectedStep = plan.RampSchedule.TotalSteps
	}
	gap := expectedStep - plan.RampSchedule.CurrentStep
	if gap < 1 {
		return nil
	}
	return &HealthFactor{
		Kind:   "behind_schedule",
		Weight: 20,
		Note:   fmt.Sprintf("Ramp is %d step(s) behind schedule", gap),
	}
}

// stalledPenalty fires when an enabled plan with a real start_date has
// never executed a step. Strict subset of behind_schedule (gap of 1
// when current_step==0), but the user signal is different — "you've
// never executed" reads worse than "you're a step behind", so it gets
// its own slot.
func stalledPenalty(plan *config.PurchasePlan, _ []config.PurchaseExecution, now time.Time) *HealthFactor {
	if !plan.Enabled || plan.RampSchedule.CurrentStep != 0 {
		return nil
	}
	if plan.RampSchedule.StepIntervalDays == 0 || plan.RampSchedule.StartDate.IsZero() {
		return nil
	}
	daysSinceStart := int(now.Sub(plan.RampSchedule.StartDate).Hours() / 24)
	if daysSinceStart <= plan.RampSchedule.StepIntervalDays {
		return nil
	}
	return &HealthFactor{
		Kind:   "stalled",
		Weight: 15,
		Note:   "Plan has not executed any step yet",
	}
}

// disabledMidwayPenalty fires when a plan was disabled mid-ramp (some
// real work was started and then halted). A freshly-disabled plan that
// never ran (CurrentStep == 0) is fine; this only targets the
// "paused with progress" state.
func disabledMidwayPenalty(plan *config.PurchasePlan, _ []config.PurchaseExecution, _ time.Time) *HealthFactor {
	if plan.Enabled {
		return nil
	}
	if plan.RampSchedule.CurrentStep <= 0 || plan.RampSchedule.CurrentStep >= plan.RampSchedule.TotalSteps {
		return nil
	}
	return &HealthFactor{
		Kind:   "disabled_midway",
		Weight: 25,
		Note:   fmt.Sprintf("Disabled at step %d of %d", plan.RampSchedule.CurrentStep, plan.RampSchedule.TotalSteps),
	}
}

// countExecutionsByStatus counts entries whose Status == target. Tiny
// helper, but it makes the penalty cases above read declaratively.
func countExecutionsByStatus(executions []config.PurchaseExecution, target string) int {
	n := 0
	for _, e := range executions {
		if e.Status == target {
			n++
		}
	}
	return n
}

// groupExecutionsByPlan buckets a flat execution slice into a map keyed
// by PlanID. Used by listPlans so the per-plan score computation is
// O(plans + executions) instead of O(plans × executions).
func groupExecutionsByPlan(executions []config.PurchaseExecution) map[string][]config.PurchaseExecution {
	out := make(map[string][]config.PurchaseExecution, len(executions))
	for _, e := range executions {
		out[e.PlanID] = append(out[e.PlanID], e)
	}
	return out
}

// planHealthExecutionStatuses is the union of statuses the score
// formula cares about. Kept as a package-level slice so the handler
// and tests reference the same set (rather than duplicating string
// literals at each call site).
//
// Includes "completed" so a single GetExecutionsByStatuses call covers
// every execution that affects the score; the count-since-start
// denominator for "behind schedule" is computed from plan fields
// directly, but having the completed rows in scope keeps the door open
// for richer factors (e.g. "no completions in the last N days")
// without a second DB hit.
var planHealthExecutionStatuses = []string{
	"completed",
	"failed",
	"expired",
	"cancelled",
	"pending",
	"notified",
}
