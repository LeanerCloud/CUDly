package api

import (
	"context"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandler_GetHealth_AllHealthy(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)

	handler := &Handler{
		config: mockStore,
		auth:   mockAuth,
	}

	response, err := handler.GetHealth(ctx)
	require.NoError(t, err)

	assert.Equal(t, "healthy", response.Status)
	assert.NotNil(t, response.Timestamp)
	assert.Len(t, response.Checks, 2)
	assert.Equal(t, "healthy", response.Checks["config_store"].Status)
	assert.Equal(t, "healthy", response.Checks["auth_service"].Status)
}

func TestHandler_GetHealth_ConfigStoreUnhealthy(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	mockStore.On("GetGlobalConfig", ctx).Return(nil, errors.New("database connection failed"))

	handler := &Handler{
		config: mockStore,
		auth:   mockAuth,
	}

	response, err := handler.GetHealth(ctx)
	require.NoError(t, err)

	assert.Equal(t, "degraded", response.Status)
	assert.Equal(t, "unhealthy", response.Checks["config_store"].Status)
	assert.Contains(t, response.Checks["config_store"].Message, "Failed to access config store")
	assert.Equal(t, "healthy", response.Checks["auth_service"].Status)
}

func TestHandler_GetHealth_AuthServiceUnhealthy(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)

	handler := &Handler{
		config: mockStore,
		auth:   nil, // Auth service not configured
	}

	response, err := handler.GetHealth(ctx)
	require.NoError(t, err)

	assert.Equal(t, "degraded", response.Status)
	assert.Equal(t, "healthy", response.Checks["config_store"].Status)
	assert.Equal(t, "unhealthy", response.Checks["auth_service"].Status)
	assert.Contains(t, response.Checks["auth_service"].Message, "Auth service not initialized")
}

func TestHandler_GetHealth_BothUnhealthy(t *testing.T) {
	ctx := context.Background()

	handler := &Handler{
		config: nil, // Config store not configured
		auth:   nil, // Auth service not configured
	}

	response, err := handler.GetHealth(ctx)
	require.NoError(t, err)

	assert.Equal(t, "degraded", response.Status)
	assert.Equal(t, "unhealthy", response.Checks["config_store"].Status)
	assert.Contains(t, response.Checks["config_store"].Message, "Config store not initialized")
	assert.Equal(t, "unhealthy", response.Checks["auth_service"].Status)
	assert.Contains(t, response.Checks["auth_service"].Message, "Auth service not initialized")
}

func TestHandler_GetHealth_ConfigStoreNotInitialized(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	handler := &Handler{
		config: nil,
		auth:   mockAuth,
	}

	response, err := handler.GetHealth(ctx)
	require.NoError(t, err)

	assert.Equal(t, "degraded", response.Status)
	assert.Equal(t, "unhealthy", response.Checks["config_store"].Status)
	assert.Contains(t, response.Checks["config_store"].Message, "Config store not initialized")
}

func TestHandler_checkConfigStore_Healthy(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	mockStore.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil)

	handler := &Handler{config: mockStore}

	check := handler.checkConfigStore(ctx)

	assert.Equal(t, "healthy", check.Status)
	assert.Empty(t, check.Message)
}

func TestHandler_checkConfigStore_NotInitialized(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{config: nil}

	check := handler.checkConfigStore(ctx)

	assert.Equal(t, "unhealthy", check.Status)
	assert.Equal(t, "Config store not initialized", check.Message)
}

func TestHandler_checkConfigStore_AccessError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	mockStore.On("GetGlobalConfig", mock.Anything).Return(nil, errors.New("connection timeout"))

	handler := &Handler{config: mockStore}

	check := handler.checkConfigStore(ctx)

	assert.Equal(t, "unhealthy", check.Status)
	assert.Contains(t, check.Message, "Failed to access config store")
	assert.Contains(t, check.Message, "connection timeout")
}

func TestHandler_checkAuthService_Healthy(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	handler := &Handler{auth: mockAuth}

	check := handler.checkAuthService(ctx)

	assert.Equal(t, "healthy", check.Status)
	assert.Empty(t, check.Message)
}

func TestHandler_checkAuthService_NotInitialized(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{auth: nil}

	check := handler.checkAuthService(ctx)

	assert.Equal(t, "unhealthy", check.Status)
	assert.Equal(t, "Auth service not initialized", check.Message)
}

func TestHealthResponse_Structure(t *testing.T) {
	response := &HealthResponse{
		Status: "healthy",
		Checks: map[string]HealthCheck{
			"test": {Status: "healthy", Message: ""},
		},
	}

	assert.Equal(t, "healthy", response.Status)
	assert.NotNil(t, response.Checks)
	assert.Len(t, response.Checks, 1)
}

func TestHealthCheck_Structure(t *testing.T) {
	check := HealthCheck{
		Status:  "unhealthy",
		Message: "Service unavailable",
	}

	assert.Equal(t, "unhealthy", check.Status)
	assert.Equal(t, "Service unavailable", check.Message)
}
