package recfilter

import (
	"math"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// ApplyCoverage applies coverage percentage to recommendations.
//
// All cost-bearing fields (CommitmentCost, OnDemandCost, EstimatedSavings,
// and for SPs the SavingsPlanDetails.HourlyCommitment) scale by coverage/100
// so the returned Recommendation represents the sized purchase rather than
// AWS's pre-sized proposal. SavingsPercentage is invariant (savings vs
// on-demand ratio) and stays unscaled. Pre-sizing values can still be
// recovered: RecommendedCount holds AWS's pre-sized count for RIs.
//
// drops accumulates per-reason drop counts for the end-of-run summary; pass
// nil to skip tracking. logf receives WARNING lines for anomalous recs
// (nil-safe; pass nil to disable logging).
func ApplyCoverage(recs []common.Recommendation, coverage float64, logf Logf, drops *common.DropSummary) []common.Recommendation {
	if coverage >= 100 {
		return recs
	}
	if coverage <= 0 {
		return []common.Recommendation{}
	}

	ratio := coverage / 100.0
	result := make([]common.Recommendation, 0, len(recs))
	for _rvc := range recs {
		rec := recs[_rvc]
		adjusted := rec

		// For Savings Plans, reduce the hourly commitment instead of count.
		// If Details is the wrong type or a nil pointer (defensive — it
		// should always be a non-nil *SavingsPlanDetails for SP recs),
		// preserve the recommendation at its original values rather than
		// silently dropping it. A missing-Details record is a logged
		// anomaly, not a reason to erase coverage from the run.
		//
		// The nil check matters: an interface holding a typed nil satisfies
		// the assertion, so testing ok alone would send an unscalable rec
		// down the scaling path.
		if common.IsSavingsPlan(rec.Service) {
			if details, ok := rec.Details.(*common.SavingsPlanDetails); ok && details != nil {
				// ScaleRecommendationCosts scales HourlyCommitment along with
				// the cost fields and replaces Details with a scaled copy.
				adjusted = common.ScaleRecommendationCosts(adjusted, ratio)
			} else {
				logf.printf("WARNING: SP recommendation for service %q has missing or unexpected Details (%T); passing through unscaled\n", rec.Service, rec.Details)
			}
			result = append(result, adjusted)
			continue
		}

		// For RIs, reduce the count and scale cost-bearing fields by the
		// DISCRETE count ratio (newCount / rec.Count) rather than the
		// requested ratio. Truncating newCount to an int then multiplying
		// costs by the unrounded ratio desynchronises Count and costs:
		// e.g. rec.Count=3 + ratio=0.5 yields newCount=1 (33% of instances)
		// but costs would scale to 50%, overstating the sized purchase
		// price by ~50%. Mirrors ApplyTargetCoverage / family-NU sizing.
		// rec.Count is guaranteed > 0 here because newCount > 0 implies
		// rec.Count >= 1 (int(0 * ratio) is 0 for any ratio).
		newCount := int(float64(rec.Count) * ratio)
		if newCount > 0 {
			sizedRatio := float64(newCount) / float64(rec.Count)
			adjusted = common.ScaleRecommendationCosts(adjusted, sizedRatio)
			adjusted.Count = newCount
			result = append(result, adjusted)
		} else {
			// No nil guard: DropSummary.Add is nil-receiver safe, and every
			// other drop site in this package relies on that.
			drops.Add(common.DropTargetSizedToZero, 1)
		}
	}
	return result
}

// ApplyTargetCoverage sizes RI/SP recommendations so that projected
// post-purchase COVERAGE lands near targetPct, leaving (100-targetPct)% of
// historical demand on-demand as headroom. See ApplyCoverage for the simpler
// rec.Count-scaled coverage flag; the two are dispatched via cmd's applySizing.
//
// AWS's recommendation count is sized for ~100% coverage of historical demand
// (average instances used per hour). --target-coverage is the lever the
// operator uses to deliberately under-buy that baseline, accepting more
// on-demand spend in exchange for less idle commitment when demand is bursty
// or trending down.
//
// The flag name says "utilization" because the original framing (issue #338)
// was a utilization floor. In practice operators set values like 70 or 80
// expecting coverage near that figure (with utilization staying ~100% on the
// commitments actually purchased), not the over-buy semantics that floor
// produces; see the #338 review discussion for the redirect.
//
// RIs (existing-aware, per-pool, strict-target):
//
//	gap      = targetPct - ExistingCoveragePct           (percentage points)
//	avg      = AverageInstancesUsedPerHour               (instances)
//	n_target = floor(avg * gap / 100)
//
//	The buy is anchored on the pool's own average demand and the absolute
//	gap to target: target%-existing% of avg instances. For example with
//	avg=10, existing=50% and target=80%: gap=30, so n_target=floor(3)=3.
//
//	An earlier version anchored on AWS's rec.Count
//	(floor(rec.Count * gap / (100-existing))). That under-bought when AWS
//	sized rec.Count for less than full coverage, and when CE's org-wide
//	ExistingCoveragePct disagreed with rec.Count's per-account derivation.
//	Both inputs of the current formula come from GetReservationCoverage, so
//	the buy lines up with the AWS console's reservations-coverage report.
//	rec.Count survives only as the denominator of the cost-scaling ratio.
//
//	If gap <= 0 (existing already at/above target) → drop with INFO log.
//	If n_target == 0 (gap too small to fit one RI) → drop with INFO log.
//	If AverageInstancesUsedPerHour <= 0 → pass through (no signal); counted
//	in the per-run skip summary.
//	Projected coverage = ExistingCoveragePct + n_target/avg * 100 (total
//	coverage after the purchase, clamped to 100). Projected utilization =
//	avg/n_target * 100 clamped to 100.
//
//	ExistingCoveragePct is sourced from CE GetReservationCoverage in the
//	same pool; zero means "no signal" and the formula reduces to
//	floor(avg * target/100) — i.e. plain target% of the pool's average
//	hourly demand.
//	For RDS the coverage lookup keys by (region, instance_type, engine).
//	Floor (rather than ceil or round) gives strict "at-most-target"
//	sizing. Pools too small to approximate the target meaningfully
//	should be filtered upstream via --min-pool-size; floor will drop
//	them as zero-count otherwise.
//
//	Pools where CE reports 100% existing coverage but AWS still recommends
//	new RIs (typical when existing RIs are near expiry) are dropped here —
//	the existing coverage is honored strictly. Use --rebuy-window-days to
//	surface those replacements before the cliff.
//
// SPs:
//
//	Scale SavingsPlanDetails.HourlyCommitment and EstimatedSavings by
//	targetPct/100 (the same lever ApplyCoverage's SP branch uses, but with
//	the explicit utilization-target framing). RecommendedUtilization is used
//	only as the no-signal guard: when AWS hasn't returned a projected
//	utilization figure, we pass the rec through unchanged and count it in
//	the skip summary, since we can't sanity-check what the scaled commitment
//	would mean.
//	If RecommendedUtilization <= 0 → pass through; counted in skip summary.
//
// Recs of any other CommitmentType are passed through unmodified (warned
// once per type per run).
//
// drops accumulates per-reason drop counts for the end-of-run summary; pass
// nil to skip tracking. logf receives WARNING/INFO lines (nil-safe; pass nil
// to disable logging).
func ApplyTargetCoverage(recs []common.Recommendation, targetPct float64, logf Logf, drops *common.DropSummary) []common.Recommendation {
	if targetPct <= 0 || targetPct > 100 {
		// Validation ensures we never get here in production, but be defensive
		// so a buggy caller doesn't divide by zero.
		logf.printf("WARNING: ApplyTargetCoverage called with targetPct=%.2f outside (0,100]; returning recs unchanged\n", targetPct)
		return recs
	}

	result := make([]common.Recommendation, 0, len(recs))
	var skipped int
	unsupportedSeen := make(map[common.CommitmentType]bool)

	for i := range recs {
		adjusted, kept, missingSignal, dropReason := applyTargetCoverageOne(recs[i], targetPct, unsupportedSeen, logf)
		if missingSignal {
			skipped++
		}
		if kept {
			result = append(result, adjusted)
		} else if dropReason != "" {
			drops.Add(dropReason, 1)
		}
	}

	if skipped > 0 {
		logf.printf("INFO: --target-coverage=%.1f%% skipped %d of %d recommendations with no utilization signal (passed through unchanged)\n",
			targetPct, skipped, len(recs))
	}

	return result
}

// applyTargetCoverageOne dispatches a single recommendation through the
// appropriate branch. Returns (rec, kept, missingSignal, dropReason):
//   - kept=true → caller appends `rec` (the adjusted or pass-through value).
//   - kept=false → caller drops the rec (only the RI "target unreachable"
//     branches return this; an INFO log already fired).
//   - missingSignal=true → counted toward the end-of-run skip summary.
//   - dropReason is non-empty when kept=false and the drop has a named category.
//
// Split out of ApplyTargetCoverage to keep that function under gocyclo's
// complexity threshold.
func applyTargetCoverageOne(rec common.Recommendation, targetPct float64, unsupportedSeen map[common.CommitmentType]bool, logf Logf) (result common.Recommendation, kept, missingSignal bool, drop string) {
	switch {
	case common.IsSavingsPlan(rec.Service):
		adjusted, ok := applyTargetCoverageSP(rec, targetPct, logf)
		if !ok {
			// SP no-signal: pass through unchanged.
			return rec, true, true, ""
		}
		return adjusted, true, false, ""
	case rec.CommitmentType == common.CommitmentReservedInstance:
		adjusted, ok, dropReason := applyTargetCoverageRI(rec, targetPct, logf)
		if !ok {
			// Distinguish "no signal" (pass through, count in summary) from
			// "target unreachable" (drop with already-fired INFO log).
			if rec.AverageInstancesUsedPerHour <= 0 {
				return rec, true, true, ""
			}
			return rec, false, false, dropReason
		}
		return adjusted, true, false, ""
	default:
		if !unsupportedSeen[rec.CommitmentType] {
			logf.printf("WARNING: --target-coverage not supported for CommitmentType=%q; passing recommendations through unchanged\n", rec.CommitmentType)
			unsupportedSeen[rec.CommitmentType] = true
		}
		return rec, true, false, ""
	}
}

// applyTargetCoverageRI is the RI branch of ApplyTargetCoverage. Returns
// (adjusted, true, "") on success, (rec, false, dropReason) when the rec
// should be passed through unscaled (no signal) or dropped (target
// unreachable). Caller distinguishes no-signal from drop via
// rec.AverageInstancesUsedPerHour and uses dropReason for the summary.
func applyTargetCoverageRI(rec common.Recommendation, targetPct float64, logf Logf) (result common.Recommendation, ok bool, drop string) {
	if rec.AverageInstancesUsedPerHour <= 0 {
		// No signal — caller will pass through and count in the summary.
		return rec, false, ""
	}

	avg := rec.AverageInstancesUsedPerHour
	// Coverage-anchored under-buy: size linearly off the pool's avg demand
	// and the absolute gap to target. Both inputs come from
	// GetReservationCoverage (AvgInstancesPerHour from
	// TotalRunningHours/window; ExistingCoveragePct from
	// CoverageHoursPercentage) so the buy lines up with the AWS console's
	// reservations-coverage report: target%-existing% of avg instances.
	//
	// The previous formula anchored on AWS's rec.Count
	// (floor(rec.Count × gap / (100−existing))), which under-bought when
	// AWS sized rec.Count for less than full coverage (ROI-curated) and
	// when CE's org-wide existing% disagreed with rec.Count's per-account
	// derivation. Anchoring on coverage's own avg removes both mismatches.
	// rec.Count is retained only for the cost-scaling ratio further down.
	//
	// Keep the subtraction in percentage units (subtract first, divide
	// later) so whole-percent values don't lose precision to float
	// rounding at integer boundaries.
	gapPct := targetPct - rec.ExistingCoveragePct
	if gapPct <= 0 {
		// Existing commitments already meet or exceed the target; no purchase
		// needed in this pool. Drop with an info log so operators can see what
		// the flag did. Returning (_, false) with avg > 0 signals "drop, don't
		// pass through".
		logf.printf("INFO: --target-coverage=%.1f%% already met by existing coverage %.1f%% for %s/%s/%s; dropped recommendation\n",
			targetPct, rec.ExistingCoveragePct, rec.Service, rec.Region, rec.ResourceType)
		return rec, false, common.DropTargetAlreadyMet
	}
	// Floor so we never over-shoot the target on integer-arithmetic edges.
	// Strict-target semantics: 80% means "at most 80% coverage", not "at
	// least 80%". Floor under-covers small/odd pools (e.g. avg=2, target=80
	// gives 1 RI = 50% rather than 2 RIs = 100%); pools too small to
	// approximate target are best filtered out via --min-pool-size upstream.
	nTarget := int(math.Floor(avg * gapPct / 100.0))

	if nTarget == 0 {
		// Floor produces zero when avg × gap% < 100 (small pools or thin
		// gaps). Drop — buying 1 RI would over-shoot target and the
		// strict-target intent prefers under-cover (run on-demand) over
		// over-cover (idle commitment). Use --min-pool-size to filter
		// these out earlier so they don't show up as drops in the log.
		logf.printf("INFO: --target-coverage=%.1f%% sizes %s/%s/%s to 0 instances (avg=%.2f, gap=%.2f%% produces <1 RI); dropped recommendation\n",
			targetPct, rec.Service, rec.Region, rec.ResourceType, avg, gapPct)
		// Returning (_, false) with avg > 0 signals "drop, don't pass through".
		// applyTargetCoverageRI's caller branches on
		// rec.AverageInstancesUsedPerHour to distinguish drop vs no-signal.
		return rec, false, common.DropTargetSizedToZero
	}

	// Cost-bearing fields scale by the ratio of sized-to-original count, so the
	// returned rec represents the sized purchase rather than AWS's pre-sized
	// proposal. SavingsPercentage is invariant (savings vs on-demand ratio).
	// rec.Count is the AWS pre-sizing count at this point (parser sets Count
	// == RecommendedCount and we haven't mutated either yet). When the
	// coverage-anchored nTarget exceeds rec.Count (AWS sized below full
	// coverage), the ratio scales costs up linearly — accurate when per-RI
	// pricing is constant, which it is within a single pool/term/payment
	// combination. Guarded against rec.Count==0 (malformed rec) by falling
	// back to nTarget so a zero-cost rec stays zero-cost rather than NaN.
	var ratio float64
	if rec.Count > 0 {
		ratio = float64(nTarget) / float64(rec.Count)
	} else {
		ratio = float64(nTarget)
	}
	adjusted := common.ScaleRecommendationCosts(rec, ratio)
	adjusted.Count = nTarget

	// Projection metrics. ProjectedCoverage is TOTAL coverage (existing +
	// new) so operators can see the figure they actually targeted.
	// ProjectedUtilization stays at the per-purchase fill rate; under-buy
	// keeps nTarget <= avg so it always clamps to 100%.
	projUtil := avg / float64(nTarget) * 100.0
	if projUtil > 100 {
		projUtil = 100
	}
	projCov := rec.ExistingCoveragePct + float64(nTarget)/avg*100.0
	if projCov > 100 {
		projCov = 100
	}
	adjusted.ProjectedUtilization = projUtil
	adjusted.ProjectedCoverage = projCov
	return adjusted, true, ""
}

// applyTargetCoverageSP is the SP branch of ApplyTargetCoverage. Returns
// (adjusted, true) when the rec is kept, (rec, false) when it should be
// skipped (caller passes through unscaled and counts in the skip summary).
func applyTargetCoverageSP(rec common.Recommendation, targetPct float64, logf Logf) (common.Recommendation, bool) {
	if rec.RecommendedUtilization <= 0 {
		return rec, false
	}
	// If Details isn't a non-nil *SavingsPlanDetails (defensive — it should
	// always be one for SP recs), log a warning and pass through UNCHANGED —
	// including leaving ProjectedUtilization at zero. Setting projection
	// fields on a rec whose commitment fields couldn't be scaled would
	// produce a misleading row (projection=target%, savings=full-unscaled).
	//
	// The nil check must precede the HourlyCommitment read below: an
	// interface holding a typed nil satisfies the assertion, so reading the
	// field off it would dereference nil.
	details, ok := rec.Details.(*common.SavingsPlanDetails)
	if !ok || details == nil {
		logf.printf("WARNING: SP recommendation for service %q has missing or unexpected Details (%T); passing through unscaled\n", rec.Service, rec.Details)
		return rec, true
	}
	// Also treat a $0 HourlyCommitment as "no signal" — CE occasionally
	// returns placeholder recs with zero commitment. Sizing such a rec
	// would produce nonsense ($0 commitment * ratio = $0) while still
	// claiming the target coverage is achieved, which is incoherent.
	// Pass through unchanged and count in the skip summary.
	if details.HourlyCommitment <= 0 {
		return rec, false
	}

	// Under-buy: scale all cost-bearing fields by target/100 against AWS's
	// recommended commitment. This deliberately spends less than AWS suggested,
	// leaving (100-target)% of the SP's projected workload on on-demand.
	// RecommendedUtilization is consulted only as a no-signal guard above (a
	// zero value means we can't sanity-check the result); the scaling itself
	// uses targetPct directly rather than a recUtil/target ratio so the flag's
	// intent is honored even when AWS already projects above target.
	ratio := targetPct / 100.0
	// ScaleRecommendationCosts scales HourlyCommitment along with the cost
	// fields and replaces Details with a scaled copy.
	adjusted := common.ScaleRecommendationCosts(rec, ratio)
	// Shrinking commitment raises projected utilization by 1/ratio
	// (used is fixed = orig_commit * RecUtil, bought is orig_commit * ratio).
	// Clamp to 100 since utilization caps at full use.
	projUtil := rec.RecommendedUtilization / ratio
	if projUtil > 100 {
		projUtil = 100
	}
	adjusted.ProjectedUtilization = projUtil
	// ProjectedCoverage stays zero for SPs — CE doesn't expose total-demand-$
	// for a clean coverage figure (see field doc on Recommendation).
	return adjusted, true
}
