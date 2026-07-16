// Package ladder implements ladder.LadderCapability for AWS: the read side
// (commitment listing, layer states, usage baseline) and the write side
// (layer purchases, buffer reshaping). Write-side methods require the write
// dependencies to be wired via AWSLadder.WithWriteSide; until then they
// return an explicit not-wired error.
package ladder

import (
	"context"
	"time"

	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	sptypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/exchange"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
)

// Savings Plan plan-type identifiers, derived from the AWS SDK enum so this
// package can never drift from the vocabulary the savingsplans service client
// uses (its PlanTypeForServiceType / ServiceTypeForPlanType mappings are built
// on sptypes.SavingsPlanType). The string form is needed because ActiveSP.
// PlanType and common.SavingsPlanDetails.PlanType are plain strings; the
// constant conversion keeps these compile-time constants, not vars.
const (
	spPlanTypeEC2Instance = string(sptypes.SavingsPlanTypeEc2Instance)
	spPlanTypeCompute     = string(sptypes.SavingsPlanTypeCompute)
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

// riCoverageSource is the narrow interface for RI coverage data, consumed by
// GetLayerStates. GetRICoverageMap returns the per-pool org-wide RI coverage
// map (keyed by "region:instance_type" for EC2) for the given lookback window
// and regions. Kept single-method (interface segregation) so implementations
// that only provide coverage need not stub the on-demand series and vice versa;
// one concrete adapter may still implement both.
type riCoverageSource interface {
	GetRICoverageMap(ctx context.Context, lookbackDays int, regions []string) (recommendations.PoolCoverageMap, error)
}

// DailyPoint is a single calendar-day entry in the on-demand cost series.
// Date is the UTC calendar day (time-of-day component is irrelevant; callers
// should truncate to midnight UTC for consistent comparisons).
// USDPerHour is the average on-demand-equivalent spend in USD per hour for
// that calendar day (total daily spend / 24).
type DailyPoint struct {
	// Date is the UTC calendar day this data point covers.
	Date time.Time
	// USDPerHour is the on-demand-equivalent spend averaged over the day.
	USDPerHour float64
}

// onDemandSeriesSource is the narrow interface for the daily on-demand spend
// series consumed by GetUsageBaseline. GetOnDemandSeries returns a slice of
// DailyPoints for the given region and lookback window, ordered oldest-to-newest.
// Each element covers one calendar day; the Date field allows GetUsageBaseline
// to enforce freshness (the most-recent point must be within maxSeriesAgeDays).
// The real implementation sources this from CE GetCostAndUsage with
// Granularity=Daily filtered to on-demand usage types; wiring happens when the
// cost-and-usage collector PR (L2) lands. Tests pass a hermetic fake.
type onDemandSeriesSource interface {
	GetOnDemandSeries(ctx context.Context, region string, lookbackDays int) ([]DailyPoint, error)
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

// riPurchaser is the narrow interface for purchasing EC2 convertible Reserved
// Instances. The concrete implementation is ec2svc.Client.PurchaseCommitment,
// which resolves the offering from the recommendation, enforces the
// idempotency-tag dedupe guard (issue #636: a lookup for an RI already tagged
// with opts.IdempotencyToken short-circuits a re-driven purchase), and tags
// the fresh RI post-purchase.
type riPurchaser interface {
	PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error)
}

// spPurchaser is the narrow interface for purchasing Savings Plans. The
// concrete implementation is savingsplans.Client.PurchaseCommitment, which
// resolves the offering (plan type + term + payment option) and calls
// CreateSavingsPlan with opts.IdempotencyToken as the native ClientToken
// (server-side idempotency: a repeated call returns the original plan).
//
// A single spPurchaser serves both SP layers: AWSLadder validates that the
// recommendation's SavingsPlanDetails.PlanType matches the dispatched layer
// (EC2Instance for LayerEC2InstanceSP, Compute for LayerComputeSP) before
// calling, and a plan-type-scoped savingsplans.Client re-validates against
// its own scope (resolveSPPlanType), so a mismatched purchase cannot slip
// through either boundary.
type spPurchaser interface {
	PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error)
}

// exchangeRunner is the narrow interface for running the automated RI
// exchange flow. The concrete implementation wraps exchange.RunAutoExchange
// and owns everything ReshapeBuffer must not know about: the exchange store,
// the ExchangeClient, the offering lookup, and the RI/utilization inventory
// conversion (the same wiring internal/server.executeRIExchangeReshape does).
// AWSLadder only supplies the run configuration; injecting the full
// exchange.RunAutoExchangeParams surface here would drag store and exchange
// client dependencies into this package for no benefit.
type exchangeRunner interface {
	RunAutoExchange(ctx context.Context, cfg exchange.RIExchangeConfig) (*exchange.AutoExchangeResult, error)
}
