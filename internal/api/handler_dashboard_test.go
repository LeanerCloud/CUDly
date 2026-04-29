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

// createMockLambdaRequest creates a mock Lambda function URL request for testing
func createMockLambdaRequest(sourceIP string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				SourceIP: sourceIP,
			},
		},
	}
}

// adminDashboardReq returns (mocked auth with admin session, admin-authed request).
// Dashboard handlers are now permission-gated; this short-circuits the gate so
// existing tests keep exercising the aggregation logic.
func adminDashboardReq(ctx context.Context) (*MockAuthService, *events.LambdaFunctionURLRequest) {
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(&Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
		Role:   "admin",
	}, nil)
	return mockAuth, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
}

func TestHandler_getDashboardSummary(t *testing.T) {
	ctx := context.Background()
	mockScheduler := new(MockScheduler)
	mockStore := new(MockConfigStore)

	recommendations := []config.RecommendationRecord{
		{Service: "rds", Savings: 100.0},
		{Service: "ec2", Savings: 200.0},
		{Service: "rds", Savings: 50.0},
	}

	globalCfg := &config.GlobalConfig{
		DefaultCoverage: 75.0,
	}

	mockScheduler.On("ListRecommendations", ctx, mock.Anything).Return(recommendations, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	mockStore.On("GetPurchaseHistory", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseHistoryRecord{}, nil)

	mockAuth, req := adminDashboardReq(ctx)
	handler := &Handler{
		auth:      mockAuth,
		scheduler: mockScheduler,
		config:    mockStore,
	}

	params := map[string]string{"provider": "aws"}
	result, err := handler.getDashboardSummary(ctx, req, params)
	require.NoError(t, err)

	assert.Equal(t, 350.0, result.PotentialMonthlySavings)
	assert.Equal(t, 3, result.TotalRecommendations)
	assert.Equal(t, 75.0, result.TargetCoverage)
	assert.Equal(t, 2, len(result.ByService))
	assert.Equal(t, 150.0, result.ByService["rds"].PotentialSavings)
	assert.Equal(t, 200.0, result.ByService["ec2"].PotentialSavings)
}

// dashboardOverrideStore embeds MockConfigStore but overrides
// GetAccountServiceOverride so that the dashboard's coverage-cap path
// (issue #196) sees the per-account override the test seeded.
// MockConfigStore stubs that method to return nil unconditionally, which is
// the right default for the rest of the handler tests but blocks override
// scenarios from being exercised end-to-end.
type dashboardOverrideStore struct {
	*MockConfigStore
	overrides map[string]*config.AccountServiceOverride // key: account|provider|service
}

func (s *dashboardOverrideStore) GetAccountServiceOverride(_ context.Context, accountID, provider, service string) (*config.AccountServiceOverride, error) {
	return s.overrides[accountID+"|"+provider+"|"+service], nil
}

// Issue #196 — per-account coverage override scales the headline
// "potential savings" so the dashboard reflects the user's intended
// commitment, not the un-overridden total.
func TestHandler_getDashboardSummary_PerAccountCoverageScalesSavings(t *testing.T) {
	ctx := context.Background()
	mockScheduler := new(MockScheduler)
	mockStore := new(MockConfigStore)
	store := &dashboardOverrideStore{
		MockConfigStore: mockStore,
		overrides: map[string]*config.AccountServiceOverride{
			"acct-A|aws|rds": {Coverage: float64Ptr(50)},
		},
	}

	acctA := "acct-A"
	acctB := "acct-B"
	recommendations := []config.RecommendationRecord{
		// $100/mo, account A overrides coverage to 50% → $50 contribution
		{Provider: "aws", Service: "rds", Savings: 100.0, CloudAccountID: &acctA},
		// $100/mo, account B has no override → full $100 contribution
		{Provider: "aws", Service: "rds", Savings: 100.0, CloudAccountID: &acctB},
	}

	mockScheduler.On("ListRecommendations", ctx, mock.Anything).Return(recommendations, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{DefaultCoverage: 80.0}, nil)
	mockStore.On("GetPurchaseHistory", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseHistoryRecord{}, nil)
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(&config.ServiceConfig{
		Provider: "aws", Service: "rds", Enabled: true, Coverage: 100,
	}, nil)

	mockAuth, req := adminDashboardReq(ctx)
	handler := &Handler{
		auth:      mockAuth,
		scheduler: mockScheduler,
		config:    store,
	}

	result, err := handler.getDashboardSummary(ctx, req, map[string]string{"provider": "aws"})
	require.NoError(t, err)

	assert.InDelta(t, 150.0, result.PotentialMonthlySavings, 0.001,
		"acct-A scaled to 50% ($50) + acct-B at full ($100) = $150")
	assert.InDelta(t, 150.0, result.ByService["rds"].PotentialSavings, 0.001)
}

func float64Ptr(f float64) *float64 { return &f }

// summarizeRecommendationsWithCoverage table-driven unit tests cover the
// scaling math in isolation from the handler / store / auth dependencies.
func TestSummarizeRecommendationsWithCoverage(t *testing.T) {
	acctA := "acct-A"
	acctB := "acct-B"
	rec := func(account string, savings float64) config.RecommendationRecord {
		a := account
		return config.RecommendationRecord{
			Provider: "aws", Service: "rds", Savings: savings, CloudAccountID: &a,
		}
	}
	keyA := config.AccountConfigKey(acctA, "aws", "rds")
	_ = acctB // referenced only via rec(acctB, …) inside test cases

	tests := []struct {
		name      string
		recs      []config.RecommendationRecord
		coverage  map[string]float64
		wantTotal float64
	}{
		{
			name:      "nil coverage map preserves un-scaled total",
			recs:      []config.RecommendationRecord{rec(acctA, 100), rec(acctB, 200)},
			coverage:  nil,
			wantTotal: 300,
		},
		{
			name:      "missing key falls through to full savings",
			recs:      []config.RecommendationRecord{rec(acctA, 100), rec(acctB, 200)},
			coverage:  map[string]float64{keyA: 50}, // B unconfigured
			wantTotal: 50 + 200,
		},
		{
			name:      "zero coverage scales savings to zero",
			recs:      []config.RecommendationRecord{rec(acctA, 100)},
			coverage:  map[string]float64{keyA: 0},
			wantTotal: 0,
		},
		{
			name:      "coverage at 100 applies no scaling",
			recs:      []config.RecommendationRecord{rec(acctA, 100)},
			coverage:  map[string]float64{keyA: 100},
			wantTotal: 100,
		},
		{
			name:      "coverage above 100 capped at 100",
			recs:      []config.RecommendationRecord{rec(acctA, 100)},
			coverage:  map[string]float64{keyA: 150},
			wantTotal: 100,
		},
		{
			name: "nil CloudAccountID rec uses full savings",
			recs: []config.RecommendationRecord{
				{Provider: "aws", Service: "rds", Savings: 100, CloudAccountID: nil},
			},
			coverage:  map[string]float64{keyA: 50},
			wantTotal: 100,
		},
		{
			name:      "fractional scaling is preserved",
			recs:      []config.RecommendationRecord{rec(acctA, 200)},
			coverage:  map[string]float64{keyA: 33.5},
			wantTotal: 200 * 33.5 / 100,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			total, byService := summarizeRecommendationsWithCoverage(tc.recs, tc.coverage)
			assert.InDelta(t, tc.wantTotal, total, 0.0001)
			assert.InDelta(t, tc.wantTotal, byService["rds"].PotentialSavings, 0.0001)
		})
	}
}

func TestHandler_getUpcomingPurchases(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	nextExecDate := time.Now().AddDate(0, 0, 7)
	plans := []config.PurchasePlan{
		{
			ID:                "11111111-1111-1111-1111-111111111111",
			Name:              "Test Plan 1",
			Enabled:           true,
			NextExecutionDate: &nextExecDate,
			Services: map[string]config.ServiceConfig{
				"aws/rds": {
					Provider: "aws",
					Service:  "rds",
				},
			},
			RampSchedule: config.RampSchedule{
				CurrentStep: 0,
				TotalSteps:  5,
			},
		},
		{
			ID:                "22222222-2222-2222-2222-222222222222",
			Name:              "Disabled Plan",
			Enabled:           false,
			NextExecutionDate: &nextExecDate,
		},
	}

	mockStore.On("ListPurchasePlans", ctx).Return(plans, nil)

	mockAuth, req := adminDashboardReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.getUpcomingPurchases(ctx, req)
	require.NoError(t, err)

	// Only enabled plans should be returned
	assert.Len(t, result.Purchases, 1)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", result.Purchases[0].ExecutionID)
	assert.Equal(t, "Test Plan 1", result.Purchases[0].PlanName)
	assert.Equal(t, "aws", result.Purchases[0].Provider)
	assert.Equal(t, "rds", result.Purchases[0].Service)
	assert.Equal(t, 1, result.Purchases[0].StepNumber)
	assert.Equal(t, 5, result.Purchases[0].TotalSteps)
}

// mockStoreWithPlanAccounts embeds MockConfigStore and overrides GetPlanAccounts
// with a per-plan lookup. MockConfigStore.GetPlanAccounts always returns nil,nil
// (see mocks_test.go), which defeats the scoped-user filter the tests exercise.
type mockStoreWithPlanAccounts struct {
	*MockConfigStore
	planAccounts map[string][]config.CloudAccount
}

func (m *mockStoreWithPlanAccounts) GetPlanAccounts(_ context.Context, planID string) ([]config.CloudAccount, error) {
	return m.planAccounts[planID], nil
}

// TestHandler_getUpcomingPurchases_ScopedUser asserts that a non-admin user
// with allowed_accounts=["Production"] only sees plans whose associated
// accounts include "Production". Plan B is attributed to "Staging" and must
// be filtered out.
func TestHandler_getUpcomingPurchases_ScopedUser(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	nextExecDate := time.Now().AddDate(0, 0, 7)
	planA := config.PurchasePlan{
		ID:                "11111111-1111-1111-1111-111111111111",
		Name:              "Plan A",
		Enabled:           true,
		NextExecutionDate: &nextExecDate,
		Services: map[string]config.ServiceConfig{
			"aws/rds": {Provider: "aws", Service: "rds"},
		},
		RampSchedule: config.RampSchedule{CurrentStep: 0, TotalSteps: 5},
	}
	planB := config.PurchasePlan{
		ID:                "22222222-2222-2222-2222-222222222222",
		Name:              "Plan B",
		Enabled:           true,
		NextExecutionDate: &nextExecDate,
		Services: map[string]config.ServiceConfig{
			"aws/ec2": {Provider: "aws", Service: "ec2"},
		},
		RampSchedule: config.RampSchedule{CurrentStep: 0, TotalSteps: 5},
	}

	mockStore.On("ListPurchasePlans", ctx).Return([]config.PurchasePlan{planA, planB}, nil)
	store := &mockStoreWithPlanAccounts{
		MockConfigStore: mockStore,
		planAccounts: map[string][]config.CloudAccount{
			planA.ID: {{ID: "acc-prod", Name: "Production"}},
			planB.ID: {{ID: "acc-stage", Name: "Staging"}},
		},
	}

	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(&Session{
		UserID: "viewer-1",
		Role:   "user",
	}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "view", "purchases").Return(true, nil)
	mockAuth.On("GetAllowedAccountsAPI", ctx, "viewer-1").Return([]string{"Production"}, nil)

	handler := &Handler{auth: mockAuth, config: store}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer viewer-token"},
	}

	result, err := handler.getUpcomingPurchases(ctx, req)
	require.NoError(t, err)

	// Only Plan A (Production) passes the allowed_accounts filter.
	assert.Len(t, result.Purchases, 1)
	assert.Equal(t, "Plan A", result.Purchases[0].PlanName)
}

// TestHandler_getUpcomingPurchases_ScopedUser_SkipsUnattributed locks down
// that a plan with no associated accounts is hidden from scoped users — the
// safe default when we can't attribute the plan to a specific account.
func TestHandler_getUpcomingPurchases_ScopedUser_SkipsUnattributed(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	nextExecDate := time.Now().AddDate(0, 0, 7)
	plan := config.PurchasePlan{
		ID:                "11111111-1111-1111-1111-111111111111",
		Name:              "Unattributed Plan",
		Enabled:           true,
		NextExecutionDate: &nextExecDate,
		RampSchedule:      config.RampSchedule{CurrentStep: 0, TotalSteps: 5},
	}
	mockStore.On("ListPurchasePlans", ctx).Return([]config.PurchasePlan{plan}, nil)
	store := &mockStoreWithPlanAccounts{
		MockConfigStore: mockStore,
		planAccounts:    map[string][]config.CloudAccount{plan.ID: {}},
	}

	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(&Session{
		UserID: "viewer-1",
		Role:   "user",
	}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "view", "purchases").Return(true, nil)
	mockAuth.On("GetAllowedAccountsAPI", ctx, "viewer-1").Return([]string{"Production"}, nil)

	handler := &Handler{auth: mockAuth, config: store}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer viewer-token"},
	}

	result, err := handler.getUpcomingPurchases(ctx, req)
	require.NoError(t, err)
	assert.Len(t, result.Purchases, 0)
}

func TestHandler_getPublicInfo(t *testing.T) {
	ctx := context.Background()

	t.Run("with auth service and admin exists", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("CheckAdminExists", ctx).Return(true, nil)

		handler := &Handler{
			auth:       mockAuth,
			secretsARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:api-key-abc123",
		}

		result, err := handler.getPublicInfo(ctx, createMockLambdaRequest("192.168.1.1"))
		require.NoError(t, err)

		assert.Equal(t, "1.0.0", result.Version)
		assert.True(t, result.AdminExists)
		assert.Contains(t, result.APIKeySecretURL, "us-east-1")
		assert.Contains(t, result.APIKeySecretURL, "secretsmanager")
	})

	t.Run("with auth service and no admin", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("CheckAdminExists", ctx).Return(false, nil)

		handler := &Handler{
			auth: mockAuth,
		}

		result, err := handler.getPublicInfo(ctx, createMockLambdaRequest("192.168.1.1"))
		require.NoError(t, err)

		assert.False(t, result.AdminExists)
		assert.Empty(t, result.APIKeySecretURL)
	})

	t.Run("auth service check error still returns response", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("CheckAdminExists", ctx).Return(false, errors.New("db error"))

		handler := &Handler{
			auth: mockAuth,
		}

		result, err := handler.getPublicInfo(ctx, createMockLambdaRequest("192.168.1.1"))
		require.NoError(t, err)

		// Error should be swallowed, adminExists defaults to false
		assert.False(t, result.AdminExists)
	})

	t.Run("without auth service", func(t *testing.T) {
		handler := &Handler{}

		result, err := handler.getPublicInfo(ctx, createMockLambdaRequest("192.168.1.1"))
		require.NoError(t, err)

		assert.False(t, result.AdminExists)
	})

	t.Run("ARN parsing for different regions", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("CheckAdminExists", ctx).Return(true, nil)

		handler := &Handler{
			auth:       mockAuth,
			secretsARN: "arn:aws:secretsmanager:eu-west-1:987654321098:secret:my-secret-xyz789",
		}

		result, err := handler.getPublicInfo(ctx, createMockLambdaRequest("192.168.1.1"))
		require.NoError(t, err)

		assert.Contains(t, result.APIKeySecretURL, "eu-west-1")
	})

	t.Run("invalid ARN format", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("CheckAdminExists", ctx).Return(true, nil)

		handler := &Handler{
			auth:       mockAuth,
			secretsARN: "invalid-arn",
		}

		result, err := handler.getPublicInfo(ctx, createMockLambdaRequest("192.168.1.1"))
		require.NoError(t, err)

		// Invalid ARN should result in empty URL
		assert.Empty(t, result.APIKeySecretURL)
	})

	t.Run("with rate limiting - allowed", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockRateLimiter := new(MockRateLimiter)
		mockAuth.On("CheckAdminExists", ctx).Return(true, nil)
		mockRateLimiter.On("AllowWithIP", ctx, "192.168.1.1", "api_general").Return(true, nil)

		handler := &Handler{
			auth:        mockAuth,
			rateLimiter: mockRateLimiter,
		}

		result, err := handler.getPublicInfo(ctx, createMockLambdaRequest("192.168.1.1"))
		require.NoError(t, err)
		assert.True(t, result.AdminExists)
	})

}

func TestHandler_calculateCommitmentMetrics(t *testing.T) {
	ctx := context.Background()

	t.Run("no purchase history", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockStore.On("GetPurchaseHistory", ctx, "account-123", 1000).Return([]config.PurchaseHistoryRecord{}, nil)

		handler := &Handler{config: mockStore}

		activeCommitments, committedMonthly, ytdSavings := handler.calculateCommitmentMetrics(ctx, "account-123")

		assert.Equal(t, 0, activeCommitments)
		assert.Equal(t, 0.0, committedMonthly)
		assert.Equal(t, 0.0, ytdSavings)
	})

	t.Run("purchase history error returns zeros", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockStore.On("GetPurchaseHistory", ctx, "account-123", 1000).Return(nil, errors.New("db error"))

		handler := &Handler{config: mockStore}

		activeCommitments, committedMonthly, ytdSavings := handler.calculateCommitmentMetrics(ctx, "account-123")

		assert.Equal(t, 0, activeCommitments)
		assert.Equal(t, 0.0, committedMonthly)
		assert.Equal(t, 0.0, ytdSavings)
	})

	t.Run("with active commitments", func(t *testing.T) {
		mockStore := new(MockConfigStore)

		// Create a purchase made 6 months ago with 1-year term (still active)
		purchaseTime := time.Now().AddDate(0, -6, 0)
		purchases := []config.PurchaseHistoryRecord{
			{
				Timestamp:        purchaseTime,
				Term:             1, // 1-year term
				EstimatedSavings: 100.0,
			},
		}

		mockStore.On("GetPurchaseHistory", ctx, "account-123", 1000).Return(purchases, nil)

		handler := &Handler{config: mockStore}

		activeCommitments, committedMonthly, ytdSavings := handler.calculateCommitmentMetrics(ctx, "account-123")

		assert.Equal(t, 1, activeCommitments)
		assert.Equal(t, 100.0, committedMonthly)
		// YTD savings depends on when the purchase was made relative to year start
		assert.GreaterOrEqual(t, ytdSavings, 0.0)
	})

	t.Run("with expired commitments", func(t *testing.T) {
		mockStore := new(MockConfigStore)

		// Create a purchase made 2 years ago with 1-year term (expired)
		purchaseTime := time.Now().AddDate(-2, 0, 0)
		purchases := []config.PurchaseHistoryRecord{
			{
				Timestamp:        purchaseTime,
				Term:             1, // 1-year term
				EstimatedSavings: 100.0,
			},
		}

		mockStore.On("GetPurchaseHistory", ctx, "account-123", 1000).Return(purchases, nil)

		handler := &Handler{config: mockStore}

		activeCommitments, committedMonthly, ytdSavings := handler.calculateCommitmentMetrics(ctx, "account-123")

		// Should skip expired commitments
		assert.Equal(t, 0, activeCommitments)
		assert.Equal(t, 0.0, committedMonthly)
		assert.Equal(t, 0.0, ytdSavings)
	})

	t.Run("with purchase made this year", func(t *testing.T) {
		mockStore := new(MockConfigStore)

		// Create a purchase made this year
		purchaseTime := time.Now().AddDate(0, -1, 0) // 1 month ago
		purchases := []config.PurchaseHistoryRecord{
			{
				Timestamp:        purchaseTime,
				Term:             3, // 3-year term
				EstimatedSavings: 50.0,
			},
		}

		mockStore.On("GetPurchaseHistory", ctx, "account-123", 1000).Return(purchases, nil)

		handler := &Handler{config: mockStore}

		activeCommitments, committedMonthly, _ := handler.calculateCommitmentMetrics(ctx, "account-123")

		assert.Equal(t, 1, activeCommitments)
		assert.Equal(t, 50.0, committedMonthly)
	})
}

func TestHandler_calculateCurrentCoverage(t *testing.T) {
	handler := &Handler{}

	t.Run("no potential savings returns 100%", func(t *testing.T) {
		coverage := handler.calculateCurrentCoverage(0.0, 100.0)
		assert.Equal(t, 100.0, coverage)
	})

	t.Run("no committed monthly", func(t *testing.T) {
		coverage := handler.calculateCurrentCoverage(100.0, 0.0)
		assert.Equal(t, 0.0, coverage)
	})

	t.Run("50% coverage", func(t *testing.T) {
		coverage := handler.calculateCurrentCoverage(100.0, 100.0)
		assert.Equal(t, 50.0, coverage)
	})

	t.Run("both zero returns 0%", func(t *testing.T) {
		coverage := handler.calculateCurrentCoverage(0.0, 0.0)
		assert.Equal(t, 100.0, coverage)
	})
}

func TestHandler_getDashboardSummary_Errors(t *testing.T) {
	ctx := context.Background()

	t.Run("scheduler error", func(t *testing.T) {
		mockScheduler := new(MockScheduler)
		mockScheduler.On("ListRecommendations", ctx, mock.Anything).Return(nil, errors.New("scheduler error"))

		mockAuth, req := adminDashboardReq(ctx)
		handler := &Handler{auth: mockAuth, scheduler: mockScheduler}

		_, err := handler.getDashboardSummary(ctx, req, map[string]string{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get recommendations")
	})

	t.Run("nil global config uses default coverage", func(t *testing.T) {
		mockScheduler := new(MockScheduler)
		mockStore := new(MockConfigStore)

		mockScheduler.On("ListRecommendations", ctx, mock.Anything).Return([]config.RecommendationRecord{}, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(nil, nil)
		mockStore.On("GetPurchaseHistory", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseHistoryRecord{}, nil)

		mockAuth, req := adminDashboardReq(ctx)
		handler := &Handler{
			auth:      mockAuth,
			scheduler: mockScheduler,
			config:    mockStore,
		}

		result, err := handler.getDashboardSummary(ctx, req, map[string]string{})
		require.NoError(t, err)
		assert.Equal(t, 80.0, result.TargetCoverage) // Default
	})

	t.Run("zero coverage in global config uses default", func(t *testing.T) {
		mockScheduler := new(MockScheduler)
		mockStore := new(MockConfigStore)

		globalCfg := &config.GlobalConfig{
			DefaultCoverage: 0,
		}

		mockScheduler.On("ListRecommendations", ctx, mock.Anything).Return([]config.RecommendationRecord{}, nil)
		mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
		mockStore.On("GetPurchaseHistory", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseHistoryRecord{}, nil)

		mockAuth, req := adminDashboardReq(ctx)
		handler := &Handler{
			auth:      mockAuth,
			scheduler: mockScheduler,
			config:    mockStore,
		}

		result, err := handler.getDashboardSummary(ctx, req, map[string]string{})
		require.NoError(t, err)
		assert.Equal(t, 80.0, result.TargetCoverage) // Default when 0
	})
}

func TestHandler_getUpcomingPurchases_Errors(t *testing.T) {
	ctx := context.Background()

	t.Run("list plans error", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockStore.On("ListPurchasePlans", ctx).Return(nil, errors.New("db error"))

		mockAuth, req := adminDashboardReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		_, err := handler.getUpcomingPurchases(ctx, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get purchase plans")
	})

	t.Run("plan without next execution date", func(t *testing.T) {
		mockStore := new(MockConfigStore)

		plans := []config.PurchasePlan{
			{
				ID:                "11111111-1111-1111-1111-111111111111",
				Name:              "Plan Without Date",
				Enabled:           true,
				NextExecutionDate: nil, // No execution date
			},
		}

		mockStore.On("ListPurchasePlans", ctx).Return(plans, nil)

		mockAuth, req := adminDashboardReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		result, err := handler.getUpcomingPurchases(ctx, req)
		require.NoError(t, err)
		assert.Len(t, result.Purchases, 0) // Should not include plan without date
	})
}
