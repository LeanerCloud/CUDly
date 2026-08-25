package recfilter

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureLogf returns a Logf that appends every formatted line to *log, for
// tests asserting on warning/info output without touching a global logger.
func captureLogf(log *[]string) Logf {
	return func(format string, args ...any) {
		*log = append(*log, fmt.Sprintf(format, args...))
	}
}

// --- T4: ApplyCoverage ---

// TestApplyCoverage_DiscreteRatioRegression is the most important test in
// this file. Count=3 at coverage=50% must scale money fields to 1/3, NOT
// 0.5: newCount = int(3*0.5) = 1, so the DISCRETE ratio (1/3) governs cost
// scaling, not the requested ratio (0.5). Regressing to the requested ratio
// scales money by 0.5 while Count truncates to 1, overstating the sized
// purchase's cost by ~50% relative to what one instance actually costs.
func TestApplyCoverage_DiscreteRatioRegression(t *testing.T) {
	t.Parallel()
	rec := common.Recommendation{
		Service:           common.ServiceEC2,
		Count:             3,
		CommitmentCost:    300,
		OnDemandCost:      600,
		EstimatedSavings:  300,
		SavingsPercentage: 50,
	}

	out := ApplyCoverage([]common.Recommendation{rec}, 50, nil, nil)

	require.Len(t, out, 1)
	assert.Equal(t, 1, out[0].Count)
	assert.InDelta(t, 100.0, out[0].CommitmentCost, 0.001, "1/3 of 300, not 0.5*300=150")
	assert.InDelta(t, 200.0, out[0].OnDemandCost, 0.001, "1/3 of 600, not 0.5*600=300")
	assert.InDelta(t, 100.0, out[0].EstimatedSavings, 0.001, "1/3 of 300, not 0.5*300=150")
}

func TestApplyCoverage_HundredOrAbove_ReturnsUnchanged(t *testing.T) {
	t.Parallel()
	recs := []common.Recommendation{
		{Service: common.ServiceEC2, Count: 7, CommitmentCost: 111},
		{Service: common.ServiceSavingsPlansCompute, Details: &common.SavingsPlanDetails{HourlyCommitment: 4}},
	}

	for _, coverage := range []float64{100, 150} {
		out := ApplyCoverage(recs, coverage, nil, nil)
		require.Len(t, out, len(recs))
		assert.Equal(t, recs, out, "coverage=%.0f must return input unchanged", coverage)
	}
}

func TestApplyCoverage_ZeroOrBelow_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	recs := []common.Recommendation{{Service: common.ServiceEC2, Count: 5}}

	for _, coverage := range []float64{0, -5} {
		out := ApplyCoverage(recs, coverage, nil, nil)
		require.NotNil(t, out, "coverage=%.0f must return a non-nil empty slice", coverage)
		assert.Len(t, out, 0)
	}
}

func TestApplyCoverage_SavingsPlan_ScalesHourlyCommitment_NotCount(t *testing.T) {
	t.Parallel()
	rec := common.Recommendation{
		Service:          common.ServiceSavingsPlansCompute,
		Count:            5,
		CommitmentCost:   1000,
		OnDemandCost:     2000,
		EstimatedSavings: 1000,
		Details:          &common.SavingsPlanDetails{HourlyCommitment: 10},
	}

	out := ApplyCoverage([]common.Recommendation{rec}, 50, nil, nil)

	require.Len(t, out, 1)
	assert.Equal(t, 5, out[0].Count, "SP branch never touches Count")
	assert.InDelta(t, 5.0, out[0].Details.(*common.SavingsPlanDetails).HourlyCommitment, 0.001)
	assert.InDelta(t, 500.0, out[0].CommitmentCost, 0.001)
}

func TestApplyCoverage_SPBadDetails_PassThroughUnscaled_WarnsOnce(t *testing.T) {
	t.Parallel()

	t.Run("wrong type", func(t *testing.T) {
		t.Parallel()
		var logs []string
		rec := common.Recommendation{
			Service:          common.ServiceSavingsPlansCompute,
			EstimatedSavings: 1500,
			Details:          common.ComputeDetails{Platform: "Linux/UNIX"},
		}
		out := ApplyCoverage([]common.Recommendation{rec}, 50, captureLogf(&logs), nil)
		require.Len(t, out, 1)
		assert.Equal(t, 1500.0, out[0].EstimatedSavings, "unscaled")
		assert.Len(t, logs, 1, "exactly one warning")
	})

	t.Run("typed nil Details", func(t *testing.T) {
		t.Parallel()
		var logs []string
		rec := common.Recommendation{
			Service:          common.ServiceSavingsPlansCompute,
			EstimatedSavings: 1500,
			Details:          (*common.SavingsPlanDetails)(nil),
		}
		// An interface holding a typed nil satisfies the type assertion with
		// ok==true, so this case exercises the `ok && details != nil` guard
		// specifically — a plain `ok` check would send this down the
		// scaling path and panic on the nil dereference.
		out := ApplyCoverage([]common.Recommendation{rec}, 50, captureLogf(&logs), nil)
		require.Len(t, out, 1)
		assert.Equal(t, 1500.0, out[0].EstimatedSavings, "unscaled")
		assert.Len(t, logs, 1, "exactly one warning")
	})
}

func TestApplyCoverage_RISizedToZero_DropsAndRecords(t *testing.T) {
	t.Parallel()
	rec := common.Recommendation{Service: common.ServiceEC2, Count: 1, CommitmentCost: 100}

	t.Run("with drops summary", func(t *testing.T) {
		t.Parallel()
		d := common.NewDropSummary()
		out := ApplyCoverage([]common.Recommendation{rec}, 10, nil, d)
		assert.Empty(t, out)
		assert.Equal(t, 1, d.Total())
		assert.Contains(t, d.FormatOneLine(), common.DropTargetSizedToZero)
	})

	t.Run("with nil drops", func(t *testing.T) {
		t.Parallel()
		assert.NotPanics(t, func() {
			out := ApplyCoverage([]common.Recommendation{rec}, 10, nil, nil)
			assert.Empty(t, out)
		})
	})
}

func TestApplyCoverage_NilLogfSafe(t *testing.T) {
	t.Parallel()
	rec := common.Recommendation{
		Service: common.ServiceSavingsPlansCompute,
		Details: common.ComputeDetails{Platform: "Linux/UNIX"}, // wrong type -> warning path
	}
	assert.NotPanics(t, func() {
		ApplyCoverage([]common.Recommendation{rec}, 50, nil, nil)
	})
}

// --- T5: ApplyTargetCoverage ---

func mkRI(count int, avg, existingCov float64) common.Recommendation {
	return common.Recommendation{
		Service:                     common.ServiceEC2,
		Region:                      "us-east-1",
		ResourceType:                "t3.medium",
		Count:                       count,
		CommitmentType:              common.CommitmentReservedInstance,
		CommitmentCost:              1000,
		OnDemandCost:                2000,
		EstimatedSavings:            500,
		AverageInstancesUsedPerHour: avg,
		ExistingCoveragePct:         existingCov,
	}
}

func mkSP(recUtil, hourlyCommitment float64) common.Recommendation {
	return common.Recommendation{
		Service:                common.ServiceSavingsPlansCompute,
		CommitmentType:         common.CommitmentSavingsPlan,
		CommitmentCost:         1000,
		OnDemandCost:           5000,
		EstimatedSavings:       1500,
		RecommendedUtilization: recUtil,
		Details:                &common.SavingsPlanDetails{HourlyCommitment: hourlyCommitment},
	}
}

func TestApplyTargetCoverage_GapAlreadyMet_Drops(t *testing.T) {
	t.Parallel()
	rec := mkRI(5, 8.0, 90) // existing=90 >= target=80
	d := common.NewDropSummary()
	out := ApplyTargetCoverage([]common.Recommendation{rec}, 80, nil, d)
	assert.Empty(t, out)
	assert.Equal(t, 1, d.Total())
	assert.Contains(t, d.FormatOneLine(), common.DropTargetAlreadyMet)
}

func TestApplyTargetCoverage_FloorSizesToZero_Drops(t *testing.T) {
	t.Parallel()
	// avg=0.5, target=80, existing=0: floor(0.5*80/100)=floor(0.4)=0.
	rec := mkRI(1, 0.5, 0)
	d := common.NewDropSummary()
	out := ApplyTargetCoverage([]common.Recommendation{rec}, 80, nil, d)
	assert.Empty(t, out)
	assert.Equal(t, 1, d.Total())
	assert.Contains(t, d.FormatOneLine(), common.DropTargetSizedToZero)
}

func TestApplyTargetCoverage_NoSignal_PassesThroughUnchanged(t *testing.T) {
	t.Parallel()
	rec := mkRI(5, 0, 0) // AverageInstancesUsedPerHour <= 0 -> no signal
	d := common.NewDropSummary()
	out := ApplyTargetCoverage([]common.Recommendation{rec}, 80, nil, d)
	require.Len(t, out, 1)
	assert.Equal(t, rec, out[0], "passed through unchanged")
	assert.True(t, d.IsEmpty(), "no-signal is not a drop")
}

// TestApplyTargetCoverage_ProjectionsClampTo100 covers the clamp on
// ProjectedUtilization (easy to overflow: avg/nTarget*100 grows unbounded as
// nTarget shrinks relative to avg) and documents why ProjectedCoverage's
// clamp is a defensive boundary check rather than a reachable overflow: floor
// guarantees nTarget/avg*100 <= gapPct, so projCov <= existing+gapPct ==
// target <= 100 in exact arithmetic. The target=100 boundary case below
// exercises that bound exactly.
func TestApplyTargetCoverage_ProjectionsClampTo100(t *testing.T) {
	t.Parallel()

	t.Run("ProjectedUtilization clamps", func(t *testing.T) {
		t.Parallel()
		// avg=100, target=10, existing=0: gap=10, nTarget=floor(100*10/100)=10.
		// Unclamped projUtil = 100/10*100 = 1000%.
		rec := mkRI(200, 100, 0)
		out := ApplyTargetCoverage([]common.Recommendation{rec}, 10, nil, nil)
		require.Len(t, out, 1)
		assert.Equal(t, 100.0, out[0].ProjectedUtilization)
	})

	t.Run("ProjectedCoverage stays at the target boundary", func(t *testing.T) {
		t.Parallel()
		// avg=10, target=100, existing=0: gap=100, nTarget=floor(10)=10.
		// projCov = 0 + 10/10*100 = 100.0 exactly at the clamp boundary.
		rec := mkRI(10, 10, 0)
		out := ApplyTargetCoverage([]common.Recommendation{rec}, 100, nil, nil)
		require.Len(t, out, 1)
		assert.LessOrEqual(t, out[0].ProjectedCoverage, 100.0)
		assert.Equal(t, 100.0, out[0].ProjectedCoverage)
	})
}

// TestApplyTargetCoverage_CountZeroNoNaNOrInf covers the rec.Count==0
// fallback (`ratio = float64(nTarget)` instead of nTarget/rec.Count) that
// guards against a division by zero producing NaN/Inf in every scaled money
// field.
func TestApplyTargetCoverage_CountZeroNoNaNOrInf(t *testing.T) {
	t.Parallel()
	monthly := 20.0
	rec := mkRI(0, 10, 0) // Count=0, avg=10, existing=0
	rec.RecurringMonthlyCost = &monthly

	out := ApplyTargetCoverage([]common.Recommendation{rec}, 80, nil, nil)

	require.Len(t, out, 1)
	got := out[0]
	for name, v := range map[string]float64{
		"CommitmentCost":       got.CommitmentCost,
		"OnDemandCost":         got.OnDemandCost,
		"EstimatedSavings":     got.EstimatedSavings,
		"RecurringMonthlyCost": *got.RecurringMonthlyCost,
		"ProjectedUtilization": got.ProjectedUtilization,
		"ProjectedCoverage":    got.ProjectedCoverage,
	} {
		assert.False(t, math.IsNaN(v), "%s is NaN", name)
		assert.False(t, math.IsInf(v, 0), "%s is Inf", name)
	}
}

func TestApplyTargetCoverage_SPEdgePassthroughs(t *testing.T) {
	t.Parallel()

	t.Run("RecommendedUtilization <= 0 passes through unchanged", func(t *testing.T) {
		t.Parallel()
		rec := mkSP(0, 2.0)
		out := ApplyTargetCoverage([]common.Recommendation{rec}, 80, nil, nil)
		require.Len(t, out, 1)
		assert.Equal(t, rec, out[0])
	})

	t.Run("HourlyCommitment <= 0 passes through unchanged", func(t *testing.T) {
		t.Parallel()
		rec := mkSP(50, 0)
		out := ApplyTargetCoverage([]common.Recommendation{rec}, 80, nil, nil)
		require.Len(t, out, 1)
		assert.Equal(t, rec, out[0])
	})

	t.Run("wrong-type Details passes through with warning, ProjectedUtilization stays zero", func(t *testing.T) {
		t.Parallel()
		var logs []string
		rec := mkSP(50, 2.0)
		rec.Details = common.ComputeDetails{Platform: "Linux/UNIX"}
		out := ApplyTargetCoverage([]common.Recommendation{rec}, 80, captureLogf(&logs), nil)
		require.Len(t, out, 1)
		assert.Equal(t, rec, out[0])
		assert.Equal(t, 0.0, out[0].ProjectedUtilization, "scaling failed, so projection must not be set")
		assert.Len(t, logs, 1)
	})

	t.Run("typed-nil Details passes through with warning, ProjectedUtilization stays zero", func(t *testing.T) {
		t.Parallel()
		var logs []string
		rec := mkSP(50, 2.0)
		rec.Details = (*common.SavingsPlanDetails)(nil)
		out := ApplyTargetCoverage([]common.Recommendation{rec}, 80, captureLogf(&logs), nil)
		require.Len(t, out, 1)
		assert.Equal(t, 0.0, out[0].ProjectedUtilization)
		assert.Len(t, logs, 1)
	})
}

// TestApplyTargetCoverage_UnsupportedType_WarnsOncePerType covers a slice
// containing several recs of the same unsupported CommitmentType: the
// warning must fire exactly once per distinct type across the whole slice,
// not once per rec, and every rec passes through unchanged regardless.
func TestApplyTargetCoverage_UnsupportedType_WarnsOncePerType(t *testing.T) {
	t.Parallel()
	var logs []string
	recs := []common.Recommendation{
		{Service: common.ServiceCompute, CommitmentType: common.CommitmentCUD, Count: 1},
		{Service: common.ServiceCompute, CommitmentType: common.CommitmentCUD, Count: 2},
		{Service: common.ServiceCompute, CommitmentType: common.CommitmentReservedCapacity, Count: 3},
	}

	out := ApplyTargetCoverage(recs, 80, captureLogf(&logs), nil)

	require.Len(t, out, 3)
	assert.Equal(t, recs, out, "unsupported types pass through unchanged")

	cudWarnings, capacityWarnings := 0, 0
	for _, l := range logs {
		switch {
		case strings.Contains(l, string(common.CommitmentCUD)):
			cudWarnings++
		case strings.Contains(l, string(common.CommitmentReservedCapacity)):
			capacityWarnings++
		}
	}
	assert.Equal(t, 1, cudWarnings, "CommitmentCUD warns exactly once despite 2 recs")
	assert.Equal(t, 1, capacityWarnings, "CommitmentReservedCapacity warns exactly once")
}

func TestApplyTargetCoverage_TargetPctOutOfRange_PassesThroughWithWarning(t *testing.T) {
	t.Parallel()
	recs := []common.Recommendation{mkRI(5, 8, 0)}

	for _, targetPct := range []float64{0, -1, 101} {
		var logs []string
		out := ApplyTargetCoverage(recs, targetPct, captureLogf(&logs), nil)
		assert.Equal(t, recs, out, "targetPct=%.0f", targetPct)
		assert.Len(t, logs, 1, "targetPct=%.0f", targetPct)
	}
}

func TestApplyTargetCoverage_NilLogfSafe(t *testing.T) {
	t.Parallel()
	recs := []common.Recommendation{
		mkRI(5, 8, 90),  // gapPct <= 0 -> INFO log
		mkRI(1, 0.5, 0), // sized to zero -> INFO log
		mkRI(5, 0, 0),   // no signal, no log
		mkSP(50, 0),     // no signal, no log
		{Service: common.ServiceCompute, CommitmentType: common.CommitmentCUD}, // unsupported -> WARNING log
	}
	assert.NotPanics(t, func() {
		ApplyTargetCoverage(recs, 80, nil, nil)
	})
	assert.NotPanics(t, func() {
		ApplyTargetCoverage(recs, 0, nil, nil) // out-of-range -> WARNING log
	})
}
