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
	mockAuth.grantAdmin()
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
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	serviceCfg := &config.ServiceConfig{
		Provider: "aws",
		Service:  "rds",
		Enabled:  true,
		Term:     3,
		Coverage: 80,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(serviceCfg, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getServiceConfig(ctx, req, "aws/rds")
	require.NoError(t, err)

	cfg := result.(*config.ServiceConfig)
	assert.Equal(t, "aws", cfg.Provider)
	assert.Equal(t, "rds", cfg.Service)
}

func TestHandler_getServiceConfig_InvalidFormat(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{corsAllowedOrigin: "*", auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getServiceConfig(ctx, req, "invalid-format")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid service path")
}

func TestHandler_getServiceConfig_NotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetServiceConfig", ctx, "aws", "unknown").Return(nil, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getServiceConfig(ctx, req, "aws/unknown")
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

// TestMergeServiceConfig_PresenceAwareFilterOverlay verifies that the
// recommendation-filter fields are overlaid from the request only when the
// body actually carries the key, while the four scalar UI fields are always
// overlaid. A partial PUT that omits a filter must preserve the existing
// value; a PUT that includes it (even empty) must apply it.
func TestMergeServiceConfig_PresenceAwareFilterOverlay(t *testing.T) {
	ctx := context.Background()
	// Each subtest gets a fresh existing record: mergeServiceConfig overlays
	// onto the pointer returned by GetServiceConfig (the production postgres
	// store returns a fresh struct per call), so a shared fixture would leak
	// mutations across subtests.
	newExisting := func() *config.ServiceConfig {
		return &config.ServiceConfig{
			Provider: "aws", Service: "rds", Enabled: true, Term: 3,
			Payment: "all-upfront", Coverage: 50,
			IncludeEngines: []string{"mysql"},
			ExcludeTypes:   []string{"db.t2.micro"},
			MinCount:       4,
		}
	}

	t.Run("body includes filter fields -> overlaid", func(t *testing.T) {
		store := new(MockConfigStore)
		store.On("GetServiceConfig", ctx, "aws", "rds").Return(newExisting(), nil)
		t.Cleanup(func() { store.AssertExpectations(t) })

		req := config.ServiceConfig{
			Provider: "aws", Service: "rds", Enabled: false, Term: 1,
			Payment: "no-upfront", Coverage: 90,
			IncludeEngines: []string{"postgres"},
			MinCount:       7,
		}
		body := `{"enabled":false,"term":1,"payment":"no-upfront","coverage":90,"include_engines":["postgres"],"min_count":7}`

		merged, err := mergeServiceConfig(ctx, store, &req, body)
		require.NoError(t, err)
		assert.False(t, merged.Enabled)
		assert.Equal(t, 1, merged.Term)
		assert.Equal(t, 90.0, merged.Coverage)
		assert.Equal(t, []string{"postgres"}, merged.IncludeEngines)
		assert.Equal(t, 7, merged.MinCount)
		// exclude_types absent from body -> preserved from existing
		assert.Equal(t, []string{"db.t2.micro"}, merged.ExcludeTypes)
	})

	t.Run("body omits filter fields -> preserved", func(t *testing.T) {
		store := new(MockConfigStore)
		store.On("GetServiceConfig", ctx, "aws", "rds").Return(newExisting(), nil)
		t.Cleanup(func() { store.AssertExpectations(t) })

		req := config.ServiceConfig{
			Provider: "aws", Service: "rds", Enabled: true, Term: 3,
			Payment: "all-upfront", Coverage: 80,
		}
		body := `{"enabled":true,"term":3,"payment":"all-upfront","coverage":80}`

		merged, err := mergeServiceConfig(ctx, store, &req, body)
		require.NoError(t, err)
		assert.Equal(t, 80.0, merged.Coverage)
		assert.Equal(t, []string{"mysql"}, merged.IncludeEngines, "omitted filter must be preserved")
		assert.Equal(t, []string{"db.t2.micro"}, merged.ExcludeTypes)
		assert.Equal(t, 4, merged.MinCount, "omitted min_count must be preserved")
	})

	t.Run("body includes empty filter -> cleared", func(t *testing.T) {
		store := new(MockConfigStore)
		store.On("GetServiceConfig", ctx, "aws", "rds").Return(newExisting(), nil)
		t.Cleanup(func() { store.AssertExpectations(t) })

		req := config.ServiceConfig{
			Provider: "aws", Service: "rds", Enabled: true, Term: 3,
			Payment: "all-upfront", Coverage: 80,
			IncludeEngines: []string{},
		}
		body := `{"enabled":true,"term":3,"payment":"all-upfront","coverage":80,"include_engines":[]}`

		merged, err := mergeServiceConfig(ctx, store, &req, body)
		require.NoError(t, err)
		assert.Empty(t, merged.IncludeEngines, "explicit empty list clears the filter")
	})
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
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetGlobalConfig", ctx).Return(nil, assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getConfig(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_getConfig_ListServiceConfigsError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	globalCfg := &config.GlobalConfig{
		EnabledProviders: []string{"aws"},
	}

	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	mockStore.On("ListServiceConfigs", ctx).Return(nil, assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
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

	adminSession := &Session{UserID: "admin-user"}
	userSession := &Session{UserID: "regular-user"}

	globalCfg := &config.GlobalConfig{EnabledProviders: []string{"aws"}}
	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	mockStore.On("ListServiceConfigs", ctx).Return([]config.ServiceConfig{}, nil)
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
	mockAuth.On("HasPermissionAPI", mock.Anything, "admin-user", mock.Anything, mock.Anything).Return(true, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	t.Run("admin sees SourceIdentity", func(t *testing.T) {
		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer admin-token"},
		}
		result, err := handler.getConfig(ctx, req)
		require.NoError(t, err)
		// resolveSourceIdentity always returns a non-nil struct (best-effort,
		// returns an empty struct on failure). The key invariant is that admin
		// sessions receive the field and non-admin sessions do not.
		require.NotNil(t, result.SourceIdentity)
	})

	t.Run("regression #407: non-admin does not receive SourceIdentity", func(t *testing.T) {
		// The regular user has view:config permission (passes requirePermission)
		// but does not hold admin:* (requireAdmin fails), so SourceIdentity is
		// withheld. We match the specific permission verbs so the admin:* call
		// (action="admin", resource="*") still returns false.
		mockAuth.On("HasPermissionAPI", ctx, "regular-user", "view", "config").Return(true, nil)
		mockAuth.On("HasPermissionAPI", ctx, "regular-user", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(false, nil).Maybe()
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
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(nil, assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getServiceConfig(ctx, req, "aws/rds")
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
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getServiceConfig(ctx, req, "invalid/rds")
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

// Regression tests for 02-M4: GET /api/config and GET /api/config/service/*
// must enforce requirePermission("view","config") and return 403 to callers
// who lack that permission, even though the route is only AuthUser-gated.

func TestHandler_getConfig_ViewConfigPermission_Enforced(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	userSession := &Session{
		UserID: "cccccccc-cccc-cccc-cccc-cccccccccccc",
		Email:  "viewer@example.com",
	}
	// User has a valid session but view:config is revoked.
	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
	mockAuth.On("HasPermissionAPI", ctx, userSession.UserID, "view", "config").Return(false, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer user-token"},
	}
	result, err := handler.getConfig(ctx, req)
	require.Error(t, err, "must be rejected when view:config is revoked")
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 403, ce.code, "expected 403, got %d", ce.code)
	mockStore.AssertNotCalled(t, "GetGlobalConfig", mock.Anything)
}

func TestHandler_getServiceConfig_ViewConfigPermission_Enforced(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	userSession := &Session{
		UserID: "cccccccc-cccc-cccc-cccc-cccccccccccc",
		Email:  "viewer@example.com",
	}
	// User has a valid session but view:config is revoked.
	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
	mockAuth.On("HasPermissionAPI", ctx, userSession.UserID, "view", "config").Return(false, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer user-token"},
	}
	result, err := handler.getServiceConfig(ctx, req, "aws/rds")
	require.Error(t, err, "must be rejected when view:config is revoked")
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 403, ce.code, "expected 403, got %d", ce.code)
	mockStore.AssertNotCalled(t, "GetServiceConfig", mock.Anything, mock.Anything, mock.Anything)
}
