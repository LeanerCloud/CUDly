package purchase

import (
	"context"
	"errors"
	"sync"
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

func TestManager_ExecutePurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockSTS := new(MockSTSClient)
	mockFactory := new(MockProviderFactory)
	mockProviderInst := new(MockProvider)
	mockServiceClient := new(MockServiceClient)

	plan := &config.PurchasePlan{
		ID:   "plan-123",
		Name: "Test Plan",
	}

	exec := &config.PurchaseExecution{
		ExecutionID: "exec-123",
		PlanID:      "plan-123",
		StepNumber:  1,
		Recommendations: []config.RecommendationRecord{
			{
				Provider:     "aws",
				Service:      "ec2",
				ResourceType: "m5.large",
				Region:       "us-east-1",
				Count:        5,
				Savings:      100.0,
				UpfrontCost:  500.0,
				Selected:     true,
			},
			{
				Provider:     "aws",
				Service:      "rds",
				ResourceType: "db.r5.large",
				Region:       "us-west-2",
				Count:        2,
				Savings:      50.0,
				UpfrontCost:  200.0,
				Selected:     false, // Not selected
			},
		},
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-123").Return(plan, nil)
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	mockSTS.On("GetCallerIdentity", ctx, mock.AnythingOfType("*sts.GetCallerIdentityInput")).Return(&sts.GetCallerIdentityOutput{
		Account: aws.String("123456789012"),
	}, nil)

	// Mock provider factory to return a mock provider
	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProviderInst, nil)
	mockProviderInst.On("GetServiceClient", ctx, common.ServiceEC2, "us-east-1").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", ctx, mock.AnythingOfType("common.Recommendation")).Return(common.PurchaseResult{
		Success:      true,
		CommitmentID: "ri-12345",
	}, nil)

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		stsClient:       mockSTS,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	err := manager.executePurchase(ctx, exec)
	require.NoError(t, err)

	// Verify that only selected recommendation was purchased
	assert.True(t, exec.Recommendations[0].Purchased)
	assert.NotEmpty(t, exec.Recommendations[0].PurchaseID)
	assert.False(t, exec.Recommendations[1].Purchased)

	mockStore.AssertExpectations(t)
	mockEmail.AssertExpectations(t)
	mockSTS.AssertExpectations(t)
	mockFactory.AssertExpectations(t)
}

func TestManager_ExecutePurchase_PlanNotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	exec := &config.PurchaseExecution{
		ExecutionID: "exec-123",
		PlanID:      "nonexistent",
		StepNumber:  1,
	}

	mockStore.On("GetPurchasePlan", ctx, "nonexistent").Return(nil, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.executePurchase(ctx, exec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "plan not found")

	mockStore.AssertExpectations(t)
}

func TestManager_ExecutePurchase_GetPlanError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	exec := &config.PurchaseExecution{
		ExecutionID: "exec-123",
		PlanID:      "plan-123",
		StepNumber:  1,
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-123").Return(nil, errors.New("database error"))

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.executePurchase(ctx, exec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get plan")

	mockStore.AssertExpectations(t)
}

func TestManager_ExecutePurchase_NoRecommendations(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockSTS := new(MockSTSClient)

	plan := &config.PurchasePlan{
		ID:   "plan-123",
		Name: "Test Plan",
	}

	exec := &config.PurchaseExecution{
		ExecutionID:     "exec-123",
		PlanID:          "plan-123",
		StepNumber:      1,
		Recommendations: []config.RecommendationRecord{},
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-123").Return(plan, nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	mockSTS.On("GetCallerIdentity", ctx, mock.AnythingOfType("*sts.GetCallerIdentityInput")).Return(&sts.GetCallerIdentityOutput{
		Account: aws.String("123456789012"),
	}, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		stsClient:    mockSTS,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.executePurchase(ctx, exec)
	require.NoError(t, err)

	mockStore.AssertExpectations(t)
	mockEmail.AssertExpectations(t)
}

func TestManager_UpdatePlanProgress(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	startDate := time.Now()
	plan := &config.PurchasePlan{
		ID:   "plan-123",
		Name: "Test Plan",
		RampSchedule: config.RampSchedule{
			Type:             "weekly",
			PercentPerStep:   25,
			StepIntervalDays: 7,
			CurrentStep:      0,
			TotalSteps:       4,
			StartDate:        startDate,
		},
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-123").Return(plan, nil)
	mockStore.On("UpdatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.updatePlanProgress(ctx, "plan-123")
	require.NoError(t, err)

	mockStore.AssertExpectations(t)
}

func TestManager_UpdatePlanProgress_PlanNotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	mockStore.On("GetPurchasePlan", ctx, "nonexistent").Return(nil, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.updatePlanProgress(ctx, "nonexistent")
	require.NoError(t, err)

	mockStore.AssertExpectations(t)
}

func TestManager_UpdatePlanProgress_GetError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	mockStore.On("GetPurchasePlan", ctx, "plan-123").Return(nil, errors.New("database error"))

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.updatePlanProgress(ctx, "plan-123")
	assert.Error(t, err)

	mockStore.AssertExpectations(t)
}

func TestManager_UpdatePlanProgress_CompleteRamp(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	startDate := time.Now()
	plan := &config.PurchasePlan{
		ID:   "plan-123",
		Name: "Test Plan",
		RampSchedule: config.RampSchedule{
			Type:             "weekly",
			PercentPerStep:   25,
			StepIntervalDays: 7,
			CurrentStep:      3, // Last step (0-indexed, so 3 is the 4th step)
			TotalSteps:       4,
			StartDate:        startDate,
		},
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-123").Return(plan, nil)
	mockStore.On("UpdatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.updatePlanProgress(ctx, "plan-123")
	require.NoError(t, err)

	mockStore.AssertExpectations(t)
}

func TestManager_GetAWSAccountID_Success(t *testing.T) {
	ctx := context.Background()
	mockSTS := new(MockSTSClient)

	mockSTS.On("GetCallerIdentity", ctx, mock.AnythingOfType("*sts.GetCallerIdentityInput")).Return(&sts.GetCallerIdentityOutput{
		Account: aws.String("987654321098"),
	}, nil)

	manager := &Manager{
		stsClient: mockSTS,
	}

	accountID := manager.getAWSAccountID(ctx)
	assert.Equal(t, "987654321098", accountID)

	mockSTS.AssertExpectations(t)
}

func TestManager_GetAWSAccountID_NoClient(t *testing.T) {
	ctx := context.Background()

	manager := &Manager{
		stsClient: nil, // No STS client configured
	}

	accountID := manager.getAWSAccountID(ctx)
	assert.Equal(t, "unknown", accountID)
}

func TestManager_GetAWSAccountID_Error(t *testing.T) {
	ctx := context.Background()
	mockSTS := new(MockSTSClient)

	mockSTS.On("GetCallerIdentity", ctx, mock.AnythingOfType("*sts.GetCallerIdentityInput")).Return(nil, errors.New("STS error"))

	manager := &Manager{
		stsClient: mockSTS,
	}

	accountID := manager.getAWSAccountID(ctx)
	assert.Equal(t, "unknown", accountID)

	mockSTS.AssertExpectations(t)
}

func TestManager_GetAWSAccountID_NilAccount(t *testing.T) {
	ctx := context.Background()
	mockSTS := new(MockSTSClient)

	mockSTS.On("GetCallerIdentity", ctx, mock.AnythingOfType("*sts.GetCallerIdentityInput")).Return(&sts.GetCallerIdentityOutput{
		Account: nil, // Nil account in response
	}, nil)

	manager := &Manager{
		stsClient: mockSTS,
	}

	accountID := manager.getAWSAccountID(ctx)
	assert.Equal(t, "unknown", accountID)

	mockSTS.AssertExpectations(t)
}

func TestManager_ExecutePurchase_MultiAccount(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockServiceClient := new(MockServiceClient)
	credStore := &MockCredentialStore{}

	plan := &config.PurchasePlan{
		ID:           "plan-multi",
		Name:         "Multi-Account Plan",
		RampSchedule: config.RampSchedule{CurrentStep: 0, TotalSteps: 1},
	}

	rec := config.RecommendationRecord{
		Provider:     "aws",
		Service:      "ec2",
		ResourceType: "m5.large",
		Region:       "us-east-1",
		Count:        1,
		Savings:      75.0,
		UpfrontCost:  300.0,
		Selected:     true,
	}

	exec := &config.PurchaseExecution{
		ExecutionID:     "exec-multi",
		PlanID:          "plan-multi",
		Status:          "pending",
		Recommendations: []config.RecommendationRecord{rec},
	}

	accounts := []config.CloudAccount{
		{ID: "aaaaaaaa-0000-0000-0000-000000000001", Name: "Prod", Provider: "aws", ExternalID: "111111111111", AWSAuthMode: "access_keys"},
		{ID: "aaaaaaaa-0000-0000-0000-000000000002", Name: "Staging", Provider: "aws", ExternalID: "222222222222", AWSAuthMode: "access_keys"},
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-multi").Return(plan, nil)
	// GetPlanAccounts and SavePurchaseExecution use override fns (not .On) because the
	// goroutine-based fan-out bypasses the testify mock tracker.
	mockStore.GetPlanAccountsFn = func(_ context.Context, _ string) ([]config.CloudAccount, error) {
		return accounts, nil
	}
	var savedExecs []*config.PurchaseExecution
	var mu sync.Mutex
	mockStore.SavePurchaseExecutionFn = func(_ context.Context, exec *config.PurchaseExecution) error {
		mu.Lock()
		savedExecs = append(savedExecs, exec)
		mu.Unlock()
		return nil
	}
	// SavePurchaseHistory and SendPurchaseConfirmation are called once per account.
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil).Times(2)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil).Times(2)

	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", ctx, common.ServiceEC2, "us-east-1").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", ctx, mock.AnythingOfType("common.Recommendation")).Return(common.PurchaseResult{
		Success: true, CommitmentID: "ri-99999",
	}, nil)

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		providerFactory: mockFactory,
		credStore:       credStore,
		// assumeRoleSTS is nil → access_keys path, no role assumption needed
	}

	err := manager.executePurchase(ctx, exec)
	require.NoError(t, err)

	// The original exec record should be unchanged (fan-out creates per-account copies).
	assert.Nil(t, exec.CloudAccountID)

	// Two per-account execution records should have been saved, each with a distinct account ID.
	require.Len(t, savedExecs, 2)
	accountIDs := []string{*savedExecs[0].CloudAccountID, *savedExecs[1].CloudAccountID}
	assert.ElementsMatch(t, []string{
		"aaaaaaaa-0000-0000-0000-000000000001",
		"aaaaaaaa-0000-0000-0000-000000000002",
	}, accountIDs)

	mockStore.AssertExpectations(t)
	mockEmail.AssertExpectations(t)
	mockFactory.AssertExpectations(t)
}

func TestPlanProvider(t *testing.T) {
	tests := []struct {
		name     string
		services map[string]config.ServiceConfig
		want     string
	}{
		{"aws prefix", map[string]config.ServiceConfig{"aws:ec2": {}}, "aws"},
		{"gcp prefix", map[string]config.ServiceConfig{"gcp:compute": {}}, "gcp"},
		{"no colon", map[string]config.ServiceConfig{"ec2": {}}, ""},
		{"empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := &config.PurchasePlan{Services: tt.services}
			assert.Equal(t, tt.want, planProvider(plan))
		})
	}
}
