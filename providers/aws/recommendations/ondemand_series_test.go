package recommendations

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockOnDemandCE is a hermetic mock for GetCostAndUsage.  Only GetCostAndUsage
// is behaviourally wired; all other CostExplorerAPI methods are stubs that
// return empty non-nil outputs (the base mockCostExplorerAPI already provides
// those stubs via embedding).
type mockOnDemandCE struct {
	mockCostExplorerAPI // provides all stub methods except GetCostAndUsage
	pages               []*costexplorer.GetCostAndUsageOutput
	tokens              []string // tokens[i] triggers pages[i+1]
	gotInputs           []*costexplorer.GetCostAndUsageInput
	apiErr              error
}

func (m *mockOnDemandCE) GetCostAndUsage(
	_ context.Context,
	params *costexplorer.GetCostAndUsageInput,
	_ ...func(*costexplorer.Options),
) (*costexplorer.GetCostAndUsageOutput, error) {
	m.gotInputs = append(m.gotInputs, params)
	if m.apiErr != nil {
		return nil, m.apiErr
	}
	// Determine which page to return based on the incoming NextPageToken.
	idx := 0
	incoming := aws.ToString(params.NextPageToken)
	for i, tok := range m.tokens {
		if tok == incoming {
			idx = i + 1
			break
		}
	}
	if idx >= len(m.pages) {
		return nil, fmt.Errorf("unexpected page token %q", incoming)
	}
	return m.pages[idx], nil
}

// dailyResult builds one ResultByTime entry for testing.
// dateStr must be in "2006-01-02" format; totalUSD is the day's UNBLENDED_COST.
func dailyResult(dateStr string, totalUSD float64) types.ResultByTime {
	return types.ResultByTime{
		TimePeriod: &types.DateInterval{
			Start: aws.String(dateStr),
			End:   aws.String(dateStr), // end is exclusive, not relevant for mock
		},
		Total: map[string]types.MetricValue{
			onDemandMetric: {Amount: aws.String(fmt.Sprintf("%.10f", totalUSD))},
		},
	}
}

// dailyResultNoMetric builds a ResultByTime entry with a missing metric key,
// exercising the "metric absent" branch that falls back to 0.
func dailyResultNoMetric(dateStr string) types.ResultByTime {
	return types.ResultByTime{
		TimePeriod: &types.DateInterval{Start: aws.String(dateStr)},
		Total:      map[string]types.MetricValue{},
	}
}

// newOnDemandClient creates a recommendations.Client wired to the given mock.
func newOnDemandClient(m *mockOnDemandCE) *Client {
	return NewClientWithAPI(m, "us-east-1")
}

// utcDate parses a "2006-01-02" string into midnight UTC, failing the test
// on malformed input. Mirrors the production ceDateLayout parse so date
// assertions compare like with like.
func utcDate(t *testing.T, dateStr string) time.Time {
	t.Helper()
	d, err := time.Parse(ceDateLayout, dateStr)
	require.NoError(t, err)
	return d
}

// requireStrictlyIncreasingDays asserts the series dates are unique ascending
// UTC calendar days (the ladder baseline's chronology contract).
func requireStrictlyIncreasingDays(t *testing.T, series []DailyCost) {
	t.Helper()
	for i := 1; i < len(series); i++ {
		require.True(t, series[i].Date.After(series[i-1].Date),
			"series dates must be strictly increasing: index %d (%s) not after index %d (%s)",
			i, series[i].Date.Format(ceDateLayout), i-1, series[i-1].Date.Format(ceDateLayout))
	}
}

// generate30DayPage builds a single-page mock returning 30 daily entries,
// each billing totalPerDay USD total (i.e. totalPerDay/24 USD/hr per entry).
func generate30DayPage(startDate time.Time, totalPerDay float64) []*costexplorer.GetCostAndUsageOutput {
	results := make([]types.ResultByTime, 30)
	for i := range results {
		day := startDate.AddDate(0, 0, i)
		results[i] = dailyResult(day.Format(ceDateLayout), totalPerDay)
	}
	return []*costexplorer.GetCostAndUsageOutput{
		{ResultsByTime: results},
	}
}

// TestGetOnDemandSeries_HappyPath verifies that 30 daily entries at
// $240/day each return 30 dated points of $10/hr ($240/24h) with strictly
// increasing unique UTC days and a fresh (most recent CE day) tail.
func TestGetOnDemandSeries_HappyPath(t *testing.T) {
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -30)
	mock := &mockOnDemandCE{pages: generate30DayPage(start, 240.0)}
	client := newOnDemandClient(mock)

	series, err := client.GetOnDemandSeries(context.Background(), "us-east-1", 30)

	require.NoError(t, err)
	require.Len(t, series, 30, "series must have one element per returned day")
	for i, p := range series {
		assert.InDelta(t, 10.0, p.USDPerHour, 1e-9, "day %d: expected $10/hr ($240/24h)", i)
		assert.Equal(t, start.AddDate(0, 0, i), p.Date,
			"day %d: Date must be the UTC day from the CE period start", i)
	}
	requireStrictlyIncreasingDays(t, series)
	// Freshness contract: the last point is the most recent CE day (start+29
	// = yesterday), within the baseline's maxSeriesAgeDays.
	assert.Equal(t, start.AddDate(0, 0, 29), series[len(series)-1].Date,
		"last point must be the most recent CE day")
}

// TestGetOnDemandSeries_OldestFirst verifies that days are returned in
// chronological (oldest-to-newest) order by Date regardless of the order CE
// returned them in.
func TestGetOnDemandSeries_OldestFirst(t *testing.T) {
	// Provide 3 days with distinct costs so order is detectable.
	pages := []*costexplorer.GetCostAndUsageOutput{{
		ResultsByTime: []types.ResultByTime{
			dailyResult("2026-01-03", 72.0), // $3/hr
			dailyResult("2026-01-01", 24.0), // $1/hr
			dailyResult("2026-01-02", 48.0), // $2/hr
		},
	}}
	mock := &mockOnDemandCE{pages: pages}
	client := newOnDemandClient(mock)

	series, err := client.GetOnDemandSeries(context.Background(), "us-east-1", 7)

	require.NoError(t, err)
	require.Len(t, series, 3)
	assert.Equal(t, utcDate(t, "2026-01-01"), series[0].Date, "oldest day first")
	assert.InDelta(t, 1.0, series[0].USDPerHour, 1e-9)
	assert.Equal(t, utcDate(t, "2026-01-02"), series[1].Date, "middle day second")
	assert.InDelta(t, 2.0, series[1].USDPerHour, 1e-9)
	assert.Equal(t, utcDate(t, "2026-01-03"), series[2].Date, "newest day last")
	assert.InDelta(t, 3.0, series[2].USDPerHour, 1e-9)
	requireStrictlyIncreasingDays(t, series)
}

// TestGetOnDemandSeries_Paginated verifies that two pages are fetched and
// their results merged into a single date-sorted series.
func TestGetOnDemandSeries_Paginated(t *testing.T) {
	pages := []*costexplorer.GetCostAndUsageOutput{
		{
			ResultsByTime: []types.ResultByTime{
				dailyResult("2026-01-01", 24.0),
				dailyResult("2026-01-02", 48.0),
			},
			NextPageToken: aws.String("tok1"),
		},
		{
			ResultsByTime: []types.ResultByTime{
				dailyResult("2026-01-03", 72.0),
			},
		},
	}
	mock := &mockOnDemandCE{pages: pages, tokens: []string{"tok1"}}
	client := newOnDemandClient(mock)

	series, err := client.GetOnDemandSeries(context.Background(), "us-east-1", 7)

	require.NoError(t, err)
	require.Len(t, series, 3, "both pages must be merged")
	assert.Equal(t, utcDate(t, "2026-01-01"), series[0].Date)
	assert.InDelta(t, 1.0, series[0].USDPerHour, 1e-9)
	assert.Equal(t, utcDate(t, "2026-01-02"), series[1].Date)
	assert.InDelta(t, 2.0, series[1].USDPerHour, 1e-9)
	assert.Equal(t, utcDate(t, "2026-01-03"), series[2].Date)
	assert.InDelta(t, 3.0, series[2].USDPerHour, 1e-9)
	requireStrictlyIncreasingDays(t, series)
	// Verify exactly 2 CE calls were made (one per page).
	assert.Len(t, mock.gotInputs, 2, "should have fetched 2 pages")
	// Second call must carry the token from the first response.
	assert.Equal(t, "tok1", aws.ToString(mock.gotInputs[1].NextPageToken))
}

// TestGetOnDemandSeries_EmptyResultErrors verifies a hard error when CE
// returns zero daily entries (no on-demand spend or CE not yet available).
func TestGetOnDemandSeries_EmptyResultErrors(t *testing.T) {
	mock := &mockOnDemandCE{pages: []*costexplorer.GetCostAndUsageOutput{{}}}
	client := newOnDemandClient(mock)

	_, err := client.GetOnDemandSeries(context.Background(), "us-east-1", 30)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no on-demand data", "error should describe the empty-data condition")
}

// TestGetOnDemandSeries_APIErrorPropagated verifies that a CE API error is
// returned to the caller without swallowing.
func TestGetOnDemandSeries_APIErrorPropagated(t *testing.T) {
	sentinel := errors.New("CE rate limit")
	mock := &mockOnDemandCE{
		pages:  []*costexplorer.GetCostAndUsageOutput{{}},
		apiErr: sentinel,
	}
	client := newOnDemandClient(mock)

	_, err := client.GetOnDemandSeries(context.Background(), "us-east-1", 30)

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

// TestGetOnDemandSeries_ParseErrorFails verifies that a non-numeric Amount
// from CE triggers a fail-loud error (feedback_strict_int_parse).
func TestGetOnDemandSeries_ParseErrorFails(t *testing.T) {
	pages := []*costexplorer.GetCostAndUsageOutput{{
		ResultsByTime: []types.ResultByTime{{
			TimePeriod: &types.DateInterval{Start: aws.String("2026-01-01")},
			Total:      map[string]types.MetricValue{onDemandMetric: {Amount: aws.String("not-a-number")}},
		}},
	}}
	mock := &mockOnDemandCE{pages: pages}
	client := newOnDemandClient(mock)

	_, err := client.GetOnDemandSeries(context.Background(), "us-east-1", 7)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse CE amount")
}

// TestGetOnDemandSeries_BadDateFails verifies that a malformed CE period-start
// date fails loud instead of being skipped or misdated
// (feedback_no_silent_fallbacks).
func TestGetOnDemandSeries_BadDateFails(t *testing.T) {
	pages := []*costexplorer.GetCostAndUsageOutput{{
		ResultsByTime: []types.ResultByTime{
			dailyResult("not-a-date", 24.0),
		},
	}}
	mock := &mockOnDemandCE{pages: pages}
	client := newOnDemandClient(mock)

	_, err := client.GetOnDemandSeries(context.Background(), "us-east-1", 7)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse CE period start date")
}

// TestGetOnDemandSeries_CorrectFilterParams verifies the CE query uses the
// correct granularity, metric, and three-clause AND filter
// (feedback_verify_api_filter_contracts).
func TestGetOnDemandSeries_CorrectFilterParams(t *testing.T) {
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -7)
	mock := &mockOnDemandCE{pages: generate30DayPage(start, 24.0)}
	client := newOnDemandClient(mock)

	_, err := client.GetOnDemandSeries(context.Background(), "eu-west-1", 7)
	require.NoError(t, err)
	require.NotEmpty(t, mock.gotInputs, "at least one CE call must have been made")

	input := mock.gotInputs[0]

	assert.Equal(t, types.GranularityDaily, input.Granularity,
		"granularity must be DAILY for the daily series")
	// The literal CamelCase value is asserted on purpose: GetCostAndUsage's
	// Metrics vocabulary is CamelCase ("UnblendedCost"), NOT the
	// types.MetricUnblendedCost enum's "UNBLENDED_COST" (that enum belongs to
	// other CE APIs and triggers a ValidationException here). Locking the
	// literal prevents a "cleanup" back to the enum from passing tests.
	assert.Equal(t, []string{"UnblendedCost"}, input.Metrics,
		"metric must be the CamelCase UnblendedCost accepted by GetCostAndUsage")

	// TimePeriod contract: [today-lookbackDays, today) with an exclusive end
	// at midnight UTC today, so today's partial data is never included.
	wantEnd := time.Now().UTC().Truncate(24 * time.Hour)
	wantStart := wantEnd.AddDate(0, 0, -7)
	require.NotNil(t, input.TimePeriod)
	assert.Equal(t, wantStart.Format(ceDateLayout), aws.ToString(input.TimePeriod.Start),
		"start must be today-lookbackDays (UTC)")
	assert.Equal(t, wantEnd.Format(ceDateLayout), aws.ToString(input.TimePeriod.End),
		"end must be midnight UTC today (exclusive: today's partial day excluded)")

	require.NotNil(t, input.Filter, "filter must be present")
	require.Len(t, input.Filter.And, 3, "filter must be a three-clause AND")

	// Extract dimension keys from the AND clauses.
	dimKeys := make([]types.Dimension, 0, 3)
	dimVals := make(map[types.Dimension][]string, 3)
	for _, clause := range input.Filter.And {
		require.NotNil(t, clause.Dimensions)
		dimKeys = append(dimKeys, clause.Dimensions.Key)
		dimVals[clause.Dimensions.Key] = clause.Dimensions.Values
	}
	assert.Contains(t, dimKeys, types.DimensionService, "SERVICE dimension required")
	assert.Contains(t, dimKeys, types.DimensionPurchaseType, "PURCHASE_TYPE dimension required")
	assert.Contains(t, dimKeys, types.DimensionRegion, "REGION dimension required")

	assert.Equal(t, []string{ec2ComputeService}, dimVals[types.DimensionService])
	assert.Equal(t, []string{purchaseTypeOnDemand}, dimVals[types.DimensionPurchaseType])
	assert.Equal(t, []string{"eu-west-1"}, dimVals[types.DimensionRegion])
}

// TestGetOnDemandSeries_ContextCancelled verifies that a cancelled context is
// propagated before the first CE call (ctx-cancel-is-terminal rule).
func TestGetOnDemandSeries_ContextCancelled(t *testing.T) {
	mock := &mockOnDemandCE{pages: generate30DayPage(time.Now(), 24.0)}
	client := newOnDemandClient(mock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately before the call

	_, err := client.GetOnDemandSeries(ctx, "us-east-1", 30)

	require.Error(t, err)
}

// TestGetOnDemandSeries_MissingMetricKeyFails verifies that a result row
// missing the requested metric key fails loud instead of fabricating a $0
// day. CE echoes every requested metric on every row (genuine $0 days arrive
// as Amount:"0"), so a missing key means the request vocabulary is wrong;
// silently writing 0 would corrupt the baseline
// (feedback_no_silent_fallbacks).
func TestGetOnDemandSeries_MissingMetricKeyFails(t *testing.T) {
	pages := []*costexplorer.GetCostAndUsageOutput{{
		ResultsByTime: []types.ResultByTime{
			dailyResult("2026-01-01", 24.0),
			dailyResultNoMetric("2026-01-02"), // missing metric -> hard error
			dailyResult("2026-01-03", 48.0),
		},
	}}
	mock := &mockOnDemandCE{pages: pages}
	client := newOnDemandClient(mock)

	_, err := client.GetOnDemandSeries(context.Background(), "us-east-1", 7)

	require.Error(t, err, "a missing metric key must fail loud, not fabricate $0")
	assert.Contains(t, err.Error(), "missing the \"UnblendedCost\" metric")
	assert.Contains(t, err.Error(), "2026-01-02", "error must name the offending day")
}

// TestGetOnDemandSeries_AllZeroSeriesFails verifies that a complete series of
// $0 rows is rejected. When a filter or metric name is wrong, CE returns
// every day as a $0 row (not an empty result), producing a fresh,
// chronological all-zero series that would pass every downstream validation
// and make the engine size purchases from fabricated data. An account with
// genuinely zero on-demand spend all window has nothing to ladder, so
// erroring is correct there too.
func TestGetOnDemandSeries_AllZeroSeriesFails(t *testing.T) {
	pages := []*costexplorer.GetCostAndUsageOutput{{
		ResultsByTime: []types.ResultByTime{
			dailyResult("2026-01-01", 0),
			dailyResult("2026-01-02", 0),
			dailyResult("2026-01-03", 0),
		},
	}}
	mock := &mockOnDemandCE{pages: pages}
	client := newOnDemandClient(mock)

	_, err := client.GetOnDemandSeries(context.Background(), "us-east-1", 7)

	require.Error(t, err, "an all-zero series must be rejected")
	assert.Contains(t, err.Error(), "all-zero")
}

// TestGetOnDemandSeries_MixedZeroDaysOK verifies that genuine $0 days
// (Amount:"0") interleaved with non-zero days are accepted and keep their
// calendar dates: only the ALL-zero case is rejected.
func TestGetOnDemandSeries_MixedZeroDaysOK(t *testing.T) {
	pages := []*costexplorer.GetCostAndUsageOutput{{
		ResultsByTime: []types.ResultByTime{
			dailyResult("2026-01-01", 24.0),
			dailyResult("2026-01-02", 0), // genuine $0 day: Amount:"0"
			dailyResult("2026-01-03", 48.0),
		},
	}}
	mock := &mockOnDemandCE{pages: pages}
	client := newOnDemandClient(mock)

	series, err := client.GetOnDemandSeries(context.Background(), "us-east-1", 7)

	require.NoError(t, err, "genuine zero days mixed with spend must be accepted")
	require.Len(t, series, 3)
	assert.Equal(t, utcDate(t, "2026-01-01"), series[0].Date)
	assert.InDelta(t, 1.0, series[0].USDPerHour, 1e-9)
	assert.Equal(t, utcDate(t, "2026-01-02"), series[1].Date,
		"the zero-spend day must keep its calendar date")
	assert.InDelta(t, 0.0, series[1].USDPerHour, 1e-9)
	assert.Equal(t, utcDate(t, "2026-01-03"), series[2].Date)
	assert.InDelta(t, 2.0, series[2].USDPerHour, 1e-9)
	requireStrictlyIncreasingDays(t, series)
}
