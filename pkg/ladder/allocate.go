package ladder

import (
	"fmt"
	"math"
	"math/big"
	"slices"
	"time"
)

// minAllocatableGapNum and minAllocatableGapDen define the smallest gap that
// triggers a new allocation as the exact rational 1/100 ($0.01/hr
// on-demand-equivalent). Gaps at or below this value result in a Hold with a
// rationale explaining that the target is already met. Expressed as an exact
// numerator/denominator pair (not a float64 literal) so the threshold used in
// comparisons is exactly one cent with no binary-float representation error.
const (
	minAllocatableGapNum = 1
	minAllocatableGapDen = 100
)

// minAllocatableGap returns the exact minimum allocatable gap threshold
// ($0.01/hr) as a *big.Rat.
func minAllocatableGap() *big.Rat {
	return big.NewRat(minAllocatableGapNum, minAllocatableGapDen)
}

// AllocationInput carries everything Allocate needs. All provider data must be
// pre-fetched by the caller; Allocate performs no I/O.
//
// Contract on LayerStates: for every LayerSpec in Layers, LayerStates must
// contain an entry whose ExistingUSDPerHour and ExpiringUSDPerHour are both
// non-nil. Providers must supply an explicit zero rather than a nil pointer
// when a field is genuinely zero; a nil pointer is treated as missing data
// and causes Allocate to return an error (fail loud: incomplete provider data
// aborts the run).
//
// InFlightUSDPerHour is the total hourly commitment already in flight for
// this config's scope: the sum of all scheduled (not-yet-fired) and fired
// (not-yet-terminal) ladder tranches, plus any in-progress purchase
// executions linked to this config's runs. It is required non-nil (fail
// loud) so callers must always explicitly account for in-flight commitment.
// A genuinely zero in-flight must be passed as a pointer to 0.0, not nil.
// Subtracting in-flight from the gap prevents new runs from re-planning
// commitment that is already en route, eliminating the pile-up that occurs
// when multiple daily runs each see the full gap before prior tranches fire.
//
// DataSources lists the data feeds (e.g. "cost-explorer", "cloudwatch") used
// to derive the inputs. The slice is propagated verbatim to every Allocation
// and PlannedAction produced by Allocate so that approval emails and audit
// logs identify the source of each decision.
//
// Now must be set by the caller; Allocate never calls time.Now internally.
type AllocationInput struct {
	Now                time.Time
	LayerStates        map[LayerType]LayerState
	Layers             []LayerSpec
	DataSources        []string
	Baseline           UsageBaseline
	Config             LadderConfig
	InFlightUSDPerHour *float64
}

// Allocation is a per-layer sizing decision produced by Allocate. The
// GapUSDPerHour field carries the positive hourly commitment delta this layer
// should grow by. Ramp and tranche logic (PR 3) converts Allocations into
// time-indexed purchase tranches.
type Allocation struct {
	Layer         LayerType
	GapUSDPerHour *big.Rat
	Rationale     string
	DataSources   []string
}

// AllocateResult is the output of Allocate.
//
// Allocations may be accompanied by informational Holds (e.g. "buffer
// utilization unknown; reshape not evaluated", or a sub-minimum split amount
// that was skipped) and by Reshapes for under-utilized buffer layers.
// Reshapes are detected independently of the purchase gap, so they can appear
// alongside an early-exit Hold (baseline unavailable, target met). A run is a
// no-op if and only if Allocations and Reshapes are both empty (see IsNoOp);
// in that case Holds carries the explanation (baseline unavailable, gap below
// the minimum threshold, or every split amount below the minimum).
type AllocateResult struct {
	Allocations []Allocation
	Reshapes    []PlannedAction
	Holds       []PlannedAction
}

// IsNoOp reports whether the result proposes no state-changing actions:
// no allocations to purchase and no buffer reshapes. Informational Holds do
// not affect no-op status.
func (r *AllocateResult) IsNoOp() bool {
	return len(r.Allocations) == 0 && len(r.Reshapes) == 0
}

// Allocate determines how much new commitment to place on each layer for one
// ladder scope run. All monetary values are computed in exact *big.Rat
// arithmetic; float64 inputs are converted at the boundary via ratFromFloat.
//
// Steps (see inline comments):
//  1. Validate config, layers (enums + topology), and layer states.
//  2. Detect under-utilized buffer layers (reshape, or informational hold
//     when utilization is unknown on a non-empty buffer layer). This runs
//     INDEPENDENTLY of the purchase gap: an over-committed account is exactly
//     when reshaping matters, so the early no-purchase exits below must not
//     mask it. Reshape detection needs only valid layer states.
//  3. Nil baseline -> Hold (explainable no-op, not an error), plus any
//     buffer maintenance from step 2.
//  4. Compute net existing per layer; derive gap.
//  5. Gap <= minimum threshold -> Hold with numbers in rationale, plus any
//     buffer maintenance from step 2.
//  6. Split gap across base, flex, and buffer roles.
//  7. Apply per-run spend cap (proportional scaling), then drop sub-minimum
//     allocations with an informational hold each (never silently lost).
//  8. Truncate to MaxActionsPerRun: sort purchases by GapUSDPerHour descending,
//     keep the largest, emit one ActionHold per dropped action for auditability
//     (reshapes are always retained first; no truncated money action is silently lost).
func Allocate(in *AllocationInput) (*AllocateResult, error) {
	if in == nil {
		return nil, fmt.Errorf("allocation input must not be nil")
	}
	if err := validateAllocateInput(in); err != nil {
		return nil, err
	}
	reshapes, maintHolds := detectReshapes(in.Layers, in.LayerStates, in.Config.BufferUtilizationThresholdPct, in.DataSources)
	if in.Baseline.LowWaterUSDPerHour == nil {
		return withMaintenance(baselineUnavailableHold(in), reshapes, maintHolds), nil
	}
	nets, existingTotal, err := computeNetExisting(in.Layers, in.LayerStates)
	if err != nil {
		return nil, err
	}
	lowWater, target, err := computeBaselineAndTarget(in)
	if err != nil {
		return nil, err
	}
	inFlight, err := ratFromFloat("in_flight_usd_hr", *in.InFlightUSDPerHour)
	if err != nil {
		return nil, fmt.Errorf("in_flight_usd_hr: %w", err)
	}
	// gap = min(target-E, lowWater-E) - inFlight.
	// The two-arm min is defensive: it guards against future target derivations
	// exceeding the observed floor (today target<=lowWater always holds because
	// TargetCoveragePct is validated to be <=100). Subtracting inFlight nets
	// out scheduled/fired tranches that are already en route so repeated runs
	// on the same day do not re-plan commitment that is already in flight.
	preInflightGap := minRat(new(big.Rat).Sub(target, existingTotal), new(big.Rat).Sub(lowWater, existingTotal))
	gap := new(big.Rat).Sub(preInflightGap, inFlight)
	if gap.Cmp(minAllocatableGap()) <= 0 {
		return withMaintenance(gapBelowMinHold(gap, lowWater, target, existingTotal, inFlight, in), reshapes, maintHolds), nil
	}
	return buildResult(in, lowWater, target, existingTotal, gap, nets, reshapes, maintHolds)
}

// withMaintenance merges independently detected buffer maintenance actions
// (reshapes and their informational holds) into an early-exit hold result so
// a no-purchase run never masks needed buffer reshaping.
func withMaintenance(result *AllocateResult, reshapes, maintHolds []PlannedAction) *AllocateResult {
	result.Reshapes = append(result.Reshapes, reshapes...)
	result.Holds = append(result.Holds, maintHolds...)
	return result
}

// ratFromFloat converts v to *big.Rat, rejecting NaN, Inf, and negative values.
// The name parameter is embedded in the error message so callers can identify
// which field triggered the rejection.
func ratFromFloat(name string, v float64) (*big.Rat, error) {
	if math.IsNaN(v) {
		return nil, fmt.Errorf("%s: NaN is not a valid monetary value", name)
	}
	if math.IsInf(v, 0) {
		return nil, fmt.Errorf("%s: Inf is not a valid monetary value", name)
	}
	if v < 0 {
		return nil, fmt.Errorf("%s: negative value %g is not allowed", name, v)
	}
	return new(big.Rat).SetFloat64(v), nil
}

// minRat returns the smaller of a and b without modifying either.
func minRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

// fmtRatUSD renders r as a fixed-4-decimal USD/hr string for use in rationales.
// Using 4 decimal places rather than 2 to preserve meaningful precision for
// small gap values.
func fmtRatUSD(r *big.Rat) string {
	f, _ := r.Float64()
	return fmt.Sprintf("$%.4f/hr", f)
}

// validateAllocateInput validates the config, layer list, layer states, and
// the required in-flight commitment figure. Returns the first error encountered.
func validateAllocateInput(in *AllocationInput) error {
	if in.InFlightUSDPerHour == nil {
		return fmt.Errorf("InFlightUSDPerHour must not be nil: callers must always supply the in-flight commitment; pass a pointer to 0.0 when there is no in-flight commitment")
	}
	if err := in.Config.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if len(in.Layers) == 0 {
		return fmt.Errorf("layers must not be empty")
	}
	if err := validateLayers(in.Layers); err != nil {
		return fmt.Errorf("layers: %w", err)
	}
	if err := validateLayerStates(in.Layers, in.LayerStates); err != nil {
		return fmt.Errorf("layer_states: %w", err)
	}
	return nil
}

// validateLayers checks the layer list for structural consistency:
//   - every layer type and role is a recognized enum value
//   - no duplicate layer types across specs
//   - exactly one RoleFlex layer
//   - at most one RoleBase layer and at most one RoleBuffer layer
//   - the only permitted multi-role layer is the base+buffer merge (Azure
//     reservation shape); RoleFlex must not share a layer with any other role
func validateLayers(layers []LayerSpec) error {
	if err := validateLayerUniqueness(layers); err != nil {
		return err
	}
	var flexCount, baseCount, bufferCount int
	for _, ls := range layers {
		if err := validateLayerEnums(ls); err != nil {
			return err
		}
		if err := validateLayerRoleCombo(ls); err != nil {
			return err
		}
		if slices.Contains(ls.Roles, RoleFlex) {
			flexCount++
		}
		if slices.Contains(ls.Roles, RoleBase) {
			baseCount++
		}
		if slices.Contains(ls.Roles, RoleBuffer) {
			bufferCount++
		}
	}
	return validateRoleCounts(flexCount, baseCount, bufferCount)
}

// validateLayerUniqueness rejects duplicate LayerTypes across specs: a layer
// appearing twice would double-count its state and produce ambiguous
// allocations.
func validateLayerUniqueness(layers []LayerSpec) error {
	seen := make(map[LayerType]bool, len(layers))
	for _, ls := range layers {
		if seen[ls.Type] {
			return fmt.Errorf("duplicate layer type %q", ls.Type)
		}
		seen[ls.Type] = true
	}
	return nil
}

// validateLayerEnums rejects unknown layer types and roles. A typo'd role
// would otherwise pass silently: the layer's commitments still count toward
// existing coverage but the layer could never receive an allocation, a
// silent degradation of the plan.
func validateLayerEnums(ls LayerSpec) error {
	if err := ls.Type.Validate(); err != nil {
		return fmt.Errorf("layer type: %w", err)
	}
	for _, r := range ls.Roles {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("layer %q: role: %w", ls.Type, err)
		}
	}
	return nil
}

// validateLayerRoleCombo permits single-role layers and the base+buffer merge
// only. RoleFlex combined with any other role (including all three roles on
// one layer) is rejected: no provider topology merges flex with base or
// buffer, so such a spec indicates a provider implementation bug.
func validateLayerRoleCombo(ls LayerSpec) error {
	if slices.Contains(ls.Roles, RoleFlex) &&
		(slices.Contains(ls.Roles, RoleBase) || slices.Contains(ls.Roles, RoleBuffer)) {
		return fmt.Errorf("layer %q: %s role must not be combined with other roles (only the %s+%s merge is allowed)",
			ls.Type, RoleFlex, RoleBase, RoleBuffer)
	}
	return nil
}

// validateRoleCounts enforces the per-role cardinality rules across the
// whole layer list.
func validateRoleCounts(flexCount, baseCount, bufferCount int) error {
	if flexCount != 1 {
		return fmt.Errorf("exactly one %s layer required, got %d", RoleFlex, flexCount)
	}
	if baseCount > 1 {
		return fmt.Errorf("at most one %s layer allowed, got %d", RoleBase, baseCount)
	}
	if bufferCount > 1 {
		return fmt.Errorf("at most one %s layer allowed, got %d", RoleBuffer, bufferCount)
	}
	return nil
}

// validateLayerStates requires every layer in layers to have an entry in states
// with non-nil ExistingUSDPerHour and ExpiringUSDPerHour. Nil pointers are
// treated as missing data (fail loud: providers must supply explicit zeros).
// A non-nil UtilizationPct must be finite and within [0, 100].
func validateLayerStates(layers []LayerSpec, states map[LayerType]LayerState) error {
	for _, ls := range layers {
		st, ok := states[ls.Type]
		if !ok {
			return fmt.Errorf("layer %q: missing state entry (provider data incomplete)", ls.Type)
		}
		if st.ExistingUSDPerHour == nil {
			return fmt.Errorf("layer %q: ExistingUSDPerHour is nil; provider must supply explicit zero if none", ls.Type)
		}
		if st.ExpiringUSDPerHour == nil {
			return fmt.Errorf("layer %q: ExpiringUSDPerHour is nil; provider must supply explicit zero if none", ls.Type)
		}
		if err := validateUtilizationPct(ls.Type, st.UtilizationPct); err != nil {
			return err
		}
	}
	return validateNoUnknownStateKeys(layers, states)
}

// validateNoUnknownStateKeys rejects LayerStates entries whose key is not in
// the supported layer list. Such entries would be silently excluded from
// existing-coverage accounting, understating E and OVERSTATING the purchase
// gap (a money-path bug, not a tolerable extra).
func validateNoUnknownStateKeys(layers []LayerSpec, states map[LayerType]LayerState) error {
	known := make(map[LayerType]bool, len(layers))
	for _, ls := range layers {
		known[ls.Type] = true
	}
	for key := range states {
		if !known[key] {
			return fmt.Errorf("layer_states contains unknown layer %q not present in the supported layers; its commitments would be excluded from coverage accounting", key)
		}
	}
	return nil
}

// validateUtilizationPct rejects non-finite (NaN/Inf) or out-of-range
// utilization percentages. A NaN would otherwise compare false against the
// reshape threshold and silently skip reshape detection; that is a data bug
// that must abort the run, not degrade it. nil means "not measured" and is
// valid (handled downstream with an informational hold).
func validateUtilizationPct(layer LayerType, pct *float64) error {
	if pct == nil {
		return nil
	}
	if math.IsNaN(*pct) || math.IsInf(*pct, 0) {
		return fmt.Errorf("layer %q: UtilizationPct must be finite, got %g", layer, *pct)
	}
	if *pct < 0 || *pct > 100 {
		return fmt.Errorf("layer %q: UtilizationPct %g must be in [0, 100]", layer, *pct)
	}
	return nil
}

// findLayerForRole returns the first LayerType that carries the given role.
func findLayerForRole(layers []LayerSpec, role LayerRole) (LayerType, bool) {
	for _, ls := range layers {
		if slices.Contains(ls.Roles, role) {
			return ls.Type, true
		}
	}
	return "", false
}

// computeNetExisting converts all layer states to exact *big.Rat values and
// returns each layer's net (existing - expiring) plus the grand total. A
// layer whose expiring amount exceeds its existing amount is an inconsistent
// provider snapshot (expiring is defined as a share of existing) and aborts
// the run with an explicit error rather than being silently clamped.
func computeNetExisting(layers []LayerSpec, states map[LayerType]LayerState) (map[LayerType]*big.Rat, *big.Rat, error) {
	total := new(big.Rat)
	nets := make(map[LayerType]*big.Rat, len(layers))
	for _, ls := range layers {
		st := states[ls.Type]
		existing, err := ratFromFloat(string(ls.Type)+".ExistingUSDPerHour", *st.ExistingUSDPerHour)
		if err != nil {
			return nil, nil, err
		}
		expiring, err := ratFromFloat(string(ls.Type)+".ExpiringUSDPerHour", *st.ExpiringUSDPerHour)
		if err != nil {
			return nil, nil, err
		}
		net := new(big.Rat).Sub(existing, expiring)
		if net.Sign() < 0 {
			return nil, nil, fmt.Errorf("%s: ExpiringUSDPerHour (%s) exceeds ExistingUSDPerHour (%s); inconsistent provider snapshot",
				ls.Type, fmtRatUSD(expiring), fmtRatUSD(existing))
		}
		nets[ls.Type] = net
		total.Add(total, net)
	}
	return nets, total, nil
}

// computeBaselineAndTarget derives the low-water baseline and the coverage
// target (low-water * TargetCoveragePct / 100) as exact *big.Rat values.
// Returns an error if the float64 inputs are invalid (NaN, Inf, or negative).
func computeBaselineAndTarget(in *AllocationInput) (lowWater, target *big.Rat, err error) {
	lowWater, err = ratFromFloat("low_water_usd_per_hour", *in.Baseline.LowWaterUSDPerHour)
	if err != nil {
		return nil, nil, err
	}
	pct, err := ratFromFloat("target_coverage_pct", in.Config.TargetCoveragePct)
	if err != nil {
		return nil, nil, err
	}
	hundred := new(big.Rat).SetInt64(100)
	target = new(big.Rat).Mul(lowWater, new(big.Rat).Quo(pct, hundred))
	return lowWater, target, nil
}

// baselineUnavailableHold returns a Hold result explaining that the baseline
// could not be computed. This is a designed in-band no-op, not a silent fallback.
func baselineUnavailableHold(in *AllocationInput) *AllocateResult {
	flexLayer, _ := findLayerForRole(in.Layers, RoleFlex)
	return &AllocateResult{
		Holds: []PlannedAction{
			{
				Action:      ActionHold,
				Layer:       flexLayer,
				Rationale:   "baseline unavailable: LowWaterUSDPerHour is nil; cannot compute allocation gap",
				DataSources: in.DataSources,
			},
		},
	}
}

// gapBelowMinHold returns a Hold result explaining that the target is already
// met (accounting for in-flight commitment). Concrete numbers (low-water,
// target %, existing, in-flight, gap) are embedded in the rationale so
// operators can distinguish "gap closed by prior commitment" from "truly met".
func gapBelowMinHold(gap, lowWater, target, existingTotal, inFlight *big.Rat, in *AllocationInput) *AllocateResult {
	flexLayer, _ := findLayerForRole(in.Layers, RoleFlex)
	rationale := fmt.Sprintf(
		"target already met: low_water=%s, target=%.2f%%, existing=%s, in_flight=%s, target_commitment=%s, gap=%s <= min_allocatable=%s",
		fmtRatUSD(lowWater), in.Config.TargetCoveragePct, fmtRatUSD(existingTotal), fmtRatUSD(inFlight),
		fmtRatUSD(target), fmtRatUSD(gap), fmtRatUSD(minAllocatableGap()),
	)
	return &AllocateResult{
		Holds: []PlannedAction{
			{
				Action:      ActionHold,
				Layer:       flexLayer,
				Rationale:   rationale,
				DataSources: in.DataSources,
			},
		},
	}
}

// buildResult assembles the full AllocateResult after the gap has been
// confirmed to exceed the minimum threshold. Buffer maintenance actions
// (reshapes and their informational holds) are detected by the caller,
// independently of the gap, and merged here.
func buildResult(in *AllocationInput, lowWater, target, existingTotal, gap *big.Rat, nets map[LayerType]*big.Rat, reshapes, maintHolds []PlannedAction) (*AllocateResult, error) {
	allocs, err := splitGap(in, gap, nets, lowWater, target, existingTotal)
	if err != nil {
		return nil, err
	}
	allocs, err = applyCapGuardrail(allocs, in.Config.MaxHourlyCommitPerRun)
	if err != nil {
		return nil, err
	}
	allocs, skipHolds := dropSubMinimumAllocations(allocs, in.DataSources)
	allocs, reshapes, truncHolds := truncateToMaxActions(allocs, reshapes, in.Config.MaxActionsPerRun, in.LayerStates, in.DataSources)
	holds := append(maintHolds, skipHolds...)
	holds = append(holds, truncHolds...)
	return &AllocateResult{
		Allocations: allocs,
		Reshapes:    reshapes,
		Holds:       holds,
	}, nil
}

// dropSubMinimumAllocations removes allocations whose amount fell below the
// minimum allocatable threshold after splitting and cap scaling, emitting an
// informational Hold for each so the skipped amount is never silently lost.
// Amounts exactly at the threshold are kept.
func dropSubMinimumAllocations(allocs []Allocation, dataSources []string) ([]Allocation, []PlannedAction) {
	minGap := minAllocatableGap()
	var kept []Allocation
	var holds []PlannedAction
	for _, a := range allocs {
		if a.GapUSDPerHour.Cmp(minGap) < 0 {
			holds = append(holds, PlannedAction{
				Action: ActionHold,
				Layer:  a.Layer,
				Rationale: fmt.Sprintf("allocation of %s on layer %s is below minimum allocatable amount; skipped (minimum %s)",
					fmtRatUSD(a.GapUSDPerHour), a.Layer, fmtRatUSD(minGap)),
				DataSources: dataSources,
			})
			continue
		}
		kept = append(kept, a)
	}
	return kept, holds
}

// truncateToMaxActions deterministically reduces allocs and reshapes to at
// most max total actions. Reshapes are preferred over purchases (they are
// risk-reducing with no new spend). Among purchases, the largest GapUSDPerHour
// are kept; the smallest are dropped. Every dropped action is returned as an
// ActionHold so no money action is silently lost.
//
// Priority rules (per Q-TRUNC):
//  1. Reshapes are always retained first. If reshapes alone exceed max,
//     keep the most urgent (lowest utilization in layerStates; nil = treated
//     as 100 so it is deprioritized), hold the rest.
//  2. Remaining slots (max - len(reshapes)) go to purchases sorted by
//     GapUSDPerHour descending; ties broken by layer name for stable ordering.
func truncateToMaxActions(
	allocs []Allocation,
	reshapes []PlannedAction,
	maxActions int,
	layerStates map[LayerType]LayerState,
	dataSources []string,
) ([]Allocation, []PlannedAction, []PlannedAction) {
	if len(allocs)+len(reshapes) <= maxActions {
		return allocs, reshapes, nil
	}
	// Case: reshapes alone fill or exceed the cap. Every dropped purchase must
	// still leave an audit Hold (Q-TRUNC), so ALL allocations are converted to
	// holds here via purchaseCap 0; none is silently discarded.
	//
	// INVARIANT: this branch is currently unreachable in production.
	// validateRoleCounts caps buffer layers at 1 (so at most one reshape) and
	// MaxActionsPerRun is validated > 0, so len(reshapes) < maxActions always
	// holds. truncateReshapes' utilization sort and layerUtilPctOrMax's
	// nil->100 path are therefore defensive-only, kept for future multi-buffer
	// topologies.
	if len(reshapes) >= maxActions {
		keptReshapes, reshapeHolds := truncateReshapes(reshapes, maxActions, layerStates, dataSources)
		_, allocHolds := truncatePurchases(allocs, 0, maxActions, dataSources)
		return nil, keptReshapes, append(allocHolds, reshapeHolds...)
	}
	// Normal case: reshapes fit; truncate the lowest-gap purchases.
	purchaseCap := maxActions - len(reshapes)
	kept, holds := truncatePurchases(allocs, purchaseCap, maxActions, dataSources)
	return kept, reshapes, holds
}

// truncateReshapes keeps the most urgent (lowest utilization) reshapes up to
// maxActions, emitting an ActionHold for each dropped one. Split from
// truncateToMaxActions to keep cyclomatic complexity within the project limit.
func truncateReshapes(reshapes []PlannedAction, maxActions int, layerStates map[LayerType]LayerState, dataSources []string) ([]PlannedAction, []PlannedAction) {
	sorted := make([]PlannedAction, len(reshapes))
	copy(sorted, reshapes)
	slices.SortStableFunc(sorted, func(a, b PlannedAction) int {
		au := layerUtilPctOrMax(layerStates, a.Layer)
		bu := layerUtilPctOrMax(layerStates, b.Layer)
		if au < bu {
			return -1
		}
		if au > bu {
			return 1
		}
		if a.Layer < b.Layer {
			return -1
		}
		if a.Layer > b.Layer {
			return 1
		}
		return 0
	})
	var holds []PlannedAction
	for _, r := range sorted[maxActions:] {
		holds = append(holds, PlannedAction{
			Action:      ActionHold,
			Layer:       r.Layer,
			Rationale:   fmt.Sprintf("deferred to a later run: max_actions_per_run=%d reached (dropped reshape on %s)", maxActions, r.Layer),
			DataSources: dataSources,
		})
	}
	return sorted[:maxActions], holds
}

// truncatePurchases keeps the largest-GapUSDPerHour allocations up to
// purchaseCap, emitting an ActionHold for each dropped one. Split from
// truncateToMaxActions to keep cyclomatic complexity within the project limit.
func truncatePurchases(allocs []Allocation, purchaseCap, maxActions int, dataSources []string) ([]Allocation, []PlannedAction) {
	sorted := make([]Allocation, len(allocs))
	copy(sorted, allocs)
	slices.SortStableFunc(sorted, func(a, b Allocation) int {
		// Descending by GapUSDPerHour (largest first).
		cmp := b.GapUSDPerHour.Cmp(a.GapUSDPerHour)
		if cmp != 0 {
			return cmp
		}
		// Stable tie-break by layer name.
		if a.Layer < b.Layer {
			return -1
		}
		if a.Layer > b.Layer {
			return 1
		}
		return 0
	})
	var holds []PlannedAction
	for _, a := range sorted[purchaseCap:] {
		holds = append(holds, PlannedAction{
			Action: ActionHold,
			Layer:  a.Layer,
			Rationale: fmt.Sprintf(
				"deferred to a later run: max_actions_per_run=%d reached (dropped %s on %s)",
				maxActions, fmtRatUSD(a.GapUSDPerHour), a.Layer,
			),
			DataSources: dataSources,
		})
	}
	return sorted[:purchaseCap], holds
}

// layerUtilPctOrMax returns the UtilizationPct for the given layer from
// layerStates, or 100 when absent or nil. Treating unknown utilization as
// 100 (fully healthy) deprioritizes it when sorting reshapes by urgency,
// so confirmed low-utilization layers are retained over unmeasured ones.
func layerUtilPctOrMax(states map[LayerType]LayerState, layer LayerType) float64 {
	if st, ok := states[layer]; ok && st.UtilizationPct != nil {
		return *st.UtilizationPct
	}
	return 100
}

// splitGap distributes gap across the base, buffer, and flex roles. It handles
// the Azure merged-layer case where a single layer carries both RoleBase and
// RoleBuffer. When BufferFraction > 0 but no layer carries RoleBuffer, it
// returns an explicit error rather than emitting an allocation aimed at a
// nonexistent layer (fail loud: a purchase directive must always name a real
// layer). BufferFraction == 0 with no buffer layer is valid: the buffer gap is
// zero and nothing is allocated to it.
func splitGap(in *AllocationInput, gap *big.Rat, nets map[LayerType]*big.Rat, lowWater, target, existingTotal *big.Rat) ([]Allocation, error) {
	bufferFracRat, err := ratFromFloat("buffer_fraction", in.Config.BufferFraction)
	if err != nil {
		return nil, err
	}
	bufferGap := new(big.Rat).Mul(gap, bufferFracRat)
	coreGap := new(big.Rat).Sub(gap, bufferGap)

	flexLayer, _ := findLayerForRole(in.Layers, RoleFlex)
	baseLayer, hasBase := findLayerForRole(in.Layers, RoleBase)
	bufferLayer, hasBuffer := findLayerForRole(in.Layers, RoleBuffer)
	if bufferGap.Sign() > 0 && !hasBuffer {
		return nil, fmt.Errorf("buffer_fraction %g > 0 but no buffer-role layer configured", in.Config.BufferFraction)
	}

	baseGap, baseNote, err := computeBaseGap(coreGap, hasBase, in.Baseline, baseLayer, nets)
	if err != nil {
		return nil, err
	}
	flexGap := new(big.Rat).Sub(coreGap, baseGap)

	ctx := allocationContext{
		lowWater:    lowWater,
		target:      target,
		existing:    existingTotal,
		gap:         gap,
		baseNote:    baseNote,
		dataSources: in.DataSources,
		cfg:         in.Config,
	}
	return assembleAllocations(&ctx, baseGap, flexGap, bufferGap, baseLayer, flexLayer, bufferLayer, hasBase), nil
}

// allocationContext bundles shared values passed to assembleAllocations so the
// signature stays within a readable parameter count.
type allocationContext struct {
	lowWater    *big.Rat
	target      *big.Rat
	existing    *big.Rat
	gap         *big.Rat
	baseNote    string
	dataSources []string
	cfg         LadderConfig
}

// computeBaseGap determines how much of the core gap should be directed to the
// base layer. When there is no base layer, or the stable baseline is unknown,
// the entire core gap routes to flex (explainable degradation: flex covers
// everything base covers, at a slightly lower discount). The two conditions
// produce distinct rationale notes so the approval email states the actual
// reason.
func computeBaseGap(coreGap *big.Rat, hasBase bool, baseline UsageBaseline, baseLayer LayerType, nets map[LayerType]*big.Rat) (*big.Rat, string, error) {
	if !hasBase {
		return new(big.Rat), "no base layer configured; routing all core gap to flex", nil
	}
	if baseline.StableUSDPerHour == nil {
		return new(big.Rat), "stable usage unknown; routing all core gap to flex", nil
	}
	stableRat, err := ratFromFloat("stable_usd_per_hour", *baseline.StableUSDPerHour)
	if err != nil {
		return nil, "", err
	}
	existingBaseNet := nets[baseLayer]
	if existingBaseNet == nil {
		existingBaseNet = new(big.Rat)
	}
	stableLeft := new(big.Rat).Sub(stableRat, existingBaseNet)
	if stableLeft.Sign() < 0 {
		stableLeft = new(big.Rat) // already at or above stable level
	}
	if coreGap.Cmp(stableLeft) <= 0 {
		return new(big.Rat).Set(coreGap), "", nil
	}
	return new(big.Rat).Set(stableLeft), "", nil
}

// assembleAllocations builds the Allocation slice, handling the Azure
// merged-layer case (same layer carries both RoleBase and RoleBuffer).
func assembleAllocations(ctx *allocationContext, baseGap, flexGap, bufferGap *big.Rat, baseLayer, flexLayer, bufferLayer LayerType, hasBase bool) []Allocation {
	var allocs []Allocation
	azureMerge := hasBase && bufferLayer == baseLayer

	if azureMerge {
		mergedGap := new(big.Rat).Add(baseGap, bufferGap)
		if mergedGap.Sign() > 0 {
			// The base routing note (e.g. "stable usage unknown") explains why
			// the BASE share is zero; it belongs on the flex allocation that
			// received the rerouted core gap, never on the merged base+buffer
			// allocation. When baseGap > 0 the note is empty anyway.
			allocs = append(allocs, mkAllocation(ctx, bufferLayer, mergedGap, "base+buffer", ""))
		}
	} else {
		if hasBase && baseGap.Sign() > 0 {
			allocs = append(allocs, mkAllocation(ctx, baseLayer, baseGap, "base", ctx.baseNote))
		}
		if bufferGap.Sign() > 0 {
			allocs = append(allocs, mkAllocation(ctx, bufferLayer, bufferGap, "buffer", ""))
		}
	}
	if flexGap.Sign() > 0 {
		allocs = append(allocs, mkAllocation(ctx, flexLayer, flexGap, "flex", flexRoutingNote(ctx.baseNote, hasBase, baseGap)))
	}
	return allocs
}

// flexRoutingNote propagates the base routing note to the flex allocation when
// the base gap is zero or no base layer exists (e.g. "stable usage unknown;
// routing all core gap to flex"). Returns "" when the note does not apply.
// Split out of assembleAllocations to keep its cyclomatic complexity within
// the pre-commit hook limit.
func flexRoutingNote(baseNote string, hasBase bool, baseGap *big.Rat) string {
	if baseNote != "" && (!hasBase || baseGap.Sign() == 0) {
		return baseNote
	}
	return ""
}

// mkAllocation constructs a single Allocation with a self-explanatory rationale
// that embeds the concrete numbers used in the decision.
func mkAllocation(ctx *allocationContext, layer LayerType, layerGap *big.Rat, role, extraNote string) Allocation {
	detail := ""
	if extraNote != "" {
		detail = "; " + extraNote
	}
	rationale := fmt.Sprintf(
		"%s layer: low_water=%s, target=%.2f%%, existing=%s, target_commitment=%s, total_gap=%s, %s_gap=%s, buffer_fraction=%.2f%s",
		role,
		fmtRatUSD(ctx.lowWater),
		ctx.cfg.TargetCoveragePct,
		fmtRatUSD(ctx.existing),
		fmtRatUSD(ctx.target),
		fmtRatUSD(ctx.gap),
		role,
		fmtRatUSD(layerGap),
		ctx.cfg.BufferFraction,
		detail,
	)
	return Allocation{
		Layer:         layer,
		GapUSDPerHour: layerGap,
		Rationale:     rationale,
		DataSources:   ctx.dataSources,
	}
}

// applyCapGuardrail scales all allocations proportionally when the total
// exceeds Config.MaxHourlyCommitPerRun. Returns the original slice unchanged
// when no cap is configured or the total is within the cap.
func applyCapGuardrail(allocs []Allocation, maxPerRun *float64) ([]Allocation, error) {
	if maxPerRun == nil || len(allocs) == 0 {
		return allocs, nil
	}
	capRat, err := ratFromFloat("max_hourly_commit_per_run", *maxPerRun)
	if err != nil {
		return nil, err
	}
	total := new(big.Rat)
	for _, a := range allocs {
		total.Add(total, a.GapUSDPerHour)
	}
	if total.Sign() <= 0 || capRat.Cmp(total) >= 0 {
		return allocs, nil
	}
	scale := new(big.Rat).Quo(capRat, total)
	scalePct, _ := new(big.Rat).Mul(new(big.Rat).Set(scale), new(big.Rat).SetInt64(100)).Float64()
	scaled := make([]Allocation, len(allocs))
	for i, a := range allocs {
		scaledGap := new(big.Rat).Mul(a.GapUSDPerHour, scale)
		scaled[i] = Allocation{
			Layer:         a.Layer,
			GapUSDPerHour: scaledGap,
			Rationale: a.Rationale + fmt.Sprintf("; capped by max_hourly_commit_per_run: scaled to %s (%.2f%% of requested)",
				fmtRatUSD(scaledGap), scalePct),
			DataSources: a.DataSources,
		}
	}
	return scaled, nil
}

// detectReshapes examines every RoleBuffer layer that has existing
// commitments. When utilization is below the threshold, it emits an
// ActionReshape. When utilization is nil, it emits an informational
// ActionHold (explainable: no data, no decision). Buffer layers with zero
// existing commitments are skipped entirely: there is nothing to reshape,
// and a fresh account must not receive noise holds or nonsense reshape
// recommendations for empty layers.
func detectReshapes(layers []LayerSpec, states map[LayerType]LayerState, threshold float64, dataSources []string) (reshapes, holds []PlannedAction) {
	for _, ls := range layers {
		if !slices.Contains(ls.Roles, RoleBuffer) {
			continue
		}
		st := states[ls.Type]
		// ExistingUSDPerHour is guaranteed non-nil by validateLayerStates.
		if *st.ExistingUSDPerHour == 0 {
			continue // empty buffer layer: nothing to reshape, no noise
		}
		if st.UtilizationPct == nil {
			holds = append(holds, PlannedAction{
				Action:      ActionHold,
				Layer:       ls.Type,
				Rationale:   fmt.Sprintf("buffer layer %s: utilization unknown; reshape not evaluated", ls.Type),
				DataSources: dataSources,
			})
			continue
		}
		if *st.UtilizationPct < threshold {
			reshapes = append(reshapes, PlannedAction{
				Action: ActionReshape,
				Layer:  ls.Type,
				Rationale: fmt.Sprintf(
					"buffer layer %s: utilization %.1f%% is below threshold %.1f%%; reshape recommended",
					ls.Type, *st.UtilizationPct, threshold,
				),
				DataSources: dataSources,
			})
		}
	}
	return reshapes, holds
}
