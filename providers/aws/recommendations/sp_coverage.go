package recommendations

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
)

// SPCoverageSummary is the Savings Plans coverage summary (optionally scoped
// to one region) over a lookback window.
//
// Unlike PoolCoverageMap (keyed by region:instance_type), SP coverage carries
// no pool key. Savings Plans commitment is dollar-denominated and applies
// cross-service (and, for Compute SPs, cross-region), so there is no
// meaningful per-instance-type breakdown at the coverage level. Any region
// scoping happens via the CE Filter on the request instead, so the summary
// stays flat. Coverage is also NOT plan-type-scoped - see GetSPCoverageSummary
// for why (CE filter contract).
//
// All money/percent fields are pointers so "CE returned no data" (nil) is
// distinguishable from "CE returned 0%" (pointer to 0.0). Days holds the
// number of daily data points that had a non-nil Coverage block in the CE
// response; zero means CE returned no coverage data at all for the window
// (e.g., no Savings Plans have ever been purchased in the account).
type SPCoverageSummary struct {
	// CoveragePct is covered / (covered + on-demand) * 100 over the window
	// (0-100): the share of SP-ELIGIBLE spend actually covered by Savings
	// Plans. CE's OnDemandCost is the UNCOVERED remainder, not the eligible
	// total, so the denominator must be the sum of both fields - dividing
	// by OnDemandCost alone overstates coverage and exceeds 100% as soon as
	// real coverage passes 50%. Nil ONLY when the eligible total is zero
	// (no SP-eligible activity in the window) or Days==0; a fully covered
	// window (OnDemandCost==0, covered>0) yields &100.0, not nil.
	CoveragePct *float64
	// CoveredUSDPerHour is the average SP-covered spend per hour over the
	// window (total SpendCoveredBySavingsPlans / windowHours). Nil when
	// Days==0.
	CoveredUSDPerHour *float64
	// OnDemandUSDPerHour is the average spend billed at on-demand rates per
	// hour, i.e. the SP-eligible spend NOT covered by any Savings Plan.
	// This is the uncovered portion (CE's OnDemandCost), not the eligible
	// total - see EligibleUSDPerHour for the total. Nil when Days==0.
	OnDemandUSDPerHour *float64
	// EligibleUSDPerHour is the average total SP-eligible spend per hour:
	// CoveredUSDPerHour + OnDemandUSDPerHour. Provided so consumers (the
	// ladder sizing math) do not have to re-derive the coverage
	// denominator. Nil when Days==0.
	EligibleUSDPerHour *float64
	// Days is the count of daily CE data points that had a non-nil Coverage
	// block in the response. Zero means CE returned no data for the window.
	Days int
}

// SPUtilizationSummary is the Savings Plans utilization summary for one plan
// type (and optionally one region) over a lookback window. All money/percent
// fields are pointers so "CE returned no data" (nil) is distinguishable from
// "CE returned 0%".
//
// UsedCommitmentUSDPerHour and TotalCommitmentUSDPerHour are derived by
// dividing the CE-reported period totals by windowHours so callers receive a
// consistent $/hr denomination, matching PoolCoverage.AvgInstancesPerHour.
type SPUtilizationSummary struct {
	// UtilizationPct is the percentage of the SP commitment actually consumed
	// over the window (0-100). Nil when CE returned no utilization data.
	UtilizationPct *float64
	// UsedCommitmentUSDPerHour is the average used SP commitment in $/hr.
	// Nil when CE returned no data.
	UsedCommitmentUSDPerHour *float64
	// TotalCommitmentUSDPerHour is the average total SP commitment in $/hr.
	// Nil when CE returned no data.
	TotalCommitmentUSDPerHour *float64
}

// spCoverageMetricSpendCovered is the value for the REQUIRED Metrics
// parameter of GetSavingsPlansCoverage. Per the Cost Explorer API reference,
// "SpendCoveredBySavingsPlans" is the SOLE valid value; omitting Metrics
// makes real calls fail or return incomplete data. There is no typed SDK
// constant for it - the types.Metric enum covers only the GetCostAndUsage
// cost metrics (BLENDED_COST etc.) - and the SDK's client-side validator
// checks only TimePeriod, which is why mock-backed tests could not catch
// the omission.
const spCoverageMetricSpendCovered = "SpendCoveredBySavingsPlans"

// maxSPCoveragePages caps the GetSavingsPlansCoverage NextToken loop,
// mirroring the maxRecommendationPages guard from issue #692 (client.go).
// Named separately because the workloads differ: 20 pages of daily coverage
// data is far beyond anything a real 13-month-max CE window can produce, so
// exceeding the cap indicates a token loop or API misbehavior and returns a
// diagnostic error instead of spinning (and billing $0.01/call) forever.
const maxSPCoveragePages = 20

// spPlanTypeDimensionValues translates the SupportedSavingsPlansType
// PARAMETER enum to the SAVINGS_PLANS_TYPE DIMENSION vocabulary.
//
// TWO-VOCABULARY TRAP (feedback_sdk_enum_string_literals class): the
// parameter enum used by GetSavingsPlansPurchaseRecommendation
// ("COMPUTE_SP", "EC2_INSTANCE_SP", ...) is NOT the vocabulary the
// SAVINGS_PLANS_TYPE filter dimension matches on. The dimension uses the
// CamelCase values below (the same vocabulary as the CUR savings_plan_type
// column). Sending the parameter-enum spelling makes CE silently match
// NOTHING - no error, just empty results - which is why no mock-backed test
// can prove this mapping. The SDK exposes no typed constants for the
// dimension vocabulary, hence this explicit map with fail-loud lookup.
var spPlanTypeDimensionValues = map[types.SupportedSavingsPlansType]string{
	types.SupportedSavingsPlansTypeComputeSp:     "ComputeSavingsPlans",
	types.SupportedSavingsPlansTypeEc2InstanceSp: "EC2InstanceSavingsPlans",
	types.SupportedSavingsPlansTypeSagemakerSp:   "SageMakerSavingsPlans",
	types.SupportedSavingsPlansTypeDatabaseSp:    "DatabaseSavingsPlans",
}

// spPlanTypeDimensionValue returns the SAVINGS_PLANS_TYPE dimension value
// for a plan-type parameter enum, failing loud on an unmapped value (e.g. a
// plan type added to the SDK enum after this map was written) rather than
// sending a spelling CE would silently match nothing on.
func spPlanTypeDimensionValue(planType types.SupportedSavingsPlansType) (string, error) {
	v, ok := spPlanTypeDimensionValues[planType]
	if !ok {
		return "", fmt.Errorf(
			"sp: no SAVINGS_PLANS_TYPE dimension value mapped for plan type %q; add it to spPlanTypeDimensionValues",
			planType)
	}
	return v, nil
}

// validateSPPlanType rejects empty or unknown Savings Plans type values at
// the API boundary (fail loud on unknown enum input rather than silently
// sending it to CE). The valid set comes from the SDK enum itself so a new
// SDK plan type is accepted automatically after an SDK upgrade.
func validateSPPlanType(planType types.SupportedSavingsPlansType) error {
	for _, v := range planType.Values() {
		if planType == v {
			return nil
		}
	}
	return fmt.Errorf("sp: unknown Savings Plans type %q (valid values: %v)",
		planType, planType.Values())
}

// spUtilizationFilter builds the CE Filter expression for
// GetSavingsPlansUtilization: a SAVINGS_PLANS_TYPE dimension carrying the
// mapped dimension-vocabulary value (see spPlanTypeDimensionValues - NOT the
// parameter-enum spelling), optionally ANDed with a REGION dimension when
// region is non-empty. Mirrors the And composition idiom of
// serviceRegionFilter in coverage.go. An empty region means "all regions"
// (no region dimension at all - relevant for Compute SPs, whose commitment
// floats across regions). Errors when the plan type has no mapped dimension
// value.
//
// DO NOT reuse this for GetSavingsPlansCoverage and do not re-symmetrize the
// two filter paths: the coverage API's Filter contract does NOT support the
// SAVINGS_PLANS_TYPE dimension (it supports only LINKED_ACCOUNT, REGION,
// SERVICE, and INSTANCE_FAMILY - see the GetSavingsPlansCoverageInput.Filter
// SDK doc), so sending SAVINGS_PLANS_TYPE to coverage fails with a
// ValidationException on every real call. Coverage uses
// spCoverageRegionFilter instead; the two helpers are deliberately separate.
func spUtilizationFilter(planType types.SupportedSavingsPlansType, region string) (*types.Expression, error) {
	dimensionValue, err := spPlanTypeDimensionValue(planType)
	if err != nil {
		return nil, err
	}
	planTypeExpr := types.Expression{
		Dimensions: &types.DimensionValues{
			Key:    types.DimensionSavingsPlansType,
			Values: []string{dimensionValue},
		},
	}
	if region == "" {
		return &planTypeExpr, nil
	}
	return &types.Expression{
		And: []types.Expression{
			planTypeExpr,
			{Dimensions: &types.DimensionValues{Key: types.DimensionRegion, Values: []string{region}}},
		},
	}, nil
}

// spCoverageRegionFilter builds the CE Filter expression for
// GetSavingsPlansCoverage: a bare REGION dimension when region is non-empty,
// or nil (no Filter field at all) when region is empty.
//
// DO NOT add a SAVINGS_PLANS_TYPE dimension here to mirror
// spUtilizationFilter: GetSavingsPlansCoverage's Filter supports only
// LINKED_ACCOUNT, REGION, SERVICE, and INSTANCE_FAMILY (per the
// GetSavingsPlansCoverageInput.Filter SDK doc); SAVINGS_PLANS_TYPE is a
// utilization-only dimension and CE rejects it here with a
// ValidationException. That asymmetry is why this helper is deliberately
// separate from spUtilizationFilter rather than a shared parameterized
// builder.
func spCoverageRegionFilter(region string) *types.Expression {
	if region == "" {
		return nil
	}
	return &types.Expression{
		Dimensions: &types.DimensionValues{Key: types.DimensionRegion, Values: []string{region}},
	}
}

// spCoverageAccumulator aggregates SP coverage data across paginated CE
// responses so the caller holds a single summary rather than a per-page slice.
type spCoverageAccumulator struct {
	totalCovered  float64
	totalOnDemand float64
	days          int
}

// add incorporates one SavingsPlansCoverage item into the accumulator.
// Items with a nil Coverage block are silently skipped; CE may omit the block
// for daily periods with no eligible SP activity, and skipping them keeps Days
// as a count of periods with actual data rather than a raw time-bucket count.
func (a *spCoverageAccumulator) add(cov types.SavingsPlansCoverage) error {
	if cov.Coverage == nil {
		return nil
	}
	if cov.Coverage.SpendCoveredBySavingsPlans != nil {
		v, err := parseSPFloat(aws.ToString(cov.Coverage.SpendCoveredBySavingsPlans), "SpendCoveredBySavingsPlans")
		if err != nil {
			return err
		}
		a.totalCovered += v
	}
	if cov.Coverage.OnDemandCost != nil {
		v, err := parseSPFloat(aws.ToString(cov.Coverage.OnDemandCost), "OnDemandCost")
		if err != nil {
			return err
		}
		a.totalOnDemand += v
	}
	a.days++
	return nil
}

// summarize converts the accumulated totals into an SPCoverageSummary.
// windowHours must equal lookbackDays*24 and be positive; zero or negative
// yields an empty summary (same as days==0).
func (a *spCoverageAccumulator) summarize(windowHours float64) SPCoverageSummary {
	if a.days == 0 || windowHours <= 0 {
		return SPCoverageSummary{Days: a.days}
	}
	covered := a.totalCovered / windowHours
	onDemand := a.totalOnDemand / windowHours
	eligible := covered + onDemand
	summary := SPCoverageSummary{
		CoveredUSDPerHour:  &covered,
		OnDemandUSDPerHour: &onDemand,
		EligibleUSDPerHour: &eligible,
		Days:               a.days,
	}
	// CE's OnDemandCost is the UNCOVERED spend, so the eligible total is
	// covered + onDemand and coverage = covered / eligible. Dividing by
	// OnDemandCost alone would overstate coverage (exceeding 100% past the
	// 50% mark) and would misreport a fully covered window (onDemand==0,
	// covered>0) as "no eligible activity". Pct is nil ONLY when the
	// eligible total is zero.
	if totalEligible := a.totalCovered + a.totalOnDemand; totalEligible > 0 {
		pct := (a.totalCovered / totalEligible) * 100
		summary.CoveragePct = &pct
	}
	return summary
}

// GetSPCoverageSummary fetches Savings Plans coverage over the last
// lookbackDays days and returns a summary the ladder engine can consume.
//
// Unlike GetSPUtilization, coverage is NOT plan-type-scoped, for two reasons:
//
//  1. CE filter contract: GetSavingsPlansCoverage's Filter supports only the
//     LINKED_ACCOUNT, REGION, SERVICE, and INSTANCE_FAMILY dimensions (see
//     the GetSavingsPlansCoverageInput.Filter SDK doc). SAVINGS_PLANS_TYPE
//     is a utilization-only dimension; passing it here fails with a
//     ValidationException on every real call.
//  2. Semantics: coverage measures the shared pool of SP-eligible on-demand
//     spend that is covered by ANY Savings Plan, so attributing coverage to
//     a single plan type is not meaningful - a dollar of eligible spend
//     covered by a Compute SP is indistinguishable, coverage-wise, from one
//     covered by an EC2 Instance SP.
//
// region == "" means all regions (no Filter at all); a non-empty region adds
// a REGION dimension filter (a supported coverage dimension).
//
// Coverage carries no pool key (unlike RI coverage's PoolCoverageMap) because
// SP commitment is dollar-denominated and applies cross-service; the return
// type is therefore a flat SPCoverageSummary rather than a keyed map.
//
// lookbackDays must be positive. Zero or negative is an error - the caller
// is responsible for supplying the window (per the no-hardcoded-window rule).
//
// An empty CE response (Days==0) is not an error: it means CE has no coverage
// data for the requested scope and window. Note that a nonexistent or
// misspelled region does NOT error either - CE simply matches nothing and
// the result is Days==0 with all pointer fields nil. Callers must read
// Days==0 as "no data for this scope", not "no SPs in the account", and
// check Days before dereferencing the pointer fields.
//
// Concurrent calls are safe: fetchSPCoveragePage derives operation-local
// retry state from the client's shared retry policy, so callers never mutate
// the same retry counter.
func (c *Client) GetSPCoverageSummary(ctx context.Context, region string, lookbackDays int) (SPCoverageSummary, error) {
	if lookbackDays <= 0 {
		return SPCoverageSummary{}, fmt.Errorf("sp coverage: lookbackDays must be positive, got %d", lookbackDays)
	}
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -lookbackDays)
	input := &costexplorer.GetSavingsPlansCoverageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start.Format("2006-01-02")),
			End:   aws.String(end.Format("2006-01-02")),
		},
		Granularity: types.GranularityDaily,
		// Metrics is REQUIRED by the API (not enforced by the SDK's
		// client-side validator) and "SpendCoveredBySavingsPlans" is its
		// sole valid value. GetSavingsPlansUtilization has no Metrics
		// parameter, so there is no counterpart on that path.
		Metrics: []string{spCoverageMetricSpendCovered},
		// Region-only filter (nil when region == ""). Deliberately NOT the
		// utilization filter shape: coverage's Filter contract rejects the
		// SAVINGS_PLANS_TYPE dimension - do not re-symmetrize the two paths.
		// See spCoverageRegionFilter / spUtilizationFilter.
		Filter: spCoverageRegionFilter(region),
	}

	var acc spCoverageAccumulator
	var token *string
	for pageIdx := 0; ; pageIdx++ {
		if err := ctx.Err(); err != nil {
			return SPCoverageSummary{}, fmt.Errorf("sp coverage: pagination canceled: %w", err)
		}
		if pageIdx >= maxSPCoveragePages {
			return SPCoverageSummary{}, fmt.Errorf(
				"sp coverage: pagination cap reached after %d pages (mirrors the issue #692 guard)",
				maxSPCoveragePages)
		}
		input.NextToken = token
		result, err := c.fetchSPCoveragePage(ctx, input)
		if err != nil {
			return SPCoverageSummary{}, err
		}
		for _, cov := range result.SavingsPlansCoverages {
			if addErr := acc.add(cov); addErr != nil {
				return SPCoverageSummary{}, fmt.Errorf("sp coverage: %w", addErr)
			}
		}
		if result.NextToken == nil || *result.NextToken == "" {
			break
		}
		token = result.NextToken
	}

	windowHours := float64(lookbackDays * 24)
	return acc.summarize(windowHours), nil
}

// fetchSPCoveragePage calls GetSavingsPlansCoverage with rate-limit retry.
// Mirrors fetchCoveragePage in coverage.go so both paths back off consistently.
func (c *Client) fetchSPCoveragePage(ctx context.Context, input *costexplorer.GetSavingsPlansCoverageInput) (*costexplorer.GetSavingsPlansCoverageOutput, error) {
	rateLimiter := c.rateLimiter.newOperation()
	for {
		if waitErr := rateLimiter.Wait(ctx); waitErr != nil {
			return nil, fmt.Errorf("rate limiter wait failed: %w", waitErr)
		}
		result, err := c.costExplorerClient.GetSavingsPlansCoverage(ctx, input)
		if !rateLimiter.ShouldRetry(err) {
			if err != nil {
				return nil, fmt.Errorf("failed to get SP coverage: %w", err)
			}
			return result, nil
		}
	}
}

// GetSPUtilization fetches Savings Plans utilization for one plan type over
// the last lookbackDays days.
//
// planType scopes the CE call to a single SAVINGS_PLANS_TYPE dimension value
// (typed SDK enum, validated at the boundary; empty or unknown values are an
// explicit error). Utilization is per-commitment: the ladder holds two
// distinct SP layers, and a blended account-wide utilization number would
// mask under-utilization in one layer being offset by over-utilization in
// the other. region == "" means all regions (no REGION filter); a non-empty
// region is ANDed with the plan-type filter. Unlike coverage, utilization's
// Filter contract DOES support SAVINGS_PLANS_TYPE - see spUtilizationFilter.
//
// GetSavingsPlansUtilization is a single-page API (the response has no
// NextToken); the Total field already aggregates the full window, so no
// pagination loop is needed.
//
// lookbackDays must be positive.
//
// Returned money fields (UsedCommitmentUSDPerHour, TotalCommitmentUSDPerHour)
// are CE's period totals divided by windowHours so callers receive consistent
// $/hr values. Nil fields mean CE returned no utilization data. As with
// coverage, a nonexistent or misspelled region (or a plan type with no
// commitments) does NOT error - CE matches nothing and every pointer field
// comes back nil. Callers must read all-nil as "no data for this scope",
// not "no SPs in the account".
//
// MANUAL VERIFICATION REQUIRED (mock-unprovable): the plan-type filter
// relies on the parameter-enum -> dimension-vocabulary mapping in
// spPlanTypeDimensionValues ("COMPUTE_SP" -> "ComputeSavingsPlans", ...).
// A wrong dimension value does not error - CE silently matches nothing -
// so no unit test can prove the mapping. Before trusting this path in
// production, verify once against the real API: call GetDimensionValues
// with Context=SAVINGS_PLANS and Dimension=SAVINGS_PLANS_TYPE (or run one
// real GetSavingsPlansUtilization per plan type) and confirm the returned
// dimension values match the map.
//
// Concurrent calls are safe: fetchSPUtilizationPage derives operation-local
// retry state from the client's shared retry policy, so callers never mutate
// the same retry counter.
func (c *Client) GetSPUtilization(ctx context.Context, planType types.SupportedSavingsPlansType, region string, lookbackDays int) (SPUtilizationSummary, error) {
	if err := validateSPPlanType(planType); err != nil {
		return SPUtilizationSummary{}, fmt.Errorf("sp utilization: %w", err)
	}
	if lookbackDays <= 0 {
		return SPUtilizationSummary{}, fmt.Errorf("sp utilization: lookbackDays must be positive, got %d", lookbackDays)
	}
	// Plan-type filter, optionally ANDed with region. This shape is valid
	// ONLY for the utilization API - coverage rejects SAVINGS_PLANS_TYPE.
	// See spUtilizationFilter / spCoverageRegionFilter.
	filter, err := spUtilizationFilter(planType, region)
	if err != nil {
		return SPUtilizationSummary{}, fmt.Errorf("sp utilization: %w", err)
	}
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -lookbackDays)
	input := &costexplorer.GetSavingsPlansUtilizationInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start.Format("2006-01-02")),
			End:   aws.String(end.Format("2006-01-02")),
		},
		Granularity: types.GranularityDaily,
		Filter:      filter,
	}
	result, err := c.fetchSPUtilizationPage(ctx, input)
	if err != nil {
		return SPUtilizationSummary{}, err
	}
	windowHours := float64(lookbackDays * 24)
	return buildSPUtilizationSummary(result, windowHours)
}

// fetchSPUtilizationPage calls GetSavingsPlansUtilization with rate-limit retry.
// Mirrors fetchUtilizationPage in utilization.go so both paths back off consistently.
func (c *Client) fetchSPUtilizationPage(ctx context.Context, input *costexplorer.GetSavingsPlansUtilizationInput) (*costexplorer.GetSavingsPlansUtilizationOutput, error) {
	rateLimiter := c.rateLimiter.newOperation()
	for {
		if waitErr := rateLimiter.Wait(ctx); waitErr != nil {
			return nil, fmt.Errorf("rate limiter wait failed: %w", waitErr)
		}
		result, err := c.costExplorerClient.GetSavingsPlansUtilization(ctx, input)
		if !rateLimiter.ShouldRetry(err) {
			if err != nil {
				return nil, fmt.Errorf("failed to get SP utilization: %w", err)
			}
			return result, nil
		}
	}
}

// buildSPUtilizationSummary converts a CE GetSavingsPlansUtilization response
// into an SPUtilizationSummary. windowHours converts CE's period-total dollar
// amounts into $/hr rates consistent with PoolCoverage.AvgInstancesPerHour.
func buildSPUtilizationSummary(result *costexplorer.GetSavingsPlansUtilizationOutput, windowHours float64) (SPUtilizationSummary, error) {
	if result.Total == nil || result.Total.Utilization == nil {
		return SPUtilizationSummary{}, nil
	}
	u := result.Total.Utilization
	var summary SPUtilizationSummary
	if u.UtilizationPercentage != nil {
		v, err := parseSPFloat(aws.ToString(u.UtilizationPercentage), "UtilizationPercentage")
		if err != nil {
			return SPUtilizationSummary{}, err
		}
		summary.UtilizationPct = &v
	}
	if windowHours > 0 {
		if u.UsedCommitment != nil {
			v, err := parseSPFloat(aws.ToString(u.UsedCommitment), "UsedCommitment")
			if err != nil {
				return SPUtilizationSummary{}, err
			}
			rate := v / windowHours
			summary.UsedCommitmentUSDPerHour = &rate
		}
		if u.TotalCommitment != nil {
			v, err := parseSPFloat(aws.ToString(u.TotalCommitment), "TotalCommitment")
			if err != nil {
				return SPUtilizationSummary{}, err
			}
			rate := v / windowHours
			summary.TotalCommitmentUSDPerHour = &rate
		}
	}
	return summary, nil
}

// parseSPFloat parses a Cost Explorer string value into a float64, returning
// an explicit error on unparseable values, NaN/Inf, or negative values
// (all of which are invalid for financial metrics). Unlike the package-level
// parseFloat helper in utilization.go, this never silently falls back to zero.
func parseSPFloat(s, field string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("sp: field %q: cannot parse %q as float: %w", field, s, err)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("sp: field %q: non-finite value %q", field, s)
	}
	if v < 0 {
		return 0, fmt.Errorf("sp: field %q: negative value %g is invalid for a financial metric", field, v)
	}
	return v, nil
}
