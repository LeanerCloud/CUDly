package purchase

import (
	"context"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/google/uuid"
)

// SendUpcomingPurchaseNotifications sends notifications for upcoming automated purchases
func (m *Manager) SendUpcomingPurchaseNotifications(ctx context.Context) (*NotificationResult, error) {
	logging.Info("Checking for upcoming purchases to notify...")

	plans, err := m.config.ListPurchasePlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list purchase plans: %w", err)
	}

	notified := 0
	for _, plan := range plans {
		if m.shouldNotifyPlan(plan) {
			if m.sendPlanNotification(ctx, &plan) {
				notified++
			}
		}
	}

	return &NotificationResult{
		Notified: notified,
	}, nil
}

// shouldNotifyPlan checks if a plan should trigger a notification
func (m *Manager) shouldNotifyPlan(plan config.PurchasePlan) bool {
	if !plan.Enabled || !plan.AutoPurchase {
		return false
	}

	if plan.NextExecutionDate == nil {
		return false
	}

	daysUntil := int(time.Until(*plan.NextExecutionDate).Hours() / config.HoursPerDay)
	if daysUntil < 0 || daysUntil > plan.NotificationDaysBefore {
		return false
	}

	// Check if we already sent notification recently
	if plan.LastNotificationSent != nil {
		hoursSinceNotification := time.Since(*plan.LastNotificationSent).Hours()
		if hoursSinceNotification < config.MinHoursBetweenNotifications {
			return false
		}
	}

	return true
}

// sendPlanNotification sends a notification for a plan and returns true if successful
func (m *Manager) sendPlanNotification(ctx context.Context, plan *config.PurchasePlan) bool {
	daysUntil := int(time.Until(*plan.NextExecutionDate).Hours() / config.HoursPerDay)
	logging.Infof("Sending notification for plan %s (purchase in %d days)", plan.Name, daysUntil)

	// Create execution record if doesn't exist
	execution, err := m.getOrCreateExecution(ctx, plan)
	if err != nil {
		logging.Errorf("Failed to create execution: %v", err)
		return false
	}

	// Send notification
	data := m.buildNotificationData(*plan, execution, daysUntil)
	if err := m.email.SendScheduledPurchaseNotification(ctx, data); err != nil {
		logging.Errorf("Failed to send notification: %v", err)
		return false
	}

	// Update notification sent time
	now := time.Now()
	plan.LastNotificationSent = &now
	if err := m.config.UpdatePurchasePlan(ctx, plan); err != nil {
		logging.Errorf("Failed to update plan: %v", err)
	}

	return true
}

// getOrCreateExecution gets existing execution or creates new one
func (m *Manager) getOrCreateExecution(ctx context.Context, plan *config.PurchasePlan) (*config.PurchaseExecution, error) {
	// Check for existing execution for this date to prevent duplicates
	existing, err := m.config.GetExecutionByPlanAndDate(ctx, plan.ID, *plan.NextExecutionDate)
	if err != nil {
		return nil, fmt.Errorf("failed to check for existing execution: %w", err)
	}
	if existing != nil {
		logging.Debugf("Found existing execution %s for plan %s on %s", existing.ExecutionID, plan.ID, plan.NextExecutionDate)
		return existing, nil
	}

	execution := &config.PurchaseExecution{
		PlanID:        plan.ID,
		ExecutionID:   uuid.New().String(),
		Status:        "pending",
		StepNumber:    plan.RampSchedule.CurrentStep,
		ScheduledDate: *plan.NextExecutionDate,
		ApprovalToken: uuid.New().String(),
	}

	if err := m.config.SavePurchaseExecution(ctx, execution); err != nil {
		return nil, err
	}

	return execution, nil
}

// buildNotificationData creates notification data from plan and execution
func (m *Manager) buildNotificationData(plan config.PurchasePlan, exec *config.PurchaseExecution, daysUntil int) email.NotificationData {
	data := email.NotificationData{
		DashboardURL:      m.dashboardURL,
		ApprovalToken:     exec.ApprovalToken,
		TotalSavings:      exec.EstimatedSavings,
		TotalUpfrontCost:  exec.TotalUpfrontCost,
		PurchaseDate:      exec.ScheduledDate.Format("January 2, 2006"),
		DaysUntilPurchase: daysUntil,
		PlanName:          plan.Name,
	}

	for _, rec := range exec.Recommendations {
		data.Recommendations = append(data.Recommendations, email.RecommendationSummary{
			Service:        rec.Service,
			ResourceType:   rec.ResourceType,
			Engine:         rec.Engine,
			Region:         rec.Region,
			Count:          rec.Count,
			MonthlySavings: rec.Savings,
		})
	}

	return data
}
