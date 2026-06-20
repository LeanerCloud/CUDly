package purchase

import (
	"context"
	"errors"
	"fmt"
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

	cfg := &ManagerConfig{
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
	defaults := Defaults{
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

	// claimAndExecute CASes the due row to "running" before executing (issue #1013).
	claimedExec := executions[0]
	claimedExec.Status = "running"
	mockStore.On("GetStaleApprovedExecutions", ctx, mock.Anything).Return([]config.PurchaseExecution{}, nil)
	mockStore.On("GetPendingExecutions", ctx).Return(executions, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "exec-123",
		[]string{"approved", "pending", "notified"}, "running", (*string)(nil)).Return(&claimedExec, nil)
	mockStore.On("GetPurchasePlan", ctx, "plan-456").Return(plan, nil).Once()
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	mockStore.On("IncrementPlanCurrentStep", ctx, "plan-456").Return(nil)
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
			Status:        "cancelled", //nolint:misspell // DB schema value 'cancelled' -- see migration 000001_initial_schema.up.sql
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

	// Canceled executions are skipped without being re-executed; processed counter
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

	claimedExec := executions[0]
	claimedExec.Status = "running"
	mockStore.On("GetStaleApprovedExecutions", ctx, mock.Anything).Return([]config.PurchaseExecution{}, nil)
	mockStore.On("GetPendingExecutions", ctx).Return(executions, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "exec-123",
		[]string{"approved", "pending", "notified"}, "running", (*string)(nil)).Return(&claimedExec, nil)
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
// for issue #632 safe-fail path: an Azure Savings Plans execution flipped to
// "approved" whose synchronous purchase run was interrupted before it finalized
// must NOT stay permanently "approved". Azure Savings Plans re-drive is unsafe
// because the OrderAlias API uses a timestamp-based alias name with no server-side
// idempotency key (#639), so the recovery sweep drives the row into a terminal
// "failed" state with a clear error and does NOT re-run the purchase (no
// provider/service-client calls), eliminating double-purchase risk.
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
			// Azure Savings Plans: safe-fail path because OrderAlias has no idempotency key.
			{Provider: "azure", Service: "savingsplans", ResourceType: "Compute", Region: "eastus", Count: 1, UpfrontCost: 500.0, Selected: true, Purchased: false},
		},
	}
	failedRow := stranded
	failedRow.Status = "failed"

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	// The atomic transition only flips rows still in "approved".
	mockStore.On("TransitionExecutionStatus", ctx, "exec-stranded", []string{"approved"}, "failed", (*string)(nil)).
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
	assert.Equal(t, "failed", saved.Status, "stranded Azure Savings Plans row must become terminally failed, never stay approved")
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
	mockStore.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

// TestManager_RecoverStrandedApprovals_AWSOnlyRedrives is the regression test for
// issue #632 Option 5: a stranded AWS-only execution with a durable ExecutionID is
// re-driven via executeAndFinalize rather than failed. All AWS executors honor
// opts.IdempotencyToken via DeriveIdempotencyToken(exec.ExecutionID, i), so the
// second call is a safe no-op on the AWS side and the row transitions directly to
// "completed" without requiring a manual Retry.
//
// The CAS claim (approved -> running) is expected before the re-drive call; only
// the winner of this CAS proceeds to executeAndFinalize, preventing concurrent
// sweeps from double-purchasing.
func TestManager_RecoverStrandedApprovals_AWSOnlyRedrives(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockSTS := new(MockSTSClient)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockServiceClient := new(MockServiceClient)

	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	t.Cleanup(func() { mockEmail.AssertExpectations(t) })
	t.Cleanup(func() { mockSTS.AssertExpectations(t) })
	t.Cleanup(func() { mockFactory.AssertExpectations(t) })
	t.Cleanup(func() { mockProvider.AssertExpectations(t) })
	t.Cleanup(func() { mockServiceClient.AssertExpectations(t) })

	stranded := config.PurchaseExecution{
		ExecutionID: "exec-aws-stranded",
		PlanID:      "plan-aws-456",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 200.0, Selected: true, Purchased: false},
		},
	}
	runningRow := stranded
	runningRow.Status = "running"

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
	// CAS claim: approved -> running. The re-drive proceeds only after winning this.
	mockStore.On("TransitionExecutionStatus", ctx, "exec-aws-stranded", []string{"approved"}, "running", (*string)(nil)).
		Return(&runningRow, nil)
	mockStore.On("GetPurchasePlan", ctx, "plan-aws-456").Return(plan, nil).Once()
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	var saved *config.PurchaseExecution
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).
		Run(func(args mock.Arguments) { saved = args.Get(1).(*config.PurchaseExecution) }).
		Return(nil)
	mockStore.On("IncrementPlanCurrentStep", ctx, "plan-aws-456").Return(nil)
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
	// The CAS claim (approved -> running) was called; "failed" transition was not.
	mockStore.AssertCalled(t, "TransitionExecutionStatus", ctx, "exec-aws-stranded", []string{"approved"}, "running", (*string)(nil))
	mockStore.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, "failed", mock.Anything)
}

// TestManager_RecoverStrandedApprovals_AzureReservationRedrives verifies that a
// stranded Azure reservation execution (compute, database, cache, etc.) is
// re-driven via executeAndFinalize rather than failed. All Azure reservation
// service clients call DoIdempotentPurchaseTwoStep with opts.IdempotencyToken
// (PR #729 / issue #721), so a re-drive with the same ExecutionID is a safe
// no-op on the Azure side (#639).
func TestManager_RecoverStrandedApprovals_AzureReservationRedrives(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockServiceClient := new(MockServiceClient)

	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	t.Cleanup(func() { mockEmail.AssertExpectations(t) })
	t.Cleanup(func() { mockFactory.AssertExpectations(t) })
	t.Cleanup(func() { mockProvider.AssertExpectations(t) })
	t.Cleanup(func() { mockServiceClient.AssertExpectations(t) })

	stranded := config.PurchaseExecution{
		ExecutionID: "exec-azure-res-stranded",
		PlanID:      "plan-azure-res",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			{Provider: "azure", Service: "compute", ResourceType: "Standard_D4s_v3", Region: "eastus", Count: 1, UpfrontCost: 300.0, Selected: true, Purchased: false},
		},
	}
	runningRow := stranded
	runningRow.Status = "running"

	plan := &config.PurchasePlan{
		ID:   "plan-azure-res",
		Name: "Azure Reservation Plan",
		RampSchedule: config.RampSchedule{
			CurrentStep: 0,
			TotalSteps:  2,
		},
	}

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	// CAS claim: approved -> running before re-drive.
	mockStore.On("TransitionExecutionStatus", ctx, "exec-azure-res-stranded", []string{"approved"}, "running", (*string)(nil)).
		Return(&runningRow, nil)
	mockStore.On("GetPurchasePlan", ctx, "plan-azure-res").Return(plan, nil).Once()
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	var saved *config.PurchaseExecution
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).
		Run(func(args mock.Arguments) { saved = args.Get(1).(*config.PurchaseExecution) }).
		Return(nil)
	mockStore.On("IncrementPlanCurrentStep", ctx, "plan-azure-res").Return(nil)

	mockFactory.On("CreateAndValidateProvider", mock.Anything, "azure", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", mock.Anything, common.ServiceCompute, "eastus").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", mock.Anything, mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(common.PurchaseResult{
		Success:      true,
		CommitmentID: "azure-res-idempotent-order-id",
	}, nil)

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	recovered, err := manager.RecoverStrandedApprovals(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, recovered, "Azure reservation strand must be counted as recovered after re-drive")

	require.NotNil(t, saved)
	assert.Equal(t, "completed", saved.Status, "successfully re-driven Azure reservation must be completed, not failed")

	// Provider was reached: re-drive called PurchaseCommitment exactly once.
	mockServiceClient.AssertCalled(t, "PurchaseCommitment", mock.Anything, mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions"))
	// CAS claim (approved -> running) was called; "failed" transition was not.
	mockStore.AssertCalled(t, "TransitionExecutionStatus", ctx, "exec-azure-res-stranded", []string{"approved"}, "running", (*string)(nil))
	mockStore.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, "failed", mock.Anything)
}

// TestManager_RecoverStrandedApprovals_GCPRedrives verifies that a stranded GCP
// compute execution is re-driven via executeAndFinalize rather than failed. The GCP
// compute client uses server-side RequestId + deterministic name from the
// IdempotencyToken (#654), so a re-drive with the same ExecutionID is a safe no-op
// on the GCP side (#639).
func TestManager_RecoverStrandedApprovals_GCPRedrives(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockServiceClient := new(MockServiceClient)

	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	t.Cleanup(func() { mockEmail.AssertExpectations(t) })
	t.Cleanup(func() { mockFactory.AssertExpectations(t) })
	t.Cleanup(func() { mockProvider.AssertExpectations(t) })
	t.Cleanup(func() { mockServiceClient.AssertExpectations(t) })

	stranded := config.PurchaseExecution{
		ExecutionID: "exec-gcp-stranded",
		PlanID:      "plan-gcp",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			{Provider: "gcp", Service: "compute", ResourceType: "n2-standard-4", Region: "us-central1", Count: 2, UpfrontCost: 150.0, Selected: true, Purchased: false},
		},
	}
	runningRow := stranded
	runningRow.Status = "running"

	plan := &config.PurchasePlan{
		ID:   "plan-gcp",
		Name: "GCP CUD Plan",
		RampSchedule: config.RampSchedule{
			CurrentStep: 0,
			TotalSteps:  2,
		},
	}

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	// CAS claim: approved -> running before re-drive.
	mockStore.On("TransitionExecutionStatus", ctx, "exec-gcp-stranded", []string{"approved"}, "running", (*string)(nil)).
		Return(&runningRow, nil)
	mockStore.On("GetPurchasePlan", ctx, "plan-gcp").Return(plan, nil).Once()
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	var saved *config.PurchaseExecution
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).
		Run(func(args mock.Arguments) { saved = args.Get(1).(*config.PurchaseExecution) }).
		Return(nil)
	mockStore.On("IncrementPlanCurrentStep", ctx, "plan-gcp").Return(nil)

	mockFactory.On("CreateAndValidateProvider", mock.Anything, "gcp", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", mock.Anything, common.ServiceCompute, "us-central1").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", mock.Anything, mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(common.PurchaseResult{
		Success:      true,
		CommitmentID: "cud-idempotent-gcp",
	}, nil)

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	recovered, err := manager.RecoverStrandedApprovals(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, recovered, "GCP strand must be counted as recovered after re-drive")

	require.NotNil(t, saved)
	assert.Equal(t, "completed", saved.Status, "successfully re-driven GCP execution must be completed, not failed")

	// Provider was reached: re-drive called PurchaseCommitment exactly once.
	mockServiceClient.AssertCalled(t, "PurchaseCommitment", mock.Anything, mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions"))
	// CAS claim (approved -> running) was called; "failed" transition was not.
	mockStore.AssertCalled(t, "TransitionExecutionStatus", ctx, "exec-gcp-stranded", []string{"approved"}, "running", (*string)(nil))
	mockStore.AssertNotCalled(t, "TransitionExecutionStatus", mock.Anything, mock.Anything, mock.Anything, "failed", mock.Anything)
}

// TestManager_RecoverStrandedApprovals_MixedAWSAzureSPSafeFails verifies that a
// stranded execution containing AWS and Azure Savings Plans recommendations falls
// through to the safe-fail path rather than being re-driven. Azure Savings Plans
// uses a timestamp-based alias name with no idempotency key, so a mixed execution
// that includes at least one savings-plans rec must never be auto-re-driven.
func TestManager_RecoverStrandedApprovals_MixedAWSAzureSPSafeFails(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)

	stranded := config.PurchaseExecution{
		ExecutionID: "exec-mixed-sp",
		PlanID:      "plan-mixed-sp",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 100.0, Selected: true},
			// Azure Savings Plans: not safe for re-drive; entire execution falls to safe-fail.
			{Provider: "azure", Service: "savingsplans", ResourceType: "Compute", Region: "eastus", Count: 1, UpfrontCost: 100.0, Selected: true},
		},
	}
	failedRow := stranded
	failedRow.Status = "failed"

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "exec-mixed-sp", []string{"approved"}, "failed", (*string)(nil)).
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

	// No provider call: execution with savings-plans rec falls through to safe-fail.
	mockFactory.AssertNotCalled(t, "CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything)
	mockStore.AssertExpectations(t)
}

// TestManager_RecoverStrandedApprovals_AzureSavingsPlansSafeFails verifies that a
// stranded execution whose every recommendation targets Azure Savings Plans falls
// through to the safe-fail path and is never re-driven. The OrderAlias API uses
// a timestamp-based alias name with no server-side idempotency key, so re-driving
// would create a duplicate savings plan (#639).
func TestManager_RecoverStrandedApprovals_AzureSavingsPlansSafeFails(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)

	stranded := config.PurchaseExecution{
		ExecutionID: "exec-azure-sp",
		PlanID:      "plan-azure-sp",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			{Provider: "azure", Service: "savingsplans", ResourceType: "Compute", Region: "eastus", Count: 2, UpfrontCost: 400.0, Selected: true},
		},
	}
	failedRow := stranded
	failedRow.Status = "failed"

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "exec-azure-sp", []string{"approved"}, "failed", (*string)(nil)).
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

	// Safe-fail: Azure Savings Plans execution must never reach the provider.
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
	mockStore.On("TransitionExecutionStatus", ctx, "", []string{"approved"}, "failed", (*string)(nil)).
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
	mockStore.On("TransitionExecutionStatus", ctx, "exec-raced", []string{"approved"}, "failed", (*string)(nil)).
		Return(nil, errors.New("execution exec-raced cannot transition from \"completed\" to \"failed\""))
	// When TransitionExecutionStatus fails the manager calls GetExecutionByID to
	// distinguish a race (row already left "approved") from a real store error.
	// Returning a "completed" row causes RecoverStrandedApprovals to skip the
	// execution, which is the behavior this test asserts.
	mockStore.On("GetExecutionByID", ctx, "exec-raced").
		Return(&config.PurchaseExecution{ExecutionID: "exec-raced", Status: "completed"}, nil)

	manager := &Manager{config: mockStore, dashboardURL: "https://dashboard.example.com"}

	recovered, err := manager.RecoverStrandedApprovals(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, recovered, "a row that completed between SELECT and UPDATE is skipped, not failed")

	mockStore.AssertExpectations(t)
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

// TestManager_RecoverStrandedApprovals_SafeFail_ErrNotFoundIsBenign verifies
// that when TransitionExecutionStatus returns config.ErrNotFound (row deleted
// between the stale SELECT and the CAS) the safe-fail path returns (false, nil)
// without calling GetExecutionByID. This avoids a pointless read on a row that
// no longer exists and treats the disappearance as a benign race-loss.
func TestManager_RecoverStrandedApprovals_SafeFail_ErrNotFoundIsBenign(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	stranded := config.PurchaseExecution{
		ExecutionID: "exec-vanished",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			// Azure Savings Plans falls through to safe-fail path (no idempotency key).
			{Provider: "azure", Service: "savingsplans", ResourceType: "Compute", Region: "eastus", Count: 1, UpfrontCost: 400.0, Selected: true},
		},
	}

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	// TransitionExecutionStatus wraps "row vanished" as config.ErrNotFound.
	mockStore.On("TransitionExecutionStatus", ctx, "exec-vanished", []string{"approved"}, "failed", (*string)(nil)).
		Return(nil, fmt.Errorf("%w: execution exec-vanished", config.ErrNotFound))

	manager := &Manager{config: mockStore, dashboardURL: "https://dashboard.example.com"}

	recovered, err := manager.RecoverStrandedApprovals(ctx)
	require.NoError(t, err, "ErrNotFound from CAS must not propagate as a sweep error")
	assert.Equal(t, 0, recovered, "a vanished row contributes nothing to the recovery count")

	// GetExecutionByID must NOT be called: ErrNotFound already tells us the row
	// is gone; the probe would be a redundant round-trip and the early branch
	// avoids it.
	mockStore.AssertNotCalled(t, "GetExecutionByID", mock.Anything, mock.Anything)
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

// TestManager_RecoverStrandedApprovals_SafeFail_ErrExecutionNotInExpectedStatusIsBenign
// verifies that when TransitionExecutionStatus returns
// config.ErrExecutionNotInExpectedStatus (the row exists but its status has
// already moved out of "approved" -- a concurrent sweep or the original run
// won the CAS) safeFail returns (false, nil) WITHOUT calling GetExecutionByID.
// Re-reading in this case is unnecessary: the sentinel already tells us the row
// is in some non-approved state. Probing can also turn the benign race into a
// hard sweep error if the second read flakes or the row disappears between the
// CAS rejection and the probe. This matches how claimAndRedrive and reaper.go
// already treat ErrExecutionNotInExpectedStatus as terminally benign.
func TestManager_RecoverStrandedApprovals_SafeFail_ErrExecutionNotInExpectedStatusIsBenign(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	stranded := config.PurchaseExecution{
		ExecutionID: "exec-already-transitioned",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			// Azure Savings Plans routes to safe-fail (no idempotency key for re-drive).
			{Provider: "azure", Service: "savingsplans", ResourceType: "Compute", Region: "eastus", Count: 1, UpfrontCost: 400.0, Selected: true},
		},
	}

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	// TransitionExecutionStatus signals the row is in a non-approved state.
	mockStore.On("TransitionExecutionStatus", ctx, "exec-already-transitioned", []string{"approved"}, "failed", (*string)(nil)).
		Return(nil, fmt.Errorf("%w: execution exec-already-transitioned status is completed", config.ErrExecutionNotInExpectedStatus))

	manager := &Manager{config: mockStore, dashboardURL: "https://dashboard.example.com"}

	recovered, err := manager.RecoverStrandedApprovals(ctx)
	require.NoError(t, err, "ErrExecutionNotInExpectedStatus from CAS must not propagate as a sweep error")
	assert.Equal(t, 0, recovered, "a row already out of approved contributes nothing to the recovery count")

	// GetExecutionByID must NOT be called: ErrExecutionNotInExpectedStatus already
	// tells us the row has moved on; the probe is a redundant round-trip and the
	// early branch avoids it (mirroring the ErrNotFound short-circuit above it).
	mockStore.AssertNotCalled(t, "GetExecutionByID", mock.Anything, mock.Anything)
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

// TestManager_RecoverStrandedApprovals_AWSRedrive_PersistenceFailurePropagates is
// the regression test for the CR #728 round-2 finding: when executeAndFinalize's
// SavePurchaseExecution fails AFTER the CAS-to-running succeeded, claimAndRedrive
// must NOT return (false, nil). The row is now "running" in the DB with no
// terminal state persisted -- silently dropping the error would strand it there
// indefinitely. The sweep must surface the error (ErrAuditLoss) so the caller
// can stop processing and the next tick retries the recovery.
func TestManager_RecoverStrandedApprovals_AWSRedrive_PersistenceFailurePropagates(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockSTS := new(MockSTSClient)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockServiceClient := new(MockServiceClient)

	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	t.Cleanup(func() { mockEmail.AssertExpectations(t) })
	t.Cleanup(func() { mockSTS.AssertExpectations(t) })
	t.Cleanup(func() { mockFactory.AssertExpectations(t) })
	t.Cleanup(func() { mockProvider.AssertExpectations(t) })
	t.Cleanup(func() { mockServiceClient.AssertExpectations(t) })

	stranded := config.PurchaseExecution{
		ExecutionID: "exec-aws-persist-fail",
		PlanID:      "plan-aws-persist-456",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 200.0, Selected: true, Purchased: false},
		},
	}
	runningRow := stranded
	runningRow.Status = "running"

	plan := &config.PurchasePlan{
		ID:   "plan-aws-persist-456",
		Name: "AWS Persist-Fail Test Plan",
		RampSchedule: config.RampSchedule{
			CurrentStep: 0,
			TotalSteps:  4,
		},
	}

	saveErr := errors.New("DB connection lost")

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	// CAS claim: approved -> running. This succeeds -- the row is now "running".
	mockStore.On("TransitionExecutionStatus", ctx, "exec-aws-persist-fail", []string{"approved"}, "running", (*string)(nil)).
		Return(&runningRow, nil)
	mockStore.On("GetPurchasePlan", ctx, "plan-aws-persist-456").Return(plan, nil).Once()
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	// SavePurchaseExecution fails AFTER the CAS-to-running succeeded.
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).
		Return(saveErr)

	mockSTS.On("GetCallerIdentity", ctx, mock.AnythingOfType("*sts.GetCallerIdentityInput")).Return(&sts.GetCallerIdentityOutput{
		Account: aws.String("123456789012"),
	}, nil)
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", mock.Anything, common.ServiceEC2, "us-east-1").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", mock.Anything, mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(common.PurchaseResult{
		Success:      true,
		CommitmentID: "ri-persist-fail-12345",
	}, nil)

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		stsClient:       mockSTS,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	recovered, err := manager.RecoverStrandedApprovals(ctx)
	// The persistence failure must surface as an error, not be silently dropped.
	require.Error(t, err, "SavePurchaseExecution failure after CAS-to-running must not be silently dropped")
	assert.ErrorIs(t, err, config.ErrAuditLoss, "error must wrap ErrAuditLoss so callers can classify it")
	assert.Equal(t, 0, recovered, "a row that failed to persist must not be counted as recovered")
}

// TestManager_RecoverStrandedApprovals_AWSRedrive_ExecAndPersistBothFail is the
// regression test for CR #728 round-3: when executePurchase returns a non-nil
// error (e.g. partialPurchaseError or a plain provider error) AND
// SavePurchaseExecution then also fails, the old guard (execErr == nil) silently
// dropped the save failure. The row was left stranded in "running" because no
// terminal status was persisted.
//
// After the fix, any SavePurchaseExecution failure is wrapped as ErrAuditLoss
// regardless of whether executePurchase itself returned an error. The test
// verifies:
//   - RecoverStrandedApprovals returns a non-nil error (not silently dropped)
//   - errors.Is(err, config.ErrAuditLoss) is true (sentinel is present)
//   - the original execution error (plan lookup failure) is still reachable via
//     errors.As / errors.Unwrap so callers can inspect the root cause
func TestManager_RecoverStrandedApprovals_AWSRedrive_ExecAndPersistBothFail(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	t.Cleanup(func() { mockEmail.AssertExpectations(t) })

	stranded := config.PurchaseExecution{
		ExecutionID: "exec-aws-both-fail",
		PlanID:      "plan-aws-both-fail",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 200.0, Selected: true, Purchased: false},
		},
	}
	runningRow := stranded
	runningRow.Status = "running"

	// planErr is the error returned by executePurchase (simulates a DB blip
	// during plan lookup that makes the purchase fail).
	planErr := errors.New("plan DB read timeout")
	saveErr := errors.New("terminal save DB connection lost")

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	// CAS claim succeeds: the row is now "running" with no terminal state yet.
	mockStore.On("TransitionExecutionStatus", ctx, "exec-aws-both-fail", []string{"approved"}, "running", (*string)(nil)).
		Return(&runningRow, nil)
	// executePurchase calls GetPurchasePlan; make it fail so execErr != nil.
	mockStore.On("GetPurchasePlan", ctx, "plan-aws-both-fail").Return(nil, planErr).Once()
	// SavePurchaseExecution also fails: the row remains "running" in the DB.
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).
		Return(saveErr)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	recovered, err := manager.RecoverStrandedApprovals(ctx)

	// The combined failure must surface -- neither the exec error nor the save
	// error may be silently swallowed.
	require.Error(t, err, "both exec and save failure must not be silently dropped")
	assert.ErrorIs(t, err, config.ErrAuditLoss,
		"error must wrap ErrAuditLoss so claimAndRedrive surfaces it to the sweep")

	// The original execution error must remain reachable so operators can
	// diagnose the root cause without reading logs.
	assert.ErrorIs(t, err, planErr,
		"original execution error (plan lookup failure) must be reachable via errors.Is")

	assert.Equal(t, 0, recovered, "a row with both exec and save failures must not be counted as recovered")
}

// TestManager_RecoverStrandedApprovals_AWSClaimLost_NoRedrive verifies that when
// the CAS claim (approved -> running) fails with ErrExecutionNotInExpectedStatus
// the AWS re-drive path does NOT call executeAndFinalize. Only the sweep that
// wins the CAS should ever re-drive; the loser must skip silently. This prevents
// two overlapping sweeps from both calling executeAndFinalize and potentially
// double-purchasing.
func TestManager_RecoverStrandedApprovals_AWSClaimLost_NoRedrive(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	t.Cleanup(func() { mockFactory.AssertExpectations(t) })

	stranded := config.PurchaseExecution{
		ExecutionID: "exec-aws-claimed",
		PlanID:      "plan-aws-claimed",
		Status:      "approved",
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, UpfrontCost: 300.0, Selected: true},
		},
	}

	mockStore.On("GetStaleApprovedExecutions", ctx, staleApprovedThreshold).
		Return([]config.PurchaseExecution{stranded}, nil)
	// A concurrent sweep has already claimed the row (status changed to "running"),
	// so our CAS (approved -> running) is rejected as ErrExecutionNotInExpectedStatus.
	mockStore.On("TransitionExecutionStatus", ctx, "exec-aws-claimed", []string{"approved"}, "running", (*string)(nil)).
		Return(nil, fmt.Errorf("%w: execution exec-aws-claimed cannot transition from \"running\" to \"running\"", config.ErrExecutionNotInExpectedStatus))

	manager := &Manager{
		config:          mockStore,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	recovered, err := manager.RecoverStrandedApprovals(ctx)
	require.NoError(t, err, "losing the CAS claim must not propagate as a sweep error")
	assert.Equal(t, 0, recovered, "a row claimed by a concurrent sweep must not be counted by the loser")

	// The loser must never reach the provider - that would be a double-purchase.
	mockFactory.AssertNotCalled(t, "CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything)
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}
