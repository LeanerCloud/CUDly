package recommendations

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
)

// ec2ComputeService is the CE SERVICE dimension value for EC2 compute.
// Matches the exact string CE uses; deviations silently return empty results.
const ec2ComputeService = "Amazon Elastic Compute Cloud - Compute"

// purchaseTypeOnDemand is the CE PURCHASE_TYPE dimension value for on-demand
// instances. Filters to usage billed at on-demand rates (excludes RI-covered,
// SP-covered, and Spot). Using the uncovered on-demand spend as the baseline
// is conservative: existing coverage is separately counted by GetLayerStates,
// so the engine correctly nets it into the gap calculation.
const purchaseTypeOnDemand = "On Demand Instances"

// onDemandMetric is the CE metric name for GetCostAndUsage. UnblendedCost is
// the actual charge to the account at the billed rate; for on-demand
// instances this equals on-demand rate x hours.
//
// The valid Metrics vocabulary for THIS operation is CamelCase
// ("AmortizedCost", "BlendedCost", "NetAmortizedCost", "NetUnblendedCost",
// "NormalizedUsageAmount", "UnblendedCost", "UsageQuantity") per the
// GetCostAndUsageInput.Metrics SDK doc (api_op_GetCostAndUsage.go). Do NOT
// substitute string(types.MetricUnblendedCost): that enum yields
// SCREAMING_SNAKE "UNBLENDED_COST", which belongs to other CE APIs
// (recommendation/utilization), and GetCostAndUsage rejects it with a
// ValidationException on every call (feedback_verify_api_filter_contracts:
// verify against the actual API op's doc, not a same-SDK enum). The same
// name keys the per-day r.Total lookup because CE echoes request metric
// names as response keys.
const onDemandMetric = "UnblendedCost"

// ceDateLayout is the calendar-day format CE uses for DateInterval boundaries
// and ResultByTime period starts ("YYYY-MM-DD"). time.Parse with this layout
// yields midnight UTC, which is the normalization the ladder baseline expects.
const ceDateLayout = "2006-01-02"

// maxOnDemandSeriesPages caps the GetCostAndUsage NextPageToken loop.
// Daily granularity over a 30-day window produces at most 1 page; the cap
// guards against a runaway token loop or API misbehavior and returns a
// diagnostic error instead of billing $0.01/call indefinitely.
const maxOnDemandSeriesPages = 20

// DailyCost is one calendar day of on-demand cost from CE GetCostAndUsage.
// Date is midnight UTC of the calendar day the entry covers; USDPerHour is
// the day's total unblended cost divided by 24. The ladder package maps this
// to its own DailyPoint type (identical fields) via a thin adapter; the type
// is duplicated because recommendations cannot import providers/aws/ladder
// (ladder already imports recommendations).
type DailyCost struct {
	// Date is the UTC calendar day (midnight UTC) this entry covers.
	Date time.Time
	// USDPerHour is the on-demand-equivalent spend averaged over the day.
	USDPerHour float64
}

// GetOnDemandSeries fetches daily on-demand EC2 compute cost for the given
// region over the past lookbackDays days from CE GetCostAndUsage and returns
// one DailyCost per returned calendar day, ordered oldest-to-newest with
// strictly increasing unique UTC days (map accumulation dedupes by date;
// lexicographic sort of "YYYY-MM-DD" keys is chronological). The last element
// is the most recent day CE has data for, which the ladder baseline's
// freshness check relies on.
//
// CE query parameters:
//   - Granularity: DAILY (one entry per calendar day)
//   - Metric: UnblendedCost (actual on-demand billed rate; see onDemandMetric)
//   - Filter: SERVICE = EC2 compute AND PURCHASE_TYPE = On Demand Instances
//     AND REGION = region (three-clause AND)
//   - Time window: [now-lookbackDays, now) exclusive end
//
// Each day's total USD is divided by 24.0 to yield USD/hr. Dividing by 24 is
// derived from the DAILY granularity contract (one day = 24 hours), not a
// magic constant.
//
// CE typically lags ~24-48h, so the returned series may be shorter than
// lookbackDays. The caller (baseline.GetUsageBaseline) enforces minimum
// length, in-window coverage, and freshness.
//
// Fail-loud conditions (feedback_no_silent_fallbacks):
//   - Empty result: error.
//   - A result row missing the requested metric key (or with a nil Amount):
//     error. CE echoes every requested metric on every row (genuine $0 days
//     arrive as Amount:"0"), so a missing key means the request vocabulary is
//     wrong, and fabricating a 0 would silently corrupt the baseline.
//   - An all-zero series: error. When a filter or metric name is wrong, CE
//     returns a complete, fresh, chronological series of $0 rows that would
//     pass every downstream validation and make the engine size purchases
//     from fabricated data. An account with genuinely zero on-demand EC2
//     spend for the whole lookback has nothing to ladder, so erroring is
//     correct there too.
func (c *Client) GetOnDemandSeries(ctx context.Context, region string, lookbackDays int) ([]DailyCost, error) {
	if err := validateOnDemandSeriesArgs(region, lookbackDays); err != nil {
		return nil, err
	}

	end := time.Now().UTC().Truncate(24 * time.Hour) // midnight today (exclusive end for CE)
	start := end.AddDate(0, 0, -lookbackDays)

	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start.Format(ceDateLayout)),
			End:   aws.String(end.Format(ceDateLayout)),
		},
		Granularity: types.GranularityDaily,
		Metrics:     []string{onDemandMetric},
		Filter:      onDemandSeriesFilter(region),
	}

	byDate := make(map[string]float64)

	var nextToken *string
	page := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("GetOnDemandSeries: context cancelled during pagination: %w", err)
		}
		page++
		if page > maxOnDemandSeriesPages {
			return nil, fmt.Errorf("GetOnDemandSeries: exceeded %d page cap; possible CE token loop", maxOnDemandSeriesPages)
		}
		input.NextPageToken = nextToken

		out, err := c.fetchOnDemandPage(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("GetOnDemandSeries: %w", err)
		}

		if err := accumulateDailyResults(byDate, out); err != nil {
			return nil, fmt.Errorf("GetOnDemandSeries: %w", err)
		}

		// aws.ToString returns "" for nil, so this single check covers both nil
		// and empty-string tokens.
		if aws.ToString(out.NextPageToken) == "" {
			break
		}
		nextToken = out.NextPageToken
	}

	series, err := buildDailySeries(byDate, region, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("GetOnDemandSeries: %w", err)
	}
	return series, nil
}

// validateOnDemandSeriesArgs rejects out-of-range GetOnDemandSeries arguments
// at the boundary. Extracted to keep GetOnDemandSeries under the cyclomatic
// complexity limit.
func validateOnDemandSeriesArgs(region string, lookbackDays int) error {
	if lookbackDays <= 0 {
		return fmt.Errorf("GetOnDemandSeries: lookbackDays must be > 0, got %d", lookbackDays)
	}
	if region == "" {
		return fmt.Errorf("GetOnDemandSeries: region must not be empty")
	}
	return nil
}

// fetchOnDemandPage calls GetCostAndUsage with rate-limit retry, mirroring
// the fetchCoveragePage / fetchUtilizationPage pattern in coverage.go and
// utilization.go. Each attempt acquires one rate-limiter slot at the API call
// site (not at goroutine creation) per feedback_semaphore_at_api_call.
func (c *Client) fetchOnDemandPage(ctx context.Context, input *costexplorer.GetCostAndUsageInput) (*costexplorer.GetCostAndUsageOutput, error) {
	rl := c.newRateLimiter()
	for {
		if waitErr := rl.Wait(ctx); waitErr != nil {
			return nil, fmt.Errorf("rate limiter wait: %w", waitErr)
		}
		out, err := c.costExplorerClient.GetCostAndUsage(ctx, input)
		if !rl.ShouldRetry(err) {
			if err != nil {
				return nil, fmt.Errorf("GetCostAndUsage: %w", err)
			}
			return out, nil
		}
	}
}

// onDemandSeriesFilter builds the three-clause AND filter for
// GetCostAndUsage: EC2 compute service, on-demand purchase type, and the
// given region. All three clauses are required:
//   - SERVICE scopes to EC2 compute (excludes RDS, ElastiCache, etc.).
//   - PURCHASE_TYPE scopes to on-demand charges (excludes RI-covered,
//     SP-covered, and Spot; those are accounted for via GetLayerStates).
//   - REGION scopes to the ladder's configured region.
//
// Per feedback_verify_api_filter_contracts: GetCostAndUsage supports SERVICE,
// PURCHASE_TYPE, and REGION as valid filter dimensions (CE API reference,
// GetCostAndUsageInput.Filter). SAVINGS_PLANS_TYPE is NOT supported here
// (that is a GetSavingsPlansCoverage/Utilization dimension only).
func onDemandSeriesFilter(region string) *types.Expression {
	return &types.Expression{
		And: []types.Expression{
			{Dimensions: &types.DimensionValues{
				Key:    types.DimensionService,
				Values: []string{ec2ComputeService},
			}},
			{Dimensions: &types.DimensionValues{
				Key:    types.DimensionPurchaseType,
				Values: []string{purchaseTypeOnDemand},
			}},
			{Dimensions: &types.DimensionValues{
				Key:    types.DimensionRegion,
				Values: []string{region},
			}},
		},
	}
}

// accumulateDailyResults extracts daily USD/hr values from one page of
// GetCostAndUsage results and merges them into byDate. All malformed rows
// fail loud (feedback_no_silent_fallbacks):
//   - A row missing its TimePeriod/Start cannot be dated; silently skipping
//     it would leave an incomplete series that can still pass downstream
//     minimum-length checks and produce an incorrect baseline.
//   - A row missing the requested metric key (or carrying a nil Amount)
//     signals a request-vocabulary bug: CE echoes every requested metric on
//     every returned row, with genuine $0 days arriving as Amount:"0";
//     writing a fabricated 0 would silently corrupt the baseline.
//   - Unparseable amount strings fail loud (feedback_strict_int_parse).
//
// Dividing by 24 converts daily USD to USD/hr; 24 is derived from the DAILY
// granularity contract (one day = 24 hours), not a magic constant.
func accumulateDailyResults(byDate map[string]float64, out *costexplorer.GetCostAndUsageOutput) error {
	for i, r := range out.ResultsByTime {
		if r.TimePeriod == nil || r.TimePeriod.Start == nil {
			return fmt.Errorf("CE result row %d is missing its period start; a row that cannot be dated would leave an undetectable gap in the daily series", i)
		}
		dateStr := aws.ToString(r.TimePeriod.Start)
		mv, ok := r.Total[onDemandMetric]
		if !ok || mv.Amount == nil {
			return fmt.Errorf("CE result row for day %s is missing the %q metric; CE echoes every requested metric on every row, so this indicates a request-vocabulary bug (genuine $0 days arrive as Amount:\"0\")",
				dateStr, onDemandMetric)
		}
		usd, err := strconv.ParseFloat(aws.ToString(mv.Amount), 64)
		if err != nil {
			return fmt.Errorf("cannot parse CE amount %q for day %s: %w",
				aws.ToString(mv.Amount), dateStr, err)
		}
		// strconv.ParseFloat accepts "NaN"/"Inf" with a nil error; a non-finite
		// daily amount would slip past the downstream all-zero fail-loud check
		// (NaN != 0) and poison the ladder baseline. Fail loud here instead. No
		// negative guard: CE unblended cost is legitimately negative on
		// credit/refund days.
		if math.IsNaN(usd) || math.IsInf(usd, 0) {
			return fmt.Errorf("CE amount %q for day %s is not a finite number",
				aws.ToString(mv.Amount), dateStr)
		}
		// Divide total daily USD by 24 to get USD/hr. Derived from DAILY
		// granularity: 1 day = 24 hours; not a magic constant.
		byDate[dateStr] = usd / 24.0
	}
	return nil
}

// buildDailySeries converts the date-keyed cost map into the final validated
// []DailyCost:
//   - Empty map: error (no data from CE).
//   - Sorted oldest-to-newest (lexicographic sort on "2006-01-02" strings is
//     chronological). Map keys are unique, so the result has strictly
//     increasing unique UTC days, which the ladder baseline's chronology
//     check requires.
//   - A date string that does not parse fails loud: it indicates a CE
//     contract violation the caller must see (feedback_no_silent_fallbacks).
//   - An all-zero series fails loud: a wrong filter or metric name makes CE
//     return a complete series of $0 rows that would pass every downstream
//     validation and let the engine size purchases from fabricated data. An
//     account with genuinely zero on-demand EC2 spend over the whole window
//     has nothing to ladder, so erroring is correct in that case too.
func buildDailySeries(byDate map[string]float64, region string, lookbackDays int) ([]DailyCost, error) {
	if len(byDate) == 0 {
		return nil, fmt.Errorf("CE returned no on-demand data for region %q over the past %d days (account may have no on-demand EC2 spend, or CE data not yet available)",
			region, lookbackDays)
	}

	dates := make([]string, 0, len(byDate))
	for d := range byDate {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	allZero := true
	series := make([]DailyCost, len(dates))
	for i, d := range dates {
		day, err := time.Parse(ceDateLayout, d)
		if err != nil {
			return nil, fmt.Errorf("cannot parse CE period start date %q: %w", d, err)
		}
		if byDate[d] != 0 {
			allZero = false
		}
		series[i] = DailyCost{Date: day, USDPerHour: byDate[d]}
	}
	if allZero {
		return nil, fmt.Errorf("CE returned an all-zero on-demand series for region %q over the past %d days; either the account has no on-demand EC2 spend to ladder, or the CE filter/metric vocabulary is wrong (a bad filter yields complete $0 rows, not an empty result)",
			region, lookbackDays)
	}
	return series, nil
}
