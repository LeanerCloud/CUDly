package server

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"

	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
)

// TaskLocker abstracts advisory lock operations for scheduled task concurrency control.
type TaskLocker interface {
	TryAdvisoryLock(ctx context.Context, lockID int64) (bool, error)
	ReleaseAdvisoryLock(ctx context.Context, lockID int64)
}

// ScheduledTaskType represents different types of scheduled tasks
type ScheduledTaskType string

const (
	TaskCollectRecommendations    ScheduledTaskType = "collect_recommendations"
	TaskProcessScheduledPurchases ScheduledTaskType = "process_scheduled_purchases"
	TaskSendNotifications         ScheduledTaskType = "send_notifications"
	TaskCleanupExpiredRecords     ScheduledTaskType = "cleanup"
	TaskRefreshAnalytics          ScheduledTaskType = "analytics_refresh"
	// TaskCollectAnalytics runs the savings-snapshot collector end to end:
	// ensure upcoming partitions, collect a snapshot across all tenants, apply
	// retention, and refresh the materialized views. Scheduled separately from
	// TaskRefreshAnalytics (the legacy refresh-only task) so the snapshot
	// ingestion cadence can differ from a pure view refresh. See issues
	// #1023 / #1033.
	TaskCollectAnalytics  ScheduledTaskType = "analytics_collect"
	TaskRIExchangeReshape ScheduledTaskType = "ri_exchange_reshape"
	// TaskReapStuckPurchases sweeps purchase_executions stuck in
	// approved/running longer than PURCHASE_APPROVED_REAP_AFTER and flips
	// them to "failed" via the existing TransitionExecutionStatus CAS.
	// Backstop for synchronous-executor crashes (Lambda timeout, OOM,
	// network hang) that leave rows orphaned in an in-flight state.
	// See internal/purchase/reaper.go + issue #678.
	TaskReapStuckPurchases ScheduledTaskType = "reap_stuck_purchases"
	// TaskFireScheduledPurchases fires purchase_executions in status=scheduled
	// whose scheduled_execution_at is in the past (Gmail-style pre-fire delay,
	// issue #291 wave-2). Wires the "fire_scheduled_purchases" event action
	// to purchase.Manager.FireScheduledDelayedPurchases.
	TaskFireScheduledPurchases ScheduledTaskType = "fire_scheduled_purchases"
)

// scheduledEventActions maps a raw scheduled-event action string to its
// ScheduledTaskType. Kept as a table (rather than a switch) so adding a task
// type stays a one-line change and ParseScheduledEvent's cyclomatic complexity
// does not grow with the task list.
var scheduledEventActions = map[string]ScheduledTaskType{
	"collect_recommendations":     TaskCollectRecommendations,
	"process_scheduled_purchases": TaskProcessScheduledPurchases,
	"send_notifications":          TaskSendNotifications,
	"cleanup":                     TaskCleanupExpiredRecords,
	"analytics_refresh":           TaskRefreshAnalytics,
	"analytics_collect":           TaskCollectAnalytics,
	"ri_exchange_reshape":         TaskRIExchangeReshape,
	"reap_stuck_purchases":        TaskReapStuckPurchases,
	"fire_scheduled_purchases":    TaskFireScheduledPurchases,
}

// HandleScheduledTask processes a scheduled task by type.
// It acquires a PostgreSQL advisory lock to prevent concurrent execution of the same task.
func (app *Application) HandleScheduledTask(ctx context.Context, taskType ScheduledTaskType) (any, error) {
	log.Printf("Handling scheduled task: %s", taskType)

	if err := app.ensureDB(ctx); err != nil {
		return nil, fmt.Errorf("database connection failed: %w", err)
	}

	locker := app.taskLocker()
	if locker != nil {
		lockID := taskLockID(taskType)
		acquired, err := locker.TryAdvisoryLock(ctx, lockID)
		if err != nil {
			return nil, fmt.Errorf("failed to check task lock: %w", err)
		}
		if !acquired {
			log.Printf("Task %s already running (advisory lock held), skipping", taskType)
			return map[string]string{"status": "skipped", "reason": "already_running"}, nil
		}
		defer locker.ReleaseAdvisoryLock(ctx, lockID)
	}

	return app.dispatchTask(ctx, taskType)
}

// dispatchTask routes a scheduled task to its handler.
func (app *Application) dispatchTask(ctx context.Context, taskType ScheduledTaskType) (any, error) {
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
	case TaskCollectAnalytics:
		return app.handleCollectAnalytics(ctx)
	case TaskRIExchangeReshape:
		return app.handleRIExchangeReshape(ctx)
	case TaskReapStuckPurchases:
		return app.handleReapStuckPurchases(ctx)
	case TaskFireScheduledPurchases:
		return app.handleFireScheduledPurchases(ctx)
	default:
		return nil, fmt.Errorf("unknown scheduled task type: %s", taskType)
	}
}

// taskLocker returns the configured TaskLocker, falling back to DB if set.
func (app *Application) taskLocker() TaskLocker {
	if app.TaskLocker != nil {
		return app.TaskLocker
	}
	if app.DB != nil {
		return app.DB
	}
	return nil
}

// taskLockID derives a stable int64 lock ID from the task type name.
func taskLockID(taskType ScheduledTaskType) int64 {
	h := fnv.New64a()
	h.Write([]byte("cudly:task:" + string(taskType)))
	return int64(h.Sum64())
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
	log.Printf("Purchases processed: %d processed, %d executed, %d stranded-approvals recovered", result.Processed, result.Executed, result.Recovered)
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

// handleReapStuckPurchases sweeps purchase_executions stuck in
// approved/running longer than the configured threshold and flips them
// to "failed" via the existing TransitionExecutionStatus CAS. See
// internal/purchase/reaper.go + issue #678 for the full rationale and
// safety properties.
//
// The threshold is read fresh from the PURCHASE_APPROVED_REAP_AFTER env
// var on every invocation (not cached at startup) so an ops tune via
// Lambda env-var rotation takes effect on the next sweep without a
// redeploy.
func (app *Application) handleReapStuckPurchases(ctx context.Context) (*purchase.ReapResult, error) {
	reapAfter := purchase.ParseReapAfterFromEnv()
	log.Printf("Reaping stuck purchase executions (threshold: %s)...", reapAfter)
	result, err := app.Purchase.ReapStuckExecutions(ctx, reapAfter)
	if err != nil {
		log.Printf("Failed to reap stuck purchase executions: %v", err)
		return nil, err
	}
	log.Printf("Reap sweep complete: found=%d reaped=%d race_lost=%d errored=%d",
		result.Found, result.Reaped, result.RaceLost, result.Errored)
	return result, nil
}

// handleFireScheduledPurchases fires purchase_executions in status=scheduled
// whose scheduled_execution_at is in the past. Part of the Gmail-style pre-fire
// delay feature (issue #291 wave-2): approve defers the cloud SDK call; this
// tick fires the SDK call when the window expires.
func (app *Application) handleFireScheduledPurchases(ctx context.Context) (*purchase.FireResult, error) {
	log.Println("Firing scheduled delayed purchases...")
	result, err := app.Purchase.FireScheduledDelayedPurchases(ctx)
	if err != nil {
		log.Printf("Failed to fire scheduled purchases: %v", err)
		return nil, err
	}
	log.Printf("Fire sweep complete: found=%d fired=%d race_lost=%d errored=%d",
		result.Found, result.Fired, result.RaceLost, result.Errored)
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

	// Refresh materialized views if analytics store is available.
	// Include the error in the result map so API callers (and the operator
	// reading the scheduled-task response body) can see it, not only the
	// server-side log (06-M4 error-visibility).
	if app.Analytics != nil {
		if err := app.Analytics.RefreshMaterializedViews(ctx); err != nil {
			log.Printf("Warning: failed to refresh materialized views: %v", err)
			result["status"] = "partial"
			result["views_error"] = err.Error()
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
	if taskType, ok := scheduledEventActions[event.Action]; ok {
		return taskType, nil
	}
	return "", fmt.Errorf("unknown scheduled task action: %q", event.Action)
}
