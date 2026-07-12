package ladder

import (
	"context"
	"fmt"

	pkgladder "github.com/LeanerCloud/CUDly/pkg/ladder"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
)

// NewFromAWSConfig constructs an AWSLadder for the given region and accountID.
// All read-side data-source adapters that are not yet wired in PR-2 are replaced
// by no-op stubs:
//   - RI and SP listers return empty slices (no existing commitments is safe for
//     a plan-only run: the engine correctly shows the full gap as unhedged).
//   - RI coverage and utilization sources return empty results.
//   - The on-demand series source returns an explicit error so GetUsageBaseline
//     fails loud; handleLadderRun treats that as a failed run and records it in
//     the DB. Real wiring arrives in PR-4 (Cost Explorer data source).
//
// The write side (PurchaseLayer / ReshapeBuffer) is not wired here; all writes
// are rejected with errWriteNotWired, which is correct for the plan-only phase.
//
// NewFromAWSConfig matches the LadderCapabilityFactory signature on Application
// so it can be assigned directly:
//
//	app.LadderCapabilityFactory = awsladder.NewFromAWSConfig
func NewFromAWSConfig(_ context.Context, region, accountID string) (pkgladder.LadderCapability, error) {
	if region == "" {
		return nil, fmt.Errorf("awsladder.NewFromAWSConfig: region must not be empty")
	}
	if accountID == "" {
		return nil, fmt.Errorf("awsladder.NewFromAWSConfig: accountID must not be empty")
	}
	cfg := Config{
		Region:    region,
		AccountID: accountID,
	}
	l, err := New(
		cfg,
		noopRILister{},
		noopSPLister{},
		noopRICoverageSource{},
		noopOnDemandSeriesSource{},
		noopUtilizationSource{},
		nil, // spCoverageSource: wired when PR-4 lands
		nil, // spUtilizationSource: wired when PR-4 lands
	)
	if err != nil {
		return nil, fmt.Errorf("awsladder.NewFromAWSConfig: %w", err)
	}
	return l, nil
}

// noopRILister satisfies riLister by reporting no active convertible RIs.
// Returning an empty slice is safe: the engine counts ExistingUSDPerHour for
// RI layers as zero and plans the full gap.
type noopRILister struct{}

func (noopRILister) ListConvertibleReservedInstances(_ context.Context) ([]ec2svc.ConvertibleRI, error) {
	return []ec2svc.ConvertibleRI{}, nil
}

// noopSPLister satisfies spLister by reporting no active Savings Plans.
// Returning an empty slice is safe for the same reason as noopRILister.
type noopSPLister struct{}

func (noopSPLister) ListActiveSPs(_ context.Context) ([]ActiveSP, error) {
	return []ActiveSP{}, nil
}

// noopRICoverageSource satisfies riCoverageSource with an empty coverage map.
// The engine sets CoveragePct to nil for all RI pools when no coverage data is
// available, which is treated as "not yet measured" (not "zero coverage").
type noopRICoverageSource struct{}

func (noopRICoverageSource) GetRICoverageMap(_ context.Context, _ int, _ []string) (recommendations.PoolCoverageMap, error) {
	return recommendations.PoolCoverageMap{}, nil
}

// noopOnDemandSeriesSource satisfies onDemandSeriesSource by returning an
// explicit error. GetUsageBaseline requires a non-empty daily spend series to
// compute the low-water baseline; without it the engine cannot produce a
// meaningful plan. The handler treats GetUsageBaseline failures as a failed run
// and records the error in the DB. Real wiring (Cost Explorer adapter) arrives
// in PR-4.
type noopOnDemandSeriesSource struct{}

func (noopOnDemandSeriesSource) GetOnDemandSeries(_ context.Context, _ string, _ int) ([]float64, error) {
	return nil, fmt.Errorf("on-demand series source not yet wired: the Cost Explorer adapter is connected in PR-4; until then plan runs will be recorded as failed")
}

// noopUtilizationSource satisfies utilizationSource by returning an empty
// slice. The engine treats nil/empty utilization as "not yet measured" and
// leaves UtilizationPct nil for RI layers.
type noopUtilizationSource struct{}

func (noopUtilizationSource) GetRIUtilization(_ context.Context, _ int) ([]recommendations.RIUtilization, error) {
	return []recommendations.RIUtilization{}, nil
}
