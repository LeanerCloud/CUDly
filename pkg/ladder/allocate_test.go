package ladder

import (
	"maps"
	"math"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// --- helpers ---

func ptr[T any](v T) *T { return &v }

// validConfigAWS returns a fully populated valid LadderConfig for AWS tests.
func validConfigAWS() LadderConfig {
	return LadderConfig{
		Scope:                         Scope{Provider: common.ProviderAWS, AccountID: "123456789012"},
		TargetCoveragePct:             100,
		BufferFraction:                0.10,
		BaselinePercentile:            5,
		LookbackDays:                  30,
		Mode:                          ModeEmailApproval,
		Cadence:                       CadenceWeekly,
		Ramp:                          RampSchedule{Steps: []RampStep{{AfterDays: 0, Fraction: 1.0}}},
		MaxActionsPerRun:              10,
		BufferUtilizationThresholdPct: DefaultBufferUtilizationThresholdPct,
	}
}

// validConfigAzure returns a fully populated valid LadderConfig for Azure
// tests, mirroring validConfigAWS with an Azure scope.
func validConfigAzure() LadderConfig {
	cfg := validConfigAWS()
	cfg.Scope = Scope{Provider: common.ProviderAzure, AccountID: "sub-abc"}
	return cfg
}

// azureLayers returns the merged Azure ladder: the reservation layer carries
// both base and buffer roles; the savings plan is the flex layer.
func azureLayers() []LayerSpec {
	return []LayerSpec{
		{Type: LayerAzureReservation, Roles: []LayerRole{RoleBase, RoleBuffer}},
		{Type: LayerAzureSavingsPlan, Roles: []LayerRole{RoleFlex}},
	}
}

// awsLayers returns the standard 3-layer AWS ladder:
//
//	EC2InstanceSP = base, ComputeSP = flex, ConvertibleRI = buffer.
func awsLayers() []LayerSpec {
	return []LayerSpec{
		{Type: LayerEC2InstanceSP, Roles: []LayerRole{RoleBase}},
		{Type: LayerComputeSP, Roles: []LayerRole{RoleFlex}},
		{Type: LayerConvertibleRI, Roles: []LayerRole{RoleBuffer}},
	}
}

// zeroStates returns LayerStates with zero existing and zero expiring for all
// provided layers.
func zeroStates(layers []LayerSpec) map[LayerType]LayerState {
	m := make(map[LayerType]LayerState, len(layers))
	for _, ls := range layers {
		m[ls.Type] = LayerState{
			Layer:              ls.Type,
			ExistingUSDPerHour: ptr(0.0),
			ExpiringUSDPerHour: ptr(0.0),
		}
	}
	return m
}

// withExisting returns a copy of states with the given layer's ExistingUSDPerHour set.
func withExisting(states map[LayerType]LayerState, layer LayerType, v float64) map[LayerType]LayerState {
	out := make(map[LayerType]LayerState, len(states))
	maps.Copy(out, states)
	s := out[layer]
	s.ExistingUSDPerHour = ptr(v)
	out[layer] = s
	return out
}

// withExpiring returns a copy of states with the given layer's ExpiringUSDPerHour set.
func withExpiring(states map[LayerType]LayerState, layer LayerType, v float64) map[LayerType]LayerState {
	out := make(map[LayerType]LayerState, len(states))
	maps.Copy(out, states)
	s := out[layer]
	s.ExpiringUSDPerHour = ptr(v)
	out[layer] = s
	return out
}

// withBufferUtilization returns a copy of states with the AWS buffer layer's
// (ConvertibleRI) UtilizationPct set. All utilization-focused tests use the
// standard AWS layer set, so the layer is fixed.
func withBufferUtilization(states map[LayerType]LayerState, v float64) map[LayerType]LayerState {
	out := make(map[LayerType]LayerState, len(states))
	maps.Copy(out, states)
	s := out[LayerConvertibleRI]
	s.UtilizationPct = ptr(v)
	out[LayerConvertibleRI] = s
	return out
}

// ratEq reports whether got equals a *big.Rat with numerator num and
// denominator den in lowest terms. Uses Cmp so both sides are normalized.
func ratEq(t *testing.T, label string, got *big.Rat, num, den int64) {
	t.Helper()
	want := new(big.Rat).SetFrac64(num, den)
	if got.Cmp(want) != 0 {
		t.Errorf("%s: got %s, want %d/%d", label, got.RatString(), num, den)
	}
}

func nowFixed() time.Time { return time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) }

// --- tests ---

func TestAllocate_NilInput(t *testing.T) {
	t.Parallel()
	_, err := Allocate(nil)
	if err == nil {
		t.Fatal("expected error for nil input, got nil")
	}
}

func TestAllocate_NilBaseline(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{}, // LowWaterUSDPerHour is nil
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
		DataSources:        []string{"cost-explorer"},
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Allocations) != 0 {
		t.Errorf("expected no allocations, got %d", len(result.Allocations))
	}
	if len(result.Holds) != 1 {
		t.Fatalf("expected 1 hold, got %d", len(result.Holds))
	}
	if !strings.Contains(result.Holds[0].Rationale, "baseline unavailable") {
		t.Errorf("hold rationale missing 'baseline unavailable': %q", result.Holds[0].Rationale)
	}
	if result.Holds[0].Layer == "" {
		t.Errorf("hold layer must not be empty")
	}
	if !result.IsNoOp() {
		t.Errorf("nil-baseline result must be a no-op")
	}
}

func TestAllocate_GapBelowMin(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	// existing = $10/hr on the flex layer -> net E = $10/hr
	// B = $10/hr, Ctgt = $10/hr (100% coverage), gap = 0 -> below min
	states = withExisting(states, LayerComputeSP, 10.0)
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(8.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Allocations) != 0 {
		t.Errorf("expected no allocations, got %d", len(result.Allocations))
	}
	if len(result.Holds) == 0 {
		t.Fatal("expected at least one hold")
	}
	r := result.Holds[0].Rationale
	for _, want := range []string{"$10.0000/hr", "100.00%", "target already met"} {
		if !strings.Contains(r, want) {
			t.Errorf("hold rationale missing %q: %q", want, r)
		}
	}
	if !result.IsNoOp() {
		t.Errorf("gap-below-min result must be a no-op")
	}
}

// TestAllocate_ExistingAboveTarget_Hold covers the negative-gap case where
// existing commitments exceed the coverage target (E > Ctgt): the gap is
// negative, which is below the minimum threshold, so the run holds.
func TestAllocate_ExistingAboveTarget_Hold(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	// existing = $15/hr on flex vs B = Ctgt = $10/hr -> gap = -5
	states = withExisting(states, LayerComputeSP, 15.0)
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(8.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Allocations) != 0 {
		t.Errorf("expected no allocations when existing exceeds target, got %d", len(result.Allocations))
	}
	if len(result.Holds) == 0 {
		t.Fatal("expected a hold when existing exceeds target")
	}
	r := result.Holds[0].Rationale
	// "$-5.0000/hr" pins the rendering of the negative gap value.
	for _, want := range []string{"target already met", "$15.0000/hr", "$10.0000/hr", "$-5.0000/hr"} {
		if !strings.Contains(r, want) {
			t.Errorf("hold rationale missing %q: %q", want, r)
		}
	}
	if !result.IsNoOp() {
		t.Errorf("existing-above-target result must be a no-op")
	}
}

// TestAllocate_AWS3LayerSplit verifies exact big.Rat arithmetic for the
// standard 3-layer AWS case. BufferFraction = 0.5 (1/2) is used because 0.5
// IS exactly representable as float64, giving clean rational intermediates.
//
// Setup:
//
//	B=$10/hr, S=$4/hr, Ctgt=$10/hr (100%), buffer_fraction=1/2
//	existing: base=$1/hr, flex=$0, buffer=$0; expiring: all $0
//	E = $1/hr
//	gap = min(10-1, 10-1) = 9/1
//	bufferGap = 9 * 1/2 = 9/2
//	coreGap   = 9 - 9/2 = 9/2
//	stableLeft = 4 - 1 = 3  ->  baseGap = min(9/2, 3) = 3 (9/2=4.5 > 3)
//	flexGap   = 9/2 - 3 = 3/2
func TestAllocate_AWS3LayerSplit(t *testing.T) {
	t.Parallel()
	cfg := validConfigAWS()
	cfg.BufferFraction = 0.5 // exactly representable as float64
	layers := awsLayers()
	states := zeroStates(layers)
	states = withExisting(states, LayerEC2InstanceSP, 1.0) // $1/hr on base

	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
		DataSources:        []string{"cost-explorer"},
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Allocations) != 3 {
		t.Fatalf("expected 3 allocations, got %d: %+v", len(result.Allocations), result.Allocations)
	}
	if result.IsNoOp() {
		t.Errorf("result with allocations must not be a no-op")
	}

	allocMap := make(map[LayerType]*big.Rat, 3)
	for _, a := range result.Allocations {
		allocMap[a.Layer] = a.GapUSDPerHour
	}

	ratEq(t, "base (EC2InstanceSP)", allocMap[LayerEC2InstanceSP], 3, 1)   // $3/hr
	ratEq(t, "buffer (ConvertibleRI)", allocMap[LayerConvertibleRI], 9, 2) // $4.50/hr
	ratEq(t, "flex (ComputeSP)", allocMap[LayerComputeSP], 3, 2)           // $1.50/hr

	// total must equal gap = $9/hr exactly
	total := new(big.Rat)
	for _, a := range result.Allocations {
		total.Add(total, a.GapUSDPerHour)
	}
	ratEq(t, "total gap", total, 9, 1)

	// rationales must contain concrete numbers
	for _, a := range result.Allocations {
		if !strings.Contains(a.Rationale, "$10.0000/hr") {
			t.Errorf("layer %s rationale missing low_water number: %q", a.Layer, a.Rationale)
		}
		if !strings.Contains(a.Rationale, "100.00%") {
			t.Errorf("layer %s rationale missing target %%: %q", a.Layer, a.Rationale)
		}
	}
}

func TestAllocate_BufferFractionZero(t *testing.T) {
	t.Parallel()
	cfg := validConfigAWS()
	cfg.BufferFraction = 0 // no buffer allocation
	layers := awsLayers()
	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, a := range result.Allocations {
		if a.Layer == LayerConvertibleRI {
			t.Errorf("expected no buffer allocation with buffer_fraction=0, got %s", a.GapUSDPerHour.RatString())
		}
	}
}

// TestAllocate_StableUnknown_BaseZeroFlexGetsAll uses buffer_fraction=0.5 for
// exact arithmetic. With no StableUSDPerHour, baseGap=0 and all core gap goes
// to flex. The flex rationale must mention "stable usage unknown".
//
//	B=$10, E=$0, gap=$10, bufferGap=5/1, coreGap=5/1, baseGap=0, flexGap=5/1
func TestAllocate_StableUnknown_BaseZeroFlexGetsAll(t *testing.T) {
	t.Parallel()
	cfg := validConfigAWS()
	cfg.BufferFraction = 0.5 // exactly representable
	layers := awsLayers()
	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0)}, // StableUSDPerHour nil
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	allocMap := make(map[LayerType]*big.Rat)
	for _, a := range result.Allocations {
		allocMap[a.Layer] = a.GapUSDPerHour
	}
	if g, ok := allocMap[LayerEC2InstanceSP]; ok && g.Sign() > 0 {
		t.Errorf("base layer should be 0 when stable unknown, got %s", g.RatString())
	}
	ratEq(t, "flex", allocMap[LayerComputeSP], 5, 1)
	ratEq(t, "buffer", allocMap[LayerConvertibleRI], 5, 1)

	// flex rationale must mention "stable usage unknown" (routing note)
	for _, a := range result.Allocations {
		if a.Layer == LayerComputeSP {
			if !strings.Contains(a.Rationale, "stable usage unknown") {
				t.Errorf("flex rationale missing 'stable usage unknown': %q", a.Rationale)
			}
		}
	}
}

// TestAllocate_NoBaseLayer_NoteNamesActualReason verifies that when the layer
// set has no RoleBase layer at all, the flex rationale carries the
// "no base layer configured" note rather than the misleading
// "stable usage unknown" message (the stable baseline IS known here).
func TestAllocate_NoBaseLayer_NoteNamesActualReason(t *testing.T) {
	t.Parallel()
	// flex + buffer only, no base layer
	layers := []LayerSpec{
		{Type: LayerComputeSP, Roles: []LayerRole{RoleFlex}},
		{Type: LayerConvertibleRI, Roles: []LayerRole{RoleBuffer}},
	}
	cfg := validConfigAWS()
	cfg.BufferFraction = 0.5
	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	foundFlex := false
	for _, a := range result.Allocations {
		if a.Layer == LayerComputeSP {
			foundFlex = true
			if !strings.Contains(a.Rationale, "no base layer configured") {
				t.Errorf("flex rationale missing 'no base layer configured': %q", a.Rationale)
			}
			if strings.Contains(a.Rationale, "stable usage unknown") {
				t.Errorf("flex rationale must not claim stable is unknown when it is known: %q", a.Rationale)
			}
		}
	}
	if !foundFlex {
		t.Fatal("expected a flex allocation")
	}
}

// TestAllocate_ExpiringExceedsExisting_Error verifies the fail-loud contract:
// a layer reporting more expiring than existing commitment is an inconsistent
// provider snapshot (expiring is defined as a share of existing) and must
// abort the run with an explicit error, never be silently clamped to zero.
func TestAllocate_ExpiringExceedsExisting_Error(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	// ConvertibleRI: existing=$2, expiring=$5 -> inconsistent snapshot
	states = withExisting(states, LayerConvertibleRI, 2.0)
	states = withExpiring(states, LayerConvertibleRI, 5.0)

	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	_, err := Allocate(in)
	if err == nil {
		t.Fatal("expected error for expiring > existing, got nil")
	}
	for _, want := range []string{"exceeds ExistingUSDPerHour", "inconsistent provider snapshot", "$5.0000/hr", "$2.0000/hr"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// TestAllocate_UtilizationClamp verifies min(Ctgt-E, B-E) when coverage=100%.
// When Ctgt = B (100% coverage), both sides of min are equal and gap = B - E.
func TestAllocate_UtilizationClamp(t *testing.T) {
	t.Parallel()
	cfg := validConfigAWS()
	cfg.TargetCoveragePct = 100 // Ctgt = B
	layers := awsLayers()
	states := zeroStates(layers)
	states = withExisting(states, LayerComputeSP, 3.0) // E = $3/hr

	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	// B=$10, Ctgt=$10, E=$3, gap=min(7,7)=$7
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	total := new(big.Rat)
	for _, a := range result.Allocations {
		total.Add(total, a.GapUSDPerHour)
	}
	ratEq(t, "gap when Ctgt=B", total, 7, 1)
}

// TestAllocate_AzureMergedBaseBuffer verifies that when a single layer carries
// both RoleBase and RoleBuffer (Azure reservation pattern), the base and buffer
// gaps are combined into one Allocation on that layer. BufferFraction=0.5 for
// exact rational arithmetic.
//
//	B=$10, S=$4, E=$0, gap=$10, bufferGap=5/1, coreGap=5/1
//	baseGap=min(5,4)=4/1, flexGap=1/1
//	merged (reservation) = 4+5 = 9/1
func TestAllocate_AzureMergedBaseBuffer(t *testing.T) {
	t.Parallel()
	// AzureReservation = base + buffer; AzureSavingsPlan = flex
	layers := azureLayers()
	cfg := validConfigAzure()
	cfg.BufferFraction = 0.5 // exactly representable
	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Allocations) != 2 {
		t.Fatalf("expected 2 allocations (merged + flex), got %d: %+v", len(result.Allocations), result.Allocations)
	}
	allocMap := make(map[LayerType]*big.Rat)
	for _, a := range result.Allocations {
		allocMap[a.Layer] = a.GapUSDPerHour
	}
	ratEq(t, "azure merged base+buffer", allocMap[LayerAzureReservation], 9, 1)
	ratEq(t, "azure flex", allocMap[LayerAzureSavingsPlan], 1, 1)

	// merged allocation rationale must contain "base+buffer"
	for _, a := range result.Allocations {
		if a.Layer == LayerAzureReservation {
			if !strings.Contains(a.Rationale, "base+buffer") {
				t.Errorf("merged rationale missing 'base+buffer': %q", a.Rationale)
			}
		}
	}
}

// TestAllocate_CapScaling verifies proportional scaling when total exceeds the
// per-run cap. The scaled sum must equal the cap exactly (big.Rat arithmetic).
func TestAllocate_CapScaling(t *testing.T) {
	t.Parallel()
	cfg := validConfigAWS()
	hourlyCap := 5.0 // $5/hr cap; uncapped gap would be $10/hr
	cfg.MaxHourlyCommitPerRun = &hourlyCap

	layers := awsLayers()
	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	total := new(big.Rat)
	for _, a := range result.Allocations {
		total.Add(total, a.GapUSDPerHour)
	}
	// total must equal the cap exactly
	ratEq(t, "scaled total == cap", total, 5, 1)

	// Each rationale must mention the capping AND the final post-cap amount.
	// Pre-cap: base=$4, buffer=$1, flex=$5 (gap $10, scale 1/2), so the
	// post-cap figures are $2, $0.50, and $2.50 respectively.
	wantScaledTo := map[LayerType]string{
		LayerEC2InstanceSP: "scaled to $2.0000/hr",
		LayerConvertibleRI: "scaled to $0.5000/hr",
		LayerComputeSP:     "scaled to $2.5000/hr",
	}
	for _, a := range result.Allocations {
		if !strings.Contains(a.Rationale, "capped by max_hourly_commit_per_run") {
			t.Errorf("layer %s rationale missing cap note: %q", a.Layer, a.Rationale)
		}
		if want := wantScaledTo[a.Layer]; !strings.Contains(a.Rationale, want) {
			t.Errorf("layer %s rationale missing post-cap amount %q: %q", a.Layer, want, a.Rationale)
		}
		if !strings.Contains(a.Rationale, "50.00% of requested") {
			t.Errorf("layer %s rationale missing scale percentage: %q", a.Layer, a.Rationale)
		}
	}
}

// TestAllocate_MaxActionsExceeded_Truncates is the primary regression test for
// B3 / Q-TRUNC. Pre-fix: Allocate returned an error when the plan exceeded
// MaxActionsPerRun; the config would stall permanently. Post-fix: it truncates,
// keeps the largest-gap purchases, and emits ActionHold entries for dropped
// ones so no money action is silently lost.
//
// Scenario: 3-layer AWS setup (EC2InstanceSP=$4/hr, ComputeSP=$5/hr,
// ConvertibleRI=$1/hr), MaxActionsPerRun=2. The engine must return exactly 2
// allocations (the two largest: ComputeSP and EC2InstanceSP) and one hold for
// the dropped ConvertibleRI allocation.
func TestAllocate_MaxActionsExceeded_Truncates(t *testing.T) {
	t.Parallel()
	cfg := validConfigAWS()
	cfg.MaxActionsPerRun = 2 // 3 allocations would normally be produced
	layers := awsLayers()
	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
		DataSources:        []string{"cost-explorer"},
	}
	result, err := Allocate(in)
	// Regression: pre-fix this returned an error; post-fix must succeed.
	if err != nil {
		t.Fatalf("expected no error after truncation fix, got: %v", err)
	}
	// Exactly MaxActionsPerRun money actions returned.
	total := len(result.Allocations) + len(result.Reshapes)
	if total != cfg.MaxActionsPerRun {
		t.Errorf("want %d money actions, got %d (allocs=%d reshapes=%d)",
			cfg.MaxActionsPerRun, total, len(result.Allocations), len(result.Reshapes))
	}
	// Largest gaps kept: ComputeSP ($5/hr) and EC2InstanceSP ($4/hr).
	kept := make(map[LayerType]bool, len(result.Allocations))
	for _, a := range result.Allocations {
		kept[a.Layer] = true
	}
	if !kept[LayerComputeSP] {
		t.Error("ComputeSP ($5/hr gap, largest) must be kept")
	}
	if !kept[LayerEC2InstanceSP] {
		t.Error("EC2InstanceSP ($4/hr gap, second largest) must be kept")
	}
	if kept[LayerConvertibleRI] {
		t.Error("ConvertibleRI ($1/hr gap, smallest) must be dropped")
	}
	// Dropped allocation must produce an auditable hold, not be silently lost.
	var truncHolds []PlannedAction
	for _, h := range result.Holds {
		if strings.Contains(h.Rationale, "max_actions_per_run") {
			truncHolds = append(truncHolds, h)
		}
	}
	if len(truncHolds) == 0 {
		t.Error("expected at least one ActionHold for the dropped allocation")
	}
	for _, h := range truncHolds {
		if h.Action != ActionHold {
			t.Errorf("dropped allocation hold must have ActionHold, got %q", h.Action)
		}
		if !strings.Contains(h.Rationale, "max_actions_per_run=2") {
			t.Errorf("hold rationale must contain max_actions_per_run=2: %q", h.Rationale)
		}
		if !strings.Contains(h.Rationale, "deferred to a later run") {
			t.Errorf("hold rationale must contain 'deferred to a later run': %q", h.Rationale)
		}
	}
}

// truncatedHoldLayers returns the set of layers named by max_actions_per_run
// truncation holds in the result, so tests can assert exactly which actions
// were dropped (not merely how many).
func truncatedHoldLayers(holds []PlannedAction) map[LayerType]bool {
	out := make(map[LayerType]bool, len(holds))
	for _, h := range holds {
		if strings.Contains(h.Rationale, "max_actions_per_run") {
			out[h.Layer] = true
		}
	}
	return out
}

// TestAllocate_MaxActions_ReshapeRetained verifies that when reshapes and
// purchases together exceed MaxActionsPerRun, reshapes are preferred over
// purchases: the reshape is kept, the single remaining slot goes to the
// largest-gap purchase, and every dropped purchase leaves a hold.
//
// Fixture: lowWater=10, stable=4, buffer_fraction=0.10, ConvertibleRI existing
// $5/hr @ 50% util (-> reshape). gap = min(10-5, 10-5) = 5; coreGap = 4.5;
// baseGap = min(4.5, stable 4) = 4.0 -> EC2InstanceSP; bufferGap = 0.5 ->
// ConvertibleRI; flexGap = 0.5 -> ComputeSP. So the largest purchase is
// EC2InstanceSP ($4/hr); ComputeSP and ConvertibleRI ($0.5/hr each) are dropped.
// With cap=2 (1 reshape + 1 purchase), EC2InstanceSP survives.
func TestAllocate_MaxActions_ReshapeRetained(t *testing.T) {
	t.Parallel()
	cfg := validConfigAWS()
	cfg.MaxActionsPerRun = 2 // 2 slots: 1 reshape + 1 purchase (out of 3 allocs)
	layers := awsLayers()
	// ConvertibleRI has existing commitments with low utilization -> reshape.
	states := zeroStates(layers)
	states = withExisting(states, LayerConvertibleRI, 5.0)
	states = withBufferUtilization(states, 50.0) // below 90% threshold -> reshape
	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
		DataSources:        []string{"cost-explorer"},
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Exactly MaxActionsPerRun money actions: 1 reshape + 1 purchase.
	total := len(result.Allocations) + len(result.Reshapes)
	if total != cfg.MaxActionsPerRun {
		t.Errorf("want %d money actions, got %d (allocs=%d reshapes=%d)",
			cfg.MaxActionsPerRun, total, len(result.Allocations), len(result.Reshapes))
	}
	// Exactly one reshape, on the buffer layer (ConvertibleRI), retained.
	if len(result.Reshapes) != 1 || result.Reshapes[0].Layer != LayerConvertibleRI {
		t.Fatalf("want exactly one reshape on ConvertibleRI, got %+v", result.Reshapes)
	}
	// Exactly one surviving purchase, on the largest-gap layer (EC2InstanceSP).
	if len(result.Allocations) != 1 {
		t.Fatalf("want exactly one surviving purchase, got %d", len(result.Allocations))
	}
	if result.Allocations[0].Layer != LayerEC2InstanceSP {
		t.Errorf("surviving purchase must be the largest-gap layer EC2InstanceSP, got %s",
			result.Allocations[0].Layer)
	}
	// Exactly the two smaller purchases were dropped with holds.
	held := truncatedHoldLayers(result.Holds)
	wantHeld := map[LayerType]bool{LayerComputeSP: true, LayerConvertibleRI: true}
	if !maps.Equal(held, wantHeld) {
		t.Errorf("dropped-purchase hold layers = %v, want %v", held, wantHeld)
	}
}

// TestAllocate_MaxActions_ReshapesFillCap is the regression for the CRITICAL
// Fable finding: when reshapes alone fill (>=) the cap, every dropped purchase
// must still leave an audit Hold. Pre-fix, this branch discarded all
// allocations with ZERO holds (holds=0); post-fix each dropped purchase names
// its layer + amount in a max_actions_per_run hold.
//
// Fixture matches TestAllocate_MaxActions_ReshapeRetained but with cap=1: the
// single reshape (ConvertibleRI) consumes the only slot, so all three purchases
// (EC2InstanceSP $4/hr, ComputeSP $0.5/hr, ConvertibleRI $0.5/hr) are dropped.
func TestAllocate_MaxActions_ReshapesFillCap(t *testing.T) {
	t.Parallel()
	cfg := validConfigAWS()
	cfg.MaxActionsPerRun = 1 // one slot, taken by the reshape
	layers := awsLayers()
	states := zeroStates(layers)
	states = withExisting(states, LayerConvertibleRI, 5.0)
	states = withBufferUtilization(states, 50.0) // below 90% threshold -> reshape
	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
		DataSources:        []string{"cost-explorer"},
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Exactly one money action, the retained reshape; no purchases survive.
	total := len(result.Allocations) + len(result.Reshapes)
	if total != cfg.MaxActionsPerRun {
		t.Errorf("want %d money action, got %d (allocs=%d reshapes=%d)",
			cfg.MaxActionsPerRun, total, len(result.Allocations), len(result.Reshapes))
	}
	if len(result.Allocations) != 0 {
		t.Errorf("no purchase may survive when the reshape fills the cap, got %d", len(result.Allocations))
	}
	if len(result.Reshapes) != 1 || result.Reshapes[0].Layer != LayerConvertibleRI {
		t.Fatalf("want the ConvertibleRI reshape retained, got %+v", result.Reshapes)
	}
	// CRITICAL: every dropped purchase must leave an audit hold (pre-fix: 0).
	held := truncatedHoldLayers(result.Holds)
	wantHeld := map[LayerType]bool{
		LayerEC2InstanceSP: true,
		LayerComputeSP:     true,
		LayerConvertibleRI: true,
	}
	if !maps.Equal(held, wantHeld) {
		t.Errorf("dropped-purchase hold layers = %v, want %v", held, wantHeld)
	}
	// Each truncation hold must name its layer and the dropped amount.
	var truncHolds int
	for _, h := range result.Holds {
		if !strings.Contains(h.Rationale, "max_actions_per_run") {
			continue
		}
		truncHolds++
		if h.Action != ActionHold {
			t.Errorf("truncation hold must be ActionHold, got %q", h.Action)
		}
		if !strings.Contains(h.Rationale, string(h.Layer)) {
			t.Errorf("hold rationale must name its layer %s: %q", h.Layer, h.Rationale)
		}
		if !strings.Contains(h.Rationale, "/hr") {
			t.Errorf("hold rationale must name the dropped amount: %q", h.Rationale)
		}
	}
	if truncHolds != 3 {
		t.Errorf("want 3 truncation holds (one per dropped purchase), got %d", truncHolds)
	}
}

// TestAllocate_ExactCap_Untouched verifies that a plan whose action count
// exactly equals MaxActionsPerRun is returned unmodified with no truncation holds.
func TestAllocate_ExactCap_Untouched(t *testing.T) {
	t.Parallel()
	cfg := validConfigAWS()
	cfg.MaxActionsPerRun = 3 // exactly the number of allocations produced
	layers := awsLayers()
	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
		DataSources:        []string{"cost-explorer"},
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// All 3 allocations must be kept at exact-cap.
	if len(result.Allocations) != 3 {
		t.Errorf("want 3 allocations at exact cap, got %d", len(result.Allocations))
	}
	// No truncation holds expected.
	for _, h := range result.Holds {
		if strings.Contains(h.Rationale, "max_actions_per_run") {
			t.Errorf("unexpected truncation hold at exact cap: %q", h.Rationale)
		}
	}
}

func TestAllocate_MissingLayerState(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	delete(states, LayerConvertibleRI) // remove buffer layer state

	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	_, err := Allocate(in)
	if err == nil {
		t.Fatal("expected error for missing layer state, got nil")
	}
	if !strings.Contains(err.Error(), "missing state entry") {
		t.Errorf("error missing 'missing state entry': %v", err)
	}
}

func TestAllocate_NilExistingUSDPerHour(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	s := states[LayerConvertibleRI]
	s.ExistingUSDPerHour = nil
	states[LayerConvertibleRI] = s

	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	_, err := Allocate(in)
	if err == nil {
		t.Fatal("expected error for nil ExistingUSDPerHour, got nil")
	}
	if !strings.Contains(err.Error(), "ExistingUSDPerHour is nil") {
		t.Errorf("error missing expected text: %v", err)
	}
}

func TestAllocate_NilExpiringUSDPerHour(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	s := states[LayerConvertibleRI]
	s.ExpiringUSDPerHour = nil
	states[LayerConvertibleRI] = s

	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	_, err := Allocate(in)
	if err == nil {
		t.Fatal("expected error for nil ExpiringUSDPerHour, got nil")
	}
	if !strings.Contains(err.Error(), "ExpiringUSDPerHour is nil") {
		t.Errorf("error missing expected text: %v", err)
	}
}

func TestAllocate_NaNInput(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	states = withExisting(states, LayerConvertibleRI, math.NaN())

	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	_, err := Allocate(in)
	if err == nil {
		t.Fatal("expected error for NaN ExistingUSDPerHour, got nil")
	}
	if !strings.Contains(err.Error(), "NaN") {
		t.Errorf("error missing 'NaN': %v", err)
	}
}

func TestAllocate_NegativeInput(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	states = withExisting(states, LayerComputeSP, -1.0)

	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	_, err := Allocate(in)
	if err == nil {
		t.Fatal("expected error for negative ExistingUSDPerHour, got nil")
	}
	if !strings.Contains(err.Error(), "negative") {
		t.Errorf("error missing 'negative': %v", err)
	}
}

func TestAllocate_NegativeBaseline(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(-5.0)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	_, err := Allocate(in)
	if err == nil {
		t.Fatal("expected error for negative LowWaterUSDPerHour, got nil")
	}
	if !strings.Contains(err.Error(), "negative") {
		t.Errorf("error missing 'negative': %v", err)
	}
}

func TestAllocate_ReshapeEmittedUnderThreshold(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	// Buffer layer has existing commitments and utilization at 70%, threshold
	// at 90% -> reshape expected. (Empty buffer layers are skipped, so the
	// layer must have non-zero existing for the reshape path to trigger.)
	states = withExisting(states, LayerConvertibleRI, 2.0)
	states = withBufferUtilization(states, 70.0)
	in := &AllocationInput{
		Config:             validConfigAWS(), // threshold=90%
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Reshapes) != 1 {
		t.Fatalf("expected 1 reshape, got %d", len(result.Reshapes))
	}
	r := result.Reshapes[0]
	if r.Layer != LayerConvertibleRI {
		t.Errorf("reshape on wrong layer: %s", r.Layer)
	}
	if !strings.Contains(r.Rationale, "70.0%") {
		t.Errorf("reshape rationale missing utilization: %q", r.Rationale)
	}
	if !strings.Contains(r.Rationale, "90.0%") {
		t.Errorf("reshape rationale missing threshold: %q", r.Rationale)
	}
	if result.IsNoOp() {
		t.Errorf("result with reshapes must not be a no-op")
	}
}

func TestAllocate_NoReshapeAtThreshold(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	// Non-empty buffer layer with utilization exactly at threshold -> no reshape
	states = withExisting(states, LayerConvertibleRI, 2.0)
	states = withBufferUtilization(states, DefaultBufferUtilizationThresholdPct)
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Reshapes) != 0 {
		t.Errorf("expected no reshape at threshold, got %d", len(result.Reshapes))
	}
}

func TestAllocate_NoReshapeAboveThreshold(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	// Non-empty buffer layer with utilization above threshold -> no reshape
	states = withExisting(states, LayerConvertibleRI, 2.0)
	states = withBufferUtilization(states, 95.0)
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Reshapes) != 0 {
		t.Errorf("expected no reshape above threshold, got %d", len(result.Reshapes))
	}
}

func TestAllocate_UtilizationNil_InformationalHold(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	// Buffer layer has existing commitments but no utilization data ->
	// informational hold. (Empty buffer layers are skipped and emit nothing.)
	states = withExisting(states, LayerConvertibleRI, 2.0)
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have an informational hold about unknown utilization
	found := false
	for _, h := range result.Holds {
		if h.Layer == LayerConvertibleRI && strings.Contains(h.Rationale, "utilization unknown") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected informational hold for nil utilization on non-empty buffer layer, holds: %+v", result.Holds)
	}
}

// TestAllocate_EmptyBufferLayer_NoNoise verifies that a buffer layer with zero
// existing commitments emits neither a reshape nor an informational hold, even
// when UtilizationPct is present and 0.0 (a fresh account must not get a
// nonsense reshape recommendation for an empty layer).
func TestAllocate_EmptyBufferLayer_NoNoise(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers) // buffer existing = 0
	states = withBufferUtilization(states, 0.0)
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Reshapes) != 0 {
		t.Errorf("expected no reshape for empty buffer layer, got %d: %+v", len(result.Reshapes), result.Reshapes)
	}
	for _, h := range result.Holds {
		if h.Layer == LayerConvertibleRI {
			t.Errorf("expected no informational hold for empty buffer layer, got: %q", h.Rationale)
		}
	}
}

func TestRatFromFloat_Validation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		v       float64
		wantErr bool
	}{
		{"valid zero", 0.0, false},
		{"valid positive", 1.5, false},
		{"NaN", math.NaN(), true},
		{"+Inf", math.Inf(1), true},
		{"-Inf", math.Inf(-1), true},
		{"negative", -0.01, true},
	}
	for _, c := range cases {
		_, err := ratFromFloat(c.name, c.v)
		if c.wantErr && err == nil {
			t.Errorf("ratFromFloat(%q, %v) = nil, want error", c.name, c.v)
		}
		if !c.wantErr && err != nil {
			t.Errorf("ratFromFloat(%q, %v) = %v, want nil", c.name, c.v, err)
		}
	}
}

// TestRatFromFloat_NegativeIsError additionally asserts that the error message
// names the negativity so callers can identify the rejection reason.
func TestRatFromFloat_NegativeIsError(t *testing.T) {
	t.Parallel()
	_, err := ratFromFloat("test", -0.01)
	if err == nil {
		t.Fatal("expected error for negative value, got nil")
	}
	if !strings.Contains(err.Error(), "negative") {
		t.Errorf("error missing 'negative': %v", err)
	}
}

func TestAllocate_InvalidConfig(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	cfg := validConfigAWS()
	cfg.TargetCoveragePct = 0 // invalid
	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	_, err := Allocate(in)
	if err == nil {
		t.Fatal("expected error for invalid config, got nil")
	}
}

func TestAllocate_EmptyLayers(t *testing.T) {
	t.Parallel()
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             nil,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0)},
		LayerStates:        map[LayerType]LayerState{},
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	_, err := Allocate(in)
	if err == nil {
		t.Fatal("expected error for empty layers, got nil")
	}
}

func TestAllocate_NoFlexLayer(t *testing.T) {
	t.Parallel()
	// One base layer + one buffer layer, no flex -> fails validateLayers
	// (flexCount=0, exactly one required).
	layers := []LayerSpec{
		{Type: LayerEC2InstanceSP, Roles: []LayerRole{RoleBase}},
		{Type: LayerConvertibleRI, Roles: []LayerRole{RoleBuffer}},
	}
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	_, err := Allocate(in)
	if err == nil {
		t.Fatal("expected error for missing flex layer, got nil")
	}
	if !strings.Contains(err.Error(), string(RoleFlex)) {
		t.Errorf("error missing 'flex' mention: %v", err)
	}
}

// TestAllocate_TwoBaseLayers verifies that two distinct RoleBase layers are
// rejected by validateLayers.
func TestAllocate_TwoBaseLayers(t *testing.T) {
	t.Parallel()
	layers := []LayerSpec{
		{Type: LayerEC2InstanceSP, Roles: []LayerRole{RoleBase}},
		{Type: LayerConvertibleRI, Roles: []LayerRole{RoleBase}},
		{Type: LayerComputeSP, Roles: []LayerRole{RoleFlex}},
	}
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	_, err := Allocate(in)
	if err == nil {
		t.Fatal("expected error for two base layers, got nil")
	}
	if !strings.Contains(err.Error(), "at most one") {
		t.Errorf("error missing 'at most one': %v", err)
	}
}

// TestAllocate_TwoFlexLayers verifies that two RoleFlex layers are rejected by
// validateLayers.
func TestAllocate_TwoFlexLayers(t *testing.T) {
	t.Parallel()
	layers := []LayerSpec{
		{Type: LayerComputeSP, Roles: []LayerRole{RoleFlex}},
		{Type: LayerEC2InstanceSP, Roles: []LayerRole{RoleFlex}},
	}
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	_, err := Allocate(in)
	if err == nil {
		t.Fatal("expected error for two flex layers, got nil")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("error missing 'exactly one': %v", err)
	}
}

// TestAllocate_LayerTopologyValidation covers the hardened topology
// invariants: duplicate layer types, more than one buffer-role layer, and
// any multi-role merge other than base+buffer are rejected; the Azure
// base+buffer merge stays accepted.
func TestAllocate_LayerTopologyValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		errContains string // empty means the topology must be accepted
		layers      []LayerSpec
	}{
		{
			name: "duplicate layer types rejected",
			layers: []LayerSpec{
				{Type: LayerComputeSP, Roles: []LayerRole{RoleFlex}},
				{Type: LayerComputeSP, Roles: []LayerRole{RoleBuffer}},
			},
			errContains: "duplicate layer type",
		},
		{
			name: "two buffer layers rejected",
			layers: []LayerSpec{
				{Type: LayerEC2InstanceSP, Roles: []LayerRole{RoleBase}},
				{Type: LayerComputeSP, Roles: []LayerRole{RoleFlex}},
				{Type: LayerConvertibleRI, Roles: []LayerRole{RoleBuffer}},
				{Type: LayerAzureReservation, Roles: []LayerRole{RoleBuffer}},
			},
			errContains: "at most one buffer",
		},
		{
			name: "flex+buffer merge rejected",
			layers: []LayerSpec{
				{Type: LayerComputeSP, Roles: []LayerRole{RoleFlex, RoleBuffer}},
			},
			errContains: "must not be combined",
		},
		{
			name: "flex+base merge rejected",
			layers: []LayerSpec{
				{Type: LayerComputeSP, Roles: []LayerRole{RoleFlex, RoleBase}},
			},
			errContains: "must not be combined",
		},
		{
			name: "all three roles on one layer rejected",
			layers: []LayerSpec{
				{Type: LayerComputeSP, Roles: []LayerRole{RoleBase, RoleFlex, RoleBuffer}},
			},
			errContains: "must not be combined",
		},
		{
			name: "bogus layer type rejected",
			layers: []LayerSpec{
				{Type: "bogus", Roles: []LayerRole{RoleFlex}},
			},
			errContains: "unknown layer type",
		},
		{
			name: "bogus role rejected",
			layers: []LayerSpec{
				{Type: LayerComputeSP, Roles: []LayerRole{RoleFlex}},
				{Type: LayerConvertibleRI, Roles: []LayerRole{"bogus-role"}},
			},
			errContains: "unknown layer role",
		},
		{
			name:   "azure base+buffer merge accepted",
			layers: azureLayers(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := &AllocationInput{
				Config:             validConfigAzure(),
				Layers:             c.layers,
				Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
				LayerStates:        zeroStates(c.layers),
				Now:                nowFixed(),
				InFlightUSDPerHour: ptr(0.0),
			}
			_, err := Allocate(in)
			if c.errContains == "" {
				if err != nil {
					t.Fatalf("expected topology to be accepted, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.errContains)
			}
			if !strings.Contains(err.Error(), c.errContains) {
				t.Errorf("error missing %q: %v", c.errContains, err)
			}
		})
	}
}

// TestAllocate_UtilizationPctValidation verifies that a non-nil UtilizationPct
// must be finite and within [0, 100]. A NaN previously compared false against
// the reshape threshold and silently skipped reshape detection; it must abort
// the run instead.
func TestAllocate_UtilizationPctValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		pct     float64
		wantErr bool
	}{
		{"NaN rejected", math.NaN(), true},
		{"+Inf rejected", math.Inf(1), true},
		{"-Inf rejected", math.Inf(-1), true},
		{"negative rejected", -1.0, true},
		{"above 100 rejected", 101.0, true},
		{"zero accepted", 0.0, false},
		{"exactly 100 accepted", 100.0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			layers := awsLayers()
			states := zeroStates(layers)
			// non-empty buffer layer so the utilization value is evaluated
			states = withExisting(states, LayerConvertibleRI, 2.0)
			states = withBufferUtilization(states, c.pct)
			in := &AllocationInput{
				Config:             validConfigAWS(),
				Layers:             layers,
				Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
				LayerStates:        states,
				Now:                nowFixed(),
				InFlightUSDPerHour: ptr(0.0),
			}
			_, err := Allocate(in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for UtilizationPct %v, got nil", c.pct)
				}
				if !strings.Contains(err.Error(), "UtilizationPct") {
					t.Errorf("error missing 'UtilizationPct': %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for UtilizationPct %v: %v", c.pct, err)
			}
		})
	}
}

// TestAllocate_NoBufferLayerWithBufferFraction_Error is a regression test for
// a money-path bug: with a base+flex-only topology and BufferFraction > 0,
// splitGap used to emit an Allocation on an empty ("") layer type, i.e. a
// purchase directive aimed at a nonexistent layer. It must instead fail loud.
func TestAllocate_NoBufferLayerWithBufferFraction_Error(t *testing.T) {
	t.Parallel()
	// base + flex only, no buffer-role layer
	layers := []LayerSpec{
		{Type: LayerEC2InstanceSP, Roles: []LayerRole{RoleBase}},
		{Type: LayerComputeSP, Roles: []LayerRole{RoleFlex}},
	}
	cfg := validConfigAWS() // BufferFraction = 0.10 > 0
	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	_, err := Allocate(in)
	if err == nil {
		t.Fatal("expected error for buffer_fraction > 0 with no buffer-role layer, got nil")
	}
	if !strings.Contains(err.Error(), "no buffer-role layer") {
		t.Errorf("error missing 'no buffer-role layer': %v", err)
	}
}

// TestAllocate_NoBufferLayerZeroFraction_OK verifies that a base+flex-only
// topology is valid when BufferFraction is 0: the buffer gap is zero, so the
// run succeeds with base and flex allocations only and no empty-layer output.
//
//	B=$10, S=$4, E=$0, gap=$10, bufferGap=0, coreGap=10
//	baseGap=min(10,4)=4/1, flexGap=6/1
func TestAllocate_NoBufferLayerZeroFraction_OK(t *testing.T) {
	t.Parallel()
	layers := []LayerSpec{
		{Type: LayerEC2InstanceSP, Roles: []LayerRole{RoleBase}},
		{Type: LayerComputeSP, Roles: []LayerRole{RoleFlex}},
	}
	cfg := validConfigAWS()
	cfg.BufferFraction = 0
	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Allocations) != 2 {
		t.Fatalf("expected 2 allocations (base + flex), got %d: %+v", len(result.Allocations), result.Allocations)
	}
	allocMap := make(map[LayerType]*big.Rat)
	for _, a := range result.Allocations {
		if a.Layer == "" {
			t.Fatalf("allocation with empty layer type emitted: %+v", a)
		}
		allocMap[a.Layer] = a.GapUSDPerHour
	}
	ratEq(t, "base (EC2InstanceSP)", allocMap[LayerEC2InstanceSP], 4, 1)
	ratEq(t, "flex (ComputeSP)", allocMap[LayerComputeSP], 6, 1)
}

// TestAllocate_TargetMet_ReshapeStillEmitted is a regression test: reshape
// detection must run independently of the purchase gap. An over-committed
// account (target met, negative or zero gap) with an under-utilized buffer is
// exactly the case where reshaping matters, so the target-met early exit must
// emit BOTH the hold and the reshape.
func TestAllocate_TargetMet_ReshapeStillEmitted(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	// E = 8 (flex) + 2 (buffer) = $10/hr = B -> target met -> hold path.
	states = withExisting(states, LayerComputeSP, 8.0)
	states = withExisting(states, LayerConvertibleRI, 2.0)
	states = withBufferUtilization(states, 70.0) // below 90% threshold
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(8.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Holds) == 0 || !strings.Contains(result.Holds[0].Rationale, "target already met") {
		t.Errorf("expected target-met hold, holds: %+v", result.Holds)
	}
	if len(result.Reshapes) != 1 {
		t.Fatalf("expected 1 reshape alongside the target-met hold, got %d", len(result.Reshapes))
	}
	if !strings.Contains(result.Reshapes[0].Rationale, "70.0%") {
		t.Errorf("reshape rationale missing utilization: %q", result.Reshapes[0].Rationale)
	}
	if result.IsNoOp() {
		t.Errorf("result with a reshape must not be a no-op")
	}
}

// TestAllocate_NilBaseline_ReshapeStillEmitted is the second independence
// regression: even when the baseline is unavailable (no gap can be computed),
// buffer reshape detection needs only layer states and must still run.
func TestAllocate_NilBaseline_ReshapeStillEmitted(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	states = withExisting(states, LayerConvertibleRI, 2.0)
	states = withBufferUtilization(states, 70.0) // below 90% threshold
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{}, // LowWaterUSDPerHour nil
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	foundBaselineHold := false
	for _, h := range result.Holds {
		if strings.Contains(h.Rationale, "baseline unavailable") {
			foundBaselineHold = true
		}
	}
	if !foundBaselineHold {
		t.Errorf("expected baseline-unavailable hold, holds: %+v", result.Holds)
	}
	if len(result.Reshapes) != 1 {
		t.Fatalf("expected 1 reshape alongside the baseline hold, got %d", len(result.Reshapes))
	}
	if result.IsNoOp() {
		t.Errorf("result with a reshape must not be a no-op")
	}
}

// TestAllocate_UnknownLayerStateKey_Error verifies that a LayerStates entry
// not present in the supported layer list is rejected: it would otherwise be
// silently excluded from existing-coverage accounting, overstating the
// purchase gap.
func TestAllocate_UnknownLayerStateKey_Error(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	states := zeroStates(layers)
	states[LayerAzureReservation] = LayerState{
		Layer:              LayerAzureReservation,
		ExistingUSDPerHour: ptr(5.0),
		ExpiringUSDPerHour: ptr(0.0),
	}
	in := &AllocationInput{
		Config:             validConfigAWS(),
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0)},
		LayerStates:        states,
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	_, err := Allocate(in)
	if err == nil {
		t.Fatal("expected error for unknown layer state key, got nil")
	}
	for _, want := range []string{"unknown layer", string(LayerAzureReservation)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// TestAllocate_SubMinimumBufferSkipped verifies that a split amount below the
// minimum allocatable threshold is dropped WITH an informational hold naming
// the layer and the skipped amount, while the other allocations stay intact.
//
//	B=$10, S=$4, E=$0, gap=$10, fraction=0.0005 -> bufferGap=$0.005 < $0.01
//	base=$4 and flex=$5.995 both stay.
func TestAllocate_SubMinimumBufferSkipped(t *testing.T) {
	t.Parallel()
	cfg := validConfigAWS()
	cfg.BufferFraction = 0.0005
	layers := awsLayers()
	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0), StableUSDPerHour: ptr(4.0)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Allocations) != 2 {
		t.Fatalf("expected 2 allocations (base + flex), got %d: %+v", len(result.Allocations), result.Allocations)
	}
	for _, a := range result.Allocations {
		if a.Layer == LayerConvertibleRI {
			t.Errorf("sub-minimum buffer allocation must be dropped, got %s", a.GapUSDPerHour.RatString())
		}
	}
	foundSkip := false
	for _, h := range result.Holds {
		if h.Layer == LayerConvertibleRI &&
			strings.Contains(h.Rationale, "below minimum allocatable amount; skipped") &&
			strings.Contains(h.Rationale, "$0.0050/hr") {
			foundSkip = true
		}
	}
	if !foundSkip {
		t.Errorf("expected informational hold for skipped sub-minimum buffer amount, holds: %+v", result.Holds)
	}
}

// TestAllocate_AllSplitsSubMinimum verifies the boundary case from the review:
// gap $0.011 with fraction 0.10 leaves every split amount below the $0.01
// minimum (buffer $0.0011, flex $0.0099 with stable unknown), so every
// allocation is dropped with its own informational hold and the run is a
// no-op. Nothing is silently lost.
func TestAllocate_AllSplitsSubMinimum(t *testing.T) {
	t.Parallel()
	layers := awsLayers()
	in := &AllocationInput{
		Config: validConfigAWS(), // fraction 0.10
		Layers: layers,
		// gap = $0.011 (just above the min-gap threshold); stable unknown so
		// base gets nothing and flex receives the whole core gap of $0.0099.
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(0.011)},
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Allocations) != 0 {
		t.Fatalf("expected all sub-minimum allocations dropped, got %d: %+v", len(result.Allocations), result.Allocations)
	}
	skips := 0
	for _, h := range result.Holds {
		if strings.Contains(h.Rationale, "below minimum allocatable amount; skipped") {
			skips++
		}
	}
	if skips != 2 { // buffer and flex splits were both sub-minimum
		t.Errorf("expected 2 skip holds (buffer + flex), got %d: %+v", skips, result.Holds)
	}
	if !result.IsNoOp() {
		t.Errorf("all-skipped result must be a no-op")
	}
}

// TestAllocate_AzureMerged_StableUnknown_NoteOnFlexOnly verifies that the
// "stable usage unknown" routing note lands ONLY on the flex allocation that
// received the rerouted core gap, not on the merged base+buffer allocation
// whose base share is zero.
func TestAllocate_AzureMerged_StableUnknown_NoteOnFlexOnly(t *testing.T) {
	t.Parallel()
	layers := azureLayers()
	cfg := validConfigAzure()
	cfg.BufferFraction = 0.5
	in := &AllocationInput{
		Config:             cfg,
		Layers:             layers,
		Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(10.0)}, // stable unknown
		LayerStates:        zeroStates(layers),
		Now:                nowFixed(),
		InFlightUSDPerHour: ptr(0.0),
	}
	// bufferGap=5, coreGap=5, baseGap=0 (stable unknown), flexGap=5, merged=5
	result, err := Allocate(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Allocations) != 2 {
		t.Fatalf("expected 2 allocations (merged + flex), got %d: %+v", len(result.Allocations), result.Allocations)
	}
	for _, a := range result.Allocations {
		hasNote := strings.Contains(a.Rationale, "stable usage unknown")
		switch a.Layer {
		case LayerAzureReservation:
			if hasNote {
				t.Errorf("merged base+buffer rationale must NOT carry the stable-unknown note: %q", a.Rationale)
			}
		case LayerAzureSavingsPlan:
			if !hasNote {
				t.Errorf("flex rationale must carry the stable-unknown note: %q", a.Rationale)
			}
		}
	}
}

// TestAllocate_ConvergenceNetting is the L5 regression guard: three sequential
// daily runs with identical usage must not accumulate planned commitment beyond
// the initial gap. Without in-flight netting each run would re-plan the full
// gap and the system would diverge; with netting only the first run allocates
// and subsequent runs hold.
//
// To confirm the test guards the real behaviour, the second sub-test ("no
// netting diverges") shows that feeding zero in-flight to every run produces
// three allocations totalling 3x the gap -- this sub-test MUST FAIL if the L5
// netting logic is removed from Allocate.
func TestAllocate_ConvergenceNetting(t *testing.T) {
	t.Parallel()

	// Baseline: low-water $10/hr, target coverage 80% => target $8/hr.
	// Existing = $0, so initial gap = $8.
	const targetPct = 80.0
	const lowWater = 10.0
	const initialGap = 8.0 // min($8-$0, $10-$0) = $8

	makeInput := func(inFlight float64) *AllocationInput {
		layers := awsLayers()
		states := zeroStates(layers)
		cfg := validConfigAWS()
		cfg.TargetCoveragePct = targetPct
		return &AllocationInput{
			Config:             cfg,
			Layers:             layers,
			Baseline:           UsageBaseline{LowWaterUSDPerHour: ptr(lowWater), StableUSDPerHour: ptr(lowWater * 0.9)},
			LayerStates:        states,
			Now:                nowFixed(),
			DataSources:        []string{"cost-explorer"},
			InFlightUSDPerHour: &inFlight,
		}
	}

	t.Run("with netting converges", func(t *testing.T) {
		t.Parallel()
		var totalPlanned float64

		// Run 1: no in-flight -> allocates the full gap.
		r1, err := Allocate(makeInput(0.0))
		if err != nil {
			t.Fatalf("run1 Allocate: %v", err)
		}
		for _, a := range r1.Allocations {
			f, _ := a.GapUSDPerHour.Float64()
			totalPlanned += f
		}
		if len(r1.Allocations) == 0 {
			t.Fatalf("run1: expected at least one allocation with zero in-flight, got hold: %+v", r1.Holds)
		}

		// Run 2: in-flight = totalPlanned from run 1 -> should hold (gap <= min).
		r2, err := Allocate(makeInput(totalPlanned))
		if err != nil {
			t.Fatalf("run2 Allocate: %v", err)
		}
		if len(r2.Allocations) != 0 {
			t.Errorf("run2: expected Hold (gap covered by in-flight), got %d allocations", len(r2.Allocations))
		}
		if len(r2.Holds) == 0 {
			t.Errorf("run2: expected at least one Hold action, got none")
		}

		// Run 3: same in-flight as run 2 -> still Hold.
		r3, err := Allocate(makeInput(totalPlanned))
		if err != nil {
			t.Fatalf("run3 Allocate: %v", err)
		}
		if len(r3.Allocations) != 0 {
			t.Errorf("run3: expected Hold (gap covered by in-flight), got %d allocations", len(r3.Allocations))
		}

		// Total planned must be approx the initial gap (not 2x or 3x).
		if totalPlanned < initialGap*0.5 || totalPlanned > initialGap*1.5 {
			t.Errorf("total planned = %.4f, want approx %.4f (1x gap, not accumulated)", totalPlanned, initialGap)
		}
	})

	t.Run("no netting diverges", func(t *testing.T) {
		// This sub-test verifies that IGNORING in-flight (always passing 0)
		// produces 3x the initial gap. It is a canary: if the L5 gap subtraction
		// is removed from Allocate this sub-test changes meaning but the
		// "with netting" sub-test above will fail first, which is the real guard.
		t.Parallel()
		var totalWithoutNetting float64
		for i := 0; i < 3; i++ {
			r, err := Allocate(makeInput(0.0)) // zero in-flight every time
			if err != nil {
				t.Fatalf("run%d Allocate: %v", i+1, err)
			}
			for _, a := range r.Allocations {
				f, _ := a.GapUSDPerHour.Float64()
				totalWithoutNetting += f
			}
		}
		if totalWithoutNetting < initialGap*2.0 {
			t.Errorf("without netting: expected total >= 2x gap (%.4f), got %.4f; the divergence canary is broken", initialGap*2.0, totalWithoutNetting)
		}
	})
}

// TestAllocateResult_IsNoOp verifies IsNoOp across the result shapes.
func TestAllocateResult_IsNoOp(t *testing.T) {
	t.Parallel()
	amount := new(big.Rat).SetInt64(1)
	cases := []struct {
		name   string
		result AllocateResult
		want   bool
	}{
		{"empty result", AllocateResult{}, true},
		{
			"holds only",
			AllocateResult{Holds: []PlannedAction{{Action: ActionHold, Layer: LayerComputeSP, Rationale: "x"}}},
			true,
		},
		{
			"with allocation",
			AllocateResult{Allocations: []Allocation{{Layer: LayerComputeSP, GapUSDPerHour: amount, Rationale: "x"}}},
			false,
		},
		{
			"with reshape",
			AllocateResult{Reshapes: []PlannedAction{{Action: ActionReshape, Layer: LayerConvertibleRI, Rationale: "x"}}},
			false,
		},
		{
			"allocation and holds",
			AllocateResult{
				Allocations: []Allocation{{Layer: LayerComputeSP, GapUSDPerHour: amount, Rationale: "x"}},
				Holds:       []PlannedAction{{Action: ActionHold, Layer: LayerConvertibleRI, Rationale: "y"}},
			},
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.result.IsNoOp(); got != c.want {
				t.Errorf("IsNoOp() = %v, want %v", got, c.want)
			}
		})
	}
}
