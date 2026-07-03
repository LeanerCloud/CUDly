// Package ladder implements the ladder.LadderCapability READ side for AWS.
// Write-side methods (PurchaseLayer, ReshapeBuffer) return explicit
// not-implemented errors until the write-side PR lands.
package ladder

import (
	"context"
	"time"

	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
)

// riLister is the narrow interface for listing active convertible RIs.
// The concrete implementation is ec2svc.Client.ListConvertibleReservedInstances.
type riLister interface {
	ListConvertibleReservedInstances(ctx context.Context) ([]ec2svc.ConvertibleRI, error)
}

// ActiveSP is a minimal view of an active Savings Plan needed by AWSLadder.
// Only EC2Instance and Compute plan types are relevant to the three ladder layers.
//
// Fields are ordered to minimize the GC pointer-scan range (fieldalignment).
type ActiveSP struct {
	// PlanID is the AWS Savings Plan ID.
	PlanID string
	// PlanType is "EC2Instance" or "Compute" (matches the SavingsPlan.SavingsPlanType
	// display form from the DescribeSavingsPlans response).
	PlanType string
	// State mirrors SavingsPlan.State as a string ("active", "pending-return", "queued").
	State string
	// StartDate is the SP activation time.
	StartDate time.Time
	// EndDate is the SP expiry time.
	EndDate time.Time
	// Region is the AWS region; empty for Compute SPs (which are global).
	Region string
	// HourlyCommitmentUSD is the committed spend in USD per hour (SavingsPlan.Commitment
	// parsed as float64). This is what the engine counts as ExistingUSDPerHour for SP layers.
	// Placed last to keep all pointer-containing fields contiguous for the GC scanner.
	HourlyCommitmentUSD float64
}

// spLister is the narrow interface for listing active Savings Plans relevant
// to the three ladder layers. The real implementation (wired when both PRs
// land) calls DescribeSavingsPlans filtering to Active state and maps the
// Commitment field to HourlyCommitmentUSD. Tests pass a hermetic fake.
type spLister interface {
	ListActiveSPs(ctx context.Context) ([]ActiveSP, error)
}

// coverageSource is the narrow interface for RI coverage data and the
// on-demand daily spend series used by GetUsageBaseline.
//
// GetRICoverageMap returns the per-pool org-wide RI coverage map (keyed by
// "region:instance_type" for EC2) for the given lookback window and regions.
//
// GetOnDemandSeries returns a slice of len(lookbackDays) daily on-demand-
// equivalent USD/hour values for the given region, ordered oldest-to-newest.
// Each element is the average on-demand spend in USD per hour for that
// calendar day. The real implementation sources this from CE GetCostAndUsage
// with Granularity=Daily filtered to on-demand usage types; wiring happens
// when the cost-and-usage collector PR lands. Tests pass a hermetic fake.
type coverageSource interface {
	GetRICoverageMap(ctx context.Context, lookbackDays int, regions []string) (recommendations.PoolCoverageMap, error)
	GetOnDemandSeries(ctx context.Context, region string, lookbackDays int) ([]float64, error)
}

// utilizationSource is the narrow interface for RI utilization data.
// The real implementation is RecommendationsClientAdapter.GetRIUtilization.
type utilizationSource interface {
	GetRIUtilization(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error)
}

// SPCoverageSummary carries the Savings Plans coverage result from the CE API.
// The CE GetSavingsPlansCoverage API does not support filtering by plan type,
// so one summary covers all SP layers. When the parallel SP coverage PR
// (PR 4) lands, reconcile this type with the one it defines.
type SPCoverageSummary struct {
	// CoveragePct is the percentage (0-100) of eligible compute spend covered
	// by Savings Plans. Nil means the CE API returned no data for the scope.
	CoveragePct *float64
}

// SPUtilizationSummary carries the Savings Plans utilization result from the
// CE API. Per-plan-type utilization is available via GetSavingsPlansUtilization,
// so each SP layer gets its own summary. When PR 4 lands, reconcile this type.
type SPUtilizationSummary struct {
	// UtilizationPct is the percentage (0-100) of the committed spend that was
	// actually used. Nil means the CE API returned no data for the scope.
	UtilizationPct *float64
}

// spCoverageSource is the narrow interface for Savings Plans coverage data.
// It is wired when the SP coverage PR (parallel to this one, PR 4) lands.
// Pass nil to skip SP coverage measurement; CoveragePct will be nil for SP layers.
//
// CE API note: GetSavingsPlansCoverage does NOT support plan-type filtering.
// The returned coverage applies to ALL Savings Plan types in the region, so
// AWSLadder sets the same CoveragePct on both EC2Instance and Compute SP layers.
//
// Adapter requirement: Go interface satisfaction needs identical return types,
// and PR 4's concrete implementation returns its own richer
// recommendations.SPCoverageSummary (more fields, e.g. Days). The wiring PR
// must therefore provide a thin adapter mapping
// recommendations.SPCoverageSummary{CoveragePct, ..., Days} -> this package's
// SPCoverageSummary, preserving nil CoveragePct as no-data (PR 4 returns nil
// when Days==0).
type spCoverageSource interface {
	GetSPCoverageSummary(ctx context.Context, region string, lookbackDays int) (SPCoverageSummary, error)
}

// spUtilizationSource is the narrow interface for Savings Plans utilization data.
// It is wired when the SP utilization PR (parallel to this one, PR 4) lands.
// Pass nil to skip SP utilization measurement; UtilizationPct will be nil for SP layers.
//
// planType uses the CE SDK enum (cetypes.SupportedSavingsPlansTypeEc2InstanceSp,
// cetypes.SupportedSavingsPlansTypeComputeSp, etc.).
// region "" = all regions; pass "" for Compute SPs (global) and the configured
// region for EC2 Instance SPs.
//
// Adapter requirement: Go interface satisfaction needs identical return types,
// and PR 4's concrete implementation returns its own richer
// recommendations.SPUtilizationSummary (more fields, e.g. Days). The wiring PR
// must therefore provide a thin adapter mapping
// recommendations.SPUtilizationSummary{UtilizationPct, ..., Days} -> this
// package's SPUtilizationSummary, preserving nil UtilizationPct as no-data
// (PR 4 returns nil when Days==0).
type spUtilizationSource interface {
	GetSPUtilization(ctx context.Context, planType cetypes.SupportedSavingsPlansType, region string, lookbackDays int) (SPUtilizationSummary, error)
}
