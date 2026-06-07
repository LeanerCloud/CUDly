package server

import (
	"context"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
)

// SchedulerInterface defines the methods required for the scheduler component
type SchedulerInterface interface {
	CollectRecommendations(ctx context.Context) (*scheduler.CollectResult, error)
	ListRecommendations(ctx context.Context, filter config.RecommendationFilter) ([]config.RecommendationRecord, error)
	// GetRecommendationByID fetches a single rec by application-level id,
	// bypassing account-override filtering. hiddenBy is non-nil when the rec
	// exists but would be dropped by the override filter. Returns nil, nil,
	// nil when absent or fully suppressed.
	GetRecommendationByID(ctx context.Context, id string) (rec *config.RecommendationRecord, hiddenBy []string, err error)
}

// PurchaseManagerInterface defines the methods required for the purchase manager component
type PurchaseManagerInterface interface {
	ProcessScheduledPurchases(ctx context.Context) (*purchase.ProcessResult, error)
	SendUpcomingPurchaseNotifications(ctx context.Context) (*purchase.NotificationResult, error)
	ProcessMessage(ctx context.Context, body string) error
	ApproveExecution(ctx context.Context, execID, token, actor string) error
	ApproveAndExecute(ctx context.Context, execID, actor string) error
	CancelExecution(ctx context.Context, execID, token, actor string) error
	// ReapStuckExecutions sweeps purchase_executions stuck in
	// approved/running longer than reapAfter and flips them to "failed"
	// via the existing TransitionExecutionStatus CAS. Wired into the
	// "reap_stuck_purchases" scheduled task. See issue #678.
	ReapStuckExecutions(ctx context.Context, reapAfter time.Duration) (*purchase.ReapResult, error)
}

// AnalyticsStoreInterface defines the methods required for analytics storage.
// Beyond the materialized-view refresh, the scheduled analytics task also keeps
// monthly partitions provisioned ahead of time and applies retention.
type AnalyticsStoreInterface interface {
	RefreshMaterializedViews(ctx context.Context) error
	// CreateFuturePartitions ensures partitions exist for the current month
	// plus monthsAhead months ahead (M3: partitions otherwise stop after the
	// seeded months and every insert falls into the catch-all default).
	CreateFuturePartitions(ctx context.Context, monthsAhead int) error
	// DropOldPartitions drops partitions older than retentionMonths (retention).
	DropOldPartitions(ctx context.Context, retentionMonths int) error
}

// AnalyticsCollectorInterface aggregates current savings into a point-in-time
// snapshot row per (tenant, provider, service, region, commitment_type) bucket.
type AnalyticsCollectorInterface interface {
	Collect(ctx context.Context) error
}
