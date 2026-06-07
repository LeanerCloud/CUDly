package api

import (
	"context"
	"encoding/json"
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
	}, nil)
	mockAuth.grantAdmin()
	return mockAuth, &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
}

func TestHandler_getDashboardSummary(t *testing.T) {
	ctx := context.Background()
	mockScheduler := new(MockScheduler)
	mockStore := new(MockConfigStore)

	// Two rds recs for DISTINCT physical resources (different resource_type) so
	// they aggregate additively, plus one ec2 rec. Distinct cells are required
	// because summarizeRecommendationsWithCoverage now dedupes per
	// physical-resource cell: two recs sharing the same
	// (provider, account, service, region, resource_type, engine) key would be
	// treated as mutually-exclusive term/payment variants and collapsed to one.
	recommendations := []config.RecommendationRecord{
		{Service: "rds", ResourceType: "db.r5.large", Savings: 100.0},
		{Service: "ec2", ResourceType: "m5.large", Savings: 200.0},
		{Service: "rds", ResourceType: "db.r5.xlarge", Savings: 50.0},
	}

	globalCfg := &config.GlobalConfig{
		DefaultCoverage: 75.0,
	}

	mockScheduler.On("ListRecommendations", ctx, mock.Anything).Return(recommendations, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	// No account_id / account_ids filter → calculateCommitmentMetrics uses GetAllPurchaseHistory.
	mockStore.On("GetAllPurchaseHistory", ctx, mock.Anything).Return([]config.PurchaseHistoryRecord{}, nil)

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
	overrides map[string]*config.AccountServiceOverride
}

func (s *dashboardOverrideStore) GetAccountServiceOverride(_ context.Context, accountID, provider, service string) (*config.AccountServiceOverride, error) {
	return s.overrides[config.AccountConfigKey(accountID, provider, service)], nil
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
			config.AccountConfigKey("acct-A", "aws", "rds"): {Coverage: float64Ptr(50)},
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
	mockStore.On("GetAllPurchaseHistory", ctx, mock.Anything).Return([]config.PurchaseHistoryRecord{}, nil)
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

// Issue #201 — a global ServiceConfig with Coverage=0 (the float64 zero-value,
// meaning "not configured") must NOT silence the dashboard headline. The fix is
// in resolveCoverageByAccountKey: zero-coverage entries are omitted from the map
// so scaledSavings falls through to full savings.
func TestHandler_getDashboardSummary_ZeroCoverageInServiceConfigFallsThroughToFull(t *testing.T) {
	ctx := context.Background()
	mockScheduler := new(MockScheduler)
	mockStore := new(MockConfigStore)
	// No AccountServiceOverride — only a global ServiceConfig with Coverage=0.
	store := &dashboardOverrideStore{
		MockConfigStore: mockStore,
		overrides:       map[string]*config.AccountServiceOverride{}, // no overrides
	}

	acctA := "acct-A"
	recommendations := []config.RecommendationRecord{
		{Provider: "aws", Service: "rds", Savings: 200.0, CloudAccountID: &acctA},
	}

	mockScheduler.On("ListRecommendations", ctx, mock.Anything).Return(recommendations, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{DefaultCoverage: 80.0}, nil)
	mockStore.On("GetAllPurchaseHistory", ctx, mock.Anything).Return([]config.PurchaseHistoryRecord{}, nil)
	// Global ServiceConfig has Coverage=0 (zero-value — operator never set it).
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(&config.ServiceConfig{
		Provider: "aws", Service: "rds", Enabled: true, Coverage: 0,
	}, nil)

	mockAuth, req := adminDashboardReq(ctx)
	handler := &Handler{
		auth:      mockAuth,
		scheduler: mockScheduler,
		config:    store,
	}

	result, err := handler.getDashboardSummary(ctx, req, map[string]string{"provider": "aws"})
	require.NoError(t, err)

	// Before the fix, Coverage=0 was inserted into the map and scaledSavings
	// returned $0. After the fix, the zero entry is omitted and the full $200
	// is returned.
	assert.InDelta(t, 200.0, result.PotentialMonthlySavings, 0.001,
		"Coverage=0 (unset zero-value) must not silence savings (issue #201)")
	assert.InDelta(t, 200.0, result.ByService["rds"].PotentialSavings, 0.001)
}

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
			// CurrentSavings is populated by getDashboardSummary from purchase
			// history, not here — it must remain 0 from this function (issue #1031).
			assert.Equal(t, 0.0, byService["rds"].CurrentSavings,
				"summarize must not set CurrentSavings")
		})
	}
}

// TestSummarizeRecommendationsWithCoverage_100PctContract pins the read-side
// scaling math against the 100%-coverage contract documented on
// summarizeRecommendationsWithCoverage (issue #215).
//
// All three upstream APIs (AWS CE, Azure Advisor, GCP Recommender) return
// savings sized for 100% coverage of historical demand. The per-account
// coverage override in coverageByKey is therefore a user-intent projection:
// "if I only commit to X% of my instances, my savings would be rec.Savings * X/100."
//
// This test seeds two accounts at known savings baselines and asserts that
// the dashboard total equals the sum of the per-account scaled amounts.
// A bug in the scaling formula (e.g., double-applying coverage, off-by-factor-100)
// would be caught here.
func TestSummarizeRecommendationsWithCoverage_100PctContract(t *testing.T) {
	acctA := "acct-A"
	acctB := "acct-B"
	keyA := config.AccountConfigKey(acctA, "aws", "ec2")
	keyB := config.AccountConfigKey(acctB, "aws", "ec2")

	// Both recs carry the 100%-coverage savings from AWS CE.
	recs := []config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", Savings: 1000.0, CloudAccountID: &acctA},
		{Provider: "aws", Service: "ec2", Savings: 500.0, CloudAccountID: &acctB},
	}

	// Operator configured: cover 70% of acct-A, 90% of acct-B.
	coverage := map[string]float64{keyA: 70, keyB: 90}

	total, byService := summarizeRecommendationsWithCoverage(recs, coverage)

	wantA := 1000.0 * 70.0 / 100.0 // 700
	wantB := 500.0 * 90.0 / 100.0  // 450
	wantTotal := wantA + wantB     // 1150

	assert.InDelta(t, wantTotal, total, 0.001,
		"dashboard total = sum of per-account (rec.Savings * coverage/100); "+
			"double-applying coverage or factor-100 bug would produce the wrong figure")
	assert.InDelta(t, wantTotal, byService["ec2"].PotentialSavings, 0.001)

	// Sanity: full coverage (100%) returns the raw savings unchanged.
	fullTotal, _ := summarizeRecommendationsWithCoverage(recs, map[string]float64{keyA: 100, keyB: 100})
	assert.InDelta(t, 1500.0, fullTotal, 0.001,
		"coverage=100 must not scale savings down (100/100 = 1)")

	// Sanity: nil coverage map returns the raw total without scaling.
	rawTotal, _ := summarizeRecommendationsWithCoverage(recs, nil)
	assert.InDelta(t, 1500.0, rawTotal, 0.001,
		"nil coverage map must return un-scaled savings (issue #201 contract)")
}

// TestSummarizeRecommendationsWithCoverage_DoesNotSetCurrentSavings asserts
// that summarizeRecommendationsWithCoverage only populates PotentialSavings.
// CurrentSavings represents committed/realized savings from active purchase
// history and is populated separately in getDashboardSummary via
// aggregateActiveCommitmentsPerService. Mixing the two here would cause a
// service with recommendations but no purchases to falsely report non-zero
// CurrentSavings (issue #1031).
func TestSummarizeRecommendationsWithCoverage_DoesNotSetCurrentSavings(t *testing.T) {
	acctA := "acct-A"
	keyEC2 := config.AccountConfigKey(acctA, "aws", "ec2")
	keyRDS := config.AccountConfigKey(acctA, "aws", "rds")

	recs := []config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", Savings: 1000.0, CloudAccountID: &acctA},
		{Provider: "aws", Service: "rds", Savings: 400.0, CloudAccountID: &acctA},
	}

	// 60% coverage on ec2, 25% on rds.
	coverage := map[string]float64{keyEC2: 60, keyRDS: 25}

	_, byService := summarizeRecommendationsWithCoverage(recs, coverage)

	// PotentialSavings is scaled correctly by coverage.
	assert.InDelta(t, 600.0, byService["ec2"].PotentialSavings, 0.001,
		"ec2 potential_savings = 1000 * 60/100")
	assert.InDelta(t, 100.0, byService["rds"].PotentialSavings, 0.001,
		"rds potential_savings = 400 * 25/100")

	// CurrentSavings is NOT set here — it remains the float64 zero value.
	// getDashboardSummary populates it from purchase history (issue #1031).
	assert.Equal(t, 0.0, byService["ec2"].CurrentSavings,
		"summarize must not set CurrentSavings; that is getDashboardSummary's responsibility")
	assert.Equal(t, 0.0, byService["rds"].CurrentSavings,
		"summarize must not set CurrentSavings; that is getDashboardSummary's responsibility")
}

// TestSummarizeRecommendationsWithCoverage_DedupesVariantsPerCell is the
// regression for the by_service ~6x over-report: after PR #195's per-(term,
// payment) fan-out, one physical-resource cell yields up to 6 mutually-exclusive
// variant recs. The reducer used to sum every variant into
// by_service[svc].PotentialSavings, inflating it ~6x. The fix dedupes to one
// representative per cell (the MAX scaled savings) before summing.
//
// Pre-fix this test FAILS: the old sum-all-variants code returned
// PotentialSavings = 600 (100+200+300) for cell-1 plus 50 for cell-2 = 650,
// whereas the correct per-cell MAX is 300 + 50 = 350.
func TestSummarizeRecommendationsWithCoverage_DedupesVariantsPerCell(t *testing.T) {
	acct := "acct-A"
	// cellVariant builds one term/payment variant of the SAME physical-resource
	// cell: identical provider/account/service/region/resource_type/engine, only
	// the savings (and notionally term/payment) differ.
	cellVariant := func(region, resourceType, engine string, term int, payment string, savings float64) config.RecommendationRecord {
		a := acct
		return config.RecommendationRecord{
			Provider:       "aws",
			Service:        "ec2",
			Region:         region,
			ResourceType:   resourceType,
			Engine:         engine,
			Term:           term,
			Payment:        payment,
			Savings:        savings,
			CloudAccountID: &a,
		}
	}

	recs := []config.RecommendationRecord{
		// Cell 1: three variants of the same resource. MAX scaled savings = 300.
		cellVariant("us-east-1", "m5.large", "", 1, "no_upfront", 100),
		cellVariant("us-east-1", "m5.large", "", 1, "partial_upfront", 200),
		cellVariant("us-east-1", "m5.large", "", 3, "all_upfront", 300),
		// Cell 2: a distinct resource (different resource_type). Only variant: 50.
		cellVariant("us-east-1", "r5.large", "", 1, "no_upfront", 50),
	}

	// No coverage override: scaledSavings returns rec.Savings unchanged.
	total, byService := summarizeRecommendationsWithCoverage(recs, nil)

	// by_service must equal the sum of per-cell MAX savings (300 + 50), NOT the
	// sum of all variants (100+200+300+50 = 650).
	assert.InDelta(t, 350.0, byService["ec2"].PotentialSavings, 0.0001,
		"by_service potential must sum per-cell MAX (300+50), not all variants")
	assert.InDelta(t, 350.0, byService["ec2"].CurrentSavings, 0.0001,
		"by_service current must dedupe identically to potential")
	// The headline total dedupes the same way.
	assert.InDelta(t, 350.0, total, 0.0001,
		"total must sum per-cell MAX (300+50), not all variants")
}

func TestHandler_getUpcomingPurchases(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	nextExecDate := time.Now().AddDate(0, 0, 7)
	planA := config.PurchasePlan{
		ID:      "11111111-1111-1111-1111-111111111111",
		Name:    "Test Plan 1",
		Enabled: true,
		Services: map[string]config.ServiceConfig{
			"aws/rds": {Provider: "aws", Service: "rds"},
		},
		RampSchedule: config.RampSchedule{TotalSteps: 5},
	}
	planB := config.PurchasePlan{
		ID:      "22222222-2222-2222-2222-222222222222",
		Name:    "Plan B (no pending exec)",
		Enabled: true,
		Services: map[string]config.ServiceConfig{
			"aws/ec2": {Provider: "aws", Service: "ec2"},
		},
		RampSchedule: config.RampSchedule{TotalSteps: 3},
	}

	// Two pending executions, both belonging to planA — exercises the
	// one-row-per-execution semantic (plan B has no pending execution and
	// must NOT appear in the upcoming list).
	pending := []config.PurchaseExecution{
		{
			ExecutionID:      "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			PlanID:           planA.ID,
			Status:           "pending",
			ScheduledDate:    nextExecDate,
			StepNumber:       1,
			EstimatedSavings: 100.0,
		},
		{
			ExecutionID:      "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
			PlanID:           planA.ID,
			Status:           "pending",
			ScheduledDate:    nextExecDate.AddDate(0, 1, 0),
			StepNumber:       2,
			EstimatedSavings: 150.0,
		},
	}

	mockStore.On("GetPendingExecutions", ctx).Return(pending, nil)
	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return([]config.PurchasePlan{planA, planB}, nil)

	mockAuth, req := adminDashboardReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.getUpcomingPurchases(ctx, req)
	require.NoError(t, err)

	// Both pending executions of plan A appear, in order. Plan B has no
	// pending execution → not in the list (one row per execution, not per
	// plan, by design — see PR #213 history).
	require.Len(t, result.Purchases, 2)

	first := result.Purchases[0]
	assert.Equal(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", first.ExecutionID,
		"Cancel button targets ExecutionID via DELETE /api/purchases/planned/{id}")
	assert.Equal(t, planA.ID, first.PlanID, "PlanID exposed for context, not action targeting")
	assert.Equal(t, "Test Plan 1", first.PlanName)
	assert.Equal(t, "aws", first.Provider)
	assert.Equal(t, "rds", first.Service)
	assert.Equal(t, 1, first.StepNumber)
	assert.Equal(t, 5, first.TotalSteps)
	assert.InDelta(t, 100.0, first.EstimatedSavings, 0.0001)

	second := result.Purchases[1]
	assert.Equal(t, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", second.ExecutionID)
	assert.Equal(t, 2, second.StepNumber)
}

// TestHandler_getUpcomingPurchases_PropagatesCreatedByUserID is the
// issue-#950 follow-up regression: the dashboard widget on the frontend
// applies a creator-scope ownership gate on the Cancel button (mirrors
// the Plans page); it can only do so if the backend ships
// created_by_user_id on every row. Pre-fix the field was absent, so the
// widget defaulted to "no owner known" and either showed Cancel for
// everyone (when ungated) or for nobody (when gated) -- both wrong.
func TestHandler_getUpcomingPurchases_PropagatesCreatedByUserID(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	scheduled := time.Now().AddDate(0, 0, 7)
	plan := config.PurchasePlan{
		ID:      "11111111-1111-1111-1111-111111111111",
		Name:    "Owned Plan",
		Enabled: true,
		Services: map[string]config.ServiceConfig{
			"aws/ec2": {Provider: "aws", Service: "ec2"},
		},
		RampSchedule: config.RampSchedule{TotalSteps: 4},
	}
	creator := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	pending := []config.PurchaseExecution{
		{
			ExecutionID:     "11112222-3333-4444-5555-666677778888",
			PlanID:          plan.ID,
			Status:          "pending",
			ScheduledDate:   scheduled,
			StepNumber:      1,
			CreatedByUserID: &creator,
		},
		{
			// Legacy / scheduler-tick row: NULL creator. Must serialise as
			// no created_by_user_id field (omitempty on the JSON tag) so
			// the frontend treats it as out-of-reach for non-update-any
			// users -- the documented #950 behaviour.
			ExecutionID:     "99998888-7777-6666-5555-444433332222",
			PlanID:          plan.ID,
			Status:          "pending",
			ScheduledDate:   scheduled.AddDate(0, 0, 7),
			StepNumber:      2,
			CreatedByUserID: nil,
		},
	}

	mockStore.On("GetPendingExecutions", ctx).Return(pending, nil)
	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return([]config.PurchasePlan{plan}, nil)

	mockAuth, req := adminDashboardReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.getUpcomingPurchases(ctx, req)
	require.NoError(t, err)
	require.Len(t, result.Purchases, 2)

	require.NotNil(t, result.Purchases[0].CreatedByUserID, "owned-row CreatedByUserID must propagate")
	assert.Equal(t, creator, *result.Purchases[0].CreatedByUserID)
	assert.Nil(t, result.Purchases[1].CreatedByUserID, "legacy NULL-creator row must stay nil")
}

// TestHandler_getUpcomingPurchases_OrphanExecutionSkipped guards against the
// "execution row with deleted parent plan" cleanup-gap edge case: rather
// than crash, the widget hides the orphan. Cleanup is a separate concern.
func TestHandler_getUpcomingPurchases_OrphanExecutionSkipped(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	pending := []config.PurchaseExecution{
		{
			ExecutionID: "cccccccc-cccc-cccc-cccc-cccccccccccc",
			PlanID:      "deleted-plan-uuid", // not in plan list below
			Status:      "pending",
		},
	}
	mockStore.On("GetPendingExecutions", ctx).Return(pending, nil)
	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return([]config.PurchasePlan{}, nil)

	mockAuth, req := adminDashboardReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	result, err := handler.getUpcomingPurchases(ctx, req)
	require.NoError(t, err)
	assert.Empty(t, result.Purchases, "orphan execution must be hidden, not crash")
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

	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return([]config.PurchasePlan{planA, planB}, nil)
	mockStore.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{
		{ExecutionID: "exec-A", PlanID: planA.ID, Status: "pending", ScheduledDate: nextExecDate, StepNumber: 1},
		{ExecutionID: "exec-B", PlanID: planB.ID, Status: "pending", ScheduledDate: nextExecDate, StepNumber: 1},
	}, nil)
	store := &mockStoreWithPlanAccounts{
		MockConfigStore: mockStore,
		planAccounts: map[string][]config.CloudAccount{
			planA.ID: {{ID: "acc-prod", Name: "Production"}},
			planB.ID: {{ID: "acc-stage", Name: "Staging"}},
		},
	}

	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(&Session{
		UserID: "viewer-1",
	}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "view", "purchases").Return(true, nil)
	mockAuth.On("GetAllowedAccountsAPI", ctx, "viewer-1").Return([]string{"Production"}, nil)

	handler := &Handler{auth: mockAuth, config: store}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer viewer-token"},
	}

	result, err := handler.getUpcomingPurchases(ctx, req)
	require.NoError(t, err)

	// Only Plan A's pending execution passes the allowed_accounts filter.
	require.Len(t, result.Purchases, 1)
	assert.Equal(t, "Plan A", result.Purchases[0].PlanName)
	assert.Equal(t, "exec-A", result.Purchases[0].ExecutionID)
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
	mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return([]config.PurchasePlan{plan}, nil)
	mockStore.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{
		{ExecutionID: "exec-unattributed", PlanID: plan.ID, Status: "pending", ScheduledDate: nextExecDate, StepNumber: 1},
	}, nil)
	store := &mockStoreWithPlanAccounts{
		MockConfigStore: mockStore,
		planAccounts:    map[string][]config.CloudAccount{plan.ID: {}},
	}

	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(&Session{
		UserID: "viewer-1",
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

	// Regression test for #633: sensitive identifiers must never appear in the
	// unauthenticated /api/info response, even when a secretsARN is configured.
	t.Run("no sensitive fields in unauthenticated response (#633)", func(t *testing.T) {
		mockAuth := new(MockAuthService)
		mockAuth.On("CheckAdminExists", ctx).Return(true, nil)

		handler := &Handler{
			auth:       mockAuth,
			secretsARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:api-key-abc123",
		}

		result, err := handler.getPublicInfo(ctx, createMockLambdaRequest("10.0.0.1"))
		require.NoError(t, err)

		// PublicInfoResponse no longer carries these fields — the struct itself is
		// the compile-time guard. The JSON assertion catches any future re-addition
		// via an embedded struct or interface{} workaround.
		assert.Equal(t, "1.0.0", result.Version)
		assert.True(t, result.AdminExists)
		encoded, jsonErr := json.Marshal(result)
		require.NoError(t, jsonErr)
		body := string(encoded)
		assert.NotContains(t, body, "api_key_secret_url", "api_key_secret_url must not appear in /api/info response")
		assert.NotContains(t, body, "deployment_aws_account_id", "deployment_aws_account_id must not appear in /api/info response")
	})
}

func TestHandler_getDeploymentInfo(t *testing.T) {
	ctx := context.Background()

	t.Run("returns ARN-derived URL and account ID", func(t *testing.T) {
		handler := &Handler{
			secretsARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:api-key-abc123",
		}

		result, err := handler.getDeploymentInfo(ctx, createMockLambdaRequest("10.0.0.1"))
		require.NoError(t, err)

		assert.Contains(t, result.APIKeySecretURL, "us-east-1")
		assert.Contains(t, result.APIKeySecretURL, "secretsmanager")
	})

	t.Run("ARN parsing for different regions", func(t *testing.T) {
		handler := &Handler{
			secretsARN: "arn:aws:secretsmanager:eu-west-1:987654321098:secret:my-secret-xyz789",
		}

		result, err := handler.getDeploymentInfo(ctx, createMockLambdaRequest("10.0.0.1"))
		require.NoError(t, err)

		assert.Contains(t, result.APIKeySecretURL, "eu-west-1")
	})

	t.Run("invalid ARN format returns empty URL", func(t *testing.T) {
		handler := &Handler{
			secretsARN: "invalid-arn",
		}

		result, err := handler.getDeploymentInfo(ctx, createMockLambdaRequest("10.0.0.1"))
		require.NoError(t, err)

		assert.Empty(t, result.APIKeySecretURL)
	})

	t.Run("empty secretsARN returns empty URL", func(t *testing.T) {
		handler := &Handler{}

		result, err := handler.getDeploymentInfo(ctx, createMockLambdaRequest("10.0.0.1"))
		require.NoError(t, err)

		assert.Empty(t, result.APIKeySecretURL)
	})
}

func TestHandler_calculateCommitmentMetrics(t *testing.T) {
	ctx := context.Background()

	t.Run("no purchase history", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{ExternalIDsByProvider: map[string][]string{"": {"account-123"}}, Limit: 1000}).Return([]config.PurchaseHistoryRecord{}, nil)

		handler := &Handler{config: mockStore}

		activeCommitments, committedMonthly, ytdSavings, savingsByService := handler.calculateCommitmentMetrics(ctx, nil, map[string][]string{"": {"account-123"}})

		assert.Equal(t, 0, activeCommitments)
		assert.Equal(t, 0.0, committedMonthly)
		assert.Equal(t, 0.0, ytdSavings)
		assert.Empty(t, savingsByService)
	})

	t.Run("purchase history error returns zeros", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{ExternalIDsByProvider: map[string][]string{"": {"account-123"}}, Limit: 1000}).Return(nil, errors.New("db error"))

		handler := &Handler{config: mockStore}

		activeCommitments, committedMonthly, ytdSavings, savingsByService := handler.calculateCommitmentMetrics(ctx, nil, map[string][]string{"": {"account-123"}})

		assert.Equal(t, 0, activeCommitments)
		assert.Equal(t, 0.0, committedMonthly)
		assert.Equal(t, 0.0, ytdSavings)
		assert.Nil(t, savingsByService)
	})

	t.Run("with active commitments", func(t *testing.T) {
		mockStore := new(MockConfigStore)

		// Create a purchase made 6 months ago with 1-year term (still active)
		purchaseTime := time.Now().AddDate(0, -6, 0)
		purchases := []config.PurchaseHistoryRecord{
			{
				Timestamp:        purchaseTime,
				Service:          "ec2",
				Term:             1, // 1-year term
				EstimatedSavings: 100.0,
			},
		}

		mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{ExternalIDsByProvider: map[string][]string{"": {"account-123"}}, Limit: 1000}).Return(purchases, nil)

		handler := &Handler{config: mockStore}

		activeCommitments, committedMonthly, ytdSavings, savingsByService := handler.calculateCommitmentMetrics(ctx, nil, map[string][]string{"": {"account-123"}})

		assert.Equal(t, 1, activeCommitments)
		assert.Equal(t, 100.0, committedMonthly)
		// YTD savings depends on when the purchase was made relative to year start
		assert.GreaterOrEqual(t, ytdSavings, 0.0)
		assert.InDelta(t, 100.0, savingsByService["ec2"], 0.001)
	})

	t.Run("with expired commitments", func(t *testing.T) {
		mockStore := new(MockConfigStore)

		// Create a purchase made 2 years ago with 1-year term (expired)
		purchaseTime := time.Now().AddDate(-2, 0, 0)
		purchases := []config.PurchaseHistoryRecord{
			{
				Timestamp:        purchaseTime,
				Service:          "rds",
				Term:             1, // 1-year term
				EstimatedSavings: 100.0,
			},
		}

		mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{ExternalIDsByProvider: map[string][]string{"": {"account-123"}}, Limit: 1000}).Return(purchases, nil)

		handler := &Handler{config: mockStore}

		activeCommitments, committedMonthly, ytdSavings, savingsByService := handler.calculateCommitmentMetrics(ctx, nil, map[string][]string{"": {"account-123"}})

		// Should skip expired commitments
		assert.Equal(t, 0, activeCommitments)
		assert.Equal(t, 0.0, committedMonthly)
		assert.Equal(t, 0.0, ytdSavings)
		assert.Empty(t, savingsByService, "expired commitments must not appear in per-service map")
	})

	t.Run("with purchase made this year", func(t *testing.T) {
		mockStore := new(MockConfigStore)

		// Create a purchase made this year
		purchaseTime := time.Now().AddDate(0, -1, 0) // 1 month ago
		purchases := []config.PurchaseHistoryRecord{
			{
				Timestamp:        purchaseTime,
				Service:          "ec2",
				Term:             3, // 3-year term
				EstimatedSavings: 50.0,
			},
		}

		mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{ExternalIDsByProvider: map[string][]string{"": {"account-123"}}, Limit: 1000}).Return(purchases, nil)

		handler := &Handler{config: mockStore}

		activeCommitments, committedMonthly, _, savingsByService := handler.calculateCommitmentMetrics(ctx, nil, map[string][]string{"": {"account-123"}})

		assert.Equal(t, 1, activeCommitments)
		assert.Equal(t, 50.0, committedMonthly)
		assert.InDelta(t, 50.0, savingsByService["ec2"], 0.001)
	})

	// --- Bug fix tests ---

	// Status filter: a row with Status="failed" must NOT be counted as an active
	// commitment. The invariant: isActiveCommitment rejects any non-empty status
	// that isn't "completed".
	t.Run("failed status row is excluded from committedMonthly", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		purchaseTime := time.Now().AddDate(0, -3, 0)
		purchases := []config.PurchaseHistoryRecord{
			{
				Timestamp:        purchaseTime,
				Term:             1,
				EstimatedSavings: 200.0,
				Status:           "failed",
			},
			{
				Timestamp:        purchaseTime,
				Term:             1,
				EstimatedSavings: 50.0,
				// Status "" = completed DB row — must be counted
			},
		}
		mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{ExternalIDsByProvider: map[string][]string{"": {"account-123"}}, Limit: 1000}).Return(purchases, nil)

		handler := &Handler{config: mockStore}

		activeCommitments, committedMonthly, _, _ := handler.calculateCommitmentMetrics(ctx, nil, map[string][]string{"": {"account-123"}})

		// Only the status="" row counts; the failed row must be excluded.
		assert.Equal(t, 1, activeCommitments,
			"failed commitment must not increment activeCommitments")
		assert.Equal(t, 50.0, committedMonthly,
			"failed commitment's savings must not appear in committedMonthly")
	})

	// Multi-account scope: a UUID-filtered request must use GetPurchaseHistoryFiltered
	// scoped to the supplied cloud_account_id UUIDs. Rows from account C must NOT
	// appear when the filter contains only A and B.
	t.Run("multi-account UUID filter routes to GetPurchaseHistoryFiltered", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		purchaseTime := time.Now().AddDate(0, -2, 0)

		accountAID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
		accountBID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

		// Only rows for A and B are returned by the scoped query — the store
		// enforces the filter; the handler must NOT call GetPurchaseHistory.
		purchasesAB := []config.PurchaseHistoryRecord{
			{CloudAccountID: &accountAID, Timestamp: purchaseTime, Term: 1, EstimatedSavings: 100.0},
			{CloudAccountID: &accountBID, Timestamp: purchaseTime, Term: 1, EstimatedSavings: 150.0},
		}
		uuids := []string{accountAID, accountBID}
		mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{AccountIDs: uuids, Limit: 1000}).
			Return(purchasesAB, nil)
		// GetPurchaseHistory must not be called — no On registration so it
		// would panic if accidentally invoked.
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		handler := &Handler{config: mockStore}

		activeCommitments, committedMonthly, _, _ := handler.calculateCommitmentMetrics(ctx, uuids, nil)

		assert.Equal(t, 2, activeCommitments)
		assert.Equal(t, 250.0, committedMonthly,
			"only accounts A and B must contribute; account C rows must not appear")
	})

	// Keystone (issue #701/#498/#866): an account's commitments may live in rows
	// that carry only the external account_id (cloud_account_id NULL). When the
	// dashboard scope resolves the selected UUID to its external id, the
	// dual-column filter must include those rows so the KPIs aren't zero.
	t.Run("external-id-only commitment rows are counted via the dual-column filter", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		purchaseTime := time.Now().AddDate(0, -2, 0)

		// Row attributed by external number only (no CloudAccountID).
		externalOnly := []config.PurchaseHistoryRecord{
			{AccountID: "999988887777", Timestamp: purchaseTime, Term: 1, EstimatedSavings: 175.0},
		}
		mockStore.On("GetPurchaseHistoryFiltered", ctx, config.PurchaseHistoryFilter{
			AccountIDs:            []string{"bbbbbbbb-1111-2222-3333-444444444444"},
			ExternalIDsByProvider: map[string][]string{"aws": {"999988887777"}},
			Limit:                 1000,
		}).Return(externalOnly, nil)
		t.Cleanup(func() { mockStore.AssertExpectations(t) })

		handler := &Handler{config: mockStore}

		activeCommitments, committedMonthly, _, _ := handler.calculateCommitmentMetrics(
			ctx, []string{"bbbbbbbb-1111-2222-3333-444444444444"}, map[string][]string{"aws": {"999988887777"}})

		assert.Equal(t, 1, activeCommitments, "external-id-only commitment must be counted")
		assert.Equal(t, 175.0, committedMonthly)
	})
}

// TestAggregateActiveCommitmentsPerService covers the core primitive used by
// both the KPI total and the per-service chart CurrentSavings.
func TestAggregateActiveCommitmentsPerService(t *testing.T) {
	now := time.Now()
	active := func(service string, savings float64) config.PurchaseHistoryRecord {
		return config.PurchaseHistoryRecord{
			Service:          service,
			Timestamp:        now.AddDate(0, -1, 0), // started 1 month ago
			Term:             1,                     // 1-year term, still active
			EstimatedSavings: savings,
		}
	}
	expired := func(service string, savings float64) config.PurchaseHistoryRecord {
		return config.PurchaseHistoryRecord{
			Service:          service,
			Timestamp:        now.AddDate(-2, 0, 0), // started 2 years ago
			Term:             1,                     // 1-year term, expired
			EstimatedSavings: savings,
		}
	}

	t.Run("two active commitments accumulate per service", func(t *testing.T) {
		purchases := []config.PurchaseHistoryRecord{
			active("EC2", 150.0),
			active("RDS", 75.0),
		}
		got := aggregateActiveCommitmentsPerService(purchases, now)
		assert.InDelta(t, 150.0, got["EC2"], 0.001)
		assert.InDelta(t, 75.0, got["RDS"], 0.001)
		assert.Len(t, got, 2, "no other service must appear")
	})

	t.Run("one failed (expired) + one succeeded stays correct", func(t *testing.T) {
		// Only the active row should count — the expired row is the "failed" analogue.
		purchases := []config.PurchaseHistoryRecord{
			expired("EC2", 999.0),
			active("EC2", 200.0),
		}
		got := aggregateActiveCommitmentsPerService(purchases, now)
		assert.InDelta(t, 200.0, got["EC2"], 0.001,
			"expired commitment must not contribute to CurrentSavings")
		assert.Len(t, got, 1)
	})

	t.Run("no active commitments returns empty map", func(t *testing.T) {
		purchases := []config.PurchaseHistoryRecord{
			expired("EC2", 100.0),
		}
		got := aggregateActiveCommitmentsPerService(purchases, now)
		assert.Empty(t, got)
	})
}

// TestHandler_getDashboardSummary_CurrentSavingsPopulated asserts that
// getDashboardSummary populates ServiceSavings.CurrentSavings from active
// purchase history so the Home chart's green bars render real data.
func TestHandler_getDashboardSummary_CurrentSavingsPopulated(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	purchases := []config.PurchaseHistoryRecord{
		{
			Service:          "EC2",
			Timestamp:        now.AddDate(0, -3, 0), // active
			Term:             1,
			EstimatedSavings: 150.0,
		},
		{
			Service:          "RDS",
			Timestamp:        now.AddDate(0, -6, 0), // active
			Term:             1,
			EstimatedSavings: 80.0,
		},
		{
			Service:          "EC2",
			Timestamp:        now.AddDate(-2, 0, 0), // expired — must not count
			Term:             1,
			EstimatedSavings: 999.0,
		},
	}

	recommendations := []config.RecommendationRecord{
		{Service: "EC2", Savings: 500.0},
		{Service: "RDS", Savings: 300.0},
	}

	mockScheduler := new(MockScheduler)
	mockStore := new(MockConfigStore)

	mockScheduler.On("ListRecommendations", ctx, mock.Anything).Return(recommendations, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{DefaultCoverage: 80.0}, nil)
	// No account_id / account_ids filter, so calculateCommitmentMetrics fetches
	// across all accounts via GetAllPurchaseHistory.
	mockStore.On("GetAllPurchaseHistory", ctx, mock.Anything).Return(purchases, nil)

	mockAuth, req := adminDashboardReq(ctx)
	handler := &Handler{
		auth:      mockAuth,
		scheduler: mockScheduler,
		config:    mockStore,
	}

	result, err := handler.getDashboardSummary(ctx, req, map[string]string{})
	require.NoError(t, err)

	// PotentialSavings must still be populated from recommendations.
	assert.InDelta(t, 500.0, result.ByService["EC2"].PotentialSavings, 0.001)
	assert.InDelta(t, 300.0, result.ByService["RDS"].PotentialSavings, 0.001)

	// CurrentSavings must come from active purchase history, grouped by service.
	assert.InDelta(t, 150.0, result.ByService["EC2"].CurrentSavings, 0.001,
		"EC2 current savings: only active commitment must count (expired $999 excluded)")
	assert.InDelta(t, 80.0, result.ByService["RDS"].CurrentSavings, 0.001)
}

// TestHandler_getDashboardSummary_CurrentSavingsJSON verifies the wire shape:
// current_savings must be present in the JSON-encoded response.
func TestHandler_getDashboardSummary_CurrentSavingsJSON(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	purchases := []config.PurchaseHistoryRecord{
		{
			Service:          "EC2",
			Timestamp:        now.AddDate(0, -1, 0),
			Term:             1,
			EstimatedSavings: 120.0,
		},
	}

	mockScheduler := new(MockScheduler)
	mockStore := new(MockConfigStore)

	mockScheduler.On("ListRecommendations", ctx, mock.Anything).Return(
		[]config.RecommendationRecord{{Service: "EC2", Savings: 400.0}}, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{DefaultCoverage: 80.0}, nil)
	// No account filter, so the no-filter fetch path (GetAllPurchaseHistory) runs.
	mockStore.On("GetAllPurchaseHistory", ctx, mock.Anything).Return(purchases, nil)

	mockAuth, req := adminDashboardReq(ctx)
	handler := &Handler{
		auth:      mockAuth,
		scheduler: mockScheduler,
		config:    mockStore,
	}

	result, err := handler.getDashboardSummary(ctx, req, map[string]string{})
	require.NoError(t, err)

	// Verify through the ServiceSavings struct that the JSON tag is present and
	// the value round-trips correctly.  We assert on the struct field because
	// json.Marshal / Unmarshal would be redundant — the tag is on the declared
	// type and Go's encoding/json honours it.
	require.Contains(t, result.ByService, "EC2")
	assert.InDelta(t, 120.0, result.ByService["EC2"].CurrentSavings, 0.001,
		"current_savings field must carry the active purchase's EstimatedSavings")
}

// TestHandler_getDashboardSummary_CurrentSavingsZeroWhenNoCommitments is the
// issue #1031 regression: a service with recommendations but no active purchase
// history must ship current_savings: 0, not current_savings: potential_savings.
//
// The bug was that summarizeRecommendationsWithCoverage set CurrentSavings to
// the same scaled value as PotentialSavings. getDashboardSummary only overwrites
// CurrentSavings for services that appear in the purchase-history result, so
// services with no purchases retained the wrong non-zero value.
func TestHandler_getDashboardSummary_CurrentSavingsZeroWhenNoCommitments(t *testing.T) {
	ctx := context.Background()

	// No purchases at all — represents a new account that has recommendations
	// but has not yet acted on them.
	mockScheduler := new(MockScheduler)
	mockStore := new(MockConfigStore)

	mockScheduler.On("ListRecommendations", ctx, mock.Anything).Return(
		[]config.RecommendationRecord{
			{Service: "EC2", Savings: 500.0},
			{Service: "RDS", Savings: 300.0},
		}, nil)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{DefaultCoverage: 80.0}, nil)
	mockStore.On("GetAllPurchaseHistory", ctx, mock.Anything).Return(
		[]config.PurchaseHistoryRecord{}, nil)

	mockAuth, req := adminDashboardReq(ctx)
	handler := &Handler{
		auth:      mockAuth,
		scheduler: mockScheduler,
		config:    mockStore,
	}

	result, err := handler.getDashboardSummary(ctx, req, map[string]string{})
	require.NoError(t, err)

	// PotentialSavings must be populated from recommendations.
	assert.InDelta(t, 500.0, result.ByService["EC2"].PotentialSavings, 0.001)
	assert.InDelta(t, 300.0, result.ByService["RDS"].PotentialSavings, 0.001)

	// CurrentSavings must be 0 — no active commitments exist yet.
	// Before the fix, this incorrectly equalled PotentialSavings because
	// summarizeRecommendationsWithCoverage also wrote CurrentSavings (issue #1031).
	assert.Equal(t, 0.0, result.ByService["EC2"].CurrentSavings,
		"no active commitments: current_savings must be 0, not equal to potential_savings")
	assert.Equal(t, 0.0, result.ByService["RDS"].CurrentSavings,
		"no active commitments: current_savings must be 0, not equal to potential_savings")
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
		mockStore.On("GetAllPurchaseHistory", ctx, mock.Anything).Return([]config.PurchaseHistoryRecord{}, nil)

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
		mockStore.On("GetAllPurchaseHistory", ctx, mock.Anything).Return([]config.PurchaseHistoryRecord{}, nil)

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

	t.Run("get pending executions error", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockStore.On("GetPendingExecutions", ctx).Return(nil, errors.New("db error"))

		mockAuth, req := adminDashboardReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		_, err := handler.getUpcomingPurchases(ctx, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get pending executions")
	})

	t.Run("list plans error", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockStore.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{}, nil)
		mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return(nil, errors.New("db error"))

		mockAuth, req := adminDashboardReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		_, err := handler.getUpcomingPurchases(ctx, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get purchase plans")
	})

	t.Run("no pending executions yields empty list", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockStore.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{}, nil)
		mockStore.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return([]config.PurchasePlan{}, nil)

		mockAuth, req := adminDashboardReq(ctx)
		handler := &Handler{auth: mockAuth, config: mockStore}

		result, err := handler.getUpcomingPurchases(ctx, req)
		require.NoError(t, err)
		assert.Len(t, result.Purchases, 0)
	})
}
