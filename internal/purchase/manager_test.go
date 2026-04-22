package purchase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	cfg := ManagerConfig{
		ConfigStore:            mockStore,
		EmailSender:            mockEmail,
		NotificationDaysBefore: 7,
		DefaultTerm:            3,
		DefaultPaymentOption:   "all-upfront",
		DefaultCoverage:        80,
		DefaultRampSchedule:    "immediate",
		DashboardURL:           "https://dashboard.example.com",
	}

	manager := NewManager(cfg)

	assert.NotNil(t, manager)
	assert.Equal(t, 7, manager.notifyDays)
	assert.Equal(t, 3, manager.defaults.Term)
	assert.Equal(t, "all-upfront", manager.defaults.Payment)
	assert.Equal(t, float64(80), manager.defaults.Coverage)
	assert.Equal(t, "immediate", manager.defaults.RampSchedule)
	assert.Equal(t, "https://dashboard.example.com", manager.dashboardURL)
}

func TestPurchaseDefaults(t *testing.T) {
	defaults := PurchaseDefaults{
		Term:         3,
		Payment:      "partial-upfront",
		Coverage:     70,
		RampSchedule: "weekly-25pct",
	}

	assert.Equal(t, 3, defaults.Term)
	assert.Equal(t, "partial-upfront", defaults.Payment)
	assert.Equal(t, float64(70), defaults.Coverage)
	assert.Equal(t, "weekly-25pct", defaults.RampSchedule)
}

func TestManager_ProcessScheduledPurchases_NoExecutions(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	mockStore.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{}, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.ProcessScheduledPurchases(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Processed)
	assert.Equal(t, 0, result.Executed)

	mockStore.AssertExpectations(t)
}

func TestManager_ProcessScheduledPurchases_FutureExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	futureDate := time.Now().Add(24 * time.Hour)
	executions := []config.PurchaseExecution{
		{
			ExecutionID:   "exec-123",
			PlanID:        "plan-456",
			Status:        "pending",
			ScheduledDate: futureDate,
		},
	}

	mockStore.On("GetPendingExecutions", ctx).Return(executions, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.ProcessScheduledPurchases(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Processed)
	assert.Equal(t, 0, result.Executed)

	mockStore.AssertExpectations(t)
}

func TestManager_ProcessScheduledPurchases_CompletedExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	pastDate := time.Now().Add(-1 * time.Hour)
	executions := []config.PurchaseExecution{
		{
			ExecutionID:   "exec-123",
			PlanID:        "plan-456",
			Status:        "completed",
			ScheduledDate: pastDate,
		},
	}

	mockStore.On("GetPendingExecutions", ctx).Return(executions, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.ProcessScheduledPurchases(ctx)
	require.NoError(t, err)

	// Completed executions are skipped without being re-executed; processed counter
	// reflects only actually-attempted executions, not skipped ones.
	assert.Equal(t, 0, result.Processed)
	assert.Equal(t, 0, result.Executed)

	mockStore.AssertExpectations(t)
}

func TestManager_ProcessScheduledPurchases_Error(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	mockStore.On("GetPendingExecutions", ctx).Return(nil, errors.New("database error"))

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.ProcessScheduledPurchases(ctx)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to get pending executions")

	mockStore.AssertExpectations(t)
}

func TestManager_ProcessScheduledPurchases_DuePurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockSTS := new(MockSTSClient)

	pastDate := time.Now().Add(-1 * time.Hour)
	executions := []config.PurchaseExecution{
		{
			ExecutionID:   "exec-123",
			PlanID:        "plan-456",
			Status:        "pending",
			ScheduledDate: pastDate,
			Recommendations: []config.RecommendationRecord{
				{
					Service:      "ec2",
					ResourceType: "m5.large",
					Region:       "us-east-1",
					Count:        1,
					Savings:      50.0,
					UpfrontCost:  200.0,
					Selected:     true,
				},
			},
		},
	}

	plan := &config.PurchasePlan{
		ID:   "plan-456",
		Name: "Test Plan",
		RampSchedule: config.RampSchedule{
			CurrentStep: 0,
			TotalSteps:  4,
		},
	}

	mockStore.On("GetPendingExecutions", ctx).Return(executions, nil)
	mockStore.On("GetPurchasePlan", ctx, "plan-456").Return(plan, nil).Twice()
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	mockStore.On("UpdatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)
	mockSTS.On("GetCallerIdentity", ctx, mock.AnythingOfType("*sts.GetCallerIdentityInput")).Return(&sts.GetCallerIdentityOutput{
		Account: aws.String("123456789012"),
	}, nil)

	// Set up mock provider factory
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockServiceClient := new(MockServiceClient)

	mockFactory.On("CreateAndValidateProvider", ctx, "", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", ctx, common.ServiceEC2, "us-east-1").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", ctx, mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(common.PurchaseResult{
		Success:      true,
		CommitmentID: "ri-12345",
	}, nil)

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		stsClient:       mockSTS,
		dashboardURL:    "https://dashboard.example.com",
		providerFactory: mockFactory,
	}

	result, err := manager.ProcessScheduledPurchases(ctx)
	require.NoError(t, err)

	assert.Equal(t, 1, result.Processed)
	assert.Equal(t, 1, result.Executed)

	mockStore.AssertExpectations(t)
	mockEmail.AssertExpectations(t)
	mockSTS.AssertExpectations(t)
}

func TestManager_ProcessScheduledPurchases_CancelledExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	pastDate := time.Now().Add(-1 * time.Hour)
	executions := []config.PurchaseExecution{
		{
			ExecutionID:   "exec-123",
			PlanID:        "plan-456",
			Status:        "cancelled",
			ScheduledDate: pastDate,
		},
	}

	mockStore.On("GetPendingExecutions", ctx).Return(executions, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.ProcessScheduledPurchases(ctx)
	require.NoError(t, err)

	// Cancelled executions are skipped without being re-executed; processed counter
	// reflects only actually-attempted executions, not skipped ones.
	assert.Equal(t, 0, result.Processed)
	assert.Equal(t, 0, result.Executed)

	mockStore.AssertExpectations(t)
}

func TestManager_ProcessScheduledPurchases_ExecutionFails(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	pastDate := time.Now().Add(-1 * time.Hour)
	executions := []config.PurchaseExecution{
		{
			ExecutionID:   "exec-123",
			PlanID:        "plan-456",
			Status:        "pending",
			ScheduledDate: pastDate,
		},
	}

	mockStore.On("GetPendingExecutions", ctx).Return(executions, nil)
	mockStore.On("GetPurchasePlan", ctx, "plan-456").Return(nil, errors.New("plan not found")).Once()
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	// updatePlanProgress is NOT called when execution fails

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := manager.ProcessScheduledPurchases(ctx)
	require.NoError(t, err)

	assert.Equal(t, 1, result.Processed)
	assert.Equal(t, 0, result.Executed)

	mockStore.AssertExpectations(t)
}
