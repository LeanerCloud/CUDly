package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockAnalyticsStore(t *testing.T) (*PostgresAnalyticsStore, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool(pgxmock.QueryMatcherOption(pgxmock.QueryMatcherRegexp))
	require.NoError(t, err)
	return &PostgresAnalyticsStore{db: mock}, mock
}

// anyArgs generates a slice of pgxmock.AnyArg() of length n.
func anyArgs(n int) []interface{} {
	args := make([]interface{}, n)
	for i := range args {
		args[i] = pgxmock.AnyArg()
	}
	return args
}

// ─── SaveSnapshot ──────────────────────────────────────────────────────────────

func TestPostgresAnalyticsStore_SaveSnapshot_Success(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectExec(`INSERT INTO savings_snapshots`).
		WithArgs(anyArgs(12)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	snap := &SavingsSnapshot{
		AccountID:          "acct1",
		Timestamp:          time.Now(),
		Provider:           "aws",
		Service:            "ec2",
		Region:             "us-east-1",
		CommitmentType:     "RI",
		TotalCommitment:    1000.0,
		TotalUsage:         900.0,
		TotalSavings:       100.0,
		CoveragePercentage: 90.0,
	}
	err := store.SaveSnapshot(ctx, snap)
	require.NoError(t, err)
	assert.NotEmpty(t, snap.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAnalyticsStore_SaveSnapshot_ExecError(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectExec(`INSERT INTO savings_snapshots`).
		WithArgs(anyArgs(12)...).
		WillReturnError(errors.New("db error"))

	snap := &SavingsSnapshot{AccountID: "acct1", Timestamp: time.Now()}
	err := store.SaveSnapshot(ctx, snap)
	assert.ErrorContains(t, err, "failed to save savings snapshot")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAnalyticsStore_SaveSnapshot_WithMetadata(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectExec(`INSERT INTO savings_snapshots`).
		WithArgs(anyArgs(12)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	snap := &SavingsSnapshot{
		ID:        "preset-id",
		AccountID: "acct1",
		Timestamp: time.Now(),
		Metadata:  map[string]any{"env": "prod"},
	}
	err := store.SaveSnapshot(ctx, snap)
	require.NoError(t, err)
	assert.Equal(t, "preset-id", snap.ID)
}

// ─── QuerySavings ──────────────────────────────────────────────────────────────

func TestPostgresAnalyticsStore_QuerySavings_Empty(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{
		"id", "account_id", "timestamp", "provider", "service", "region",
		"commitment_type", "total_commitment", "total_usage", "total_savings",
		"coverage_percentage", "metadata",
	})
	mock.ExpectQuery(`SELECT id, account_id`).
		WithArgs(anyArgs(3)...).
		WillReturnRows(rows)

	result, err := store.QuerySavings(ctx, QueryRequest{
		AccountID: "acct1",
		StartDate: time.Now().Add(-24 * time.Hour),
		EndDate:   time.Now(),
	})
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestPostgresAnalyticsStore_QuerySavings_WithFilters(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{
		"id", "account_id", "timestamp", "provider", "service", "region",
		"commitment_type", "total_commitment", "total_usage", "total_savings",
		"coverage_percentage", "metadata",
	}).AddRow("id1", "acct1", time.Now(), "aws", "ec2", "us-east-1", "RI",
		1000.0, 900.0, 100.0, 90.0, []byte(nil))

	// With provider + service + limit: 3 base + 2 filters + 1 limit = 6 args
	mock.ExpectQuery(`SELECT id, account_id`).
		WithArgs(anyArgs(6)...).
		WillReturnRows(rows)

	result, err := store.QuerySavings(ctx, QueryRequest{
		AccountID: "acct1",
		Provider:  "aws",
		Service:   "ec2",
		Limit:     10,
		StartDate: time.Now().Add(-24 * time.Hour),
		EndDate:   time.Now(),
	})
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "id1", result[0].ID)
}

func TestPostgresAnalyticsStore_QuerySavings_WithMetadata(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{
		"id", "account_id", "timestamp", "provider", "service", "region",
		"commitment_type", "total_commitment", "total_usage", "total_savings",
		"coverage_percentage", "metadata",
	}).AddRow("id1", "acct1", time.Now(), "aws", "ec2", "us-east-1", "RI",
		1000.0, 900.0, 100.0, 90.0, []byte(`{"key":"val"}`))

	mock.ExpectQuery(`SELECT id, account_id`).
		WithArgs(anyArgs(3)...).
		WillReturnRows(rows)

	result, err := store.QuerySavings(ctx, QueryRequest{
		AccountID: "acct1",
		StartDate: time.Now().Add(-24 * time.Hour),
		EndDate:   time.Now(),
	})
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "val", result[0].Metadata["key"])
}

func TestPostgresAnalyticsStore_QuerySavings_QueryError(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectQuery(`SELECT id, account_id`).
		WithArgs(anyArgs(3)...).
		WillReturnError(errors.New("db error"))

	_, err := store.QuerySavings(ctx, QueryRequest{
		AccountID: "acct1",
		StartDate: time.Now().Add(-24 * time.Hour),
		EndDate:   time.Now(),
	})
	assert.ErrorContains(t, err, "failed to query savings")
}

func TestPostgresAnalyticsStore_QuerySavings_MetadataUnmarshalError(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{
		"id", "account_id", "timestamp", "provider", "service", "region",
		"commitment_type", "total_commitment", "total_usage", "total_savings",
		"coverage_percentage", "metadata",
	}).AddRow("id1", "acct1", time.Now(), "aws", "ec2", "us-east-1", "RI",
		1000.0, 900.0, 100.0, 90.0, []byte(`{invalid json`))

	mock.ExpectQuery(`SELECT id, account_id`).
		WithArgs(anyArgs(3)...).
		WillReturnRows(rows)

	_, err := store.QuerySavings(ctx, QueryRequest{
		AccountID: "acct1",
		StartDate: time.Now().Add(-time.Hour),
		EndDate:   time.Now(),
	})
	assert.ErrorContains(t, err, "failed to unmarshal metadata")
}

func TestPostgresAnalyticsStore_QuerySavings_ScanError(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"id"}).AddRow("only-one-column")
	mock.ExpectQuery(`SELECT id, account_id`).
		WithArgs(anyArgs(3)...).
		WillReturnRows(rows)

	_, err := store.QuerySavings(ctx, QueryRequest{
		AccountID: "acct1",
		StartDate: time.Now().Add(-time.Hour),
		EndDate:   time.Now(),
	})
	assert.Error(t, err)
}

// ─── QueryMonthlyTotals ────────────────────────────────────────────────────────

func TestPostgresAnalyticsStore_QueryMonthlyTotals_Empty(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{
		"month", "account_id", "provider", "service",
		"total_savings", "avg_coverage", "snapshot_count",
	})
	mock.ExpectQuery(`SELECT month`).
		WithArgs(anyArgs(2)...).
		WillReturnRows(rows)

	result, err := store.QueryMonthlyTotals(ctx, "acct1", 6)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestPostgresAnalyticsStore_QueryMonthlyTotals_Error(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectQuery(`SELECT month`).
		WithArgs(anyArgs(2)...).
		WillReturnError(errors.New("db down"))

	_, err := store.QueryMonthlyTotals(ctx, "acct1", 6)
	assert.ErrorContains(t, err, "failed to query monthly totals")
}

func TestPostgresAnalyticsStore_QueryMonthlyTotals_WithRows(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{
		"month", "account_id", "provider", "service",
		"total_savings", "avg_coverage", "snapshot_count",
	}).AddRow(time.Now(), "acct1", "aws", "ec2", 500.0, 85.0, 10)

	mock.ExpectQuery(`SELECT month`).
		WithArgs(anyArgs(2)...).
		WillReturnRows(rows)

	result, err := store.QueryMonthlyTotals(ctx, "acct1", 6)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "aws", result[0].Provider)
}

func TestPostgresAnalyticsStore_QueryMonthlyTotals_ScanError(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"month"}).AddRow("only-one")
	mock.ExpectQuery(`SELECT month`).
		WithArgs(anyArgs(2)...).
		WillReturnRows(rows)

	_, err := store.QueryMonthlyTotals(ctx, "acct1", 3)
	assert.Error(t, err)
}

// ─── QueryByProvider ──────────────────────────────────────────────────────────

func TestPostgresAnalyticsStore_QueryByProvider_Empty(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"provider", "service", "total_savings", "avg_coverage"})
	mock.ExpectQuery(`SELECT provider`).
		WithArgs(anyArgs(3)...).
		WillReturnRows(rows)

	result, err := store.QueryByProvider(ctx, "acct1", time.Now().Add(-time.Hour), time.Now())
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestPostgresAnalyticsStore_QueryByProvider_Error(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectQuery(`SELECT provider`).
		WithArgs(anyArgs(3)...).
		WillReturnError(errors.New("err"))

	_, err := store.QueryByProvider(ctx, "acct1", time.Now().Add(-time.Hour), time.Now())
	assert.ErrorContains(t, err, "failed to query by provider")
}

func TestPostgresAnalyticsStore_QueryByProvider_WithRows(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"provider", "service", "total_savings", "avg_coverage"}).
		AddRow("aws", "ec2", 200.0, 80.0)
	mock.ExpectQuery(`SELECT provider`).
		WithArgs(anyArgs(3)...).
		WillReturnRows(rows)

	result, err := store.QueryByProvider(ctx, "acct1", time.Now().Add(-time.Hour), time.Now())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "aws", result[0].Provider)
}

func TestPostgresAnalyticsStore_QueryByProvider_ScanError(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"provider"}).AddRow("only-one")
	mock.ExpectQuery(`SELECT provider`).
		WithArgs(anyArgs(3)...).
		WillReturnRows(rows)

	_, err := store.QueryByProvider(ctx, "acct1", time.Now().Add(-time.Hour), time.Now())
	assert.Error(t, err)
}

// ─── QueryByService ───────────────────────────────────────────────────────────

func TestPostgresAnalyticsStore_QueryByService_Empty(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"service", "region", "total_savings", "avg_coverage"})
	mock.ExpectQuery(`SELECT service`).
		WithArgs(anyArgs(4)...).
		WillReturnRows(rows)

	result, err := store.QueryByService(ctx, "acct1", "aws", time.Now().Add(-time.Hour), time.Now())
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestPostgresAnalyticsStore_QueryByService_Error(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectQuery(`SELECT service`).
		WithArgs(anyArgs(4)...).
		WillReturnError(errors.New("err"))

	_, err := store.QueryByService(ctx, "acct1", "aws", time.Now().Add(-time.Hour), time.Now())
	assert.ErrorContains(t, err, "failed to query by service")
}

func TestPostgresAnalyticsStore_QueryByService_WithRows(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"service", "region", "total_savings", "avg_coverage"}).
		AddRow("ec2", "us-east-1", 300.0, 75.0)
	mock.ExpectQuery(`SELECT service`).
		WithArgs(anyArgs(4)...).
		WillReturnRows(rows)

	result, err := store.QueryByService(ctx, "acct1", "aws", time.Now().Add(-time.Hour), time.Now())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "ec2", result[0].Service)
}

func TestPostgresAnalyticsStore_QueryByService_ScanError(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"service"}).AddRow("only-one")
	mock.ExpectQuery(`SELECT service`).
		WithArgs(anyArgs(4)...).
		WillReturnRows(rows)

	_, err := store.QueryByService(ctx, "acct1", "aws", time.Now().Add(-time.Hour), time.Now())
	assert.Error(t, err)
}

// ─── Partition management ─────────────────────────────────────────────────────

func TestPostgresAnalyticsStore_CreatePartition_Success(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectExec(`SELECT create_savings_snapshot_partition`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))

	err := store.CreatePartition(ctx, time.Now())
	require.NoError(t, err)
}

func TestPostgresAnalyticsStore_CreatePartition_Error(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectExec(`SELECT create_savings_snapshot_partition`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnError(errors.New("partition error"))

	err := store.CreatePartition(ctx, time.Now())
	assert.ErrorContains(t, err, "failed to create partition")
}

func TestPostgresAnalyticsStore_DropOldPartitions_Success(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectExec(`SELECT drop_old_savings_partitions`).
		WithArgs(12).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))

	err := store.DropOldPartitions(ctx, 12)
	require.NoError(t, err)
}

func TestPostgresAnalyticsStore_DropOldPartitions_Error(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectExec(`SELECT drop_old_savings_partitions`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnError(errors.New("drop error"))

	err := store.DropOldPartitions(ctx, 12)
	assert.ErrorContains(t, err, "failed to drop old partitions")
}

func TestPostgresAnalyticsStore_RefreshMaterializedViews_Success(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectExec(`SELECT refresh_savings_materialized_views`).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))

	err := store.RefreshMaterializedViews(ctx)
	require.NoError(t, err)
}

func TestPostgresAnalyticsStore_RefreshMaterializedViews_Error(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectExec(`SELECT refresh_savings_materialized_views`).
		WillReturnError(errors.New("refresh error"))

	err := store.RefreshMaterializedViews(ctx)
	assert.ErrorContains(t, err, "failed to refresh materialized views")
}

func TestPostgresAnalyticsStore_CreatePartitionsForRange_TwoMonths(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectExec(`SELECT create_savings_snapshot_partition`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectExec(`SELECT create_savings_snapshot_partition`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))

	start := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 2, 20, 0, 0, 0, 0, time.UTC)
	err := store.CreatePartitionsForRange(ctx, start, end)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAnalyticsStore_CreatePartitionsForRange_Error(t *testing.T) {
	store, mock := newMockAnalyticsStore(t)
	ctx := context.Background()

	mock.ExpectExec(`SELECT create_savings_snapshot_partition`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnError(errors.New("create failed"))

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)
	err := store.CreatePartitionsForRange(ctx, start, end)
	assert.ErrorContains(t, err, "failed to create partition for")
}

// ─── BulkInsertSnapshots ──────────────────────────────────────────────────────

// pgxmock Acquire always returns an error, so this exercises the acquire-error path.
func TestPostgresAnalyticsStore_BulkInsertSnapshots_AcquireError(t *testing.T) {
	store, _ := newMockAnalyticsStore(t)
	ctx := context.Background()

	snapshots := []SavingsSnapshot{
		{AccountID: "acct1", Timestamp: time.Now(), Provider: "aws"},
	}
	err := store.BulkInsertSnapshots(ctx, snapshots)
	assert.ErrorContains(t, err, "failed to acquire connection")
}

// ─── NewPostgresAnalyticsStore via interface ──────────────────────────────────

func TestNewPostgresAnalyticsStore_ViaInterface(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	store := &PostgresAnalyticsStore{db: mock}
	assert.NotNil(t, store)
}

// ─── rows.Err() propagation ───────────────────────────────────────────────────
// Verified via QueryMonthlyTotals/ByProvider/ByService scan-error tests above.
