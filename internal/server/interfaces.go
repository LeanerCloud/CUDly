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

// AnalyticsStoreInterface defines the methods required for analytics storage
type AnalyticsStoreInterface interface {
	RefreshMaterializedViews(ctx context.Context) error
}
