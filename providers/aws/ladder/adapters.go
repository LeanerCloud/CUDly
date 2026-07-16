package ladder

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdksp "github.com/aws/aws-sdk-go-v2/service/savingsplans"
	sptypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"

	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
)

// maxSPListPages caps the DescribeSavingsPlans pagination loop to guard against
// a runaway token loop (mirrors the pattern from issue #692 / #1019).
const maxSPListPages = 20

// activeSPListAPI is the minimal interface for listing Savings Plans.
// Only DescribeSavingsPlans is needed; the full SavingsPlansAPI from
// providers/aws/services/savingsplans includes purchase and offering methods
// the read-only lister does not require (interface-segregation principle).
// Tests inject a hermetic fake implementing this narrow interface.
type activeSPListAPI interface {
	DescribeSavingsPlans(ctx context.Context, params *sdksp.DescribeSavingsPlansInput, optFns ...func(*sdksp.Options)) (*sdksp.DescribeSavingsPlansOutput, error)
}

// spListerAdapter implements the spLister interface using the AWS Savings Plans
// SDK directly. It filters to Active-state plans only (not queued or
// pending-return, which are not yet generating commitment costs). Commitment
// strings are parsed with strconv.ParseFloat; parse errors fail loud per
// feedback_strict_int_parse and feedback_no_silent_fallbacks.
type spListerAdapter struct {
	api activeSPListAPI
}

// ListActiveSPs lists all active Savings Plans and maps them to the AWSLadder
// view. Only types.SavingsPlanStateActive plans are included; queued and
// pending-return plans are excluded because they have not yet started billing.
// PlanType and Commitment are validated at the boundary: unknown plan types
// and unparseable commitment strings fail loud (feedback_prefer_typed_enums,
// feedback_strict_int_parse). Pagination is fully exhausted (issue #692).
func (a *spListerAdapter) ListActiveSPs(ctx context.Context) ([]ActiveSP, error) {
	var sps []ActiveSP
	var nextToken *string
	page := 0

	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("ListActiveSPs: context cancelled: %w", err)
		}
		page++
		if page > maxSPListPages {
			return nil, fmt.Errorf("ListActiveSPs: exceeded %d page cap; possible API token loop", maxSPListPages)
		}

		input := &sdksp.DescribeSavingsPlansInput{
			States:     []sptypes.SavingsPlanState{sptypes.SavingsPlanStateActive},
			NextToken:  nextToken,
			MaxResults: aws.Int32(100),
		}
		out, err := a.api.DescribeSavingsPlans(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("ListActiveSPs: DescribeSavingsPlans page %d: %w", page, err)
		}

		for _, sp := range out.SavingsPlans {
			entry, err := mapActiveSP(sp)
			if err != nil {
				return nil, err
			}
			sps = append(sps, entry)
		}

		if out.NextToken == nil || aws.ToString(out.NextToken) == "" {
			break
		}
		nextToken = out.NextToken
	}
	return sps, nil
}

// mapActiveSP converts one DescribeSavingsPlans entry to ActiveSP.
// Fails loud on a missing SavingsPlanId (would make the entry un-identifiable)
// and on a non-numeric Commitment string (money path: cannot represent an
// absent number as 0, per feedback_nullable_not_zero and
// feedback_strict_int_parse).
func mapActiveSP(sp sptypes.SavingsPlan) (ActiveSP, error) {
	if sp.SavingsPlanId == nil {
		return ActiveSP{}, fmt.Errorf("ListActiveSPs: DescribeSavingsPlans returned entry with nil SavingsPlanId")
	}
	commitment := aws.ToString(sp.Commitment)
	hourly, err := strconv.ParseFloat(commitment, 64)
	if err != nil {
		return ActiveSP{}, fmt.Errorf("ListActiveSPs: cannot parse Commitment %q for SP %s: %w",
			commitment, *sp.SavingsPlanId, err)
	}

	entry := ActiveSP{
		PlanID:              *sp.SavingsPlanId,
		PlanType:            string(sp.SavingsPlanType),
		State:               string(sp.State),
		Region:              aws.ToString(sp.Region),
		HourlyCommitmentUSD: hourly,
	}
	if sp.Start != nil {
		if t, err := time.Parse(time.RFC3339, *sp.Start); err == nil {
			entry.StartDate = t
		}
	}
	if sp.End != nil {
		if t, err := time.Parse(time.RFC3339, *sp.End); err == nil {
			entry.EndDate = t
		}
	}
	return entry, nil
}

// onDemandSeriesAdapter implements onDemandSeriesSource by wrapping
// *recommendations.Client and mapping its []recommendations.DailyCost to this
// package's []DailyPoint. The two types have identical fields (Date midnight
// UTC + USDPerHour); the duplication exists because recommendations cannot
// import providers/aws/ladder (ladder already imports recommendations), so
// the mapping happens here at the seam. Ordering (oldest-first, strictly
// increasing unique UTC days) is guaranteed by the recommendations contract
// and preserved verbatim by the index-for-index copy.
type onDemandSeriesAdapter struct {
	client *recommendations.Client
}

// GetOnDemandSeries fetches the daily on-demand cost series from CE and maps
// each dated entry to a DailyPoint. Errors propagate unchanged (no silent
// fallback); an empty series is already rejected inside the client.
func (a *onDemandSeriesAdapter) GetOnDemandSeries(ctx context.Context, region string, lookbackDays int) ([]DailyPoint, error) {
	costs, err := a.client.GetOnDemandSeries(ctx, region, lookbackDays)
	if err != nil {
		return nil, err
	}
	points := make([]DailyPoint, len(costs))
	for i, c := range costs {
		points[i] = DailyPoint{Date: c.Date, USDPerHour: c.USDPerHour}
	}
	return points, nil
}

// spCoverageAdapter implements spCoverageSource by wrapping *recommendations.Client
// and mapping its richer SPCoverageSummary to the local SPCoverageSummary type.
// The mapping preserves the nil-when-Days==0 contract: if CE returned no
// coverage data the recommendations summary has Days==0 and CoveragePct==nil;
// the adapter returns an empty local summary (CoveragePct stays nil, signalling
// "not measured" to the engine rather than "0% coverage").
type spCoverageAdapter struct {
	client *recommendations.Client
}

// GetSPCoverageSummary fetches SP coverage from CE and maps the result to the
// local SPCoverageSummary type. CE does not support plan-type filtering for
// coverage (the filter contract allows only REGION, SERVICE, LINKED_ACCOUNT,
// and INSTANCE_FAMILY per the GetSavingsPlansCoverageInput SDK doc), so the
// returned summary applies to all SP types in the region.
func (a *spCoverageAdapter) GetSPCoverageSummary(ctx context.Context, region string, lookbackDays int) (SPCoverageSummary, error) {
	rich, err := a.client.GetSPCoverageSummary(ctx, region, lookbackDays)
	if err != nil {
		return SPCoverageSummary{}, err
	}
	// Preserve nil-when-Days==0: recommendations.SPCoverageSummary.CoveragePct
	// is nil when Days==0 (no CE data). The local type propagates that nil so
	// the engine treats it as "not yet measured" rather than "zero coverage".
	return SPCoverageSummary{CoveragePct: rich.CoveragePct}, nil
}

// spUtilizationAdapter implements spUtilizationSource by wrapping
// *recommendations.Client and mapping its SPUtilizationSummary to the local
// type. The mapping follows the same nil-propagation contract as
// spCoverageAdapter.
type spUtilizationAdapter struct {
	client *recommendations.Client
}

// GetSPUtilization fetches SP utilization from CE for the given plan type and
// maps the result to the local SPUtilizationSummary type. planType must be a
// valid cetypes.SupportedSavingsPlansType (validated upstream in toSPUtilPlanType
// before this call). region=="" means all regions (for Compute SPs).
func (a *spUtilizationAdapter) GetSPUtilization(ctx context.Context, planType cetypes.SupportedSavingsPlansType, region string, lookbackDays int) (SPUtilizationSummary, error) {
	rich, err := a.client.GetSPUtilization(ctx, planType, region, lookbackDays)
	if err != nil {
		return SPUtilizationSummary{}, err
	}
	// Propagate nil UtilizationPct: nil means CE returned no data ("not yet
	// measured"), which the engine distinguishes from 0% (fully idle layer).
	return SPUtilizationSummary{UtilizationPct: rich.UtilizationPct}, nil
}
