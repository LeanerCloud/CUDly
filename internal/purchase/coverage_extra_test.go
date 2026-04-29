package purchase

import (
	"context"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Tests for mapServiceType to hit all branches

func TestMapServiceType_AllBranches(t *testing.T) {
	m := &Manager{}

	cases := []struct {
		input    string
		expected common.ServiceType
	}{
		{"ec2", common.ServiceEC2},
		{"compute", common.ServiceEC2},
		{"rds", common.ServiceRDS},
		{"relational-db", common.ServiceRDS},
		{"elasticache", common.ServiceElastiCache},
		{"cache", common.ServiceElastiCache},
		{"opensearch", common.ServiceOpenSearch},
		{"search", common.ServiceOpenSearch},
		{"redshift", common.ServiceRedshift},
		{"data-warehouse", common.ServiceRedshift},
		{"memorydb", common.ServiceMemoryDB},
		{"savings-plans", common.ServiceSavingsPlans},
		{"savingsplans", common.ServiceSavingsPlans},
		{"unknown-service", common.ServiceType("unknown-service")},
		{"", common.ServiceType("")},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := m.mapServiceType(tc.input)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// Tests for resolveAccountProvider with unknown provider
func TestResolveAccountProvider_UnknownProvider(t *testing.T) {
	m := &Manager{}
	account := config.CloudAccount{
		ID:       "acc-1",
		Provider: "unknown-cloud",
	}
	result, err := m.resolveAccountProvider(context.Background(), account)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown cloud provider")
	assert.Nil(t, result)
}

// Tests for resolveAWSProvider when assumeRoleSTS is nil
func TestResolveAWSProvider_NoSTS(t *testing.T) {
	m := &Manager{
		assumeRoleSTS: nil,
	}
	account := config.CloudAccount{
		ID:          "acc-aws",
		Provider:    "aws",
		AWSAuthMode: "assume_role",
		AWSRoleARN:  "arn:aws:iam::123456789012:role/testrole",
	}
	result, err := m.resolveAWSProvider(context.Background(), account)
	// Without STS, returns error (not silent nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// Tests for resolveAzureProvider without credStore and not managed_identity — returns error
func TestResolveAzureProvider_NoCredStoreNoManagedIdentity(t *testing.T) {
	m := &Manager{
		credStore: nil,
	}
	account := config.CloudAccount{
		ID:            "acc-azure",
		Provider:      "azure",
		AzureAuthMode: "service_principal", // not managed_identity
	}
	result, err := m.resolveAzureProvider(context.Background(), account)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// Tests for resolveGCPProvider without credStore and not application_default — returns error
func TestResolveGCPProvider_NoCredStoreNoADC(t *testing.T) {
	m := &Manager{
		credStore: nil,
	}
	account := config.CloudAccount{
		ID:          "acc-gcp",
		Provider:    "gcp",
		GCPAuthMode: "service_account_key", // not application_default
	}
	result, err := m.resolveGCPProvider(context.Background(), account)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// Tests for resolveGCPProvider with application_default (nil credStore is okay)
func TestResolveGCPProvider_ApplicationDefault(t *testing.T) {
	m := &Manager{
		credStore: nil,
	}
	account := config.CloudAccount{
		ID:           "acc-gcp-adc",
		Provider:     "gcp",
		GCPAuthMode:  "application_default",
		GCPProjectID: "my-project",
	}
	// application_default → returns (nil, nil) since ADC is ambient
	result, err := m.resolveGCPProvider(context.Background(), account)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

// Tests for handleExecutePurchase: execution found + status "approved" → success path
func TestHandleExecutePurchase_ApprovedStatus(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockSTS := new(MockSTSClient)

	plan := &config.PurchasePlan{
		ID:   "plan-approved",
		Name: "Approved Plan",
		RampSchedule: config.RampSchedule{
			TotalSteps:  4,
			CurrentStep: 0,
		},
	}

	exec := &config.PurchaseExecution{
		ExecutionID:     "exec-approved",
		PlanID:          "plan-approved",
		Status:          "approved",
		Recommendations: []config.RecommendationRecord{},
	}

	mockStore.On("GetExecutionByID", ctx, "exec-approved").Return(exec, nil)
	mockStore.On("GetPurchasePlan", ctx, "plan-approved").Return(plan, nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	mockStore.On("UpdatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)
	mockSTS.On("GetCallerIdentity", ctx, mock.Anything).Return(nil, errors.New("sts error"))

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		providerFactory: mockFactory,
		stsClient:       mockSTS,
		dashboardURL:    "https://dashboard.example.com",
	}

	msg := AsyncMessage{
		Type:        MessageTypeExecutePurchase,
		ExecutionID: "exec-approved",
	}
	err := manager.handleExecutePurchase(ctx, msg)
	require.NoError(t, err)

	mockStore.AssertExpectations(t)
}

// Tests for handleExecutePurchase: GetExecutionByID returns error
func TestHandleExecutePurchase_GetError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	mockStore.On("GetExecutionByID", ctx, "exec-err").Return(nil, errors.New("db error"))

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	msg := AsyncMessage{
		Type:        MessageTypeExecutePurchase,
		ExecutionID: "exec-err",
	}
	err := manager.handleExecutePurchase(ctx, msg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get execution")
}

// Tests for handleExecutePurchase: SavePurchaseExecution error after failed purchase
func TestHandleExecutePurchase_SaveError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockSTS := new(MockSTSClient)

	plan := &config.PurchasePlan{
		ID:   "plan-save-err",
		Name: "Plan",
	}

	rec := config.RecommendationRecord{
		Provider:     "aws",
		Service:      "ec2",
		ResourceType: "m5.large",
		Region:       "us-east-1",
		Count:        1,
		Selected:     true,
	}

	exec := &config.PurchaseExecution{
		ExecutionID:     "exec-save-err",
		PlanID:          "plan-save-err",
		Status:          "pending",
		Recommendations: []config.RecommendationRecord{rec},
	}

	mockStore.On("GetExecutionByID", ctx, "exec-save-err").Return(exec, nil)
	mockStore.On("GetPurchasePlan", ctx, "plan-save-err").Return(plan, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(errors.New("save failed"))
	mockSTS.On("GetCallerIdentity", ctx, mock.Anything).Return(nil, errors.New("sts error"))

	// Provider factory returns error → purchase fails
	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(nil, errors.New("provider error"))

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		providerFactory: mockFactory,
		stsClient:       mockSTS,
		dashboardURL:    "https://dashboard.example.com",
	}

	msg := AsyncMessage{
		Type:        MessageTypeExecutePurchase,
		ExecutionID: "exec-save-err",
	}
	err := manager.handleExecutePurchase(ctx, msg)
	// Returns the save error (not the purchase error)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to save execution status")
}

// Tests for ProcessMessage with approve and cancel happy paths.
// Post-hardening: SQS approve/cancel messages MUST carry actor_email,
// and the actor MUST match a per-account contact_email on one of the
// execution's recommendations. The mocks below set up that gating.
func TestProcessMessage_ApproveHappyPath(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	accountID := "acct-1"
	exec := &config.PurchaseExecution{
		ExecutionID:   "exec-appv",
		Status:        "pending",
		ApprovalToken: "correct-token",
		Recommendations: []config.RecommendationRecord{
			{CloudAccountID: &accountID},
		},
	}
	account := &config.CloudAccount{ID: accountID, ContactEmail: "owner@example.com"}

	// verifyAsyncApprovalActor + ApproveExecution both load the
	// execution; mock returns it twice.
	mockStore.On("GetExecutionByID", ctx, "exec-appv").Return(exec, nil).Twice()
	mockStore.On("GetCloudAccount", ctx, accountID).Return(account, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.ProcessMessage(ctx, `{"type":"approve","execution_id":"exec-appv","token":"correct-token","actor_email":"owner@example.com"}`)
	require.NoError(t, err)
	mockStore.AssertExpectations(t)
}

func TestProcessMessage_CancelHappyPath(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	accountID := "acct-1"
	exec := &config.PurchaseExecution{
		ExecutionID:   "exec-cancel",
		Status:        "pending",
		ApprovalToken: "correct-token",
		Recommendations: []config.RecommendationRecord{
			{CloudAccountID: &accountID},
		},
	}
	account := &config.CloudAccount{ID: accountID, ContactEmail: "owner@example.com"}

	// verifyAsyncApprovalActor + CancelExecution both load the
	// execution; mock returns it twice.
	mockStore.On("GetExecutionByID", ctx, "exec-cancel").Return(exec, nil).Twice()
	mockStore.On("GetCloudAccount", ctx, accountID).Return(account, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.ProcessMessage(ctx, `{"type":"cancel","execution_id":"exec-cancel","token":"correct-token","actor_email":"owner@example.com"}`)
	require.NoError(t, err)
	mockStore.AssertExpectations(t)
}

// TestProcessMessage_ApproveRejectsMissingActor verifies the SQS
// approve handler refuses to process a message without an actor_email.
// This is the regression test for the closed bypass: legacy / replayed
// payloads without the field must NOT fall through to a tokenless
// approval.
func TestProcessMessage_ApproveRejectsMissingActor(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.ProcessMessage(ctx, `{"type":"approve","execution_id":"exec-x","token":"some-token"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "actor_email required")
	// No store calls should have happened — no approval, no save.
	mockStore.AssertNotCalled(t, "GetExecutionByID")
	mockStore.AssertNotCalled(t, "SavePurchaseExecution")
}

// TestProcessMessage_CancelRejectsMissingActor mirrors the approve
// guard for the cancel path.
func TestProcessMessage_CancelRejectsMissingActor(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.ProcessMessage(ctx, `{"type":"cancel","execution_id":"exec-x","token":"some-token"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "actor_email required")
	mockStore.AssertNotCalled(t, "GetExecutionByID")
	mockStore.AssertNotCalled(t, "SavePurchaseExecution")
}

// TestProcessMessage_ApproveRejectsNonMatchingActor: token is valid,
// actor_email is present but not on the per-account contact_email
// approver list. Reject — same outcome as the HTTP path's
// authorizeApprovalAction.
func TestProcessMessage_ApproveRejectsNonMatchingActor(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	accountID := "acct-1"
	exec := &config.PurchaseExecution{
		ExecutionID:   "exec-mismatch",
		Status:        "pending",
		ApprovalToken: "correct-token",
		Recommendations: []config.RecommendationRecord{
			{CloudAccountID: &accountID},
		},
	}
	account := &config.CloudAccount{ID: accountID, ContactEmail: "owner@example.com"}

	mockStore.On("GetExecutionByID", ctx, "exec-mismatch").Return(exec, nil)
	mockStore.On("GetCloudAccount", ctx, accountID).Return(account, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.ProcessMessage(ctx, `{"type":"approve","execution_id":"exec-mismatch","token":"correct-token","actor_email":"intruder@example.com"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an authorised approver")
	// SavePurchaseExecution must NOT have been called — no mutation.
	mockStore.AssertNotCalled(t, "SavePurchaseExecution")
}

// TestProcessMessage_ApproveRejectsTokenMismatch: token is wrong,
// actor_email is set. Existing behaviour (token check fails) — verified
// here so a future refactor of verifyAsyncApprovalActor doesn't
// regress the token comparison ordering.
func TestProcessMessage_ApproveRejectsTokenMismatch(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	exec := &config.PurchaseExecution{
		ExecutionID:   "exec-bad-token",
		Status:        "pending",
		ApprovalToken: "correct-token",
	}

	mockStore.On("GetExecutionByID", ctx, "exec-bad-token").Return(exec, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.ProcessMessage(ctx, `{"type":"approve","execution_id":"exec-bad-token","token":"wrong-token","actor_email":"owner@example.com"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid approval token")
	// No GetCloudAccount (token failure is checked before approver
	// resolution) and no SavePurchaseExecution.
	mockStore.AssertNotCalled(t, "GetCloudAccount")
	mockStore.AssertNotCalled(t, "SavePurchaseExecution")
}

// TestProcessMessage_ApproveRejectsNoApprovers: token + actor are valid
// in shape, but no recommendation references an account with a
// configured contact_email. Same policy as the HTTP path: the global
// notify mailbox is not an approver — reject.
func TestProcessMessage_ApproveRejectsNoApprovers(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	exec := &config.PurchaseExecution{
		ExecutionID:     "exec-no-approvers",
		Status:          "pending",
		ApprovalToken:   "correct-token",
		Recommendations: []config.RecommendationRecord{}, // no account refs
	}

	mockStore.On("GetExecutionByID", ctx, "exec-no-approvers").Return(exec, nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.ProcessMessage(ctx, `{"type":"approve","execution_id":"exec-no-approvers","token":"correct-token","actor_email":"owner@example.com"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no per-account contact email configured")
	mockStore.AssertNotCalled(t, "SavePurchaseExecution")
}

// Tests for executeSinglePurchase error paths via executePurchase

func TestManager_ExecuteSinglePurchase_ProviderError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockSTS := new(MockSTSClient)

	plan := &config.PurchasePlan{
		ID:   "plan-prov-err",
		Name: "Test Plan",
	}

	exec := &config.PurchaseExecution{
		ExecutionID: "exec-prov-err",
		PlanID:      "plan-prov-err",
		StepNumber:  1,
		Recommendations: []config.RecommendationRecord{
			{
				Provider:     "aws",
				Service:      "ec2",
				ResourceType: "m5.large",
				Region:       "us-east-1",
				Count:        1,
				Savings:      100.0,
				Selected:     true,
			},
		},
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-prov-err").Return(plan, nil)
	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(nil, errors.New("provider unavailable"))
	mockSTS.On("GetCallerIdentity", ctx, mock.Anything).Return(nil, errors.New("sts error"))

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		stsClient:       mockSTS,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	_, err := manager.executePurchase(ctx, exec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "some purchases failed")
	assert.Equal(t, "failed to create aws provider: provider unavailable", exec.Recommendations[0].Error)
}

func TestManager_ExecuteSinglePurchase_ServiceClientError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockSTS := new(MockSTSClient)

	plan := &config.PurchasePlan{
		ID:   "plan-svc-err",
		Name: "Test Plan",
	}

	exec := &config.PurchaseExecution{
		ExecutionID: "exec-svc-err",
		PlanID:      "plan-svc-err",
		StepNumber:  1,
		Recommendations: []config.RecommendationRecord{
			{
				Provider:     "aws",
				Service:      "rds",
				ResourceType: "db.r5.large",
				Region:       "eu-west-1",
				Count:        2,
				Savings:      200.0,
				Selected:     true,
			},
		},
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-svc-err").Return(plan, nil)
	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", ctx, common.ServiceRDS, "eu-west-1").Return(nil, errors.New("service client error"))
	mockSTS.On("GetCallerIdentity", ctx, mock.Anything).Return(nil, errors.New("sts error"))

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		stsClient:       mockSTS,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	_, err := manager.executePurchase(ctx, exec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "some purchases failed")
	assert.Contains(t, exec.Recommendations[0].Error, "failed to get service client")
}

func TestManager_ExecuteSinglePurchase_PurchaseNotSuccessful(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockServiceClient := new(MockServiceClient)
	mockSTS := new(MockSTSClient)

	plan := &config.PurchasePlan{
		ID:   "plan-not-success",
		Name: "Test Plan",
	}

	exec := &config.PurchaseExecution{
		ExecutionID: "exec-not-success",
		PlanID:      "plan-not-success",
		Recommendations: []config.RecommendationRecord{
			{
				Provider:     "aws",
				Service:      "elasticache",
				ResourceType: "cache.r5.large",
				Region:       "ap-southeast-1",
				Count:        1,
				Savings:      50.0,
				Selected:     true,
			},
		},
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-not-success").Return(plan, nil)
	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", ctx, common.ServiceElastiCache, "ap-southeast-1").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", ctx, mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(
		common.PurchaseResult{Success: false}, nil,
	)
	mockSTS.On("GetCallerIdentity", ctx, mock.Anything).Return(nil, errors.New("sts error"))

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		stsClient:       mockSTS,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	_, err := manager.executePurchase(ctx, exec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "some purchases failed")
	assert.Contains(t, exec.Recommendations[0].Error, "purchase was not successful")
}

func TestManager_ExecuteSinglePurchase_PurchaseNotSuccessful_WithError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockServiceClient := new(MockServiceClient)
	mockSTS := new(MockSTSClient)

	plan := &config.PurchasePlan{
		ID:   "plan-err-result",
		Name: "Test Plan",
	}

	exec := &config.PurchaseExecution{
		ExecutionID: "exec-err-result",
		PlanID:      "plan-err-result",
		Recommendations: []config.RecommendationRecord{
			{
				Provider:     "aws",
				Service:      "opensearch",
				ResourceType: "r5.large.search",
				Region:       "us-west-2",
				Count:        1,
				Savings:      75.0,
				Selected:     true,
			},
		},
	}

	specificErr := errors.New("capacity limit exceeded")
	mockStore.On("GetPurchasePlan", ctx, "plan-err-result").Return(plan, nil)
	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", ctx, common.ServiceOpenSearch, "us-west-2").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", ctx, mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(
		common.PurchaseResult{Success: false, Error: specificErr}, nil,
	)
	mockSTS.On("GetCallerIdentity", ctx, mock.Anything).Return(nil, errors.New("sts error"))

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		stsClient:       mockSTS,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	_, err := manager.executePurchase(ctx, exec)
	assert.Error(t, err)
	assert.Contains(t, exec.Recommendations[0].Error, "capacity limit exceeded")
}

// Tests for executeSinglePurchase with Engine field (DatabaseDetails branch)
func TestManager_ExecuteSinglePurchase_WithEngine(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockServiceClient := new(MockServiceClient)
	mockSTS := new(MockSTSClient)

	plan := &config.PurchasePlan{
		ID:   "plan-engine",
		Name: "DB Plan",
	}

	exec := &config.PurchaseExecution{
		ExecutionID: "exec-engine",
		PlanID:      "plan-engine",
		Recommendations: []config.RecommendationRecord{
			{
				Provider:     "aws",
				Service:      "rds",
				ResourceType: "db.r5.large",
				Engine:       "mysql",
				Region:       "us-east-1",
				Count:        1,
				Savings:      150.0,
				UpfrontCost:  600.0,
				Selected:     true,
			},
		},
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-engine").Return(plan, nil)
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", ctx, common.ServiceRDS, "us-east-1").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", ctx, mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(
		common.PurchaseResult{Success: true, CommitmentID: "ri-engine-001"}, nil,
	)
	mockSTS.On("GetCallerIdentity", ctx, mock.Anything).Return(nil, errors.New("sts error"))

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		stsClient:       mockSTS,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	_, err := manager.executePurchase(ctx, exec)
	require.NoError(t, err)
	assert.True(t, exec.Recommendations[0].Purchased)
	assert.Equal(t, "ri-engine-001", exec.Recommendations[0].PurchaseID)
}

// Tests for savePurchaseHistory error path (just logs, doesn't fail)
func TestManager_SavePurchaseHistory_Error(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockServiceClient := new(MockServiceClient)
	mockSTS := new(MockSTSClient)

	plan := &config.PurchasePlan{
		ID:   "plan-hist-err",
		Name: "History Error Plan",
	}

	exec := &config.PurchaseExecution{
		ExecutionID: "exec-hist-err",
		PlanID:      "plan-hist-err",
		Recommendations: []config.RecommendationRecord{
			{
				Provider:     "aws",
				Service:      "ec2",
				ResourceType: "c5.large",
				Region:       "us-east-1",
				Count:        2,
				Savings:      80.0,
				UpfrontCost:  320.0,
				Selected:     true,
			},
		},
	}

	mockStore.On("GetPurchasePlan", ctx, "plan-hist-err").Return(plan, nil)
	// SavePurchaseHistory returns error — should be logged but not fail executePurchase
	mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(errors.New("history write error"))
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", ctx, common.ServiceEC2, "us-east-1").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", ctx, mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(
		common.PurchaseResult{Success: true, CommitmentID: "ri-hist-001"}, nil,
	)
	mockSTS.On("GetCallerIdentity", ctx, mock.Anything).Return(nil, errors.New("sts error"))

	manager := &Manager{
		config:          mockStore,
		email:           mockEmail,
		stsClient:       mockSTS,
		providerFactory: mockFactory,
		dashboardURL:    "https://dashboard.example.com",
	}

	// Should succeed even though history save failed
	_, err := manager.executePurchase(ctx, exec)
	require.NoError(t, err)
	assert.True(t, exec.Recommendations[0].Purchased)
}
