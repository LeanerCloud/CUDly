package recommendations

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
)

// usageHistoryLookbackDays is the number of daily samples fetched for the
// inline sparkline. 7 days is wide enough to show a weekly pattern without
// ballooning the payload; it matches the default LookbackPeriod used by
// GetRecommendationsForService.
const usageHistoryLookbackDays = 7

// GetDailyUsagePcts returns a slice of daily RI-coverage percentages (0-100)
// for the given (service, resourceType, region) over the last
// usageHistoryLookbackDays days, ordered oldest-to-newest. Each element
// corresponds to one calendar day. If CE returns no data for a day the slot
// is filled with 0.0 so the sparkline always has exactly
// usageHistoryLookbackDays points when data is present.
//
// Returns (nil, nil) on API success when CE has no historical data at all for
// the tuple; callers store nil so the frontend renders "—" rather than a
// flat-zero sparkline.
//
// serviceFilter is the canonical CE SERVICE dimension value (e.g.
// "Amazon Elastic Compute Cloud - Compute"). The resourceType is matched
// against the INSTANCE_TYPE dimension so we only pick up coverage for the
// exact SKU the recommendation targets.
func (c *Client) GetDailyUsagePcts(ctx context.Context, serviceFilter, resourceType, region string) ([]float64, error) {
	if serviceFilter == "" || resourceType == "" || region == "" {
		return nil, nil
	}

	end := time.Now().UTC()
	start := end.AddDate(0, 0, -usageHistoryLookbackDays)

	dayPct, start2 := newDayWindow(start)
	anyData, err := c.fetchDailyCoverage(ctx, serviceFilter, resourceType, region, start2, end, dayPct)
	if err != nil {
		return nil, err
	}
	if !anyData {
		return nil, nil
	}
	return orderedDaySlice(start2, dayPct), nil
}

// newDayWindow initialises a zeroed day map for the lookback window starting at
// start and returns both the map and start unchanged (to keep the call site
// readable).
func newDayWindow(start time.Time) (map[string]float64, time.Time) {
	dayPct := make(map[string]float64, usageHistoryLookbackDays)
	for i := 0; i < usageHistoryLookbackDays; i++ {
		dayPct[start.AddDate(0, 0, i).Format("2006-01-02")] = 0.0
	}
	return dayPct, start
}

// orderedDaySlice converts dayPct into a slice ordered oldest-to-newest.
func orderedDaySlice(start time.Time, dayPct map[string]float64) []float64 {
	out := make([]float64, usageHistoryLookbackDays)
	for i := 0; i < usageHistoryLookbackDays; i++ {
		out[i] = dayPct[start.AddDate(0, 0, i).Format("2006-01-02")]
	}
	return out
}

// fetchDailyCoverage pages through GetReservationCoverage and applies matching
// results into dayPct. Returns true if any data was written.
func (c *Client) fetchDailyCoverage(ctx context.Context, serviceFilter, resourceType, region string, start, end time.Time, dayPct map[string]float64) (bool, error) {
	input := &costexplorer.GetReservationCoverageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start.Format("2006-01-02")),
			End:   aws.String(end.Format("2006-01-02")),
		},
		Granularity: types.GranularityDaily,
		GroupBy: []types.GroupDefinition{
			{
				Type: types.GroupDefinitionTypeDimension,
				Key:  aws.String(string(types.DimensionInstanceType)),
			},
		},
		Filter:  dailyUsageFilter(serviceFilter, region),
		Metrics: []string{"Hour"},
	}

	anyData := false
	var token *string
	for {
		input.NextPageToken = token
		result, err := c.fetchCoveragePage(ctx, input)
		if err != nil {
			return false, fmt.Errorf("failed to get daily coverage for %s/%s/%s: %w", serviceFilter, resourceType, region, err)
		}
		if applyPeriodsToDayMap(result.CoveragesByTime, resourceType, dayPct) {
			anyData = true
		}
		if result.NextPageToken == nil || *result.NextPageToken == "" {
			break
		}
		token = result.NextPageToken
	}
	return anyData, nil
}

// applyPeriodsToDayMap writes CE coverage percentages from periods into dayPct,
// matching only the given resourceType. Reports whether any data was written.
func applyPeriodsToDayMap(periods []types.CoverageByTime, resourceType string, dayPct map[string]float64) bool {
	anyData := false
	for _, period := range periods {
		if period.TimePeriod == nil || period.TimePeriod.Start == nil {
			continue
		}
		day := aws.ToString(period.TimePeriod.Start)
		for _, group := range period.Groups {
			instType := extractInstanceTypeAttr(group.Attributes)
			if !strings.EqualFold(instType, resourceType) {
				continue
			}
			if group.Coverage == nil || group.Coverage.CoverageHours == nil ||
				group.Coverage.CoverageHours.CoverageHoursPercentage == nil {
				continue
			}
			pct := parseFloat(aws.ToString(group.Coverage.CoverageHours.CoverageHoursPercentage))
			dayPct[day] = pct
			anyData = true
		}
	}
	return anyData
}

// tupleKey is the map key for a unique (serviceFilter, region, resourceType) triple.
type tupleKey struct{ service, region, resourceType string }

// groupRecsByTuple builds an ordered unique list of tuples and an index map
// from each tuple to the recommendation indices that share it.
func groupRecsByTuple(recs []common.Recommendation) ([]tupleKey, map[tupleKey][]int) {
	order := make([]tupleKey, 0)
	seen := make(map[tupleKey]struct{})
	recsByTuple := make(map[tupleKey][]int)
	for i, r := range recs {
		sf := getServiceStringForCostExplorer(r.Service)
		if sf == "" || r.Region == "" || r.ResourceType == "" {
			continue
		}
		k := tupleKey{sf, r.Region, r.ResourceType}
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			order = append(order, k)
		}
		recsByTuple[k] = append(recsByTuple[k], i)
	}
	return order, recsByTuple
}

// AttachDailyUsageHistory enriches each recommendation in recs with a
// UsageHistory slice sourced from GetDailyUsagePcts. It batches by unique
// (serviceFilter, region, resourceType) so a 20-rec list for the same SKU
// fires only one CE call. Errors from individual tuples are logged and
// skipped so a single CE failure doesn't drop the whole collection.
//
// SavingsPlans have no per-instance-type coverage breakdown in CE, so recs
// whose Service resolves to the empty coverage-filter string are silently
// skipped.
func (c *Client) AttachDailyUsageHistory(ctx context.Context, recs []common.Recommendation) {
	order, recsByTuple := groupRecsByTuple(recs)
	for _, k := range order {
		if ctx.Err() != nil {
			return
		}
		pcts, err := c.GetDailyUsagePcts(ctx, k.service, k.resourceType, k.region)
		if err != nil {
			logging.Warnf("usage_history: failed to fetch daily coverage for %s/%s/%s: %v", k.service, k.resourceType, k.region, err)
			continue
		}
		// pcts==nil means no CE data for this tuple; leave UsageHistory nil.
		if pcts == nil {
			continue
		}
		for _, idx := range recsByTuple[k] {
			recs[idx].UsageHistory = pcts
		}
	}
}

// dailyUsageFilter builds a CE filter that scopes GetReservationCoverage to
// a single (service, region) tuple for the daily-sparkline fetch.
func dailyUsageFilter(serviceFilter, region string) *types.Expression {
	return &types.Expression{
		And: []types.Expression{
			{Dimensions: &types.DimensionValues{
				Key:    types.DimensionService,
				Values: []string{serviceFilter},
			}},
			{Dimensions: &types.DimensionValues{
				Key:    types.DimensionRegion,
				Values: []string{region},
			}},
		},
	}
}

// extractInstanceTypeAttr extracts the INSTANCE_TYPE value from CE's
// Attributes map. CE encodes the key in camelCase ("instanceType"); we
// lower-case both sides to match extractGroupAttributes in coverage.go.
func extractInstanceTypeAttr(attrs map[string]string) string {
	for k, v := range attrs {
		if strings.ToLower(strings.ReplaceAll(k, "_", "")) == "instancetype" {
			return v
		}
	}
	return ""
}
