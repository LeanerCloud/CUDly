package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
)

// ScheduledTaskType represents different types of scheduled tasks
type ScheduledTaskType string

const (
	TaskCollectRecommendations    ScheduledTaskType = "collect_recommendations"
	TaskProcessScheduledPurchases ScheduledTaskType = "process_scheduled_purchases"
	TaskSendNotifications         ScheduledTaskType = "send_notifications"
	TaskCleanupExpiredRecords     ScheduledTaskType = "cleanup"
	TaskRefreshAnalytics          ScheduledTaskType = "analytics_refresh"
	TaskRIExchangeReshape         ScheduledTaskType = "ri_exchange_reshape"
)

// HandleScheduledTask processes a scheduled task by type
func (app *Application) HandleScheduledTask(ctx context.Context, taskType ScheduledTaskType) (any, error) {
	log.Printf("Handling scheduled task: %s", taskType)

	switch taskType {
	case TaskCollectRecommendations:
		return app.handleCollectRecommendations(ctx)
	case TaskProcessScheduledPurchases:
		return app.handleProcessScheduledPurchases(ctx)
	case TaskSendNotifications:
		return app.handleSendNotifications(ctx)
	case TaskCleanupExpiredRecords:
		return app.handleCleanupExpiredRecords(ctx)
	case TaskRefreshAnalytics:
		return app.handleRefreshAnalytics(ctx)
	case TaskRIExchangeReshape:
		return app.handleRIExchangeReshape(ctx)
	default:
		return nil, fmt.Errorf("unknown scheduled task type: %s", taskType)
	}
}

// handleCollectRecommendations collects cost optimization recommendations
func (app *Application) handleCollectRecommendations(ctx context.Context) (*scheduler.CollectResult, error) {
	log.Println("Collecting recommendations...")
	result, err := app.Scheduler.CollectRecommendations(ctx)
	if err != nil {
		log.Printf("Failed to collect recommendations: %v", err)
		return nil, err
	}
	log.Printf("Recommendations collected: %d total, savings: $%.2f", result.Recommendations, result.TotalSavings)
	return result, nil
}

// handleProcessScheduledPurchases processes scheduled purchases
func (app *Application) handleProcessScheduledPurchases(ctx context.Context) (*purchase.ProcessResult, error) {
	log.Println("Processing scheduled purchases...")
	result, err := app.Purchase.ProcessScheduledPurchases(ctx)
	if err != nil {
		log.Printf("Failed to process scheduled purchases: %v", err)
		return nil, err
	}
	log.Printf("Purchases processed: %d processed, %d executed", result.Processed, result.Executed)
	return result, nil
}

// handleSendNotifications sends upcoming purchase notifications
func (app *Application) handleSendNotifications(ctx context.Context) (*purchase.NotificationResult, error) {
	log.Println("Sending notifications...")
	result, err := app.Purchase.SendUpcomingPurchaseNotifications(ctx)
	if err != nil {
		log.Printf("Failed to send notifications: %v", err)
		return nil, err
	}
	log.Printf("Notifications sent: %d notified", result.Notified)
	return result, nil
}

// handleCleanupExpiredRecords cleans up expired sessions and execution records
func (app *Application) handleCleanupExpiredRecords(ctx context.Context) (map[string]int64, error) {
	log.Println("Cleaning up expired records...")

	result := map[string]int64{
		"sessions_deleted":   0,
		"executions_deleted": 0,
	}

	// Clean up expired sessions via auth service
	if app.Auth != nil {
		if err := app.Auth.CleanupExpiredSessions(ctx); err != nil {
			log.Printf("Warning: failed to cleanup expired sessions: %v", err)
		} else {
			log.Println("Expired sessions cleaned up successfully")
		}
	}

	// Clean up old execution records (30+ days)
	if app.Config != nil {
		const retentionDays = 30
		deleted, err := app.Config.CleanupOldExecutions(ctx, retentionDays)
		if err != nil {
			log.Printf("Warning: failed to cleanup old executions: %v", err)
		} else {
			result["executions_deleted"] = deleted
			log.Printf("Cleaned up %d old execution records", deleted)
		}
	}

	log.Printf("Cleanup complete: %d sessions, %d executions deleted", result["sessions_deleted"], result["executions_deleted"])
	return result, nil
}

// handleRefreshAnalytics refreshes materialized views and analytics data
func (app *Application) handleRefreshAnalytics(ctx context.Context) (map[string]any, error) {
	log.Println("Refreshing analytics...")

	result := map[string]any{
		"status":             "success",
		"views_refreshed":    0,
		"partitions_created": 0,
		"partitions_dropped": 0,
	}

	// Refresh materialized views if analytics store is available
	if app.Analytics != nil {
		if err := app.Analytics.RefreshMaterializedViews(ctx); err != nil {
			log.Printf("Warning: failed to refresh materialized views: %v", err)
			result["status"] = "partial"
		} else {
			result["views_refreshed"] = 1
			log.Println("Materialized views refreshed successfully")
		}
	} else {
		log.Println("Analytics store not available, skipping materialized view refresh")
	}

	log.Printf("Analytics refresh complete")
	return result, nil
}

// HandleSQSMessage processes an SQS message for async purchase processing
func (app *Application) HandleSQSMessage(ctx context.Context, body string) error {
	log.Printf("Processing SQS message (size: %d bytes)", len(body))
	if err := app.Purchase.ProcessMessage(ctx, body); err != nil {
		log.Printf("Failed to process SQS message: %v", err)
		return err
	}
	log.Println("SQS message processed successfully")
	return nil
}

// ScheduledEvent represents a generic scheduled event
type ScheduledEvent struct {
	Source     string          `json:"source"`
	DetailType string          `json:"detail-type"`
	Action     string          `json:"action"`
	Detail     json.RawMessage `json:"detail"`
}

// ParseScheduledEvent parses a scheduled event and returns the task type
func ParseScheduledEvent(rawEvent json.RawMessage) (ScheduledTaskType, error) {
	var event ScheduledEvent
	if err := json.Unmarshal(rawEvent, &event); err != nil {
		return "", fmt.Errorf("failed to parse scheduled event: %w", err)
	}

	// Map action to task type
	switch event.Action {
	case "collect_recommendations":
		return TaskCollectRecommendations, nil
	case "process_scheduled_purchases":
		return TaskProcessScheduledPurchases, nil
	case "send_notifications":
		return TaskSendNotifications, nil
	case "cleanup":
		return TaskCleanupExpiredRecords, nil
	case "analytics_refresh":
		return TaskRefreshAnalytics, nil
	case "ri_exchange_reshape":
		return TaskRIExchangeReshape, nil
	default:
		return "", fmt.Errorf("unknown scheduled task action: %q", event.Action)
	}
}
