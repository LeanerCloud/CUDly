// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// analyticsDBConn is the minimal interface used by PostgresAnalyticsClient.
// Both *database.Connection and pgxmock.PgxPoolIface satisfy it so tests
// can drop in a mock without needing a real Postgres.
type analyticsDBConn interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// PostgresAnalyticsClient implements AnalyticsClientInterface by aggregating
// the purchase_history table. It replaces the legacy S3/Athena-backed
// client — all purchase history is now written to Postgres and we want
// the analytics endpoints to serve the same shape without a second
// storage layer.
type PostgresAnalyticsClient struct {
	db analyticsDBConn
}

// NewPostgresAnalyticsClient creates a new Postgres-backed analytics client.
func NewPostgresAnalyticsClient(db *database.Connection) *PostgresAnalyticsClient {
	return &PostgresAnalyticsClient{db: db}
}

// Verify PostgresAnalyticsClient implements AnalyticsClientInterface.
var _ AnalyticsClientInterface = (*PostgresAnalyticsClient)(nil)

// intervalToTruncUnit maps the caller-facing interval names to the
// corresponding Postgres date_trunc() unit. We allowlist the values to
// defend against SQL injection since the unit is interpolated directly
// (date_trunc's first argument doesn't accept parameter binding on
// most drivers).
func intervalToTruncUnit(interval string) (string, error) {
	switch interval {
	case "hourly":
		return "hour", nil
	case "daily", "":
		return "day", nil
	case "weekly":
		return "week", nil
	case "monthly":
		return "month", nil
	default:
		return "", fmt.Errorf("unsupported interval %q", interval)
	}
}

// dimensionToColumn maps the caller-facing dimension to the underlying
// purchase_history column. Allowlisted for the same reason as
// intervalToTruncUnit.
func dimensionToColumn(dimension string) (string, error) {
	switch dimension {
	case "service", "":
		return "service", nil
	case "provider":
		return "provider", nil
	case "region":
		return "region", nil
	case "account":
		return "account_id", nil
	default:
		return "", fmt.Errorf("unsupported dimension %q", dimension)
	}
}

// QueryHistory aggregates purchase_history rows bucketed by interval.
// accountID == "" means "all accounts accessible to the caller"; scoping
// is enforced upstream in the handler. Returns data points in ascending
// order and a summary covering the full window.
func (c *PostgresAnalyticsClient) QueryHistory(
	ctx context.Context,
	accountID string,
	start, end time.Time,
	interval string,
) ([]HistoryDataPoint, *HistorySummary, error) {
	unit, err := intervalToTruncUnit(interval)
	if err != nil {
		return nil, nil, err
	}

	// Single query grouped by (bucket, service, provider) so we can assemble
	// both the top-line bucket totals and the by_service / by_provider
	// breakdowns without a second trip to the DB.
	//
	// #nosec G201 — `unit` is allowlisted by intervalToTruncUnit above.
	query := fmt.Sprintf(`
		SELECT date_trunc('%s', timestamp) AS bucket,
		       service,
		       provider,
		       SUM(estimated_savings)::float8 AS savings,
		       SUM(upfront_cost)::float8 AS upfront,
		       COUNT(*) AS purchases
		FROM purchase_history
		WHERE timestamp >= $1
		  AND timestamp <= $2
		  AND ($3 = '' OR account_id = $3)
		GROUP BY bucket, service, provider
		ORDER BY bucket ASC
	`, unit)

	rows, err := c.db.Query(ctx, query, start, end, accountID)
	if err != nil {
		return nil, nil, fmt.Errorf("query purchase_history: %w", err)
	}
	defer rows.Close()

	// Fold rows into bucket → HistoryDataPoint with per-service/provider maps.
	bucketIndex := make(map[time.Time]*HistoryDataPoint)
	var bucketOrder []time.Time
	summary := &HistorySummary{}

	for rows.Next() {
		var bucket time.Time
		var service, provider string
		var savings, upfront float64
		var purchases int
		if err := rows.Scan(&bucket, &service, &provider, &savings, &upfront, &purchases); err != nil {
			return nil, nil, fmt.Errorf("scan purchase_history row: %w", err)
		}

		dp, ok := bucketIndex[bucket]
		if !ok {
			dp = &HistoryDataPoint{
				Timestamp:  bucket,
				ByService:  make(map[string]float64),
				ByProvider: make(map[string]float64),
			}
			bucketIndex[bucket] = dp
			bucketOrder = append(bucketOrder, bucket)
		}
		dp.TotalSavings += savings
		dp.TotalUpfront += upfront
		dp.PurchaseCount += purchases
		dp.ByService[service] += savings
		dp.ByProvider[provider] += savings

		summary.TotalUpfront += upfront
		summary.TotalMonthlySavings += savings
		summary.TotalPurchases += purchases
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate purchase_history rows: %w", err)
	}

	// Build ordered slice and compute the running cumulative savings.
	dataPoints := make([]HistoryDataPoint, 0, len(bucketOrder))
	var cumulative float64
	for _, b := range bucketOrder {
		dp := bucketIndex[b]
		cumulative += dp.TotalSavings
		dp.CumulativeSavings = cumulative
		dataPoints = append(dataPoints, *dp)
	}

	// Rows in purchase_history are all considered completed (the table is
	// written only after a successful purchase), so TotalCompleted mirrors
	// TotalPurchases and the other state counters stay zero.
	summary.TotalCompleted = summary.TotalPurchases
	summary.TotalAnnualSavings = summary.TotalMonthlySavings * 12

	return dataPoints, summary, nil
}

// QueryBreakdown groups purchase_history by dimension (service, provider,
// region, account) and returns totals + percentage-of-total-savings per
// bucket.
func (c *PostgresAnalyticsClient) QueryBreakdown(
	ctx context.Context,
	accountID string,
	start, end time.Time,
	dimension string,
) (map[string]BreakdownValue, error) {
	column, err := dimensionToColumn(dimension)
	if err != nil {
		return nil, err
	}

	// #nosec G201 — `column` is allowlisted by dimensionToColumn above.
	query := fmt.Sprintf(`
		SELECT %s AS bucket,
		       SUM(estimated_savings)::float8 AS savings,
		       SUM(upfront_cost)::float8 AS upfront,
		       COUNT(*) AS purchases
		FROM purchase_history
		WHERE timestamp >= $1
		  AND timestamp <= $2
		  AND ($3 = '' OR account_id = $3)
		GROUP BY bucket
		ORDER BY savings DESC
	`, column)

	rows, err := c.db.Query(ctx, query, start, end, accountID)
	if err != nil {
		return nil, fmt.Errorf("query breakdown: %w", err)
	}
	defer rows.Close()

	type rawRow struct {
		bucket    string
		savings   float64
		upfront   float64
		purchases int
	}
	var raws []rawRow
	var totalSavings float64
	for rows.Next() {
		var r rawRow
		if err := rows.Scan(&r.bucket, &r.savings, &r.upfront, &r.purchases); err != nil {
			return nil, fmt.Errorf("scan breakdown row: %w", err)
		}
		raws = append(raws, r)
		totalSavings += r.savings
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate breakdown rows: %w", err)
	}

	result := make(map[string]BreakdownValue, len(raws))
	for _, r := range raws {
		pct := 0.0
		if totalSavings > 0 {
			pct = (r.savings / totalSavings) * 100.0
		}
		result[r.bucket] = BreakdownValue{
			TotalSavings:  r.savings,
			TotalUpfront:  r.upfront,
			PurchaseCount: r.purchases,
			Percentage:    pct,
		}
	}
	return result, nil
}
