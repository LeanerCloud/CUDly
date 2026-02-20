package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandler_approvePurchase(t *testing.T) {
	ctx := context.Background()
	mockPurchase := new(MockPurchaseManager)

	mockPurchase.On("ApproveExecution", ctx, "12345678-1234-1234-1234-123456789abc", "valid-token").Return(nil)

	handler := &Handler{purchase: mockPurchase}

	result, err := handler.approvePurchase(ctx, "12345678-1234-1234-1234-123456789abc", "valid-token")
	require.NoError(t, err)

	resultMap := result.(map[string]string)
	assert.Equal(t, "approved", resultMap["status"])
}

func TestHandler_cancelPurchase(t *testing.T) {
	ctx := context.Background()
	mockPurchase := new(MockPurchaseManager)

	mockPurchase.On("CancelExecution", ctx, "45645645-6456-4564-5645-645645645645", "valid-token").Return(nil)

	handler := &Handler{purchase: mockPurchase}

	result, err := handler.cancelPurchase(ctx, "45645645-6456-4564-5645-645645645645", "valid-token")
	require.NoError(t, err)

	resultMap := result.(map[string]string)
	assert.Equal(t, "cancelled", resultMap["status"])
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

	execution := &config.PurchaseExecution{
		ExecutionID: "11111111-1111-1111-1111-111111111111",
		Status:      "pending",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetExecutionByID", ctx, "11111111-1111-1111-1111-111111111111").Return(execution, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.MatchedBy(func(e *config.PurchaseExecution) bool {
		return e.Status == "paused"
	})).Return(nil)

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
	mockStore.On("GetExecutionByID", ctx, "99999999-9999-9999-9999-999999999999").Return(nil, errors.New("not found"))

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.pausePlannedPurchase(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "execution not found")
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

	execution := &config.PurchaseExecution{
		ExecutionID: "11111111-1111-1111-1111-111111111111",
		Status:      "paused",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetExecutionByID", ctx, "11111111-1111-1111-1111-111111111111").Return(execution, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.MatchedBy(func(e *config.PurchaseExecution) bool {
		return e.Status == "pending"
	})).Return(nil)

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

	execution := &config.PurchaseExecution{
		ExecutionID: "11111111-1111-1111-1111-111111111111",
		Status:      "pending",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetExecutionByID", ctx, "11111111-1111-1111-1111-111111111111").Return(execution, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.MatchedBy(func(e *config.PurchaseExecution) bool {
		return e.Status == "running"
	})).Return(nil)

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

	execution := &config.PurchaseExecution{
		ExecutionID: "11111111-1111-1111-1111-111111111111",
		Status:      "pending",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetExecutionByID", ctx, "11111111-1111-1111-1111-111111111111").Return(execution, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.MatchedBy(func(e *config.PurchaseExecution) bool {
		return e.Status == "cancelled"
	})).Return(nil)

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

	// Return nil execution with no error
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetExecutionByID", ctx, "99999999-9999-9999-9999-999999999999").Return(nil, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.pausePlannedPurchase(ctx, req, "99999999-9999-9999-9999-999999999999")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "execution not found")
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
	mockStore.On("GetExecutionByID", ctx, "99999999-9999-9999-9999-999999999999").Return(nil, nil)

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
	mockStore.On("GetExecutionByID", ctx, "99999999-9999-9999-9999-999999999999").Return(nil, nil)

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
	mockStore.On("GetExecutionByID", ctx, "99999999-9999-9999-9999-999999999999").Return(nil, nil)

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
