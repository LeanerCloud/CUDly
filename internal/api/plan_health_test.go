package api

import (
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// factorCodes extracts the Code of each factor, in order, for easy
// assertion without caring about penalty/note wording in most tests.
func factorCodes(factors []PlanHealthFactor) []PlanHealthFactorCode {
	codes := make([]PlanHealthFactorCode, len(factors))
	for i, f := range factors {
		codes[i] = f.Code
	}
	return codes
}

func TestComputePlanHealth_HealthyPlanScoresPerfect(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	plan := config.PurchasePlan{
		Enabled: true,
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 7,
			CurrentStep:      1,
			TotalSteps:       4,
			StartDate:        now.AddDate(0, 0, -1), // just started, well within the first interval
		},
	}

	score, factors := computePlanHealth(plan, now, nil)

	assert.Equal(t, 100, score)
	assert.Empty(t, factors)
}

func TestComputePlanHealth_CompletedPlanShortCircuitsRegardlessOfOtherIssues(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	overdueDate := now.AddDate(0, 0, -5)
	plan := config.PurchasePlan{
		Enabled:           false, // would otherwise trip disabled_midway
		NextExecutionDate: &overdueDate,
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 7,
			CurrentStep:      4,
			TotalSteps:       4,
			StartDate:        now.AddDate(0, 0, -60),
		},
	}
	executions := []config.PurchaseExecution{
		{Status: "failed"}, {Status: "failed"}, {Status: config.StatusCanceled},
	}

	score, factors := computePlanHealth(plan, now, executions)

	assert.Equal(t, 100, score)
	assert.Empty(t, factors)
}

func TestComputePlanHealth_Overdue(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	past := now.AddDate(0, 0, -1)
	plan := config.PurchasePlan{
		Enabled:           true,
		NextExecutionDate: &past,
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 30,
			CurrentStep:      1,
			TotalSteps:       4,
			StartDate:        now.AddDate(0, 0, -1),
		},
	}

	score, factors := computePlanHealth(plan, now, nil)

	require.Len(t, factors, 1)
	assert.Equal(t, HealthFactorOverdue, factors[0].Code)
	assert.Equal(t, 30, factors[0].Penalty)
	assert.Equal(t, 100-30, score)
}

func TestComputePlanHealth_OverdueRequiresEnabled(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	past := now.AddDate(0, 0, -1)
	plan := config.PurchasePlan{
		Enabled:           false,
		NextExecutionDate: &past,
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 30,
			CurrentStep:      1,
			TotalSteps:       4,
			StartDate:        now.AddDate(0, 0, -1),
		},
	}

	_, factors := computePlanHealth(plan, now, nil)

	assert.NotContains(t, factorCodes(factors), HealthFactorOverdue)
}

func TestComputePlanHealth_FailedExecutionsPenaltyCapsAtFour(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	plan := config.PurchasePlan{
		Enabled: true,
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 30,
			CurrentStep:      1,
			TotalSteps:       4,
			StartDate:        now.AddDate(0, 0, -1),
		},
	}
	// 6 failed executions: penalty must cap at 4 * 10 = 40, but the note
	// must still report the true count of 6.
	var executions []config.PurchaseExecution
	for i := 0; i < 6; i++ {
		executions = append(executions, config.PurchaseExecution{Status: "failed"})
	}

	score, factors := computePlanHealth(plan, now, executions)

	require.Len(t, factors, 1)
	assert.Equal(t, HealthFactorFailedExecutions, factors[0].Code)
	assert.Equal(t, 40, factors[0].Penalty)
	assert.Contains(t, factors[0].Note, "6 failed")
	assert.Equal(t, 100-40, score)
}

func TestComputePlanHealth_CanceledExecutionsPenaltyCapsAtFourAndCountsBothSpellings(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	plan := config.PurchasePlan{
		Enabled: true,
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 30,
			CurrentStep:      1,
			TotalSteps:       4,
			StartDate:        now.AddDate(0, 0, -1),
		},
	}
	// 3 canonical + 2 legacy-spelled = 5 total canceled; penalty caps at
	// 4 * 5 = 20 but the note reports the true count of 5.
	executions := []config.PurchaseExecution{
		{Status: config.StatusCanceled},
		{Status: config.StatusCanceled},
		{Status: config.StatusCanceled},
		{Status: config.LegacyStatusCanceled},
		{Status: config.LegacyStatusCanceled},
	}

	score, factors := computePlanHealth(plan, now, executions)

	require.Len(t, factors, 1)
	assert.Equal(t, HealthFactorCanceledExecutions, factors[0].Code)
	assert.Equal(t, 20, factors[0].Penalty)
	assert.Contains(t, factors[0].Note, "5 canceled")
	assert.Equal(t, 100-20, score)
}

func TestComputePlanHealth_Stalled(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	plan := config.PurchasePlan{
		Enabled: true,
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 7,
			CurrentStep:      0,
			TotalSteps:       4,
			StartDate:        now.AddDate(0, 0, -10), // past the first interval
		},
	}

	score, factors := computePlanHealth(plan, now, nil)

	require.Len(t, factors, 1)
	assert.Equal(t, HealthFactorStalled, factors[0].Code)
	assert.Equal(t, 15, factors[0].Penalty)
	assert.Equal(t, 100-15, score)
}

func TestComputePlanHealth_BehindSchedule(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	plan := config.PurchasePlan{
		Enabled: true,
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 7,
			CurrentStep:      1, // expected step by day 30 is 4
			TotalSteps:       6,
			StartDate:        now.AddDate(0, 0, -30),
		},
	}

	score, factors := computePlanHealth(plan, now, nil)

	require.Len(t, factors, 1)
	assert.Equal(t, HealthFactorBehindSchedule, factors[0].Code)
	assert.Equal(t, 20, factors[0].Penalty)
	assert.Contains(t, factors[0].Note, "step 1")
	assert.Equal(t, 100-20, score)
}

func TestComputePlanHealth_StalledAndBehindScheduleAreMutuallyExclusive(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	plan := config.PurchasePlan{
		Enabled: true,
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 7,
			CurrentStep:      0,
			TotalSteps:       6,
			StartDate:        now.AddDate(0, 0, -30),
		},
	}

	_, factors := computePlanHealth(plan, now, nil)

	codes := factorCodes(factors)
	assert.Contains(t, codes, HealthFactorStalled)
	assert.NotContains(t, codes, HealthFactorBehindSchedule)
}

func TestComputePlanHealth_ImmediatePlanSkipsScheduleFactors(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	plan := config.PurchasePlan{
		Enabled: true,
		RampSchedule: config.RampSchedule{
			Type:             "immediate",
			StepIntervalDays: 0, // no positive interval: nothing to fall behind on
			CurrentStep:      0,
			TotalSteps:       1,
			StartDate:        now.AddDate(0, 0, -30),
		},
	}

	_, factors := computePlanHealth(plan, now, nil)

	codes := factorCodes(factors)
	assert.NotContains(t, codes, HealthFactorStalled)
	assert.NotContains(t, codes, HealthFactorBehindSchedule)
}

func TestComputePlanHealth_DisabledMidway(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	plan := config.PurchasePlan{
		Enabled: false,
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 7,
			CurrentStep:      2,
			TotalSteps:       4,
			StartDate:        now.AddDate(0, 0, -1),
		},
	}

	score, factors := computePlanHealth(plan, now, nil)

	require.Len(t, factors, 1)
	assert.Equal(t, HealthFactorDisabledMidway, factors[0].Code)
	assert.Equal(t, 25, factors[0].Penalty)
	assert.Equal(t, 100-25, score)
}

func TestComputePlanHealth_DisabledButNeverStartedIsBehindScheduleNotDisabledMidway(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	plan := config.PurchasePlan{
		Enabled: false,
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 7,
			CurrentStep:      0,
			TotalSteps:       4,
			StartDate:        now.AddDate(0, 0, -30),
		},
	}

	_, factors := computePlanHealth(plan, now, nil)

	codes := factorCodes(factors)
	assert.Contains(t, codes, HealthFactorBehindSchedule)
	assert.NotContains(t, codes, HealthFactorDisabledMidway)
	assert.NotContains(t, codes, HealthFactorStalled)
}

func TestComputePlanHealth_ScoreClampsAtZeroWhenPenaltiesStack(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	past := now.AddDate(0, 0, -1)
	plan := config.PurchasePlan{
		Enabled:           false,
		NextExecutionDate: &past,
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 7,
			CurrentStep:      1, // way behind the expected step
			TotalSteps:       10,
			StartDate:        now.AddDate(0, 0, -90),
		},
	}
	var executions []config.PurchaseExecution
	for i := 0; i < 6; i++ {
		executions = append(executions, config.PurchaseExecution{Status: "failed"})
	}
	for i := 0; i < 6; i++ {
		executions = append(executions, config.PurchaseExecution{Status: config.StatusCanceled})
	}

	score, factors := computePlanHealth(plan, now, executions)

	assert.Equal(t, 0, score)
	// disabled_midway note: overdue only requires Enabled, which is false
	// here, so overdue must NOT fire even though NextExecutionDate is past.
	assert.NotContains(t, factorCodes(factors), HealthFactorOverdue)
	assert.Contains(t, factorCodes(factors), HealthFactorDisabledMidway)
	assert.Contains(t, factorCodes(factors), HealthFactorBehindSchedule)
	assert.Contains(t, factorCodes(factors), HealthFactorFailedExecutions)
	assert.Contains(t, factorCodes(factors), HealthFactorCanceledExecutions)
}

func TestComputePlanHealth_UnrelatedExecutionStatusesAreNotCounted(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	plan := config.PurchasePlan{
		Enabled: true,
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 30,
			CurrentStep:      1,
			TotalSteps:       4,
			StartDate:        now.AddDate(0, 0, -1),
		},
	}
	executions := []config.PurchaseExecution{
		{Status: "pending"}, {Status: "notified"}, {Status: "completed"}, {Status: "approved"},
	}

	score, factors := computePlanHealth(plan, now, executions)

	assert.Equal(t, 100, score)
	assert.Empty(t, factors)
}
