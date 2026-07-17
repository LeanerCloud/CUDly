package purchase

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestManager_SendUpcomingPurchaseNotifications_NoPlans(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return([]config.PurchasePlan{}, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		notifyDays:   7,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.SendUpcomingPurchaseNotifications(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Notified)

	mockStore.AssertExpectations(t)
}

func TestManager_SendUpcomingPurchaseNotifications_DisabledPlan(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	plans := []config.PurchasePlan{
		{
			ID:           "plan-123",
			Name:         "Test Plan",
			Enabled:      false,
			AutoPurchase: true,
		},
	}

	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return(plans, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		notifyDays:   7,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.SendUpcomingPurchaseNotifications(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Notified)

	mockStore.AssertExpectations(t)
}

func TestManager_SendUpcomingPurchaseNotifications_NotAutoPurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	plans := []config.PurchasePlan{
		{
			ID:           "plan-123",
			Name:         "Test Plan",
			Enabled:      true,
			AutoPurchase: false,
		},
	}

	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return(plans, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		notifyDays:   7,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.SendUpcomingPurchaseNotifications(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Notified)

	mockStore.AssertExpectations(t)
}

func TestManager_SendUpcomingPurchaseNotifications_Error(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return(nil, errors.New("database error"))

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		notifyDays:   7,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.SendUpcomingPurchaseNotifications(ctx)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to list purchase plans")

	mockStore.AssertExpectations(t)
}

func TestManager_BuildNotificationData(t *testing.T) {
	manager := &Manager{
		dashboardURL: "https://dashboard.example.com",
	}

	plan := config.PurchasePlan{
		ID:   "plan-123",
		Name: "Test Plan",
	}

	scheduledDate := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	execution := &config.PurchaseExecution{
		ExecutionID:      "exec-456",
		ApprovalToken:    "token-abc",
		EstimatedSavings: 500.0,
		TotalUpfrontCost: 1500.0,
		ScheduledDate:    scheduledDate,
		Recommendations: []config.RecommendationRecord{
			{
				Service:      "rds",
				ResourceType: "db.r5.large",
				Engine:       "postgres",
				Region:       "us-east-1",
				Count:        2,
				Savings:      200.0,
			},
		},
	}

	data := manager.buildNotificationData(plan, execution, 5, "notify@example.com")

	assert.Equal(t, "https://dashboard.example.com", data.DashboardURL)
	assert.Equal(t, "token-abc", data.ApprovalToken)
	assert.Equal(t, 500.0, data.TotalSavings)
	assert.Equal(t, 1500.0, data.TotalUpfrontCost)
	assert.Equal(t, "February 1, 2024", data.PurchaseDate)
	assert.Equal(t, 5, data.DaysUntilPurchase)
	assert.Equal(t, "Test Plan", data.PlanName)
	assert.Len(t, data.Recommendations, 1)
	assert.Equal(t, "rds", data.Recommendations[0].Service)
}

func TestManager_GetOrCreateExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	nextExec := time.Now().Add(24 * time.Hour)
	plan := &config.PurchasePlan{
		ID:   "plan-123",
		Name: "Test Plan",
		RampSchedule: config.RampSchedule{
			CurrentStep: 1,
		},
		NextExecutionDate: &nextExec,
	}

	// No existing execution found
	mockStore.On("GetExecutionByPlanAndDate", ctx, "plan-123", nextExec).Return(nil, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	execution, err := manager.getOrCreateExecution(ctx, plan)
	require.NoError(t, err)
	assert.NotNil(t, execution)
	assert.Equal(t, "plan-123", execution.PlanID)
	assert.Equal(t, "pending", execution.Status)
	assert.Equal(t, 1, execution.StepNumber)
	assert.NotEmpty(t, execution.ExecutionID)
	assert.NotEmpty(t, execution.ApprovalToken)

	mockStore.AssertExpectations(t)
}

func TestManager_GetOrCreateExecution_ExistingExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	nextExec := time.Now().Add(24 * time.Hour)
	plan := &config.PurchasePlan{
		ID:   "plan-123",
		Name: "Test Plan",
		RampSchedule: config.RampSchedule{
			CurrentStep: 1,
		},
		NextExecutionDate: &nextExec,
	}

	existingExec := &config.PurchaseExecution{
		ExecutionID:   "existing-exec-id",
		PlanID:        "plan-123",
		Status:        "pending",
		ScheduledDate: nextExec,
	}

	// Existing execution found - should return it without saving
	mockStore.On("GetExecutionByPlanAndDate", ctx, "plan-123", nextExec).Return(existingExec, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	execution, err := manager.getOrCreateExecution(ctx, plan)
	require.NoError(t, err)
	assert.NotNil(t, execution)
	assert.Equal(t, "existing-exec-id", execution.ExecutionID)
	assert.Equal(t, "plan-123", execution.PlanID)

	mockStore.AssertExpectations(t)
}

func TestManager_GetOrCreateExecution_SaveError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	nextExec := time.Now().Add(24 * time.Hour)
	plan := &config.PurchasePlan{
		ID:                "plan-123",
		Name:              "Test Plan",
		NextExecutionDate: &nextExec,
	}

	// No existing execution found
	mockStore.On("GetExecutionByPlanAndDate", ctx, "plan-123", nextExec).Return(nil, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(errors.New("save failed"))

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	execution, err := manager.getOrCreateExecution(ctx, plan)
	assert.Error(t, err)
	assert.Nil(t, execution)

	mockStore.AssertExpectations(t)
}

func TestManager_GetOrCreateExecution_LookupError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	nextExec := time.Now().Add(24 * time.Hour)
	plan := &config.PurchasePlan{
		ID:                "plan-123",
		Name:              "Test Plan",
		NextExecutionDate: &nextExec,
	}

	// Error looking up existing execution
	mockStore.On("GetExecutionByPlanAndDate", ctx, "plan-123", nextExec).Return(nil, errors.New("db error"))

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	execution, err := manager.getOrCreateExecution(ctx, plan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check for existing execution")
	assert.Nil(t, execution)

	mockStore.AssertExpectations(t)
}

// TestManager_GetOrCreateExecution_CreatesOnErrNotFound is the F2 regression
// guard: when GetExecutionByPlanAndDate wraps ErrNotFound (zero rows), the
// create branch must fire rather than treating it as a hard error.
// Pre-fix, the store returned a plain fmt.Errorf on zero rows, so getOrCreateExecution
// treated it as a fatal error and the create branch was unreachable.
func TestManager_GetOrCreateExecution_CreatesOnErrNotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	nextExec := time.Now().Add(24 * time.Hour)
	plan := &config.PurchasePlan{
		ID:                "plan-f2",
		Name:              "F2 Plan",
		NextExecutionDate: &nextExec,
		RampSchedule:      config.RampSchedule{CurrentStep: 2},
	}

	// Store returns ErrNotFound (wrapped), matching the post-fix store behavior.
	notFoundErr := fmt.Errorf("%w: plan plan-f2 at %v", config.ErrNotFound, nextExec)
	mockStore.On("GetExecutionByPlanAndDate", ctx, "plan-f2", nextExec).Return(nil, notFoundErr)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)

	manager := &Manager{config: mockStore, dashboardURL: "https://example.com"}

	execution, err := manager.getOrCreateExecution(ctx, plan)
	require.NoError(t, err, "ErrNotFound must trigger the create path, not a hard error (F2)")
	require.NotNil(t, execution)
	assert.Equal(t, "plan-f2", execution.PlanID)
	assert.Equal(t, "pending", execution.Status)
	assert.Equal(t, 2, execution.StepNumber)
	assert.NotEmpty(t, execution.ExecutionID)
	assert.NotEmpty(t, execution.ApprovalToken)

	mockStore.AssertExpectations(t)
}

func TestManager_SendUpcomingPurchaseNotifications_WithNotification(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	nextExec := time.Now().Add(3 * 24 * time.Hour) // 3 days from now
	plans := []config.PurchasePlan{
		{
			ID:                     "plan-123",
			Name:                   "Test Plan",
			Enabled:                true,
			AutoPurchase:           true,
			NotificationDaysBefore: 7,
			NextExecutionDate:      &nextExec,
			LastNotificationSent:   nil,
			RampSchedule:           config.RampSchedule{CurrentStep: 0},
		},
	}

	notifyEmailStr := "notify@example.com"
	globalCfg := &config.GlobalConfig{NotificationEmail: &notifyEmailStr}
	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return(plans, nil)
	// No existing execution found
	mockStore.On("GetExecutionByPlanAndDate", ctx, "plan-123", nextExec).Return(nil, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	mockEmail.On("SendScheduledPurchaseNotification", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	mockStore.On("UpdatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		notifyDays:   7,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.SendUpcomingPurchaseNotifications(ctx)
	require.NoError(t, err)

	assert.Equal(t, 1, result.Notified)

	mockStore.AssertExpectations(t)
	mockEmail.AssertExpectations(t)
}

func TestManager_SendUpcomingPurchaseNotifications_TooFarAway(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	nextExec := time.Now().Add(14 * 24 * time.Hour) // 14 days from now
	plans := []config.PurchasePlan{
		{
			ID:                     "plan-123",
			Name:                   "Test Plan",
			Enabled:                true,
			AutoPurchase:           true,
			NotificationDaysBefore: 7,
			NextExecutionDate:      &nextExec,
		},
	}

	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return(plans, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		notifyDays:   7,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.SendUpcomingPurchaseNotifications(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Notified)

	mockStore.AssertExpectations(t)
}

func TestManager_SendUpcomingPurchaseNotifications_RecentNotification(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	nextExec := time.Now().Add(3 * 24 * time.Hour) // 3 days from now
	lastNotif := time.Now().Add(-12 * time.Hour)   // 12 hours ago
	plans := []config.PurchasePlan{
		{
			ID:                     "plan-123",
			Name:                   "Test Plan",
			Enabled:                true,
			AutoPurchase:           true,
			NotificationDaysBefore: 7,
			NextExecutionDate:      &nextExec,
			LastNotificationSent:   &lastNotif,
		},
	}

	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return(plans, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		notifyDays:   7,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.SendUpcomingPurchaseNotifications(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Notified)

	mockStore.AssertExpectations(t)
}

func TestManager_SendUpcomingPurchaseNotifications_NoNextExecutionDate(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	plans := []config.PurchasePlan{
		{
			ID:                     "plan-123",
			Name:                   "Test Plan",
			Enabled:                true,
			AutoPurchase:           true,
			NotificationDaysBefore: 7,
			NextExecutionDate:      nil, // No next execution date
		},
	}

	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return(plans, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		notifyDays:   7,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.SendUpcomingPurchaseNotifications(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Notified)

	mockStore.AssertExpectations(t)
}

func TestManager_SendUpcomingPurchaseNotifications_EmailFails(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	nextExec := time.Now().Add(3 * 24 * time.Hour)
	plans := []config.PurchasePlan{
		{
			ID:                     "plan-123",
			Name:                   "Test Plan",
			Enabled:                true,
			AutoPurchase:           true,
			NotificationDaysBefore: 7,
			NextExecutionDate:      &nextExec,
			RampSchedule:           config.RampSchedule{CurrentStep: 0},
		},
	}

	notifyEmailStr := "notify@example.com"
	globalCfg := &config.GlobalConfig{NotificationEmail: &notifyEmailStr}
	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return(plans, nil)
	// No existing execution found
	mockStore.On("GetExecutionByPlanAndDate", ctx, "plan-123", nextExec).Return(nil, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	mockEmail.On("SendScheduledPurchaseNotification", ctx, mock.AnythingOfType("email.NotificationData")).Return(errors.New("email failed"))

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		notifyDays:   7,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.SendUpcomingPurchaseNotifications(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Notified)

	mockStore.AssertExpectations(t)
	mockEmail.AssertExpectations(t)
}
