package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PostgresAnalyticsStore implements AnalyticsStore using PostgreSQL
type PostgresAnalyticsStore struct {
	db *database.Connection
}

// NewPostgresAnalyticsStore creates a new PostgreSQL analytics store
func NewPostgresAnalyticsStore(db *database.Connection) *PostgresAnalyticsStore {
	return &PostgresAnalyticsStore{db: db}
}

// Verify PostgresAnalyticsStore implements AnalyticsStore
var _ AnalyticsStore = (*PostgresAnalyticsStore)(nil)

// ==========================================
// SNAPSHOT OPERATIONS
// ==========================================

// SaveSnapshot stores a single savings snapshot
func (s *PostgresAnalyticsStore) SaveSnapshot(ctx context.Context, snapshot *SavingsSnapshot) error {
	// Generate UUID if not provided
	if snapshot.ID == "" {
		snapshot.ID = uuid.New().String()
	}

	// Marshal metadata to JSONB
	var metadataJSON []byte
	var err error
	if snapshot.Metadata != nil {
		metadataJSON, err = json.Marshal(snapshot.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
	}

	query := `
		INSERT INTO savings_snapshots (
			id, account_id, timestamp, provider, service, region,
			commitment_type, total_commitment, total_usage, total_savings,
			coverage_percentage, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	_, err = s.db.Exec(ctx, query,
		snapshot.ID,
		snapshot.AccountID,
		snapshot.Timestamp,
		snapshot.Provider,
		snapshot.Service,
		snapshot.Region,
		snapshot.CommitmentType,
		snapshot.TotalCommitment,
		snapshot.TotalUsage,
		snapshot.TotalSavings,
		snapshot.CoveragePercentage,
		metadataJSON,
	)

	if err != nil {
		return fmt.Errorf("failed to save savings snapshot: %w", err)
	}

	return nil
}

// BulkInsertSnapshots inserts multiple snapshots efficiently
func (s *PostgresAnalyticsStore) BulkInsertSnapshots(ctx context.Context, snapshots []SavingsSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}

	// Use COPY for efficient bulk insert
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	// Prepare COPY statement
	_, err = conn.Conn().CopyFrom(
		ctx,
		pgx.Identifier{"savings_snapshots"},
		[]string{
			"id", "account_id", "timestamp", "provider", "service", "region",
			"commitment_type", "total_commitment", "total_usage", "total_savings",
			"coverage_percentage", "metadata",
		},
		pgx.CopyFromSlice(len(snapshots), func(i int) ([]any, error) {
			snapshot := snapshots[i]

			// Generate UUID if not provided
			if snapshot.ID == "" {
				snapshot.ID = uuid.New().String()
			}

			// Marshal metadata. Use []byte so pgx transmits it as a JSON value
			// for the jsonb column rather than as a bytea literal.
			var metadataJSON []byte
			if snapshot.Metadata != nil {
				data, err := json.Marshal(snapshot.Metadata)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal metadata for snapshot %d: %w", i, err)
				}
				metadataJSON = data
			}

			return []any{
				snapshot.ID,
				snapshot.AccountID,
				snapshot.Timestamp,
				snapshot.Provider,
				snapshot.Service,
				snapshot.Region,
				snapshot.CommitmentType,
				snapshot.TotalCommitment,
				snapshot.TotalUsage,
				snapshot.TotalSavings,
				snapshot.CoveragePercentage,
				metadataJSON,
			}, nil
		}),
	)

	if err != nil {
		return fmt.Errorf("failed to bulk insert snapshots: %w", err)
	}

	return nil
}

// QuerySavings retrieves savings snapshots based on query parameters
func (s *PostgresAnalyticsStore) QuerySavings(ctx context.Context, req QueryRequest) ([]SavingsSnapshot, error) {
	// Build query with optional filters
	query := `
		SELECT id, account_id, timestamp, provider, service, region,
		       commitment_type, total_commitment, total_usage, total_savings,
		       coverage_percentage, metadata
		FROM savings_snapshots
		WHERE account_id = $1
		  AND timestamp >= $2
		  AND timestamp <= $3
	`

	args := []any{req.AccountID, req.StartDate, req.EndDate}
	argIndex := 4

	// Add optional filters
	if req.Provider != "" {
		query += fmt.Sprintf(" AND provider = $%d", argIndex)
		args = append(args, req.Provider)
		argIndex++
	}

	if req.Service != "" {
		query += fmt.Sprintf(" AND service = $%d", argIndex)
		args = append(args, req.Service)
		argIndex++
	}

	query += " ORDER BY timestamp DESC"

	// Add limit
	if req.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, req.Limit)
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query savings: %w", err)
	}
	defer rows.Close()

	snapshots := make([]SavingsSnapshot, 0)
	for rows.Next() {
		var snapshot SavingsSnapshot
		var metadataJSON []byte

		err := rows.Scan(
			&snapshot.ID,
			&snapshot.AccountID,
			&snapshot.Timestamp,
			&snapshot.Provider,
			&snapshot.Service,
			&snapshot.Region,
			&snapshot.CommitmentType,
			&snapshot.TotalCommitment,
			&snapshot.TotalUsage,
			&snapshot.TotalSavings,
			&snapshot.CoveragePercentage,
			&metadataJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan snapshot: %w", err)
		}

		// Unmarshal metadata
		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &snapshot.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		snapshots = append(snapshots, snapshot)
	}

	return snapshots, rows.Err()
}

// ==========================================
// AGGREGATED QUERIES
// ==========================================

// QueryMonthlyTotals retrieves monthly aggregated totals
func (s *PostgresAnalyticsStore) QueryMonthlyTotals(ctx context.Context, accountID string, months int) ([]MonthlySummary, error) {
	query := `
		SELECT month, account_id, provider, service, total_savings, avg_coverage, snapshot_count
		FROM monthly_savings_summary
		WHERE account_id = $1
		  AND month >= DATE_TRUNC('month', NOW() - INTERVAL '1 month' * $2)
		ORDER BY month DESC, provider, service
	`

	rows, err := s.db.Query(ctx, query, accountID, months)
	if err != nil {
		return nil, fmt.Errorf("failed to query monthly totals: %w", err)
	}
	defer rows.Close()

	summaries := make([]MonthlySummary, 0)
	for rows.Next() {
		var summary MonthlySummary
		err := rows.Scan(
			&summary.Month,
			&summary.AccountID,
			&summary.Provider,
			&summary.Service,
			&summary.TotalSavings,
			&summary.AvgCoverage,
			&summary.SnapshotCount,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan monthly summary: %w", err)
		}
		summaries = append(summaries, summary)
	}

	return summaries, rows.Err()
}

// QueryByProvider retrieves savings breakdown by provider
func (s *PostgresAnalyticsStore) QueryByProvider(ctx context.Context, accountID string, startDate, endDate time.Time) ([]ProviderBreakdown, error) {
	query := `
		SELECT provider, service, SUM(total_savings) as total_savings, AVG(coverage_percentage) as avg_coverage
		FROM savings_snapshots
		WHERE account_id = $1
		  AND timestamp >= $2
		  AND timestamp <= $3
		GROUP BY provider, service
		ORDER BY total_savings DESC
	`

	rows, err := s.db.Query(ctx, query, accountID, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to query by provider: %w", err)
	}
	defer rows.Close()

	breakdowns := make([]ProviderBreakdown, 0)
	for rows.Next() {
		var breakdown ProviderBreakdown
		err := rows.Scan(
			&breakdown.Provider,
			&breakdown.Service,
			&breakdown.TotalSavings,
			&breakdown.AvgCoverage,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan provider breakdown: %w", err)
		}
		breakdowns = append(breakdowns, breakdown)
	}

	return breakdowns, rows.Err()
}

// QueryByService retrieves savings breakdown by service
func (s *PostgresAnalyticsStore) QueryByService(ctx context.Context, accountID string, provider string, startDate, endDate time.Time) ([]ServiceBreakdown, error) {
	query := `
		SELECT service, region, SUM(total_savings) as total_savings, AVG(coverage_percentage) as avg_coverage
		FROM savings_snapshots
		WHERE account_id = $1
		  AND provider = $2
		  AND timestamp >= $3
		  AND timestamp <= $4
		GROUP BY service, region
		ORDER BY total_savings DESC
	`

	rows, err := s.db.Query(ctx, query, accountID, provider, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to query by service: %w", err)
	}
	defer rows.Close()

	breakdowns := make([]ServiceBreakdown, 0)
	for rows.Next() {
		var breakdown ServiceBreakdown
		err := rows.Scan(
			&breakdown.Service,
			&breakdown.Region,
			&breakdown.TotalSavings,
			&breakdown.AvgCoverage,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan service breakdown: %w", err)
		}
		breakdowns = append(breakdowns, breakdown)
	}

	return breakdowns, rows.Err()
}

// ==========================================
// PARTITION MANAGEMENT
// ==========================================

// CreatePartition creates a partition for a specific month
func (s *PostgresAnalyticsStore) CreatePartition(ctx context.Context, forMonth time.Time) error {
	query := `SELECT create_savings_snapshot_partition($1)`

	_, err := s.db.Exec(ctx, query, forMonth)
	if err != nil {
		return fmt.Errorf("failed to create partition: %w", err)
	}

	return nil
}

// DropOldPartitions removes partitions older than retention period
func (s *PostgresAnalyticsStore) DropOldPartitions(ctx context.Context, retentionMonths int) error {
	query := `SELECT drop_old_savings_partitions($1)`

	_, err := s.db.Exec(ctx, query, retentionMonths)
	if err != nil {
		return fmt.Errorf("failed to drop old partitions: %w", err)
	}

	return nil
}

// CreatePartitionsForRange creates partitions for a date range (used during migration)
func (s *PostgresAnalyticsStore) CreatePartitionsForRange(ctx context.Context, startDate, endDate time.Time) error {
	// Create partition for each month in the range
	current := time.Date(startDate.Year(), startDate.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(endDate.Year(), endDate.Month(), 1, 0, 0, 0, 0, time.UTC)

	for !current.After(end) {
		if err := s.CreatePartition(ctx, current); err != nil {
			return fmt.Errorf("failed to create partition for %v: %w", current, err)
		}
		current = current.AddDate(0, 1, 0)
	}

	return nil
}

// ==========================================
// MATERIALIZED VIEW MANAGEMENT
// ==========================================

// RefreshMaterializedViews refreshes all analytics materialized views
func (s *PostgresAnalyticsStore) RefreshMaterializedViews(ctx context.Context) error {
	query := `SELECT refresh_savings_materialized_views()`

	_, err := s.db.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to refresh materialized views: %w", err)
	}

	return nil
}

// ==========================================
// CLEANUP
// ==========================================

// Close cleans up resources (no-op for PostgreSQL store)
func (s *PostgresAnalyticsStore) Close() error {
	return nil
}
