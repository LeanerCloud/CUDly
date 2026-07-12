package ladder

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/LeanerCloud/CUDly/pkg/ladder"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
)

// ptr wraps a float64 as a non-nil pointer. Used to convert derived metrics
// into the pointer form required by LayerState.
func ptr(v float64) *float64 { return &v }

// GetLayerStates returns a point-in-time snapshot for each of the three
// supported AWS ladder layers. The snapshot rules are:
//
//   - ExistingUSDPerHour: 0 (explicit zero pointer) when the layer has no
//     active commitments; non-nil with the summed hourly amortized cost otherwise.
//   - ExpiringUSDPerHour: 0 (explicit zero pointer) when nothing expires within
//     Config.HorizonDays; the expiring share otherwise.
//   - CoveragePct: nil when not measured (SP layers before the parallel SP
//     coverage PR lands); non-nil from CE coverage data for the RI layer.
//   - UtilizationPct: nil on an empty layer or when data is unavailable; non-nil
//     from CE utilization data for the RI layer.
//
// CE API note: GetSavingsPlansCoverage does not support plan-type filtering,
// so both EC2Instance and Compute SP layers share the same CoveragePct value.
// This is documented on each SP LayerState via the layer type field.
//
// The scope must match Config.AccountID and common.ProviderAWS.
func (a *AWSLadder) GetLayerStates(ctx context.Context, scope ladder.Scope) (map[ladder.LayerType]ladder.LayerState, error) {
	if err := a.validateScope(scope); err != nil {
		return nil, err
	}

	ris, err := a.ris.ListConvertibleReservedInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetLayerStates: RI listing failed: %w", err)
	}

	sps, err := a.sps.ListActiveSPs(ctx)
	if err != nil {
		return nil, fmt.Errorf("GetLayerStates: SP listing failed: %w", err)
	}

	coverageMap, covErr := a.riCoverage.GetRICoverageMap(ctx, a.cfg.lookbackDays(), []string{a.cfg.Region})
	// covErr is checked per-layer below; a coverage failure does not fail the
	// whole snapshot — it degrades CoveragePct to nil.

	utils, utilErr := a.utilization.GetRIUtilization(ctx, a.cfg.lookbackDays())
	// utilErr is handled the same way: degrade UtilizationPct to nil.

	now := time.Now()
	horizon := now.Add(time.Duration(a.cfg.horizonDays()) * 24 * time.Hour)

	// SP coverage is fetched once; the CE GetSavingsPlansCoverage API does not
	// support filtering by plan type, so both SP layers receive the same value.
	spCovPct := a.fetchSPCoveragePct(ctx)

	states := make(map[ladder.LayerType]ladder.LayerState, 3)
	states[ladder.LayerConvertibleRI] = a.riLayerState(ris, horizon, coverageMap, covErr, utils, utilErr)
	states[ladder.LayerEC2InstanceSP] = a.spLayerState(ctx, ladder.LayerEC2InstanceSP, spPlanTypeEC2Instance, sps, horizon, spCovPct)
	states[ladder.LayerComputeSP] = a.spLayerState(ctx, ladder.LayerComputeSP, spPlanTypeCompute, sps, horizon, spCovPct)
	return states, nil
}

// riLayerState builds the LayerState for the ConvertibleRI (buffer) layer.
func (a *AWSLadder) riLayerState(
	ris []ec2svc.ConvertibleRI,
	horizon time.Time,
	coverageMap recommendations.PoolCoverageMap,
	covErr error,
	utils []recommendations.RIUtilization,
	utilErr error,
) ladder.LayerState {
	existing := sumRIHourlyCost(ris)
	expiring := sumExpiringRIHourlyCost(ris, horizon)

	state := ladder.LayerState{
		Layer:              ladder.LayerConvertibleRI,
		ExistingUSDPerHour: ptr(existing),
		ExpiringUSDPerHour: ptr(expiring),
	}

	switch {
	case covErr != nil:
		// CoveragePct stays nil (unmeasured). Log so a persistently failing CE
		// call is visible — silent degradation would quietly disable reshape
		// triggering downstream.
		log.Printf("WARNING: AWSLadder GetLayerStates: RI coverage degraded to nil (layer=%s, source=GetRICoverageMap, region=%s): %v",
			ladder.LayerConvertibleRI, a.cfg.Region, covErr)
	case len(coverageMap) > 0:
		state.CoveragePct = computeEC2CoveragePct(coverageMap, a.cfg.Region)
	}

	if utilErr != nil {
		// UtilizationPct stays nil (unmeasured); same observability rationale.
		log.Printf("WARNING: AWSLadder GetLayerStates: RI utilization degraded to nil (layer=%s, source=GetRIUtilization, region=%s): %v",
			ladder.LayerConvertibleRI, a.cfg.Region, utilErr)
	} else {
		state.UtilizationPct = computeRIUtilizationPct(utils)
	}

	return state
}

// spLayerState builds the LayerState for an EC2Instance or Compute SP layer.
//
// sharedCovPct is the SP coverage percentage fetched once for both SP layers;
// the CE GetSavingsPlansCoverage API does not support plan-type filtering so
// both layers receive the same value (nil when the source is not yet wired).
//
// UtilizationPct is fetched per-layer via spUtilizationSource, which uses
// GetSavingsPlansUtilization and does support plan-type filtering.
func (a *AWSLadder) spLayerState(
	ctx context.Context,
	layerType ladder.LayerType,
	planType string,
	sps []ActiveSP,
	horizon time.Time,
	sharedCovPct *float64,
) ladder.LayerState {
	existing := sumSPHourlyCost(sps, planType)
	expiring := sumExpiringSPHourlyCost(sps, planType, horizon)

	state := ladder.LayerState{
		Layer:              layerType,
		ExistingUSDPerHour: ptr(existing),
		ExpiringUSDPerHour: ptr(expiring),
		CoveragePct:        sharedCovPct,
		UtilizationPct:     a.fetchSPUtilizationPct(ctx, planType),
	}
	return state
}

// fetchSPCoveragePct calls the injected spCoverageSource when wired; returns
// nil (unmeasured) when the interface is nil (PR 4 not yet landed).
// No planType is passed: the CE GetSavingsPlansCoverage API does not support
// filtering by plan type; the result applies to all SP types in the region.
func (a *AWSLadder) fetchSPCoveragePct(ctx context.Context) *float64 {
	if a.spCoverage == nil {
		return nil
	}
	summary, err := a.spCoverage.GetSPCoverageSummary(ctx, a.cfg.Region, a.cfg.lookbackDays())
	if err != nil {
		// Degrade gracefully (caller treats nil as unmeasured) but log: a
		// persistently failing CE call must not silently disable SP coverage.
		log.Printf("WARNING: AWSLadder GetLayerStates: SP coverage degraded to nil (layers=%s+%s, source=GetSPCoverageSummary, region=%s): %v",
			ladder.LayerEC2InstanceSP, ladder.LayerComputeSP, a.cfg.Region, err)
		return nil
	}
	return summary.CoveragePct
}

// fetchSPUtilizationPct calls the injected spUtilizationSource when wired;
// returns nil when the interface is nil (PR 4 not yet landed).
//
// Compute SPs are global; their utilization is queried with region="" (all
// regions). EC2 Instance SPs are region-specific; the configured region is used.
func (a *AWSLadder) fetchSPUtilizationPct(ctx context.Context, planType string) *float64 {
	if a.spUtil == nil {
		return nil
	}
	cePlanType, err := toSPUtilPlanType(planType)
	if err != nil {
		// Defensive: unknown plan type -> unmeasured; log for observability.
		log.Printf("WARNING: AWSLadder GetLayerStates: SP utilization degraded to nil (planType=%s, source=toSPUtilPlanType): %v",
			planType, err)
		return nil
	}
	// Compute SPs are global; EC2 Instance SPs are region-scoped.
	region := a.cfg.Region
	if planType == spPlanTypeCompute {
		region = "" // "" = all regions in the CE GetSavingsPlansUtilization API
	}
	summary, err := a.spUtil.GetSPUtilization(ctx, cePlanType, region, a.cfg.lookbackDays())
	if err != nil {
		// Degrade gracefully but log: silent CE failures must stay visible.
		log.Printf("WARNING: AWSLadder GetLayerStates: SP utilization degraded to nil (planType=%s, source=GetSPUtilization, region=%q): %v",
			planType, region, err)
		return nil
	}
	return summary.UtilizationPct
}

// toSPUtilPlanType maps the DescribeSavingsPlans planType string (from ActiveSP)
// to the CE SDK enum required by GetSavingsPlansUtilization.
func toSPUtilPlanType(planType string) (cetypes.SupportedSavingsPlansType, error) {
	switch planType {
	case spPlanTypeEC2Instance:
		return cetypes.SupportedSavingsPlansTypeEc2InstanceSp, nil
	case spPlanTypeCompute:
		return cetypes.SupportedSavingsPlansTypeComputeSp, nil
	default:
		return "", fmt.Errorf("toSPUtilPlanType: unrecognized SP plan type %q", planType)
	}
}

// sumRIHourlyCost returns the total hourly amortized cost across all RIs.
// Index-based range with pointer avoids copying the large ConvertibleRI struct.
func sumRIHourlyCost(ris []ec2svc.ConvertibleRI) float64 {
	var total float64
	for i := range ris {
		total += riHourlyCost(&ris[i])
	}
	return total
}

// riHourlyCost computes the reservation-total hourly amortized cost for an RI.
// ri is taken by pointer to avoid copying the large ConvertibleRI struct (hugeParam).
//
// DescribeReservedInstances pricing fields are PER-INSTANCE (same semantics as
// the repo's canonical monthlyCostFromConvertibleRI helper in
// internal/api/handler_ri_exchange.go), so the per-instance hourly rate is
// multiplied by InstanceCount:
//
//	hourly = (RecurringHourlyAmount + UsagePrice + FixedPrice/(Duration/3600)) * InstanceCount
//
// RecurringHourlyAmount covers the recurring charge (non-zero for no-upfront
// and partial-upfront); UsagePrice is the legacy per-hour usage fee;
// FixedPrice / (Duration / 3600) amortizes the upfront payment over the term
// (Duration is in seconds). Upfront amortization is skipped when Duration is
// zero (defensive; avoids divide-by-zero).
func riHourlyCost(ri *ec2svc.ConvertibleRI) float64 {
	var upfrontAmortized float64
	if ri.Duration > 0 {
		upfrontAmortized = ri.FixedPrice / (float64(ri.Duration) / 3600.0)
	}
	perInstance := ri.RecurringHourlyAmount + ri.UsagePrice + upfrontAmortized
	return perInstance * float64(ri.InstanceCount)
}

// sumExpiringRIHourlyCost sums the hourly costs of RIs whose EndDate is
// non-zero and falls on or before horizon. A zero EndDate is treated as
// "no expiry known" and excluded, to avoid misclassifying perpetual-term RIs.
// Index-based range with pointer avoids copying the large ConvertibleRI struct.
func sumExpiringRIHourlyCost(ris []ec2svc.ConvertibleRI, horizon time.Time) float64 {
	var total float64
	for i := range ris {
		if !ris[i].End.IsZero() && !ris[i].End.After(horizon) {
			total += riHourlyCost(&ris[i])
		}
	}
	return total
}

// sumSPHourlyCost sums the hourly commitment amounts for SPs of the given
// plan type.
func sumSPHourlyCost(sps []ActiveSP, planType string) float64 {
	var total float64
	for _, sp := range sps {
		if sp.PlanType == planType {
			total += sp.HourlyCommitmentUSD
		}
	}
	return total
}

// sumExpiringSPHourlyCost sums the hourly commitments of SPs of the given
// plan type that expire on or before horizon. A zero EndDate is excluded.
func sumExpiringSPHourlyCost(sps []ActiveSP, planType string, horizon time.Time) float64 {
	var total float64
	for _, sp := range sps {
		if sp.PlanType != planType {
			continue
		}
		if !sp.EndDate.IsZero() && !sp.EndDate.After(horizon) {
			total += sp.HourlyCommitmentUSD
		}
	}
	return total
}

// computeEC2CoveragePct derives an aggregate EC2 RI coverage percentage from
// the PoolCoverageMap for the given region. Only EC2 pools (keys matching
// "region:*" with a non-zero AvgInstancesPerHour or Pct) are considered.
//
// Weighting: when any pool has a non-zero AvgInstancesPerHour, coverage is
// a weighted average (weight = AvgInstancesPerHour). When all pools have
// AvgInstancesPerHour == 0 (CE returned coverage % but no running hours --
// unusual), a simple (unweighted) average is used. Returns nil when no EC2
// pools for the region are found in the map.
func computeEC2CoveragePct(coverageMap recommendations.PoolCoverageMap, region string) *float64 {
	prefix := strings.ToLower(region) + ":"
	var weightedSum, totalWeight float64
	var simpleSum float64
	count := 0

	for key, cov := range coverageMap {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		// Exclude RDS keys (contain extra ":" segments for engine:deployment).
		// EC2 pool keys are exactly "region:instance_type" (one colon).
		if strings.Count(key, ":") != 1 {
			continue
		}
		count++
		simpleSum += cov.Pct
		if cov.AvgInstancesPerHour > 0 {
			weightedSum += cov.Pct * cov.AvgInstancesPerHour
			totalWeight += cov.AvgInstancesPerHour
		}
	}

	if count == 0 {
		return nil
	}

	var result float64
	if totalWeight > 0 {
		result = weightedSum / totalWeight
	} else {
		result = simpleSum / float64(count)
	}
	return ptr(result)
}

// computeRIUtilizationPct aggregates per-RI utilization data from the CE
// GetReservationUtilization response into a single percentage.
//
// Method: sum(TotalActualHours) / sum(PurchasedHours) * 100. Returns nil when
// the slice is empty or the sum of PurchasedHours is zero (layer is empty or
// CE returned no hours -- genuinely unmeasured per the LayerState contract).
func computeRIUtilizationPct(utils []recommendations.RIUtilization) *float64 {
	var purchased, actual float64
	for _, u := range utils {
		purchased += u.PurchasedHours
		actual += u.TotalActualHours
	}
	if purchased == 0 {
		return nil
	}
	return ptr((actual / purchased) * 100.0)
}
