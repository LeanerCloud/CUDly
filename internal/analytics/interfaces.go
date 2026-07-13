package analytics

import (
	"context"
	"time"
)

// SavingsSnapshot represents a single savings data point.
//
// Tenant key: CloudAccountID (the cloud_accounts UUID FK) is the multi-tenant
// scoping key, mirroring purchase_history / purchase_executions. AccountID is
// the cloud-provider external account string (AWS account number, Azure
// subscription id, GCP project id), kept as a descriptive attribute. A row may
// carry only one of them populated (CloudAccountID is NULL on the AWS ambient-
// credentials path and on legacy rows), so both are written when available.
type SavingsSnapshot struct {
	Timestamp          time.Time      `json:"timestamp"`
	TotalUsage         *float64       `json:"total_usage,omitempty"`
	CloudAccountID     *string        `json:"cloud_account_id,omitempty"`
	CoveragePercentage *float64       `json:"coverage_percentage,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
	AccountID          string         `json:"account_id"`
	Provider           string         `json:"provider"`
	Service            string         `json:"service"`
	Region             string         `json:"region"`
	CommitmentType     string         `json:"commitment_type"`
	ID                 string         `json:"id"`
	TotalCommitment    float64        `json:"total_commitment"`
	TotalSavings       float64        `json:"total_savings"`
}

// QueryRequest defines parameters for querying savings data.
//
// Scoping uses the same dual-column model as the live purchase_history path:
// rows match when cloud_account_id = ANY(AccountUUIDs) OR (provider = p AND
// account_id = ANY(AccountExternalIDsByProvider[p])). Both nil/empty means
// "all accounts accessible to the caller" — the caller MUST enforce scoping
// upstream before passing empty filters.
type QueryRequest struct {
	StartDate                    time.Time
	EndDate                      time.Time
	AccountExternalIDsByProvider map[string][]string
	Provider                     string
	Service                      string
	AccountUUIDs                 []string
	Limit                        int
}

// MonthlySummary represents aggregated monthly savings.
type MonthlySummary struct {
	Month          time.Time `json:"month"`
	CloudAccountID *string   `json:"cloud_account_id,omitempty"`
	AvgCoverage    *float64  `json:"avg_coverage,omitempty"`
	AccountID      string    `json:"account_id"`
	Provider       string    `json:"provider"`
	Service        string    `json:"service"`
	TotalSavings   float64   `json:"total_savings"`
	SnapshotCount  int       `json:"snapshot_count"`
}

// ProviderBreakdown represents savings breakdown by provider.
type ProviderBreakdown struct {
	AvgCoverage  *float64 `json:"avg_coverage,omitempty"`
	Provider     string   `json:"provider"`
	Service      string   `json:"service"`
	TotalSavings float64  `json:"total_savings"`
}

// ServiceBreakdown represents savings breakdown by service.
type ServiceBreakdown struct {
	AvgCoverage  *float64 `json:"avg_coverage,omitempty"`
	Service      string   `json:"service"`
	Region       string   `json:"region"`
	TotalSavings float64  `json:"total_savings"`
}

// AnalyticsStore defines the interface for analytics storage.
type AnalyticsStore interface { //nolint:revive // exported: doc comment style intentional
	// SaveSnapshot stores a single savings snapshot.
	SaveSnapshot(ctx context.Context, snapshot *SavingsSnapshot) error

	// BulkInsertSnapshots inserts multiple snapshots efficiently.
	BulkInsertSnapshots(ctx context.Context, snapshots []SavingsSnapshot) error

	// QuerySavings retrieves savings snapshots based on query parameters.
	QuerySavings(ctx context.Context, req QueryRequest) ([]SavingsSnapshot, error)

	// Aggregated queries (using materialized views for performance). Scoping is
	// the dual-column model; pass empty filters only after enforcing scope.
	QueryMonthlyTotals(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string, months int) ([]MonthlySummary, error)
	QueryByProvider(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string, startDate, endDate time.Time) ([]ProviderBreakdown, error)
	QueryByService(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string, provider string, startDate, endDate time.Time) ([]ServiceBreakdown, error)

	// Partition management.
	CreatePartition(ctx context.Context, forMonth time.Time) error
	CreateFuturePartitions(ctx context.Context, monthsAhead int) error
	DropOldPartitions(ctx context.Context, retentionMonths int) error
	CreatePartitionsForRange(ctx context.Context, startDate, endDate time.Time) error

	// Materialized view management.
	RefreshMaterializedViews(ctx context.Context) error

	// Close cleans up resources.
	Close() error
}
