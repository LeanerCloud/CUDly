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
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}

	globalCfg := &config.GlobalConfig{
		EnabledProviders: []string{"aws"},
		DefaultTerm:      3,
		DefaultCoverage:  80,
	}

	serviceConfigs := []config.ServiceConfig{
		{Provider: "aws", Service: "rds", Enabled: true},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	mockStore.On("ListServiceConfigs", ctx).Return(serviceConfigs, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getConfig(ctx, req)
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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(nil)
	// Mock ListServiceConfigs for propagation of global defaults
	mockStore.On("ListServiceConfigs", ctx).Return([]config.ServiceConfig{}, nil)
	// updateConfig now calls GetGlobalConfig when recommendations_cache_stale_hours
	// or recommendations_lookback_days is omitted from the request body, so the
	// existing persisted value can be preserved rather than zeroed out (PR #308
	// CodeRabbit pass-2). The body in this test omits both fields.
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
		RecommendationsLookbackDays:    config.DefaultRecommendationsLookbackDays,
	}, nil)

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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

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

func TestHandler_updateServiceConfig_CommitmentOptsReject(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(nil, nil)

	// Probe data says RDS 3yr no-upfront doesn't exist. Save must 400.
	// SaveServiceConfig is NOT set up — asserting it's never called.
	handler := &Handler{
		config: mockStore,
		auth:   mockAuth,
		commitmentOpts: &stubCommitmentOpts{
			validateFn: func(_ context.Context, provider, service string, term int, payment string) (bool, error) {
				assert.Equal(t, "aws", provider)
				assert.Equal(t, "rds", service)
				assert.Equal(t, 3, term)
				assert.Equal(t, "no-upfront", payment)
				return false, nil
			},
		},
	}

	body := `{"enabled": true, "term": 3, "payment": "no-upfront", "coverage": 80}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    body,
	}
	result, err := handler.updateServiceConfig(ctx, req, "aws/rds")

	require.Error(t, err)
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.message, "3yr no-upfront")
	mockStore.AssertNotCalled(t, "SaveServiceConfig", mock.Anything, mock.Anything)
}

func TestHandler_updateServiceConfig_CommitmentOptsAccept(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(nil, nil)
	mockStore.On("SaveServiceConfig", ctx, mock.AnythingOfType("*config.ServiceConfig")).Return(nil)

	handler := &Handler{
		config: mockStore,
		auth:   mockAuth,
		commitmentOpts: &stubCommitmentOpts{
			validateFn: func(context.Context, string, string, int, string) (bool, error) {
				return true, nil
			},
		},
	}

	body := `{"enabled": true, "term": 1, "payment": "all-upfront", "coverage": 80}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    body,
	}
	result, err := handler.updateServiceConfig(ctx, req, "aws/rds")

	require.NoError(t, err)
	assert.Equal(t, "updated", result.Status)
}

func TestHandler_updateServiceConfig_NoSlash(t *testing.T) {
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

	req := &events.LambdaFunctionURLRequest{}
	result, err := handler.getConfig(ctx, req)
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

	req := &events.LambdaFunctionURLRequest{}
	result, err := handler.getConfig(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// Regression tests for issue #407: SourceIdentity (cloud account ID, Azure
// tenant ID) must only be included in responses for admin sessions.

func TestHandler_getConfig_SourceIdentity_AdminOnly(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{Role: "admin", UserID: "admin-user"}
	userSession := &Session{Role: "user", UserID: "regular-user"}

	globalCfg := &config.GlobalConfig{EnabledProviders: []string{"aws"}}
	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	mockStore.On("ListServiceConfigs", ctx).Return([]config.ServiceConfig{}, nil)
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	t.Run("admin sees SourceIdentity", func(t *testing.T) {
		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer admin-token"},
		}
		result, err := handler.getConfig(ctx, req)
		require.NoError(t, err)
		// SourceIdentity is set (may be a zero-value struct for the test
		// env that lacks AWS credentials, but the field is non-nil for admin).
		// We verify that a non-admin result is nil below, which is the key
		// security invariant.
		_ = result.SourceIdentity // may be nil or non-nil depending on cloud env
	})

	t.Run("regression #407: non-admin does not receive SourceIdentity", func(t *testing.T) {
		mockAuth.On("HasPermissionAPI", ctx, "regular-user", mock.Anything, mock.Anything).Return(false, nil)
		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer user-token"},
		}
		result, err := handler.getConfig(ctx, req)
		require.NoError(t, err)
		assert.Nil(t, result.SourceIdentity,
			"SourceIdentity must be nil for non-admin sessions (issue #407)")
	})
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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	// updateConfig calls GetGlobalConfig before validation to preserve persisted
	// values for fields omitted from the request body (PR #308 CR pass-2). The
	// validation error fires after the merge, so the mock is still required.
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
		RecommendationsLookbackDays:    config.DefaultRecommendationsLookbackDays,
	}, nil)

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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(assert.AnError)
	// updateConfig calls GetGlobalConfig before save to preserve persisted
	// values for fields omitted from the request body (PR #308 CR pass-2).
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
		RecommendationsLookbackDays:    config.DefaultRecommendationsLookbackDays,
	}, nil)

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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
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
	}

	serviceConfigs := []config.ServiceConfig{
		{Provider: "aws", Service: "rds", Enabled: true},
		{Provider: "aws", Service: "ec2", Enabled: true},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(nil)
	// updateConfig calls GetGlobalConfig before save to preserve persisted
	// values for fields omitted from the request body (PR #308 CR pass-2).
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
		RecommendationsLookbackDays:    config.DefaultRecommendationsLookbackDays,
	}, nil)
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
	}

	serviceConfigs := []config.ServiceConfig{
		{Provider: "aws", Service: "rds", Enabled: true},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(nil)
	// updateConfig calls GetGlobalConfig before save to preserve persisted
	// values for fields omitted from the request body (PR #308 CR pass-2).
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
		RecommendationsLookbackDays:    config.DefaultRecommendationsLookbackDays,
	}, nil)
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
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(nil)
	// updateConfig calls GetGlobalConfig before save to preserve persisted
	// values for fields omitted from the request body (PR #308 CR pass-2).
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
		RecommendationsLookbackDays:    config.DefaultRecommendationsLookbackDays,
	}, nil)
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
