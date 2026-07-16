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

// generate30Days builds a single-page mock returning 30 daily entries,
// each billing totalPerDay USD total (i.e. totalPerDay/24 USD/hr per entry).
func generate30DayPage(startDate time.Time, totalPerDay float64) []*costexplorer.GetCostAndUsageOutput {
	results := make([]types.ResultByTime, 30)
	for i := range results {
		day := startDate.AddDate(0, 0, i)
		results[i] = dailyResult(day.Format("2006-01-02"), totalPerDay)
	}
	return []*costexplorer.GetCostAndUsageOutput{
		{ResultsByTime: results},
	}
}

// TestGetOnDemandSeries_HappyPath verifies that 30 daily entries at
// $240/day each return 30 elements of $10/hr ($240/24h).
func TestGetOnDemandSeries_HappyPath(t *testing.T) {
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -30)
	mock := &mockOnDemandCE{pages: generate30DayPage(start, 240.0)}
	client := newOnDemandClient(mock)

	series, err := client.GetOnDemandSeries(context.Background(), "us-east-1", 30)

	require.NoError(t, err)
	assert.Len(t, series, 30, "series must have one element per returned day")
	for i, v := range series {
		assert.InDelta(t, 10.0, v, 1e-9, "day %d: expected $10/hr ($240/24h)", i)
	}
}

// TestGetOnDemandSeries_OldestFirst verifies that days are returned in
// chronological (oldest-to-newest) order regardless of map iteration order.
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
	assert.InDelta(t, 1.0, series[0], 1e-9, "oldest day first")
	assert.InDelta(t, 2.0, series[1], 1e-9, "middle day second")
	assert.InDelta(t, 3.0, series[2], 1e-9, "newest day last")
}

// TestGetOnDemandSeries_Paginated verifies that two pages are fetched and
// their results merged into a single sorted series.
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
	assert.InDelta(t, 1.0, series[0], 1e-9)
	assert.InDelta(t, 2.0, series[1], 1e-9)
	assert.InDelta(t, 3.0, series[2], 1e-9)
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
	assert.Equal(t, []string{onDemandMetric}, input.Metrics,
		"metric must be UNBLENDED_COST")

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

// TestGetOnDemandSeries_MissingMetricKey verifies that a day with a missing
// metric key is treated as $0 rather than causing an error.
func TestGetOnDemandSeries_MissingMetricKey(t *testing.T) {
	pages := []*costexplorer.GetCostAndUsageOutput{{
		ResultsByTime: []types.ResultByTime{
			dailyResult("2026-01-01", 24.0),
			dailyResultNoMetric("2026-01-02"), // missing metric -> treated as 0
			dailyResult("2026-01-03", 48.0),
		},
	}}
	mock := &mockOnDemandCE{pages: pages}
	client := newOnDemandClient(mock)

	series, err := client.GetOnDemandSeries(context.Background(), "us-east-1", 7)

	require.NoError(t, err)
	require.Len(t, series, 3)
	assert.InDelta(t, 1.0, series[0], 1e-9)
	assert.InDelta(t, 0.0, series[1], 1e-9, "missing metric key is treated as $0/hr")
	assert.InDelta(t, 2.0, series[2], 1e-9)
}
