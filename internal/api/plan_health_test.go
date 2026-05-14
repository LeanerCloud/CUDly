package api

import (
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
)

// fixedNow is the reference "now" used across the table-driven tests so
// each case's date arithmetic is reproducible. Picked a deterministic
// non-DST date well clear of any wall-clock weirdness.
var fixedNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// helper: dereference time pointer for inline struct construction.
func tp(t time.Time) *time.Time { return &t }

func TestComputePlanHealth(t *testing.T) {
	cases := []struct {
		name           string
		plan           *config.PurchasePlan
		executions     []config.PurchaseExecution
		wantScore      int
		wantFactorKind []string // ordered list of factor.Kind we expect
	}{
		{
			name: "ceiling: young plan, on schedule, no executions yet",
			plan: &config.PurchasePlan{
				ID:      "p1",
				Enabled: true,
				RampSchedule: config.RampSchedule{
					Type:             "weekly",
					StepIntervalDays: 7,
					CurrentStep:      0,
					TotalSteps:       4,
					StartDate:        fixedNow, // started today, hasn't ramped yet
				},
				NextExecutionDate: tp(fixedNow.AddDate(0, 0, 7)),
			},
			executions:     nil,
			wantScore:      100,
			wantFactorKind: nil,
		},
		{
			name: "completed plan short-circuits to 100 even when disabled afterwards",
			plan: &config.PurchasePlan{
				ID:      "p1",
				Enabled: false, // toggled off after completion
				RampSchedule: config.RampSchedule{
					Type:             "weekly",
					StepIntervalDays: 7,
					CurrentStep:      4,
					TotalSteps:       4,
					StartDate:        fixedNow.AddDate(0, 0, -28),
				},
			},
			executions: []config.PurchaseExecution{
				// Even a stale failed row shouldn't drag a finished plan
				// below 100 — the short-circuit fires first.
				{PlanID: "p1", Status: "failed"},
			},
			wantScore:      100,
			wantFactorKind: nil,
		},
		{
			name: "single factor: overdue only",
			plan: &config.PurchasePlan{
				ID:      "p1",
				Enabled: true,
				RampSchedule: config.RampSchedule{
					Type:             "weekly",
					StepIntervalDays: 7,
					CurrentStep:      1,
					TotalSteps:       4,
					StartDate:        fixedNow.AddDate(0, 0, -7),
				},
				NextExecutionDate: tp(fixedNow.AddDate(0, 0, -1)),
			},
			executions:     nil,
			wantScore:      70,
			wantFactorKind: []string{"overdue"},
		},
		{
			name: "multi-factor: overdue + 2 failures + 1 cancelled + behind schedule",
			plan: &config.PurchasePlan{
				ID:      "p1",
				Enabled: true,
				RampSchedule: config.RampSchedule{
					Type:             "weekly",
					StepIntervalDays: 7,
					CurrentStep:      1,
					TotalSteps:       4,
					// 21 days back -> expected step 3, actual 1 -> 2 behind
					StartDate: fixedNow.AddDate(0, 0, -21),
				},
				NextExecutionDate: tp(fixedNow.AddDate(0, 0, -2)),
			},
			executions: []config.PurchaseExecution{
				{PlanID: "p1", Status: "failed"},
				{PlanID: "p1", Status: "failed"},
				{PlanID: "p1", Status: "cancelled"},
				{PlanID: "p1", Status: "completed"}, // ignored — no penalty
			},
			// 100 - 30 (overdue) - 20 (2 failed) - 5 (1 cancelled) - 20 (behind) = 25
			wantScore:      25,
			wantFactorKind: []string{"overdue", "failed_executions", "cancelled_executions", "behind_schedule"},
		},
		{
			name: "floor: every penalty active, clamps to 0",
			plan: &config.PurchasePlan{
				ID:      "p1",
				Enabled: false, // disabled mid-ramp
				RampSchedule: config.RampSchedule{
					Type:             "weekly",
					StepIntervalDays: 7,
					CurrentStep:      1, // disabled at 1/4
					TotalSteps:       4,
					StartDate:        fixedNow.AddDate(0, 0, -28),
				},
				// NextExecutionDate is in the past but the plan is
				// disabled — the overdue factor checks Enabled first, so
				// this row should NOT contribute. Disabled-midway covers
				// the "this plan stopped progressing" case instead.
				NextExecutionDate: tp(fixedNow.AddDate(0, 0, -7)),
			},
			executions: []config.PurchaseExecution{
				{PlanID: "p1", Status: "failed"},
				{PlanID: "p1", Status: "failed"},
				{PlanID: "p1", Status: "failed"},
				{PlanID: "p1", Status: "failed"},
				{PlanID: "p1", Status: "failed"}, // 5th — capped at 4
				{PlanID: "p1", Status: "cancelled"},
				{PlanID: "p1", Status: "cancelled"},
				{PlanID: "p1", Status: "cancelled"},
				{PlanID: "p1", Status: "cancelled"},
				{PlanID: "p1", Status: "cancelled"}, // 5th — capped at 4
			},
			// 100 - 40 (4 failed, capped) - 20 (4 cancelled, capped) - 25 (disabled midway) = 15
			// behind_schedule fires too: 28 days / 7 = expected 4, actual 1, gap = 3 -> -20 -> -5 -> clamped to 0
			wantScore:      0,
			wantFactorKind: []string{"failed_executions", "cancelled_executions", "behind_schedule", "disabled_midway"},
		},
		{
			name: "stalled: enabled plan past first interval with no steps taken",
			plan: &config.PurchasePlan{
				ID:      "p1",
				Enabled: true,
				RampSchedule: config.RampSchedule{
					Type:             "weekly",
					StepIntervalDays: 7,
					CurrentStep:      0,
					TotalSteps:       4,
					StartDate:        fixedNow.AddDate(0, 0, -10), // > 1 interval ago
				},
				NextExecutionDate: tp(fixedNow.AddDate(0, 0, 1)), // not overdue
			},
			executions: nil,
			// 100 - 20 (behind: expected step 1, actual 0) - 15 (stalled) = 65
			wantScore:      65,
			wantFactorKind: []string{"behind_schedule", "stalled"},
		},
		{
			name: "empty executions slice doesn't crash and scores cleanly",
			plan: &config.PurchasePlan{
				ID:      "p1",
				Enabled: true,
				RampSchedule: config.RampSchedule{
					Type:             "immediate",
					StepIntervalDays: 0,
					CurrentStep:      0,
					TotalSteps:       1,
				},
			},
			executions:     []config.PurchaseExecution{},
			wantScore:      100,
			wantFactorKind: nil,
		},
		{
			name: "disabled plan with stale next_execution_date is NOT marked overdue",
			plan: &config.PurchasePlan{
				ID:      "p1",
				Enabled: false,
				RampSchedule: config.RampSchedule{
					Type:             "weekly",
					StepIntervalDays: 7,
					CurrentStep:      0,
					TotalSteps:       4,
				},
				NextExecutionDate: tp(fixedNow.AddDate(0, 0, -30)),
			},
			executions:     nil,
			wantScore:      100,
			wantFactorKind: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			score, factors := computePlanHealth(tc.plan, tc.executions, fixedNow)
			if score != tc.wantScore {
				t.Errorf("score = %d, want %d (factors=%+v)", score, tc.wantScore, factors)
			}
			if len(factors) != len(tc.wantFactorKind) {
				t.Fatalf("factor count = %d, want %d (got %+v)", len(factors), len(tc.wantFactorKind), factors)
			}
			for i, want := range tc.wantFactorKind {
				if factors[i].Kind != want {
					t.Errorf("factor[%d].Kind = %q, want %q", i, factors[i].Kind, want)
				}
				if factors[i].Weight <= 0 {
					t.Errorf("factor[%d].Weight = %d, want positive", i, factors[i].Weight)
				}
				if factors[i].Note == "" {
					t.Errorf("factor[%d].Note is empty", i)
				}
			}
		})
	}
}

func TestComputePlanHealth_ClampedBetween0And100(t *testing.T) {
	// Defensive regression: even a totally pathological plan must stay
	// in [0, 100]. The factor-table sums to 150, so without clamping
	// the worst case would render -50.
	plan := &config.PurchasePlan{
		ID:      "p1",
		Enabled: false,
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 1,
			CurrentStep:      1,
			TotalSteps:       100,
			StartDate:        fixedNow.AddDate(0, 0, -1000),
		},
		NextExecutionDate: tp(fixedNow.AddDate(0, 0, -1)),
	}
	execs := []config.PurchaseExecution{
		{PlanID: "p1", Status: "failed"},
		{PlanID: "p1", Status: "failed"},
		{PlanID: "p1", Status: "failed"},
		{PlanID: "p1", Status: "failed"},
		{PlanID: "p1", Status: "cancelled"},
		{PlanID: "p1", Status: "cancelled"},
		{PlanID: "p1", Status: "cancelled"},
		{PlanID: "p1", Status: "cancelled"},
	}
	score, _ := computePlanHealth(plan, execs, fixedNow)
	if score < 0 || score > 100 {
		t.Fatalf("score = %d, want 0..100", score)
	}
}

func TestGroupExecutionsByPlan(t *testing.T) {
	execs := []config.PurchaseExecution{
		{PlanID: "a", Status: "completed"},
		{PlanID: "b", Status: "failed"},
		{PlanID: "a", Status: "pending"},
		{PlanID: "c", Status: "cancelled"},
		{PlanID: "a", Status: "failed"},
	}
	got := groupExecutionsByPlan(execs)
	if len(got["a"]) != 3 {
		t.Errorf("plan a: got %d, want 3", len(got["a"]))
	}
	if len(got["b"]) != 1 {
		t.Errorf("plan b: got %d, want 1", len(got["b"]))
	}
	if len(got["c"]) != 1 {
		t.Errorf("plan c: got %d, want 1", len(got["c"]))
	}
	if _, ok := got["nonexistent"]; ok {
		t.Errorf("unexpected key 'nonexistent' in result")
	}
}
