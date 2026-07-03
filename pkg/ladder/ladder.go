package ladder

import (
	"fmt"
	"math/big"
	"time"
)

// TrancheInput carries everything BuildTranches needs to turn a set of
// Allocations into immediate purchase actions and future ramp tranches. All
// time and ID generation is injected so BuildTranches is deterministic and
// free of hidden side effects.
type TrancheInput struct {
	// Config provides the ramp schedule and is re-validated by BuildTranches.
	Config *LadderConfig
	// RunID stamps every produced Tranche row for run linkage. Required.
	RunID string
	// Term is the commitment term (e.g. "1yr", "3yr") passed to each buy-now
	// purchase action. The caller selects the term; v1 uses a single term for
	// all allocations.
	Term string
	// PaymentOption is the payment structure (e.g. "no-upfront") passed to
	// each buy-now purchase action.
	PaymentOption string
	// NewID is called once per produced Tranche to assign a unique identifier.
	// Callers may inject a UUID function, a sequential counter, or any other
	// scheme. Must return a non-empty string on every call.
	NewID func() string
	// Now is the wall-clock time of the run. BuildTranches never calls
	// time.Now internally; callers inject the clock for testability and
	// audit-trail accuracy.
	Now time.Time
	// Allocations is the full set of per-layer sizing decisions from Allocate.
	// Each allocation is split across all ramp steps in the schedule.
	Allocations []Allocation
}

// TrancheResult is the output of BuildTranches.
//
// When the ramp schedule contains no step with AfterDays == 0 (a fully-
// delayed ramp), BuyNow will be empty and all commitment activity appears as
// future Tranches. This is a valid configuration; callers should document a
// fully-delayed ramp explicitly so operators are not surprised by the absence
// of an immediate purchase.
type TrancheResult struct {
	// BuyNow holds ActionPurchase actions for the immediate (AfterDays == 0)
	// ramp step, if one exists in the schedule.
	BuyNow []PlannedAction
	// Tranches holds future scheduled ramp rows for every step with
	// AfterDays > 0. Each row has status TrancheStatusScheduled and a
	// FireAfter timestamp set to Now + AfterDays.
	Tranches []Tranche
}

// BuildTranches turns each Allocation into (a) buy-now PlannedActions for the
// ramp step with AfterDays == 0, if present, and (b) future Tranche rows for
// every ramp step with AfterDays > 0, staggered over the ramp schedule so
// commitment terms expire at different times instead of bunching.
//
// Step amounts are computed in exact big.Rat arithmetic. Every step amount is
// clamped to the remaining unallocated gap and the last step receives the
// exact leftover, so the total across all produced items reconstructs the gap
// exactly -- no cent lost, duplicated, or over-allocated -- for every schedule
// that passes RampSchedule.Validate, despite the binary-float representation
// of step fractions. Steps whose computed amount is zero (possible when the
// clamp floors a step after earlier fractions consumed the whole gap) are
// skipped entirely without affecting total exactness.
//
// BuildTranches performs no I/O and never calls time.Now.
func BuildTranches(in *TrancheInput) (*TrancheResult, error) {
	if err := validateTrancheInput(in); err != nil {
		return nil, err
	}
	return buildTrancheResult(in)
}

// validateTrancheInput checks all required fields on TrancheInput. Returns a
// descriptive error naming the offending field on any violation.
func validateTrancheInput(in *TrancheInput) error {
	if in == nil {
		return fmt.Errorf("tranche input must not be nil")
	}
	if in.Config == nil {
		return fmt.Errorf("config must not be nil")
	}
	if err := in.Config.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if in.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if in.Now.IsZero() {
		return fmt.Errorf("now must not be zero (inject the run wall-clock time)")
	}
	if in.NewID == nil {
		return fmt.Errorf("new_id must not be nil (inject an ID generator)")
	}
	if in.Term == "" {
		return fmt.Errorf("term is required (e.g. \"1yr\" or \"3yr\")")
	}
	if in.PaymentOption == "" {
		return fmt.Errorf("payment_option is required (e.g. \"no-upfront\")")
	}
	return validateInputAllocations(in.Allocations)
}

// validateInputAllocations checks each allocation for a recognized layer, a
// positive gap, and a non-empty rationale. An empty allocation slice is valid
// (BuildTranches returns an empty result) but each element must be
// well-formed. Every Allocation produced by Allocate carries a rationale, so
// the rationale check only catches direct API misuse; it still fails loud
// because the rationale feeds money-path audit trails and approval emails.
func validateInputAllocations(allocs []Allocation) error {
	for i, a := range allocs {
		if err := a.Layer.Validate(); err != nil {
			return fmt.Errorf("allocation[%d]: layer: %w", i, err)
		}
		if a.GapUSDPerHour == nil || a.GapUSDPerHour.Sign() <= 0 {
			return fmt.Errorf("allocation[%d]: gap_usd_per_hour must be positive", i)
		}
		if a.Rationale == "" {
			return fmt.Errorf("allocation[%d]: rationale is required (money-path auditability)", i)
		}
	}
	return nil
}

// buildTrancheResult iterates over all allocations and splits each one across
// the configured ramp schedule steps, then verifies that the injected NewID
// produced a unique, non-empty ID for every tranche of the run.
func buildTrancheResult(in *TrancheInput) (*TrancheResult, error) {
	steps := in.Config.Ramp.Steps
	result := &TrancheResult{}
	for _, alloc := range in.Allocations {
		if err := processAllocation(in, alloc, steps, result); err != nil {
			return nil, err
		}
	}
	if err := validateTrancheIDs(result.Tranches); err != nil {
		return nil, err
	}
	return result, nil
}

// validateTrancheIDs verifies that every produced tranche carries a unique,
// non-empty ID. SaveTranches upserts by tranche ID, so a duplicate or empty
// ID from a misbehaving injected NewID would silently collapse scheduled
// purchases at persistence time -- money-path data loss. Fail loud naming
// the offender instead. (An empty ID is already rejected earlier by
// Tranche.Validate inside buildFutureTranche; the check here is kept so this
// function is a self-contained guarantee over the whole batch.)
func validateTrancheIDs(tranches []Tranche) error {
	seen := make(map[string]struct{}, len(tranches))
	for i := range tranches {
		tr := &tranches[i]
		if tr.ID == "" {
			return fmt.Errorf("tranche[%d] (layer %s, step %d): NewID returned an empty ID", i, tr.Layer, tr.StepIndex)
		}
		if _, dup := seen[tr.ID]; dup {
			return fmt.Errorf(
				"tranche[%d] (layer %s, step %d): NewID returned duplicate ID %q; upsert-by-ID would silently drop a scheduled purchase",
				i, tr.Layer, tr.StepIndex, tr.ID)
		}
		seen[tr.ID] = struct{}{}
	}
	return nil
}

// processAllocation splits one allocation across all ramp steps. Each step
// amount is clamped to the remaining unallocated gap (see computeStepAmount)
// and the last step receives the exact leftover, so the sum of all produced
// amounts equals the allocation gap exactly for every Validate-passing
// schedule, regardless of binary-float rounding in the step fractions. Steps
// with a zero computed amount are skipped; priorSum is still updated (adding
// zero is a no-op) to keep the remainder arithmetic consistent.
func processAllocation(in *TrancheInput, alloc Allocation, steps []RampStep, result *TrancheResult) error {
	nSteps := len(steps)
	priorSum := new(big.Rat)
	for i, step := range steps {
		isLast := i == nSteps-1
		amount := computeStepAmount(alloc.GapUSDPerHour, step.Fraction, isLast, priorSum)
		if !isLast {
			// Accumulate before the zero-skip check: adding a zero amount is a
			// no-op on priorSum, but always updating keeps the remainder
			// arithmetic consistent regardless of which earlier steps were
			// skipped.
			priorSum.Add(priorSum, amount)
		}
		if amount.Sign() == 0 {
			// Zero amounts are skipped to avoid zero-amount purchases or
			// tranches (PlannedAction.Validate would reject them). A zero can
			// arise when earlier steps' exact rational fractions already
			// consumed the whole gap and the clamp in computeStepAmount
			// floored this step at the remaining zero. The clamp keeps the
			// grand total exactly equal to the gap regardless of skips.
			continue
		}
		if err := appendStep(in, alloc, step, i, nSteps, amount, result); err != nil {
			return err
		}
	}
	return nil
}

// appendStep routes one non-zero step amount to BuyNow (AfterDays == 0) or
// Tranches (AfterDays > 0).
func appendStep(in *TrancheInput, alloc Allocation, step RampStep, stepIdx, nSteps int, amount *big.Rat, result *TrancheResult) error {
	if step.AfterDays == 0 {
		action, err := buildBuyNowAction(in, alloc, step, stepIdx, nSteps, amount)
		if err != nil {
			return err
		}
		result.BuyNow = append(result.BuyNow, action)
		return nil
	}
	tr, err := buildFutureTranche(in, alloc, step, stepIdx, amount)
	if err != nil {
		return err
	}
	result.Tranches = append(result.Tranches, tr)
	return nil
}

// computeStepAmount returns the exact big.Rat amount for one ramp step,
// clamped to the remaining unallocated gap.
//
// For all steps except the last: amount = min(gap * fraction, remaining),
// where remaining = max(gap - priorSum, 0) and fraction is converted to a
// rational via big.Rat.SetFloat64 at the boundary (same discipline as
// ratFromFloat in allocate.go). RampSchedule.Validate rejects NaN and bounds
// fractions to (0, 1], so NaN/Inf/negative cannot occur here.
//
// For the last step: amount = remaining (the exact leftover, floored at 0).
//
// The clamp matters because RampSchedule.Validate accepts fraction sets whose
// float64 sum is within rampSumEpsilon of 1.0 but whose exact rational sum
// slightly exceeds 1 (e.g. {0.5, 0.5000000008, 1e-10}). Without clamping, the
// last-step remainder would go negative and BuildTranches would reject a
// config its own validator accepted. With the clamp, the sum of all step
// amounts equals the allocation gap exactly for every Validate-passing
// schedule: no cent is ever lost, duplicated, or over-allocated.
func computeStepAmount(gap *big.Rat, fraction float64, isLast bool, priorSum *big.Rat) *big.Rat {
	remaining := new(big.Rat).Sub(gap, priorSum)
	if remaining.Sign() < 0 {
		remaining = new(big.Rat)
	}
	if isLast {
		return remaining
	}
	amount := new(big.Rat).Mul(gap, new(big.Rat).SetFloat64(fraction))
	if amount.Cmp(remaining) > 0 {
		return remaining
	}
	return amount
}

// stepRationale returns a human-readable rationale string for a buy-now action
// or future tranche, embedding the step position, fraction percentage, computed
// amount, total gap, and the allocation's original rationale. The percentage
// uses %.4g so small fractions render meaningfully (0.4% instead of the
// misleading "0%" that fixed zero-decimal rounding would produce on a
// positive purchase).
func stepRationale(alloc Allocation, stepIdx, totalSteps int, fraction float64, amount, gap *big.Rat) string {
	return fmt.Sprintf(
		"ramp step %d/%d (%.4g%%): %s of %s total. %s",
		stepIdx+1, totalSteps, fraction*100,
		fmtRatUSD(amount), fmtRatUSD(gap),
		alloc.Rationale,
	)
}

// buildBuyNowAction creates a validated PlannedAction (ActionPurchase) for a
// ramp step with AfterDays == 0. Returns an error if the produced action fails
// its own Validate check, which would indicate a bug in the caller-supplied
// input (e.g. an empty Term).
func buildBuyNowAction(in *TrancheInput, alloc Allocation, step RampStep, stepIdx, nSteps int, amount *big.Rat) (PlannedAction, error) {
	rationale := stepRationale(alloc, stepIdx, nSteps, step.Fraction, amount, alloc.GapUSDPerHour)
	action := PlannedAction{
		Action:           ActionPurchase,
		Layer:            alloc.Layer,
		AmountUSDPerHour: new(big.Rat).Set(amount),
		Term:             in.Term,
		PaymentOption:    in.PaymentOption,
		Rationale:        rationale,
		DataSources:      alloc.DataSources,
	}
	if err := action.Validate(); err != nil {
		return PlannedAction{}, fmt.Errorf("buy-now action (layer %s, step %d): %w", alloc.Layer, stepIdx, err)
	}
	return action, nil
}

// buildFutureTranche creates a validated Tranche for a ramp step with
// AfterDays > 0. FireAfter is set to Now + AfterDays days. The ID is assigned
// by calling in.NewID(). Layer, Term, and PaymentOption are stamped so the
// tranche is fully self-describing: the executor that fires it must be able
// to place the purchase without consulting the parent run's plan (two
// allocations with equal gaps on different layers stay distinguishable from
// the tranche row alone). Returns an error if the produced tranche fails its
// own Validate check.
func buildFutureTranche(in *TrancheInput, alloc Allocation, step RampStep, stepIdx int, amount *big.Rat) (Tranche, error) {
	tr := Tranche{
		ID:               in.NewID(),
		RunID:            in.RunID,
		StepIndex:        stepIdx,
		Status:           TrancheStatusScheduled,
		FireAfter:        in.Now.AddDate(0, 0, step.AfterDays),
		AmountUSDPerHour: amount.RatString(),
		Layer:            alloc.Layer,
		Term:             in.Term,
		PaymentOption:    in.PaymentOption,
	}
	if err := tr.Validate(); err != nil {
		return Tranche{}, fmt.Errorf("tranche (layer %s, step %d): %w", alloc.Layer, stepIdx, err)
	}
	return tr, nil
}
