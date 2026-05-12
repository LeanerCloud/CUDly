package recommendations

import (
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// AdjustExistingCoverageForExpiringCommitments reduces each rec's
// ExistingCoveragePct by the share of pool demand attributable to RIs that
// expire within windowDays. Sized purchases downstream then treat the
// expiring share as already-uncovered and recommend replacements.
//
// Returns the number of recommendations whose ExistingCoveragePct was
// adjusted, so the caller can log a meaningful summary.
//
// No-op when windowDays <= 0 or commitments is empty. The intent is that
// --rebuy-window-days controls whether this adjustment runs at all;
// callers gate the invocation on cfg.RebuyWindowDays.
//
// Pool matching uses the same engine-aware key as the coverage map so the
// adjustment lines up with the data ApplyCoverageMapToRecommendations
// just populated. Commitments whose pool key doesn't match any rec are
// silently dropped (they're for instance types we're not currently
// recommending; nothing to subtract from).
func AdjustExistingCoverageForExpiringCommitments(
	recs []common.Recommendation,
	commitments []common.Commitment,
	windowDays int,
) int {
	if windowDays <= 0 || len(commitments) == 0 {
		return 0
	}
	cutoff := time.Now().Add(time.Duration(windowDays) * 24 * time.Hour)
	expiringByPool := expiringCountsByPool(commitments, cutoff)
	if len(expiringByPool) == 0 {
		return 0
	}
	return applyExpiringAdjustments(recs, expiringByPool)
}

// expiringCountsByPool aggregates active commitments expiring at-or-before
// cutoff into a pool-keyed count map. The State filter mirrors
// DuplicateChecker.filterRecentCommitments — only RIs currently providing
// coverage are counted (queued / retired RIs aren't covering demand now).
func expiringCountsByPool(commitments []common.Commitment, cutoff time.Time) map[string]int {
	out := make(map[string]int)
	for _, c := range commitments {
		if !commitmentIsActive(c) {
			continue
		}
		if c.EndDate.IsZero() || c.EndDate.After(cutoff) {
			continue
		}
		key := commitmentPoolKey(c)
		if key == "" {
			continue
		}
		out[key] += c.Count
	}
	return out
}

// applyExpiringAdjustments subtracts each rec's matching expiring-pool
// share from ExistingCoveragePct, clamping at zero. Returns the number
// of recs touched. Recs without a positive avg signal are skipped — we
// can't compute a per-pool percentage without it.
func applyExpiringAdjustments(recs []common.Recommendation, expiringByPool map[string]int) int {
	adjusted := 0
	for i := range recs {
		if recs[i].AverageInstancesUsedPerHour <= 0 {
			continue
		}
		expCount, ok := expiringByPool[lookupPoolKey(recs[i])]
		if !ok || expCount == 0 {
			continue
		}
		// Convert expiring count to percentage of pool demand. Clamp to
		// avoid negative ExistingCoveragePct if the expiring share exceeds
		// the CE-reported existing coverage (can happen when CE coverage
		// is org-wide-averaged below per-pool truth, or when expiring
		// count includes ZONAL RIs the regional aggregate doesn't credit).
		expiringPct := float64(expCount) / recs[i].AverageInstancesUsedPerHour * 100.0
		if expiringPct > recs[i].ExistingCoveragePct {
			recs[i].ExistingCoveragePct = 0
		} else {
			recs[i].ExistingCoveragePct -= expiringPct
		}
		adjusted++
	}
	return adjusted
}

// commitmentIsActive returns true for commitments whose State indicates
// they're currently providing coverage. Matches the state set used by
// DuplicateChecker for consistency.
func commitmentIsActive(c common.Commitment) bool {
	return c.State == "active" || c.State == "payment-pending"
}

// commitmentPoolKey returns the same lookup key shape used by the coverage
// map: engine-aware for RDS, region+instance-type for everything else.
// Both forms include the linked-account ID so adjustments align with the
// per-account existing-coverage signal they're modifying.
func commitmentPoolKey(c common.Commitment) string {
	if c.Service == common.ServiceRDS || c.Service == common.ServiceRelationalDB {
		return rdsPoolKey(c.Region, c.ResourceType, c.Engine, c.Account)
	}
	return poolKey(c.Region, c.ResourceType, c.Account)
}
