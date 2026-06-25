package apihttp

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
	}

	plans := []config.PurchasePlan{
		{ID: "11111111-1111-1111-1111-111111111111", Name: "Test Plan 1", Enabled: true},
		{ID: "22222222-2222-2222-2222-222222222222", Name: "Test Plan 2", Enabled: false},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	}

	plans := []config.PurchasePlan{
		{ID: "11111111-1111-1111-1111-111111111111", Name: "Account Plan", Enabled: true},
	}

	accountID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	expectedFilter := config.PurchasePlanFilter{AccountIDs: []string{accountID}}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	}

	targetAccountID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

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
	}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

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
// rollback DeletePurchasePlan also fails, the caller still receives a 500
// ClientError (not the raw DB error) and the rollback error does not leak.
// The rollback failure is logged at WARN; the SetPlanAccounts error is logged
// at ERROR -- see handler_plans.go createPlan rollbackPlan closure.
func TestHandler_createPlan_RollbackDeleteFailureSurfacesOriginalError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}
	targetAccountID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	// Handler returns a 500 ClientError with a safe generic message; raw DB
	// errors are logged, not returned. Rollback error also must not leak.
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 500, ce.code)
	assert.NotContains(t, ce.Error(), "setplanaccounts boom", "DB error must not leak to caller")
	assert.NotContains(t, ce.Error(), "rollback delete boom", "rollback error must not leak to caller")
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
	}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

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
	}

	plan := &config.PurchasePlan{
		ID:      "12345678-1234-1234-1234-123456789abc",
		Name:    "Test Plan",
		Enabled: true,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	}

	existingPlan := &config.PurchasePlan{
		ID:      "12345678-1234-1234-1234-123456789abc",
		Name:    "Old Plan",
		Enabled: true,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

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
	mockAuth.grantAdmin()
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

// TestHandler_createPlannedPurchases_StampsCreator is the issue-#950 regression
// guard: every execution row written through POST /api/plans/{id}/purchases
// MUST carry the session user's UUID in CreatedByUserID, otherwise the
// per-row ownership gate (authorizeExecutionManagement in
// handler_purchases.go) downstream cannot recognise the actor as the
// rightful manager and the user who just scheduled the purchases is
// locked out of pause / resume / run / delete until an admin steps in.
//
// Pre-fix the field shipped zero-valued (nil pointer), making every
// freshly scheduled row look like a legacy unattributed entry.
func TestHandler_createPlannedPurchases_StampsCreator(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	const userID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	userSession := &Session{UserID: userID, Email: "u@example.com"}

	plan := &config.PurchasePlan{
		ID:   "11111111-1111-1111-1111-111111111111",
		Name: "Test Plan",
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 7,
			CurrentStep:      0,
		},
	}

	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetPurchasePlan", ctx, "11111111-1111-1111-1111-111111111111").Return(plan, nil)

	// Capture every saved execution's CreatedByUserID so we can assert
	// the field is stamped on each row in the batch (not just the first).
	var savedCreators []*string
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).
		Run(func(args mock.Arguments) {
			exec := args.Get(1).(*config.PurchaseExecution)
			savedCreators = append(savedCreators, exec.CreatedByUserID)
		}).
		Return(nil).Times(3)
	mockStore.On("UpdatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"count": 3, "start_date": "2024-12-01"}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer user-token"},
		Body:    body,
	}
	result, err := handler.createPlannedPurchases(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)
	assert.Equal(t, 3, result.Created)

	require.Len(t, savedCreators, 3, "expected 3 saved executions")
	for i, c := range savedCreators {
		require.NotNil(t, c, "execution %d shipped a nil CreatedByUserID (issue #950 regression)", i)
		assert.Equal(t, userID, *c, "execution %d shipped the wrong CreatedByUserID", i)
	}
}

// TestHandler_createPlannedPurchases_AdminAPIKeyCreatorIsNil locks in that
// the stateless admin-API-key path (UserID == apiKeyAdminUserID, not a UUID)
// stamps NULL rather than the literal sentinel. resolveCreatorUserID rejects
// non-UUID UserIDs so the FK to users stays valid; the row falls through to
// the admin / update-any management path exactly like a legacy scheduler-
// created row would.
func TestHandler_createPlannedPurchases_AdminAPIKeyCreatorIsNil(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	apiKeySession := &Session{UserID: apiKeyAdminUserID, Email: "admin-api-key"}

	plan := &config.PurchasePlan{
		ID:   "11111111-1111-1111-1111-111111111111",
		Name: "Test Plan",
		RampSchedule: config.RampSchedule{
			StepIntervalDays: 7,
			CurrentStep:      0,
		},
	}

	mockAuth.On("ValidateSession", ctx, "api-key").Return(apiKeySession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetPurchasePlan", ctx, "11111111-1111-1111-1111-111111111111").Return(plan, nil)

	var savedCreators []*string
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).
		Run(func(args mock.Arguments) {
			exec := args.Get(1).(*config.PurchaseExecution)
			savedCreators = append(savedCreators, exec.CreatedByUserID)
		}).
		Return(nil).Times(2)
	mockStore.On("UpdatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"count": 2, "start_date": "2024-12-01"}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer api-key"},
		Body:    body,
	}
	_, err := handler.createPlannedPurchases(ctx, req, "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)

	require.Len(t, savedCreators, 2)
	for i, c := range savedCreators {
		assert.Nil(t, c, "execution %d should ship a nil CreatedByUserID for the admin-API-key path", i)
	}
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
	mockAuth.grantAdmin()
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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	// Post-#965-followup: getPlanForPurchaseCreation routes DB errors through
	// mapCreatePlanStorageError, returning a generic 500 ClientError without
	// the raw plan UUID or the raw DB error string in the user-facing message.
	ce, ok := IsClientError(err)
	require.True(t, ok, "DB error must surface as a ClientError, not propagate raw to the router log")
	assert.Equal(t, 500, ce.code)
	assert.NotContains(t, ce.Error(), "99999999-9999-9999-9999-999999999999", "500 message must not echo the plan UUID")
	assert.NotContains(t, ce.Error(), "plan not found", "500 message must not be the 404 branch (it would mask a real DB outage)")
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
	mockAuth.grantAdmin()
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
	mockAuth.grantAdmin()
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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	// Post-#965-followup: patchPlan routes generic DB errors through
	// mapCreatePlanStorageError, returning a generic 500 ClientError without
	// the raw plan UUID or the raw DB error string in the user-facing message.
	ce, ok := IsClientError(err)
	require.True(t, ok, "DB error must surface as a ClientError, not propagate raw to the router log")
	assert.Equal(t, 500, ce.code)
	assert.NotContains(t, ce.Error(), "99999999-9999-9999-9999-999999999999", "500 message must not echo the plan UUID")
}

// TestHandler_patchPlan_GetPlanDBError_NoPIILeak is a regression test for the
// PII-leak sibling of issue #965: the GetPurchasePlan DB-error path in patchPlan
// previously wrapped the raw error with the raw plan UUID
// (`fmt.Errorf("failed to get plan %s: %w", planID, err)`), and that non-
// ClientError propagated to the router's logging.Errorf("API error: %v"),
// leaking both the UUID and the raw DB error string. Asserts that on a DB
// error: (1) a 500 ClientError is returned, (2) neither the returned message
// nor the captured default-log line contains the plan UUID or the raw DB
// error string.
func TestHandler_patchPlan_GetPlanDBError_NoPIILeak(t *testing.T) {
	ctx := context.Background()

	const (
		planUUID = "99999999-9999-9999-9999-999999999999"
		rawDBErr = "pq: SSL connection has been closed unexpectedly by host db-plans-99.example.com"
	)

	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetPurchasePlan", ctx, planUUID).Return(nil, errors.New(rawDBErr))

	handler := &Handler{config: mockStore, auth: mockAuth}

	logBuf := captureDefaultLog(t)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"enabled": true}`,
	}
	result, err := handler.patchPlan(ctx, req, planUUID)
	require.Error(t, err)
	assert.Nil(t, result)

	ce, ok := IsClientError(err)
	require.True(t, ok, "DB error must surface as a ClientError, not propagate raw to the router log")
	assert.Equal(t, 500, ce.code, "a DB error fetching the plan is a server-side fault")
	assert.NotContains(t, ce.Error(), planUUID, "500 message must not echo the plan UUID")
	assert.NotContains(t, ce.Error(), rawDBErr, "500 message must not leak the raw DB error string")

	logged := logBuf.String()
	assert.NotContains(t, logged, planUUID, "log must not contain the raw plan UUID (issue #965 sibling)")
	assert.NotContains(t, logged, rawDBErr, "log must not contain the raw DB error string (issue #965 sibling)")
}

// TestHandler_updatePlan_GetPlanDBError_NoPIILeak guards the same shape on the
// PUT updatePlan path (handler_plans.go:209): the existingPlan-fetch DB error
// path used to wrap with the raw plan UUID.
func TestHandler_updatePlan_GetPlanDBError_NoPIILeak(t *testing.T) {
	ctx := context.Background()

	const (
		planUUID = "99999999-9999-9999-9999-999999999999"
		rawDBErr = "pq: connection refused to host db-internal-99.example.com"
	)

	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetPurchasePlan", ctx, planUUID).Return(nil, errors.New(rawDBErr))

	handler := &Handler{config: mockStore, auth: mockAuth}

	logBuf := captureDefaultLog(t)

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		// Minimal valid body; updatePlan validates fields after the fetch.
		Body: `{"name":"x","provider":"aws","service":"ec2"}`,
	}
	result, err := handler.updatePlan(ctx, req, planUUID)
	require.Error(t, err)
	assert.Nil(t, result)

	ce, ok := IsClientError(err)
	require.True(t, ok, "DB error must surface as a ClientError, not propagate raw to the router log")
	assert.Equal(t, 500, ce.code)
	assert.NotContains(t, ce.Error(), planUUID, "500 message must not echo the plan UUID")
	assert.NotContains(t, ce.Error(), rawDBErr, "500 message must not leak the raw DB error string")

	logged := logBuf.String()
	assert.NotContains(t, logged, planUUID)
	assert.NotContains(t, logged, rawDBErr)
}

func TestHandler_patchPlan_NilPlan(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	}

	existingPlan := &config.PurchasePlan{
		ID:      "11111111-1111-1111-1111-111111111111",
		Name:    "Test Plan",
		Enabled: false,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	}

	existingPlan := &config.PurchasePlan{
		ID:      "11111111-1111-1111-1111-111111111111",
		Name:    "Test Plan",
		Enabled: false,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	}

	existingPlan := &config.PurchasePlan{
		ID:      "11111111-1111-1111-1111-111111111111",
		Name:    "Test Plan",
		Enabled: false,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	}

	existingPlan := &config.PurchasePlan{
		ID:      "11111111-1111-1111-1111-111111111111",
		Name:    "Test Plan",
		Enabled: false,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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

// TestHandler_createPlan_UnknownAccountReturns404 is the regression guard for
// the 500/404 inconsistency: createPlan with a target_accounts entry whose
// UUID is not present in the store must return 404, not 500.
func TestHandler_createPlan_UnknownAccountReturns404(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}
	unknownAccountID := "cccccccc-cccc-cccc-cccc-cccccccccccc"

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("CreatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)
	// Rollback is triggered when validatePlanAccountProviders returns an error.
	mockStore.On("DeletePurchasePlan", ctx, mock.AnythingOfType("string")).Return(nil)
	// GetPurchasePlan is called by getPlanForAccountProviderValidation inside
	// validatePlanAccountProviders. Return a plan with services so
	// DerivePlanProviders produces a non-empty expected set and the
	// provider-match check actually runs (empty services => check skipped).
	mockStore.GetPurchasePlanFn = func(_ context.Context, _ string) (*config.PurchasePlan, error) {
		return &config.PurchasePlan{
			Services: map[string]config.ServiceConfig{
				"aws/rds": {Provider: "aws", Service: "rds"},
			},
		}, nil
	}
	// GetCloudAccount returns nil (not found) to simulate the missing account.
	mockStore.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return nil, nil // nil account, nil error => "not found" branch
	}

	handler := &Handler{config: mockStore, auth: mockAuth}
	body := `{"name": "P", "provider": "aws", "service": "rds", "target_accounts": ["` + unknownAccountID + `"]}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    body,
	}
	result, err := handler.createPlan(ctx, req)
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError (not opaque 500), got %T: %v", err, err)
	assert.Equal(t, 404, ce.code, "unknown account must return 404, not 500")
}

// TestHandler_createPlan_DBErrorOnCreateReturns500WithLog verifies that a DB
// failure on CreatePurchasePlan returns a well-formed 500 ClientError instead
// of a raw unwrapped error (which would look the same but is less explicit).
func TestHandler_createPlan_DBErrorOnCreateReturns500WithLog(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}
	targetAccountID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	dbErr := errors.New("connection refused")
	mockStore.On("CreatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(dbErr)

	handler := &Handler{config: mockStore, auth: mockAuth}
	body := `{"name": "P", "provider": "aws", "service": "rds", "target_accounts": ["` + targetAccountID + `"]}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    body,
	}
	result, err := handler.createPlan(ctx, req)
	assert.Nil(t, result)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "DB error must be wrapped as ClientError, got %T: %v", err, err)
	assert.Equal(t, 500, ce.code)
	// Raw DB error must NOT be exposed to the caller.
	assert.NotContains(t, ce.Error(), "connection refused", "internal DB error must not leak to caller")
}
