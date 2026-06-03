package recommendations

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// buildDailyOutput constructs a GetReservationCoverageOutput that has one
// CoverageByTime entry per day in the last usageHistoryLookbackDays window,
// each reporting 100% coverage for instType. Used to exercise the happy path
// without hitting AWS.
func buildDailyOutput(instType string) *costexplorer.GetReservationCoverageOutput {
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -usageHistoryLookbackDays)
	periods := make([]types.CoverageByTime, 0, usageHistoryLookbackDays)
	for i := 0; i < usageHistoryLookbackDays; i++ {
		day := start.AddDate(0, 0, i)
		periods = append(periods, types.CoverageByTime{
			TimePeriod: &types.DateInterval{
				Start: aws.String(day.Format("2006-01-02")),
				End:   aws.String(day.AddDate(0, 0, 1).Format("2006-01-02")),
			},
			Groups: []types.ReservationCoverageGroup{
				{
					Attributes: map[string]string{
						"instanceType": instType,
					},
					Coverage: &types.Coverage{
						CoverageHours: &types.CoverageHours{
							CoverageHoursPercentage: aws.String("80.0"),
						},
					},
				},
			},
		})
	}
	return &costexplorer.GetReservationCoverageOutput{CoveragesByTime: periods}
}

// TestGetDailyUsagePcts_ReturnsNDailyPoints asserts that GetDailyUsagePcts
// returns exactly usageHistoryLookbackDays points ordered oldest-to-newest
// when CE reports coverage for every day.
func TestGetDailyUsagePcts_ReturnsNDailyPoints(t *testing.T) {
	mock := &mockCoverageCE{
		coverageOutput: buildDailyOutput("m5.large"),
	}

	client := NewClientWithAPI(mock, "us-east-1")
	pcts, err := client.GetDailyUsagePcts(context.Background(), "Amazon Elastic Compute Cloud - Compute", "m5.large", "us-east-1")

	require.NoError(t, err)
	require.NotNil(t, pcts, "expected non-nil slice when CE has data")
	assert.Len(t, pcts, usageHistoryLookbackDays, "should return exactly %d points", usageHistoryLookbackDays)
	for i, p := range pcts {
		assert.InDelta(t, 80.0, p, 0.001, "day %d: expected 80.0%% coverage", i)
	}
}

// TestGetDailyUsagePcts_ReturnsNilOnNoData asserts that GetDailyUsagePcts
// returns (nil, nil) when CE has no data for the tuple so the frontend
// renders "—" (not a flat-zero sparkline).
func TestGetDailyUsagePcts_ReturnsNilOnNoData(t *testing.T) {
	mock := &mockCoverageCE{
		coverageOutput: &costexplorer.GetReservationCoverageOutput{
			CoveragesByTime: []types.CoverageByTime{},
		},
	}

	client := NewClientWithAPI(mock, "us-east-1")
	pcts, err := client.GetDailyUsagePcts(context.Background(), "Amazon Elastic Compute Cloud - Compute", "m5.large", "us-east-1")

	require.NoError(t, err)
	assert.Nil(t, pcts, "nil means no data; frontend renders a dash, not a flat-zero sparkline")
}

// TestGetDailyUsagePcts_EmptyInputsReturnNil asserts that empty serviceFilter,
// resourceType, or region short-circuit without an API call.
func TestGetDailyUsagePcts_EmptyInputsReturnNil(t *testing.T) {
	mock := &mockCoverageCE{}

	client := NewClientWithAPI(mock, "us-east-1")

	pcts, err := client.GetDailyUsagePcts(context.Background(), "", "m5.large", "us-east-1")
	require.NoError(t, err)
	assert.Nil(t, pcts)
	assert.Equal(t, 0, mock.coverageCalls, "no CE call when serviceFilter is empty")
}

// TestAttachDailyUsageHistory_PopulatesUsageHistory is the end-to-end
// assertion required by issue #239: given a slice of recommendations with a
// single distinct (service, region, resourceType) tuple, AttachDailyUsageHistory
// should populate every matching rec's UsageHistory field with the daily
// coverage percentages returned by CE.
func TestAttachDailyUsageHistory_PopulatesUsageHistory(t *testing.T) {
	mock := &mockCoverageCE{
		coverageOutput: buildDailyOutput("m5.xlarge"),
	}

	client := NewClientWithAPI(mock, "us-east-1")
	recs := []common.Recommendation{
		{
			Service:      common.ServiceEC2,
			Region:       "us-east-1",
			ResourceType: "m5.xlarge",
		},
		{
			Service:      common.ServiceEC2,
			Region:       "us-east-1",
			ResourceType: "m5.xlarge",
		},
	}

	client.AttachDailyUsageHistory(context.Background(), recs)

	for i, r := range recs {
		require.NotNil(t, r.UsageHistory, "rec[%d] UsageHistory must be non-nil after attach", i)
		assert.Len(t, r.UsageHistory, usageHistoryLookbackDays,
			"rec[%d] expected %d daily points", i, usageHistoryLookbackDays)
	}
	// Two recs sharing the same tuple must result in only one CE call (batching).
	assert.Equal(t, 1, mock.coverageCalls, "identical tuples should share a single CE call")
}

// TestAttachDailyUsageHistory_SkipsEmptyRegion asserts that recs with a
// missing Region are silently skipped so AttachDailyUsageHistory never fires
// a CE call with an empty region filter value.
func TestAttachDailyUsageHistory_SkipsEmptyRegion(t *testing.T) {
	mock := &mockCoverageCE{}

	client := NewClientWithAPI(mock, "us-east-1")
	recs := []common.Recommendation{
		{
			Service:      common.ServiceEC2,
			Region:       "",
			ResourceType: "m5.large",
		},
	}

	client.AttachDailyUsageHistory(context.Background(), recs)

	assert.Nil(t, recs[0].UsageHistory, "rec with empty region must not have UsageHistory populated")
	assert.Equal(t, 0, mock.coverageCalls, "no CE call when region is empty")
}
