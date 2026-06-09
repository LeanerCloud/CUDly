package purchase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Tests for mapServiceType to hit all branches.
//
// As of the Azure-purchase regression fix (issue #626), mapServiceType
// applies a canonical/legacy split: canonical slugs (compute, relational-db,
// cache, search, data-warehouse) map to the canonical ServiceType
// constants so Azure (and pre-emptively GCP) providers can resolve them
// in GetServiceClient; legacy AWS-only slugs (ec2, rds, elasticache,
// opensearch, redshift, memorydb) keep mapping to the legacy AWS
// constants for backward compat with rec rows persisted before the
// canonical normalisation. The AWS provider accepts both forms via its
// case-list union, so legacy spellings stay safe on AWS rec rows.
func TestMapServiceType_AllBranches(t *testing.T) {
	m := &Manager{}

	cases := []struct {
		input    string
		expected common.ServiceType
	}{
		// Canonical slugs must map to canonical ServiceType constants.
		// Issue #626: collapsing canonical onto AWS-legacy constants broke
		// every Azure (and pre-emptively GCP) approve for these service
		// families because their providers' GetServiceClient switches are
		// canonical-only.
		{"compute", common.ServiceCompute},
		{"relational-db", common.ServiceRelationalDB},
		{"cache", common.ServiceCache},
		{"search", common.ServiceSearch},
		{"data-warehouse", common.ServiceDataWarehouse},
		// Legacy AWS-only slugs preserved for backward compat (AWS provider
		// accepts both forms; older rec rows persisted before normalisation
		// still carry these spellings).
		{"ec2", common.ServiceEC2},
		{"rds", common.ServiceRDS},
		{"elasticache", common.ServiceElastiCache},
		{"opensearch", common.ServiceOpenSearch},
		{"redshift", common.ServiceRedshift},
		{"memorydb", common.ServiceMemoryDB},
		{"savingsplans", common.ServiceSavingsPlans},
		// Issue #85: the legacy hyphenated form is still accepted as a
		// backwards-compat alias so Lambda-scheduled purchase executions
		// persisted before the normalisation (rec.Service == "savings-plans"
		// in purchase_executions.recommendations JSONB) still map correctly
		// when re-executed. Once historical rows have aged out, the alias
		// arm can be dropped and this case should flip to ServiceType("savings-plans").
		{"savings-plans", common.ServiceSavingsPlans},
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

// TestMapServiceType_AzureCanonicalTypesResolveOnAzureProvider is the
// regression test for the Azure purchase failure traced to execution
// 757c9a6e-b22a-4a81-bf04-b11043cbf658 ("unsupported service: ec2"):
// the previous mapping returned AWS-specific ServiceEC2 for both "ec2"
// and "compute", which broke every Azure VM purchase because the Azure
// provider's GetServiceClient switch only accepts ServiceCompute.
// Pinning the canonical-type contract here keeps a future refactor
// from re-introducing the bug.
func TestMapServiceType_AzureCanonicalTypesResolveOnAzureProvider(t *testing.T) {
	m := &Manager{}

	// "compute" is the slug Azure's collector writes for VM
	// recommendations (providers/azure/recommendations.go:
	// convertAdvisorRecommendation maps Microsoft.Compute ->
	// common.ServiceCompute). The mapping must round-trip to a value
	// the Azure provider can dispatch.
	azureInputs := []string{"compute", "relational-db", "cache", "search", "data-warehouse"}
	awsAliases := []common.ServiceType{
		common.ServiceEC2,
		common.ServiceRDS,
		common.ServiceElastiCache,
		common.ServiceOpenSearch,
		common.ServiceRedshift,
	}
	for _, slug := range azureInputs {
		got := m.mapServiceType(slug)
		for _, alias := range awsAliases {
			assert.NotEqual(t, alias, got, "slug %q must not map to AWS-specific %s", slug, alias)
		}
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
	// claimAndExecute CASes the row to "running" before executing (issue #1013).
	runningExec := *exec
	runningExec.Status = "running"
	mockStore.On("TransitionExecutionStatus", ctx, "exec-approved",
		[]string{"approved", "pending", "notified"}, "running").Return(&runningExec, nil)
	mockStore.On("GetPurchasePlan", ctx, "plan-approved").Return(plan, nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	mockStore.On("IncrementPlanCurrentStep", ctx, "plan-approved").Return(nil)
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
	// claimAndExecute CASes the row to "running" before executing (issue #1013).
	runningExec := *exec
	runningExec.Status = "running"
	mockStore.On("TransitionExecutionStatus", ctx, "exec-save-err",
		[]string{"approved", "pending", "notified"}, "running").Return(&runningExec, nil)
	mockStore.On("GetPurchasePlan", ctx, "plan-save-err").Return(plan, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(errors.New("save failed"))
	mockSTS.On("GetCallerIdentity", ctx, mock.Anything).Return(nil, errors.New("sts error"))

	// Provider factory returns error → purchase fails
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(nil, errors.New("provider error"))

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
	// The terminal-save failure now surfaces from executeAndFinalize as
	// ErrAuditLoss (the row is stranded in "running"). The SQS handler returns
	// it so the message is redelivered. A single-account provider failure with
	// no committed recs is not multi-account-ackable, so it is returned.
	assert.Error(t, err)
	assert.ErrorIs(t, err, config.ErrAuditLoss)
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
	// Non-empty PlanID exercises the plan-id propagation through the
	// approve→execute chain. Earlier versions of this test used "" and
	// would have silently passed even if the approve handler dropped or
	// substituted the plan id en route to GetPurchasePlan.
	planID := "plan-appv"
	exec := &config.PurchaseExecution{
		ExecutionID:   "exec-appv",
		PlanID:        planID,
		Status:        "pending",
		ApprovalToken: "correct-token",
		// Recommendations are non-Selected, so processPurchaseRecommendations
		// is a no-op and no AWS API call is made.
		Recommendations: []config.RecommendationRecord{
			{CloudAccountID: &accountID},
		},
	}
	approved := &config.PurchaseExecution{
		ExecutionID:     "exec-appv",
		PlanID:          planID,
		Status:          "approved",
		ApprovalToken:   "correct-token",
		Recommendations: exec.Recommendations,
	}
	account := &config.CloudAccount{ID: accountID, ContactEmail: "owner@example.com"}

	// verifyAsyncApprovalActor + ApproveExecution both load the
	// execution; mock returns it twice.
	mockStore.On("GetExecutionByID", ctx, "exec-appv").Return(exec, nil).Twice()
	mockStore.On("GetCloudAccount", ctx, accountID).Return(account, nil)
	// Atomic approve transition (issue #372 fix).
	mockStore.On("TransitionExecutionStatus", ctx, "exec-appv", []string{"pending", "notified"}, "approved").Return(approved, nil)
	// Synchronous execute chain: GetPurchasePlan is called with the
	// approved execution's PlanID — pinning the non-empty id here means a
	// regression that drops it (e.g. passes "" or exec.ExecutionID by
	// mistake) would mismatch the mock expectation and fail the test.
	plan := &config.PurchasePlan{ID: planID, Name: "test-plan"}
	mockStore.On("GetPurchasePlan", ctx, planID).Return(plan, nil)
	mockEmail.On("SendPurchaseConfirmation", ctx, mock.Anything).Return(nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	mockStore.On("IncrementPlanCurrentStep", ctx, planID).Return(nil)

	manager := &Manager{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	err := manager.ProcessMessage(ctx, `{"type":"approve","execution_id":"exec-appv","token":"correct-token","actor_email":"owner@example.com"}`)
	require.NoError(t, err)
	mockStore.AssertExpectations(t)
	mockEmail.AssertExpectations(t)
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
	// CancelExecutionAtomic is called inside WithTx (nil tx sentinel in
	// tests); actor_email is non-empty so cancelledBy is non-nil.
	actor := "owner@example.com"
	mockStore.On("CancelExecutionAtomic", ctx, mock.Anything, "exec-cancel", &actor).
		Return(true, "cancelled", nil)
	// Suppression cleanup must follow a successful atomic cancel.
	mockStore.On("DeleteSuppressionsByExecutionTx", ctx, mock.Anything, "exec-cancel").
		Return(nil)

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
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(nil, errors.New("provider unavailable"))
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
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", mock.MatchedBy(hasPerRecDeadline(30*time.Second)), common.ServiceRDS, "eu-west-1").Return(nil, errors.New("service client error"))
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
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", mock.MatchedBy(hasPerRecDeadline(30*time.Second)), common.ServiceElastiCache, "ap-southeast-1").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", mock.MatchedBy(hasPerRecDeadline(30*time.Second)), mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(
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
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", mock.MatchedBy(hasPerRecDeadline(30*time.Second)), common.ServiceOpenSearch, "us-west-2").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", mock.MatchedBy(hasPerRecDeadline(30*time.Second)), mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(
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
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", mock.MatchedBy(hasPerRecDeadline(30*time.Second)), common.ServiceRDS, "us-east-1").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", mock.MatchedBy(hasPerRecDeadline(30*time.Second)), mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(
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
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetServiceClient", mock.MatchedBy(hasPerRecDeadline(30*time.Second)), common.ServiceEC2, "us-east-1").Return(mockServiceClient, nil)
	mockServiceClient.On("PurchaseCommitment", mock.MatchedBy(hasPerRecDeadline(30*time.Second)), mock.AnythingOfType("common.Recommendation"), mock.AnythingOfType("common.PurchaseOptions")).Return(
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

// TestManager_SavePurchaseHistory_RevocationWindow is the regression guard for
// the issue #290 "dead Revoke button" gap: savePurchaseHistory is the real
// write path for completed purchases, and it must stamp
// RevocationWindowClosesAt for Azure (Timestamp + 7d free-cancel window) so the
// History UI's canRevokeCompletedRow check shows the button. AWS and GCP have
// no in-app direct-cancel window in Phase 1, so the field must stay nil and the
// button must stay hidden.
func TestManager_SavePurchaseHistory_RevocationWindow(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		wantWindow   bool // true => RevocationWindowClosesAt non-nil
		wantDaysFrom int  // days after Timestamp when window is expected
	}{
		{name: "azure stamps 7-day window", provider: "azure", wantWindow: true, wantDaysFrom: config.AzureRevocationWindowDays},
		{name: "aws leaves window nil", provider: "aws", wantWindow: false},
		{name: "gcp leaves window nil", provider: "gcp", wantWindow: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mockStore := new(MockConfigStore)
			t.Cleanup(func() { mockStore.AssertExpectations(t) })

			var captured *config.PurchaseHistoryRecord
			mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).
				Run(func(args mock.Arguments) {
					captured = args.Get(1).(*config.PurchaseHistoryRecord)
				}).Return(nil)

			manager := &Manager{config: mockStore}

			plan := &config.PurchasePlan{ID: "plan-rev", Name: "Revocation Window Plan"}
			exec := &config.PurchaseExecution{ExecutionID: "exec-rev", PlanID: "plan-rev"}
			rec := config.RecommendationRecord{
				Provider:     tc.provider,
				Service:      "ec2",
				ResourceType: "c5.large",
				Region:       "us-east-1",
				Count:        1,
			}
			result := common.PurchaseResult{Success: true, CommitmentID: "commit-rev-001"}

			err := manager.savePurchaseHistory(ctx, exec, plan, rec, result, "acct-1")
			require.NoError(t, err)
			require.NotNil(t, captured)

			if !tc.wantWindow {
				assert.Nil(t, captured.RevocationWindowClosesAt,
					"%s purchases must not stamp a revocation window", tc.provider)
				return
			}

			require.NotNil(t, captured.RevocationWindowClosesAt,
				"azure purchases must stamp a revocation window so the Revoke button shows")
			wantClose := captured.Timestamp.AddDate(0, 0, tc.wantDaysFrom)
			assert.WithinDuration(t, wantClose, *captured.RevocationWindowClosesAt, time.Second,
				"window must be Timestamp + %d days", tc.wantDaysFrom)
		})
	}
}

// TestManager_ExecuteSinglePurchase_DetailsByService is the regression guard
// for issue #453. Before the fix, executeSinglePurchase assigned a value-
// typed common.DatabaseDetails (and only when rec.Engine was non-empty);
// every AWS service client's findOfferingID type-asserts a *pointer*, so the
// assertion failed and surfaced as "invalid service details for <Service>"
// for every dashboard-driven purchase after #373 made approve synchronous.
//
// The revised fix (post-design-review) persists the full ServiceDetails
// payload onto the RecommendationRecord at collection time and reconstructs
// the correct typed *Details pointer in executeSinglePurchase. This table-
// driven test:
//
//  1. Seeds a rec with the canonical Details JSON for each service (the
//     "happy path" — new rows post-fix carry full details).
//  2. Captures the rec.Details passed to the cloud client and asserts
//     both the concrete pointer type AND that every field round-tripped
//     intact (Platform=Windows must come through, not be silently
//     defaulted to Linux/UNIX).
//
// A second sub-test below covers the legacy fallback (empty Details JSON).
func TestManager_ExecuteSinglePurchase_DetailsByService(t *testing.T) {
	cases := []struct {
		name        string
		service     string
		serviceType common.ServiceType
		region      string
		resource    string
		engine      string
		details     common.ServiceDetails
		// assertDetails inspects the rec.Details captured by the mock
		// PurchaseCommitment call and asserts both the concrete pointer
		// type and the per-service fields the AWS client reads.
		assertDetails func(t *testing.T, d common.ServiceDetails)
	}{
		{
			name:        "ec2_windows",
			service:     "ec2",
			serviceType: common.ServiceEC2,
			region:      "us-east-1",
			resource:    "t4g.nano",
			details:     &common.ComputeDetails{InstanceType: "t4g.nano", Platform: "Windows", Tenancy: "dedicated", Scope: "Region"},
			assertDetails: func(t *testing.T, d common.ServiceDetails) {
				cd, ok := d.(*common.ComputeDetails)
				if assert.True(t, ok, "EC2 details must be *common.ComputeDetails, got %T", d) {
					// Platform must round-trip — silent fallback to
					// Linux/UNIX is precisely the silent mis-purchase
					// the revised fix exists to prevent.
					assert.Equal(t, "Windows", cd.Platform)
					assert.Equal(t, "dedicated", cd.Tenancy)
					assert.Equal(t, "Region", cd.Scope)
				}
			},
		},
		{
			name:        "rds_postgres",
			service:     "rds",
			serviceType: common.ServiceRDS,
			region:      "us-east-1",
			resource:    "db.r5.large",
			engine:      "postgres",
			details:     &common.DatabaseDetails{Engine: "postgres", AZConfig: "multi-az", InstanceClass: "db.r5.large"},
			assertDetails: func(t *testing.T, d common.ServiceDetails) {
				dd, ok := d.(*common.DatabaseDetails)
				if assert.True(t, ok, "RDS details must be *common.DatabaseDetails, got %T", d) {
					assert.Equal(t, "postgres", dd.Engine)
					assert.Equal(t, "multi-az", dd.AZConfig)
					assert.Equal(t, "db.r5.large", dd.InstanceClass)
				}
			},
		},
		{
			name:        "elasticache_redis",
			service:     "elasticache",
			serviceType: common.ServiceElastiCache,
			region:      "us-east-1",
			resource:    "cache.r5.large",
			engine:      "redis",
			details:     &common.CacheDetails{Engine: "redis", NodeType: "cache.r5.large"},
			assertDetails: func(t *testing.T, d common.ServiceDetails) {
				cd, ok := d.(*common.CacheDetails)
				if assert.True(t, ok, "ElastiCache details must be *common.CacheDetails, got %T", d) {
					assert.Equal(t, "redis", cd.Engine)
					assert.Equal(t, "cache.r5.large", cd.NodeType)
				}
			},
		},
		{
			name:        "opensearch",
			service:     "opensearch",
			serviceType: common.ServiceOpenSearch,
			region:      "us-west-2",
			resource:    "r5.large.search",
			details:     &common.SearchDetails{InstanceType: "r5.large.search"},
			assertDetails: func(t *testing.T, d common.ServiceDetails) {
				sd, ok := d.(*common.SearchDetails)
				if assert.True(t, ok, "OpenSearch details must be *common.SearchDetails, got %T", d) {
					assert.Equal(t, "r5.large.search", sd.InstanceType)
				}
			},
		},
		{
			name:        "redshift",
			service:     "redshift",
			serviceType: common.ServiceRedshift,
			region:      "us-east-1",
			resource:    "ra3.xlplus",
			details:     &common.DataWarehouseDetails{NodeType: "ra3.xlplus", NumberOfNodes: 2, ClusterType: "multi-node"},
			assertDetails: func(t *testing.T, d common.ServiceDetails) {
				dw, ok := d.(*common.DataWarehouseDetails)
				if assert.True(t, ok, "Redshift details must be *common.DataWarehouseDetails, got %T", d) {
					assert.Equal(t, "ra3.xlplus", dw.NodeType)
					assert.Equal(t, 2, dw.NumberOfNodes)
				}
			},
		},
		{
			name:        "sp_compute",
			service:     "savings-plans-compute",
			serviceType: common.ServiceSavingsPlansCompute,
			region:      "us-east-1",
			resource:    "ComputeSP",
			details:     &common.SavingsPlanDetails{PlanType: "Compute", HourlyCommitment: 1.50},
			assertDetails: func(t *testing.T, d common.ServiceDetails) {
				sp, ok := d.(*common.SavingsPlanDetails)
				if assert.True(t, ok, "SP-Compute details must be *common.SavingsPlanDetails, got %T", d) {
					assert.Equal(t, "Compute", sp.PlanType)
					assert.InDelta(t, 1.50, sp.HourlyCommitment, 0.001)
				}
			},
		},
		{
			name:        "sp_ec2instance",
			service:     "savings-plans-ec2instance",
			serviceType: common.ServiceSavingsPlansEC2Instance,
			region:      "us-east-1",
			resource:    "EC2InstanceSP",
			details:     &common.SavingsPlanDetails{PlanType: "EC2Instance", HourlyCommitment: 2.0},
			assertDetails: func(t *testing.T, d common.ServiceDetails) {
				sp, ok := d.(*common.SavingsPlanDetails)
				if assert.True(t, ok, "SP-EC2Instance details must be *common.SavingsPlanDetails, got %T", d) {
					assert.Equal(t, "EC2Instance", sp.PlanType)
				}
			},
		},
		{
			// Legacy umbrella slug ("savings-plans") that mapSavingsPlansSlug
			// still accepts for purchase_execution rows persisted before the
			// rename in PR #94. DecodeServiceDetailsFor must also recognise
			// this alias so a stored "savings-plans" rec round-trips through
			// the codec the same way as the canonical "savingsplans" form.
			// Regression guard: without the "savings-plans" case in
			// newDetailsForService, DecodeServiceDetailsFor returns (nil,
			// false) and recommendation.Details is nil — the SP client's
			// findOfferingID type-assertion then fails with "invalid service
			// details" on legacy rows, re-introducing #453 for SP umbrella.
			name:        "sp_legacy_umbrella",
			service:     "savings-plans",
			serviceType: common.ServiceSavingsPlans,
			region:      "us-east-1",
			resource:    "LegacySP",
			details:     &common.SavingsPlanDetails{PlanType: "Compute", HourlyCommitment: 0.75},
			assertDetails: func(t *testing.T, d common.ServiceDetails) {
				sp, ok := d.(*common.SavingsPlanDetails)
				if assert.True(t, ok, "legacy SP umbrella must decode to *common.SavingsPlanDetails, got %T", d) {
					assert.Equal(t, "Compute", sp.PlanType)
					assert.InDelta(t, 0.75, sp.HourlyCommitment, 0.001)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mockStore := new(MockConfigStore)
			mockEmail := new(MockEmailSender)
			mockFactory := new(MockProviderFactory)
			mockProvider := new(MockProvider)
			mockServiceClient := new(MockServiceClient)
			mockSTS := new(MockSTSClient)

			// Marshal the canonical Details into the RecommendationRecord
			// — this is exactly what the scheduler does at collection
			// time via common.MarshalServiceDetails, so the round-trip
			// here is the same one a real purchase exercises.
			detailsBlob, err := common.MarshalServiceDetails(tc.details)
			require.NoError(t, err)
			require.NotNil(t, detailsBlob, "test seed must produce a non-empty Details blob")

			plan := &config.PurchasePlan{ID: "plan-" + tc.name, Name: "Plan"}
			exec := &config.PurchaseExecution{
				ExecutionID: "exec-" + tc.name,
				PlanID:      "plan-" + tc.name,
				Recommendations: []config.RecommendationRecord{
					{
						Provider:     "aws",
						Service:      tc.service,
						ResourceType: tc.resource,
						Engine:       tc.engine,
						Details:      detailsBlob,
						Region:       tc.region,
						Count:        1,
						Term:         1,
						Payment:      "All Upfront",
						Savings:      10.0,
						UpfrontCost:  100.0,
						Selected:     true,
					},
				},
			}

			// Capture the rec passed to PurchaseCommitment so we can
			// inspect Details after the call.
			var capturedRec common.Recommendation
			mockStore.On("GetPurchasePlan", ctx, plan.ID).Return(plan, nil)
			mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
			mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
			mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
			mockProvider.On("GetServiceClient", mock.MatchedBy(hasPerRecDeadline(30*time.Second)), tc.serviceType, tc.region).Return(mockServiceClient, nil)
			mockServiceClient.On(
				"PurchaseCommitment",
				mock.MatchedBy(hasPerRecDeadline(30*time.Second)),
				mock.AnythingOfType("common.Recommendation"),
				mock.AnythingOfType("common.PurchaseOptions"),
			).Run(func(args mock.Arguments) {
				capturedRec = args.Get(1).(common.Recommendation)
			}).Return(
				common.PurchaseResult{Success: true, CommitmentID: "ri-" + tc.name},
				nil,
			)
			mockSTS.On("GetCallerIdentity", ctx, mock.Anything).Return(nil, errors.New("sts error"))

			manager := &Manager{
				config:          mockStore,
				email:           mockEmail,
				stsClient:       mockSTS,
				providerFactory: mockFactory,
				dashboardURL:    "https://dashboard.example.com",
			}

			_, err = manager.executePurchase(ctx, exec)
			require.NoError(t, err, "purchase should not return the regression error 'invalid service details for <Service>'")
			assert.True(t, exec.Recommendations[0].Purchased, "rec should be marked purchased")
			assert.Empty(t, exec.Recommendations[0].Error, "rec error should be empty")
			require.NotNil(t, capturedRec.Details, "rec.Details handed to the cloud client must be non-nil")
			tc.assertDetails(t, capturedRec.Details)
		})
	}
}

// TestManager_ExecuteSinglePurchase_LegacyEmptyDetails locks down the
// graceful-degradation path for rows persisted before #453 — they carry
// an empty Details payload but the cloud client's findOfferingID still
// needs a typed *pointer* to type-assert against. DecodeServiceDetailsFor
// returns a zero-valued typed pointer in that case; the engine column
// (which legacy rows DID carry) is folded back onto the Details by
// applyEngineFallback so a non-default-engine DB rec doesn't silently
// mis-purchase as the default.
func TestManager_ExecuteSinglePurchase_LegacyEmptyDetails(t *testing.T) {
	cases := []struct {
		name          string
		service       string
		serviceType   common.ServiceType
		region        string
		resource      string
		engine        string
		assertDetails func(t *testing.T, d common.ServiceDetails)
	}{
		{
			name:        "legacy_ec2",
			service:     "ec2",
			serviceType: common.ServiceEC2,
			region:      "us-east-1",
			resource:    "t4g.nano",
			assertDetails: func(t *testing.T, d common.ServiceDetails) {
				cd, ok := d.(*common.ComputeDetails)
				if assert.True(t, ok, "EC2 legacy details must be *common.ComputeDetails, got %T", d) {
					// Zero-valued — service client's
					// buildOfferingFilters substitutes defaults.
					assert.Empty(t, cd.Platform)
					assert.Empty(t, cd.Tenancy)
				}
			},
		},
		{
			name:        "legacy_rds_postgres",
			service:     "rds",
			serviceType: common.ServiceRDS,
			region:      "us-east-1",
			resource:    "db.r5.large",
			engine:      "postgres",
			assertDetails: func(t *testing.T, d common.ServiceDetails) {
				dd, ok := d.(*common.DatabaseDetails)
				if assert.True(t, ok, "RDS legacy details must be *common.DatabaseDetails, got %T", d) {
					// Engine must be backfilled from the Engine
					// column — without applyEngineFallback, the
					// Postgres rec would silently mis-purchase as
					// MySQL (or whatever default).
					assert.Equal(t, "postgres", dd.Engine)
				}
			},
		},
		{
			name:        "legacy_elasticache_redis",
			service:     "elasticache",
			serviceType: common.ServiceElastiCache,
			region:      "us-east-1",
			resource:    "cache.r5.large",
			engine:      "redis",
			assertDetails: func(t *testing.T, d common.ServiceDetails) {
				cd, ok := d.(*common.CacheDetails)
				if assert.True(t, ok, "ElastiCache legacy details must be *common.CacheDetails, got %T", d) {
					assert.Equal(t, "redis", cd.Engine)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mockStore := new(MockConfigStore)
			mockEmail := new(MockEmailSender)
			mockFactory := new(MockProviderFactory)
			mockProvider := new(MockProvider)
			mockServiceClient := new(MockServiceClient)
			mockSTS := new(MockSTSClient)

			plan := &config.PurchasePlan{ID: "plan-" + tc.name, Name: "Plan"}
			exec := &config.PurchaseExecution{
				ExecutionID: "exec-" + tc.name,
				PlanID:      "plan-" + tc.name,
				Recommendations: []config.RecommendationRecord{
					{
						Provider:     "aws",
						Service:      tc.service,
						ResourceType: tc.resource,
						Engine:       tc.engine,
						// Details deliberately empty — simulates a
						// row persisted before #453.
						Region:      tc.region,
						Count:       1,
						Term:        1,
						Payment:     "All Upfront",
						Savings:     10.0,
						UpfrontCost: 100.0,
						Selected:    true,
					},
				},
			}

			var capturedRec common.Recommendation
			mockStore.On("GetPurchasePlan", ctx, plan.ID).Return(plan, nil)
			mockStore.On("SavePurchaseHistory", ctx, mock.AnythingOfType("*config.PurchaseHistoryRecord")).Return(nil)
			mockEmail.On("SendPurchaseConfirmation", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)
			mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
			mockProvider.On("GetServiceClient", mock.MatchedBy(hasPerRecDeadline(30*time.Second)), tc.serviceType, tc.region).Return(mockServiceClient, nil)
			mockServiceClient.On(
				"PurchaseCommitment",
				mock.MatchedBy(hasPerRecDeadline(30*time.Second)),
				mock.AnythingOfType("common.Recommendation"),
				mock.AnythingOfType("common.PurchaseOptions"),
			).Run(func(args mock.Arguments) {
				capturedRec = args.Get(1).(common.Recommendation)
			}).Return(
				common.PurchaseResult{Success: true, CommitmentID: "ri-" + tc.name},
				nil,
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
			require.NoError(t, err, "legacy empty-Details rec must still purchase cleanly")
			require.NotNil(t, capturedRec.Details, "rec.Details handed to the cloud client must be non-nil even for legacy rows")
			tc.assertDetails(t, capturedRec.Details)
		})
	}
}

// TestApplyEngineFallback covers the engine-backfill helper directly so a
// future refactor that changes which *Details types carry an Engine field
// surfaces here rather than via a silent mis-purchase regression.
func TestApplyEngineFallback(t *testing.T) {
	// RDS: empty Engine should be backfilled.
	rds := &common.DatabaseDetails{}
	applyEngineFallback(rds, "mysql")
	assert.Equal(t, "mysql", rds.Engine, "empty DB Engine must be backfilled from the record column")

	// RDS: pre-populated Engine must NOT be overwritten — Details is
	// the source of truth when present.
	rdsKeep := &common.DatabaseDetails{Engine: "postgres"}
	applyEngineFallback(rdsKeep, "mysql")
	assert.Equal(t, "postgres", rdsKeep.Engine, "non-empty DB Engine must be preserved")

	// Cache: same rule.
	ec := &common.CacheDetails{}
	applyEngineFallback(ec, "redis")
	assert.Equal(t, "redis", ec.Engine)

	// Empty engine arg is a no-op.
	rdsZero := &common.DatabaseDetails{}
	applyEngineFallback(rdsZero, "")
	assert.Empty(t, rdsZero.Engine, "empty engine arg must leave Details untouched")

	// Non-DB/Cache types are no-ops (don't accidentally write into them).
	cc := &common.ComputeDetails{}
	applyEngineFallback(cc, "mysql")
	assert.Empty(t, cc.Platform, "ComputeDetails must remain untouched by applyEngineFallback")
}
