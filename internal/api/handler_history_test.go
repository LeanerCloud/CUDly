package api

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adminHistoryReq builds an admin-authed request and wires the auth mock so
// requirePermission short-circuits. Returns the mocked auth so tests can add
// extra expectations.
func adminHistoryReq(ctx context.Context) (*MockAuthService, *events.LambdaFunctionURLRequest) {
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}, nil)
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	return mockAuth, req
}

func TestHandler_getHistory(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	history := []config.PurchaseHistoryRecord{
		{AccountID: "123456789012", PurchaseID: "purchase-1", UpfrontCost: 100.0, EstimatedSavings: 10.0},
	}

	mockStore.On("GetPurchaseHistory", ctx, "123456789012", 100).Return(history, nil)
	mockStore.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{}, nil)

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	params := map[string]string{
		"account_id": "123456789012",
	}

	result, err := handler.getHistory(ctx, req, params)
	require.NoError(t, err)

	historyResp := result.(HistoryResponse)
	assert.Len(t, historyResp.Purchases, 1)
	assert.Equal(t, "completed", historyResp.Purchases[0].Status, "DB-backed rows must be tagged completed")
	assert.Equal(t, 1, historyResp.Summary.TotalPurchases)
	assert.Equal(t, 1, historyResp.Summary.TotalCompleted)
	assert.Equal(t, 0, historyResp.Summary.TotalPending)
	assert.Equal(t, 100.0, historyResp.Summary.TotalUpfront)
	assert.Equal(t, 10.0, historyResp.Summary.TotalMonthlySavings)
	assert.Equal(t, 120.0, historyResp.Summary.TotalAnnualSavings)
}

func TestHandler_getHistory_AllAccounts(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	history := []config.PurchaseHistoryRecord{
		{AccountID: "111111111111", PurchaseID: "purchase-1", UpfrontCost: 100.0, EstimatedSavings: 10.0},
		{AccountID: "222222222222", PurchaseID: "purchase-2", UpfrontCost: 200.0, EstimatedSavings: 20.0},
	}

	mockStore.On("GetAllPurchaseHistory", ctx, 100).Return(history, nil)
	mockStore.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{}, nil)

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	params := map[string]string{}

	result, err := handler.getHistory(ctx, req, params)
	require.NoError(t, err)

	historyResp := result.(HistoryResponse)
	assert.Len(t, historyResp.Purchases, 2)
	assert.Equal(t, 2, historyResp.Summary.TotalPurchases)
	assert.Equal(t, 2, historyResp.Summary.TotalCompleted)
	assert.Equal(t, 300.0, historyResp.Summary.TotalUpfront)
	assert.Equal(t, 30.0, historyResp.Summary.TotalMonthlySavings)
}

// TestHandler_getHistory_IncludesPending verifies a pending PurchaseExecution
// shows up in the response as a synthetic history row with status=pending,
// counted toward TotalPending, and explicitly NOT folded into the dollar
// totals (TotalUpfront stays at the completed row's value).
func TestHandler_getHistory_IncludesPending(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	completed := []config.PurchaseHistoryRecord{
		{AccountID: "acc-1", PurchaseID: "done-1", UpfrontCost: 500.0, EstimatedSavings: 50.0},
	}
	pending := []config.PurchaseExecution{
		{
			ExecutionID:      "pend-1",
			Status:           "pending",
			TotalUpfrontCost: 999.0,
			EstimatedSavings: 99.0,
			Recommendations: []config.RecommendationRecord{
				{Provider: "aws", Service: "ec2", Region: "us-east-1"},
				{Provider: "aws", Service: "rds", Region: "us-east-1"},
			},
		},
	}

	approverEmail := "ops@example.com"
	mockStore.On("GetAllPurchaseHistory", ctx, 100).Return(completed, nil)
	mockStore.On("GetPendingExecutions", ctx).Return(pending, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{NotificationEmail: &approverEmail}, nil)

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.getHistory(ctx, req, map[string]string{})
	require.NoError(t, err)

	historyResp := result.(HistoryResponse)
	assert.Len(t, historyResp.Purchases, 2)
	assert.Equal(t, 2, historyResp.Summary.TotalPurchases)
	assert.Equal(t, 1, historyResp.Summary.TotalCompleted)
	assert.Equal(t, 1, historyResp.Summary.TotalPending)
	assert.Equal(t, 500.0, historyResp.Summary.TotalUpfront, "pending spend must not inflate committed totals")
	assert.Equal(t, 50.0, historyResp.Summary.TotalMonthlySavings)

	// Locate the pending row by PurchaseID and assert its shape.
	var pendingRow *config.PurchaseHistoryRecord
	for i := range historyResp.Purchases {
		if historyResp.Purchases[i].PurchaseID == "pend-1" {
			pendingRow = &historyResp.Purchases[i]
			break
		}
	}
	require.NotNil(t, pendingRow, "pending execution must render as a history row")
	assert.Equal(t, "pending", pendingRow.Status)
	assert.Equal(t, "aws", pendingRow.Provider, "single-provider execution must collapse to that provider")
	assert.Equal(t, 999.0, pendingRow.UpfrontCost)
	assert.Equal(t, 2, pendingRow.Count)
	assert.Equal(t, "2 commitment(s)", pendingRow.ResourceType)
	assert.Equal(t, approverEmail, pendingRow.Approver, "pending rows must expose the approver email so the UI can tell the user whose inbox to check")
}

func TestHandler_getHistory_CustomLimit(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	mockStore.On("GetAllPurchaseHistory", ctx, 50).Return([]config.PurchaseHistoryRecord{}, nil)
	mockStore.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{}, nil)

	mockAuth, req := adminHistoryReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	params := map[string]string{
		"limit": "50",
	}

	_, err := handler.getHistory(ctx, req, params)
	require.NoError(t, err)
}

// TestHandler_getHistory_PermissionDenied asserts that a non-admin user without
// view:purchases gets 403 and never reaches the store.
func TestHandler_getHistory_PermissionDenied(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(&Session{
		UserID: "viewer-1",
		Role:   "user",
	}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "view", "purchases").Return(false, nil)

	handler := &Handler{auth: mockAuth, config: mockStore}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer viewer-token"},
	}
	_, err := handler.getHistory(ctx, req, map[string]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
	mockStore.AssertNotCalled(t, "GetPurchaseHistory")
	mockStore.AssertNotCalled(t, "GetAllPurchaseHistory")
}
