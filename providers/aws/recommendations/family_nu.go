package recommendations

import (
	"math"
	"strings"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// rdsInstanceNU maps an RDS instance size suffix to the normalized-units
// value AWS uses for RDS RI size-flexibility within an instance family.
// Reference:
// https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_WorkingWithReservedDBInstances.html#USER_WorkingWithReservedDBInstances.SizeFlexible
//
// Used by ApplyFamilyNUSizingRDS to translate AWS-rec-API's family-NU-
// bundled buy recommendations into the family target NU need under
// --target-coverage. Sizes not in this map evaluate to 0 NU, which
// causes the family-NU step to leave the rec unchanged (the per-pool
// sizing path still handles it downstream).
var rdsInstanceNU = map[string]float64{
	"nano":     0.25,
	"micro":    0.5,
	"small":    1,
	"medium":   2,
	"large":    4,
	"xlarge":   8,
	"2xlarge":  16,
	"4xlarge":  32,
	"8xlarge":  64,
	"10xlarge": 80,
	"12xlarge": 96,
	"16xlarge": 128,
	"24xlarge": 192,
	"32xlarge": 256,
}

// RDSInstanceNUFromType is the exported counterpart of rdsInstanceNUFromType
// for callers outside this package that need the NU value for an RDS
// instance type (e.g. CSV writers displaying per-row family-NU
// contribution). Returns 0 for unknown sizes — same fallback semantics
// as the unexported helper.
func RDSInstanceNUFromType(instanceType string) float64 {
	return rdsInstanceNUFromType(instanceType)
}

// RDSFamilyFromType is the exported counterpart of rdsFamilyFromType for
// callers outside this package that need the family prefix of an RDS
// instance type (e.g. CSV writers grouping rows by family). Empty
// string when the type doesn't carry a recognisable size suffix.
func RDSFamilyFromType(instanceType string) string {
	return rdsFamilyFromType(instanceType)
}

// rdsInstanceNUFromType returns the NU value for an instance type like
// "db.r7g.2xlarge", parsing out the size suffix ("2xlarge" → 16). Returns
// 0 when the size isn't recognised — callers treat that as "no family-NU
// signal" and fall back to per-pool sizing.
func rdsInstanceNUFromType(instanceType string) float64 {
	parts := strings.Split(instanceType, ".")
	if len(parts) < 3 {
		return 0
	}
	return rdsInstanceNU[parts[len(parts)-1]]
}

// rdsFamilyFromType returns the family prefix for an instance type, e.g.
// "db.r7g.2xlarge" → "db.r7g". Empty string when the type doesn't carry
// a recognisable size suffix.
func rdsFamilyFromType(instanceType string) string {
	parts := strings.Split(instanceType, ".")
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[:len(parts)-1], ".")
}

// rdsFamilyKey returns the lookup key for an RDS family's aggregated
// coverage view. Matches the shape of rdsPoolKey but at family-level
// granularity ("db.r7g" rather than "db.r7g.large"), so the family-NU
// step groups all sizes within (region, family, engine, deployment) into
// a single demand bucket.
func rdsFamilyKey(region, family, engine, deployment string) string {
	return strings.ToLower(region) + ":" +
		strings.ToLower(family) + ":" +
		normaliseRDSEngine(engine) + ":" +
		normaliseDeployment(deployment)
}

// FamilyCoverage is the family-NU-aggregated view of coverage for an
// RDS instance family. TotalNU is the sum of (avg × NU(size)) across
// every size in the family that has CE coverage data; CoveredNU is the
// sum of (avg × NU(size) × pct/100). Pct can be derived as
// CoveredNU / TotalNU × 100 (callers do this inline).
type FamilyCoverage struct {
	TotalNU   float64
	CoveredNU float64
}

// AggregateRDSFamilyCoverage walks the per-pool coverage map and returns
// a family-NU-aggregated view keyed by rdsFamilyKey. Used by family-NU
// sizing to size buys against the total family demand rather than
// per-size pool demand — matching how AWS's
// GetReservationPurchaseRecommendation bundles size-flex demand into a
// single rec at one size.
//
// Skips non-RDS entries (their keys are 2-part, not 4-part) and entries
// whose instance size isn't in rdsInstanceNU.
func AggregateRDSFamilyCoverage(coverage PoolCoverageMap) map[string]FamilyCoverage {
	out := make(map[string]FamilyCoverage)
	for key, pc := range coverage {
		parts := strings.Split(key, ":")
		if len(parts) != 4 {
			// Non-RDS pool keys are "region:instance_type" (2 parts).
			continue
		}
		region, instType, engine, deployment := parts[0], parts[1], parts[2], parts[3]
		nu := rdsInstanceNUFromType(instType)
		if nu == 0 {
			continue
		}
		family := rdsFamilyFromType(instType)
		if family == "" {
			continue
		}
		fk := rdsFamilyKey(region, family, engine, deployment)
		cur := out[fk]
		cur.TotalNU += pc.AvgInstancesPerHour * nu
		cur.CoveredNU += pc.AvgInstancesPerHour * nu * pc.Pct / 100.0
		out[fk] = cur
	}
	return out
}

// ApplyFamilyNUSizingRDS replaces per-pool RI sizing with family-NU
// sizing for RDS recommendations. AWS's GetReservationPurchaseRecommendation
// already bundles size-flex demand within an instance family into a
// single recommendation at one size; per-pool sizing under-buys because
// it only sees that size's pool demand. Family-NU sizing rescales each
// RDS rec's Count so the family-wide NU sum matches the user's target
// across the whole family.
//
// Algorithm:
//  1. Group RDS recs by (region, family, engine, deployment).
//  2. For each family:
//     a. Compute family existing_pct = covered_NU / total_NU * 100
//     b. gap = targetPct − existing_pct (drop family if ≤ 0)
//     c. target_NU_need = gap / 100 * total_NU
//     d. current_rec_NU = Σ rec.Count × NU(rec.size)
//     e. scale = target_NU_need / current_rec_NU
//     f. Apply scale to each rec.Count and cost-bearing fields
//  3. Recs scaled to zero are dropped (size flex left no room).
//  4. Non-RDS recs are returned unchanged so callers can continue them
//     through the per-pool sizing path.
//
// Returns (sizedRDS, nonRDS). When targetPct is outside (0,100] or
// coverage has no family-NU signal for an RDS rec's family, the rec is
// passed through unchanged in sizedRDS (so per-pool sizing downstream
// doesn't re-process it — caller treats sizedRDS as already sized).
func ApplyFamilyNUSizingRDS(
	recs []common.Recommendation,
	coverage PoolCoverageMap,
	targetPct float64,
) (sizedRDS, nonRDS []common.Recommendation) {
	if targetPct <= 0 || targetPct > 100 {
		return nil, recs
	}
	familyCov := AggregateRDSFamilyCoverage(coverage)
	familyIdx, nonRDS := partitionRDSRecsByFamily(recs)
	for fk, indices := range familyIdx {
		sized := sizeRDSFamilyRecs(recs, indices, familyCov[fk], targetPct)
		sizedRDS = append(sizedRDS, sized...)
	}
	return sizedRDS, nonRDS
}

// partitionRDSRecsByFamily splits recs into (a) an index map keyed by
// rdsFamilyKey that groups RDS RI recs by their (region, family, engine,
// deployment), and (b) a slice of non-RDS recs that flow through to the
// caller's per-pool sizing path unchanged. Recs that are RDS RIs but
// carry an instance type without a recognisable size suffix (and thus
// can't be NU-scaled) fall into nonRDS so per-pool sizing still handles
// them.
func partitionRDSRecsByFamily(recs []common.Recommendation) (map[string][]int, []common.Recommendation) {
	familyIdx := make(map[string][]int)
	nonRDS := make([]common.Recommendation, 0)
	for i := range recs {
		if !isRDSRIRec(recs[i]) {
			nonRDS = append(nonRDS, recs[i])
			continue
		}
		family := rdsFamilyFromType(recs[i].ResourceType)
		if family == "" {
			nonRDS = append(nonRDS, recs[i])
			continue
		}
		engine, deployment := rdsEngineDeploymentFromRec(recs[i])
		fk := rdsFamilyKey(recs[i].Region, family, engine, deployment)
		familyIdx[fk] = append(familyIdx[fk], i)
	}
	return familyIdx, nonRDS
}

// sizeRDSFamilyRecs sizes the recs in one family-key group: returns the
// sized recs, drops empty/over-target families, and returns the
// unchanged AWS-recommended counts when there's no coverage signal. The
// scaling math (target_NU_need / current_rec_NU) and per-rec field
// rewrite live in scaleRDSRecInFamily.
func sizeRDSFamilyRecs(
	recs []common.Recommendation,
	indices []int,
	family FamilyCoverage,
	targetPct float64,
) []common.Recommendation {
	if family.TotalNU <= 0 {
		// No coverage signal — keep AWS-recommended counts as-is.
		out := make([]common.Recommendation, 0, len(indices))
		for _, i := range indices {
			out = append(out, recs[i])
		}
		return out
	}
	existingPct := family.CoveredNU / family.TotalNU * 100.0
	gap := targetPct - existingPct
	if gap <= 0 {
		// Family already at-or-above target — drop the whole family's recs.
		return nil
	}
	targetNU := gap / 100.0 * family.TotalNU
	currentNU := 0.0
	for _, i := range indices {
		currentNU += float64(recs[i].Count) * rdsInstanceNUFromType(recs[i].ResourceType)
	}
	if currentNU <= 0 {
		// Recs sum to zero NU — nothing to scale.
		return nil
	}
	scale := targetNU / currentNU
	out := make([]common.Recommendation, 0, len(indices))
	for _, i := range indices {
		sized, kept := scaleRDSRecInFamily(recs[i], scale, existingPct, family.TotalNU)
		if kept {
			out = append(out, sized)
		}
	}
	return out
}

// scaleRDSRecInFamily applies the family-wide scale factor to one rec:
// computes newCount = floor(oldCount × scale), scales cost-bearing
// fields by the same ratio, and rewrites projection metrics for the
// family-NU view. Returns (rec, false) when newCount is zero so the
// caller drops the rec (family target left no room for it).
func scaleRDSRecInFamily(rec common.Recommendation, scale, existingPct, familyTotalNU float64) (common.Recommendation, bool) {
	oldCount := rec.Count
	newCount := int(math.Floor(float64(oldCount) * scale))
	if newCount <= 0 {
		return rec, false
	}
	ratio := float64(newCount) / float64(oldCount)
	rec.Count = newCount
	rec.CommitmentCost *= ratio
	rec.OnDemandCost *= ratio
	rec.EstimatedSavings *= ratio
	rec.ExistingCoveragePct = existingPct
	newNU := float64(newCount) * rdsInstanceNUFromType(rec.ResourceType)
	projCov := existingPct + newNU/familyTotalNU*100.0
	if projCov > 100 {
		projCov = 100
	}
	rec.ProjectedCoverage = projCov
	if rec.AverageInstancesUsedPerHour > 0 {
		util := rec.AverageInstancesUsedPerHour / float64(newCount) * 100.0
		if util > 100 {
			util = 100
		}
		rec.ProjectedUtilization = util
	}
	return rec, true
}

// isRDSRIRec reports whether rec is an RDS-family RI recommendation that
// the family-NU pass should size. Excludes Savings Plans and any other
// commitment type so they reach the per-pool sizing path unchanged.
func isRDSRIRec(rec common.Recommendation) bool {
	if rec.CommitmentType != common.CommitmentReservedInstance {
		return false
	}
	return rec.Service == common.ServiceRDS || rec.Service == common.ServiceRelationalDB
}
