package server

import (
	"context"

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
	ApproveExecution(ctx context.Context, execID, token string) error
	CancelExecution(ctx context.Context, execID, token string) error
}

// AnalyticsStoreInterface defines the methods required for analytics storage
type AnalyticsStoreInterface interface {
	RefreshMaterializedViews(ctx context.Context) error
}
