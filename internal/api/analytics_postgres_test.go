package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockAnalyticsClient(t *testing.T) (*PostgresAnalyticsClient, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool(pgxmock.QueryMatcherOption(pgxmock.QueryMatcherRegexp))
	require.NoError(t, err)
	return &PostgresAnalyticsClient{db: mock}, mock
}

func TestIntervalToTruncUnit(t *testing.T) {
	cases := map[string]string{
		"hourly":  "hour",
		"daily":   "day",
		"":        "day",
		"weekly":  "week",
		"monthly": "month",
	}
	for in, want := range cases {
		got, err := intervalToTruncUnit(in)
		require.NoErrorf(t, err, "interval=%q", in)
		assert.Equalf(t, want, got, "interval=%q", in)
	}
	_, err := intervalToTruncUnit("yearly")
	assert.Error(t, err, "unsupported interval must error")
}

func TestDimensionToColumn(t *testing.T) {
	cases := map[string]string{
		"service":  "service",
		"":         "service",
		"provider": "provider",
		"region":   "region",
		"account":  "account_id",
	}
	for in, want := range cases {
		got, err := dimensionToColumn(in)
		require.NoErrorf(t, err, "dimension=%q", in)
		assert.Equalf(t, want, got, "dimension=%q", in)
	}
	_, err := dimensionToColumn("team")
	assert.Error(t, err, "unsupported dimension must error")
}

func TestQueryHistory_Success(t *testing.T) {
	client, mock := newMockAnalyticsClient(t)
	ctx := context.Background()

	bucket1 := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)
	bucket2 := time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC)

	rows := mock.NewRows([]string{"bucket", "service", "provider", "savings", "upfront", "purchases"}).
		AddRow(bucket1, "ec2", "aws", 100.0, 50.0, 2).
		AddRow(bucket1, "rds", "aws", 40.0, 10.0, 1).
		AddRow(bucket2, "ec2", "aws", 75.0, 0.0, 1)

	mock.ExpectQuery(`SELECT date_trunc\('day', timestamp\)`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), "acct-1").
		WillReturnRows(rows)

	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	points, summary, err := client.QueryHistory(ctx, "acct-1", start, end, "daily")
	require.NoError(t, err)
	require.Len(t, points, 2)

	// Bucket 1 aggregates: savings 140, upfront 60, purchases 3.
	assert.Equal(t, bucket1, points[0].Timestamp)
	assert.InDelta(t, 140.0, points[0].TotalSavings, 1e-9)
	assert.InDelta(t, 60.0, points[0].TotalUpfront, 1e-9)
	assert.Equal(t, 3, points[0].PurchaseCount)
	assert.InDelta(t, 100.0, points[0].ByService["ec2"], 1e-9)
	assert.InDelta(t, 40.0, points[0].ByService["rds"], 1e-9)
	assert.InDelta(t, 140.0, points[0].ByProvider["aws"], 1e-9)

	// Bucket 2 and cumulative.
	assert.Equal(t, bucket2, points[1].Timestamp)
	assert.InDelta(t, 75.0, points[1].TotalSavings, 1e-9)
	assert.InDelta(t, 140.0, points[0].CumulativeSavings, 1e-9)
	assert.InDelta(t, 215.0, points[1].CumulativeSavings, 1e-9)

	require.NotNil(t, summary)
	assert.Equal(t, 4, summary.TotalPurchases)
	assert.Equal(t, 4, summary.TotalCompleted)
	assert.InDelta(t, 60.0, summary.TotalUpfront, 1e-9)
	assert.InDelta(t, 215.0, summary.TotalMonthlySavings, 1e-9)
	assert.InDelta(t, 215.0*12, summary.TotalAnnualSavings, 1e-9)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueryHistory_BadInterval(t *testing.T) {
	client, _ := newMockAnalyticsClient(t)
	_, _, err := client.QueryHistory(context.Background(), "", time.Now(), time.Now(), "yearly")
	assert.Error(t, err)
}

func TestQueryHistory_QueryError(t *testing.T) {
	client, mock := newMockAnalyticsClient(t)
	mock.ExpectQuery(`SELECT date_trunc`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), "").
		WillReturnError(errors.New("db down"))
	_, _, err := client.QueryHistory(context.Background(), "", time.Now(), time.Now(), "daily")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueryBreakdown_Success(t *testing.T) {
	client, mock := newMockAnalyticsClient(t)
	rows := mock.NewRows([]string{"bucket", "savings", "upfront", "purchases"}).
		AddRow("ec2", 300.0, 150.0, 5).
		AddRow("rds", 100.0, 50.0, 2)

	mock.ExpectQuery(`SELECT service AS bucket`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), "").
		WillReturnRows(rows)

	out, err := client.QueryBreakdown(context.Background(), "",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		"service")
	require.NoError(t, err)
	require.Len(t, out, 2)

	ec2 := out["ec2"]
	assert.InDelta(t, 300.0, ec2.TotalSavings, 1e-9)
	assert.InDelta(t, 150.0, ec2.TotalUpfront, 1e-9)
	assert.Equal(t, 5, ec2.PurchaseCount)
	assert.InDelta(t, 75.0, ec2.Percentage, 1e-9)

	rds := out["rds"]
	assert.InDelta(t, 25.0, rds.Percentage, 1e-9)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueryBreakdown_BadDimension(t *testing.T) {
	client, _ := newMockAnalyticsClient(t)
	_, err := client.QueryBreakdown(context.Background(), "", time.Now(), time.Now(), "team")
	assert.Error(t, err)
}

func TestQueryBreakdown_ZeroTotalYieldsZeroPct(t *testing.T) {
	client, mock := newMockAnalyticsClient(t)
	rows := mock.NewRows([]string{"bucket", "savings", "upfront", "purchases"}).
		AddRow("ec2", 0.0, 0.0, 0)

	mock.ExpectQuery(`SELECT service AS bucket`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), "").
		WillReturnRows(rows)

	out, err := client.QueryBreakdown(context.Background(), "", time.Now(), time.Now(), "service")
	require.NoError(t, err)
	assert.Equal(t, 0.0, out["ec2"].Percentage, "percentage must be 0 when total savings is 0")
	assert.NoError(t, mock.ExpectationsWereMet())
}
