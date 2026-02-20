package analytics

import (
	"context"
	"time"
)

// SavingsSnapshot represents a single savings data point
type SavingsSnapshot struct {
	ID                 string         `json:"id"`
	AccountID          string         `json:"account_id"`
	Timestamp          time.Time      `json:"timestamp"`
	Provider           string         `json:"provider"`
	Service            string         `json:"service"`
	Region             string         `json:"region"`
	CommitmentType     string         `json:"commitment_type"` // "RI" or "SavingsPlan"
	TotalCommitment    float64        `json:"total_commitment"`
	TotalUsage         float64        `json:"total_usage"`
	TotalSavings       float64        `json:"total_savings"`
	CoveragePercentage float64        `json:"coverage_percentage"`
	Metadata           map[string]any `json:"metadata,omitempty"`
}

// QueryRequest defines parameters for querying savings data
type QueryRequest struct {
	AccountID string
	Provider  string // optional filter
	Service   string // optional filter
	StartDate time.Time
	EndDate   time.Time
	Limit     int
}

// MonthlySummary represents aggregated monthly savings
type MonthlySummary struct {
	Month         time.Time `json:"month"`
	AccountID     string    `json:"account_id"`
	Provider      string    `json:"provider"`
	Service       string    `json:"service"`
	TotalSavings  float64   `json:"total_savings"`
	AvgCoverage   float64   `json:"avg_coverage"`
	SnapshotCount int       `json:"snapshot_count"`
}

// ProviderBreakdown represents savings breakdown by provider
type ProviderBreakdown struct {
	Provider     string  `json:"provider"`
	Service      string  `json:"service"`
	TotalSavings float64 `json:"total_savings"`
	AvgCoverage  float64 `json:"avg_coverage"`
}

// ServiceBreakdown represents savings breakdown by service
type ServiceBreakdown struct {
	Service      string  `json:"service"`
	Region       string  `json:"region"`
	TotalSavings float64 `json:"total_savings"`
	AvgCoverage  float64 `json:"avg_coverage"`
}

// AnalyticsStore defines the interface for analytics storage
type AnalyticsStore interface {
	// SaveSnapshot stores a single savings snapshot
	SaveSnapshot(ctx context.Context, snapshot *SavingsSnapshot) error

	// BulkInsertSnapshots inserts multiple snapshots efficiently (for migrations)
	BulkInsertSnapshots(ctx context.Context, snapshots []SavingsSnapshot) error

	// QuerySavings retrieves savings snapshots based on query parameters
	QuerySavings(ctx context.Context, req QueryRequest) ([]SavingsSnapshot, error)

	// Aggregated queries (using materialized views for performance)
	QueryMonthlyTotals(ctx context.Context, accountID string, months int) ([]MonthlySummary, error)
	QueryByProvider(ctx context.Context, accountID string, startDate, endDate time.Time) ([]ProviderBreakdown, error)
	QueryByService(ctx context.Context, accountID string, provider string, startDate, endDate time.Time) ([]ServiceBreakdown, error)

	// Partition management
	CreatePartition(ctx context.Context, forMonth time.Time) error
	DropOldPartitions(ctx context.Context, retentionMonths int) error
	CreatePartitionsForRange(ctx context.Context, startDate, endDate time.Time) error

	// Materialized view management
	RefreshMaterializedViews(ctx context.Context) error

	// Close cleans up resources
	Close() error
}
