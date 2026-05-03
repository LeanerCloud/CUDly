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
	mockServiceClient.On("PurchaseCommitment", ctx, mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(common.PurchaseResult{
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

	_, err := manager.executePurchase(ctx, exec)
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

// TestManager_ExecutePurchase_WebSourcePropagates covers the gap noted in the
// plan audit: the CLI path has a source-asserting test via TestExecutePurchase,
// but the web path (web handler → DB → Manager.executePurchase → provider)
// needs its own check that exec.Source ends up as opts.Source on the
// provider's PurchaseCommitment call.
func TestManager_ExecutePurchase_WebSourcePropagates(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockSTS := new(MockSTSClient)
	mockFactory := new(MockProviderFactory)
	mockProviderInst := new(MockProvider)
	mockServiceClient := new(MockServiceClient)

	plan := &config.PurchasePlan{ID: "plan-web", Name: "Web Plan"}
	exec := &config.PurchaseExecution{
		ExecutionID: "exec-web",
		PlanID:      "plan-web",
		StepNumber:  1,
		Source:      common.PurchaseSourceWeb,
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, Selected: true},
		},
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-web").Return(plan, nil)
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	mockSTS.On("GetCallerIdentity", ctx, mock.AnythingOfType("*sts.GetCallerIdentityInput")).Return(&sts.GetCallerIdentityOutput{
		Account: aws.String("123456789012"),
	}, nil)
	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProviderInst, nil)
	mockProviderInst.On("GetServiceClient", ctx, common.ServiceEC2, "us-east-1").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", ctx,
		mock.AnythingOfType("common.Recommendation"),
		common.PurchaseOptions{Source: common.PurchaseSourceWeb},
	).Return(common.PurchaseResult{Success: true, CommitmentID: "ri-web"}, nil)

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		stsClient:       mockSTS,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	_, err := manager.executePurchase(ctx, exec)
	require.NoError(t, err)
	mockServiceClient.AssertExpectations(t)
}

// TestManager_ExecutePurchase_InvalidSourceFallsBackUntagged verifies the
// NormalizeSource defence-in-depth: a DB row with an unexpected source value
// proceeds with an empty source (untagged) rather than failing the already-
// approved execution or poisoning cloud tags with arbitrary strings.
func TestManager_ExecutePurchase_InvalidSourceFallsBackUntagged(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockSTS := new(MockSTSClient)
	mockFactory := new(MockProviderFactory)
	mockProviderInst := new(MockProvider)
	mockServiceClient := new(MockServiceClient)

	plan := &config.PurchasePlan{ID: "plan-bad", Name: "Bad Plan"}
	exec := &config.PurchaseExecution{
		ExecutionID: "exec-bad",
		PlanID:      "plan-bad",
		StepNumber:  1,
		Source:      "cudly-evil", // not in whitelist — should be rejected
		Recommendations: []config.RecommendationRecord{
			{Provider: "aws", Service: "ec2", ResourceType: "m5.large", Region: "us-east-1", Count: 1, Selected: true},
		},
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-bad").Return(plan, nil)
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	mockSTS.On("GetCallerIdentity", ctx, mock.AnythingOfType("*sts.GetCallerIdentityInput")).Return(&sts.GetCallerIdentityOutput{
		Account: aws.String("123456789012"),
	}, nil)
	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProviderInst, nil)
	mockProviderInst.On("GetServiceClient", ctx, common.ServiceEC2, "us-east-1").Return(mockServiceClient, nil)
	// Expect EMPTY source, not "cudly-evil" — NormalizeSource must have wiped it.
	mockServiceClient.On("PurchaseCommitment", ctx,
		mock.AnythingOfType("common.Recommendation"),
		common.PurchaseOptions{Source: ""},
	).Return(common.PurchaseResult{Success: true, CommitmentID: "ri-bad"}, nil)

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		stsClient:       mockSTS,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	_, err := manager.executePurchase(ctx, exec)
	require.NoError(t, err)
	mockServiceClient.AssertExpectations(t)
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

	_, err := manager.executePurchase(ctx, exec)
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

	_, err := manager.executePurchase(ctx, exec)
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

	_, err := manager.executePurchase(ctx, exec)
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
	credStore := &MockCredentialStore{
		LoadRawFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte(`{"access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_access_key":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}`), nil
		},
	}

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
	mockServiceClient.On("PurchaseCommitment", ctx, mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(common.PurchaseResult{
		Success: true, CommitmentID: "ri-99999",
	}, nil)

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		providerFactory: mockFactory,
		credStore:       credStore,
		// assumeRoleSTS is nil → access_keys path, no role assumption needed
	}

	_, err := manager.executePurchase(ctx, exec)
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

// TestExecuteForAccount_CredentialFailure_MarksFailed locks down the invariant
// that a credential-resolution error is a hard failure: the per-account
// execution record must be saved with Status="failed", the provider factory
// must NEVER be invoked (no ambient fallback), and executePurchase must
// surface the error. The production bug this guards was fixed in 9531681a4
// across AWS/Azure/GCP; this test ensures a future refactor cannot silently
// reintroduce the ambient fallback.
func TestExecuteForAccount_CredentialFailure_MarksFailed(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		account  config.CloudAccount
	}{
		{
			name:     "aws access_keys credentials missing",
			provider: "aws",
			account: config.CloudAccount{
				ID: "aaaaaaaa-0000-0000-0000-000000000001", Name: "aws-failing",
				Provider: "aws", ExternalID: "111111111111", AWSAuthMode: "access_keys",
			},
		},
		{
			name:     "azure client_secret credentials missing",
			provider: "azure",
			account: config.CloudAccount{
				ID: "bbbbbbbb-0000-0000-0000-000000000002", Name: "azure-failing",
				Provider: "azure", ExternalID: "sub-222", AzureAuthMode: "client_secret",
				AzureTenantID: "tenant-222", AzureClientID: "client-222",
			},
		},
		{
			name:     "gcp service_account credentials missing",
			provider: "gcp",
			account: config.CloudAccount{
				ID: "cccccccc-0000-0000-0000-000000000003", Name: "gcp-failing",
				Provider: "gcp", ExternalID: "proj-333", GCPAuthMode: "service_account_key",
				GCPProjectID: "proj-333",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockStore := new(MockConfigStore)
			mockEmail := new(MockEmailSender)
			mockFactory := new(MockProviderFactory)
			// Credential store returns nothing for every load — this triggers
			// the "no credentials stored" error path in the resolver for all
			// three providers.
			credStore := &MockCredentialStore{
				LoadRawFn: func(_ context.Context, _, _ string) ([]byte, error) {
					return nil, nil
				},
			}

			plan := &config.PurchasePlan{
				ID:           "plan-credfail",
				Name:         "Credential Failure Plan",
				RampSchedule: config.RampSchedule{CurrentStep: 0, TotalSteps: 1},
			}
			rec := config.RecommendationRecord{
				Provider: tt.provider, Service: "ec2", ResourceType: "m5.large",
				Region: "us-east-1", Count: 1, Savings: 75.0, UpfrontCost: 300.0, Selected: true,
			}
			exec := &config.PurchaseExecution{
				ExecutionID:     "exec-credfail",
				PlanID:          "plan-credfail",
				Status:          "pending",
				Recommendations: []config.RecommendationRecord{rec},
			}

			mockStore.On("GetPurchasePlan", ctx, "plan-credfail").Return(plan, nil)
			mockStore.GetPlanAccountsFn = func(_ context.Context, _ string) ([]config.CloudAccount, error) {
				return []config.CloudAccount{tt.account}, nil
			}
			var saved []*config.PurchaseExecution
			var mu sync.Mutex
			mockStore.SavePurchaseExecutionFn = func(_ context.Context, e *config.PurchaseExecution) error {
				mu.Lock()
				saved = append(saved, e)
				mu.Unlock()
				return nil
			}

			manager := &Manager{
				config:          mockStore,
				email:           mockEmail,
				providerFactory: mockFactory,
				credStore:       credStore,
			}

			_, err := manager.executePurchase(ctx, exec)

			// Must surface the credential failure — no ambient fallback.
			require.Error(t, err)
			assert.Contains(t, err.Error(), "credential resolution failed")

			// Per-account execution record must be persisted with Status=failed
			// so the audit trail reflects the failure.
			require.NotEmpty(t, saved, "SavePurchaseExecution should be called for the failed account")
			assert.Equal(t, "failed", saved[0].Status)
			assert.NotEmpty(t, saved[0].Error, "failed execution record must carry the error message")

			// The provider factory must NEVER be invoked when credentials
			// fail to resolve. If this fires, something is attempting an
			// ambient-credential fallback — exactly the bug 9531681a4 closed.
			mockFactory.AssertNotCalled(t, "CreateAndValidateProvider")
		})
	}
}

// TestExecuteMultiAccount_PartialFailure_IsolatesAccounts is the regression
// guard for spec E-2: when one account (account-I) has invalid credentials and
// another (account-V) has valid credentials, account-V's execution must
// complete successfully and its record must be independent of account-I's failure.
//
// What this test pins:
//   - Two SavePurchaseExecution calls fire (one per account — no record is skipped).
//   - account-V's record: Status=="completed", PurchaseID set (purchase went through).
//   - account-I's record: Status=="failed", Error non-empty.
//   - account-I's error text does NOT contain account-V's credential material
//     (cross-account data isolation / log-sanitisation guard).
//   - account-V's CommitmentID does NOT appear in account-I's error text
//     (result_data independence).
//   - The overall executePurchase call returns an error (aggregated from I) —
//     proving the errgroup collected I's failure — while V's record was still
//     saved as "completed" (proving the errgroup did NOT short-circuit on I).
func TestExecuteMultiAccount_PartialFailure_IsolatesAccounts(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProviderInst := new(MockProvider)
	mockServiceClient := new(MockServiceClient)

	// account-V: valid mock credentials (access_keys path, LoadRaw returns key JSON).
	// account-I: invalid mock credentials (LoadRaw returns nil → "no credentials" resolver error).
	const (
		accountVID  = "vvvvvvvv-0000-0000-0000-000000000001"
		accountIID  = "iiiiiiii-0000-0000-0000-000000000002"
		accountVKey = "AKIAIOSFODNN7EXAMPLE" // sentinel — must not appear in I's error
		commitmentV = "ri-valid-001"         // V's CommitmentID — must not appear in I's error
	)

	accounts := []config.CloudAccount{
		{ID: accountVID, Name: "Valid", Provider: "aws", ExternalID: "111111111111", AWSAuthMode: "access_keys"},
		{ID: accountIID, Name: "Invalid", Provider: "aws", ExternalID: "222222222222", AWSAuthMode: "access_keys"},
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
		ExecutionID:     "exec-partial",
		PlanID:          "plan-partial",
		Status:          "pending",
		Recommendations: []config.RecommendationRecord{rec},
	}

	plan := &config.PurchasePlan{
		ID:           "plan-partial",
		Name:         "Partial Failure Plan",
		RampSchedule: config.RampSchedule{CurrentStep: 0, TotalSteps: 1},
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-partial").Return(plan, nil)
	mockStore.GetPlanAccountsFn = func(_ context.Context, _ string) ([]config.CloudAccount, error) {
		return accounts, nil
	}

	// Collect saved execution records concurrency-safely (goroutine fan-out).
	var savedExecs []*config.PurchaseExecution
	var mu sync.Mutex
	mockStore.SavePurchaseExecutionFn = func(_ context.Context, e *config.PurchaseExecution) error {
		mu.Lock()
		// Copy the record before appending — executeForAccount reuses the acctExec
		// struct value but Recommendations slice is already deep-copied; capture
		// the status and error which are set before Save is called.
		saved := *e
		savedExecs = append(savedExecs, &saved)
		mu.Unlock()
		return nil
	}

	// Only account-V reaches the provider; account-I fails at credential resolution.
	// Use .Once() so testify fails if the factory is called for account-I as well.
	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProviderInst, nil).Once()
	mockProviderInst.On("GetServiceClient", ctx, common.ServiceEC2, "us-east-1").Return(mockServiceClient, nil).Once()
	mockServiceClient.On("PurchaseCommitment", ctx,
		mock.AnythingOfType("common.Recommendation"),
		mock.AnythingOfType("common.PurchaseOptions"),
	).Return(common.PurchaseResult{Success: true, CommitmentID: commitmentV}, nil).Once()

	// Only account-V triggers history + notification.
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil).Once()
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil).Once()

	// Credential store: V gets valid key JSON; I gets nil (no credentials stored).
	credStore := &MockCredentialStore{
		LoadRawFn: func(_ context.Context, accountID, _ string) ([]byte, error) {
			if accountID == accountVID {
				return []byte(`{"access_key_id":"` + accountVKey + `","secret_access_key":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}`), nil
			}
			// account-I: no credentials stored → resolver returns error.
			return nil, nil
		},
	}

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		providerFactory: mockFactory,
		credStore:       credStore,
	}

	_, err := manager.executePurchase(ctx, exec)

	// The call must return an error (account-I's failure is aggregated).
	// This proves the errgroup collected the failure rather than discarding it.
	require.Error(t, err, "executePurchase must surface account-I's credential failure")

	// Both per-account records must have been saved (no silent skip).
	require.Len(t, savedExecs, 2, "exactly two SavePurchaseExecution calls expected — one per account")

	// Build a map keyed by cloud_account_id for order-independent lookup.
	byAccount := make(map[string]*config.PurchaseExecution, 2)
	for _, e := range savedExecs {
		require.NotNil(t, e.CloudAccountID, "every saved record must have cloud_account_id set")
		byAccount[*e.CloudAccountID] = e
	}

	// --- account-V assertions (success path must be unaffected by I's failure) ---
	recordV, ok := byAccount[accountVID]
	require.True(t, ok, "account-V's execution record must be saved")
	assert.Equal(t, "completed", recordV.Status, "account-V must complete successfully")
	assert.Empty(t, recordV.Error, "account-V's record must carry no error message")

	// Verify the purchase went through: at least one recommendation must be marked Purchased.
	purchasedV := false
	for _, r := range recordV.Recommendations {
		if r.Purchased {
			purchasedV = true
			assert.Equal(t, commitmentV, r.PurchaseID, "account-V's commitment ID must match the mock return value")
		}
	}
	assert.True(t, purchasedV, "account-V must have at least one purchased recommendation")

	// --- account-I assertions (failure path must be correctly recorded) ---
	recordI, ok := byAccount[accountIID]
	require.True(t, ok, "account-I's execution record must be saved")
	assert.Equal(t, "failed", recordI.Status, "account-I must be marked failed")
	assert.NotEmpty(t, recordI.Error, "account-I's record must carry an error message")

	// Log-sanitisation guard: account-I's error must not contain account-V's
	// credential material (cross-account data leak would be a security regression).
	assert.NotContains(t, recordI.Error, accountVKey,
		"account-I's error must not contain account-V's access key (cross-account credential leak)")

	// Result-data independence: account-V's CommitmentID must not bleed into
	// account-I's error or recommendation error fields.
	assert.NotContains(t, recordI.Error, commitmentV,
		"account-I's error must not reference account-V's commitment ID")
	for _, r := range recordI.Recommendations {
		assert.NotContains(t, r.Error, commitmentV,
			"account-I's recommendation errors must not reference account-V's commitment ID")
	}

	// The base exec record must remain untagged (fan-out creates per-account copies).
	assert.Nil(t, exec.CloudAccountID, "base execution record must have nil cloud_account_id after fan-out")

	mockStore.AssertExpectations(t)
	mockEmail.AssertExpectations(t)
	mockFactory.AssertExpectations(t)
	mockProviderInst.AssertExpectations(t)
	mockServiceClient.AssertExpectations(t)
}

// TestExecuteMultiAccount_RunsAccountsInParallel pins the spec E-2 parallelism
// promise: a multi-account plan must fan out concurrently, not iterate serially.
// The companion TestExecuteMultiAccount_PartialFailure_IsolatesAccounts above
// proves per-account error isolation but would still pass if executeMultiAccount
// were refactored to a serial for-loop. This test fails on a serial loop.
//
// Mechanics: two accounts, each with valid credentials, each blocking inside
// PurchaseCommitment for perCallDelay. Parallel fan-out completes in roughly
// perCallDelay; a serial loop would require ~2 * perCallDelay. The threshold
// is set comfortably between the two so a slow CI runner does not flake on
// parallel scheduling overhead but a serial-loop regression is caught.
func TestExecuteMultiAccount_RunsAccountsInParallel(t *testing.T) {
	const (
		perCallDelay     = 300 * time.Millisecond
		serialBoundary   = 2 * perCallDelay // serial fan-out lower bound: 600ms
		parallelMaxBound = 500 * time.Millisecond
	)

	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProviderInst := new(MockProvider)
	mockServiceClient := new(MockServiceClient)

	const (
		accountAID = "aaaaaaaa-0000-0000-0000-000000000001"
		accountBID = "bbbbbbbb-0000-0000-0000-000000000002"
	)

	accounts := []config.CloudAccount{
		{ID: accountAID, Name: "A", Provider: "aws", ExternalID: "111111111111", AWSAuthMode: "access_keys"},
		{ID: accountBID, Name: "B", Provider: "aws", ExternalID: "222222222222", AWSAuthMode: "access_keys"},
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
		ExecutionID:     "exec-parallel",
		PlanID:          "plan-parallel",
		Status:          "pending",
		Recommendations: []config.RecommendationRecord{rec},
	}

	plan := &config.PurchasePlan{
		ID:           "plan-parallel",
		Name:         "Parallel Fan-Out Plan",
		RampSchedule: config.RampSchedule{CurrentStep: 0, TotalSteps: 1},
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-parallel").Return(plan, nil)
	mockStore.GetPlanAccountsFn = func(_ context.Context, _ string) ([]config.CloudAccount, error) {
		return accounts, nil
	}

	var savedMu sync.Mutex
	var savedExecs []*config.PurchaseExecution
	mockStore.SavePurchaseExecutionFn = func(_ context.Context, e *config.PurchaseExecution) error {
		savedMu.Lock()
		saved := *e
		savedExecs = append(savedExecs, &saved)
		savedMu.Unlock()
		return nil
	}

	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProviderInst, nil).Twice()
	mockProviderInst.On("GetServiceClient", ctx, common.ServiceEC2, "us-east-1").Return(mockServiceClient, nil).Twice()

	// Each PurchaseCommitment call blocks for perCallDelay. Under serial
	// execution the total elapsed time would exceed serialBoundary; under
	// errgroup-style parallelism it stays close to perCallDelay.
	mockServiceClient.On("PurchaseCommitment", ctx,
		mock.AnythingOfType("common.Recommendation"),
		mock.AnythingOfType("common.PurchaseOptions"),
	).Return(common.PurchaseResult{Success: true, CommitmentID: "ri-parallel"}, nil).
		Run(func(_ mock.Arguments) {
			time.Sleep(perCallDelay)
		}).Twice()

	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil).Twice()
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil).Twice()

	credStore := &MockCredentialStore{
		LoadRawFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte(`{"access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_access_key":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}`), nil
		},
	}

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		providerFactory: mockFactory,
		credStore:       credStore,
	}

	start := time.Now()
	_, err := manager.executePurchase(ctx, exec)
	elapsed := time.Since(start)

	require.NoError(t, err, "both accounts have valid credentials and should succeed")
	require.Len(t, savedExecs, 2, "exactly two SavePurchaseExecution calls expected — one per account")

	// The core parallelism assertion: a serial loop would take >= serialBoundary
	// (2 * perCallDelay = 600ms). Parallel fan-out completes in roughly
	// perCallDelay (300ms) plus modest scheduling overhead. The boundary is
	// set with margin so CI scheduler jitter does not flake the test, while
	// any serial-loop regression is caught.
	assert.Less(t, elapsed, parallelMaxBound,
		"executeMultiAccount must run accounts in parallel — serial would take ~%v, parallel ~%v, observed %v",
		serialBoundary, perCallDelay, elapsed)

	mockStore.AssertExpectations(t)
	mockEmail.AssertExpectations(t)
	mockFactory.AssertExpectations(t)
	mockProviderInst.AssertExpectations(t)
	mockServiceClient.AssertExpectations(t)
}
