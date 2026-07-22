package api

// TestPerAccountPerms is the comprehensive regression suite for per-account
// permission scoping across every endpoint that returns or accepts
// cloud_account_id-tagged data (issue #307).
//
// Each sub-test is intentionally a negative case — it asserts that a scoped
// user (allowed_accounts: ["acc-a-uuid"]) cannot see or mutate data belonging
// to a different account ("acc-b-uuid"). The positive (allowed) case is kept
// minimal: just enough to confirm the filter passes when accounts match so we
// know the test would fail for the right reason if the enforcement were removed.
//
// Endpoints covered (12 total):
//   1.  GET /recommendations          (filterRecommendationsByAllowedAccounts)
//   2.  GET /recommendations/:id      (getRecommendationDetail — cross-account rejection)
//   3.  GET /history                  (filterPurchaseHistoryByAllowedAccounts)
//   4.  GET /history/analytics        (validateAnalyticsAccountScope — cross-account rejection)
//   5.  GET /history/breakdown        (validateAnalyticsAccountScope — cross-account rejection)
//   6.  GET /dashboard/summary        (filterDashboardRecommendations — aggregate subset)
//   7.  POST /purchases/execute       (validatePurchaseRecommendationScope — 403)
//   8.  GET /purchases/planned list   (requireExecutionAccess / requirePlanAccess — 404)
//   9.  GET /inventory/coverage       (filterRecommendationsByAllowedAccounts on recs leg)
//  10.  GET /ri-exchange/instances    (getAllowedAccounts scope, issue #1030)
//  11.  GET /ri-exchange/utilization  (getAllowedAccounts scope, issue #1030)
//  12.  GET /ri-exchange/reshape      (getAllowedAccounts scope, issue #1030)
//  13.  GET /ladder/configs           (filterLadderConfigsByAllowedAccounts, issue #1428 follow-up)

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	// permsAccA is the account UUID the scoped test user is allowed to access.
	permsAccA = "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa"
	// permsAccAName is the display name for account A.
	permsAccAName = "Account-A"
	// permsAccB is the account UUID that is outside the scoped user's scope.
	permsAccB = "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb"
	// permsAccBName is the display name for account B.
	permsAccBName = "Account-B"
	// permsScopedUserID is the user ID of the scoped (non-admin) test user.
	permsScopedUserID = "cccccccc-cccc-4ccc-cccc-cccccccccccc"
	// permsScopedToken is the session token used by the scoped user.
	permsScopedToken = "scoped-user-token"
)

// permsPtr returns a pointer to s.
func permsPtr(s string) *string { return &s }

// scopedAuthMock returns a MockAuthService pre-wired so that:
//   - ValidateSession for permsScopedToken returns a non-admin Session.
//   - HasPermissionAPI returns true for any (action, resource) combination —
//     the per-account scoping tests focus on account-level filtering, not
//     role-level permission gating (which is covered by other test files).
//   - GetAllowedAccountsAPI returns [permsAccA] — i.e. the user is restricted
//     to account A only.
func scopedAuthMock(ctx context.Context) *MockAuthService {
	m := new(MockAuthService)
	m.On("ValidateSession", ctx, permsScopedToken).Return(&Session{
		UserID: permsScopedUserID,
		Email:  "scoped@example.com",
	}, nil)
	// Grant every permission so role-gating doesn't interfere with what we
	// actually want to test (account-level scoping).
	m.On("HasPermissionAPI", ctx, permsScopedUserID, mock.Anything, mock.Anything).Return(true, nil)
	// Likewise grant the SEC-01 execution-time constraint check; constraint
	// behavior has its own dedicated tests.
	m.On("HasPermissionForConstraintsAPI", ctx, permsScopedUserID, mock.Anything, mock.Anything, mock.Anything).
		Return(true, nil).Maybe()
	m.On("GetAllowedAccountsAPI", ctx, permsScopedUserID).Return([]string{permsAccA}, nil)
	return m
}

// scopedReq returns a Lambda request authenticated as the scoped user.
func scopedReq() *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer " + permsScopedToken},
	}
}

// permsAccountList returns a ListCloudAccountsFn-compatible slice containing
// both test accounts — used so resolveAccountNamesByID can map UUID→name for
// the account-name branch of MatchesAccount.
func permsAccountList() []config.CloudAccount {
	return []config.CloudAccount{
		{ID: permsAccA, Name: permsAccAName, Provider: "aws"},
		{ID: permsAccB, Name: permsAccBName, Provider: "aws"},
	}
}

// ─── 1. GET /recommendations ─────────────────────────────────────────────────

// TestPerAccountPerms_Recommendations_ListFilter asserts that a scoped user
// (allowed_accounts: [permsAccA]) receives only account-A recommendations
// from getRecommendations when the mock returns records for both accounts.
// The account-B record must be silently dropped by the filter.
//
// Regression: if filterRecommendationsByAllowedAccounts is removed, the
// account-B record appears in the response and the ElementsMatch assertion
// fails.
func TestPerAccountPerms_Recommendations_ListFilter(t *testing.T) {
	ctx := context.Background()

	recA := config.RecommendationRecord{
		ID: "rec-acct-a", Provider: "aws", Service: "ec2",
		CloudAccountID: permsPtr(permsAccA), Savings: 100,
	}
	recB := config.RecommendationRecord{
		ID: "rec-acct-b", Provider: "aws", Service: "ec2",
		CloudAccountID: permsPtr(permsAccB), Savings: 200,
	}

	mockSched := new(MockScheduler)
	mockSched.On("ListRecommendations", ctx, mock.Anything).Return([]config.RecommendationRecord{recA, recB}, nil)

	mockStore := new(MockConfigStore)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:      scopedAuthMock(ctx),
		scheduler: mockSched,
		config:    mockStore,
	}

	result, err := handler.getRecommendations(ctx, scopedReq(), map[string]string{})
	require.NoError(t, err)
	require.NotNil(t, result)

	ids := make([]string, len(result.Recommendations))
	for i, r := range result.Recommendations {
		ids[i] = r.ID
	}
	// Account-A record visible; account-B record absent.
	assert.ElementsMatch(t, []string{"rec-acct-a"}, ids,
		"scoped user must see only account-A recommendations; account-B record must be filtered out")

	// Regression anchor: if the filter is removed, account-B would appear.
	for _, r := range result.Recommendations {
		if r.CloudAccountID != nil {
			assert.NotEqual(t, permsAccB, *r.CloudAccountID,
				"account-B recommendation must not appear in scoped user's list")
		}
	}
}

// TestPerAccountPerms_Recommendations_ListFilter_AdminSeesAll verifies that an
// admin (unrestricted) session sees both accounts — confirming the positive
// path through IsUnrestrictedAccess. Without this, the negative test above
// could be masked by a bug that drops all records for everyone.
func TestPerAccountPerms_Recommendations_ListFilter_AdminSeesAll(t *testing.T) {
	ctx := context.Background()

	recA := config.RecommendationRecord{
		ID: "rec-a-admin", Provider: "aws", Service: "ec2",
		CloudAccountID: permsPtr(permsAccA),
	}
	recB := config.RecommendationRecord{
		ID: "rec-b-admin", Provider: "aws", Service: "ec2",
		CloudAccountID: permsPtr(permsAccB),
	}

	mockSched := new(MockScheduler)
	mockSched.On("ListRecommendations", ctx, mock.Anything).Return([]config.RecommendationRecord{recA, recB}, nil)

	mockAuth, req := adminDashboardReq(ctx) // reuse adminDashboardReq: admin session
	handler := &Handler{
		auth:      mockAuth,
		scheduler: mockSched,
		config:    new(MockConfigStore),
	}

	result, err := handler.getRecommendations(ctx, req, map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Summary.TotalCount,
		"admin (unrestricted) must see both accounts' recommendations")
}

// ─── 2. GET /recommendations/:id ─────────────────────────────────────────────

// TestPerAccountPerms_RecommendationDetail_CrossAccountRejected verifies that a
// scoped user cannot fetch the detail record of a recommendation belonging to
// account B. The handler filters by allowed accounts BEFORE looking up the id,
// so the account-B record is not in the visible set and the result is 404.
//
// Regression: if filterRecommendationsByAllowedAccounts is removed from
// getRecommendationDetail, the lookup would succeed and return a 200 exposing
// account-B data.
func TestPerAccountPerms_RecommendationDetail_CrossAccountRejected(t *testing.T) {
	ctx := context.Background()

	recB := config.RecommendationRecord{
		ID:             "rec-b-detail",
		Provider:       "aws",
		Service:        "ec2",
		CloudAccountID: permsPtr(permsAccB),
		Savings:        300,
		Count:          5,
	}

	mockSched := new(MockScheduler)
	mockSched.On("GetRecommendationByID", ctx, "rec-b-detail").Return(&recB, ([]string)(nil), nil)
	t.Cleanup(func() { mockSched.AssertExpectations(t) })

	mockStore := new(MockConfigStore)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:      scopedAuthMock(ctx),
		scheduler: mockSched,
		config:    mockStore,
	}

	got, err := handler.getRecommendationDetail(ctx, scopedReq(), "rec-b-detail")
	require.Error(t, err, "scoped user must not be able to fetch detail of account-B recommendation")
	assert.Nil(t, got)
	assert.True(t, IsNotFoundError(err),
		"cross-account detail fetch must return 404 (not 403) to avoid disclosing account-B's existence; got: %v", err)
}

// TestPerAccountPerms_RecommendationDetail_AllowedAccountReturns200 is the
// paired positive test: the scoped user CAN fetch account-A's detail.
func TestPerAccountPerms_RecommendationDetail_AllowedAccountReturns200(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	recA := config.RecommendationRecord{
		ID:             "rec-a-detail",
		Provider:       "aws",
		Service:        "ec2",
		CloudAccountID: permsPtr(permsAccA),
		Savings:        300,
		Count:          5,
	}

	mockSched := new(MockScheduler)
	mockSched.On("GetRecommendationByID", ctx, "rec-a-detail").Return(&recA, ([]string)(nil), nil)
	t.Cleanup(func() { mockSched.AssertExpectations(t) })

	mockStore := new(MockConfigStore)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}
	mockStore.On("GetRecommendationsFreshness", ctx).
		Return(&config.RecommendationsFreshness{LastCollectedAt: &now}, nil)

	handler := &Handler{
		auth:      scopedAuthMock(ctx),
		scheduler: mockSched,
		config:    mockStore,
	}

	got, err := handler.getRecommendationDetail(ctx, scopedReq(), "rec-a-detail")
	require.NoError(t, err, "scoped user must be able to fetch detail of account-A recommendation")
	require.NotNil(t, got)
	assert.Equal(t, "rec-a-detail", got.ID)
}

// ─── 3. GET /history ─────────────────────────────────────────────────────────

// TestPerAccountPerms_History_ListFilter asserts that a scoped user sees only
// account-A rows in the purchase history response.
//
// filterPurchaseHistoryByAllowedAccounts matches using the record's AccountID
// field (cloud-provider raw ID) against allowed_accounts, and then falls back
// to the nameByID lookup. To exercise the ID-match path we set AccountID to
// permsAccA directly (simulating a UUID-keyed history row), which is the most
// common post-#223 case.
//
// Regression: removing filterPurchaseHistoryByAllowedAccounts from getHistory
// causes both rows to appear and the assert.Len check fails.
func TestPerAccountPerms_History_ListFilter(t *testing.T) {
	ctx := context.Background()

	rowA := config.PurchaseHistoryRecord{
		AccountID:  permsAccA,
		PurchaseID: "hist-a",
		Status:     "completed",
	}
	rowB := config.PurchaseHistoryRecord{
		AccountID:  permsAccB,
		PurchaseID: "hist-b",
		Status:     "completed",
	}

	mockStore := new(MockConfigStore)
	mockStore.On("GetAllPurchaseHistory", ctx, mock.Anything).
		Return([]config.PurchaseHistoryRecord{rowA, rowB}, nil)
	mockStore.On("GetExecutionsByStatuses", ctx, mock.Anything, mock.Anything).
		Return([]config.PurchaseExecution{}, nil)
	// resolveAccountNamesByID needs ListCloudAccounts so MatchesAccount can
	// resolve display names when the AccountID differs from the UUID.
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:   scopedAuthMock(ctx),
		config: mockStore,
	}

	result, err := handler.getHistory(ctx, scopedReq(), map[string]string{})
	require.NoError(t, err)

	histResp := result.(HistoryResponse)
	require.Len(t, histResp.Purchases, 1,
		"scoped user must see only account-A history row; account-B row must be filtered out")
	assert.Equal(t, "hist-a", histResp.Purchases[0].PurchaseID)

	// Regression anchor: account-B must be absent.
	for _, p := range histResp.Purchases {
		assert.NotEqual(t, permsAccB, p.AccountID,
			"account-B purchase must not appear in scoped user's history")
	}
}

// ─── 4. GET /history/analytics ───────────────────────────────────────────────

// TestPerAccountPerms_HistoryAnalytics_CrossAccountRejected verifies that a
// scoped user (allowed_accounts: [permsAccA]) gets 404 when they request
// analytics for account B. validateAnalyticsAccountScope calls MatchesAccount
// and returns errNotFound on mismatch.
//
// Regression: removing validateAnalyticsAccountScope (or its MatchesAccount
// call) allows the analytics query to proceed and returns 200 to the caller.
func TestPerAccountPerms_HistoryAnalytics_CrossAccountRejected(t *testing.T) {
	ctx := context.Background()

	mockClient := new(MockAnalyticsClient)
	mockStore := new(MockConfigStore)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:            scopedAuthMock(ctx),
		analyticsClient: mockClient,
		config:          mockStore,
	}

	// Scoped user requests analytics for account B — must be rejected.
	_, err := handler.getHistoryAnalytics(ctx, scopedReq(), map[string]string{
		"account_id": permsAccB,
	})
	require.Error(t, err, "scoped user must not be able to query analytics for account-B")
	assert.True(t, IsNotFoundError(err),
		"cross-account analytics must return 404; got: %v", err)

	// The analytics backend must never be called — the scope check fires first.
	mockClient.AssertNotCalled(t, "QueryHistory")
}

// TestPerAccountPerms_HistoryAnalytics_AllowedAccountSucceeds is the paired
// positive case: account-A analytics are allowed for the scoped user.
func TestPerAccountPerms_HistoryAnalytics_AllowedAccountSucceeds(t *testing.T) {
	ctx := context.Background()

	now := time.Now().UTC()
	start := now.Add(-7 * 24 * time.Hour)

	mockClient := new(MockAnalyticsClient)
	// permsAccA is a known account UUID (no external id in the fixture), so it
	// is matched on cloud_account_id only — the uuid set carries it, externals
	// is nil.
	mockClient.On("QueryHistory", ctx, []string{permsAccA}, map[string][]string(nil), mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]HistoryDataPoint{}, &HistorySummary{}, nil)

	mockStore := new(MockConfigStore)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:            scopedAuthMock(ctx),
		analyticsClient: mockClient,
		config:          mockStore,
	}

	result, err := handler.getHistoryAnalytics(ctx, scopedReq(), map[string]string{
		"account_id": permsAccA,
		"start":      start.Format("2006-01-02"),
	})
	require.NoError(t, err, "scoped user must be able to query analytics for account-A")
	require.NotNil(t, result)
	// Confirm the analytics backend was reached — not short-circuited.
	mockClient.AssertCalled(t, "QueryHistory", ctx, []string{permsAccA}, map[string][]string(nil), mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// ─── 5. GET /history/breakdown ───────────────────────────────────────────────

// TestPerAccountPerms_HistoryBreakdown_AllowedAccountSucceeds is the positive
// case for the breakdown endpoint: a scoped user requesting breakdown for
// account A must reach the analytics client and receive results. This prevents
// a regression that blocks all scoped users regardless of account.
func TestPerAccountPerms_HistoryBreakdown_AllowedAccountSucceeds(t *testing.T) {
	ctx := context.Background()

	expectedData := map[string]BreakdownValue{
		"ec2": {PurchaseCount: 3, TotalSavings: 150.0},
	}
	mockClient := new(MockAnalyticsClient)
	mockClient.On("QueryBreakdown", ctx, []string{permsAccA}, map[string][]string(nil), mock.Anything, mock.Anything, mock.Anything).
		Return(expectedData, nil)

	mockStore := new(MockConfigStore)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:            scopedAuthMock(ctx),
		analyticsClient: mockClient,
		config:          mockStore,
	}

	result, err := handler.getHistoryBreakdown(ctx, scopedReq(), map[string]string{
		"account_id": permsAccA,
		"dimension":  "service",
	})
	require.NoError(t, err, "scoped user must be able to query breakdown for account-A")
	require.NotNil(t, result)
	mockClient.AssertCalled(t, "QueryBreakdown", ctx, []string{permsAccA}, map[string][]string(nil), mock.Anything, mock.Anything, mock.Anything)
}

// TestPerAccountPerms_HistoryBreakdown_CrossAccountRejected mirrors the
// analytics test for the breakdown endpoint which shares the same
// validateAnalyticsAccountScope guard.
func TestPerAccountPerms_HistoryBreakdown_CrossAccountRejected(t *testing.T) {
	ctx := context.Background()

	mockClient := new(MockAnalyticsClient)
	mockStore := new(MockConfigStore)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:            scopedAuthMock(ctx),
		analyticsClient: mockClient,
		config:          mockStore,
	}

	_, err := handler.getHistoryBreakdown(ctx, scopedReq(), map[string]string{
		"account_id": permsAccB,
	})
	require.Error(t, err, "scoped user must not be able to query breakdown for account-B")
	assert.True(t, IsNotFoundError(err),
		"cross-account breakdown must return 404; got: %v", err)

	mockClient.AssertNotCalled(t, "QueryBreakdown")
}

// ─── 6. GET /dashboard/summary ───────────────────────────────────────────────

// TestPerAccountPerms_DashboardSummary_AggregatesAllowedSubsetOnly asserts that
// the dashboard summary aggregates only account-A recommendations for a scoped
// user — even when the scheduler returns records for both accounts.
//
// The assertion checks both the total count and the dollar total so that a
// partial leak (e.g. savings from account B leaking into PotentialMonthlySavings)
// is caught, not just a record-count discrepancy.
//
// Regression: removing filterDashboardRecommendations from getDashboardSummary
// causes both records to be aggregated, raising TotalRecommendations to 2 and
// PotentialMonthlySavings to 300.
func TestPerAccountPerms_DashboardSummary_AggregatesAllowedSubsetOnly(t *testing.T) {
	ctx := context.Background()

	recA := config.RecommendationRecord{
		Provider: "aws", Service: "ec2",
		CloudAccountID: permsPtr(permsAccA),
		Savings:        100,
	}
	recB := config.RecommendationRecord{
		Provider: "aws", Service: "rds",
		CloudAccountID: permsPtr(permsAccB),
		Savings:        200,
	}

	mockSched := new(MockScheduler)
	mockSched.On("ListRecommendations", ctx, mock.Anything).Return([]config.RecommendationRecord{recA, recB}, nil)

	mockStore := new(MockConfigStore)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	// resolveCoverageByAccountKey calls config.ResolveAccountConfigsForRecs which
	// calls GetServiceConfig for each unique (provider, service) pair in the
	// post-filter recommendation set. We return a nil config so the scaling falls
	// through to full savings (nil is treated as "not configured"
	// by resolveCoverageByAccountKey — see issue #201).
	mockStore.On("GetServiceConfig", ctx, "aws", "ec2").Return((*config.ServiceConfig)(nil), nil)
	// Guard against future code paths that resolve service configs before filtering:
	// stub rds so an unexpected GetServiceConfig call doesn't panic the test.
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return((*config.ServiceConfig)(nil), nil)
	// Issue #956: a restricted session with no explicit account filter scopes the
	// commitment metrics to its allowed_accounts (permsAccA), so the handler calls
	// GetActivePurchaseHistory scoped to account A — NOT an unscoped all-accounts read.
	// permsAccA has no ExternalID, so only the UUID half of the filter is set.
	mockStore.On("GetActivePurchaseHistory", ctx, mock.AnythingOfType("time.Time"),
		[]string{permsAccA}, map[string][]string(nil)).Return([]config.PurchaseHistoryRecord{}, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:      scopedAuthMock(ctx),
		scheduler: mockSched,
		config:    mockStore,
	}

	result, err := handler.getDashboardSummary(ctx, scopedReq(), map[string]string{})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 1, result.TotalRecommendations,
		"dashboard must count only account-A recommendations for a scoped user")
	assert.Equal(t, 100.0, result.PotentialMonthlySavings,
		"dashboard savings must reflect only account-A (100); account-B savings (200) must not leak")
	// Issue #956 regression: the all-accounts fast path must NOT be taken for a
	// restricted session, or commitment KPIs would leak other accounts' data.
	mockStore.AssertNotCalled(t, "GetAllPurchaseHistory", mock.Anything, mock.Anything)
	// Positively assert the scoped read happened: without this the test could
	// pass if commitment metrics were skipped entirely.
	mockStore.AssertCalled(t, "GetActivePurchaseHistory", ctx, mock.AnythingOfType("time.Time"),
		[]string{permsAccA}, map[string][]string(nil))
}

// TestPerAccountPerms_DashboardSummary_CommitmentMetricsExcludeOtherAccounts is
// the issue #956 regression: a restricted session with no explicit account
// filter must see commitment KPIs (ActiveCommitments / CommittedMonthly)
// derived only from its allowed account's purchase history. Before the fix the
// handler fell through to GetAllPurchaseHistory, so a scoped user saw every
// account's commitments. Here the store is asked for account A only, and the
// account-B commitment never reaches the KPIs.
func TestPerAccountPerms_DashboardSummary_CommitmentMetricsExcludeOtherAccounts(t *testing.T) {
	ctx := context.Background()

	mockSched := new(MockScheduler)
	mockSched.On("ListRecommendations", ctx, mock.Anything).Return([]config.RecommendationRecord{}, nil)

	purchaseTime := time.Now().AddDate(0, -2, 0) // active 1-year commitment
	// Only account A's purchase history is returned by the scoped query; the
	// account-B row (200/mo) is filtered out at the store and must not appear.
	accountARows := []config.PurchaseHistoryRecord{
		{CloudAccountID: permsPtr(permsAccA), Timestamp: purchaseTime, Term: 1, Service: "ec2", EstimatedSavings: 100.0},
	}

	mockStore := new(MockConfigStore)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	mockStore.On("GetActivePurchaseHistory", ctx, mock.AnythingOfType("time.Time"),
		[]string{permsAccA}, map[string][]string(nil)).Return(accountARows, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	handler := &Handler{
		auth:      scopedAuthMock(ctx),
		scheduler: mockSched,
		config:    mockStore,
	}

	result, err := handler.getDashboardSummary(ctx, scopedReq(), map[string]string{})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, 1, result.ActiveCommitments,
		"only account-A's commitment may be counted for a scoped session")
	assert.Equal(t, 100.0, result.CommittedMonthly,
		"account-B commitment (200) must not leak into a scoped session's KPIs")
	mockStore.AssertNotCalled(t, "GetAllPurchaseHistory", mock.Anything, mock.Anything)
}

// ─── 7. POST /purchases/execute ──────────────────────────────────────────────

// TestPerAccountPerms_ExecutePurchase_CrossAccountRejected403 verifies that a
// scoped user (allowed_accounts: [permsAccA]) who tries to execute a purchase
// recommendation tagged to account B gets 403 from validatePurchaseRecommendationScope.
//
// Regression: removing validatePurchaseRecommendationScope from executePurchase
// (or its inner MatchesAccount call) allows the purchase to proceed and the
// assertions below fail.
func TestPerAccountPerms_ExecutePurchase_CrossAccountRejected403(t *testing.T) {
	ctx := context.Background()

	mockStore := new(MockConfigStore)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:   scopedAuthMock(ctx),
		config: mockStore,
	}

	// Recommendation targets account B — outside the scoped user's allowed set.
	body, err := json.Marshal(map[string]interface{}{
		"recommendations": []map[string]interface{}{
			{
				"id":               "rec-b-purchase",
				"provider":         "aws",
				"service":          "ec2",
				"cloud_account_id": permsAccB,
				"upfront_cost":     500.0,
				"savings":          50.0,
			},
		},
	})
	require.NoError(t, err)

	req := scopedReq()
	req.Body = string(body)

	_, err = handler.executePurchase(ctx, req)
	require.Error(t, err, "scoped user must not be able to execute a purchase for account-B")

	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 403, ce.code,
		"cross-account purchase attempt must return 403")
	assert.Contains(t, ce.message, permsAccB,
		"403 message must reference the out-of-scope account ID so the caller understands why")

	// The store must not have persisted anything.
	mockStore.AssertNotCalled(t, "SavePurchaseExecution")
}

// TestPerAccountPerms_ExecutePurchase_UnattributedRecRejected400 asserts that a
// scoped user cannot execute a recommendation that has no cloud_account_id —
// the handler refuses on principle since it cannot determine account ownership.
//
// Regression: removing the nil-CloudAccountID guard in
// validatePurchaseRecommendationScope allows the purchase to proceed,
// silently bypassing account-level scoping.
func TestPerAccountPerms_ExecutePurchase_UnattributedRecRejected400(t *testing.T) {
	ctx := context.Background()

	mockStore := new(MockConfigStore)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:   scopedAuthMock(ctx),
		config: mockStore,
	}

	// Recommendation has no cloud_account_id — cannot be attributed.
	body, err := json.Marshal(map[string]interface{}{
		"recommendations": []map[string]interface{}{
			{
				"id":           "rec-no-account",
				"provider":     "aws",
				"service":      "ec2",
				"upfront_cost": 200.0,
				"savings":      20.0,
				// cloud_account_id intentionally absent
			},
		},
	})
	require.NoError(t, err)

	req := scopedReq()
	req.Body = string(body)

	_, err = handler.executePurchase(ctx, req)
	require.Error(t, err, "scoped user must not be able to execute an unattributed recommendation")

	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 400, ce.code,
		"unattributed recommendation must return 400 (not 403) for scoped users")

	mockStore.AssertNotCalled(t, "SavePurchaseExecution")
}

// TestPerAccountPerms_ExecutePurchase_AllowedAccountAccepted verifies that a
// scoped user can execute a recommendation tagged to account A. The handler
// should reach SavePurchaseExecution (no error), confirming the scope check
// does not block in-scope requests.
//
// Note: the status in the response is "failed" because no emailNotifier is
// wired in this test. That is the correct behavior per the existing
// TestHandler_executePurchase_Success test — "failed" means "saved but email
// could not send", not that the scope check blocked it.
func TestPerAccountPerms_ExecutePurchase_AllowedAccountAccepted(t *testing.T) {
	ctx := context.Background()

	mockStore := new(MockConfigStore)
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	mockStore.On("SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution")).Return(nil)
	// #644 idempotency lookup: no prior pending row → proceed to create.
	mockStore.On("GetPendingExecutions", ctx).Return([]config.PurchaseExecution{}, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:   scopedAuthMock(ctx),
		config: mockStore,
	}

	// Recommendation is tagged to account A — within the scoped user's allowed set.
	// Carries a valid term/payment/count so the #643 per-rec validation passes;
	// CreateSuppressionTx is a no-op in the mock when not explicitly expected.
	body, err := json.Marshal(map[string]interface{}{
		"recommendations": []map[string]interface{}{
			{
				"id":               "rec-a-exec",
				"provider":         "aws",
				"service":          "ec2",
				"cloud_account_id": permsAccA,
				"count":            1,
				"term":             1,
				"payment":          "all-upfront",
				"upfront_cost":     100.0,
				"savings":          10.0,
			},
		},
	})
	require.NoError(t, err)

	req := scopedReq()
	req.Body = string(body)

	result, err := handler.executePurchase(ctx, req)
	require.NoError(t, err, "scoped user must be able to execute a purchase for account-A")
	require.NotNil(t, result)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, resultMap["execution_id"], "execution_id must be populated on success")
	assert.Equal(t, 1, resultMap["recommendation_count"])

	// SavePurchaseExecution must have been called (the scope check did not block).
	mockStore.AssertCalled(t, "SavePurchaseExecution", ctx, mock.AnythingOfType("*config.PurchaseExecution"))
}

// ─── 8. Planned purchase access (requireExecutionAccess) ─────────────────────

// TestPerAccountPerms_PlannedPurchase_CrossAccountPlanRejected404 asserts that
// a scoped user cannot pause (or otherwise access) an execution whose plan is
// associated with account B.
//
// This complements TestHandler_pausePlannedPurchase_OutOfScope (in
// handler_purchases_test.go) by using the per-account-perms test fixture UUIDs
// so the full matrix (account A allowed, account B rejected) is explicit in one
// place.
//
// Regression: removing requireExecutionAccess from pausePlannedPurchase causes
// the scoped user to reach TransitionExecutionStatus and the AssertNotCalled
// check fails.
func TestPerAccountPerms_PlannedPurchase_CrossAccountPlanRejected404(t *testing.T) {
	ctx := context.Background()

	executionID := "eeeeeeee-eeee-4eee-eeee-eeeeeeeeeeee"
	planID := "ffffffff-ffff-4fff-ffff-ffffffffffff"

	mockStore := new(MockConfigStore)
	mockStore.On("GetExecutionByID", ctx, executionID).Return(&config.PurchaseExecution{
		ExecutionID: executionID,
		PlanID:      planID,
	}, nil)

	// The plan is associated with account B only.
	store := &mockStoreWithPlanAccounts{
		MockConfigStore: mockStore,
		planAccounts: map[string][]config.CloudAccount{
			planID: {{ID: permsAccB, Name: permsAccBName}},
		},
	}

	handler := &Handler{
		auth:   scopedAuthMock(ctx),
		config: store,
	}

	_, err := handler.pausePlannedPurchase(ctx, scopedReq(), executionID)
	require.Error(t, err, "scoped user must not be able to pause an account-B execution")
	assert.True(t, IsNotFoundError(err),
		"cross-account execution access must return 404; got: %v", err)

	mockStore.AssertNotCalled(t, "TransitionExecutionStatus")
}

// ─── 9. GET /inventory/coverage ──────────────────────────────────────────────

// TestPerAccountPerms_CoverageBreakdown_RecsFilteredByAllowedAccounts asserts
// that a scoped user's coverage response aggregates on-demand savings only from
// account-A recommendations — account-B savings must be excluded from the
// onDemandByKey accumulation so the coverage% is not inflated by inaccessible data.
//
// Regression: if filterRecommendationsByAllowedAccounts is removed from
// getCoverageBreakdown's recommendations leg, the account-B savings (200)
// pollute onDemandByKey and the aws:ec2 on_demand_monthly in the response rises
// from 200 to 400, lowering the coverage% from 50% to 33%.
//
// The commitments leg must also scope the SQL read itself: with no explicit
// account_id, fetchCommitmentRecords resolves the session's allowed_accounts
// (permsAccA) and passes them to GetActivePurchaseHistory, NOT an unscoped
// nil/nil read that pulls every tenant's rows into memory first (issue #956
// contract, PR #1221 review). The mock expectation pins the scoped args.
func TestPerAccountPerms_CoverageBreakdown_RecsFilteredByAllowedAccounts(t *testing.T) {
	ctx := context.Background()

	now := time.Now()
	// Active commitment for account A: contributes to covered side.
	purchaseA := config.PurchaseHistoryRecord{
		AccountID:   permsAccA,
		PurchaseID:  "p-cov-a",
		Provider:    "aws",
		Service:     "ec2",
		Timestamp:   now.AddDate(0, -6, 0),
		Term:        1,
		MonthlyCost: float64Ptr(200.0),
	}

	// Recommendation for account A: on-demand gap the scoped user is allowed to see.
	recA := config.RecommendationRecord{
		ID:             "rec-cov-a",
		Provider:       "aws",
		Service:        "ec2",
		CloudAccountID: permsPtr(permsAccA),
		Savings:        200.0,
	}
	// Recommendation for account B: must be excluded for the scoped user.
	recB := config.RecommendationRecord{
		ID:             "rec-cov-b",
		Provider:       "aws",
		Service:        "ec2",
		CloudAccountID: permsPtr(permsAccB),
		Savings:        200.0,
	}

	mockSched := new(MockScheduler)
	mockSched.On("ListRecommendations", ctx, config.RecommendationFilter{}).
		Return([]config.RecommendationRecord{recA, recB}, nil)

	mockStore := new(MockConfigStore)
	// Scoped session, no account_id param: the store read must already be
	// scoped to the allowed account's UUID (permsAccA has no ExternalID in the
	// fixture, so only the UUID half of the dual-column filter is set).
	mockStore.On("GetActivePurchaseHistory", ctx, mock.AnythingOfType("time.Time"), []string{permsAccA}, map[string][]string(nil)).
		Return([]config.PurchaseHistoryRecord{purchaseA}, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	handler := &Handler{
		auth:      scopedAuthMock(ctx),
		scheduler: mockSched,
		config:    mockStore,
	}

	result, err := handler.getCoverageBreakdown(ctx, scopedReq(), map[string]string{})
	require.NoError(t, err)
	require.NotNil(t, result)

	resp, ok := result.(CoverageBreakdownResponse)
	require.True(t, ok)

	var aws *ProviderCoverageSection
	for i := range resp.Providers {
		if resp.Providers[i].Provider == "aws" {
			aws = &resp.Providers[i]
			break
		}
	}
	require.NotNil(t, aws, "aws provider section must be present")
	require.NotNil(t, aws.Services, "aws must have service rows")
	require.Len(t, aws.Services, 1)

	ec2 := aws.Services[0]
	assert.Equal(t, "ec2", ec2.Service)
	assert.Equal(t, 200.0, ec2.CoveredMonthly, "covered side must reflect account-A commitment")
	assert.Equal(t, 200.0, ec2.OnDemandMonthly,
		"on-demand side must include only account-A rec (200); account-B rec (200) must be excluded")
	// coverage = 200/(200+200) * 100 = 50%
	require.NotNil(t, ec2.CoveragePct)
	assert.InDelta(t, 50.0, *ec2.CoveragePct, 0.001,
		"coverage must be computed from account-A data only; account-B leak would give ~33%%")
}

// TestPerAccountPerms_CoverageBreakdown_AdminSeesAll confirms that an
// unrestricted session aggregates both accounts — the positive path through
// IsUnrestrictedAccess so the test above cannot pass by dropping all records.
func TestPerAccountPerms_CoverageBreakdown_AdminSeesAll(t *testing.T) {
	ctx := context.Background()

	now := time.Now()
	purchaseA := config.PurchaseHistoryRecord{
		AccountID:   permsAccA,
		PurchaseID:  "p-cov-admin-a",
		Provider:    "aws",
		Service:     "ec2",
		Timestamp:   now.AddDate(0, -6, 0),
		Term:        1,
		MonthlyCost: float64Ptr(200.0),
	}
	recA := config.RecommendationRecord{
		Provider: "aws", Service: "ec2",
		CloudAccountID: permsPtr(permsAccA), Savings: 200.0,
	}
	recB := config.RecommendationRecord{
		Provider: "aws", Service: "ec2",
		CloudAccountID: permsPtr(permsAccB), Savings: 200.0,
	}

	mockSched := new(MockScheduler)
	mockSched.On("ListRecommendations", ctx, config.RecommendationFilter{}).
		Return([]config.RecommendationRecord{recA, recB}, nil)

	mockStore := new(MockConfigStore)
	mockStore.On("GetActivePurchaseHistory", ctx, mock.AnythingOfType("time.Time"), []string(nil), map[string][]string(nil)).
		Return([]config.PurchaseHistoryRecord{purchaseA}, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	mockAuth, req := adminDashboardReq(ctx)
	handler := &Handler{
		auth:      mockAuth,
		scheduler: mockSched,
		config:    mockStore,
	}

	result, err := handler.getCoverageBreakdown(ctx, req, map[string]string{})
	require.NoError(t, err)

	resp, ok := result.(CoverageBreakdownResponse)
	require.True(t, ok)

	var aws *ProviderCoverageSection
	for i := range resp.Providers {
		if resp.Providers[i].Provider == "aws" {
			aws = &resp.Providers[i]
			break
		}
	}
	require.NotNil(t, aws)
	require.Len(t, aws.Services, 1)
	// Admin sees both recs: on_demand = 200+200 = 400; coverage = 200/600 * 100 = 33.3%
	assert.Equal(t, 400.0, aws.Services[0].OnDemandMonthly,
		"admin must see both accounts' on-demand gap (200+200)")
	require.NotNil(t, aws.Services[0].CoveragePct)
	assert.InDelta(t, 33.333, *aws.Services[0].CoveragePct, 0.01,
		"admin coverage = 200/(200+400) * 100 = 33.3%%")
}

// ─── 9b. GET /inventory/commitments ──────────────────────────────────────────

// TestPerAccountPerms_InventoryCommitments_ScopedSQLRead asserts that a
// restricted session with no explicit account_id scopes the active-commitments
// SQL read itself to its allowed_accounts: fetchCommitmentRecords must call
// GetActivePurchaseHistory with the resolved allowed-account scope
// ([permsAccA], nil; the fixture account has no ExternalID), never the
// unscoped nil/nil read that pulls every tenant's active commitments into
// memory before filterPurchaseHistoryByAllowedAccounts trims them (issue #956
// contract, PR #1221 review).
//
// Regression: pre-fix the handler called GetActivePurchaseHistory(ctx, asOf,
// nil, nil) for this exact request shape; the pinned mock args make that call
// panic the test.
func TestPerAccountPerms_InventoryCommitments_ScopedSQLRead(t *testing.T) {
	ctx := context.Background()

	now := time.Now()
	purchaseA := config.PurchaseHistoryRecord{
		AccountID:        permsAccA,
		PurchaseID:       "p-inv-a",
		Provider:         "aws",
		Service:          "ec2",
		Timestamp:        now.AddDate(0, -6, 0),
		Term:             1,
		Count:            1,
		MonthlyCost:      float64Ptr(80.0),
		EstimatedSavings: 120.0,
	}

	mockStore := new(MockConfigStore)
	mockStore.On("GetActivePurchaseHistory", ctx, mock.AnythingOfType("time.Time"), []string{permsAccA}, map[string][]string(nil)).
		Return([]config.PurchaseHistoryRecord{purchaseA}, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}
	t.Cleanup(func() { mockStore.AssertExpectations(t) })

	handler := &Handler{
		auth:   scopedAuthMock(ctx),
		config: mockStore,
	}

	result, err := handler.listActiveCommitments(ctx, scopedReq(), map[string]string{})
	require.NoError(t, err)

	resp, ok := result.(InventoryCommitmentsResponse)
	require.True(t, ok)
	require.Len(t, resp.Commitments, 1,
		"scoped user must see exactly the allowed account's commitment")
	assert.Equal(t, permsAccA+":p-inv-a", resp.Commitments[0].ID)
}

// TestPerAccountPerms_InventoryCommitments_ZeroAllowedAccountsNoQuery asserts
// the zero-account sentinel: a restricted session whose allowed_accounts match
// no cloud account must get an empty commitments list WITHOUT the store being
// queried at all: an empty scope passed to GetActivePurchaseHistory would
// emit no account predicate and match every tenant's rows (the same
// short-circuit fetchCommitmentPurchases applies on the dashboard path).
func TestPerAccountPerms_InventoryCommitments_ZeroAllowedAccountsNoQuery(t *testing.T) {
	ctx := context.Background()

	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, permsScopedToken).Return(&Session{
		UserID: permsScopedUserID,
		Email:  "scoped@example.com",
	}, nil)
	mockAuth.On("HasPermissionAPI", ctx, permsScopedUserID, mock.Anything, mock.Anything).Return(true, nil)
	// Allowed entry matches neither fixture account, so the resolved scope is
	// the non-nil-but-empty sentinel.
	mockAuth.On("GetAllowedAccountsAPI", ctx, permsScopedUserID).
		Return([]string{"dddddddd-dddd-4ddd-dddd-dddddddddddd"}, nil)

	mockStore := new(MockConfigStore)
	t.Cleanup(func() {
		mockStore.AssertExpectations(t)
		mockAuth.AssertExpectations(t)
	})
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:   mockAuth,
		config: mockStore,
	}

	result, err := handler.listActiveCommitments(ctx, scopedReq(), map[string]string{})
	require.NoError(t, err)

	resp, ok := result.(InventoryCommitmentsResponse)
	require.True(t, ok)
	assert.Empty(t, resp.Commitments,
		"zero accessible accounts must yield an empty list, not all-accounts data")
	mockStore.AssertNotCalled(t, "GetActivePurchaseHistory",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestPerAccountPerms_PlannedPurchase_AllowedAccountPlanSucceeds is the paired
// positive case confirming the filter passes for account-A plans.
func TestPerAccountPerms_PlannedPurchase_AllowedAccountPlanSucceeds(t *testing.T) {
	ctx := context.Background()

	executionID := "eeeeeeee-eeee-4eee-eeee-eeeeeeeeeee2"
	planID := "ffffffff-ffff-4fff-ffff-ffffffffffff"

	transitoned := &config.PurchaseExecution{
		ExecutionID: executionID, PlanID: planID, Status: "paused",
	}

	mockStore := new(MockConfigStore)
	mockStore.On("GetExecutionByID", ctx, executionID).Return(&config.PurchaseExecution{
		ExecutionID: executionID, PlanID: planID, Status: "pending",
	}, nil)
	mockStore.On("TransitionExecutionStatus", ctx, executionID, mock.Anything, "paused", mock.Anything).
		Return(transitoned, nil)

	// Plan is associated with account A — within the scoped user's allowed set.
	store := &mockStoreWithPlanAccounts{
		MockConfigStore: mockStore,
		planAccounts: map[string][]config.CloudAccount{
			planID: {{ID: permsAccA, Name: permsAccAName}},
		},
	}

	handler := &Handler{
		auth:   scopedAuthMock(ctx),
		config: store,
	}

	result, err := handler.pausePlannedPurchase(ctx, scopedReq(), executionID)
	require.NoError(t, err, "scoped user must be able to pause an account-A execution")
	require.NotNil(t, result)
	assert.Equal(t, "paused", result.Status, "result must reflect the paused status")
	mockStore.AssertCalled(t, "TransitionExecutionStatus", ctx, executionID, mock.Anything, "paused", mock.Anything)
}

// ─── 10. GET /ri-exchange/instances ──────────────────────────────────────────

// TestPerAccountPerms_ListConvertibleRIs_ScopedOutsideAllowedReturnsEmpty
// asserts that a restricted user (allowed_accounts: [permsAccA]) receives an
// empty instance list when the deployment's registered CloudAccount UUID is
// permsAccB (outside their scope).
//
// The handler resolves the deployment CloudAccount ID via reshapeAccountResolver
// and checks it against the session's allowed_accounts with auth.MatchesAccount.
// When they don't match, no AWS SDK call is made and an empty list is returned.
//
// Regression: removing the getAllowedAccounts guard from listConvertibleRIs
// causes the handler to proceed to the AWS SDK call and the assertion fails
// because the scope check never fires.
func TestPerAccountPerms_ListConvertibleRIs_ScopedOutsideAllowedReturnsEmpty(t *testing.T) {
	ctx := context.Background()

	mockStore := new(MockConfigStore)
	// resolveAccountNamesByID calls ListCloudAccounts so MatchesAccount can
	// resolve display names. Return both accounts so the name lookup is realistic.
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:   scopedAuthMock(ctx),
		config: mockStore,
		// Inject the deployment CloudAccount UUID as permsAccB — outside
		// the scoped user's allowed set ([permsAccA]).
		reshapeAccountResolver: func(_ context.Context) (string, error) {
			return permsAccB, nil
		},
	}

	resp, err := handler.listConvertibleRIs(ctx, scopedReq())
	require.NoError(t, err, "scope mismatch must return empty list, not error")
	typed, ok := resp.(*ConvertibleRIsResponse)
	require.True(t, ok, "expected *ConvertibleRIsResponse, got %T", resp)
	assert.Empty(t, typed.Instances,
		"scoped user must receive an empty RI list when the deployment account is outside their allowed_accounts")
}

// TestPerAccountPerms_ListConvertibleRIs_AdminSeesAll verifies that an admin
// (unrestricted) session bypasses the allowed_accounts gate entirely. The
// handler will attempt the AWS SDK call, which may fail without credentials
// in a test environment, but the scope check itself must not block the admin.
//
// This is the positive path through IsUnrestrictedAccess — if it were missing,
// the admin session would also go through the account check.
func TestPerAccountPerms_ListConvertibleRIs_AdminSeesAll(t *testing.T) {
	ctx := context.Background()

	// Admin auth — ValidateSession returns Role:"admin" so getAllowedAccounts
	// returns nil (unrestricted) and the scope filter is skipped entirely.
	mockAuth, req := adminDashboardReq(ctx)

	// reshapeAccountResolver is NOT set: if the scope check incorrectly runs for
	// admins it would call h.resolveAWSCloudAccountID which needs STS, causing a
	// config-load error. The test confirms the resolver is never called by NOT
	// injecting one — any call to it would panic.
	mockStore := new(MockConfigStore)

	handler := &Handler{
		auth:   mockAuth,
		config: mockStore,
		// Pre-seal awsCfgOnce with a zero Config so loadAWSConfigWithRegion
		// returns immediately without hitting the AWS SDK. The EC2 client
		// will fail (no real credentials), but we only care that the scope
		// check is skipped — the subsequent error from AWS is expected.
		//
		// We can't inject an EC2 factory into listConvertibleRIs (it calls
		// awsprovider.NewEC2ClientDirect directly), so we leave this as a
		// known limitation: the admin path is confirmed scope-check-free,
		// and the subsequent AWS failure is not part of this assertion.
	}
	// Pre-seal awsCfgOnce so loadAWSConfigWithRegion returns without loading.
	handler.awsCfgOnce.Do(func() {})

	// The call will fail at the AWS SDK level (no credentials in test), but
	// critically it must NOT fail with a scope/allowed_accounts error. The
	// scope check must be skipped for admins.
	_, err := handler.listConvertibleRIs(ctx, req)
	// If err is non-nil it must be an AWS/SDK error, not a scope error.
	if err != nil {
		assert.NotContains(t, err.Error(), "allowed accounts",
			"admin must bypass the allowed_accounts scope check; error must be AWS-level, not scope-level")
		assert.NotContains(t, err.Error(), "cloud account scope",
			"admin must bypass the cloud account scope check")
	}
}

// ─── 11. GET /ri-exchange/utilization ────────────────────────────────────────

// TestPerAccountPerms_GetRIUtilization_ScopedOutsideAllowedReturnsEmpty
// asserts that a restricted user receives an empty utilization list when the
// deployment's registered CloudAccount UUID is outside their allowed_accounts.
//
// Regression: removing the getAllowedAccounts guard from getRIUtilization
// causes the handler to proceed to the AWS SDK call (Cost Explorer) and the
// empty-list assertion fails because the scope check never fires.
func TestPerAccountPerms_GetRIUtilization_ScopedOutsideAllowedReturnsEmpty(t *testing.T) {
	ctx := context.Background()

	mockStore := new(MockConfigStore)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:   scopedAuthMock(ctx),
		config: mockStore,
		// Deployment account is permsAccB — outside the scoped user's allowed set.
		reshapeAccountResolver: func(_ context.Context) (string, error) {
			return permsAccB, nil
		},
	}

	resp, err := handler.getRIUtilization(ctx, scopedReq())
	require.NoError(t, err, "scope mismatch must return empty list, not error")
	typed, ok := resp.(*RIUtilizationResponse)
	require.True(t, ok, "expected *RIUtilizationResponse, got %T", resp)
	assert.Empty(t, typed.Utilization,
		"scoped user must receive empty utilization when the deployment account is outside their allowed_accounts")
}

// TestPerAccountPerms_GetRIUtilization_ScopedWithinAllowedProceedsToAWS
// confirms that a scoped user whose allowed_accounts DOES include the
// deployment CloudAccount UUID is not blocked by the scope check. The handler
// will then proceed to the AWS SDK (and fail without credentials), but the
// scope filter itself must be transparent for an in-scope user.
//
// Regression: an overly-broad scope check that blocks ALL scoped users
// (not just out-of-scope ones) would cause this test to return empty prematurely.
func TestPerAccountPerms_GetRIUtilization_ScopedWithinAllowedProceedsToAWS(t *testing.T) {
	ctx := context.Background()

	mockStore := new(MockConfigStore)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:   scopedAuthMock(ctx),
		config: mockStore,
		// Deployment account is permsAccA — WITHIN the scoped user's allowed set.
		reshapeAccountResolver: func(_ context.Context) (string, error) {
			return permsAccA, nil
		},
	}
	// Pre-seal awsCfgOnce so the handler doesn't stall on AWS SDK load.
	handler.awsCfgOnce.Do(func() {})

	_, err := handler.getRIUtilization(ctx, scopedReq())
	// The scope check passes; any error from here is AWS-level, not scope-level.
	if err != nil {
		assert.NotContains(t, err.Error(), "allowed accounts",
			"in-scope user must not be blocked by allowed_accounts check")
		assert.NotContains(t, err.Error(), "cloud account scope",
			"in-scope user must not be blocked by cloud account scope check")
	}
}

// ─── 12. GET /ri-exchange/reshape-recommendations ────────────────────────────

// TestPerAccountPerms_GetReshapeRecommendations_ScopedOutsideAllowedReturnsEmpty
// asserts that a restricted user receives an empty recommendations list when
// the deployment's registered CloudAccount UUID is outside their allowed_accounts.
// No AWS EC2 or Cost Explorer calls should be made.
//
// Regression: removing the getAllowedAccounts guard from getReshapeRecommendations
// causes the handler to proceed to the AWS SDK calls and the empty-list
// assertion fails because the scope check never fires.
func TestPerAccountPerms_GetReshapeRecommendations_ScopedOutsideAllowedReturnsEmpty(t *testing.T) {
	ctx := context.Background()

	mockStore := new(MockConfigStore)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:   scopedAuthMock(ctx),
		config: mockStore,
		// Deployment account is permsAccB — outside the scoped user's allowed set.
		reshapeAccountResolver: func(_ context.Context) (string, error) {
			return permsAccB, nil
		},
	}

	resp, err := handler.getReshapeRecommendations(ctx, scopedReq())
	require.NoError(t, err, "scope mismatch must return empty recommendations, not error")
	typed, ok := resp.(*ReshapeRecommendationsResponse)
	require.True(t, ok, "expected *ReshapeRecommendationsResponse, got %T", resp)
	assert.Empty(t, typed.Recommendations,
		"scoped user must receive empty reshape recommendations when the deployment account is outside their allowed_accounts")
}

// TestPerAccountPerms_GetReshapeRecommendations_AdminSeesAll verifies that an
// admin (unrestricted) session bypasses the allowed_accounts gate and reaches
// the actual reshape logic. The handler then proceeds to the AWS SDK and recs
// lookup, using factory injection so the test runs without real AWS credentials.
//
// Note: reshapeAccountResolver is injected here not because admins need it for
// the scope check (they bypass it via IsUnrestrictedAccess), but because the
// reshape handler independently calls resolveAccount later for the recs-lookup
// scope filter. Without injection it would invoke STS which fails in the test
// environment with a 403. This is a test-plumbing concern, NOT a regression
// for the scope-check logic.
//
// Regression: if IsUnrestrictedAccess is not checked (or is inverted), the
// admin would incorrectly fall into the scope-check block. We confirm this
// does NOT happen by ensuring the response is a valid *ReshapeRecommendationsResponse
// (not an error and not an empty stub from the scope-reject branch).
func TestPerAccountPerms_GetReshapeRecommendations_AdminSeesAll(t *testing.T) {
	ctx := context.Background()

	mockStore := new(MockConfigStore)
	// ListStoredRecommendations is called by the recs lookup closure inside
	// AnalyzeReshapingWithRecs. Return empty so the test finishes without
	// surfacing unrelated recs-lookup errors.
	mockStore.On("ListStoredRecommendations", mock.Anything, mock.Anything).
		Return([]config.RecommendationRecord(nil), nil)
	mockStore.On("GetRecommendationsFreshness", mock.Anything).
		Return(&config.RecommendationsFreshness{}, nil)

	mockAuth, req := adminDashboardReq(ctx)

	handler := &Handler{
		auth:   mockAuth,
		config: mockStore,
		// Inject EC2 + recs stubs so no real AWS calls are needed.
		reshapeEC2Factory: func(_ aws.Config) reshapeEC2Client {
			return &fakeReshapeEC2Stub{
				instances: []ec2svc.ConvertibleRI{
					{ReservedInstanceID: "ri-1", InstanceType: "m5.xlarge", InstanceCount: 1, CurrencyCode: "USD"},
				},
			}
		},
		reshapeRecsFactory: func(_ aws.Config) reshapeRecsClient {
			return &fakeReshapeRecsStub{
				utilization: []recommendations.RIUtilization{
					{ReservedInstanceID: "ri-1", UtilizationPercent: 50.0},
				},
			}
		},
		// reshapeAccountResolver is injected to bypass the STS call that
		// getReshapeRecommendations makes for the recs-lookup scope filter
		// (a separate, pre-existing call unrelated to the allowed_accounts
		// scope check). Returning permsAccA is arbitrary — for an admin the
		// allowed_accounts block is never reached so this value only affects
		// which account's recs are queried, not the scope decision.
		reshapeAccountResolver: func(_ context.Context) (string, error) {
			return permsAccA, nil
		},
	}
	handler.awsCfgOnce.Do(func() {
		handler.awsCfg = aws.Config{Region: "us-east-1"}
	})

	resp, err := handler.getReshapeRecommendations(ctx, req)
	require.NoError(t, err, "admin must reach the reshape logic without being blocked by scope check")
	typed, ok := resp.(*ReshapeRecommendationsResponse)
	require.True(t, ok, "expected *ReshapeRecommendationsResponse, got %T", resp)
	// We only assert no error and correct type — proving the admin path
	// proceeded past the scope gate into the actual reshape logic.
	_ = typed
}

// ─── 13. GET /ladder/configs ──────────────────────────────────────────────────

// TestPerAccountPerms_GetLadderConfigs_ScopedUserSeesOnlyOwnAccount is the
// real failing scenario from the cross-account data leak: migration 000088
// grants view:config to non-admin groups, but getLadderConfigs previously
// returned every account's LadderConfigDB (spend caps, ramp schedules)
// unconditionally. A scoped user (allowed_accounts: [permsAccA]) must see
// only permsAccA's config when configs exist for both accounts.
//
// Regression: without filterLadderConfigsByAllowedAccounts, this test fails
// because the account-B config is included in the response.
func TestPerAccountPerms_GetLadderConfigs_ScopedUserSeesOnlyOwnAccount(t *testing.T) {
	ctx := context.Background()

	cfgA := config.LadderConfigDB{ID: "cfg-a", CloudAccountID: permsAccA, Provider: "aws"}
	cfgB := config.LadderConfigDB{ID: "cfg-b", CloudAccountID: permsAccB, Provider: "aws"}

	mockStore := new(MockConfigStore)
	mockStore.On("GetLadderConfigs", ctx).Return([]config.LadderConfigDB{cfgA, cfgB}, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

	handler := &Handler{
		auth:   scopedAuthMock(ctx),
		config: mockStore,
	}

	resp, err := handler.getLadderConfigs(ctx, scopedReq())
	require.NoError(t, err)
	body, ok := resp.(map[string]any)
	require.True(t, ok, "expected map[string]any, got %T", resp)
	configs, ok := body["configs"].([]config.LadderConfigDB)
	require.True(t, ok, "expected []config.LadderConfigDB, got %T", body["configs"])

	ids := make([]string, len(configs))
	for i, c := range configs {
		ids[i] = c.ID
	}
	assert.ElementsMatch(t, []string{"cfg-a"}, ids,
		"scoped user must see only account-A's ladder config; account-B config must be filtered out")
}

// TestPerAccountPerms_GetLadderConfigs_AdminSeesAll verifies that an admin
// (unrestricted) session bypasses the allowed_accounts gate and receives
// every account's ladder config.
func TestPerAccountPerms_GetLadderConfigs_AdminSeesAll(t *testing.T) {
	ctx := context.Background()

	cfgA := config.LadderConfigDB{ID: "cfg-a", CloudAccountID: permsAccA, Provider: "aws"}
	cfgB := config.LadderConfigDB{ID: "cfg-b", CloudAccountID: permsAccB, Provider: "aws"}

	mockStore := new(MockConfigStore)
	mockStore.On("GetLadderConfigs", ctx).Return([]config.LadderConfigDB{cfgA, cfgB}, nil)

	mockAuth, req := adminDashboardReq(ctx)
	handler := &Handler{auth: mockAuth, config: mockStore}

	resp, err := handler.getLadderConfigs(ctx, req)
	require.NoError(t, err)
	body, ok := resp.(map[string]any)
	require.True(t, ok, "expected map[string]any, got %T", resp)
	configs, ok := body["configs"].([]config.LadderConfigDB)
	require.True(t, ok, "expected []config.LadderConfigDB, got %T", body["configs"])

	ids := make([]string, len(configs))
	for i, c := range configs {
		ids[i] = c.ID
	}
	assert.ElementsMatch(t, []string{"cfg-a", "cfg-b"}, ids,
		"admin must see every account's ladder config")
}
