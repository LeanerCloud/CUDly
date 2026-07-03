package ladder

import (
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// --- test helpers ---

// seqID returns a function that generates IDs sequentially: prefix-1, prefix-2, ...
func seqID(prefix string) func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("%s-%d", prefix, n)
	}
}

// baseConfig returns a valid LadderConfig using the provided ramp schedule.
func baseConfig(ramp RampSchedule) *LadderConfig {
	return &LadderConfig{
		Scope: Scope{
			Provider:  common.ProviderAWS,
			AccountID: "123456789012",
		},
		Mode:                          ModeEmailApproval,
		Cadence:                       CadenceWeekly,
		Ramp:                          ramp,
		TargetCoveragePct:             100,
		BufferFraction:                0,
		BaselinePercentile:            5,
		LookbackDays:                  30,
		MaxActionsPerRun:              20,
		BufferUtilizationThresholdPct: DefaultBufferUtilizationThresholdPct,
	}
}

// singleStepRamp returns a ramp with one immediate step covering 100% of the gap.
func singleStepRamp() RampSchedule {
	return RampSchedule{Steps: []RampStep{{AfterDays: 0, Fraction: 1.0}}}
}

// threeStepRamp returns a ramp with AfterDays 0/30/60 and fractions 0.4/0.3/0.3.
func threeStepRamp() RampSchedule {
	return RampSchedule{Steps: []RampStep{
		{AfterDays: 0, Fraction: 0.4},
		{AfterDays: 30, Fraction: 0.3},
		{AfterDays: 60, Fraction: 0.3},
	}}
}

// delayedRamp returns a ramp with all steps delayed (no AfterDays == 0).
func delayedRamp() RampSchedule {
	return RampSchedule{Steps: []RampStep{
		{AfterDays: 30, Fraction: 0.5},
		{AfterDays: 60, Fraction: 0.5},
	}}
}

// inexactFracRamp returns a ramp with 0.33/0.33/0.34 fractions (inexact in float64).
func inexactFracRamp() RampSchedule {
	return RampSchedule{Steps: []RampStep{
		{AfterDays: 0, Fraction: 0.33},
		{AfterDays: 30, Fraction: 0.33},
		{AfterDays: 60, Fraction: 0.34},
	}}
}

// mkAlloc builds an Allocation with the given layer and a whole-dollar hourly
// gap as an exact rational.
func mkAlloc(layer LayerType, gapUSD int64) Allocation {
	return Allocation{
		Layer:         layer,
		GapUSDPerHour: new(big.Rat).SetInt64(gapUSD),
		Rationale:     fmt.Sprintf("test rationale for %s", layer),
		DataSources:   []string{"test-source"},
	}
}

// baseInput builds a minimal valid TrancheInput for the given config and allocations.
func baseInput(cfg *LadderConfig, allocs []Allocation, now time.Time) *TrancheInput {
	return &TrancheInput{
		Config:        cfg,
		Allocations:   allocs,
		RunID:         "run-abc",
		Term:          "1yr",
		PaymentOption: "no-upfront",
		Now:           now,
		NewID:         seqID("tr"),
	}
}

// totalAmount sums all amounts across BuyNow actions and Tranches. Panics if
// a tranche AmountUSDPerHour fails to parse (the test has already verified
// Validate passes, so this indicates a bug in the test helper).
func totalAmount(result *TrancheResult) *big.Rat {
	total := new(big.Rat)
	for _, a := range result.BuyNow {
		total.Add(total, a.AmountUSDPerHour)
	}
	for _, tr := range result.Tranches {
		r := new(big.Rat)
		if _, ok := r.SetString(tr.AmountUSDPerHour); !ok {
			panic(fmt.Sprintf("totalAmount: cannot parse tranche amount %q", tr.AmountUSDPerHour))
		}
		total.Add(total, r)
	}
	return total
}

// assertAllValid calls Validate on every produced action and tranche, reporting
// failures with t.Errorf.
func assertAllValid(t *testing.T, result *TrancheResult) {
	t.Helper()
	for i, a := range result.BuyNow {
		if err := a.Validate(); err != nil {
			t.Errorf("BuyNow[%d].Validate() = %v", i, err)
		}
	}
	for i, tr := range result.Tranches {
		if err := tr.Validate(); err != nil {
			t.Errorf("Tranches[%d].Validate() = %v", i, err)
		}
	}
}

// --- tests ---

// TestBuildTranches_ThreeStepRamp verifies the standard 3-step ramp
// (AfterDays 0/30/60, fractions 0.4/0.3/0.3) over a $5/hr allocation.
// The buy-now step uses the immediate fraction; the two future tranches use the
// remainder logic. The total across all steps must reconstruct the gap exactly.
func TestBuildTranches_ThreeStepRamp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	gap := new(big.Rat).SetInt64(5) // exactly $5/hr

	in := baseInput(baseConfig(threeStepRamp()), []Allocation{
		{
			Layer:         LayerComputeSP,
			GapUSDPerHour: gap,
			Rationale:     "flex layer gap",
			DataSources:   []string{"cost-explorer"},
		},
	}, now)

	result, err := BuildTranches(in)
	if err != nil {
		t.Fatalf("BuildTranches() error = %v", err)
	}

	// Immediate step (AfterDays == 0, fraction 0.4): must be positive.
	// We assert the total instead of the exact step value because 0.4 is not
	// exactly representable in float64; the amount is gap * SetFloat64(0.4),
	// which is close but not equal to the rational 2/5.
	if len(result.BuyNow) != 1 {
		t.Fatalf("BuyNow count = %d, want 1", len(result.BuyNow))
	}
	if result.BuyNow[0].AmountUSDPerHour.Sign() <= 0 {
		t.Errorf("BuyNow[0].AmountUSDPerHour must be positive, got %s",
			result.BuyNow[0].AmountUSDPerHour.RatString())
	}

	// Two future tranches for days 30 and 60.
	if len(result.Tranches) != 2 {
		t.Fatalf("Tranches count = %d, want 2", len(result.Tranches))
	}
	wantDay30 := now.AddDate(0, 0, 30)
	if !result.Tranches[0].FireAfter.Equal(wantDay30) {
		t.Errorf("Tranches[0].FireAfter = %v, want %v", result.Tranches[0].FireAfter, wantDay30)
	}
	wantDay60 := now.AddDate(0, 0, 60)
	if !result.Tranches[1].FireAfter.Equal(wantDay60) {
		t.Errorf("Tranches[1].FireAfter = %v, want %v", result.Tranches[1].FireAfter, wantDay60)
	}

	// Grand total must reconstruct the gap exactly.
	got := totalAmount(result)
	if got.Cmp(gap) != 0 {
		t.Errorf("total amount = %s, want %s (gap not reconstructed exactly)",
			got.RatString(), gap.RatString())
	}

	// RunID stamped on all tranches.
	for i, tr := range result.Tranches {
		if tr.RunID != in.RunID {
			t.Errorf("Tranches[%d].RunID = %q, want %q", i, tr.RunID, in.RunID)
		}
		if tr.Status != TrancheStatusScheduled {
			t.Errorf("Tranches[%d].Status = %q, want %q", i, tr.Status, TrancheStatusScheduled)
		}
	}

	assertAllValid(t, result)
}

// TestBuildTranches_FullyDelayedRamp verifies that a ramp with no AfterDays == 0
// step produces zero buy-now actions. All commitment activity is deferred.
func TestBuildTranches_FullyDelayedRamp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	gap := new(big.Rat).SetFrac64(10, 1)

	in := baseInput(baseConfig(delayedRamp()), []Allocation{
		{
			Layer:         LayerComputeSP,
			GapUSDPerHour: gap,
			Rationale:     "delayed ramp test",
			DataSources:   []string{"cost-explorer"},
		},
	}, now)

	result, err := BuildTranches(in)
	if err != nil {
		t.Fatalf("BuildTranches() error = %v", err)
	}

	if len(result.BuyNow) != 0 {
		t.Errorf("BuyNow count = %d, want 0 (fully delayed ramp)", len(result.BuyNow))
	}
	if len(result.Tranches) != 2 {
		t.Fatalf("Tranches count = %d, want 2", len(result.Tranches))
	}

	// Grand total must still equal the gap exactly.
	got := totalAmount(result)
	if got.Cmp(gap) != 0 {
		t.Errorf("total = %s, want %s", got.RatString(), gap.RatString())
	}
	assertAllValid(t, result)
}

// TestBuildTranches_SingleStep verifies a single-step ramp (fraction 1.0,
// AfterDays 0): everything becomes a buy-now action with no tranches.
func TestBuildTranches_SingleStep(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	gap := new(big.Rat).SetFrac64(7, 2) // $3.50/hr

	in := baseInput(baseConfig(singleStepRamp()), []Allocation{
		{
			Layer:         LayerComputeSP,
			GapUSDPerHour: gap,
			Rationale:     "single step test",
			DataSources:   []string{"cost-explorer"},
		},
	}, now)

	result, err := BuildTranches(in)
	if err != nil {
		t.Fatalf("BuildTranches() error = %v", err)
	}

	if len(result.BuyNow) != 1 {
		t.Fatalf("BuyNow count = %d, want 1", len(result.BuyNow))
	}
	if len(result.Tranches) != 0 {
		t.Errorf("Tranches count = %d, want 0", len(result.Tranches))
	}
	if result.BuyNow[0].AmountUSDPerHour.Cmp(gap) != 0 {
		t.Errorf("BuyNow[0].Amount = %s, want %s",
			result.BuyNow[0].AmountUSDPerHour.RatString(), gap.RatString())
	}
	assertAllValid(t, result)
}

// TestBuildTranches_MultipleAllocations verifies that multiple allocations
// (base, flex, buffer) are each split correctly and that running BuildTranches
// twice with identical deterministic inputs produces identical output order and
// amounts.
func TestBuildTranches_MultipleAllocations(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	cfg := baseConfig(threeStepRamp())

	allocs := []Allocation{
		mkAlloc(LayerEC2InstanceSP, 3), // $3/hr base
		mkAlloc(LayerComputeSP, 5),     // $5/hr flex
		mkAlloc(LayerConvertibleRI, 2), // $2/hr buffer
	}

	buildOnce := func() *TrancheResult {
		in := &TrancheInput{
			Config:        cfg,
			Allocations:   allocs,
			RunID:         "run-multi",
			Term:          "1yr",
			PaymentOption: "no-upfront",
			Now:           now,
			NewID:         seqID("id"),
		}
		res, err := BuildTranches(in)
		if err != nil {
			t.Fatalf("BuildTranches() error = %v", err)
		}
		return res
	}

	first := buildOnce()
	second := buildOnce()

	// Three allocations x one buy-now step = three buy-now actions.
	if len(first.BuyNow) != 3 {
		t.Errorf("BuyNow count = %d, want 3", len(first.BuyNow))
	}
	// Three allocations x two future steps = six tranches.
	if len(first.Tranches) != 6 {
		t.Errorf("Tranches count = %d, want 6", len(first.Tranches))
	}

	// Determinism: lengths must match first (a shorter second run must fail
	// loudly, not pass silently on the shared prefix), then per-element
	// amounts must match between runs.
	if len(first.BuyNow) != len(second.BuyNow) {
		t.Fatalf("BuyNow lengths differ between runs: %d vs %d", len(first.BuyNow), len(second.BuyNow))
	}
	if len(first.Tranches) != len(second.Tranches) {
		t.Fatalf("Tranches lengths differ between runs: %d vs %d", len(first.Tranches), len(second.Tranches))
	}
	for i := range first.BuyNow {
		a1 := first.BuyNow[i].AmountUSDPerHour
		a2 := second.BuyNow[i].AmountUSDPerHour
		if a1.Cmp(a2) != 0 {
			t.Errorf("BuyNow[%d] amounts differ between runs: %s vs %s", i, a1.RatString(), a2.RatString())
		}
	}
	for i := range first.Tranches {
		if first.Tranches[i].AmountUSDPerHour != second.Tranches[i].AmountUSDPerHour {
			t.Errorf("Tranches[%d] amounts differ: %s vs %s",
				i, first.Tranches[i].AmountUSDPerHour, second.Tranches[i].AmountUSDPerHour)
		}
	}

	// Grand total must reconstruct the sum of all gaps exactly.
	// Output ordering: processAllocation iterates allocations in slice order;
	// within each allocation steps run in schedule order. So for 3 allocs and
	// 3 steps (day0/day30/day60):
	//   BuyNow: [alloc0-step0, alloc1-step0, alloc2-step0]
	//   Tranches: [alloc0-step1, alloc0-step2, alloc1-step1, alloc1-step2, alloc2-step1, alloc2-step2]
	wantGaps := []*big.Rat{
		new(big.Rat).SetFrac64(3, 1),
		new(big.Rat).SetFrac64(5, 1),
		new(big.Rat).SetFrac64(2, 1),
	}
	wantTotal := new(big.Rat).SetFrac64(10, 1) // 3 + 5 + 2
	got := totalAmount(first)
	if got.Cmp(wantTotal) != 0 {
		t.Errorf("total amount = %s, want %s", got.RatString(), wantTotal.RatString())
	}

	// Per-allocation totals: alloc[i] occupies BuyNow[i] and Tranches[i*2], Tranches[i*2+1].
	for i, wantGap := range wantGaps {
		buyNowAmt := first.BuyNow[i].AmountUSDPerHour
		tr1Rat := new(big.Rat)
		tr2Rat := new(big.Rat)
		// errcheck excluded for _test.go; Validate already confirmed these parse.
		tr1Rat.SetString(first.Tranches[i*2].AmountUSDPerHour)
		tr2Rat.SetString(first.Tranches[i*2+1].AmountUSDPerHour)
		layerTotal := new(big.Rat).Add(buyNowAmt, new(big.Rat).Add(tr1Rat, tr2Rat))
		if layerTotal.Cmp(wantGap) != 0 {
			t.Errorf("allocation[%d] total = %s, want %s (gap not reconstructed)",
				i, layerTotal.RatString(), wantGap.RatString())
		}
	}

	assertAllValid(t, first)
}

// TestBuildTranches_InexactFractionRemainder verifies that when step fractions
// are not exactly representable in float64 (e.g. 0.33/0.33/0.34), the last
// step's remainder ensures the grand total reconstructs the gap exactly.
func TestBuildTranches_InexactFractionRemainder(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	// Use a gap of exactly $5/hr expressed as 5/1.
	gap := new(big.Rat).SetInt64(5)

	in := baseInput(baseConfig(inexactFracRamp()), []Allocation{
		{
			Layer:         LayerComputeSP,
			GapUSDPerHour: gap,
			Rationale:     "inexact fraction test",
			DataSources:   []string{"test"},
		},
	}, now)

	result, err := BuildTranches(in)
	if err != nil {
		t.Fatalf("BuildTranches() error = %v", err)
	}

	// 1 buy-now (step 0, AfterDays==0) + 2 tranches (steps 1 and 2).
	if len(result.BuyNow) != 1 {
		t.Errorf("BuyNow count = %d, want 1", len(result.BuyNow))
	}
	if len(result.Tranches) != 2 {
		t.Errorf("Tranches count = %d, want 2", len(result.Tranches))
	}

	// Grand total must be exactly $5 regardless of float64 fraction imprecision.
	got := totalAmount(result)
	if got.Cmp(gap) != 0 {
		t.Errorf("total = %s, want 5/1 (remainder did not reconstruct gap exactly)",
			got.RatString())
	}
	assertAllValid(t, result)
}

// TestBuildTranches_TinyGapExactness verifies that even for very small gap
// values (e.g. $0.001/hr) the total across all steps reconstructs the gap
// exactly. With exact big.Rat arithmetic and positive fractions, step amounts
// are never zero for a positive gap.
func TestBuildTranches_TinyGapExactness(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// $1/1000 per hour = $0.001/hr, smaller than the $0.01 min-allocatable
	// threshold but valid as a raw amount for BuildTranches (threshold is
	// enforced by Allocate, not BuildTranches).
	gap := new(big.Rat).SetFrac64(1, 1000)

	in := baseInput(baseConfig(inexactFracRamp()), []Allocation{
		{
			Layer:         LayerComputeSP,
			GapUSDPerHour: gap,
			Rationale:     "tiny gap test",
			DataSources:   []string{"test"},
		},
	}, now)

	result, err := BuildTranches(in)
	if err != nil {
		t.Fatalf("BuildTranches() error = %v", err)
	}

	got := totalAmount(result)
	if got.Cmp(gap) != 0 {
		t.Errorf("tiny gap total = %s, want %s", got.RatString(), gap.RatString())
	}
	assertAllValid(t, result)
}

// TestBuildTranches_IDsStamped verifies that produced tranches carry the IDs
// returned by NewID in call order, and that the RunID from the input is
// stamped on every tranche.
func TestBuildTranches_IDsStamped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	in := &TrancheInput{
		Config: baseConfig(delayedRamp()),
		Allocations: []Allocation{
			mkAlloc(LayerComputeSP, 4),
			mkAlloc(LayerConvertibleRI, 2),
		},
		RunID:         "run-id-stamp-test",
		Term:          "1yr",
		PaymentOption: "no-upfront",
		Now:           now,
		NewID:         seqID("myid"),
	}

	result, err := BuildTranches(in)
	if err != nil {
		t.Fatalf("BuildTranches() error = %v", err)
	}

	// delayedRamp has 2 steps, 2 allocations -> 4 tranches total.
	if len(result.Tranches) != 4 {
		t.Fatalf("Tranches count = %d, want 4", len(result.Tranches))
	}
	// IDs should be myid-1, myid-2, myid-3, myid-4 in order.
	wantIDs := []string{"myid-1", "myid-2", "myid-3", "myid-4"}
	for i, tr := range result.Tranches {
		if tr.ID != wantIDs[i] {
			t.Errorf("Tranches[%d].ID = %q, want %q", i, tr.ID, wantIDs[i])
		}
		if tr.RunID != in.RunID {
			t.Errorf("Tranches[%d].RunID = %q, want %q", i, tr.RunID, in.RunID)
		}
	}
	assertAllValid(t, result)
}

// TestBuildTranches_ValidationFailures exercises the fail-loud validation path
// for malformed TrancheInput structs.
func TestBuildTranches_ValidationFailures(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	goodConfig := baseConfig(singleStepRamp())
	goodAlloc := mkAlloc(LayerComputeSP, 5)

	cases := []struct {
		build func() *TrancheInput
		name  string
	}{
		{
			name:  "nil input",
			build: func() *TrancheInput { return nil },
		},
		{
			name: "nil config",
			build: func() *TrancheInput {
				in := baseInput(goodConfig, []Allocation{goodAlloc}, now)
				in.Config = nil
				return in
			},
		},
		{
			name: "empty RunID",
			build: func() *TrancheInput {
				in := baseInput(goodConfig, []Allocation{goodAlloc}, now)
				in.RunID = ""
				return in
			},
		},
		{
			name: "zero Now",
			build: func() *TrancheInput {
				in := baseInput(goodConfig, []Allocation{goodAlloc}, now)
				in.Now = time.Time{}
				return in
			},
		},
		{
			name: "nil NewID",
			build: func() *TrancheInput {
				in := baseInput(goodConfig, []Allocation{goodAlloc}, now)
				in.NewID = nil
				return in
			},
		},
		{
			name: "empty Term",
			build: func() *TrancheInput {
				in := baseInput(goodConfig, []Allocation{goodAlloc}, now)
				in.Term = ""
				return in
			},
		},
		{
			name: "empty PaymentOption",
			build: func() *TrancheInput {
				in := baseInput(goodConfig, []Allocation{goodAlloc}, now)
				in.PaymentOption = ""
				return in
			},
		},
		{
			name: "allocation with invalid layer",
			build: func() *TrancheInput {
				bad := Allocation{
					Layer:         LayerType("bogus-layer"),
					GapUSDPerHour: new(big.Rat).SetInt64(1),
					Rationale:     "x",
				}
				return baseInput(goodConfig, []Allocation{bad}, now)
			},
		},
		{
			name: "allocation with nil gap",
			build: func() *TrancheInput {
				bad := Allocation{
					Layer:         LayerComputeSP,
					GapUSDPerHour: nil,
					Rationale:     "x",
				}
				return baseInput(goodConfig, []Allocation{bad}, now)
			},
		},
		{
			name: "allocation with zero gap",
			build: func() *TrancheInput {
				bad := Allocation{
					Layer:         LayerComputeSP,
					GapUSDPerHour: new(big.Rat),
					Rationale:     "x",
				}
				return baseInput(goodConfig, []Allocation{bad}, now)
			},
		},
		{
			name: "allocation with negative gap",
			build: func() *TrancheInput {
				bad := Allocation{
					Layer:         LayerComputeSP,
					GapUSDPerHour: new(big.Rat).SetInt64(-1),
					Rationale:     "x",
				}
				return baseInput(goodConfig, []Allocation{bad}, now)
			},
		},
		{
			name: "allocation with empty rationale",
			build: func() *TrancheInput {
				bad := Allocation{
					Layer:         LayerComputeSP,
					GapUSDPerHour: new(big.Rat).SetInt64(1),
					Rationale:     "",
				}
				return baseInput(goodConfig, []Allocation{bad}, now)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := BuildTranches(c.build())
			if err == nil {
				t.Errorf("BuildTranches() = nil error, want error for case %q", c.name)
			}
		})
	}
}

// TestBuildTranches_StepIndexStamped verifies that each tranche carries the
// correct StepIndex matching its position in the ramp schedule.
func TestBuildTranches_StepIndexStamped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	ramp := RampSchedule{Steps: []RampStep{
		{AfterDays: 10, Fraction: 0.5},
		{AfterDays: 20, Fraction: 0.3},
		{AfterDays: 30, Fraction: 0.2},
	}}
	in := baseInput(baseConfig(ramp), []Allocation{
		mkAlloc(LayerComputeSP, 6),
	}, now)

	result, err := BuildTranches(in)
	if err != nil {
		t.Fatalf("BuildTranches() error = %v", err)
	}

	if len(result.BuyNow) != 0 {
		t.Errorf("BuyNow count = %d, want 0 (all steps delayed)", len(result.BuyNow))
	}
	if len(result.Tranches) != 3 {
		t.Fatalf("Tranches count = %d, want 3", len(result.Tranches))
	}
	for i, tr := range result.Tranches {
		if tr.StepIndex != i {
			t.Errorf("Tranches[%d].StepIndex = %d, want %d", i, tr.StepIndex, i)
		}
		wantFireAfter := now.AddDate(0, 0, ramp.Steps[i].AfterDays)
		if !tr.FireAfter.Equal(wantFireAfter) {
			t.Errorf("Tranches[%d].FireAfter = %v, want %v", i, tr.FireAfter, wantFireAfter)
		}
	}

	// Total must reconstruct gap exactly.
	got := totalAmount(result)
	want := new(big.Rat).SetInt64(6)
	if got.Cmp(want) != 0 {
		t.Errorf("total = %s, want %s", got.RatString(), want.RatString())
	}
	assertAllValid(t, result)
}

// TestBuildTranches_EmptyAllocations verifies that an empty allocations slice
// produces a valid empty result without error.
func TestBuildTranches_EmptyAllocations(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	in := baseInput(baseConfig(singleStepRamp()), nil, now)

	result, err := BuildTranches(in)
	if err != nil {
		t.Fatalf("BuildTranches() error = %v", err)
	}
	if len(result.BuyNow) != 0 {
		t.Errorf("BuyNow count = %d, want 0 for empty allocations", len(result.BuyNow))
	}
	if len(result.Tranches) != 0 {
		t.Errorf("Tranches count = %d, want 0 for empty allocations", len(result.Tranches))
	}
}

// TestBuildTranches_RationaleContents verifies that the rationale string on a
// buy-now action embeds the step index, step count, and the original allocation
// rationale.
func TestBuildTranches_RationaleContents(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	allocRationale := "flex layer: low_water=$5.0000/hr, gap=$5.0000/hr"
	in := &TrancheInput{
		Config: baseConfig(singleStepRamp()),
		Allocations: []Allocation{{
			Layer:         LayerComputeSP,
			GapUSDPerHour: new(big.Rat).SetInt64(5),
			Rationale:     allocRationale,
			DataSources:   []string{"cost-explorer"},
		}},
		RunID:         "run-rationale",
		Term:          "1yr",
		PaymentOption: "no-upfront",
		Now:           now,
		NewID:         seqID("r"),
	}

	result, err := BuildTranches(in)
	if err != nil {
		t.Fatalf("BuildTranches() error = %v", err)
	}
	if len(result.BuyNow) != 1 {
		t.Fatalf("BuyNow count = %d, want 1", len(result.BuyNow))
	}

	r := result.BuyNow[0].Rationale
	checks := []string{"ramp step 1/1", "100%", allocRationale}
	for _, want := range checks {
		if !strings.Contains(r, want) {
			t.Errorf("rationale %q missing expected substring %q", r, want)
		}
	}
}

// TestBuildTranches_DataSourcesPropagated verifies that the DataSources from
// each allocation are propagated verbatim to each buy-now PlannedAction.
func TestBuildTranches_DataSourcesPropagated(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	wantSources := []string{"cost-explorer", "cloudwatch"}

	in := baseInput(baseConfig(singleStepRamp()), []Allocation{{
		Layer:         LayerComputeSP,
		GapUSDPerHour: new(big.Rat).SetInt64(3),
		Rationale:     "ds test",
		DataSources:   wantSources,
	}}, now)

	result, err := BuildTranches(in)
	if err != nil {
		t.Fatalf("BuildTranches() error = %v", err)
	}
	if len(result.BuyNow) != 1 {
		t.Fatalf("BuyNow count = %d, want 1", len(result.BuyNow))
	}
	got := result.BuyNow[0].DataSources
	if len(got) != len(wantSources) {
		t.Fatalf("DataSources len = %d, want %d", len(got), len(wantSources))
	}
	for i, s := range wantSources {
		if got[i] != s {
			t.Errorf("DataSources[%d] = %q, want %q", i, got[i], s)
		}
	}
}

// TestBuildTranches_TranchesSelfDescribing verifies that two allocations with
// identical gaps on different layers produce tranches that are distinguishable
// by their Layer field, and that Term and PaymentOption are stamped on every
// tranche, so a fired tranche is executable without consulting the parent
// run's plan.
func TestBuildTranches_TranchesSelfDescribing(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	// Two allocations with the SAME gap on DIFFERENT layers: without the
	// Layer field these would produce identical tranche rows except for ID.
	in := baseInput(baseConfig(delayedRamp()), []Allocation{
		mkAlloc(LayerComputeSP, 4),
		mkAlloc(LayerConvertibleRI, 4),
	}, now)

	result, err := BuildTranches(in)
	if err != nil {
		t.Fatalf("BuildTranches() error = %v", err)
	}
	// delayedRamp has 2 delayed steps x 2 allocations = 4 tranches.
	if len(result.Tranches) != 4 {
		t.Fatalf("Tranches count = %d, want 4", len(result.Tranches))
	}

	// Allocation order is preserved: tranches 0,1 belong to LayerComputeSP,
	// tranches 2,3 to LayerConvertibleRI.
	wantLayers := []LayerType{LayerComputeSP, LayerComputeSP, LayerConvertibleRI, LayerConvertibleRI}
	for i, tr := range result.Tranches {
		if tr.Layer != wantLayers[i] {
			t.Errorf("Tranches[%d].Layer = %q, want %q", i, tr.Layer, wantLayers[i])
		}
		if tr.Term != in.Term {
			t.Errorf("Tranches[%d].Term = %q, want %q", i, tr.Term, in.Term)
		}
		if tr.PaymentOption != in.PaymentOption {
			t.Errorf("Tranches[%d].PaymentOption = %q, want %q", i, tr.PaymentOption, in.PaymentOption)
		}
	}

	// Same StepIndex + same amount across the two layers must still be
	// distinguishable via Layer (the whole point of self-description).
	if result.Tranches[0].AmountUSDPerHour != result.Tranches[2].AmountUSDPerHour ||
		result.Tranches[0].StepIndex != result.Tranches[2].StepIndex {
		t.Fatalf("test setup expectation broken: tranches 0 and 2 should share amount and step index")
	}
	if result.Tranches[0].Layer == result.Tranches[2].Layer {
		t.Errorf("tranches 0 and 2 are indistinguishable: same amount, step index, and layer %q", result.Tranches[0].Layer)
	}
	assertAllValid(t, result)
}

// TestBuildTranches_EpsilonOvershootClamped is the regression test for
// fraction sets whose float64 sum passes RampSchedule.Validate's epsilon
// check but whose exact rational sum exceeds 1. Without the clamp in
// computeStepAmount, the last-step remainder would go negative and
// BuildTranches would reject a config its own validator accepted.
func TestBuildTranches_EpsilonOvershootClamped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)

	// Float64 sum is within rampSumEpsilon of 1.0 (passes Validate), but the
	// exact rational sum of the first two fractions already exceeds 1.
	ramp := RampSchedule{Steps: []RampStep{
		{AfterDays: 0, Fraction: 0.5},
		{AfterDays: 30, Fraction: 0.5000000008},
		{AfterDays: 60, Fraction: 1e-10},
	}}
	if err := ramp.Validate(); err != nil {
		t.Fatalf("test premise broken: ramp must pass Validate, got %v", err)
	}

	gap := new(big.Rat).SetInt64(8) // $8/hr
	in := baseInput(baseConfig(ramp), []Allocation{
		{
			Layer:         LayerComputeSP,
			GapUSDPerHour: gap,
			Rationale:     "epsilon overshoot test",
			DataSources:   []string{"test"},
		},
	}, now)

	result, err := BuildTranches(in)
	if err != nil {
		t.Fatalf("BuildTranches() error = %v (validator-accepted schedule must not be rejected)", err)
	}

	// Total must reconstruct the gap exactly despite the overshooting fractions.
	got := totalAmount(result)
	if got.Cmp(gap) != 0 {
		t.Errorf("total = %s, want %s (clamp must keep the total exact)", got.RatString(), gap.RatString())
	}

	// No output item may carry a zero or negative amount: the overshot step is
	// clamped to the remaining gap and the starved last step is skipped.
	for i, a := range result.BuyNow {
		if a.AmountUSDPerHour.Sign() <= 0 {
			t.Errorf("BuyNow[%d] amount = %s, want > 0", i, a.AmountUSDPerHour.RatString())
		}
	}
	for i, tr := range result.Tranches {
		r := new(big.Rat)
		if _, ok := r.SetString(tr.AmountUSDPerHour); !ok {
			t.Fatalf("Tranches[%d].AmountUSDPerHour %q does not parse", i, tr.AmountUSDPerHour)
		}
		if r.Sign() <= 0 {
			t.Errorf("Tranches[%d] amount = %s, want > 0", i, tr.AmountUSDPerHour)
		}
	}

	// Concretely: buy-now consumes gap*0.5 exactly (0.5 is representable);
	// step 1 overshoots and is clamped to the remaining gap*0.5; step 2 is
	// starved to zero and skipped.
	if len(result.BuyNow) != 1 {
		t.Errorf("BuyNow count = %d, want 1", len(result.BuyNow))
	}
	if len(result.Tranches) != 1 {
		t.Errorf("Tranches count = %d, want 1 (starved last step skipped)", len(result.Tranches))
	}
	assertAllValid(t, result)
}

// TestBuildTranches_DuplicateIDsRejected verifies that a misbehaving injected
// NewID producing repeated IDs is rejected with an explicit error rather than
// silently collapsing tranches at SaveTranches upsert time, while a
// well-behaved sequential generator succeeds.
func TestBuildTranches_DuplicateIDsRejected(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	build := func(newID func() string) (*TrancheResult, error) {
		in := baseInput(baseConfig(delayedRamp()), []Allocation{
			mkAlloc(LayerComputeSP, 4),
		}, now)
		in.NewID = newID
		return BuildTranches(in)
	}

	// Constant generator: two delayed steps get the same ID -> explicit error.
	_, err := build(func() string { return "same-id" })
	if err == nil {
		t.Fatalf("BuildTranches() = nil error, want duplicate-ID error for constant NewID")
	}
	if !strings.Contains(err.Error(), "duplicate ID") || !strings.Contains(err.Error(), "same-id") {
		t.Errorf("duplicate-ID error %q must name the offender and the duplication", err)
	}

	// Empty generator: rejected loudly (never persisted with a blank key).
	_, err = build(func() string { return "" })
	if err == nil {
		t.Fatalf("BuildTranches() = nil error, want error for empty-string NewID")
	}

	// Sequential generator: unique IDs -> success.
	result, err := build(seqID("ok"))
	if err != nil {
		t.Fatalf("BuildTranches() error = %v, want nil for sequential NewID", err)
	}
	if len(result.Tranches) != 2 {
		t.Errorf("Tranches count = %d, want 2", len(result.Tranches))
	}
	assertAllValid(t, result)
}

// TestBuildTranches_SmallFractionRationale verifies that a small step fraction
// renders with meaningful precision in the rationale (e.g. "0.4%") instead of
// the misleading "(0%)" that zero-decimal rounding would produce on a
// positive purchase.
func TestBuildTranches_SmallFractionRationale(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	ramp := RampSchedule{Steps: []RampStep{
		{AfterDays: 0, Fraction: 0.004},
		{AfterDays: 30, Fraction: 0.996},
	}}
	in := baseInput(baseConfig(ramp), []Allocation{
		mkAlloc(LayerComputeSP, 100),
	}, now)

	result, err := BuildTranches(in)
	if err != nil {
		t.Fatalf("BuildTranches() error = %v", err)
	}
	if len(result.BuyNow) != 1 {
		t.Fatalf("BuyNow count = %d, want 1", len(result.BuyNow))
	}
	r := result.BuyNow[0].Rationale
	if !strings.Contains(r, "(0.4%)") {
		t.Errorf("rationale %q must render the small fraction as \"(0.4%%)\"", r)
	}
	if strings.Contains(r, "(0%)") {
		t.Errorf("rationale %q renders a positive purchase as \"(0%%)\"", r)
	}
}
