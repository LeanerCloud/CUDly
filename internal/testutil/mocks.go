package testutil

import (
	"context"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
)

// MockScheduler is a mock implementation of server.SchedulerInterface
type MockScheduler struct {
	CollectRecommendationsFunc func(ctx context.Context) (*scheduler.CollectResult, error)
	ListRecommendationsFunc    func(ctx context.Context, filter config.RecommendationFilter) ([]config.RecommendationRecord, error)
}

func (m *MockScheduler) CollectRecommendations(ctx context.Context) (*scheduler.CollectResult, error) {
	if m.CollectRecommendationsFunc != nil {
		return m.CollectRecommendationsFunc(ctx)
	}
	return &scheduler.CollectResult{}, nil
}

func (m *MockScheduler) ListRecommendations(ctx context.Context, filter config.RecommendationFilter) ([]config.RecommendationRecord, error) {
	if m.ListRecommendationsFunc != nil {
		return m.ListRecommendationsFunc(ctx, filter)
	}
	return []config.RecommendationRecord{}, nil
}

// MockPurchaseManager is a mock implementation of server.PurchaseManagerInterface
type MockPurchaseManager struct {
	ProcessScheduledPurchasesFunc         func(ctx context.Context) (*purchase.ProcessResult, error)
	SendUpcomingPurchaseNotificationsFunc func(ctx context.Context) (*purchase.NotificationResult, error)
	ProcessMessageFunc                    func(ctx context.Context, body string) error
	ApproveExecutionFunc                  func(ctx context.Context, execID, token string) error
	CancelExecutionFunc                   func(ctx context.Context, execID, token string) error
}

func (m *MockPurchaseManager) ProcessScheduledPurchases(ctx context.Context) (*purchase.ProcessResult, error) {
	if m.ProcessScheduledPurchasesFunc != nil {
		return m.ProcessScheduledPurchasesFunc(ctx)
	}
	return &purchase.ProcessResult{}, nil
}

func (m *MockPurchaseManager) SendUpcomingPurchaseNotifications(ctx context.Context) (*purchase.NotificationResult, error) {
	if m.SendUpcomingPurchaseNotificationsFunc != nil {
		return m.SendUpcomingPurchaseNotificationsFunc(ctx)
	}
	return &purchase.NotificationResult{}, nil
}

func (m *MockPurchaseManager) ProcessMessage(ctx context.Context, body string) error {
	if m.ProcessMessageFunc != nil {
		return m.ProcessMessageFunc(ctx, body)
	}
	return nil
}

func (m *MockPurchaseManager) ApproveExecution(ctx context.Context, execID, token string) error {
	if m.ApproveExecutionFunc != nil {
		return m.ApproveExecutionFunc(ctx, execID, token)
	}
	return nil
}

func (m *MockPurchaseManager) CancelExecution(ctx context.Context, execID, token string) error {
	if m.CancelExecutionFunc != nil {
		return m.CancelExecutionFunc(ctx, execID, token)
	}
	return nil
}
