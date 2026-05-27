package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandler_listPlans(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	plans := []config.PurchasePlan{
		{ID: "11111111-1111-1111-1111-111111111111", Name: "Test Plan 1", Enabled: true},
		{ID: "22222222-2222-2222-2222-222222222222", Name: "Test Plan 2", Enabled: false},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return(plans, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.listPlans(ctx, req, map[string]string{})
	require.NoError(t, err)

	assert.Len(t, result.Plans, 2)
}

func TestHandler_listPlans_AccountIDsFilter(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	plans := []config.PurchasePlan{
		{ID: "11111111-1111-1111-1111-111111111111", Name: "Account Plan", Enabled: true},
	}

	accountID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	expectedFilter := config.PurchasePlanFilter{AccountIDs: []string{accountID}}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("ListPurchasePlans", ctx, expectedFilter).Return(plans, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	params := map[string]string{"account_ids": accountID}
	result, err := handler.listPlans(ctx, req, params)
	require.NoError(t, err)

	assert.Len(t, result.Plans, 1)
	assert.Equal(t, "Account Plan", result.Plans[0].Name)
}

func TestHandler_createPlan(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	targetAccountID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("CreatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)
	mockStore.On("SetPlanAccounts", ctx, mock.AnythingOfType("string"), []string{targetAccountID}).Return(nil)

	// validatePlanAccountProviders looks up each target account and checks
	// its provider matches the plan's. Stub GetCloudAccount to return an
	// aws account so the provider-match passes (plan service is aws:rds).
	mockStore.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, Name: "test-aws", Provider: "aws"}, nil
	}

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"name": "New Plan", "enabled": true, "auto_purchase": false, "provider": "aws", "service": "rds", "target_accounts": ["` + targetAccountID + `"]}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.createPlan(ctx, req)
	require.NoError(t, err)

	plan := result.(*config.PurchasePlan)
	assert.Equal(t, "New Plan", plan.Name)
	assert.True(t, plan.Enabled)
	mockStore.AssertCalled(t, "SetPlanAccounts", ctx, mock.AnythingOfType("string"), []string{targetAccountID})
}

func TestHandler_createPlan_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{corsAllowedOrigin: "*", auth: mockAuth}

	body := `{invalid}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.createPlan(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestHandler_createPlan_RejectsEmptyTargetAccounts verifies the universal-plan
// fix: a POST /plans without target_accounts (or with an empty list) returns
// HTTP 400 and never reaches CreatePurchasePlan. This is the design invariant
// every purchase_plans row must have at least one matching plan_accounts row.
func TestHandler_createPlan_RejectsEmptyTargetAccounts(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	cases := []struct {
		name string
		body string
	}{
		{"missing field", `{"name": "P", "provider": "aws", "service": "rds"}`},
		{"empty array", `{"name": "P", "provider": "aws", "service": "rds", "target_accounts": []}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Fresh store per case: AssertNotCalled below would otherwise
			// see the previous case's setup; tests share nothing.
			mockStore := new(MockConfigStore)
			handler := &Handler{config: mockStore, auth: mockAuth}
			req := &events.LambdaFunctionURLRequest{
				Headers: map[string]string{"Authorization": "Bearer admin-token"},
				Body:    tc.body,
			}
			result, err := handler.createPlan(ctx, req)
			assert.Nil(t, result)
			require.Error(t, err)
			ce, ok := IsClientError(err)
			require.True(t, ok, "expected ClientError, got %T: %v", err, err)
			assert.Equal(t, 400, ce.code)
			assert.Contains(t, ce.Error(), "target_accounts")
			mockStore.AssertNotCalled(t, "CreatePurchasePlan", mock.Anything, mock.Anything)
		})
	}
}

// TestHandler_createPlan_RollbackDeleteFailureSurfacesOriginalError verifies
// that when SetPlanAccounts fails after CreatePurchasePlan succeeds, AND the
// rollback DeletePurchasePlan also fails, the caller still receives the
// original SetPlanAccounts error (wrapped as "accounts: …") rather than the
// rollback error. The rollback failure is logged at WARN so an operator can
// clean up manually — see handler_plans.go createPlan rollbackPlan closure.
// Regression guard for CR #743 finding F1: rollback errors must not be
// silently discarded.
func TestHandler_createPlan_RollbackDeleteFailureSurfacesOriginalError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}
	targetAccountID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("CreatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)
	setAccountsErr := errors.New("setplanaccounts boom")
	mockStore.On("SetPlanAccounts", ctx, mock.AnythingOfType("string"), []string{targetAccountID}).Return(setAccountsErr)
	// Rollback DeletePurchasePlan also fails — caller must still get the
	// original SetPlanAccounts error, not the rollback error.
	deleteErr := errors.New("rollback delete boom")
	mockStore.On("DeletePurchasePlan", ctx, mock.AnythingOfType("string")).Return(deleteErr)

	// Stub GetCloudAccount so provider-match validation passes — we want to
	// reach the SetPlanAccounts step.
	mockStore.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, Name: "test-aws", Provider: "aws"}, nil
	}

	handler := &Handler{config: mockStore, auth: mockAuth}
	body := `{"name": "New Plan", "enabled": true, "provider": "aws", "service": "rds", "target_accounts": ["` + targetAccountID + `"]}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    body,
	}
	result, err := handler.createPlan(ctx, req)
	assert.Nil(t, result)
	require.Error(t, err)
	// Original error from SetPlanAccounts is what reaches the caller —
	// wrapped as "accounts: …" per the handler. The rollback error is logged
	// (not returned), so it must NOT appear in the user-facing error chain.
	assert.Contains(t, err.Error(), "accounts:")
	assert.True(t, errors.Is(err, setAccountsErr), "expected wrapped SetPlanAccounts error, got %v", err)
	assert.NotContains(t, err.Error(), "rollback delete boom", "rollback error must not leak into user-facing error")
	mockStore.AssertCalled(t, "DeletePurchasePlan", ctx, mock.AnythingOfType("string"))
}

// TestHandler_createPlan_RejectsInvalidTargetAccountUUID verifies that a
// malformed UUID in target_accounts is rejected before any DB write — same
// validation contract as PUT /plans/:id/accounts (handler_accounts.go).
func TestHandler_createPlan_RejectsInvalidTargetAccountUUID(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}
	body := `{"name": "P", "provider": "aws", "service": "rds", "target_accounts": ["not-a-uuid"]}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    body,
	}
	result, err := handler.createPlan(ctx, req)
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 400, ce.code)
	mockStore.AssertNotCalled(t, "CreatePurchasePlan", mock.Anything, mock.Anything)
}

func TestHandler_getPlan(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	plan := &config.PurchasePlan{
		ID:      "12345678-1234-1234-1234-123456789abc",
		Name:    "Test Plan",
		Enabled: true,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPurchasePlan", ctx, "12345678-1234-1234-1234-123456789abc").Return(plan, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.getPlan(ctx, req, "12345678-1234-1234-1234-123456789abc")
	require.NoError(t, err)

	resultPlan := result.(*config.PurchasePlan)
	assert.Equal(t, "12345678-1234-1234-1234-123456789abc", resultPlan.ID)
}

func TestHandler_updatePlan(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	existingPlan := &config.PurchasePlan{
		ID:      "12345678-1234-1234-1234-123456789abc",
		Name:    "Old Plan",
		Enabled: true,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPurchasePlan", ctx, "12345678-1234-1234-1234-123456789abc").Return(existingPlan, nil)
	mockStore.On("UpdatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"name": "Updated Plan", "enabled": false}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updatePlan(ctx, req, "12345678-1234-1234-1234-123456789abc")
	require.NoError(t, err)

	plan := result.(*config.PurchasePlan)
	assert.Equal(t, "12345678-1234-1234-1234-123456789abc", plan.ID)
	assert.Equal(t, "Updated Plan", plan.Name)
}

func TestHandler_deletePlan(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("DeletePurchasePlan", ctx, "12345678-1234-1234-1234-123456789abc").Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.deletePlan(ctx, req, "12345678-1234-1234-1234-123456789abc")
	require.NoError(t, err)

	resultMap := result.(map[string]string)
	assert.Equal(t, "deleted", resultMap["status"])
}

func TestHandler_updatePlan_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{corsAllowedOrigin: "*", auth: mockAuth}

	body := `{invalid}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updatePlan(ctx, req, "12345678-1234-1234-1234-123456789abc")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

// MockAuthService is a mock implementation of AuthServiceInterface

func TestHandler_createPlannedPurchases(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	plan := &config.PurchasePlan{
		ID:   "11111111-1111-1111-1111-111111111111",
		Name: "Test Plan",
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 7,
			CurrentStep:      0,
		},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPurchasePlan", ctx, "11111111-1111-1111-1111-111111111111").Return(plan, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil).Times(3)
	mockStore.On("UpdatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"count": 3, "start_date": "2024-12-01"}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.createPlannedPurchases(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	assert.Equal(t, 3, result.Created)
}

// TestHandler_createPlannedPurchases_MidLoopFailureRollsBack verifies
// the partial-failure regression CodeRabbit flagged: a save failure on
// row N must NOT leave rows 1..N-1 persisted (they would be retried as
// duplicates) and must NOT bump the plan's next_execution_date (which
// would otherwise be ahead of the actually-committed rows). The fix
// wraps the loop + plan update in a single WithTx; this test asserts
// the rollback semantics by making the 3rd SavePurchaseExecutionTx
// call fail and confirming UpdatePurchasePlan was never reached.
func TestHandler_createPlannedPurchases_MidLoopFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	plan := &config.PurchasePlan{
		ID:   "11111111-1111-1111-1111-111111111111",
		Name: "Test Plan",
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 7,
			CurrentStep:      0,
		},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPurchasePlan", ctx, "11111111-1111-1111-1111-111111111111").Return(plan, nil)

	// Explicit WithTx expectation: this is the regression CR flagged. The
	// fallback in MockConfigStore.WithTx invokes fn(nil) regardless of
	// whether the handler uses WithTx, so a future refactor that drops
	// the transaction boundary would still pass without this assertion.
	// By registering the expectation we guarantee createPlannedPurchases
	// MUST run the loop inside a WithTx; if it ever stops doing so the
	// test fails on AssertExpectations below.
	//
	// We mirror the production WithTx contract: invoke the callback so
	// the inner SavePurchaseExecutionTx expectations actually fire, then
	// capture its error in withTxFnErr for the assertion below. The mock
	// itself returns a sentinel error (the inner-loop error is asserted
	// directly via withTxFnErr instead of through the handler's
	// returned err — both cover the regression).
	withTxCalled := false
	var withTxFnErr error
	mockStore.On("WithTx", ctx, mock.AnythingOfType("func(pgx.Tx) error")).
		Run(func(args mock.Arguments) {
			withTxCalled = true
			fn := args.Get(1).(func(pgx.Tx) error)
			withTxFnErr = fn(nil)
		}).
		Return(errors.New("simulated transient DB error")).
		Once()

	// Drive the SavePurchaseExecutionTx mock branch directly (registered
	// expectation overrides the un-tx fallback). Two successes, then a
	// hard failure on the third row — simulates a transient DB blip in
	// the middle of the loop.
	saveCall := 0
	mockStore.On("SavePurchaseExecutionTx", ctx, mock.Anything, mock.AnythingOfType("*config.PurchaseExecution")).
		Return(nil).
		Run(func(_ mock.Arguments) { saveCall++ }).
		Times(2)
	mockStore.On("SavePurchaseExecutionTx", ctx, mock.Anything, mock.AnythingOfType("*config.PurchaseExecution")).
		Return(errors.New("simulated transient DB error")).
		Once()

	// Critical assertion: UpdatePurchasePlan / UpdatePurchasePlanTx
	// must NOT be called when the inner loop bails. The mock framework
	// would otherwise raise an unexpected-call error if either fired.
	// We don't register expectations for them.

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"count": 5, "start_date": "2024-12-01"}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.createPlannedPurchases(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.Error(t, err)
	assert.Nil(t, result)
	// The handler propagates whatever WithTx returned; the regression we
	// care about is the inner-loop wrapping, asserted via withTxFnErr.
	require.Error(t, withTxFnErr, "inner WithTx callback must surface the save failure")
	assert.Contains(t, withTxFnErr.Error(), "failed to save execution")

	mockStore.AssertExpectations(t)
	// Belt-and-braces: explicitly assert the plan update was never
	// attempted. Both the un-tx and Tx variants are guarded.
	mockStore.AssertNotCalled(t, "UpdatePurchasePlan")
	mockStore.AssertNotCalled(t, "UpdatePurchasePlanTx")
	// And confirm the loop ran inside a transaction — see the WithTx
	// expectation above for why this matters.
	require.True(t, withTxCalled, "createPlannedPurchases must run save loop inside WithTx")
}

func TestHandler_createPlannedPurchases_InvalidCount(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{auth: mockAuth}

	body := `{"count": 100, "start_date": "2024-12-01"}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.createPlannedPurchases(ctx, req, "11111111-1111-1111-1111-111111111111")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "count must be between 1 and 52")
}

func TestHandler_createPlannedPurchases_InvalidDate(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{auth: mockAuth}

	body := `{"count": 3, "start_date": "invalid-date"}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.createPlannedPurchases(ctx, req, "11111111-1111-1111-1111-111111111111")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid start_date format")
}

// Profile endpoint tests

func TestHandler_createPlannedPurchases_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{auth: mockAuth}

	body := `{invalid json}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.createPlannedPurchases(ctx, req, "11111111-1111-1111-1111-111111111111")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_createPlannedPurchases_PlanNotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPurchasePlan", ctx, "99999999-9999-9999-9999-999999999999").Return(nil, errors.New("plan not found"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"count": 2, "start_date": "2024-12-01"}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.createPlannedPurchases(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to get plan")
}

func TestCalculateNextExecutionDate(t *testing.T) {
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		plan     *config.PurchasePlan
		expected time.Time
	}{
		{
			name: "immediate type",
			plan: &config.PurchasePlan{
				RampSchedule: config.RampSchedule{
					Type: "immediate",
				},
			},
			expected: now.AddDate(0, 0, 1),
		},
		{
			name: "with step interval",
			plan: &config.PurchasePlan{
				RampSchedule: config.RampSchedule{
					Type:             "weekly",
					StepIntervalDays: 7,
				},
			},
			expected: now.AddDate(0, 0, 7),
		},
		{
			name: "default to tomorrow",
			plan: &config.PurchasePlan{
				RampSchedule: config.RampSchedule{
					Type:             "custom",
					StepIntervalDays: 0,
				},
			},
			expected: now.AddDate(0, 0, 1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateNextExecutionDate(tt.plan, now)
			assert.NotNil(t, result)
			assert.Equal(t, tt.expected, *result)
		})
	}
}

// Tests for patchPlan

func TestHandler_patchPlan_Success(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	existingPlan := &config.PurchasePlan{
		ID:                     "11111111-1111-1111-1111-111111111111",
		Name:                   "Original Name",
		Enabled:                false,
		AutoPurchase:           false,
		NotificationDaysBefore: 3,
		CreatedAt:              time.Now().AddDate(0, -1, 0),
		Services: map[string]config.ServiceConfig{
			"aws/rds": {Provider: "aws", Service: "rds"},
		},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPurchasePlan", ctx, "11111111-1111-1111-1111-111111111111").Return(existingPlan, nil)
	mockStore.On("UpdatePurchasePlan", ctx, mock.MatchedBy(func(p *config.PurchasePlan) bool {
		return p.Enabled == true && p.Name == "Original Name"
	})).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"enabled": true}`,
	}
	result, err := handler.patchPlan(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	plan := result.(*config.PurchasePlan)
	assert.Equal(t, true, plan.Enabled)
	assert.Equal(t, "Original Name", plan.Name)
}

func TestHandler_patchPlan_UpdateMultipleFields(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	existingPlan := &config.PurchasePlan{
		ID:                     "11111111-1111-1111-1111-111111111111",
		Name:                   "Original Name",
		Enabled:                false,
		AutoPurchase:           false,
		NotificationDaysBefore: 3,
		Services: map[string]config.ServiceConfig{
			"aws/rds": {Provider: "aws", Service: "rds"},
		},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPurchasePlan", ctx, "11111111-1111-1111-1111-111111111111").Return(existingPlan, nil)
	mockStore.On("UpdatePurchasePlan", ctx, mock.MatchedBy(func(p *config.PurchasePlan) bool {
		return p.Enabled == true && p.Name == "New Name" && p.AutoPurchase == true && p.NotificationDaysBefore == 5
	})).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"enabled": true, "name": "New Name", "auto_purchase": true, "notification_days_before": 5}`,
	}
	result, err := handler.patchPlan(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	plan := result.(*config.PurchasePlan)
	assert.Equal(t, true, plan.Enabled)
	assert.Equal(t, "New Name", plan.Name)
	assert.Equal(t, true, plan.AutoPurchase)
	assert.Equal(t, 5, plan.NotificationDaysBefore)
}

func TestHandler_patchPlan_InvalidUUID(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"enabled": true}`,
	}
	result, err := handler.patchPlan(ctx, req, "invalid-uuid")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid ID format")
}

func TestHandler_patchPlan_InvalidBody(t *testing.T) {
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
	result, err := handler.patchPlan(ctx, req, "11111111-1111-1111-1111-111111111111")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_patchPlan_NotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPurchasePlan", ctx, "99999999-9999-9999-9999-999999999999").Return(nil, errors.New("not found"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"enabled": true}`,
	}
	result, err := handler.patchPlan(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to get plan")
}

func TestHandler_patchPlan_NilPlan(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPurchasePlan", ctx, "99999999-9999-9999-9999-999999999999").Return(nil, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"enabled": true}`,
	}
	result, err := handler.patchPlan(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "plan not found")
}

func TestHandler_patchPlan_EmptyName(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	existingPlan := &config.PurchasePlan{
		ID:      "11111111-1111-1111-1111-111111111111",
		Name:    "Test Plan",
		Enabled: false,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPurchasePlan", ctx, "11111111-1111-1111-1111-111111111111").Return(existingPlan, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"name": ""}`,
	}
	result, err := handler.patchPlan(ctx, req, "11111111-1111-1111-1111-111111111111")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "cannot be empty")
}

func TestHandler_patchPlan_InvalidNotificationDays(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	existingPlan := &config.PurchasePlan{
		ID:      "11111111-1111-1111-1111-111111111111",
		Name:    "Test Plan",
		Enabled: false,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPurchasePlan", ctx, "11111111-1111-1111-1111-111111111111").Return(existingPlan, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"notification_days_before": 50}`,
	}
	result, err := handler.patchPlan(ctx, req, "11111111-1111-1111-1111-111111111111")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "must be between 0 and 30")
}

func TestHandler_patchPlan_NegativeNotificationDays(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	existingPlan := &config.PurchasePlan{
		ID:      "11111111-1111-1111-1111-111111111111",
		Name:    "Test Plan",
		Enabled: false,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPurchasePlan", ctx, "11111111-1111-1111-1111-111111111111").Return(existingPlan, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"notification_days_before": -5}`,
	}
	result, err := handler.patchPlan(ctx, req, "11111111-1111-1111-1111-111111111111")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "must be between 0 and 30")
}

func TestHandler_patchPlan_UpdateError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	existingPlan := &config.PurchasePlan{
		ID:      "11111111-1111-1111-1111-111111111111",
		Name:    "Test Plan",
		Enabled: false,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetPurchasePlan", ctx, "11111111-1111-1111-1111-111111111111").Return(existingPlan, nil)
	mockStore.On("UpdatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(errors.New("database error"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: `{"enabled": true}`,
	}
	result, err := handler.patchPlan(ctx, req, "11111111-1111-1111-1111-111111111111")
	assert.Error(t, err)
	assert.Nil(t, result)
}
