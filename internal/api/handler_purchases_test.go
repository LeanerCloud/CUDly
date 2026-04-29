package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// approvalTestExec builds a purchase execution wired to a single
// recommendation against an account whose contact_email is `contact`. Used
// to satisfy the post-hardening approver-set policy (see
// authorizeApprovalAction): the global notify mailbox is no longer an
// authorised approver, so tests must wire a per-account contact email.
func approvalTestExec(execID, contact string, mockConfig *MockConfigStore) *config.PurchaseExecution {
	accountID := "acct-1"
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
		Recommendations: []config.RecommendationRecord{
			{ID: "r1", CloudAccountID: &accountID},
		},
	}
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, ContactEmail: contact}, nil
	}
	return exec
}

func TestHandler_approvePurchase(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	approver := "admin@example.com"

	mockConfig := new(MockConfigStore)
	exec := approvalTestExec(execID, approver, mockConfig)
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &approver,
	}, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: approver}, nil)

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("ApproveExecution", ctx, execID, "valid-token", approver).Return(nil)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	result, err := handler.approvePurchase(ctx, req, execID, "valid-token")
	require.NoError(t, err)

	resultMap := result.(map[string]string)
	assert.Equal(t, "approved", resultMap["status"])
}

func TestHandler_cancelPurchase(t *testing.T) {
	ctx := context.Background()
	execID := "45645645-6456-4564-5645-645645645645"
	approver := "admin@example.com"

	mockConfig := new(MockConfigStore)
	exec := approvalTestExec(execID, approver, mockConfig)
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &approver,
	}, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: approver}, nil)

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("CancelExecution", ctx, execID, "valid-token", approver).Return(nil)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	result, err := handler.cancelPurchase(ctx, req, execID, "valid-token")
	require.NoError(t, err)

	resultMap := result.(map[string]string)
	assert.Equal(t, "cancelled", resultMap["status"])
}

func TestHandler_approvePurchase_RejectsMismatchedSession(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	approver := "approver@example.com"

	mockConfig := new(MockConfigStore)
	exec := approvalTestExec(execID, approver, mockConfig)
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &approver,
	}, nil)

	mockAuth := new(MockAuthService)
	// Session belongs to someone who is NOT the authorised approver.
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: "wrong@example.com"}, nil)

	mockPurchase := new(MockPurchaseManager)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	_, err := handler.approvePurchase(ctx, req, execID, "valid-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not the authorised approver")
	// ApproveExecution must not have been called — purchase manager mock
	// asserts nothing by construction; a .On(...) entry above would create
	// a false positive, so we pin the negative by confirming the error is
	// the authz error, not an approval-manager error.
	mockPurchase.AssertNotCalled(t, "ApproveExecution")
}

// TestHandler_approvePurchase_RejectsMissingContactEmail covers the
// security-hardened behaviour: when an execution's recommendations do not
// resolve to ANY per-account contact_email, the approval is rejected even
// if the session belongs to the global notification mailbox. Closes the
// loophole where a catch-all inbox could approve purchases on accounts it
// doesn't own.
func TestHandler_approvePurchase_RejectsMissingContactEmail(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	globalNotify := "global@cudly.example"
	accountID := "acct-no-contact"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
		Recommendations: []config.RecommendationRecord{
			{ID: "r1", CloudAccountID: &accountID},
		},
	}
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id /* no ContactEmail */}, nil
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &globalNotify,
	}, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: globalNotify}, nil)

	mockPurchase := new(MockPurchaseManager)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	_, err := handler.approvePurchase(ctx, req, execID, "valid-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no per-account contact email")
	mockPurchase.AssertNotCalled(t, "ApproveExecution")
}

func TestHandler_approvePurchase_RejectsMissingSession(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)

	handler := &Handler{config: mockConfig}

	// No Authorization header → no session → reject.
	_, err := handler.approvePurchase(ctx, &events.LambdaFunctionURLRequest{}, execID, "valid-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sign in")
}

func TestHandler_approvePurchase_AcceptsContactEmailSession(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	contactEmail := "contact@archera.example"
	globalNotify := "global@cudly.example"
	accountID := "acct-1"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
		Recommendations: []config.RecommendationRecord{
			{ID: "r1", CloudAccountID: &accountID},
		},
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &globalNotify,
	}, nil)
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, ContactEmail: contactEmail}, nil
	}

	mockAuth := new(MockAuthService)
	// Session email matches the account contact email — global notify is
	// NOT enough here because a contact email exists for the account.
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: contactEmail}, nil)

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("ApproveExecution", ctx, execID, "valid-token", contactEmail).Return(nil)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	result, err := handler.approvePurchase(ctx, req, execID, "valid-token")
	require.NoError(t, err)
	assert.Equal(t, "approved", result.(map[string]string)["status"])
}

func TestHandler_approvePurchase_RejectsGlobalNotifyWhenContactSet(t *testing.T) {
	// Regression: once an account has contact_email set, the global
	// notification email is only CC'd — it should NOT be accepted as an
	// approver. The session owner of the global notify address must not
	// be able to approve on that account's behalf.
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	contactEmail := "contact@archera.example"
	globalNotify := "global@cudly.example"
	accountID := "acct-1"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "valid-token",
		Status:        "pending",
		Recommendations: []config.RecommendationRecord{
			{ID: "r1", CloudAccountID: &accountID},
		},
	}
	mockConfig.On("GetExecutionByID", ctx, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		NotificationEmail: &globalNotify,
	}, nil)
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, ContactEmail: contactEmail}, nil
	}

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "sess-tok").Return(&Session{Email: globalNotify}, nil)

	mockPurchase := new(MockPurchaseManager)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
	_, err := handler.approvePurchase(ctx, req, execID, "valid-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not the authorised approver")
	mockPurchase.AssertNotCalled(t, "ApproveExecution")
}

func TestHandler_resolveApprovalRecipients_ContactBecomesTo(t *testing.T) {
	ctx := context.Background()
	contactA := "contact-a@example.com"
	contactB := "contact-b@example.com"
	globalNotify := "global@cudly.example"
	accountA := "acct-a"
	accountB := "acct-b"

	mockConfig := new(MockConfigStore)
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		switch id {
		case accountA:
			return &config.CloudAccount{ID: accountA, ContactEmail: contactA}, nil
		case accountB:
			return &config.CloudAccount{ID: accountB, ContactEmail: contactB}, nil
		}
		return nil, nil
	}

	h := &Handler{config: mockConfig}
	recs := []config.RecommendationRecord{
		{ID: "r1", CloudAccountID: &accountA},
		{ID: "r2", CloudAccountID: &accountB},
		{ID: "r3", CloudAccountID: &accountA}, // duplicate account
	}
	to, cc, approvers, err := h.resolveApprovalRecipients(ctx, recs, globalNotify)
	require.NoError(t, err)
	assert.Equal(t, contactA, to, "first contact email becomes To")
	assert.Equal(t, []string{contactB, globalNotify}, cc, "other contact + global end up in Cc")
	assert.Equal(t, []string{contactA, contactB}, approvers, "approvers are the contact emails, not global")
}

// TestHandler_resolveApprovalRecipients_NoContactEmail covers the security-
// hardened behaviour: when no recommendation has a per-account contact_email,
// the global notify mailbox receives the email (To) but is NOT added to the
// approver set. This closes the loophole where a catch-all inbox could
// authorise spend on accounts it doesn't own; authorizeApprovalAction will
// reject the approve/cancel because approvers is empty.
func TestHandler_resolveApprovalRecipients_NoContactEmail(t *testing.T) {
	ctx := context.Background()
	globalNotify := "global@cudly.example"
	accountID := "acct-no-contact"

	mockConfig := new(MockConfigStore)
	mockConfig.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		// No ContactEmail — legacy account.
		return &config.CloudAccount{ID: id}, nil
	}

	h := &Handler{config: mockConfig}
	recs := []config.RecommendationRecord{
		{ID: "r1", CloudAccountID: &accountID},
	}
	to, cc, approvers, err := h.resolveApprovalRecipients(ctx, recs, globalNotify)
	require.NoError(t, err)
	assert.Equal(t, globalNotify, to, "global notify still receives the email as the To addressee")
	assert.Nil(t, cc)
	assert.Empty(t, approvers, "global notify must NOT be in the approver set — only per-account contact_email can approve")
}

// TestHandler_resolveApprovalRecipients_LookupErrorPropagates verifies
// the regression CodeRabbit flagged: a transient GetCloudAccount error
// must NOT silently degrade to a globalNotify-only fallback (which
// would change who is authorised to approve based on a DB blip).
// Instead, the lookup error propagates to the caller, which surfaces
// it as a retriable failure so the operator's next attempt sees the
// real approver list.
func TestHandler_resolveApprovalRecipients_LookupErrorPropagates(t *testing.T) {
	ctx := context.Background()
	globalNotify := "global@cudly.example"
	accountID := "acct-flaky"
	transient := errors.New("connection reset by peer")

	mockConfig := new(MockConfigStore)
	mockConfig.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) {
		return nil, transient
	}

	h := &Handler{config: mockConfig}
	recs := []config.RecommendationRecord{
		{ID: "r1", CloudAccountID: &accountID},
	}
	to, cc, approvers, err := h.resolveApprovalRecipients(ctx, recs, globalNotify)
	require.Error(t, err, "transient lookup error must propagate")
	assert.ErrorIs(t, err, transient, "wrapped error chain must preserve the underlying cause")
	assert.Empty(t, to)
	assert.Nil(t, cc)
	assert.Nil(t, approvers)
}

func TestHandler_getPlannedPurchases(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	scheduledDate := time.Now().AddDate(0, 0, 7)
	executions := []config.PurchaseExecution{
		{
			ExecutionID:      "11111111-1111-1111-1111-111111111111",
			PlanID:           "11111111-1111-1111-1111-111111111111",
			Status:           "pending",
			ScheduledDate:    scheduledDate,
			StepNumber:       1,
			EstimatedSavings: 100.0,
			TotalUpfrontCost: 500.0,
		},
	}

	plans := []config.PurchasePlan{
		{
			ID:   "11111111-1111-1111-1111-111111111111",
			Name: "Test Plan",
			Services: map[string]config.ServiceConfig{
				"aws/rds": {
					Provider: "aws",
					Service:  "rds",
					Term:     3,
					Payment:  "no-upfront",
				},
			},
			RampSchedule: config.RampSchedule{
				TotalSteps: 5,
			},
		},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPendingExecutions", ctx).Return(executions, nil)
	mockStore.On("ListPurchasePlans", ctx).Return(plans, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPlannedPurchases(ctx, req)
	require.NoError(t, err)

	assert.Len(t, result.Purchases, 1)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", result.Purchases[0].ID)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", result.Purchases[0].PlanID)
	assert.Equal(t, "Test Plan", result.Purchases[0].PlanName)
	assert.Equal(t, "aws", result.Purchases[0].Provider)
	assert.Equal(t, "rds", result.Purchases[0].Service)
	assert.Equal(t, 3, result.Purchases[0].Term)
	assert.Equal(t, "no-upfront", result.Purchases[0].Payment)
	assert.Equal(t, 100.0, result.Purchases[0].EstimatedSavings)
	assert.Equal(t, 500.0, result.Purchases[0].UpfrontCost)
	assert.Equal(t, "pending", result.Purchases[0].Status)
	assert.Equal(t, 1, result.Purchases[0].StepNumber)
	assert.Equal(t, 5, result.Purchases[0].TotalSteps)
}

func TestHandler_getPlannedPurchases_ErrorGettingExecutions(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPendingExecutions", ctx).Return(nil, errors.New("database error"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPlannedPurchases(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to get pending executions")
}

func TestHandler_pausePlannedPurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	paused := &config.PurchaseExecution{ExecutionID: "11111111-1111-1111-1111-111111111111", Status: "paused"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "11111111-1111-1111-1111-111111111111", []string{"pending", "running"}, "paused").Return(paused, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.pausePlannedPurchase(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	assert.Equal(t, "paused", result.Status)
}

func TestHandler_pausePlannedPurchase_NotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "99999999-9999-9999-9999-999999999999", []string{"pending", "running"}, "paused").Return(nil, fmt.Errorf("execution not found: 99999999-9999-9999-9999-999999999999"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.pausePlannedPurchase(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_resumePlannedPurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	resumed := &config.PurchaseExecution{ExecutionID: "11111111-1111-1111-1111-111111111111", Status: "pending"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "11111111-1111-1111-1111-111111111111", []string{"paused"}, "pending").Return(resumed, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.resumePlannedPurchase(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	assert.Equal(t, "resumed", result.Status)
}

func TestHandler_runPlannedPurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	transitioned := &config.PurchaseExecution{
		ExecutionID: "11111111-1111-1111-1111-111111111111",
		Status:      "running",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "11111111-1111-1111-1111-111111111111", []string{"pending", "paused"}, "running").Return(transitioned, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.runPlannedPurchase(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	resultMap := result.(map[string]interface{})
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", resultMap["execution_id"])
	assert.Equal(t, "running", resultMap["status"])
}

func TestHandler_deletePlannedPurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	cancelled := &config.PurchaseExecution{ExecutionID: "11111111-1111-1111-1111-111111111111", Status: "cancelled"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "11111111-1111-1111-1111-111111111111", []string{"pending", "paused"}, "cancelled").Return(cancelled, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.deletePlannedPurchase(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	assert.Equal(t, "cancelled", result.Status)
}

func TestHandler_pausePlannedPurchase_NilExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "99999999-9999-9999-9999-999999999999", []string{"pending", "running"}, "paused").Return(nil, fmt.Errorf("execution not found: 99999999-9999-9999-9999-999999999999"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.pausePlannedPurchase(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_resumePlannedPurchase_NilExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "99999999-9999-9999-9999-999999999999", []string{"paused"}, "pending").Return(nil, fmt.Errorf("execution not found: 99999999-9999-9999-9999-999999999999"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.resumePlannedPurchase(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_runPlannedPurchase_NilExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "99999999-9999-9999-9999-999999999999", []string{"pending", "paused"}, "running").Return(nil, fmt.Errorf("execution not found: 99999999-9999-9999-9999-999999999999"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.runPlannedPurchase(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_deletePlannedPurchase_NilExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("TransitionExecutionStatus", ctx, "99999999-9999-9999-9999-999999999999", []string{"pending", "paused"}, "cancelled").Return(nil, fmt.Errorf("execution not found: 99999999-9999-9999-9999-999999999999"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.deletePlannedPurchase(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_getPlannedPurchases_ErrorGettingPlans(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	executions := []config.PurchaseExecution{{ExecutionID: "11111111-1111-1111-1111-111111111111", PlanID: "11111111-1111-1111-1111-111111111111"}}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPendingExecutions", ctx).Return(executions, nil)
	mockStore.On("ListPurchasePlans", ctx).Return(nil, errors.New("database error"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPlannedPurchases(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to get purchase plans")
}

// Tests for getPurchaseDetails

func TestHandler_getPurchaseDetails_Success(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	scheduledDate := time.Now().AddDate(0, 0, 7)
	execution := &config.PurchaseExecution{
		ExecutionID:      "11111111-1111-1111-1111-111111111111",
		PlanID:           "22222222-2222-2222-2222-222222222222",
		Status:           "pending",
		StepNumber:       1,
		ScheduledDate:    scheduledDate,
		TotalUpfrontCost: 1000.0,
		EstimatedSavings: 500.0,
	}

	plan := &config.PurchasePlan{
		ID:   "22222222-2222-2222-2222-222222222222",
		Name: "Test Plan",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetExecutionByID", ctx, "11111111-1111-1111-1111-111111111111").Return(execution, nil)
	mockStore.On("GetPurchasePlan", ctx, "22222222-2222-2222-2222-222222222222").Return(plan, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPurchaseDetails(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	resultMap := result.(map[string]interface{})
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", resultMap["execution_id"])
	assert.Equal(t, "22222222-2222-2222-2222-222222222222", resultMap["plan_id"])
	assert.Equal(t, "Test Plan", resultMap["plan_name"])
	assert.Equal(t, "pending", resultMap["status"])
	assert.Equal(t, 1, resultMap["step_number"])
	assert.Equal(t, 1000.0, resultMap["total_upfront_cost"])
	assert.Equal(t, 500.0, resultMap["estimated_savings"])
}

func TestHandler_getPurchaseDetails_InvalidUUID(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPurchaseDetails(ctx, req, "invalid-uuid")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid ID format")
}

func TestHandler_getPurchaseDetails_NotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetExecutionByID", ctx, "99999999-9999-9999-9999-999999999999").Return(nil, errors.New("not found"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPurchaseDetails(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "execution not found")
}

func TestHandler_getPurchaseDetails_NilExecution(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetExecutionByID", ctx, "99999999-9999-9999-9999-999999999999").Return(nil, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPurchaseDetails(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "execution not found")
}

func TestHandler_getPurchaseDetails_WithTimestamps(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	scheduledDate := time.Now().AddDate(0, 0, 7)
	notificationSent := time.Now().AddDate(0, 0, -1)
	completedAt := time.Now()
	execution := &config.PurchaseExecution{
		ExecutionID:      "11111111-1111-1111-1111-111111111111",
		PlanID:           "22222222-2222-2222-2222-222222222222",
		Status:           "completed",
		StepNumber:       1,
		ScheduledDate:    scheduledDate,
		NotificationSent: &notificationSent,
		CompletedAt:      &completedAt,
		Error:            "some error",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetExecutionByID", ctx, "11111111-1111-1111-1111-111111111111").Return(execution, nil)
	mockStore.On("GetPurchasePlan", ctx, "22222222-2222-2222-2222-222222222222").Return(nil, errors.New("not found"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPurchaseDetails(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	resultMap := result.(map[string]interface{})
	assert.Equal(t, "completed", resultMap["status"])
	assert.NotNil(t, resultMap["notification_sent"])
	assert.NotNil(t, resultMap["completed_at"])
	assert.Equal(t, "some error", resultMap["error"])
}

// Tests for executePurchase

func TestHandler_executePurchase_Success(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	// executePurchase reads GlobalConfig to look up the per-provider
	// grace period. Return an empty-but-valid config so the grace
	// window falls back to defaults and no suppression rows get
	// written (the recs in this request have no CloudAccountID).
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"recommendations": [{"id": "rec-1", "upfront_cost": 100.0, "savings": 50.0}, {"id": "rec-2", "upfront_cost": 200.0, "savings": 100.0}]}`,
	}
	result, err := handler.executePurchase(ctx, req)
	require.NoError(t, err)

	resultMap := result.(map[string]interface{})
	// With no emailNotifier wired on the handler the approval email cannot
	// send, and the execution is dead on arrival (no one can ever approve
	// it — the token only lives in the email). The handler flips the status
	// from "pending" to "failed" so the History view shows it correctly
	// instead of parking it in Pending forever.
	assert.Equal(t, "failed", resultMap["status"])
	assert.Equal(t, 2, resultMap["recommendation_count"])
	assert.Equal(t, 300.0, resultMap["total_upfront_cost"])
	assert.Equal(t, 150.0, resultMap["estimated_savings"])
	assert.NotEmpty(t, resultMap["execution_id"])
	assert.Equal(t, false, resultMap["email_sent"], "email_sent must be false when emailNotifier is nil")
	assert.Equal(t, "email notifier not configured for this deployment", resultMap["email_reason"])
}

func TestHandler_executePurchase_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `invalid json`,
	}
	result, err := handler.executePurchase(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_executePurchase_EmptyRecommendations(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"recommendations": []}`,
	}
	result, err := handler.executePurchase(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no recommendations provided")
}

func TestHandler_executePurchase_NegativeUpfrontCost(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"recommendations": [{"id": "rec-1", "upfront_cost": -100.0, "savings": 50.0}]}`,
	}
	result, err := handler.executePurchase(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "negative upfront cost")
}

func TestHandler_executePurchase_NegativeSavings(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"recommendations": [{"id": "rec-1", "upfront_cost": 100.0, "savings": -50.0}]}`,
	}
	result, err := handler.executePurchase(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "negative savings")
}

func TestHandler_executePurchase_TooManyRecommendations(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{auth: mockAuth}

	// Create JSON with 1001 recommendations (exceeds max of 1000)
	recommendations := make([]map[string]interface{}, 1001)
	for i := range recommendations {
		recommendations[i] = map[string]interface{}{
			"id":           fmt.Sprintf("rec-%d", i),
			"upfront_cost": 1.0,
			"savings":      0.5,
		}
	}
	body, _ := json.Marshal(map[string]interface{}{"recommendations": recommendations})

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: string(body),
	}
	result, err := handler.executePurchase(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "too many recommendations")
}

func TestHandler_executePurchase_ExceedsMaxAmount(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"recommendations": [{"id": "rec-1", "upfront_cost": 15000000.0, "savings": 50.0}]}`,
	}
	result, err := handler.executePurchase(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "exceeds maximum allowed")
}

func TestHandler_executePurchase_SaveError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(errors.New("database error"))
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"recommendations": [{"id": "rec-1", "upfront_cost": 100.0, "savings": 50.0}]}`,
	}
	result, err := handler.executePurchase(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to save execution")
}

// TestHandler_pausePlannedPurchase_OutOfScope locks down that a non-admin
// user whose allowed_accounts do not intersect with the execution's plan
// gets 404 and never reaches TransitionExecutionStatus. Covers the
// requireExecutionAccess hop added in the plans/purchases scoping commit.
func TestHandler_pausePlannedPurchase_OutOfScope(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	executionID := "77777777-7777-7777-7777-777777777777"
	planID := "88888888-8888-8888-8888-888888888888"

	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(&Session{
		UserID: "viewer-1", Role: "user",
	}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "update", "purchases").Return(true, nil)
	mockAuth.On("GetAllowedAccountsAPI", ctx, "viewer-1").Return([]string{"Production"}, nil)
	mockStore.On("GetExecutionByID", ctx, executionID).Return(&config.PurchaseExecution{
		ExecutionID: executionID, PlanID: planID,
	}, nil)

	store := &mockStoreWithPlanAccounts{
		MockConfigStore: mockStore,
		planAccounts: map[string][]config.CloudAccount{
			planID: {{ID: "acc-stage", Name: "Staging"}},
		},
	}

	handler := &Handler{auth: mockAuth, config: store}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer viewer-token"},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Path: "/api/purchases/planned/" + executionID + "/pause",
			},
		},
	}
	_, err := handler.pausePlannedPurchase(ctx, req, executionID)
	require.Error(t, err)
	assert.True(t, IsNotFoundError(err), "expected 404 not-found, got %v", err)
	mockStore.AssertNotCalled(t, "TransitionExecutionStatus")
}

// ─── Session-authed Cancel (issue #46) ─────────────────────────────────────
//
// Covers the full cancel-any / cancel-own RBAC matrix on the
// session-authed branch of cancelPurchase (token == ""):
//
//   1. admin                                     → allowed (any execution)
//   2. user with cancel-any (e.g. ops role)      → allowed (any execution)
//   3. user with cancel-own + matching creator   → allowed
//   4. user with cancel-own + different creator  → 403
//   5. user with neither verb                    → 403
//   6. cancellable-state guard                   → 409 on non-pending status
//   7. legacy NULL creator + non-admin cancel-own → 403 (still reachable
//      via the email-token path, which is exercised by the existing
//      TestHandler_cancelPurchase happy-path test).

const cancelExecID = "55555555-5555-5555-5555-555555555555"
const cancelCallerID = "66666666-6666-6666-6666-666666666666"
const cancelOtherID = "77777777-7777-7777-7777-777777777777"

// buildSessionCancelHandler wires the handler with mocks the session-authed
// cancel tests share. Token is left empty by callers when invoking
// cancelPurchase to drive the new branch.
func buildSessionCancelHandler(exec *config.PurchaseExecution, session *Session, hasAny, hasOwn bool) (*Handler, *MockConfigStore, *MockAuthService) {
	mockConfig := new(MockConfigStore)
	mockConfig.On("GetExecutionByID", mock.Anything, exec.ExecutionID).Return(exec, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", mock.Anything, "sess-tok").Return(session, nil)
	if session != nil && session.Role != "admin" {
		mockAuth.On("HasPermissionAPI", mock.Anything, session.UserID, "cancel-any", "purchases").Return(hasAny, nil).Maybe()
		mockAuth.On("HasPermissionAPI", mock.Anything, session.UserID, "cancel-own", "purchases").Return(hasOwn, nil).Maybe()
	}

	return &Handler{config: mockConfig, auth: mockAuth}, mockConfig, mockAuth
}

func sessionCancelReq() *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
}

// runSessionCancelAllowed asserts the success path of the session-authed
// branch given a permission-matrix cell that should be allowed. The
// cancel commits in a single tx (SavePurchaseExecutionTx +
// DeleteSuppressionsByExecutionTx via WithTx); the mock store's WithTx
// default forwards fn(nil) and SavePurchaseExecutionTx default routes
// through SavePurchaseExecution, which we wire here. The suppression
// delete returns nil by default so we don't need to register it.
//
// Captures the saved execution so the caller can assert the audit-stamp
// invariants — primarily that CancelledBy is set to session.Email when
// the session has a non-empty email. cancelPurchase relies on this stamp
// for History UI attribution; if SavePurchaseExecution stops being
// called with the email-bearing copy the matrix tests would otherwise
// silently regress.
func runSessionCancelAllowed(t *testing.T, exec *config.PurchaseExecution, session *Session, hasAny, hasOwn bool) {
	t.Helper()
	handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, hasAny, hasOwn)
	var saved *config.PurchaseExecution
	mockConfig.On("SavePurchaseExecution", mock.Anything, mock.AnythingOfType("*config.PurchaseExecution")).
		Run(func(args mock.Arguments) {
			saved = args.Get(1).(*config.PurchaseExecution)
		}).
		Return(nil)

	result, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "")
	require.NoError(t, err)
	assert.Equal(t, "cancelled", result.(map[string]string)["status"])
	// Status flip + suppression cleanup are paired in one tx — the mock
	// only sees the un-tx variants because of how MockConfigStore wires
	// SavePurchaseExecutionTx → SavePurchaseExecution. Asserting the
	// un-tx call ran is enough for the matrix tests; the atomicity
	// itself is exercised by the live integration tests.
	mockConfig.AssertCalled(t, "SavePurchaseExecution", mock.Anything, mock.AnythingOfType("*config.PurchaseExecution"))
	require.NotNil(t, saved, "SavePurchaseExecution should have captured the execution")
	assert.Equal(t, "cancelled", saved.Status)
	if session != nil && session.Email != "" {
		require.NotNil(t, saved.CancelledBy, "CancelledBy must be stamped when session has an email")
		assert.Equal(t, session.Email, *saved.CancelledBy, "CancelledBy must equal session.Email for audit attribution")
	}
	// Verify the session-auth boundary actually fired — without this a
	// regression that bypassed ValidateSession (or stopped consulting
	// HasPermissionAPI for non-admins) would silently still pass the
	// status/audit assertions above.
	mockAuth.AssertExpectations(t)
}

func TestHandler_cancelPurchase_Session_Admin_AllowsAny(t *testing.T) {
	creator := cancelOtherID
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "pending",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: cancelCallerID, Role: "admin", Email: "admin@example.com"}
	runSessionCancelAllowed(t, exec, session, false, false)
}

func TestHandler_cancelPurchase_Session_CancelAny_AllowsAny(t *testing.T) {
	// Non-admin operator role with cancel-any:purchases. Future use case
	// (no role currently has it by default) but the verb exists today.
	creator := cancelOtherID
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "pending",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: cancelCallerID, Role: "user", Email: "ops@example.com"}
	runSessionCancelAllowed(t, exec, session, true, false)
}

func TestHandler_cancelPurchase_Session_CancelOwn_AllowsCreator(t *testing.T) {
	creator := cancelCallerID // execution created by the same user
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "notified",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: cancelCallerID, Role: "user", Email: "u1@example.com"}
	runSessionCancelAllowed(t, exec, session, false, true)
}

func TestHandler_cancelPurchase_Session_CancelOwn_RejectsNonCreator(t *testing.T) {
	creator := cancelOtherID // someone else created it
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "pending",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: cancelCallerID, Role: "user", Email: "u1@example.com"}

	handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, false, true)

	_, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another user's pending purchase")
	mockConfig.AssertNotCalled(t, "WithTx")
	mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
	mockAuth.AssertExpectations(t)
}

func TestHandler_cancelPurchase_Session_NoVerb_Rejects(t *testing.T) {
	creator := cancelCallerID // even own row is rejected without the verb
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "pending",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: cancelCallerID, Role: "user", Email: "u1@example.com"}

	handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, false, false)

	_, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cancel-any or cancel-own")
	mockConfig.AssertNotCalled(t, "WithTx")
	mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
	mockAuth.AssertExpectations(t)
}

func TestHandler_cancelPurchase_Session_RejectsTerminalStatus(t *testing.T) {
	creator := cancelCallerID
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "completed", // already done — cannot transition
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: cancelCallerID, Role: "admin"}

	handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, false, false)

	_, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be cancelled")
	assert.Contains(t, err.Error(), "completed")
	mockConfig.AssertNotCalled(t, "WithTx")
	mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
	mockAuth.AssertExpectations(t)
}

func TestHandler_cancelPurchase_Session_LegacyNullCreator_NonAdminRejected(t *testing.T) {
	// Pre-migration row: created_by_user_id is NULL. cancel-own can't
	// match a NULL creator, so a non-admin must be rejected. The email
	// token in the inbox stays the escape hatch (covered by the existing
	// TestHandler_cancelPurchase happy-path test).
	exec := &config.PurchaseExecution{
		ExecutionID:     cancelExecID,
		Status:          "pending",
		CreatedByUserID: nil,
	}
	session := &Session{UserID: cancelCallerID, Role: "user", Email: "u1@example.com"}

	handler, mockConfig, mockAuth := buildSessionCancelHandler(exec, session, false, true)

	_, err := handler.cancelPurchase(context.Background(), sessionCancelReq(), cancelExecID, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another user's pending purchase")
	mockConfig.AssertNotCalled(t, "WithTx")
	mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
	mockAuth.AssertExpectations(t)
}

func TestHandler_cancelPurchase_Session_RejectsMissingSession(t *testing.T) {
	exec := &config.PurchaseExecution{ExecutionID: cancelExecID, Status: "pending"}
	mockConfig := new(MockConfigStore)
	mockConfig.On("GetExecutionByID", mock.Anything, cancelExecID).Return(exec, nil)

	handler := &Handler{config: mockConfig, auth: new(MockAuthService)}
	// No Authorization header → 401, not 403. Tokenless + sessionless is
	// the only state where the user can't reach either branch.
	_, err := handler.cancelPurchase(context.Background(), &events.LambdaFunctionURLRequest{}, cancelExecID, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authorization token provided")
}

// ─── Session-authed Retry (issue #47) ──────────────────────────────────────
//
// Mirror image of the cancel matrix above. retryPurchase creates a NEW
// execution from the failed row's stored Recommendations slice and stamps
// retry_execution_id on the original.
//
// Covered cells:
//   1. admin                                     → allowed (any failed row)
//   2. user with retry-any (operator role)       → allowed (any failed row)
//   3. user with retry-own + matching creator    → allowed
//   4. user with retry-own + different creator   → 403
//   5. user with neither verb                    → 403
//   6. failed-state guard                        → 409 on non-failed status
//   7. legacy NULL creator + non-admin retry-own → 403
//   8. persistent-failure block                  → 409 + ops_hint
//   9. threshold soft-block                      → 409 (n=5, no force)
//  10. threshold soft-block + force=true         → allowed (n=5, force=true)
//  11. just-under threshold                      → allowed (n=4)
//  12. happy path: linkage + retry_attempt_n+1

const retryExecID = "88888888-8888-8888-8888-888888888888"
const retryCallerID = "99999999-9999-9999-9999-999999999999"
const retryOtherID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

func buildSessionRetryHandler(failed *config.PurchaseExecution, session *Session, hasAny, hasOwn bool) (*Handler, *MockConfigStore, *MockAuthService) {
	mockConfig := new(MockConfigStore)
	mockConfig.On("GetExecutionByID", mock.Anything, failed.ExecutionID).Return(failed, nil)
	// GetGlobalConfig is consulted for grace-period suppressions; an
	// empty config is fine (no suppressions written).
	mockConfig.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil).Maybe()

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", mock.Anything, "sess-tok").Return(session, nil)
	if session != nil && session.Role != "admin" {
		mockAuth.On("HasPermissionAPI", mock.Anything, session.UserID, "retry-any", "purchases").Return(hasAny, nil).Maybe()
		mockAuth.On("HasPermissionAPI", mock.Anything, session.UserID, "retry-own", "purchases").Return(hasOwn, nil).Maybe()
	}

	return &Handler{config: mockConfig, auth: mockAuth}, mockConfig, mockAuth
}

func sessionRetryReq() *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"authorization": "Bearer sess-tok"},
	}
}

func sessionRetryReqWithForce() *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers:               map[string]string{"authorization": "Bearer sess-tok"},
		QueryStringParameters: map[string]string{"force": "true"},
	}
}

// runSessionRetryAllowed asserts the success path of retryPurchase given a
// permission-matrix cell that should be allowed. Captures BOTH saves —
// the new successor execution AND the original failed row updated with
// the linkage pointer — so callers can assert linkage invariants
// (retry_attempt_n stamped to predecessor.n+1, RetryExecutionID on the
// original points at the successor).
func runSessionRetryAllowed(t *testing.T, failed *config.PurchaseExecution, session *Session, hasAny, hasOwn bool, req *events.LambdaFunctionURLRequest) (newExec, updatedOriginal *config.PurchaseExecution) {
	t.Helper()
	handler, mockConfig, mockAuth := buildSessionRetryHandler(failed, session, hasAny, hasOwn)
	saved := []*config.PurchaseExecution{}
	mockConfig.On("SavePurchaseExecution", mock.Anything, mock.AnythingOfType("*config.PurchaseExecution")).
		Run(func(args mock.Arguments) {
			// Copy so subsequent in-place mutations by the handler
			// (e.g. finalizePurchaseStatus flipping status to failed
			// when the email path errors out) don't retroactively
			// rewrite the captured record.
			snap := *args.Get(1).(*config.PurchaseExecution)
			saved = append(saved, &snap)
		}).
		Return(nil)

	result, err := handler.retryPurchase(context.Background(), req, failed.ExecutionID)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.NotEmpty(t, resp["execution_id"])
	assert.Equal(t, failed.ExecutionID, resp["original_execution"])
	require.GreaterOrEqual(t, len(saved), 2, "expected at least 2 SavePurchaseExecution calls (new + original linkage)")

	// First save is the new successor; second is the original with
	// retry_execution_id stamped. The retry tx orders them this way
	// so the FK constraint on retry_execution_id is satisfied.
	newExec = saved[0]
	updatedOriginal = saved[1]
	mockAuth.AssertExpectations(t)
	return newExec, updatedOriginal
}

func TestHandler_retryPurchase_Admin_AllowsAny(t *testing.T) {
	creator := retryOtherID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		Error:           "send failed: transient SES throttle",
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID, Role: "admin", Email: "admin@example.com"}
	newExec, updated := runSessionRetryAllowed(t, failed, session, false, false, sessionRetryReq())
	assert.Equal(t, "pending", newExec.Status)
	assert.Equal(t, 1, newExec.RetryAttemptN, "fresh first retry → n=1")
	require.NotNil(t, updated.RetryExecutionID, "original must carry pointer to successor")
	assert.Equal(t, newExec.ExecutionID, *updated.RetryExecutionID)
	assert.Equal(t, "failed", updated.Status, "original keeps failed status as historical record")
}

func TestHandler_retryPurchase_RetryAny_AllowsAny(t *testing.T) {
	creator := retryOtherID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		Error:           "send failed: transient SES throttle",
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID, Role: "user", Email: "ops@example.com"}
	runSessionRetryAllowed(t, failed, session, true, false, sessionRetryReq())
}

func TestHandler_retryPurchase_RetryOwn_AllowsCreator(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		Error:           "send failed: SES recipient mailbox full",
		CreatedByUserID: &creator,
		RetryAttemptN:   2, // already retried twice
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID, Role: "user", Email: "u1@example.com"}
	newExec, updated := runSessionRetryAllowed(t, failed, session, false, true, sessionRetryReq())
	assert.Equal(t, 3, newExec.RetryAttemptN, "n=2 predecessor → n=3 successor")
	require.NotNil(t, updated.RetryExecutionID)
	assert.Equal(t, newExec.ExecutionID, *updated.RetryExecutionID)
}

func TestHandler_retryPurchase_RetryOwn_RejectsNonCreator(t *testing.T) {
	creator := retryOtherID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: retryCallerID, Role: "user", Email: "u1@example.com"}

	handler, mockConfig, mockAuth := buildSessionRetryHandler(failed, session, false, true)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another user's failed purchase")
	mockConfig.AssertNotCalled(t, "WithTx")
	mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
	mockAuth.AssertExpectations(t)
}

func TestHandler_retryPurchase_NoVerb_Rejects(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: retryCallerID, Role: "user", Email: "u1@example.com"}

	handler, mockConfig, mockAuth := buildSessionRetryHandler(failed, session, false, false)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retry-any or retry-own")
	mockConfig.AssertNotCalled(t, "WithTx")
	mockConfig.AssertNotCalled(t, "SavePurchaseExecution")
	mockAuth.AssertExpectations(t)
}

func TestHandler_retryPurchase_RejectsNonFailedStatus(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "completed", // already done — no retry from here
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: retryCallerID, Role: "admin"}
	handler, mockConfig, _ := buildSessionRetryHandler(failed, session, false, false)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be retried")
	assert.Contains(t, err.Error(), "completed")
	mockConfig.AssertNotCalled(t, "WithTx")
}

func TestHandler_retryPurchase_LegacyNullCreator_NonAdminRejected(t *testing.T) {
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		CreatedByUserID: nil, // pre-migration row
	}
	session := &Session{UserID: retryCallerID, Role: "user", Email: "u1@example.com"}
	handler, mockConfig, mockAuth := buildSessionRetryHandler(failed, session, false, true)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another user's failed purchase")
	mockConfig.AssertNotCalled(t, "WithTx")
	mockAuth.AssertExpectations(t)
}

func TestHandler_retryPurchase_PersistentFailure_BlocksWithOpsHint(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		Error:           "FROM_EMAIL not configured for this deployment",
		CreatedByUserID: &creator,
	}
	session := &Session{UserID: retryCallerID, Role: "admin"}
	handler, mockConfig, _ := buildSessionRetryHandler(failed, session, false, false)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "operator-fixable")
	// The structured details carry the ops hint so the frontend can
	// render the badge without parsing the message.
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 409, ce.code)
	require.NotNil(t, ce.Details())
	assert.Equal(t, "Set FROM_EMAIL tfvar then retry", ce.Details()["ops_hint"])
	mockConfig.AssertNotCalled(t, "WithTx")
}

func TestHandler_retryPurchase_PersistentFailure_NoMatch_AllowsRetry(t *testing.T) {
	// Transient SES throttle is NOT in the persistent-failure map →
	// retry proceeds normally. Sanity check that we don't accidentally
	// classify all SES errors as persistent.
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		Error:           "send failed: SES throttle exceeded, please retry",
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID, Role: "admin"}
	runSessionRetryAllowed(t, failed, session, false, false, sessionRetryReq())
}

func TestHandler_retryPurchase_Threshold_BlocksAtFive_NoForce(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		RetryAttemptN:   5, // already at the threshold
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID, Role: "admin"}
	handler, mockConfig, _ := buildSessionRetryHandler(failed, session, false, false)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "force=true")
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 409, ce.code)
	require.NotNil(t, ce.Details())
	assert.Equal(t, 5, ce.Details()["retry_attempt_n"])
	mockConfig.AssertNotCalled(t, "WithTx")
}

func TestHandler_retryPurchase_Threshold_AllowsWithForce(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		RetryAttemptN:   5,
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID, Role: "admin"}
	newExec, _ := runSessionRetryAllowed(t, failed, session, false, false, sessionRetryReqWithForce())
	assert.Equal(t, 6, newExec.RetryAttemptN, "force=true past threshold still increments the chain count")
}

func TestHandler_retryPurchase_JustUnderThreshold_AllowsNoForce(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		Status:          "failed",
		RetryAttemptN:   4, // n=4 < threshold=5 → allowed
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID, Role: "admin"}
	newExec, _ := runSessionRetryAllowed(t, failed, session, false, false, sessionRetryReq())
	assert.Equal(t, 5, newExec.RetryAttemptN)
}

func TestHandler_retryPurchase_AlreadyRetried_Rejects(t *testing.T) {
	// A failed row that already has a retry_execution_id pointer must
	// not be retried again — that would silently overwrite the linkage
	// and orphan the previous chain.
	creator := retryCallerID
	successor := "11111111-2222-3333-4444-555555555555"
	failed := &config.PurchaseExecution{
		ExecutionID:      retryExecID,
		Status:           "failed",
		CreatedByUserID:  &creator,
		RetryExecutionID: &successor,
		Recommendations:  []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID, Role: "admin"}
	handler, mockConfig, _ := buildSessionRetryHandler(failed, session, false, false)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already retried")
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 409, ce.code)
	assert.Equal(t, successor, ce.Details()["retry_execution_id"])
	mockConfig.AssertNotCalled(t, "WithTx")
}

func TestHandler_retryPurchase_RejectsMissingSession(t *testing.T) {
	failed := &config.PurchaseExecution{ExecutionID: retryExecID, Status: "failed"}
	mockConfig := new(MockConfigStore)
	mockConfig.On("GetExecutionByID", mock.Anything, retryExecID).Return(failed, nil)
	handler := &Handler{config: mockConfig, auth: new(MockAuthService)}
	_, err := handler.retryPurchase(context.Background(), &events.LambdaFunctionURLRequest{}, retryExecID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authorization token provided")
}

// TestHandler_retryPurchase_PreservesPlanMetadata verifies that retry
// successors inherit PlanID + StepNumber from the predecessor (CR #168
// review — without this, a retried planned execution would drop out of
// plan-scoped history and lose its ramp-step attribution).
func TestHandler_retryPurchase_PreservesPlanMetadata(t *testing.T) {
	creator := retryCallerID
	failed := &config.PurchaseExecution{
		ExecutionID:     retryExecID,
		PlanID:          "plan-abc",
		StepNumber:      3,
		Status:          "failed",
		Error:           "send failed: transient SES throttle",
		CreatedByUserID: &creator,
		Recommendations: []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	session := &Session{UserID: retryCallerID, Role: "admin"}
	newExec, _ := runSessionRetryAllowed(t, failed, session, false, false, sessionRetryReq())
	assert.Equal(t, "plan-abc", newExec.PlanID, "successor must inherit predecessor PlanID")
	assert.Equal(t, 3, newExec.StepNumber, "successor must inherit predecessor StepNumber")
}

// TestHandler_retryPurchase_AlreadyRetried_RBACBeforeLeak verifies the
// fix to a CR #168 finding: the already-retried 409 must NOT fire for
// an unauthorized session, because doing so would leak the descendant
// execution UUID to anyone who can guess a failed-row ID. The
// authorization gate must run first and surface a 403 instead.
func TestHandler_retryPurchase_AlreadyRetried_RBACBeforeLeak(t *testing.T) {
	creator := retryOtherID // someone else owns the failed row
	successor := "11111111-2222-3333-4444-555555555555"
	failed := &config.PurchaseExecution{
		ExecutionID:      retryExecID,
		Status:           "failed",
		CreatedByUserID:  &creator,
		RetryExecutionID: &successor, // already retried
		Recommendations:  []config.RecommendationRecord{{Provider: "aws", Service: "ec2", Term: 1}},
	}
	// Caller is a non-admin holding NEITHER retry-any nor retry-own —
	// must hit the 403 from authorizeSessionRetry, NOT the 409 with
	// successor exposure.
	session := &Session{UserID: retryCallerID, Role: "user"}
	handler, _, _ := buildSessionRetryHandler(failed, session, false, false)
	_, err := handler.retryPurchase(context.Background(), sessionRetryReq(), retryExecID)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 403, ce.code, "must fail with 403 (RBAC) not 409 (already-retried)")
	assert.NotContains(t, err.Error(), "already retried")
	// Most importantly: details must NOT leak the descendant UUID.
	if d := ce.Details(); d != nil {
		_, leaked := d["retry_execution_id"]
		assert.False(t, leaked, "403 must not surface retry_execution_id")
	}
}

func TestResolveOpsHint(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty input", "", ""},
		{"transient SES throttle (no match)", "send failed: SES throttle exceeded", ""},
		{"FROM_EMAIL exact", "FROM_EMAIL not configured for this deployment", "Set FROM_EMAIL tfvar then retry"},
		{"SES sandbox case-insensitive", "send failed: ses sandbox active", "Move SES out of sandbox or verify recipient, then retry"},
		{"domain not verified", "send failed: SES domain not verified", "Verify SES domain in AWS console, then retry"},
		{"IAM denied", "AssumeRole error: IAM denied", "Grant the deploy role missing IAM permission, then retry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, resolveOpsHint(tt.input))
		})
	}
}
