package recommendations

import (
	"context"
	"fmt"
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

// onDemandMetric is the CE metric for GetCostAndUsage. UNBLENDED_COST is the
// actual charge to the account at the billed rate; for on-demand instances
// this equals on-demand rate x hours. Named constant (not magic string) per
// feedback_no_hardcoded_magic_values; the SDK types.MetricUnblendedCost is
// used to derive it so it cannot drift from the CE enum vocabulary.
const onDemandMetric = string(types.MetricUnblendedCost)

// maxOnDemandSeriesPages caps the GetCostAndUsage NextPageToken loop.
// Daily granularity over a 30-day window produces at most 1 page; the cap
// guards against a runaway token loop or API misbehavior and returns a
// diagnostic error instead of billing $0.01/call indefinitely.
const maxOnDemandSeriesPages = 20

// GetOnDemandSeries fetches daily on-demand EC2 compute cost for the given
// region over the past lookbackDays days from CE GetCostAndUsage and returns
// a slice of len(returned_days) USD/hr values ordered oldest-to-newest.
//
// CE query parameters:
//   - Granularity: DAILY (one entry per calendar day)
//   - Metric: UNBLENDED_COST (actual on-demand billed rate; spec: unblended)
//   - Filter: SERVICE = EC2 compute AND PURCHASE_TYPE = On Demand Instances
//     AND REGION = region (three-clause AND)
//   - Time window: [now-lookbackDays, now) exclusive end
//
// Each day's total USD is divided by 24.0 to yield USD/hr. Dividing by 24 is
// derived from the DAILY granularity contract (one day = 24 hours), not a
// magic constant.
//
// CE typically lags ~24-48h, so the returned series may be shorter than
// lookbackDays. The caller (baseline.GetUsageBaseline) enforces the minimum-
// length requirement (minBaselineSeriesDays); this function only errors on a
// completely empty result.
//
// TODO(#1365): when the feat/ladder-stale-series PR merges, change the
// interface to return []DailyPoint{Date time.Time; USDPerHour float64} instead
// of []float64. The accumulator below already builds per-date entries; the
// final flatten step is the only change needed.
func (c *Client) GetOnDemandSeries(ctx context.Context, region string, lookbackDays int) ([]float64, error) {
	if lookbackDays <= 0 {
		return nil, fmt.Errorf("GetOnDemandSeries: lookbackDays must be > 0, got %d", lookbackDays)
	}
	if region == "" {
		return nil, fmt.Errorf("GetOnDemandSeries: region must not be empty")
	}

	end := time.Now().UTC().Truncate(24 * time.Hour) // midnight today (exclusive end for CE)
	start := end.AddDate(0, 0, -lookbackDays)

	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start.Format("2006-01-02")),
			End:   aws.String(end.Format("2006-01-02")),
		},
		Granularity: types.GranularityDaily,
		Metrics:     []string{onDemandMetric},
		Filter:      onDemandSeriesFilter(region),
	}

	// TODO(#1365): build []struct{Date time.Time; USDPerHour float64} here
	// and flatten to []float64 at the return site once the interface changes.
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

	if len(byDate) == 0 {
		return nil, fmt.Errorf("GetOnDemandSeries: CE returned no on-demand data for region %q over the past %d days (account may have no on-demand EC2 spend, or CE data not yet available)",
			region, lookbackDays)
	}

	return sortedDailySeries(byDate), nil
}

// fetchOnDemandPage calls GetCostAndUsage with rate-limit retry, mirroring
// the fetchCoveragePage / fetchUtilizationPage pattern in coverage.go and
// utilization.go. Each attempt acquires one rate-limiter slot at the API call
// site (not at goroutine creation) per feedback_semaphore_at_api_call.
func (c *Client) fetchOnDemandPage(ctx context.Context, input *costexplorer.GetCostAndUsageInput) (*costexplorer.GetCostAndUsageOutput, error) {
	c.rateLimiter.Reset()
	for {
		if waitErr := c.rateLimiter.Wait(ctx); waitErr != nil {
			return nil, fmt.Errorf("rate limiter wait: %w", waitErr)
		}
		out, err := c.costExplorerClient.GetCostAndUsage(ctx, input)
		if !c.rateLimiter.ShouldRetry(err) {
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
// GetCostAndUsage results and merges them into byDate. Days with a missing or
// nil metric are stored as 0 (CE may omit a day if spend was exactly $0).
// Days with an unparseable amount string fail loud (feedback_strict_int_parse).
// Dividing by 24 converts daily USD to USD/hr; 24 is derived from the DAILY
// granularity contract (one day = 24 hours), not a magic constant.
func accumulateDailyResults(byDate map[string]float64, out *costexplorer.GetCostAndUsageOutput) error {
	for _, r := range out.ResultsByTime {
		if r.TimePeriod == nil || r.TimePeriod.Start == nil {
			continue
		}
		dateStr := aws.ToString(r.TimePeriod.Start)
		mv, ok := r.Total[onDemandMetric]
		if !ok || mv.Amount == nil {
			byDate[dateStr] = 0
			continue
		}
		usd, err := strconv.ParseFloat(aws.ToString(mv.Amount), 64)
		if err != nil {
			return fmt.Errorf("cannot parse CE amount %q for day %s: %w",
				aws.ToString(mv.Amount), dateStr, err)
		}
		// Divide total daily USD by 24 to get USD/hr. Derived from DAILY
		// granularity: 1 day = 24 hours; not a magic constant.
		byDate[dateStr] = usd / 24.0
	}
	return nil
}

// sortedDailySeries converts a date-keyed cost map to a []float64 sorted
// oldest-to-newest (lexicographic sort on "2006-01-02" strings is
// chronological). The baseline contract requires oldest-first ordering.
func sortedDailySeries(byDate map[string]float64) []float64 {
	dates := make([]string, 0, len(byDate))
	for d := range byDate {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	series := make([]float64, len(dates))
	for i, d := range dates {
		series[i] = byDate[d]
	}
	return series
}
