package api

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandler_getConfig(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	globalCfg := &config.GlobalConfig{
		EnabledProviders: []string{"aws"},
		DefaultTerm:      3,
		DefaultCoverage:  80,
	}

	serviceConfigs := []config.ServiceConfig{
		{Provider: "aws", Service: "rds", Enabled: true},
	}

	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	mockStore.On("ListServiceConfigs", ctx).Return(serviceConfigs, nil)

	handler := &Handler{config: mockStore}

	result, err := handler.getConfig(ctx)
	require.NoError(t, err)

	assert.NotNil(t, result.Global)
	assert.NotNil(t, result.Services)
}

func TestHandler_updateConfig(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(nil)
	// Mock ListServiceConfigs for propagation of global defaults
	mockStore.On("ListServiceConfigs", ctx).Return([]config.ServiceConfig{}, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"enabled_providers": ["aws", "azure"], "default_term": 3}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateConfig(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, "updated", result.Status)
}

func TestHandler_updateConfig_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{corsAllowedOrigin: "*", auth: mockAuth}

	body := `{invalid json}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateConfig(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_getServiceConfig(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	serviceCfg := &config.ServiceConfig{
		Provider: "aws",
		Service:  "rds",
		Enabled:  true,
		Term:     3,
		Coverage: 80,
	}

	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(serviceCfg, nil)

	handler := &Handler{config: mockStore}

	result, err := handler.getServiceConfig(ctx, "aws/rds")
	require.NoError(t, err)

	cfg := result.(*config.ServiceConfig)
	assert.Equal(t, "aws", cfg.Provider)
	assert.Equal(t, "rds", cfg.Service)
}

func TestHandler_getServiceConfig_InvalidFormat(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{corsAllowedOrigin: "*"}

	result, err := handler.getServiceConfig(ctx, "invalid-format")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid service path")
}

func TestHandler_getServiceConfig_NotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	mockStore.On("GetServiceConfig", ctx, "aws", "unknown").Return(nil, nil)

	handler := &Handler{config: mockStore}

	result, err := handler.getServiceConfig(ctx, "aws/unknown")
	require.NoError(t, err)

	// Returns empty response for not found
	_, ok := result.(*EmptyServiceConfigResponse)
	assert.True(t, ok)
}

func TestHandler_updateServiceConfig(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(nil, nil)
	mockStore.On("SaveServiceConfig", ctx, mock.AnythingOfType("*config.ServiceConfig")).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"enabled": true, "term": 3, "coverage": 80}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateServiceConfig(ctx, req, "aws/rds")
	require.NoError(t, err)

	assert.Equal(t, "updated", result.Status)
}

func TestHandler_updateServiceConfig_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)

	handler := &Handler{corsAllowedOrigin: "*", auth: mockAuth}

	body := `{invalid json}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateServiceConfig(ctx, req, "aws/rds")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_updateServiceConfig_NoSlash(t *testing.T) {
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

	// Without proper format (no slash), provider won't be set and validation should fail
	body := `{"enabled": true}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateServiceConfig(ctx, req, "invalid")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid service path")
}

func TestHandler_getConfig_GlobalConfigError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	mockStore.On("GetGlobalConfig", ctx).Return(nil, assert.AnError)

	handler := &Handler{config: mockStore}

	result, err := handler.getConfig(ctx)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_getConfig_ListServiceConfigsError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	globalCfg := &config.GlobalConfig{
		EnabledProviders: []string{"aws"},
	}

	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	mockStore.On("ListServiceConfigs", ctx).Return(nil, assert.AnError)

	handler := &Handler{config: mockStore}

	result, err := handler.getConfig(ctx)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_getServiceConfig_Error(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(nil, assert.AnError)

	handler := &Handler{config: mockStore}

	result, err := handler.getServiceConfig(ctx, "aws/rds")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_updateConfig_ValidationError(t *testing.T) {
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

	// Invalid config - negative coverage percentage
	body := `{"enabled_providers": ["aws"], "default_coverage": -10}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateConfig(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "validation error")
}

func TestHandler_updateConfig_SaveError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"enabled_providers": ["aws"], "default_term": 3}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateConfig(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_updateServiceConfig_SaveError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(nil, nil)
	mockStore.On("SaveServiceConfig", ctx, mock.AnythingOfType("*config.ServiceConfig")).Return(assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"enabled": true, "term": 3, "coverage": 80}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateServiceConfig(ctx, req, "aws/rds")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_getServiceConfig_InvalidProvider(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{}

	result, err := handler.getServiceConfig(ctx, "invalid/rds")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid provider")
}

func TestHandler_updateConfig_WithPropagation(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	serviceConfigs := []config.ServiceConfig{
		{Provider: "aws", Service: "rds", Enabled: true},
		{Provider: "aws", Service: "ec2", Enabled: true},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(nil)
	mockStore.On("ListServiceConfigs", ctx).Return(serviceConfigs, nil)
	mockStore.On("SaveServiceConfig", ctx, mock.AnythingOfType("*config.ServiceConfig")).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"enabled_providers": ["aws"], "default_term": 3, "default_coverage": 80}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateConfig(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "updated", result.Status)

	// Verify SaveServiceConfig was called for each service
	mockStore.AssertNumberOfCalls(t, "SaveServiceConfig", 2)
}

func TestHandler_updateConfig_PropagationServiceSaveError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	serviceConfigs := []config.ServiceConfig{
		{Provider: "aws", Service: "rds", Enabled: true},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(nil)
	mockStore.On("ListServiceConfigs", ctx).Return(serviceConfigs, nil)
	// Simulate failure when saving service config during propagation
	mockStore.On("SaveServiceConfig", ctx, mock.AnythingOfType("*config.ServiceConfig")).Return(assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"enabled_providers": ["aws"], "default_term": 3}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	// Should still succeed even if service config propagation fails
	result, err := handler.updateConfig(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "updated", result.Status)
}

func TestHandler_updateConfig_PropagationListError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(nil)
	// Simulate failure when listing service configs for propagation
	mockStore.On("ListServiceConfigs", ctx).Return(nil, assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"enabled_providers": ["aws"], "default_term": 3}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	// Should still succeed even if listing fails - global config was saved
	result, err := handler.updateConfig(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "updated", result.Status)
}
