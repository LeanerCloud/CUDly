package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/LeanerCloud/CUDly/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dbConn is the minimal interface used by PostgresAnalyticsStore.
// Both *database.Connection and pgxmock.PgxPoolIface satisfy this interface.
type dbConn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Acquire(ctx context.Context) (*pgxpool.Conn, error)
}

// PostgresAnalyticsStore implements AnalyticsStore using PostgreSQL.
type PostgresAnalyticsStore struct {
	db dbConn
}

// NewPostgresAnalyticsStore creates a new PostgreSQL analytics store.
func NewPostgresAnalyticsStore(db *database.Connection) *PostgresAnalyticsStore {
	return &PostgresAnalyticsStore{db: db}
}

// Verify PostgresAnalyticsStore implements Store.
var _ Store = (*PostgresAnalyticsStore)(nil)

// accountFilterClause builds the dual-column account WHERE fragment plus the
// full positional arg list for the analytics queries. baseArgs holds the fixed
// leading binds; the account array binds (if any) are appended after them and
// the returned clause references them by the right positions.
//
// savings_snapshots carries two account identifiers, either of which may be the
// only one populated on a row: account_id (the cloud-provider external number)
// and cloud_account_id (the cloud_accounts UUID FK, NULL on the AWS ambient and
// legacy rows). Matching only one column silently drops rows that carry only the
// other, so we OR both, with the external-id half grouped per provider so a
// reused external number across providers cannot leak the wrong rows. This
// mirrors api.accountFilterClause on the live purchase_history path
// (issue #701/#498/#866). Both empty -> "TRUE" (caller must enforce scope).
func accountFilterClause(accountUUIDs []string, accountExternalIDsByProvider map[string][]string, baseArgs []any) (clause string, args []any) {
	args = baseArgs
	var ors []string
	if len(accountUUIDs) > 0 {
		args = append(args, accountUUIDs)
		ors = append(ors, fmt.Sprintf("cloud_account_id = ANY($%d)", len(args)))
	}
	for _, provider := range sortedProviderKeys(accountExternalIDsByProvider) {
		exts := accountExternalIDsByProvider[provider]
		if len(exts) == 0 {
			continue
		}
		if provider == "" {
			args = append(args, exts)
			ors = append(ors, fmt.Sprintf("account_id = ANY($%d)", len(args)))
			continue
		}
		args = append(args, provider)
		providerArg := len(args)
		args = append(args, exts)
		ors = append(ors, fmt.Sprintf("(provider = $%d AND account_id = ANY($%d))", providerArg, len(args)))
	}
	if len(ors) == 0 {
		return "TRUE", args
	}
	return "(" + strings.Join(ors, " OR ") + ")", args
}

// sortedProviderKeys returns the map keys in ascending order so generated SQL
// (and its bind-arg ordering) is deterministic and testable.
func sortedProviderKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ==========================================
// SNAPSHOT OPERATIONS
// ==========================================

// SaveSnapshot stores a single savings snapshot.
func (s *PostgresAnalyticsStore) SaveSnapshot(ctx context.Context, snapshot *SavingsSnapshot) error {
	if err := validateCommitmentType(snapshot.CommitmentType); err != nil {
		return fmt.Errorf("invalid savings snapshot: %w", err)
	}
	if snapshot.ID == "" {
		snapshot.ID = uuid.New().String()
	}

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
			id, account_id, cloud_account_id, timestamp, provider, service, region,
			commitment_type, total_commitment, total_usage, total_savings,
			coverage_percentage, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`

	_, err = s.db.Exec(ctx, query,
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
		metadataJSON,
	)
	if err != nil {
		return fmt.Errorf("failed to save savings snapshot: %w", err)
	}
	return nil
}

// validateCommitmentType rejects a commitment_type that the savings_snapshots
// table CHECK constraint would reject. Extracted so the guard is unit-testable
// directly (the COPY path acquires a real pooled connection that pgxmock can't
// stand in for, so an end-to-end test can only prove the acquire-failure path).
func validateCommitmentType(commitmentType string) error {
	if commitmentType != "RI" && commitmentType != "SavingsPlan" {
		return fmt.Errorf("invalid commitment_type %q (want RI or SavingsPlan)", commitmentType)
	}
	return nil
}

// BulkInsertSnapshots inserts multiple snapshots efficiently via COPY.
func (s *PostgresAnalyticsStore) BulkInsertSnapshots(ctx context.Context, snapshots []SavingsSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}

	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	_, err = conn.Conn().CopyFrom(
		ctx,
		pgx.Identifier{"savings_snapshots"},
		[]string{
			"id", "account_id", "cloud_account_id", "timestamp", "provider", "service", "region",
			"commitment_type", "total_commitment", "total_usage", "total_savings",
			"coverage_percentage", "metadata",
		},
		pgx.CopyFromSlice(len(snapshots), func(i int) ([]any, error) {
			snapshot := snapshots[i]

			if snapshot.ID == "" {
				snapshot.ID = uuid.New().String()
			}

			// Validate commitment_type against the table CHECK before COPY so a
			// single bad value doesn't abort the entire batch server-side (L4).
			if valErr := validateCommitmentType(snapshot.CommitmentType); valErr != nil {
				return nil, fmt.Errorf("snapshot %d: %w", i, valErr)
			}

			// Marshal metadata as []byte so pgx transmits it as a JSON value for
			// the jsonb column rather than as a bytea literal.
			var metadataJSON []byte
			if snapshot.Metadata != nil {
				data, marshalErr := json.Marshal(snapshot.Metadata)
				if marshalErr != nil {
					return nil, fmt.Errorf("failed to marshal metadata for snapshot %d: %w", i, marshalErr)
				}
				metadataJSON = data
			}

			return []any{
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
				metadataJSON,
			}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("failed to bulk insert snapshots: %w", err)
	}
	return nil
}

// QuerySavings retrieves savings snapshots based on query parameters.
func (s *PostgresAnalyticsStore) QuerySavings(ctx context.Context, req *QueryRequest) ([]SavingsSnapshot, error) {
	accountClause, args := accountFilterClause(req.AccountUUIDs, req.AccountExternalIDsByProvider, []any{req.StartDate, req.EndDate})

	// #nosec G201 — accountClause references only parameter placeholders built
	// internally; the optional provider/service filters below are also bound.
	query := `
		SELECT id, account_id, cloud_account_id, timestamp, provider, service, region,
		       commitment_type, total_commitment, total_usage, total_savings,
		       coverage_percentage, metadata
		FROM savings_snapshots
		WHERE timestamp >= $1
		  AND timestamp <= $2
		  AND ` + accountClause

	if req.Provider != "" {
		args = append(args, req.Provider)
		query += fmt.Sprintf(" AND provider = $%d", len(args))
	}
	if req.Service != "" {
		args = append(args, req.Service)
		query += fmt.Sprintf(" AND service = $%d", len(args))
	}

	query += " ORDER BY timestamp DESC"

	if req.Limit > 0 {
		args = append(args, req.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
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
			&snapshot.CloudAccountID,
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

// QueryMonthlyTotals retrieves monthly aggregated totals for the last N months
// (inclusive of the current month). months <= 0 returns no rows.
func (s *PostgresAnalyticsStore) QueryMonthlyTotals(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string, months int) ([]MonthlySummary, error) {
	if months <= 0 {
		return []MonthlySummary{}, nil
	}
	// Last N inclusive months: floor(now) back N-1 months. make_interval avoids
	// the INTERVAL '1 month' * N off-by-one (M1).
	accountClause, args := accountFilterClause(accountUUIDs, accountExternalIDsByProvider, []any{months})

	// #nosec G201 — accountClause uses only internally-built placeholders.
	query := `
		SELECT month, account_id, cloud_account_id, provider, service, total_savings, avg_coverage, snapshot_count
		FROM monthly_savings_summary
		WHERE month >= DATE_TRUNC('month', NOW()) - make_interval(months => $1 - 1)
		  AND ` + accountClause + `
		ORDER BY month DESC, provider, service
	`

	rows, err := s.db.Query(ctx, query, args...)
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
			&summary.CloudAccountID,
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

// QueryByProvider retrieves savings breakdown by provider/service.
func (s *PostgresAnalyticsStore) QueryByProvider(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string, startDate, endDate time.Time) ([]ProviderBreakdown, error) {
	accountClause, args := accountFilterClause(accountUUIDs, accountExternalIDsByProvider, []any{startDate, endDate})

	// #nosec G201 — accountClause uses only internally-built placeholders.
	query := `
		SELECT provider, service, AVG(total_savings) as total_savings, AVG(coverage_percentage) as avg_coverage
		FROM savings_snapshots
		WHERE timestamp >= $1
		  AND timestamp <= $2
		  AND ` + accountClause + `
		GROUP BY provider, service
		ORDER BY total_savings DESC
	`

	rows, err := s.db.Query(ctx, query, args...)
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

// QueryByService retrieves savings breakdown by service/region, optionally
// filtered to a single provider. An empty provider returns all providers'
// services.
func (s *PostgresAnalyticsStore) QueryByService(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string, provider string, startDate, endDate time.Time) ([]ServiceBreakdown, error) {
	accountClause, args := accountFilterClause(accountUUIDs, accountExternalIDsByProvider, []any{startDate, endDate})

	providerClause := ""
	if provider != "" {
		args = append(args, provider)
		providerClause = fmt.Sprintf(" AND provider = $%d", len(args))
	}

	// #nosec G201 — accountClause / providerClause use only internally-built placeholders.
	query := fmt.Sprintf(`
		SELECT service, region, AVG(total_savings) as total_savings, AVG(coverage_percentage) as avg_coverage
		FROM savings_snapshots
		WHERE timestamp >= $1
		  AND timestamp <= $2
		  AND %s%s
		GROUP BY service, region
		ORDER BY total_savings DESC
	`, accountClause, providerClause)

	rows, err := s.db.Query(ctx, query, args...)
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

// CreatePartition creates a partition for a specific month.
func (s *PostgresAnalyticsStore) CreatePartition(ctx context.Context, forMonth time.Time) error {
	if _, err := s.db.Exec(ctx, `SELECT create_savings_snapshot_partition($1)`, forMonth); err != nil {
		return fmt.Errorf("failed to create partition: %w", err)
	}
	return nil
}

// CreateFuturePartitions ensures partitions exist for the current month plus
// monthsAhead months ahead via the create_future_savings_partitions SQL helper.
func (s *PostgresAnalyticsStore) CreateFuturePartitions(ctx context.Context, monthsAhead int) error {
	if monthsAhead < 0 {
		return fmt.Errorf("monthsAhead must be >= 0, got %d", monthsAhead)
	}
	if _, err := s.db.Exec(ctx, `SELECT create_future_savings_partitions($1)`, monthsAhead); err != nil {
		return fmt.Errorf("failed to create future partitions: %w", err)
	}
	return nil
}

// DropOldPartitions removes partitions older than the retention period.
func (s *PostgresAnalyticsStore) DropOldPartitions(ctx context.Context, retentionMonths int) error {
	if retentionMonths <= 0 {
		return fmt.Errorf("retentionMonths must be > 0, got %d", retentionMonths)
	}
	if _, err := s.db.Exec(ctx, `SELECT drop_old_savings_partitions($1)`, retentionMonths); err != nil {
		return fmt.Errorf("failed to drop old partitions: %w", err)
	}
	return nil
}

// CreatePartitionsForRange creates partitions for each month in a date range
// (used during backfill / migration).
func (s *PostgresAnalyticsStore) CreatePartitionsForRange(ctx context.Context, startDate, endDate time.Time) error {
	if startDate.After(endDate) {
		return fmt.Errorf("startDate %v must not be after endDate %v", startDate, endDate)
	}
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

// RefreshMaterializedViews refreshes all analytics materialized views.
func (s *PostgresAnalyticsStore) RefreshMaterializedViews(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, `SELECT refresh_savings_materialized_views()`); err != nil {
		return fmt.Errorf("failed to refresh materialized views: %w", err)
	}
	return nil
}

// ==========================================
// CLEANUP
// ==========================================

// Close cleans up resources (no-op for PostgreSQL store).
func (s *PostgresAnalyticsStore) Close() error {
	return nil
}
