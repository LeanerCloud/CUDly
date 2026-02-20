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
	mockStore.On("ListPurchasePlans", ctx).Return(plans, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
	}
	result, err := handler.listPlans(ctx, req)
	require.NoError(t, err)

	assert.Len(t, result.Plans, 2)
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

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("CreatePurchasePlan", ctx, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"name": "New Plan", "enabled": true, "auto_purchase": false}`
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
