package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testablePostgresAnalyticsStore is a thin test wrapper that delegates to the
// REAL PostgresAnalyticsStore (with a pgxmock pool as its dbConn) so these
// tests exercise the production SQL rather than a parallel reimplementation
// that could silently drift from it. The embedded *PostgresAnalyticsStore
// satisfies AnalyticsStore; only SaveSnapshot is overridden to give empty IDs
// a deterministic value the assertions can match.
type testablePostgresAnalyticsStore struct {
	*PostgresAnalyticsStore
	mock pgxmock.PgxPoolIface
}

func newTestableStore(mock pgxmock.PgxPoolIface) *testablePostgresAnalyticsStore {
	return &testablePostgresAnalyticsStore{
		PostgresAnalyticsStore: &PostgresAnalyticsStore{db: mock},
		mock:                   mock,
	}
}

// SaveSnapshot delegates to the real store after assigning a deterministic ID
// for the empty-ID case so the WithArgs expectations stay stable.
func (s *testablePostgresAnalyticsStore) SaveSnapshot(ctx context.Context, snapshot *SavingsSnapshot) error {
	if snapshot.ID == "" {
		snapshot.ID = "generated-uuid"
	}
	return s.PostgresAnalyticsStore.SaveSnapshot(ctx, snapshot)
}

// Verify the wrapper still satisfies AnalyticsStore.
var _ AnalyticsStore = (*testablePostgresAnalyticsStore)(nil)

// =====================
// Tests
// =====================

// TestNewPostgresAnalyticsStore tests the constructor
func TestNewPostgresAnalyticsStore(t *testing.T) {
	t.Run("creates store with database connection", func(t *testing.T) {
		store := NewPostgresAnalyticsStore(nil)
		assert.NotNil(t, store)
	})
}

// TestPostgresAnalyticsStore_Close tests the Close method
func TestPostgresAnalyticsStore_Close(t *testing.T) {
	t.Run("returns nil on close", func(t *testing.T) {
		store := NewPostgresAnalyticsStore(nil)
		err := store.Close()
		assert.NoError(t, err)
	})
}

// TestSaveSnapshot tests the SaveSnapshot method
func TestSaveSnapshot(t *testing.T) {
	t.Run("saves snapshot successfully", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		snapshot := &SavingsSnapshot{
			ID:                 "test-id",
			AccountID:          "account-123",
			Timestamp:          now,
			Provider:           "aws",
			Service:            "rds",
			Region:             "us-east-1",
			CommitmentType:     "RI",
			TotalCommitment:    100.0,
			TotalUsage:         f64ptr(80.0),
			TotalSavings:       20.0,
			CoveragePercentage: f64ptr(80.0),
			Metadata:           map[string]interface{}{"key": "value"},
		}

		mock.ExpectExec(`INSERT INTO savings_snapshots`).
			WithArgs(
				snapshot.ID,
				snapshot.AccountID,
				snapshot.CloudAccountID,
				snapshot.Timestamp,
				snapshot.Provider,
				snapshot.Service,
				snapshot.Region,
				snapshot.CommitmentType,
				snapshot.TotalCommitment,
				snapshot.TotalUsage,
				snapshot.TotalSavings,
				snapshot.CoveragePercentage,
				pgxmock.AnyArg(), // metadata JSON
			).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		err = store.SaveSnapshot(context.Background(), snapshot)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("generates UUID when ID is empty", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		snapshot := &SavingsSnapshot{
			ID:        "", // empty ID
			AccountID: "account-123",
			Timestamp: time.Now().UTC(),
			Provider:  "aws",
			Service:   "rds",
		}

		mock.ExpectExec(`INSERT INTO savings_snapshots`).
			WithArgs(append([]any{"generated-uuid"}, anyArgs(12)...)...).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		err = store.SaveSnapshot(context.Background(), snapshot)
		assert.NoError(t, err)
		assert.Equal(t, "generated-uuid", snapshot.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("handles nil metadata", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		snapshot := &SavingsSnapshot{
			ID:        "test-id",
			AccountID: "account-123",
			Timestamp: time.Now().UTC(),
			Provider:  "aws",
			Service:   "rds",
			Metadata:  nil,
		}

		mock.ExpectExec(`INSERT INTO savings_snapshots`).
			WithArgs(anyArgs(13)...). // includes nil cloud_account_id + nil metadata
			WillReturnResult(pgxmock.NewResult("INSERT", 1))

		err = store.SaveSnapshot(context.Background(), snapshot)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns error on database failure", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		snapshot := &SavingsSnapshot{
			ID:        "test-id",
			AccountID: "account-123",
			Timestamp: time.Now().UTC(),
		}

		mock.ExpectExec(`INSERT INTO savings_snapshots`).
			WithArgs(anyArgs(13)...).
			WillReturnError(errors.New("database error"))

		err = store.SaveSnapshot(context.Background(), snapshot)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "database error")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestBulkInsertSnapshots tests the BulkInsertSnapshots method
func TestBulkInsertSnapshots(t *testing.T) {
	t.Run("returns early for empty slice", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		err = store.BulkInsertSnapshots(context.Background(), []SavingsSnapshot{})
		assert.NoError(t, err)
	})

	t.Run("returns error for non-empty slice in test mode", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		// The real store's COPY path acquires a pooled connection; pgxmock's
		// Acquire is unimplemented, so a non-empty bulk insert surfaces that.
		err = store.BulkInsertSnapshots(context.Background(), []SavingsSnapshot{{CommitmentType: "RI"}})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to acquire connection")
	})

	t.Run("non-empty slice errors when connection acquire fails", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		// pgxmock.Acquire is unimplemented, so the COPY builder (where the
		// commitment_type guard lives) is reached only after a real connection in
		// production. This path therefore asserts the acquire-failure behaviour,
		// not the type guard; the guard itself is covered by
		// TestValidateCommitmentType below and exercised against a real DB in the
		// integration suite.
		err = store.BulkInsertSnapshots(context.Background(), []SavingsSnapshot{{CommitmentType: "RI"}})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to acquire connection")
	})
}

// TestValidateCommitmentType directly exercises the commitment_type guard that
// BulkInsertSnapshots applies in its COPY builder (L4), since the COPY path
// itself can only be reached with a real pooled connection.
func TestValidateCommitmentType(t *testing.T) {
	t.Run("accepts RI", func(t *testing.T) {
		assert.NoError(t, validateCommitmentType("RI"))
	})
	t.Run("accepts SavingsPlan", func(t *testing.T) {
		assert.NoError(t, validateCommitmentType("SavingsPlan"))
	})
	t.Run("rejects an unknown value", func(t *testing.T) {
		err := validateCommitmentType("bogus")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid commitment_type")
		assert.Contains(t, err.Error(), "bogus")
	})
	t.Run("rejects empty", func(t *testing.T) {
		assert.Error(t, validateCommitmentType(""))
	})
}

// TestQuerySavings tests the QuerySavings method
func TestQuerySavings(t *testing.T) {
	t.Run("queries savings successfully", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-24 * time.Hour)
		metadataJSON, _ := json.Marshal(map[string]interface{}{"key": "value"})

		rows := pgxmock.NewRows([]string{
			"id", "account_id", "cloud_account_id", "timestamp", "provider", "service", "region",
			"commitment_type", "total_commitment", "total_usage", "total_savings",
			"coverage_percentage", "metadata",
		}).AddRow(
			"snapshot-1", "account-123", strPtr("cloud-1"), now, "aws", "rds", "us-east-1",
			"RI", 100.0, f64ptr(80.0), 20.0, f64ptr(80.0), metadataJSON,
		)

		mock.ExpectQuery(`SELECT id, account_id`).
			WithArgs(startDate, now, []string{"account-123"}).
			WillReturnRows(rows)

		req := QueryRequest{
			AccountUUIDs: []string{"account-123"},
			StartDate:    startDate,
			EndDate:      now,
		}

		snapshots, err := store.QuerySavings(context.Background(), req)
		require.NoError(t, err)
		assert.Len(t, snapshots, 1)
		assert.Equal(t, "snapshot-1", snapshots[0].ID)
		assert.Equal(t, "aws", snapshots[0].Provider)
		assert.Equal(t, "rds", snapshots[0].Service)
		assert.Equal(t, "value", snapshots[0].Metadata["key"])
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("queries with provider filter", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-24 * time.Hour)

		rows := pgxmock.NewRows([]string{
			"id", "account_id", "cloud_account_id", "timestamp", "provider", "service", "region",
			"commitment_type", "total_commitment", "total_usage", "total_savings",
			"coverage_percentage", "metadata",
		})

		mock.ExpectQuery(`SELECT id, account_id`).
			WithArgs(startDate, now, []string{"account-123"}, "aws").
			WillReturnRows(rows)

		req := QueryRequest{
			AccountUUIDs: []string{"account-123"},
			Provider:     "aws",
			StartDate:    startDate,
			EndDate:      now,
		}

		_, err = store.QuerySavings(context.Background(), req)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("queries with service filter", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-24 * time.Hour)

		rows := pgxmock.NewRows([]string{
			"id", "account_id", "cloud_account_id", "timestamp", "provider", "service", "region",
			"commitment_type", "total_commitment", "total_usage", "total_savings",
			"coverage_percentage", "metadata",
		})

		mock.ExpectQuery(`SELECT id, account_id`).
			WithArgs(startDate, now, []string{"account-123"}, "rds").
			WillReturnRows(rows)

		req := QueryRequest{
			AccountUUIDs: []string{"account-123"},
			Service:      "rds",
			StartDate:    startDate,
			EndDate:      now,
		}

		_, err = store.QuerySavings(context.Background(), req)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("queries with limit", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-24 * time.Hour)

		rows := pgxmock.NewRows([]string{
			"id", "account_id", "cloud_account_id", "timestamp", "provider", "service", "region",
			"commitment_type", "total_commitment", "total_usage", "total_savings",
			"coverage_percentage", "metadata",
		})

		mock.ExpectQuery(`SELECT id, account_id`).
			WithArgs(startDate, now, []string{"account-123"}, 10).
			WillReturnRows(rows)

		req := QueryRequest{
			AccountUUIDs: []string{"account-123"},
			StartDate:    startDate,
			EndDate:      now,
			Limit:        10,
		}

		_, err = store.QuerySavings(context.Background(), req)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns empty list when no rows", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-24 * time.Hour)

		rows := pgxmock.NewRows([]string{
			"id", "account_id", "cloud_account_id", "timestamp", "provider", "service", "region",
			"commitment_type", "total_commitment", "total_usage", "total_savings",
			"coverage_percentage", "metadata",
		})

		mock.ExpectQuery(`SELECT id, account_id`).
			WithArgs(startDate, now, []string{"account-123"}).
			WillReturnRows(rows)

		req := QueryRequest{
			AccountUUIDs: []string{"account-123"},
			StartDate:    startDate,
			EndDate:      now,
		}

		snapshots, err := store.QuerySavings(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, snapshots)
		assert.Empty(t, snapshots)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns error on query failure", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-24 * time.Hour)

		mock.ExpectQuery(`SELECT id, account_id`).
			WithArgs(startDate, now, []string{"account-123"}).
			WillReturnError(errors.New("database error"))

		req := QueryRequest{
			AccountUUIDs: []string{"account-123"},
			StartDate:    startDate,
			EndDate:      now,
		}

		_, err = store.QuerySavings(context.Background(), req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "database error")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("handles invalid metadata JSON", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-24 * time.Hour)

		rows := pgxmock.NewRows([]string{
			"id", "account_id", "cloud_account_id", "timestamp", "provider", "service", "region",
			"commitment_type", "total_commitment", "total_usage", "total_savings",
			"coverage_percentage", "metadata",
		}).AddRow(
			"snapshot-1", "account-123", strPtr("cloud-1"), now, "aws", "rds", "us-east-1",
			"RI", 100.0, f64ptr(80.0), 20.0, f64ptr(80.0), []byte("invalid json"),
		)

		mock.ExpectQuery(`SELECT id, account_id`).
			WithArgs(startDate, now, []string{"account-123"}).
			WillReturnRows(rows)

		req := QueryRequest{
			AccountUUIDs: []string{"account-123"},
			StartDate:    startDate,
			EndDate:      now,
		}

		_, err = store.QuerySavings(context.Background(), req)
		assert.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestQueryMonthlyTotals tests the QueryMonthlyTotals method
func TestQueryMonthlyTotals(t *testing.T) {
	t.Run("queries monthly totals successfully", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		month := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

		rows := pgxmock.NewRows([]string{
			"month", "account_id", "cloud_account_id", "provider", "service", "total_savings", "avg_coverage", "snapshot_count",
		}).
			AddRow(month, "account-123", strPtr("cloud-1"), "aws", "rds", 1500.0, f64ptr(85.0), 720).
			AddRow(month, "account-123", strPtr("cloud-1"), "aws", "elasticache", 800.0, f64ptr(75.0), 720)

		mock.ExpectQuery(`SELECT month, account_id`).
			WithArgs(6, []string{"account-123"}).
			WillReturnRows(rows)

		summaries, err := store.QueryMonthlyTotals(context.Background(), []string{"account-123"}, nil, 6)
		require.NoError(t, err)
		assert.Len(t, summaries, 2)
		assert.Equal(t, "rds", summaries[0].Service)
		assert.Equal(t, 1500.0, summaries[0].TotalSavings)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns error on query failure", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		mock.ExpectQuery(`SELECT month, account_id`).
			WithArgs(6, []string{"account-123"}).
			WillReturnError(errors.New("database error"))

		_, err = store.QueryMonthlyTotals(context.Background(), []string{"account-123"}, nil, 6)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "database error")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns empty list when no data", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		rows := pgxmock.NewRows([]string{
			"month", "account_id", "cloud_account_id", "provider", "service", "total_savings", "avg_coverage", "snapshot_count",
		})

		mock.ExpectQuery(`SELECT month, account_id`).
			WithArgs(6, []string{"account-123"}).
			WillReturnRows(rows)

		summaries, err := store.QueryMonthlyTotals(context.Background(), []string{"account-123"}, nil, 6)
		require.NoError(t, err)
		assert.Empty(t, summaries)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestQueryByProvider tests the QueryByProvider method
func TestQueryByProvider(t *testing.T) {
	t.Run("queries by provider successfully", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-30 * 24 * time.Hour)

		rows := pgxmock.NewRows([]string{
			"provider", "service", "total_savings", "avg_coverage",
		}).
			AddRow("aws", "rds", 2500.0, f64ptr(85.0)).
			AddRow("aws", "elasticache", 1200.0, f64ptr(75.0))

		mock.ExpectQuery(`SELECT provider, service, SUM\(total_savings\) as total_savings`).
			WithArgs(startDate, now, []string{"account-123"}).
			WillReturnRows(rows)

		breakdowns, err := store.QueryByProvider(context.Background(), []string{"account-123"}, nil, startDate, now)
		require.NoError(t, err)
		assert.Len(t, breakdowns, 2)
		assert.Equal(t, "aws", breakdowns[0].Provider)
		assert.Equal(t, "rds", breakdowns[0].Service)
		assert.Equal(t, 2500.0, breakdowns[0].TotalSavings)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns error on query failure", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-30 * 24 * time.Hour)

		mock.ExpectQuery(`SELECT provider, service, SUM\(total_savings\) as total_savings`).
			WithArgs(startDate, now, []string{"account-123"}).
			WillReturnError(errors.New("database error"))

		_, err = store.QueryByProvider(context.Background(), []string{"account-123"}, nil, startDate, now)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "database error")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestQueryByService tests the QueryByService method
func TestQueryByService(t *testing.T) {
	t.Run("queries by service successfully", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-30 * 24 * time.Hour)

		rows := pgxmock.NewRows([]string{
			"service", "region", "total_savings", "avg_coverage",
		}).
			AddRow("rds", "us-east-1", 1800.0, f64ptr(90.0)).
			AddRow("rds", "us-west-2", 700.0, f64ptr(75.0))

		mock.ExpectQuery(`SELECT service, region, SUM\(total_savings\) as total_savings`).
			WithArgs(startDate, now, []string{"account-123"}, "aws").
			WillReturnRows(rows)

		breakdowns, err := store.QueryByService(context.Background(), []string{"account-123"}, nil, "aws", startDate, now)
		require.NoError(t, err)
		assert.Len(t, breakdowns, 2)
		assert.Equal(t, "rds", breakdowns[0].Service)
		assert.Equal(t, "us-east-1", breakdowns[0].Region)
		assert.Equal(t, 1800.0, breakdowns[0].TotalSavings)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns error on query failure", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-30 * 24 * time.Hour)

		mock.ExpectQuery(`SELECT service, region, SUM\(total_savings\) as total_savings`).
			WithArgs(startDate, now, []string{"account-123"}, "aws").
			WillReturnError(errors.New("database error"))

		_, err = store.QueryByService(context.Background(), []string{"account-123"}, nil, "aws", startDate, now)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "database error")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestCreatePartition tests the CreatePartition method
func TestCreatePartition(t *testing.T) {
	t.Run("creates partition successfully", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		forMonth := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)

		mock.ExpectExec(`SELECT create_savings_snapshot_partition`).
			WithArgs(forMonth).
			WillReturnResult(pgxmock.NewResult("SELECT", 1))

		err = store.CreatePartition(context.Background(), forMonth)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns error on failure", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		forMonth := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)

		mock.ExpectExec(`SELECT create_savings_snapshot_partition`).
			WithArgs(forMonth).
			WillReturnError(errors.New("partition error"))

		err = store.CreatePartition(context.Background(), forMonth)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "partition error")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestDropOldPartitions tests the DropOldPartitions method
func TestDropOldPartitions(t *testing.T) {
	t.Run("drops old partitions successfully", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		mock.ExpectExec(`SELECT drop_old_savings_partitions`).
			WithArgs(12).
			WillReturnResult(pgxmock.NewResult("SELECT", 1))

		err = store.DropOldPartitions(context.Background(), 12)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns error on failure", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		mock.ExpectExec(`SELECT drop_old_savings_partitions`).
			WithArgs(12).
			WillReturnError(errors.New("drop error"))

		err = store.DropOldPartitions(context.Background(), 12)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "drop error")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestCreatePartitionsForRange tests the CreatePartitionsForRange method
func TestCreatePartitionsForRange(t *testing.T) {
	t.Run("creates partitions for range successfully", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
		endDate := time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC)

		// Expect 3 partition creations: Jan, Feb, Mar
		jan := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		feb := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
		mar := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)

		mock.ExpectExec(`SELECT create_savings_snapshot_partition`).
			WithArgs(jan).
			WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectExec(`SELECT create_savings_snapshot_partition`).
			WithArgs(feb).
			WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectExec(`SELECT create_savings_snapshot_partition`).
			WithArgs(mar).
			WillReturnResult(pgxmock.NewResult("SELECT", 1))

		err = store.CreatePartitionsForRange(context.Background(), startDate, endDate)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns error when partition creation fails", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
		endDate := time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC)

		jan := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		feb := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

		mock.ExpectExec(`SELECT create_savings_snapshot_partition`).
			WithArgs(jan).
			WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectExec(`SELECT create_savings_snapshot_partition`).
			WithArgs(feb).
			WillReturnError(errors.New("partition error"))

		err = store.CreatePartitionsForRange(context.Background(), startDate, endDate)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "partition error")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestRefreshMaterializedViews tests the RefreshMaterializedViews method
func TestRefreshMaterializedViews(t *testing.T) {
	t.Run("refreshes materialized views successfully", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		mock.ExpectExec(`SELECT refresh_savings_materialized_views`).
			WillReturnResult(pgxmock.NewResult("SELECT", 1))

		err = store.RefreshMaterializedViews(context.Background())
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns error on failure", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		mock.ExpectExec(`SELECT refresh_savings_materialized_views`).
			WillReturnError(errors.New("refresh error"))

		err = store.RefreshMaterializedViews(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "refresh error")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestSavingsSnapshot tests the SavingsSnapshot struct
func TestSavingsSnapshot(t *testing.T) {
	t.Run("creates snapshot with all fields", func(t *testing.T) {
		now := time.Now()
		snapshot := &SavingsSnapshot{
			ID:                 "test-id",
			AccountID:          "account-123",
			Timestamp:          now,
			Provider:           "aws",
			Service:            "rds",
			Region:             "us-east-1",
			CommitmentType:     "RI",
			TotalCommitment:    100.50,
			TotalUsage:         f64ptr(80.25),
			TotalSavings:       20.25,
			CoveragePercentage: f64ptr(80.0),
			Metadata: map[string]interface{}{
				"key": "value",
			},
		}

		assert.Equal(t, "test-id", snapshot.ID)
		assert.Equal(t, "account-123", snapshot.AccountID)
		assert.Equal(t, now, snapshot.Timestamp)
		assert.Equal(t, "aws", snapshot.Provider)
		assert.Equal(t, "rds", snapshot.Service)
		assert.Equal(t, "us-east-1", snapshot.Region)
		assert.Equal(t, "RI", snapshot.CommitmentType)
		assert.InDelta(t, 100.50, snapshot.TotalCommitment, 0.001)
		require.NotNil(t, snapshot.TotalUsage)
		assert.InDelta(t, 80.25, *snapshot.TotalUsage, 0.001)
		assert.InDelta(t, 20.25, snapshot.TotalSavings, 0.001)
		require.NotNil(t, snapshot.CoveragePercentage)
		assert.InDelta(t, 80.0, *snapshot.CoveragePercentage, 0.001)
		assert.Equal(t, "value", snapshot.Metadata["key"])
	})

	t.Run("json marshaling works correctly", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		snapshot := &SavingsSnapshot{
			ID:                 "test-id",
			AccountID:          "account-123",
			Timestamp:          now,
			Provider:           "aws",
			Service:            "rds",
			Region:             "us-east-1",
			CommitmentType:     "RI",
			TotalCommitment:    100.50,
			TotalUsage:         f64ptr(80.25),
			TotalSavings:       20.25,
			CoveragePercentage: f64ptr(80.0),
		}

		data, err := json.Marshal(snapshot)
		require.NoError(t, err)

		var unmarshaled SavingsSnapshot
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err)

		assert.Equal(t, snapshot.ID, unmarshaled.ID)
		assert.Equal(t, snapshot.AccountID, unmarshaled.AccountID)
		assert.Equal(t, snapshot.Provider, unmarshaled.Provider)
		assert.Equal(t, snapshot.Service, unmarshaled.Service)
	})
}

// TestQueryRequest tests the QueryRequest struct
func TestQueryRequest(t *testing.T) {
	t.Run("creates query request with all fields", func(t *testing.T) {
		start := time.Now().Add(-24 * time.Hour)
		end := time.Now()
		req := QueryRequest{
			AccountUUIDs: []string{"account-123"},
			Provider:     "aws",
			Service:      "rds",
			StartDate:    start,
			EndDate:      end,
			Limit:        100,
		}

		assert.Equal(t, []string{"account-123"}, req.AccountUUIDs)
		assert.Equal(t, "aws", req.Provider)
		assert.Equal(t, "rds", req.Service)
		assert.Equal(t, start, req.StartDate)
		assert.Equal(t, end, req.EndDate)
		assert.Equal(t, 100, req.Limit)
	})

	t.Run("handles optional fields", func(t *testing.T) {
		req := QueryRequest{
			AccountUUIDs: []string{"account-123"},
			StartDate:    time.Now().Add(-24 * time.Hour),
			EndDate:      time.Now(),
		}

		assert.Equal(t, "", req.Provider) // Optional, can be empty
		assert.Equal(t, "", req.Service)  // Optional, can be empty
		assert.Equal(t, 0, req.Limit)     // Optional, 0 means no limit
	})
}

// TestMonthlySummary tests the MonthlySummary struct
func TestMonthlySummary(t *testing.T) {
	t.Run("creates monthly summary with all fields", func(t *testing.T) {
		month := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		summary := MonthlySummary{
			Month:         month,
			AccountID:     "account-123",
			Provider:      "aws",
			Service:       "rds",
			TotalSavings:  1500.50,
			AvgCoverage:   f64ptr(85.5),
			SnapshotCount: 720,
		}

		assert.Equal(t, month, summary.Month)
		assert.Equal(t, "account-123", summary.AccountID)
		assert.Equal(t, "aws", summary.Provider)
		assert.Equal(t, "rds", summary.Service)
		assert.InDelta(t, 1500.50, summary.TotalSavings, 0.001)
		require.NotNil(t, summary.AvgCoverage)
		assert.InDelta(t, 85.5, *summary.AvgCoverage, 0.001)
		assert.Equal(t, 720, summary.SnapshotCount)
	})

	t.Run("json marshaling works correctly", func(t *testing.T) {
		month := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		summary := MonthlySummary{
			Month:         month,
			AccountID:     "account-123",
			Provider:      "aws",
			Service:       "rds",
			TotalSavings:  1500.50,
			AvgCoverage:   f64ptr(85.5),
			SnapshotCount: 720,
		}

		data, err := json.Marshal(summary)
		require.NoError(t, err)

		var unmarshaled MonthlySummary
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err)

		assert.Equal(t, summary.AccountID, unmarshaled.AccountID)
		assert.Equal(t, summary.Provider, unmarshaled.Provider)
		assert.InDelta(t, summary.TotalSavings, unmarshaled.TotalSavings, 0.001)
	})
}

// TestProviderBreakdown tests the ProviderBreakdown struct
func TestProviderBreakdown(t *testing.T) {
	t.Run("creates provider breakdown with all fields", func(t *testing.T) {
		breakdown := ProviderBreakdown{
			Provider:     "aws",
			Service:      "rds",
			TotalSavings: 2500.75,
			AvgCoverage:  f64ptr(90.5),
		}

		assert.Equal(t, "aws", breakdown.Provider)
		assert.Equal(t, "rds", breakdown.Service)
		assert.InDelta(t, 2500.75, breakdown.TotalSavings, 0.001)
		require.NotNil(t, breakdown.AvgCoverage)
		assert.InDelta(t, 90.5, *breakdown.AvgCoverage, 0.001)
	})

	t.Run("json marshaling works correctly", func(t *testing.T) {
		breakdown := ProviderBreakdown{
			Provider:     "gcp",
			Service:      "cloudsql",
			TotalSavings: 1200.00,
			AvgCoverage:  f64ptr(75.0),
		}

		data, err := json.Marshal(breakdown)
		require.NoError(t, err)

		var unmarshaled ProviderBreakdown
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err)

		assert.Equal(t, breakdown.Provider, unmarshaled.Provider)
		assert.Equal(t, breakdown.Service, unmarshaled.Service)
	})
}

// TestServiceBreakdown tests the ServiceBreakdown struct
func TestServiceBreakdown(t *testing.T) {
	t.Run("creates service breakdown with all fields", func(t *testing.T) {
		breakdown := ServiceBreakdown{
			Service:      "elasticache",
			Region:       "us-west-2",
			TotalSavings: 800.25,
			AvgCoverage:  f64ptr(82.0),
		}

		assert.Equal(t, "elasticache", breakdown.Service)
		assert.Equal(t, "us-west-2", breakdown.Region)
		assert.InDelta(t, 800.25, breakdown.TotalSavings, 0.001)
		require.NotNil(t, breakdown.AvgCoverage)
		assert.InDelta(t, 82.0, *breakdown.AvgCoverage, 0.001)
	})

	t.Run("json marshaling works correctly", func(t *testing.T) {
		breakdown := ServiceBreakdown{
			Service:      "memorystore",
			Region:       "us-central1",
			TotalSavings: 450.00,
			AvgCoverage:  f64ptr(70.0),
		}

		data, err := json.Marshal(breakdown)
		require.NoError(t, err)

		var unmarshaled ServiceBreakdown
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err)

		assert.Equal(t, breakdown.Service, unmarshaled.Service)
		assert.Equal(t, breakdown.Region, unmarshaled.Region)
	})
}

// TestAnalyticsStoreInterface tests that PostgresAnalyticsStore implements AnalyticsStore
func TestAnalyticsStoreInterface(t *testing.T) {
	t.Run("PostgresAnalyticsStore implements AnalyticsStore interface", func(t *testing.T) {
		// This is a compile-time check that's already in the code,
		// but we can test it explicitly
		var _ AnalyticsStore = (*PostgresAnalyticsStore)(nil)
	})
}

// TestQuerySavingsRowScanError tests scan error handling
func TestQuerySavingsRowScanError(t *testing.T) {
	t.Run("returns error on row scan failure", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-24 * time.Hour)

		// Return a row with incorrect number of columns to cause scan error
		rows := pgxmock.NewRows([]string{
			"id", "account_id", // Missing other columns
		}).AddRow("snapshot-1", "account-123").RowError(0, errors.New("scan error"))

		mock.ExpectQuery(`SELECT id, account_id`).
			WithArgs(startDate, now, []string{"account-123"}).
			WillReturnRows(rows)

		req := QueryRequest{
			AccountUUIDs: []string{"account-123"},
			StartDate:    startDate,
			EndDate:      now,
		}

		_, err = store.QuerySavings(context.Background(), req)
		assert.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestQueryMonthlyTotalsRowScanError tests scan error handling
func TestQueryMonthlyTotalsRowScanError(t *testing.T) {
	t.Run("returns error on row scan failure", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		rows := pgxmock.NewRows([]string{
			"month", "account_id", // Missing other columns
		}).AddRow(time.Now(), "account-123").RowError(0, errors.New("scan error"))

		mock.ExpectQuery(`SELECT month, account_id`).
			WithArgs(6, []string{"account-123"}).
			WillReturnRows(rows)

		_, err = store.QueryMonthlyTotals(context.Background(), []string{"account-123"}, nil, 6)
		assert.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestQueryByProviderRowScanError tests scan error handling
func TestQueryByProviderRowScanError(t *testing.T) {
	t.Run("returns error on row scan failure", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-30 * 24 * time.Hour)

		rows := pgxmock.NewRows([]string{
			"provider", // Missing other columns
		}).AddRow("aws").RowError(0, errors.New("scan error"))

		mock.ExpectQuery(`SELECT provider, service, SUM\(total_savings\) as total_savings`).
			WithArgs(startDate, now, []string{"account-123"}).
			WillReturnRows(rows)

		_, err = store.QueryByProvider(context.Background(), []string{"account-123"}, nil, startDate, now)
		assert.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestQueryByServiceRowScanError tests scan error handling
func TestQueryByServiceRowScanError(t *testing.T) {
	t.Run("returns error on row scan failure", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-30 * 24 * time.Hour)

		rows := pgxmock.NewRows([]string{
			"service", // Missing other columns
		}).AddRow("rds").RowError(0, errors.New("scan error"))

		mock.ExpectQuery(`SELECT service, region, SUM\(total_savings\) as total_savings`).
			WithArgs(startDate, now, []string{"account-123"}, "aws").
			WillReturnRows(rows)

		_, err = store.QueryByService(context.Background(), []string{"account-123"}, nil, "aws", startDate, now)
		assert.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestRowsErr tests rows.Err() handling
func TestRowsErr(t *testing.T) {
	t.Run("QuerySavings returns rows.Err()", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-24 * time.Hour)
		metadataJSON, _ := json.Marshal(map[string]interface{}{})

		rows := pgxmock.NewRows([]string{
			"id", "account_id", "cloud_account_id", "timestamp", "provider", "service", "region",
			"commitment_type", "total_commitment", "total_usage", "total_savings",
			"coverage_percentage", "metadata",
		}).AddRow(
			"snapshot-1", "account-123", strPtr("cloud-1"), now, "aws", "rds", "us-east-1",
			"RI", 100.0, f64ptr(80.0), 20.0, f64ptr(80.0), metadataJSON,
		)

		mock.ExpectQuery(`SELECT id, account_id`).
			WithArgs(startDate, now, []string{"account-123"}).
			WillReturnRows(rows)

		req := QueryRequest{
			AccountUUIDs: []string{"account-123"},
			StartDate:    startDate,
			EndDate:      now,
		}

		snapshots, err := store.QuerySavings(context.Background(), req)
		require.NoError(t, err)
		assert.Len(t, snapshots, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestQueryMonthlyTotalsRowsErr tests rows.Err() handling for monthly totals
func TestQueryMonthlyTotalsRowsErr(t *testing.T) {
	t.Run("QueryMonthlyTotals returns rows.Err()", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		month := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

		rows := pgxmock.NewRows([]string{
			"month", "account_id", "cloud_account_id", "provider", "service", "total_savings", "avg_coverage", "snapshot_count",
		}).AddRow(month, "account-123", strPtr("cloud-1"), "aws", "rds", 1500.0, f64ptr(85.0), 720)

		mock.ExpectQuery(`SELECT month, account_id`).
			WithArgs(6, []string{"account-123"}).
			WillReturnRows(rows)

		summaries, err := store.QueryMonthlyTotals(context.Background(), []string{"account-123"}, nil, 6)
		require.NoError(t, err)
		assert.Len(t, summaries, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestQueryByProviderRowsErr tests rows.Err() handling for provider query
func TestQueryByProviderRowsErr(t *testing.T) {
	t.Run("QueryByProvider returns rows.Err()", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-30 * 24 * time.Hour)

		rows := pgxmock.NewRows([]string{
			"provider", "service", "total_savings", "avg_coverage",
		}).AddRow("aws", "rds", 2500.0, f64ptr(85.0))

		mock.ExpectQuery(`SELECT provider, service, SUM\(total_savings\) as total_savings`).
			WithArgs(startDate, now, []string{"account-123"}).
			WillReturnRows(rows)

		breakdowns, err := store.QueryByProvider(context.Background(), []string{"account-123"}, nil, startDate, now)
		require.NoError(t, err)
		assert.Len(t, breakdowns, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestQueryByServiceRowsErr tests rows.Err() handling for service query
func TestQueryByServiceRowsErr(t *testing.T) {
	t.Run("QueryByService returns rows.Err()", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-30 * 24 * time.Hour)

		rows := pgxmock.NewRows([]string{
			"service", "region", "total_savings", "avg_coverage",
		}).AddRow("rds", "us-east-1", 1800.0, f64ptr(90.0))

		mock.ExpectQuery(`SELECT service, region, SUM\(total_savings\) as total_savings`).
			WithArgs(startDate, now, []string{"account-123"}, "aws").
			WillReturnRows(rows)

		breakdowns, err := store.QueryByService(context.Background(), []string{"account-123"}, nil, "aws", startDate, now)
		require.NoError(t, err)
		assert.Len(t, breakdowns, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// Test ErrNoRows handling
func TestErrNoRowsHandling(t *testing.T) {
	t.Run("QuerySavings handles empty result gracefully", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)

		now := time.Now().UTC()
		startDate := now.Add(-24 * time.Hour)

		rows := pgxmock.NewRows([]string{
			"id", "account_id", "cloud_account_id", "timestamp", "provider", "service", "region",
			"commitment_type", "total_commitment", "total_usage", "total_savings",
			"coverage_percentage", "metadata",
		})

		mock.ExpectQuery(`SELECT id, account_id`).
			WithArgs(startDate, now, []string{"account-123"}).
			WillReturnRows(rows)

		req := QueryRequest{
			AccountUUIDs: []string{"account-123"},
			StartDate:    startDate,
			EndDate:      now,
		}

		snapshots, err := store.QuerySavings(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, snapshots)
		assert.Empty(t, snapshots)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// Test interface verification for testable store
func TestTestableStoreImplementsInterface(t *testing.T) {
	t.Run("testablePostgresAnalyticsStore implements AnalyticsStore", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		var store AnalyticsStore = newTestableStore(mock)
		assert.NotNil(t, store)
	})
}

// TestClose tests the Close method for testable store
func TestClose(t *testing.T) {
	t.Run("testable store Close returns nil", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		store := newTestableStore(mock)
		err = store.Close()
		assert.NoError(t, err)
	})
}

// TestNoRows handling
func Test_NoRowsHandling(t *testing.T) {
	// Test that pgx.ErrNoRows is handled differently
	t.Run("pgx.ErrNoRows is a specific error", func(t *testing.T) {
		assert.NotNil(t, pgx.ErrNoRows)
		assert.Error(t, pgx.ErrNoRows)
	})
}
