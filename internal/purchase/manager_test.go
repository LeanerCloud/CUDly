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

	mockStore.On("GetStaleApprovedExecutions", ctx, mock.Anything).Return([]config.PurchaseExecution{}, nil)
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

	mockStore.On("GetStaleApprovedExecutions", ctx, mock.Anything).Return([]config.PurchaseExecution{}, nil)
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

	mockStore.On("GetStaleApprovedExecutions", ctx, mock.Anything).Return([]config.PurchaseExecution{}, nil)
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

	mockStore.On("GetStaleApprovedExecutions", ctx, mock.Anything).Return([]config.PurchaseExecution{}, nil)
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

	mockStore.On("GetStaleApprovedExecutions", ctx, mock.Anything).Return([]config.PurchaseExecution{}, nil)
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

	mockFactory.On("CreateAndValidateProvider", mock.Anything, "", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", mock.MatchedBy(hasPerRecDeadline(30*time.Second)), common.ServiceEC2, "us-east-1").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", mock.MatchedBy(hasPerRecDeadline(30*time.Second)), mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(common.PurchaseResult{
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

	mockStore.On("GetStaleApprovedExecutions", ctx, mock.Anything).Return([]config.PurchaseExecution{}, nil)
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

	mockStore.On("GetStaleApprovedExecutions", ctx, mock.Anything).Return([]config.PurchaseExecution{}, nil)
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

// TestManager_RecoverStrandedApprovals_FailsStrandedRow is the regression test
// for issue #632 safe-fail path: an Azure execution flipped to "approved" whose
// synchronous purchase run was interrupted before it finalized must NOT stay
// permanently "approved". Azure re-drive idempotency is blocked by issue #721, so
// the recovery sweep drives the row into a terminal "failed" state with a clear
// error and does NOT re-run the purchase (no provider/service-client calls),
// eliminating any double-purchase risk for non-AWS providers.
func TestManager_RecoverStrandedApprovals_FailsStrandedRow(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)

	stranded := config.PurchaseExecution{
		ExecutionID: "exec-stranded",
		PlanID:      "plan-456",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			// Azure provider: falls through to safe-fail path (issue #721 blocks re-drive).
			{Provider: "azure", Service: "reservations", ResourceType: "Standard_D4s_v3", Region: "eastus", Count: 1, UpfrontCost: 500.0, Selected: true, Purchased: false},
		},
	}
	failedRow := stranded
	failedRow.Status = "failed"

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	// The atomic transition only flips rows still in "approved".
	mockStore.On("TransitionExecutionStatus", ctx, "exec-stranded", []string{"approved"}, "failed").
		Return(&failedRow, nil)
	// The explanatory error is stamped on the now-failed row.
	var saved *config.PurchaseExecution
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).
		Run(func(args mock.Arguments) { saved = args.Get(1).(*config.PurchaseExecution) }).
		Return(nil)

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	recovered, err := manager.RecoverStrandedApprovals(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, recovered)

	require.NotNil(t, saved)
	assert.Equal(t, "failed", saved.Status, "stranded Azure row must become terminally failed, never stay approved")
	assert.NotEmpty(t, saved.Error, "the failed row must carry a clear, operator-readable error")
	assert.Contains(t, saved.Error, "interrupted")
	assert.False(t, saved.Recommendations[0].Purchased, "recovery must not mark anything purchased")

	mockStore.AssertExpectations(t)
	// No provider was ever created: the sweep fails, it does not re-purchase.
	// A double-purchase would require CreateAndValidateProvider here.
	mockFactory.AssertNotCalled(t, "CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything)
}

// TestManager_RecoverStrandedApprovals_FreshRowUntouched verifies the sweep only
// acts on rows the store returns as stale. A freshly-approved execution (still
// within staleApprovedThreshold, hence excluded by GetStaleApprovedExecutions)
// is never transitioned or saved, so an in-flight synchronous purchase is never
// failed out from under itself.
func TestManager_RecoverStrandedApprovals_FreshRowUntouched(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{}, nil)

	manager := &Manager{config: mockStore, dashboardURL: "https://dashboard.example.com"}

	recovered, err := manager.RecoverStrandedApprovals(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, recovered)

	mockStore.AssertExpectations(t)
	mockStore.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

// TestManager_RecoverStrandedApprovals_AWSOnlyRedrives is the regression test for
// issue #632 Option 5: a stranded AWS-only execution with a durable ExecutionID is
// re-driven via executeAndFinalize rather than failed. All AWS executors honour
// opts.IdempotencyToken via DeriveIdempotencyToken(exec.ExecutionID, i), so the
// second call is a safe no-op on the AWS side and the row transitions directly to
// "completed" without requiring a manual Retry.
func TestManager_RecoverStrandedApprovals_AWSOnlyRedrives(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockSTS := new(MockSTSClient)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockServiceClient := new(MockServiceClient)

	stranded := config.PurchaseExecution{
		ExecutionID: "exec-aws-stranded",
		PlanID:      "plan-aws-456",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 200.0, Selected: true, Purchased: false},
		},
	}

	plan := &config.PurchasePlan{
		ID:   "plan-aws-456",
		Name: "AWS Test Plan",
		RampSchedule: config.RampSchedule{
			CurrentStep: 0,
			TotalSteps:  4,
		},
	}

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	mockStore.On("GetPurchasePlan", ctx, "plan-aws-456").Return(plan, nil).Twice()
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	var saved *config.PurchaseExecution
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).
		Run(func(args mock.Arguments) { saved = args.Get(1).(*config.PurchaseExecution) }).
		Return(nil)
	mockStore.On("UpdatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)
	mockSTS.On("GetCallerIdentity", ctx, mock.AnythingOfType("*sts.GetCallerIdentityInput")).Return(&sts.GetCallerIdentityOutput{
		Account: aws.String("123456789012"),
	}, nil)

	// executeSinglePurchase wraps ctx in a per-rec WithTimeout before calling
	// CreateAndValidateProvider, so we must match any context, not ctx itself.
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", mock.Anything, common.ServiceEC2, "us-east-1").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", mock.Anything, mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(common.PurchaseResult{
		Success:      true,
		CommitmentID: "ri-idempotent-12345",
	}, nil)

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		stsClient:       mockSTS,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	recovered, err := manager.RecoverStrandedApprovals(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, recovered, "AWS-only strand must be counted as recovered after re-drive")

	require.NotNil(t, saved)
	assert.Equal(t, "completed", saved.Status, "successfully re-driven AWS execution must be completed, not failed")

	// The provider was reached: the re-drive called PurchaseCommitment exactly once.
	mockServiceClient.AssertCalled(t, "PurchaseCommitment", mock.Anything, mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions"))
	// TransitionExecutionStatus to "failed" must NOT have been called - we re-drove, not failed.
	mockStore.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	mockStore.AssertExpectations(t)
	mockEmail.AssertExpectations(t)
	mockSTS.AssertExpectations(t)
}

// TestManager_RecoverStrandedApprovals_MixedAWSAzureSafeFails verifies that a
// stranded execution containing both AWS and Azure recommendations falls through
// to the safe-fail path rather than being re-driven. Azure re-drive idempotency
// is blocked by issue #721; a mixed execution must never be auto-re-driven.
func TestManager_RecoverStrandedApprovals_MixedAWSAzureSafeFails(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)

	stranded := config.PurchaseExecution{
		ExecutionID: "exec-mixed",
		PlanID:      "plan-mixed",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 100.0, Selected: true},
			{Provider: "azure", Service: "reservations", ResourceType: "Standard_D4s_v3", Region: "eastus", Count: 1, UpfrontCost: 100.0, Selected: true},
		},
	}
	failedRow := stranded
	failedRow.Status = "failed"

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "exec-mixed", []string{"approved"}, "failed").
		Return(&failedRow, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)

	manager := &Manager{
		config:          mockStore,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	recovered, err := manager.RecoverStrandedApprovals(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, recovered)

	// No provider call: mixed execution falls through to safe-fail, not re-driven.
	mockFactory.AssertNotCalled(t, "CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything)
	mockStore.AssertExpectations(t)
}

// TestManager_RecoverStrandedApprovals_PureAzureSafeFails verifies that a stranded
// execution whose every recommendation targets Azure falls through to the safe-fail
// path and is never re-driven (issue #721 guard against future regression).
func TestManager_RecoverStrandedApprovals_PureAzureSafeFails(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)

	stranded := config.PurchaseExecution{
		ExecutionID: "exec-azure",
		PlanID:      "plan-azure",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			{Provider: "azure", Service: "reservations", ResourceType: "Standard_D4s_v3", Region: "eastus", Count: 2, UpfrontCost: 400.0, Selected: true},
		},
	}
	failedRow := stranded
	failedRow.Status = "failed"

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "exec-azure", []string{"approved"}, "failed").
		Return(&failedRow, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)

	manager := &Manager{
		config:          mockStore,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	recovered, err := manager.RecoverStrandedApprovals(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, recovered)

	// Safe-fail: Azure-only execution must never reach the provider.
	mockFactory.AssertNotCalled(t, "CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything)
	mockStore.AssertExpectations(t)
}

// TestManager_RecoverStrandedApprovals_LegacyNoExecutionIDSafeFails verifies that
// a stranded AWS execution with an empty ExecutionID (a pre-UUID legacy row) falls
// through to the safe-fail path. DeriveIdempotencyToken("", i) would produce the
// same token for every such row, making an auto-re-drive of legacy rows unsafe.
func TestManager_RecoverStrandedApprovals_LegacyNoExecutionIDSafeFails(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)

	stranded := config.PurchaseExecution{
		ExecutionID: "", // legacy row: no stable ID for token derivation
		PlanID:      "plan-legacy",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 200.0, Selected: true},
		},
	}
	failedRow := stranded
	failedRow.Status = "failed"

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "", []string{"approved"}, "failed").
		Return(&failedRow, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)

	manager := &Manager{
		config:          mockStore,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	recovered, err := manager.RecoverStrandedApprovals(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, recovered)

	// No provider call: legacy execution must not be re-driven without a stable token.
	mockFactory.AssertNotCalled(t, "CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything)
	mockStore.AssertExpectations(t)
}

// TestManager_RecoverStrandedApprovals_LateCompletionNotClobbered covers the
// race where the original interrupted run actually finalizes between the stale
// SELECT and the recovery UPDATE. TransitionExecutionStatus's atomic
// WHERE status='approved' returns an error (the row is no longer approved), so
// the sweep skips it - the genuine "completed" status is preserved and the row
// is not re-saved as failed.
func TestManager_RecoverStrandedApprovals_LateCompletionNotClobbered(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	stranded := config.PurchaseExecution{ExecutionID: "exec-raced", Status: "approved"}

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "exec-raced", []string{"approved"}, "failed").
		Return(nil, errors.New("execution exec-raced cannot transition from \"completed\" to \"failed\""))
	// When TransitionExecutionStatus fails the manager calls GetExecutionByID to
	// distinguish a race (row already left "approved") from a real store error.
	// Returning a "completed" row causes RecoverStrandedApprovals to skip the
	// execution, which is the behaviour this test asserts.
	mockStore.On("GetExecutionByID", ctx, "exec-raced").
		Return(&config.PurchaseExecution{ExecutionID: "exec-raced", Status: "completed"}, nil)

	manager := &Manager{config: mockStore, dashboardURL: "https://dashboard.example.com"}

	recovered, err := manager.RecoverStrandedApprovals(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, recovered, "a row that completed between SELECT and UPDATE is skipped, not failed")

	mockStore.AssertExpectations(t)
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}
