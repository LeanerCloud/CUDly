package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// expectStoredRecs registers the ListStoredRecommendations expectation the
// pricing lookup issues: one call per distinct provider, filtered by provider
// only. Registering the exact filter pins that contract.
func expectStoredRecs(store *MockConfigStore, recs ...config.RecommendationRecord) {
	byProvider := map[string][]config.RecommendationRecord{}
	for _, r := range recs {
		byProvider[r.Provider] = append(byProvider[r.Provider], r)
	}
	for provider, rows := range byProvider {
		store.On("ListStoredRecommendations", mock.Anything, config.RecommendationFilter{Provider: provider}).Return(rows, nil)
	}
}

// TestHandler_executePurchase_CapUsesStoredPriceNotClientPrice is the issue's
// reproduction (audit A01-001): a purchaser holding execute:purchases with a
// $1,000 MaxPurchaseAmount cap submits upfront_cost: 1 for 100 x m5.24xlarge
// x 3yr all-upfront reservations whose real stored price is $1,000,000. The
// cap must be enforced against the stored price, not the client's number.
func TestHandler_executePurchase_CapUsesStoredPriceNotClientPrice(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	mockPurchase := new(MockPurchaseManager)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	t.Cleanup(func() { mockPurchase.AssertExpectations(t) })

	session := &Session{UserID: "dddddddd-dddd-dddd-dddd-dddddddddddd", Email: "capped@example.com"}
	mockAuth.On("ValidateSession", ctx, "cap-token").Return(session, nil)
	mockAuth.grantPermissions([]auth.Permission{
		{Action: auth.ActionExecute, Resource: auth.ResourcePurchases, Constraints: &auth.PermissionConstraints{MaxPurchaseAmount: 1000}},
		{Action: auth.ActionExecuteAny, Resource: auth.ResourcePurchases},
	})

	expectStoredRecs(mockStore, config.RecommendationRecord{
		ID:       "aws|123456789012|ec2|us-east-1|m5.24xlarge||3|all-upfront",
		Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "m5.24xlarge",
		Count: 100, Term: 3, Payment: "all-upfront", UpfrontCost: 1_000_000, Savings: 40_000,
	})
	// Registered .Maybe(): pre-fix these are reached, post-fix they must not
	// be. Not requiring GetGlobalConfig/GetPendingExecutions here because the
	// mock's isExpected guard on GetGlobalConfig defaults gracefully, and
	// this test is not asserting on the persisted row's suppression window;
	// GetPendingExecutions has no such guard, so it must be stubbed to avoid
	// a panic on the pre-fix run, which does reach persistExecutionAndSuppressions.
	mockStore.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil).Maybe()
	mockStore.On("GetPendingExecutions", mock.Anything).Return([]config.PurchaseExecution{}, nil).Maybe()
	mockStore.On("SavePurchaseExecution", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockPurchase.On("ApproveAndExecute", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	handler := &Handler{config: mockStore, auth: mockAuth, purchase: mockPurchase}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer cap-token"},
		Body:    `{"recommendations":[{"id":"aws|123456789012|ec2|us-east-1|m5.24xlarge||3|all-upfront","provider":"aws","service":"ec2","region":"us-east-1","resource_type":"m5.24xlarge","count":100,"term":3,"payment":"all-upfront","upfront_cost":1,"monthly_cost":null,"savings":40000,"selected":true}],"execute_mode":"direct"}`,
	}
	_, err := handler.executePurchase(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError, got: %v", err)
	assert.Equal(t, 403, ce.code)
	assert.Contains(t, ce.Error(), "constraints")
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
	mockPurchase.AssertNotCalled(t, "ApproveAndExecute", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestHandler_executePurchase_StoredPriceUnderCapProceeds is the no-over-
// rejection companion to T1: the client overstates upfront_cost far beyond
// the cap, but the STORED price is well under it, so the purchase must
// proceed and the response must reflect the stored numbers.
func TestHandler_executePurchase_StoredPriceUnderCapProceeds(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	mockPurchase := new(MockPurchaseManager)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	t.Cleanup(func() { mockPurchase.AssertExpectations(t) })

	session := &Session{UserID: "dddddddd-dddd-dddd-dddd-dddddddddddd", Email: "capped@example.com"}
	mockAuth.On("ValidateSession", ctx, "cap-token").Return(session, nil)
	mockAuth.grantPermissions([]auth.Permission{
		{Action: auth.ActionExecute, Resource: auth.ResourcePurchases, Constraints: &auth.PermissionConstraints{MaxPurchaseAmount: 1000}},
		{Action: auth.ActionExecuteAny, Resource: auth.ResourcePurchases},
	})

	expectStoredRecs(mockStore, config.RecommendationRecord{
		ID:       "aws|123456789012|ec2|us-east-1|m5.24xlarge||3|all-upfront",
		Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "m5.24xlarge",
		Count: 1, Term: 3, Payment: "all-upfront", UpfrontCost: 900, Savings: 30,
	})
	mockStore.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil)
	mockStore.On("GetPendingExecutions", mock.Anything).Return([]config.PurchaseExecution{}, nil)
	mockStore.On("SavePurchaseExecution", mock.Anything, mock.Anything).Return(nil)
	mockPurchase.On("ApproveAndExecute", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth, purchase: mockPurchase}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer cap-token"},
		Body:    `{"recommendations":[{"id":"aws|123456789012|ec2|us-east-1|m5.24xlarge||3|all-upfront","provider":"aws","service":"ec2","region":"us-east-1","resource_type":"m5.24xlarge","count":1,"term":3,"payment":"all-upfront","upfront_cost":5000,"savings":30,"selected":true}],"execute_mode":"direct"}`,
	}
	result, err := handler.executePurchase(ctx, req)
	require.NoError(t, err)
	resultMap := result.(map[string]any)
	assert.Equal(t, "completed", resultMap["status"])
	assert.Equal(t, 900.0, resultMap["total_upfront_cost"])
	assert.Equal(t, 30.0, resultMap["estimated_savings"])
}

// TestHandler_executePurchase_PersistsStoredCostsNotClientCosts pins the
// second half of #1905: the execution row and the approval email must carry
// the store-derived costs, not the client's.
func TestHandler_executePurchase_PersistsStoredCostsNotClientCosts(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Email: "admin@example.com"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdminPurchaser()

	notify := "notify@example.com"
	mockStore.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{NotificationEmail: &notify}, nil)
	mockStore.On("GetPendingExecutions", mock.Anything).Return([]config.PurchaseExecution{}, nil)

	var saved *config.PurchaseExecution
	mockStore.On("SavePurchaseExecution", mock.Anything, mock.AnythingOfType("*config.PurchaseExecution")).
		Run(func(args mock.Arguments) { saved = args.Get(1).(*config.PurchaseExecution) }).
		Return(nil)

	expectStoredRecs(mockStore, config.RecommendationRecord{
		ID: "stored-id", Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "m5.24xlarge",
		Count: 4, Term: 3, Payment: "all-upfront",
		UpfrontCost: 4000, MonthlyCost: float64Ptr(40), Savings: 400, OnDemandCost: float64Ptr(1000),
		Details: json.RawMessage(`{"platform":"Linux/UNIX"}`),
	})

	notifier := &recordingEmailNotifier{}
	handler := &Handler{config: mockStore, auth: mockAuth, emailNotifier: notifier}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"recommendations":[{"id":"client-id","provider":"aws","service":"ec2","region":"us-east-1","resource_type":"m5.24xlarge","count":2,"term":3,"payment":"all-upfront","recommended_count":4,"upfront_cost":1,"monthly_cost":1,"savings":999,"on_demand_cost":1,"details":{"platform":"Windows"},"selected":true}],"capacity_percent":50}`,
	}
	result, err := handler.executePurchase(ctx, req)
	require.NoError(t, err)

	require.NotNil(t, saved)
	require.Len(t, saved.Recommendations, 1)
	rec := saved.Recommendations[0]
	assert.Equal(t, "stored-id", rec.ID)
	assert.Equal(t, 2, rec.Count)
	assert.Equal(t, 4, rec.RecommendedCount)
	assert.Equal(t, 2000.0, rec.UpfrontCost)
	require.NotNil(t, rec.MonthlyCost)
	assert.Equal(t, 20.0, *rec.MonthlyCost)
	assert.Equal(t, 200.0, rec.Savings)
	require.NotNil(t, rec.OnDemandCost)
	assert.Equal(t, 500.0, *rec.OnDemandCost)
	assert.Equal(t, `{"platform":"Linux/UNIX"}`, string(rec.Details))
	assert.True(t, rec.Selected)

	assert.Equal(t, 2000.0, saved.TotalUpfrontCost)
	assert.Equal(t, 200.0, saved.EstimatedSavings)

	assert.Equal(t, 2000.0, notifier.captured.TotalUpfrontCost)
	assert.Equal(t, 200.0, notifier.captured.TotalSavings)

	resultMap := result.(map[string]any)
	assert.Equal(t, 2000.0, resultMap["total_upfront_cost"])
	assert.Equal(t, 200.0, resultMap["estimated_savings"])
	assert.Equal(t, true, resultMap["email_sent"])
}

// TestHandler_executePurchase_UnknownRecommendationRefused: a rec that
// matches no stored recommendation is refused with 409, before anything is
// persisted.
func TestHandler_executePurchase_UnknownRecommendationRefused(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Email: "admin@example.com"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdminPurchaser()

	// Registered .Maybe(): grantAdminPurchaser carries no MaxPurchaseAmount
	// cap, so pre-fix the (unpriced) constraint check trivially passes and
	// the flow reaches persistExecutionAndSuppressions, which needs these to
	// avoid a panic on the unstubbed mock; post-fix they are never reached.
	mockStore.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil).Maybe()
	mockStore.On("GetPendingExecutions", mock.Anything).Return([]config.PurchaseExecution{}, nil).Maybe()
	mockStore.On("SavePurchaseExecution", mock.Anything, mock.Anything).Return(nil).Maybe()
	expectStoredRecs(mockStore, config.RecommendationRecord{
		Provider: "aws", Service: "ec2", ResourceType: "m5.large", Count: 1, Term: 1, Payment: "all-upfront", UpfrontCost: 100,
	})

	handler := &Handler{config: mockStore, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"recommendations":[{"id":"rec-1","provider":"aws","service":"ec2","resource_type":"m5.xlarge","count":1,"term":1,"payment":"all-upfront","upfront_cost":100,"savings":10}]}`,
	}
	_, err := handler.executePurchase(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError, got: %v", err)
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, ce.Error(), "not in the current recommendation set")
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

// TestHandler_executePurchase_CrossAccountMismatchRefused is a handler-level
// companion to TestRecIdentityKey_TupleSemantics' "a different account
// yields a different key" subtest: that pure unit test proves recIdentityKey
// includes the account, but not that the scope check and the key agree end
// to end, through the real handler, on what "account" means (a mutation
// that dropped CloudAccountID from recIdentityKey would still pass an
// unrestricted admin session's scope check). Stored recommendations exist
// only under account A; the request claims the identical resource under
// account B. The mismatch must be refused with 409 before anything is
// persisted or the provider is contacted.
func TestHandler_executePurchase_CrossAccountMismatchRefused(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	mockPurchase := new(MockPurchaseManager)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	t.Cleanup(func() { mockPurchase.AssertExpectations(t) })

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Email: "admin@example.com"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdminPurchaser()

	// Registered .Maybe(): see TestHandler_executePurchase_UnknownRecommendationRefused.
	mockStore.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil).Maybe()
	mockStore.On("GetPendingExecutions", mock.Anything).Return([]config.PurchaseExecution{}, nil).Maybe()
	mockStore.On("SavePurchaseExecution", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockPurchase.On("ApproveAndExecute", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	accountA := "111111111111"
	expectStoredRecs(mockStore, config.RecommendationRecord{
		Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "m5.large",
		CloudAccountID: &accountA, Count: 1, Term: 1, Payment: "all-upfront", UpfrontCost: 100,
	})

	handler := &Handler{config: mockStore, auth: mockAuth, purchase: mockPurchase}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"recommendations":[{"id":"rec-1","provider":"aws","service":"ec2","region":"us-east-1","resource_type":"m5.large","cloud_account_id":"222222222222","count":1,"term":1,"payment":"all-upfront","upfront_cost":100,"savings":10}]}`,
	}
	_, err := handler.executePurchase(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError, got: %v", err)
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, ce.Error(), "not in the current recommendation set")
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
	mockPurchase.AssertNotCalled(t, "ApproveAndExecute", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestHandler_executePurchase_StoredRecWithoutPriceRefused: a matched stored
// row that carries no usable price is refused with 409.
func TestHandler_executePurchase_StoredRecWithoutPriceRefused(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Email: "admin@example.com"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdminPurchaser()

	// Registered .Maybe(): see TestHandler_executePurchase_UnknownRecommendationRefused.
	mockStore.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil).Maybe()
	mockStore.On("GetPendingExecutions", mock.Anything).Return([]config.PurchaseExecution{}, nil).Maybe()
	mockStore.On("SavePurchaseExecution", mock.Anything, mock.Anything).Return(nil).Maybe()
	expectStoredRecs(mockStore, config.RecommendationRecord{
		Provider: "aws", Service: "ec2", ResourceType: "m5.large", Count: 1, Term: 1, Payment: "all-upfront",
		UpfrontCost: 0, MonthlyCost: nil,
	})

	handler := &Handler{config: mockStore, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"recommendations":[{"id":"rec-1","provider":"aws","service":"ec2","resource_type":"m5.large","count":1,"term":1,"payment":"all-upfront","upfront_cost":100,"savings":10}]}`,
	}
	_, err := handler.executePurchase(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError, got: %v", err)
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, ce.Error(), "no usable price")
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

// TestHandler_executePurchase_StoredRecWithZeroCountRefused: a matched
// stored row with a non-positive Count cannot yield a per-unit price and
// must be refused with 409 rather than dividing by zero or by a negative
// count (section 4 of the #1905 plan).
func TestHandler_executePurchase_StoredRecWithZeroCountRefused(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Email: "admin@example.com"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdminPurchaser()

	// Registered .Maybe(): see TestHandler_executePurchase_UnknownRecommendationRefused.
	mockStore.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil).Maybe()
	mockStore.On("GetPendingExecutions", mock.Anything).Return([]config.PurchaseExecution{}, nil).Maybe()
	mockStore.On("SavePurchaseExecution", mock.Anything, mock.Anything).Return(nil).Maybe()
	expectStoredRecs(mockStore, config.RecommendationRecord{
		Provider: "aws", Service: "ec2", ResourceType: "m5.large", Count: 0, Term: 1, Payment: "all-upfront", UpfrontCost: 100,
	})

	handler := &Handler{config: mockStore, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"recommendations":[{"id":"rec-1","provider":"aws","service":"ec2","resource_type":"m5.large","count":1,"term":1,"payment":"all-upfront","upfront_cost":100,"savings":10}]}`,
	}
	_, err := handler.executePurchase(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError, got: %v", err)
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, ce.Error(), "cannot derive a per-unit price")
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

// TestHandler_executePurchase_StoreErrorFailsClosed: a store read error is a
// 500, never a client-value fallback.
func TestHandler_executePurchase_StoreErrorFailsClosed(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Email: "admin@example.com"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdminPurchaser()

	// Registered .Maybe(): see TestHandler_executePurchase_UnknownRecommendationRefused.
	mockStore.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil).Maybe()
	mockStore.On("GetPendingExecutions", mock.Anything).Return([]config.PurchaseExecution{}, nil).Maybe()
	mockStore.On("SavePurchaseExecution", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockStore.On("ListStoredRecommendations", mock.Anything, config.RecommendationFilter{Provider: "aws"}).
		Return(nil, errors.New("pg down"))

	handler := &Handler{config: mockStore, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"recommendations":[{"id":"rec-1","provider":"aws","service":"ec2","resource_type":"m5.large","count":1,"term":1,"payment":"all-upfront","upfront_cost":100,"savings":10}]}`,
	}
	_, err := handler.executePurchase(ctx, req)
	require.Error(t, err)
	_, isClient := IsClientError(err)
	assert.False(t, isClient, "a store read failure must not be reported as a client error")
	assert.ErrorContains(t, err, "load stored recommendations for aws")
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

// TestHandler_executePurchase_PaymentChangePricedFromStoredVariant: the
// purchase modal's term/payment change (#111, #197, #1903) resolves to the
// stored variant the user actually chose, not the id's original variant.
func TestHandler_executePurchase_PaymentChangePricedFromStoredVariant(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	session := &Session{UserID: "dddddddd-dddd-dddd-dddd-dddddddddddd", Email: "capped@example.com"}
	mockAuth.On("ValidateSession", ctx, "cap-token").Return(session, nil)
	mockAuth.grantPermissions([]auth.Permission{
		{Action: auth.ActionExecute, Resource: auth.ResourcePurchases, Constraints: &auth.PermissionConstraints{MaxPurchaseAmount: 1500}},
	})

	mockStore.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil)
	mockStore.On("GetPendingExecutions", mock.Anything).Return([]config.PurchaseExecution{}, nil)
	var saved *config.PurchaseExecution
	mockStore.On("SavePurchaseExecution", mock.Anything, mock.AnythingOfType("*config.PurchaseExecution")).
		Run(func(args mock.Arguments) { saved = args.Get(1).(*config.PurchaseExecution) }).
		Return(nil)

	expectStoredRecs(mockStore,
		config.RecommendationRecord{
			ID:       "aws|acct|ec2|us-east-1|m5.large||1|all-upfront",
			Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "m5.large",
			Count: 1, Term: 1, Payment: "all-upfront", UpfrontCost: 3000, Savings: 10,
		},
		config.RecommendationRecord{
			ID:       "aws|acct|ec2|us-east-1|m5.large||1|no-upfront",
			Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "m5.large",
			Count: 1, Term: 1, Payment: "no-upfront", UpfrontCost: 0, MonthlyCost: float64Ptr(100), Savings: 10,
		},
	)

	handler := &Handler{config: mockStore, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer cap-token"},
		// Client sends variant A's id, but the NEW payment "no-upfront" with
		// stale variant-A costs.
		Body: `{"recommendations":[{"id":"aws|acct|ec2|us-east-1|m5.large||1|all-upfront","provider":"aws","service":"ec2","region":"us-east-1","resource_type":"m5.large","count":1,"term":1,"payment":"no-upfront","upfront_cost":3000,"savings":10}]}`,
	}
	result, err := handler.executePurchase(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, saved)
	require.Len(t, saved.Recommendations, 1)
	assert.Equal(t, "aws|acct|ec2|us-east-1|m5.large||1|no-upfront", saved.Recommendations[0].ID)
	assert.Equal(t, 0.0, saved.Recommendations[0].UpfrontCost)
	require.NotNil(t, saved.Recommendations[0].MonthlyCost)
	assert.Equal(t, 100.0, *saved.Recommendations[0].MonthlyCost)
	resultMap := result.(map[string]any)
	assert.Equal(t, 0.0, resultMap["total_upfront_cost"])
}

// TestHandler_executePurchase_SavingsPlanCountMustMatchStored: a Savings Plan
// rec is priced by hourly commitment, not by count; a count mismatch is
// refused rather than scaled.
func TestHandler_executePurchase_SavingsPlanCountMustMatchStored(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Email: "admin@example.com"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdminPurchaser()

	// Registered .Maybe(): see TestHandler_executePurchase_UnknownRecommendationRefused.
	mockStore.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil).Maybe()
	mockStore.On("GetPendingExecutions", mock.Anything).Return([]config.PurchaseExecution{}, nil).Maybe()
	mockStore.On("SavePurchaseExecution", mock.Anything, mock.Anything).Return(nil).Maybe()
	expectStoredRecs(mockStore, config.RecommendationRecord{
		Provider: "aws", Service: string(common.ServiceSavingsPlansCompute), Count: 1, Term: 1, Payment: "all-upfront",
		UpfrontCost: 0, MonthlyCost: float64Ptr(200),
	})

	handler := &Handler{config: mockStore, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"recommendations":[{"id":"rec-1","provider":"aws","service":"savings-plans-compute","count":2,"term":1,"payment":"all-upfront","upfront_cost":0,"savings":10}]}`,
	}
	_, err := handler.executePurchase(ctx, req)
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected a clientError, got: %v", err)
	assert.Equal(t, 409, ce.code)
	assert.Contains(t, ce.Error(), "savings plan count")
	mockStore.AssertNotCalled(t, "SavePurchaseExecution", mock.Anything, mock.Anything)
}

// TestPriceFromStored_ScalesByCountAndKeepsStoredIdentity is a pure unit
// test of priceFromStored's scaling and field-provenance contract.
func TestPriceFromStored_ScalesByCountAndKeepsStoredIdentity(t *testing.T) {
	stored := config.RecommendationRecord{
		ID: "stored-id", Provider: "aws", Service: "ec2", Count: 4,
		UpfrontCost: 1000, Savings: 100, MonthlyCost: nil, OnDemandCost: float64Ptr(2000),
		Details: json.RawMessage(`{"platform":"Linux/UNIX"}`),
	}

	t.Run("ratio 0.5 with nil MonthlyCost stays nil", func(t *testing.T) {
		req := config.RecommendationRecord{Count: 2, RecommendedCount: 4, Selected: true}
		out, err := priceFromStored(&req, &stored, 0)
		require.NoError(t, err)
		assert.Equal(t, 500.0, out.UpfrontCost)
		assert.Equal(t, 50.0, out.Savings)
		assert.Nil(t, out.MonthlyCost)
		require.NotNil(t, out.OnDemandCost)
		assert.Equal(t, 1000.0, *out.OnDemandCost)
	})

	t.Run("ratio 1 reproduces stored values exactly", func(t *testing.T) {
		req := config.RecommendationRecord{Count: 4, RecommendedCount: 4, Selected: true}
		out, err := priceFromStored(&req, &stored, 0)
		require.NoError(t, err)
		assert.Equal(t, stored.UpfrontCost, out.UpfrontCost)
		assert.Equal(t, stored.Savings, out.Savings)
		require.NotNil(t, out.OnDemandCost)
		assert.Equal(t, *stored.OnDemandCost, *out.OnDemandCost)
	})

	t.Run("Selected and RecommendedCount come from the request", func(t *testing.T) {
		req := config.RecommendationRecord{Count: 4, RecommendedCount: 8, Selected: false}
		out, err := priceFromStored(&req, &stored, 0)
		require.NoError(t, err)
		assert.Equal(t, 8, out.RecommendedCount)
		assert.False(t, out.Selected)
	})

	t.Run("ID, Details, Purchased, PurchaseID and Error come from stored even when the request sets Purchased", func(t *testing.T) {
		req := config.RecommendationRecord{
			Count: 4, RecommendedCount: 4, Selected: true,
			Purchased: true, PurchaseID: "req-purchase-id", Error: "req-error",
		}
		out, err := priceFromStored(&req, &stored, 0)
		require.NoError(t, err)
		assert.Equal(t, "stored-id", out.ID)
		assert.Equal(t, string(stored.Details), string(out.Details))
		assert.False(t, out.Purchased, "Purchased must come from the stored row, not the request")
		assert.Empty(t, out.PurchaseID)
		assert.Empty(t, out.Error)
	})
}

// TestLoadStoredRecommendationIndex_CaseFoldCollisionRefused: the store's
// unique index on the identity tuple (migration 000043) is case-sensitive on
// provider and payment, while recIdentityKey folds their case. Two stored
// rows differing only in case therefore collide under the fold even though
// the index allowed both rows to exist; the index build must refuse rather
// than silently keep whichever row inserted last (unreachable today because
// the scheduler always writes lowercase, but this is a money path and must
// fail loud rather than pick a row on a broken assumption).
func TestLoadStoredRecommendationIndex_CaseFoldCollisionRefused(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	mockStore.On("ListStoredRecommendations", mock.Anything, config.RecommendationFilter{Provider: "aws"}).Return([]config.RecommendationRecord{
		{ID: "lower-id", Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "m5.large", Term: 1, Payment: "all-upfront", Count: 1, UpfrontCost: 100},
		{ID: "upper-id", Provider: "AWS", Service: "ec2", Region: "us-east-1", ResourceType: "m5.large", Term: 1, Payment: "ALL-UPFRONT", Count: 1, UpfrontCost: 200},
	}, nil)

	handler := &Handler{config: mockStore}
	_, err := handler.loadStoredRecommendationIndex(ctx, []config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "m5.large", Term: 1, Payment: "all-upfront"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than one row for identity key")
	_, isClient := IsClientError(err)
	assert.False(t, isClient, "an index invariant violation is a server-side bug, not a client error")
}

// TestRecIdentityKey_TupleSemantics pins the identity-tuple contract:
// nil and "" CloudAccountID collapse to the same key, provider/payment case
// is folded, and every other component change yields a different key.
func TestRecIdentityKey_TupleSemantics(t *testing.T) {
	base := config.RecommendationRecord{
		Provider: "aws", Service: "ec2", Region: "us-east-1", ResourceType: "m5.large",
		Engine: "mysql", Term: 3, Payment: "all-upfront",
	}

	t.Run("nil and empty CloudAccountID collapse to the same key", func(t *testing.T) {
		empty := ""
		withNil := base
		withEmpty := base
		withEmpty.CloudAccountID = &empty
		assert.Equal(t, recIdentityKey(&withNil), recIdentityKey(&withEmpty))
	})

	t.Run("provider and payment case is folded", func(t *testing.T) {
		upper := base
		upper.Provider = "AWS"
		upper.Payment = "ALL-UPFRONT"
		assert.Equal(t, recIdentityKey(&base), recIdentityKey(&upper))
	})

	t.Run("a different payment yields a different key", func(t *testing.T) {
		other := base
		other.Payment = "no-upfront"
		assert.NotEqual(t, recIdentityKey(&base), recIdentityKey(&other))
	})

	t.Run("a different term yields a different key", func(t *testing.T) {
		other := base
		other.Term = 1
		assert.NotEqual(t, recIdentityKey(&base), recIdentityKey(&other))
	})

	t.Run("a different engine yields a different key", func(t *testing.T) {
		other := base
		other.Engine = "postgres"
		assert.NotEqual(t, recIdentityKey(&base), recIdentityKey(&other))
	})

	t.Run("a different account yields a different key", func(t *testing.T) {
		acctA := "acct-a"
		acctB := "acct-b"
		withA := base
		withA.CloudAccountID = &acctA
		withB := base
		withB.CloudAccountID = &acctB
		assert.NotEqual(t, recIdentityKey(&withA), recIdentityKey(&withB))
	})
}
