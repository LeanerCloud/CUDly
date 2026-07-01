package apihttp

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

	// External-id-only filter: predicate is (account_id = ANY($3)), bound to the
	// single external id. No cloud_account_id half since no UUIDs supplied.
	mock.ExpectQuery(`(?s)SELECT date_trunc\('day', timestamp\).*AND \(account_id = ANY\(\$3\)\)`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), []string{"acct-1"}).
		WillReturnRows(rows)

	start := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	points, summary, err := client.QueryHistory(ctx, nil, map[string][]string{"": {"acct-1"}}, "", start, end, "daily")
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
	_, _, err := client.QueryHistory(context.Background(), nil, nil, "", time.Now(), time.Now(), "yearly")
	assert.Error(t, err)
}

func TestQueryHistory_QueryError(t *testing.T) {
	client, mock := newMockAnalyticsClient(t)
	// No account filter: predicate degrades to TRUE, only start/end bound.
	mock.ExpectQuery(`SELECT date_trunc`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(errors.New("db down"))
	_, _, err := client.QueryHistory(context.Background(), nil, nil, "", time.Now(), time.Now(), "daily")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueryBreakdown_Success(t *testing.T) {
	client, mock := newMockAnalyticsClient(t)
	rows := mock.NewRows([]string{"bucket", "savings", "upfront", "purchases"}).
		AddRow("ec2", 300.0, 150.0, 5).
		AddRow("rds", 100.0, 50.0, 2)

	mock.ExpectQuery(`SELECT service AS bucket`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(rows)

	out, err := client.QueryBreakdown(context.Background(), nil, nil,
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
	_, err := client.QueryBreakdown(context.Background(), nil, nil, time.Now(), time.Now(), "team")
	assert.Error(t, err)
}

func TestQueryBreakdown_ZeroTotalYieldsZeroPct(t *testing.T) {
	client, mock := newMockAnalyticsClient(t)
	rows := mock.NewRows([]string{"bucket", "savings", "upfront", "purchases"}).
		AddRow("ec2", 0.0, 0.0, 0)

	mock.ExpectQuery(`SELECT service AS bucket`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(rows)

	out, err := client.QueryBreakdown(context.Background(), nil, nil, time.Now(), time.Now(), "service")
	require.NoError(t, err)
	assert.Equal(t, 0.0, out["ec2"].Percentage, "percentage must be 0 when total savings is 0")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestQueryHistory_DualColumnFilter verifies the dual-column account predicate
// (issue #701/#498/#866): when a UUID and its resolved external id (grouped
// under its provider) are both supplied, the WHERE clause ORs cloud_account_id =
// ANY($3) with (provider = $4 AND account_id = ANY($5)), so a row that carries
// only one of the two account representations is still aggregated while the
// external-id half stays provider-scoped. pgxmock validates the SQL shape and
// the array arg binding (cloud_account_id is compared directly, no ::text cast).
func TestQueryHistory_DualColumnFilter(t *testing.T) {
	client, mock := newMockAnalyticsClient(t)
	ctx := context.Background()
	uuid := "aabbccdd-1234-5678-abcd-aabbccddee00"
	external := "123456789012"

	bucket := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	rows := mock.NewRows([]string{"bucket", "service", "provider", "savings", "upfront", "purchases"}).
		AddRow(bucket, "ec2", "aws", 50.0, 20.0, 1)

	mock.ExpectQuery(`(?s)SELECT date_trunc\('day', timestamp\).*AND \(cloud_account_id = ANY\(\$3\) OR \(provider = \$4 AND account_id = ANY\(\$5\)\)\)`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), []string{uuid}, "aws", []string{external}).
		WillReturnRows(rows)

	start := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	points, summary, err := client.QueryHistory(ctx, []string{uuid}, map[string][]string{"aws": {external}}, "", start, end, "daily")
	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.InDelta(t, 50.0, points[0].TotalSavings, 1e-9)
	require.NotNil(t, summary)
	assert.Equal(t, 1, summary.TotalPurchases)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestQueryBreakdown_DualColumnFilter mirrors TestQueryHistory_DualColumnFilter
// for the breakdown aggregate (issue #701/#498/#866).
func TestQueryBreakdown_DualColumnFilter(t *testing.T) {
	client, mock := newMockAnalyticsClient(t)
	ctx := context.Background()
	uuid := "aabbccdd-1234-5678-abcd-aabbccddee00"
	external := "123456789012"

	rows := mock.NewRows([]string{"bucket", "savings", "upfront", "purchases"}).
		AddRow("rds", 200.0, 80.0, 3)

	mock.ExpectQuery(`(?s)SELECT service AS bucket.*AND \(cloud_account_id = ANY\(\$3\) OR \(provider = \$4 AND account_id = ANY\(\$5\)\)\)`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), []string{uuid}, "aws", []string{external}).
		WillReturnRows(rows)

	out, err := client.QueryBreakdown(ctx, []string{uuid}, map[string][]string{"aws": {external}},
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		"service")
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.InDelta(t, 200.0, out["rds"].TotalSavings, 1e-9)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestQueryHistory_CrossProviderScoped is the analytics regression for issue
// #956 CR finding #1: when the same external id "123" exists under two providers
// the WHERE clause emits a separate provider-gated branch per provider (sorted),
// so an external id only matches rows of its own provider and aws/123 never
// pulls azure/123 rows. The args are bound in sorted-provider order.
func TestQueryHistory_CrossProviderScoped(t *testing.T) {
	client, mock := newMockAnalyticsClient(t)
	ctx := context.Background()

	bucket := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	rows := mock.NewRows([]string{"bucket", "service", "provider", "savings", "upfront", "purchases"}).
		AddRow(bucket, "ec2", "aws", 10.0, 5.0, 1)

	mock.ExpectQuery(`(?s)SELECT date_trunc\('day', timestamp\).*AND \(\(provider = \$3 AND account_id = ANY\(\$4\)\) OR \(provider = \$5 AND account_id = ANY\(\$6\)\)\)`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), "aws", []string{"123"}, "azure", []string{"123"}).
		WillReturnRows(rows)

	start := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	_, _, err := client.QueryHistory(ctx, nil, map[string][]string{"aws": {"123"}, "azure": {"123"}}, "", start, end, "daily")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestQueryHistory_ProviderFilter is the regression for the Savings History
// chart provider-filter bug (issue #498, QA 2.3): selecting a provider in the
// global filter must scope the savings-over-time series to that provider. The
// handler forwards the chip value, so QueryHistory must append
// `AND provider = $N` bound to the provider so Azure/GCP rows are excluded.
// Pre-fix QueryHistory ignored the provider entirely and this test fails
// because no provider bind is emitted.
func TestQueryHistory_ProviderFilter(t *testing.T) {
	client, mock := newMockAnalyticsClient(t)
	ctx := context.Background()

	bucket := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	// Only the aws row comes back from the DB; the provider predicate is what
	// keeps azure/gcp rows out, so the mock returns just the aws bucket.
	rows := mock.NewRows([]string{"bucket", "service", "provider", "savings", "upfront", "purchases"}).
		AddRow(bucket, "ec2", "aws", 10.0, 5.0, 1)

	// No account filter (all-accessible), so the account clause degrades to TRUE
	// and start/end are $1/$2; the provider bind is therefore $3.
	mock.ExpectQuery(`(?s)SELECT date_trunc\('day', timestamp\).*AND TRUE AND provider = \$3`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), "aws").
		WillReturnRows(rows)

	start := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	points, _, err := client.QueryHistory(ctx, nil, nil, "aws", start, end, "daily")
	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.InDelta(t, 10.0, points[0].TotalSavings, 1e-9)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestQueryHistory_ProviderFilterWithAccount verifies the provider bind is
// positioned after the dual-column account binds (provider becomes $6 when a
// UUID + provider-grouped external id precede it), so account + provider
// filtering compose correctly (issue #498).
func TestQueryHistory_ProviderFilterWithAccount(t *testing.T) {
	client, mock := newMockAnalyticsClient(t)
	ctx := context.Background()
	uuid := "aabbccdd-1234-5678-abcd-aabbccddee00"
	external := "123456789012"

	bucket := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	rows := mock.NewRows([]string{"bucket", "service", "provider", "savings", "upfront", "purchases"}).
		AddRow(bucket, "ec2", "aws", 50.0, 20.0, 1)

	mock.ExpectQuery(`(?s)SELECT date_trunc\('day', timestamp\).*AND \(cloud_account_id = ANY\(\$3\) OR \(provider = \$4 AND account_id = ANY\(\$5\)\)\) AND provider = \$6`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), []string{uuid}, "aws", []string{external}, "aws").
		WillReturnRows(rows)

	start := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	_, _, err := client.QueryHistory(ctx, []string{uuid}, map[string][]string{"aws": {external}}, "aws", start, end, "daily")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestQueryHistory_ExternalIDFilter verifies that when only external IDs are
// supplied, the WHERE clause emits account_id = ANY($3) bound to the flat slice,
// so a row that carries only account_id (cloud_account_id NULL) is still matched
// (issue #701/#498/#866).
func TestQueryHistory_ExternalIDFilter(t *testing.T) {
	client, mock := newMockAnalyticsClient(t)
	ctx := context.Background()

	bucket := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	rows := mock.NewRows([]string{"bucket", "service", "provider", "savings", "upfront", "purchases"}).
		AddRow(bucket, "ec2", "aws", 10.0, 5.0, 1)

	mock.ExpectQuery(`(?s)SELECT date_trunc\('day', timestamp\).*AND \(account_id = ANY\(\$3\)\)`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), []string{"123456789012"}).
		WillReturnRows(rows)

	start := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	_, _, err := client.QueryHistory(ctx, nil, map[string][]string{"": {"123456789012"}}, "", start, end, "daily")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
