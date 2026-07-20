package ladder

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdksp "github.com/aws/aws-sdk-go-v2/service/savingsplans"
	sptypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"

	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
)

// activeSPListAPI is the minimal interface for listing Savings Plans.
// Only DescribeSavingsPlans is needed; the full SavingsPlansAPI from
// providers/aws/services/savingsplans includes purchase and offering methods
// the read-only lister does not require (interface-segregation principle).
// Tests inject a hermetic fake implementing this narrow interface.
type activeSPListAPI interface {
	DescribeSavingsPlans(ctx context.Context, params *sdksp.DescribeSavingsPlansInput, optFns ...func(*sdksp.Options)) (*sdksp.DescribeSavingsPlansOutput, error)
}

// spListerStates is the set of Savings Plan states counted as existing
// commitment.
//   - active: currently billing.
//   - payment-pending: purchase accepted, first payment in flight. A
//     just-purchased SP sits in this state briefly; it MUST count as existing
//     commitment immediately, or the next scheduled run would not see it and
//     double-purchase. Safe default before the L6 write side lands.
//   - queued (future-dated) SPs are deliberately EXCLUDED: they have not
//     started, so counting them would suppress purchases the queued SP will
//     not cover until its start date. Revisit with L14 (expiry alignment),
//     which can reason about future-dated coverage explicitly.
var spListerStates = []sptypes.SavingsPlanState{
	sptypes.SavingsPlanStateActive,
	sptypes.SavingsPlanStatePaymentPending,
}

// spListerAdapter implements the spLister interface using the AWS Savings
// Plans SDK directly. It filters to spListerStates (active + payment-pending;
// see that var for rationale) and scopes EC2Instance SPs to the ladder's
// region. Commitment strings are parsed with strconv.ParseFloat; parse errors
// fail loud per feedback_strict_int_parse and feedback_no_silent_fallbacks.
type spListerAdapter struct {
	api activeSPListAPI
	// region is the ladder's configured region. DescribeSavingsPlans is an
	// account-wide API, but the ladder's layer state is region-scoped:
	// EC2Instance SPs from other regions must not inflate this region's
	// ExistingUSDPerHour (that would understate the gap and under-purchase).
	region string
}

// ListActiveSPs lists the account's Savings Plans in spListerStates and maps
// them to the AWSLadder view, region-scoped:
//
//   - EC2Instance SPs are region-bound; entries whose Region differs from the
//     adapter's region are EXCLUDED (they cover other regions' usage).
//   - Compute SPs are global and are counted fully in every region. This is
//     deliberately conservative: attributing the full global commitment to
//     this region can only make the engine see MORE existing coverage and
//     purchase LESS, never over-purchase. Multi-region laddering (L18) will
//     revisit this attribution.
//
// Commitment strings and SP dates are validated at the boundary and fail
// loud (feedback_strict_int_parse, feedback_no_silent_fallbacks).
//
// Pagination is fully exhausted (issue #692): DescribeSavingsPlans documents
// no page limit, so a fixed page cap would silently truncate accounts with
// more Savings Plans than the cap (understating existing commitment). The
// only loop guard needed is against a REPEATED NextToken, which indicates
// API misbehavior rather than a legitimately long listing.
func (a *spListerAdapter) ListActiveSPs(ctx context.Context) ([]ActiveSP, error) {
	var sps []ActiveSP
	var nextToken *string
	seenTokens := make(map[string]struct{})
	page := 0

	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("ListActiveSPs: context cancelled: %w", err)
		}
		page++

		input := &sdksp.DescribeSavingsPlansInput{
			States:     spListerStates,
			NextToken:  nextToken,
			MaxResults: aws.Int32(100),
		}
		out, err := a.api.DescribeSavingsPlans(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("ListActiveSPs: DescribeSavingsPlans page %d: %w", page, err)
		}

		sps, err = appendRegionScopedSPs(sps, out.SavingsPlans, a.region)
		if err != nil {
			return nil, err
		}

		tok := aws.ToString(out.NextToken)
		if tok == "" {
			break
		}
		if _, dup := seenTokens[tok]; dup {
			return nil, fmt.Errorf("ListActiveSPs: DescribeSavingsPlans returned a repeated pagination token on page %d; aborting to avoid an infinite loop", page)
		}
		seenTokens[tok] = struct{}{}
		nextToken = out.NextToken
	}
	return sps, nil
}

// appendRegionScopedSPs maps one DescribeSavingsPlans page onto the
// accumulated slice, applying the region-scoping rule: EC2Instance SPs bound
// to another region cover that region's usage, not ours; including them
// would inflate ExistingUSDPerHour and under-purchase. Compute SPs are
// global and always kept (see ListActiveSPs doc). Extracted to keep
// ListActiveSPs under the cyclomatic complexity limit.
func appendRegionScopedSPs(sps []ActiveSP, page []sptypes.SavingsPlan, region string) ([]ActiveSP, error) {
	for _, sp := range page {
		entry, err := mapActiveSP(sp)
		if err != nil {
			return nil, err
		}
		if entry.PlanType == spPlanTypeEC2Instance && entry.Region != region {
			continue
		}
		sps = append(sps, entry)
	}
	return sps, nil
}

// mapActiveSP converts one DescribeSavingsPlans entry to ActiveSP.
// Fails loud on a missing SavingsPlanId (would make the entry
// un-identifiable), on a non-numeric Commitment string (money path: cannot
// represent an absent number as 0, per feedback_nullable_not_zero and
// feedback_strict_int_parse), on missing or unparseable Start/End dates
// (a silently zero EndDate would drop the SP from sumExpiringSPHourlyCost
// and understate expiring commitment), and on an EC2Instance-plan SP with
// an empty Region (AWS always populates Region for region-bound plans, so
// an empty one means corrupted data; letting it fall through to the
// region-scope filter would silently exclude it, understating existing
// commitment and over-purchasing).
func mapActiveSP(sp sptypes.SavingsPlan) (ActiveSP, error) {
	if sp.SavingsPlanId == nil {
		return ActiveSP{}, fmt.Errorf("ListActiveSPs: DescribeSavingsPlans returned entry with nil SavingsPlanId")
	}
	if string(sp.SavingsPlanType) == spPlanTypeEC2Instance && aws.ToString(sp.Region) == "" {
		return ActiveSP{}, fmt.Errorf("ListActiveSPs: EC2Instance SP %s has an empty Region; AWS always populates Region for region-bound plans, so this indicates corrupted data (silently excluding it would understate existing commitment)",
			*sp.SavingsPlanId)
	}
	commitment := aws.ToString(sp.Commitment)
	hourly, err := strconv.ParseFloat(commitment, 64)
	if err != nil {
		return ActiveSP{}, fmt.Errorf("ListActiveSPs: cannot parse Commitment %q for SP %s: %w",
			commitment, *sp.SavingsPlanId, err)
	}
	// strconv.ParseFloat accepts "NaN"/"Inf" with a nil error; a non-finite or
	// negative hourly commitment would flow through sumSPHourlyCost /
	// sumExpiringSPHourlyCost into the ladder layer-state totals the engine
	// sizes purchases from. Fail loud (a commitment is a non-negative money rate).
	if math.IsNaN(hourly) || math.IsInf(hourly, 0) || hourly < 0 {
		return ActiveSP{}, fmt.Errorf("ListActiveSPs: Commitment %q for SP %s is not a finite non-negative number",
			commitment, *sp.SavingsPlanId)
	}
	start, err := parseSPDate("Start", sp.Start, *sp.SavingsPlanId)
	if err != nil {
		return ActiveSP{}, err
	}
	end, err := parseSPDate("End", sp.End, *sp.SavingsPlanId)
	if err != nil {
		return ActiveSP{}, err
	}

	return ActiveSP{
		PlanID:              *sp.SavingsPlanId,
		PlanType:            string(sp.SavingsPlanType),
		State:               string(sp.State),
		Region:              aws.ToString(sp.Region),
		StartDate:           start,
		EndDate:             end,
		HourlyCommitmentUSD: hourly,
	}, nil
}

// parseSPDate parses a DescribeSavingsPlans RFC3339 date field, failing loud
// on a nil or unparseable value. Expiry math (sumExpiringSPHourlyCost)
// depends on EndDate; a silently zero date would exclude the SP from the
// expiring sum and understate expiring commitment
// (feedback_no_silent_fallbacks).
func parseSPDate(field string, value *string, planID string) (time.Time, error) {
	if value == nil {
		return time.Time{}, fmt.Errorf("ListActiveSPs: SP %s has nil %s date; expiry math requires it", planID, field)
	}
	t, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return time.Time{}, fmt.Errorf("ListActiveSPs: cannot parse %s date %q for SP %s: %w",
			field, *value, planID, err)
	}
	return t, nil
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
