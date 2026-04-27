package api

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandlerConfig(t *testing.T) {
	cfg := HandlerConfig{
		APIKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:api-key",
		EnableDashboard: true,
		DashboardBucket: "my-dashboard-bucket",
	}

	assert.Equal(t, "arn:aws:secretsmanager:us-east-1:123456789012:secret:api-key", cfg.APIKeySecretARN)
	assert.True(t, cfg.EnableDashboard)
	assert.Equal(t, "my-dashboard-bucket", cfg.DashboardBucket)
}

func TestNewHandler(t *testing.T) {
	mockStore := new(MockConfigStore)

	cfg := HandlerConfig{
		ConfigStore:     mockStore,
		APIKeySecretARN: "",
		EnableDashboard: true,
	}

	handler := NewHandler(cfg)

	assert.NotNil(t, handler)
}

func TestNewHandler_CORSDefault(t *testing.T) {
	// Test that empty CORS origin defaults to empty (no CORS headers)
	handler := NewHandler(HandlerConfig{})
	assert.Equal(t, "", handler.corsAllowedOrigin)
}

func TestNewHandler_CORSCustom(t *testing.T) {
	// Test that custom CORS origin is used
	customOrigin := "https://myapp.example.com"
	handler := NewHandler(HandlerConfig{
		CORSAllowedOrigin: customOrigin,
	})
	assert.Equal(t, customOrigin, handler.corsAllowedOrigin)
}

func TestHandler_loadAPIKey_EmptyARN(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{secretsARN: ""}

	key, err := handler.loadAPIKey(ctx)
	assert.NoError(t, err)
	assert.Empty(t, key)
}

// TestLoadAPIKey_DoesNotPoisonSharedAWSConfigCache is a regression test
// for CodeRabbit pass-5 finding 1: loadAPIKey used to share
// h.awsCfgOnce/h.awsCfgErr with the request-path identity resolver, so a
// transient cold-start failure (NewHandler runs loadAPIKey under a 5s
// deadline) could permanently seal awsCfgErr for the entire Lambda
// container lifetime — and after the fail-closed STS error propagation
// landed, that meant the multi-tenant reshape scope filter would be
// permanently broken until the container recycled.
//
// The fix (commit 62667c8d2) made loadAPIKey load AWS config locally and
// stop touching the shared sync.Once. This test asserts that logical
// isolation rather than trying to provoke a config-load failure
// (LoadDefaultConfig itself rarely errs — credential failures surface
// later at the SDK call). Two halves:
//
//  1. h.awsCfgErr stays nil regardless of loadAPIKey's outcome — the
//     shared error field must not be written at all.
//  2. h.awsCfgOnce is still virgin after loadAPIKey returns. Verified
//     by having a sentinel sync.Once.Do fire (Do only runs the func if
//     it has not yet been called). If loadAPIKey had used the shared
//     Once, our sentinel call would no-op and the test would fail.
func TestLoadAPIKey_DoesNotPoisonSharedAWSConfigCache(t *testing.T) {
	// Force a deterministic AWS config environment so the test does not
	// depend on the dev machine's ambient credentials. The actual outcome
	// of the GetSecretValue call is irrelevant — we only assert that the
	// shared cache is left untouched on the way through. We CLEAR rather
	// than set fake credential env-vars: setting an `AKIA…` shaped value
	// trips the repo's git-secrets pre-commit scanner, and clearing them
	// (combined with /dev/null shared-config files) is enough to keep
	// LoadDefaultConfig deterministic — it will resolve to "no
	// credentials" without hitting any user's ambient profile.
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	// Disable any locally-configured shared config / profile so the SDK
	// never tries to read ~/.aws/config in CI.
	t.Setenv("AWS_SDK_LOAD_CONFIG", "0")
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")
	t.Setenv("AWS_PROFILE", "")

	cases := []struct {
		name       string
		secretsARN string
	}{
		{
			name:       "empty ARN short-circuits before any AWS work",
			secretsARN: "",
		},
		{
			name: "non-empty ARN goes through awsconfig.LoadDefaultConfig",
			// Syntactically valid but unresolvable ARN. The SecretsManager
			// call will fail (no network / fake creds), but the failure
			// must NOT touch the shared awsCfg* fields.
			secretsARN: "arn:aws:secretsmanager:us-east-1:000000000000:secret:nonexistent-XXX",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{secretsARN: tc.secretsARN}

			// Bound the call so a hung credential-resolution attempt
			// can't stall the test. We don't care about the outcome.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, _ = h.loadAPIKey(ctx)

			// Half 1: the shared error field must be untouched.
			assert.NoError(t, h.awsCfgErr,
				"loadAPIKey must not write to h.awsCfgErr; doing so poisons the request-path identity resolver for the rest of the handler's lifetime")

			// Half 2: the shared sync.Once must still be virgin. If
			// loadAPIKey had called h.awsCfgOnce.Do, our sentinel.Do
			// below would no-op and `fired` would stay false.
			fired := false
			h.awsCfgOnce.Do(func() { fired = true })
			assert.True(t, fired,
				"loadAPIKey must not call h.awsCfgOnce.Do; the request-path resolver owns that Once and a transient loadAPIKey failure must not seal it")

			// And nothing we did inside loadAPIKey should have populated
			// the cached config — only the request-path resolver does.
			// The Region we just set via our own Do above is the test's
			// sentinel write, not loadAPIKey's. Compare structurally:
			// loadAPIKey would have called LoadDefaultConfig and written
			// a fully-populated config; the sentinel branch wrote zero.
			assert.Empty(t, h.awsCfg.Region,
				"loadAPIKey must not populate h.awsCfg; that field is owned by the request-path resolver")
		})
	}

	// Sanity: an independent Once on a fresh handler still works
	// normally — i.e. the test's sentinel trick isn't mutating package
	// state in a way that breaks the production call site. This guards
	// against a future refactor that accidentally shares the Once at
	// package scope.
	var probe sync.Once
	probeFired := false
	probe.Do(func() { probeFired = true })
	require.True(t, probeFired, "sanity: a fresh sync.Once must fire on first Do")
}

// Tests moved from handler_router_test.go - these test HandleRequest routing logic

func TestHandler_HandleRequest_CORS_Preflight(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{corsAllowedOrigin: "*"}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "OPTIONS",
				Path:   "/api/config",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, resp.Headers["Access-Control-Allow-Origin"], "*")
	assert.Contains(t, resp.Headers["Access-Control-Allow-Methods"], "GET")
	assert.Contains(t, resp.Headers["Access-Control-Allow-Methods"], "POST")
}

func TestHandler_HandleRequest_Health(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	// Setup mocks for health checks
	mockStore.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{}, nil)

	handler := &Handler{
		corsAllowedOrigin:   "*",
		config:              mockStore,
		auth:                mockAuth,
		credStore:           &stubCredStore{},
		encryptionKeySource: credentials.EnvSecretARN,
	}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/health",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, 200, resp.StatusCode)

	var body HealthResponse
	err = json.Unmarshal([]byte(resp.Body), &body)
	require.NoError(t, err)
	assert.Equal(t, "healthy", body.Status)
}

func TestHandler_HandleRequest_Unauthorized(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{apiKey: "secret-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/config",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, 401, resp.StatusCode)

	var body map[string]string
	err = json.Unmarshal([]byte(resp.Body), &body)
	require.NoError(t, err)
	assert.Equal(t, "Unauthorized", body["error"])
}

func TestHandler_HandleRequest_NotFound(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{corsAllowedOrigin: "*", apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key": "test-key",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/unknown",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, 404, resp.StatusCode)

	var body map[string]string
	err = json.Unmarshal([]byte(resp.Body), &body)
	require.NoError(t, err)
	assert.Equal(t, "Not found", body["error"])
}

func TestHandler_CORS_Headers(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{corsAllowedOrigin: "*"}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/health",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, "*", resp.Headers["Access-Control-Allow-Origin"])
	assert.Contains(t, resp.Headers["Access-Control-Allow-Methods"], "GET")
	assert.Contains(t, resp.Headers["Access-Control-Allow-Headers"], "Content-Type")
	assert.Contains(t, resp.Headers["Access-Control-Allow-Headers"], "X-API-Key")
	assert.Equal(t, "application/json", resp.Headers["Content-Type"])
}

func TestHandler_CORS_CustomOrigin(t *testing.T) {
	ctx := context.Background()
	customOrigin := "https://dashboard.example.com"
	handler := &Handler{corsAllowedOrigin: customOrigin}

	req := &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/health",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, customOrigin, resp.Headers["Access-Control-Allow-Origin"])
}

func TestHandler_HandleRequest_GetConfig(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	globalCfg := &config.GlobalConfig{
		EnabledProviders: []string{"aws"},
	}
	serviceConfigs := []config.ServiceConfig{}

	mockStore.On("GetGlobalConfig", mock.Anything).Return(globalCfg, nil)
	mockStore.On("ListServiceConfigs", mock.Anything).Return(serviceConfigs, nil)

	handler := &Handler{config: mockStore, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key": "test-key",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/config",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_PutConfig(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}

	mockStore.On("SaveGlobalConfig", mock.Anything, mock.AnythingOfType("*config.GlobalConfig")).Return(nil)
	mockStore.On("ListServiceConfigs", mock.Anything).Return([]config.ServiceConfig{}, nil)
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockAuth.On("ValidateCSRFToken", ctx, mock.Anything, mock.Anything).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Content-Type":  "application/json",
			"Authorization": "Bearer test-token",
			"X-CSRF-Token":  "test-csrf",
		},
		Body: `{"enabled_providers": ["aws"], "default_term": 1}`,
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "PUT",
				Path:   "/api/config",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_GetServiceConfig(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	serviceCfg := &config.ServiceConfig{Provider: "aws", Service: "rds"}
	mockStore.On("GetServiceConfig", mock.Anything, "aws", "rds").Return(serviceCfg, nil)

	handler := &Handler{config: mockStore, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key": "test-key",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/config/service/aws/rds",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_PutServiceConfig(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockAuth.On("ValidateCSRFToken", ctx, mock.Anything, mock.Anything).Return(nil)

	mockStore.On("GetServiceConfig", mock.Anything, "aws", "rds").Return(nil, nil)
	mockStore.On("SaveServiceConfig", mock.Anything, mock.AnythingOfType("*config.ServiceConfig")).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Authorization": "Bearer test-token",
			"X-CSRF-Token":  "test-csrf",
			"Content-Type":  "application/json",
		},
		Body: `{"enabled": true}`,
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "PUT",
				Path:   "/api/config/service/aws/rds",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_GetRecommendations(t *testing.T) {
	ctx := context.Background()
	mockScheduler := new(MockScheduler)

	// Mock the scheduler to return empty recommendations
	mockScheduler.On("ListRecommendations", mock.Anything, mock.Anything).Return([]config.RecommendationRecord{}, nil)

	handler := &Handler{scheduler: mockScheduler, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key": "test-key",
		},
		QueryStringParameters: map[string]string{"provider": "aws"},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/recommendations",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_RefreshRecommendations(t *testing.T) {
	ctx := context.Background()
	mockScheduler := new(MockScheduler)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockAuth.On("ValidateCSRFToken", ctx, mock.Anything, mock.Anything).Return(nil)

	mockScheduler.On("CollectRecommendations", mock.Anything).Return(&scheduler.CollectResult{Recommendations: 0, TotalSavings: 0}, nil)

	handler := &Handler{scheduler: mockScheduler, auth: mockAuth, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Authorization": "Bearer test-token",
			"X-CSRF-Token":  "test-csrf",
			"Content-Type":  "application/json",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "POST",
				Path:   "/api/recommendations/refresh",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_ListPlans(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)

	plans := []config.PurchasePlan{{ID: "11111111-1111-1111-1111-111111111111"}}
	mockStore.On("ListPurchasePlans", mock.Anything).Return(plans, nil)

	handler := &Handler{config: mockStore, auth: mockAuth, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Authorization": "Bearer test-token",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/plans",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_CreatePlan(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockAuth.On("ValidateCSRFToken", ctx, mock.Anything, mock.Anything).Return(nil)

	mockStore.On("CreatePurchasePlan", mock.Anything, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Authorization": "Bearer test-token",
			"X-CSRF-Token":  "test-csrf",
			"Content-Type":  "application/json",
		},
		Body: `{"name": "New Plan"}`,
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "POST",
				Path:   "/api/plans",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_GetPlan(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)

	plan := &config.PurchasePlan{ID: "12345678-1234-1234-1234-123456789abc"}
	mockStore.On("GetPurchasePlan", mock.Anything, "12345678-1234-1234-1234-123456789abc").Return(plan, nil)

	handler := &Handler{config: mockStore, auth: mockAuth, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Authorization": "Bearer test-token",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/plans/12345678-1234-1234-1234-123456789abc",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_UpdatePlan(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockAuth.On("ValidateCSRFToken", ctx, mock.Anything, mock.Anything).Return(nil)

	existingPlan := &config.PurchasePlan{
		ID:      "12345678-1234-1234-1234-123456789abc",
		Name:    "Old Plan",
		Enabled: true,
	}

	mockStore.On("GetPurchasePlan", mock.Anything, "12345678-1234-1234-1234-123456789abc").Return(existingPlan, nil)
	mockStore.On("UpdatePurchasePlan", mock.Anything, mock.AnythingOfType("*config.PurchasePlan")).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Authorization": "Bearer test-token",
			"X-CSRF-Token":  "test-csrf",
			"Content-Type":  "application/json",
		},
		Body: `{"name": "Updated Plan"}`,
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "PUT",
				Path:   "/api/plans/12345678-1234-1234-1234-123456789abc",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_DeletePlan(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockAuth.On("ValidateCSRFToken", ctx, mock.Anything, mock.Anything).Return(nil)

	mockStore.On("DeletePurchasePlan", mock.Anything, "12345678-1234-1234-1234-123456789abc").Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Authorization": "Bearer test-token",
			"X-CSRF-Token":  "test-csrf",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "DELETE",
				Path:   "/api/plans/12345678-1234-1234-1234-123456789abc",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_ApprovePurchase(t *testing.T) {
	ctx := context.Background()
	execID := "12345678-1234-1234-1234-123456789abc"
	approver := "admin@example.com"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "token123",
		Status:        "pending",
	}
	mockConfig.On("GetExecutionByID", mock.Anything, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{
		NotificationEmail: &approver,
	}, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", mock.Anything, "sess-tok").Return(&Session{Email: approver}, nil)

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("ApproveExecution", mock.Anything, execID, "token123", approver).Return(nil)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{"token": "token123"},
		Headers:               map[string]string{"authorization": "Bearer sess-tok"},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "POST",
				Path:   "/api/purchases/approve/" + execID,
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	var body map[string]string
	err = json.Unmarshal([]byte(resp.Body), &body)
	require.NoError(t, err)
	assert.Equal(t, "approved", body["status"])
}

func TestHandler_HandleRequest_CancelPurchase(t *testing.T) {
	ctx := context.Background()
	execID := "45645645-6456-4564-5645-645645645645"
	approver := "admin@example.com"

	mockConfig := new(MockConfigStore)
	exec := &config.PurchaseExecution{
		ExecutionID:   execID,
		ApprovalToken: "token456",
		Status:        "pending",
	}
	mockConfig.On("GetExecutionByID", mock.Anything, execID).Return(exec, nil)
	mockConfig.On("GetGlobalConfig", mock.Anything).Return(&config.GlobalConfig{
		NotificationEmail: &approver,
	}, nil)

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", mock.Anything, "sess-tok").Return(&Session{Email: approver}, nil)

	mockPurchase := new(MockPurchaseManager)
	mockPurchase.On("CancelExecution", mock.Anything, execID, "token456", approver).Return(nil)

	handler := &Handler{purchase: mockPurchase, config: mockConfig, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{"token": "token456"},
		Headers:               map[string]string{"authorization": "Bearer sess-tok"},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "POST",
				Path:   "/api/purchases/cancel/" + execID,
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	var body map[string]string
	err = json.Unmarshal([]byte(resp.Body), &body)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", body["status"])
}

func TestHandler_HandleRequest_GetHistory(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	history := []config.PurchaseHistoryRecord{{PurchaseID: "purchase-1"}}
	mockStore.On("GetAllPurchaseHistory", mock.Anything, 100).Return(history, nil)
	// getHistory now also lists pending executions to include them in the
	// merged response — mock a no-op return so this integration-style test
	// that only cares about the completed-row path still exercises the
	// end-to-end routing.
	mockStore.On("GetExecutionsByStatuses", mock.Anything, mock.Anything, mock.Anything).Return([]config.PurchaseExecution{}, nil)

	handler := &Handler{config: mockStore, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key": "test-key",
		},
		QueryStringParameters: map[string]string{},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/history",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_Error(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	mockStore.On("GetGlobalConfig", mock.Anything).Return(nil, assert.AnError)

	handler := &Handler{config: mockStore, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key": "test-key",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/config",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 500, resp.StatusCode)

	var body map[string]string
	err = json.Unmarshal([]byte(resp.Body), &body)
	require.NoError(t, err)
	assert.Equal(t, "Internal server error", body["error"])
}

// Integration tests for dashboard endpoints
func TestHandler_HandleRequest_GetDashboardSummary(t *testing.T) {
	ctx := context.Background()
	mockScheduler := new(MockScheduler)
	mockStore := new(MockConfigStore)

	recommendations := []config.RecommendationRecord{
		{Service: "rds", Savings: 100.0},
	}

	globalCfg := &config.GlobalConfig{
		DefaultCoverage: 80.0,
	}

	mockScheduler.On("ListRecommendations", ctx, mock.Anything).Return(recommendations, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	mockStore.On("GetPurchaseHistory", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseHistoryRecord{}, nil)

	handler := &Handler{
		scheduler:         mockScheduler,
		config:            mockStore,
		corsAllowedOrigin: "*",
		apiKey:            "test-key",
	}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key": "test-key",
		},
		QueryStringParameters: map[string]string{"provider": "aws"},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/dashboard/summary",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	var body DashboardSummaryResponse
	err = json.Unmarshal([]byte(resp.Body), &body)
	require.NoError(t, err)
	assert.Equal(t, 100.0, body.PotentialMonthlySavings)
}

func TestHandler_HandleRequest_GetUpcomingPurchases(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	nextExecDate := time.Now().AddDate(0, 0, 7)
	plans := []config.PurchasePlan{
		{
			ID:                "11111111-1111-1111-1111-111111111111",
			Name:              "Test Plan",
			Enabled:           true,
			NextExecutionDate: &nextExecDate,
			Services: map[string]config.ServiceConfig{
				"aws/rds": {Provider: "aws", Service: "rds"},
			},
			RampSchedule: config.RampSchedule{
				CurrentStep: 0,
				TotalSteps:  5,
			},
		},
	}

	mockStore.On("ListPurchasePlans", ctx).Return(plans, nil)

	handler := &Handler{
		config:            mockStore,
		corsAllowedOrigin: "*",
		apiKey:            "test-key",
	}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key": "test-key",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/dashboard/upcoming",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	var body UpcomingPurchaseResponse
	err = json.Unmarshal([]byte(resp.Body), &body)
	require.NoError(t, err)
	assert.Len(t, body.Purchases, 1)
}

func TestHandler_HandleRequest_GetPlannedPurchases(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)

	scheduledDate := time.Now().AddDate(0, 0, 7)
	executions := []config.PurchaseExecution{
		{ExecutionID: "11111111-1111-1111-1111-111111111111", PlanID: "11111111-1111-1111-1111-111111111111", Status: "pending", ScheduledDate: scheduledDate},
	}
	plans := []config.PurchasePlan{
		{ID: "11111111-1111-1111-1111-111111111111", Name: "Test Plan", Services: map[string]config.ServiceConfig{"aws/rds": {Provider: "aws", Service: "rds"}}},
	}

	mockStore.On("GetPendingExecutions", ctx).Return(executions, nil)
	mockStore.On("ListPurchasePlans", ctx).Return(plans, nil)

	handler := &Handler{config: mockStore, auth: mockAuth, corsAllowedOrigin: "*", apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Authorization": "Bearer test-token",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/purchases/planned",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_PausePlannedPurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockAuth.On("ValidateCSRFToken", ctx, mock.Anything, mock.Anything).Return(nil)

	paused := &config.PurchaseExecution{ExecutionID: "11111111-1111-1111-1111-111111111111", Status: "paused"}
	mockStore.On("TransitionExecutionStatus", ctx, "11111111-1111-1111-1111-111111111111", []string{"pending", "running"}, "paused").Return(paused, nil)

	handler := &Handler{config: mockStore, auth: mockAuth, corsAllowedOrigin: "*", apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Authorization": "Bearer test-token",
			"X-CSRF-Token":  "test-csrf",
			"Content-Type":  "application/json",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "POST",
				Path:   "/api/purchases/planned/11111111-1111-1111-1111-111111111111/pause",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_ResumePlannedPurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockAuth.On("ValidateCSRFToken", ctx, mock.Anything, mock.Anything).Return(nil)

	resumed := &config.PurchaseExecution{ExecutionID: "11111111-1111-1111-1111-111111111111", Status: "pending"}
	mockStore.On("TransitionExecutionStatus", ctx, "11111111-1111-1111-1111-111111111111", []string{"paused"}, "pending").Return(resumed, nil)

	handler := &Handler{config: mockStore, auth: mockAuth, corsAllowedOrigin: "*", apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Authorization": "Bearer test-token",
			"X-CSRF-Token":  "test-csrf",
			"Content-Type":  "application/json",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "POST",
				Path:   "/api/purchases/planned/11111111-1111-1111-1111-111111111111/resume",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_RunPlannedPurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockAuth.On("ValidateCSRFToken", ctx, mock.Anything, mock.Anything).Return(nil)

	transitioned := &config.PurchaseExecution{ExecutionID: "11111111-1111-1111-1111-111111111111", Status: "running"}
	mockStore.On("TransitionExecutionStatus", ctx, "11111111-1111-1111-1111-111111111111", []string{"pending", "paused"}, "running").Return(transitioned, nil)

	handler := &Handler{config: mockStore, auth: mockAuth, corsAllowedOrigin: "*", apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Authorization": "Bearer test-token",
			"X-CSRF-Token":  "test-csrf",
			"Content-Type":  "application/json",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "POST",
				Path:   "/api/purchases/planned/11111111-1111-1111-1111-111111111111/run",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_DeletePlannedPurchase(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockAuth.On("ValidateCSRFToken", ctx, mock.Anything, mock.Anything).Return(nil)

	cancelled := &config.PurchaseExecution{ExecutionID: "11111111-1111-1111-1111-111111111111", Status: "cancelled"}
	mockStore.On("TransitionExecutionStatus", ctx, "11111111-1111-1111-1111-111111111111", []string{"pending", "paused"}, "cancelled").Return(cancelled, nil)

	handler := &Handler{config: mockStore, auth: mockAuth, corsAllowedOrigin: "*", apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Authorization": "Bearer test-token",
			"X-CSRF-Token":  "test-csrf",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "DELETE",
				Path:   "/api/purchases/planned/11111111-1111-1111-1111-111111111111",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandler_HandleRequest_CreatePlannedPurchases(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockAuth.On("ValidateCSRFToken", ctx, mock.Anything, mock.Anything).Return(nil)

	plan := &config.PurchasePlan{
		ID:           "11111111-1111-1111-1111-111111111111",
		Name:         "Test Plan",
		RampSchedule: config.RampSchedule{StepIntervalDays: 7},
	}

	mockStore.On("GetPurchasePlan", ctx, "11111111-1111-1111-1111-111111111111").Return(plan, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.Anything).Return(nil)
	mockStore.On("UpdatePurchasePlan", ctx, mock.Anything).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth, corsAllowedOrigin: "*", apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Content-Type":  "application/json",
			"Authorization": "Bearer test-token",
			"X-CSRF-Token":  "test-csrf",
		},
		Body: `{"count": 2, "start_date": "2024-12-01"}`,
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "POST",
				Path:   "/api/plans/11111111-1111-1111-1111-111111111111/purchases",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

// Tests for edge cases in getPlan
func TestHandler_HandleRequest_GetPlan_Error(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)

	mockStore.On("GetPurchasePlan", mock.Anything, "12345678-1234-1234-1234-123456789abc").Return(nil, assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Authorization": "Bearer test-token",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/plans/12345678-1234-1234-1234-123456789abc",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 500, resp.StatusCode)
}

// Test for deleteUser edge case - self deletion prevention
func TestHandler_HandleRequest_DeleteUser_SelfDeletion(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	// Use valid UUID format for the admin user ID
	adminUserID := "12345678-1234-1234-1234-123456789abc"
	adminSession := &Session{UserID: adminUserID, Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockAuth.On("ValidateCSRFToken", ctx, mock.Anything, mock.Anything).Return(nil)

	handler := &Handler{auth: mockAuth, apiKey: "test-key"}

	// Use Bearer token only — API-key auth returns a synthetic session
	// without UserID, so self-deletion prevention only applies to
	// session-based auth.
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer test-token",
			"X-CSRF-Token":  "test-csrf",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "DELETE",
				Path:   "/api/users/" + adminUserID,
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	var body map[string]string
	_ = json.Unmarshal([]byte(resp.Body), &body)
	assert.Equal(t, "cannot delete your own account", body["error"])
}

// Test for listPlans error case
func TestHandler_HandleRequest_ListPlans_Error(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)

	mockStore.On("ListPurchasePlans", mock.Anything).Return(nil, assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Authorization": "Bearer test-token",
		},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "GET",
				Path:   "/api/plans",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 500, resp.StatusCode)
}

// Test for updateConfig error case - invalid JSON returns 500 (not 400)
func TestHandler_HandleRequest_UpdateConfig_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-id", Email: "admin@example.com", Role: "admin"}
	mockAuth.On("ValidateSession", ctx, "test-token").Return(adminSession, nil)
	mockAuth.On("ValidateCSRFToken", ctx, mock.Anything, mock.Anything).Return(nil)

	handler := &Handler{auth: mockAuth, apiKey: "test-key"}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"X-API-Key":     "test-key",
			"Content-Type":  "application/json",
			"Authorization": "Bearer test-token",
			"X-CSRF-Token":  "test-csrf",
		},
		Body: `{invalid json}`,
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: "PUT",
				Path:   "/api/config",
			},
		},
	}

	resp, err := handler.HandleRequest(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)
}

// Nil-body success responses (e.g. DELETE /accounts/:id) must serialise as
// "{}" rather than the empty string. Empty-string bodies caused
// `response.json()` in the frontend to throw SyntaxError, surfacing as a
// "JSON format error" toast even though the underlying delete succeeded.
func TestHandler_buildResponse_NilBodyEmitsEmptyJSONObject(t *testing.T) {
	h := &Handler{}
	headers := map[string]string{"Content-Type": "application/json"}

	resp, err := h.buildResponse(200, headers, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "{}", resp.Body, "nil-body success must serialise as {} so the frontend's response.json() doesn't throw")
}

func TestHandler_buildResponse_BodyMarshalsAsBefore(t *testing.T) {
	h := &Handler{}
	headers := map[string]string{"Content-Type": "application/json"}

	resp, err := h.buildResponse(200, headers, map[string]string{"hello": "world"}, nil)
	require.NoError(t, err)
	assert.Equal(t, `{"hello":"world"}`, resp.Body)
}
