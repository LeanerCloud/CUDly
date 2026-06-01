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
// Endpoints covered (9 total):
//   1. GET /recommendations          (filterRecommendationsByAllowedAccounts)
//   2. GET /recommendations/:id      (getRecommendationDetail — cross-account rejection)
//   3. GET /history                  (filterPurchaseHistoryByAllowedAccounts)
//   4. GET /history/analytics        (validateAnalyticsAccountScope — cross-account rejection)
//   5. GET /history/breakdown        (validateAnalyticsAccountScope — cross-account rejection)
//   6. GET /dashboard/summary        (filterDashboardRecommendations — aggregate subset)
//   7. POST /purchases/execute       (validatePurchaseRecommendationScope — 403)
//   8. GET /purchases/planned list   (requireExecutionAccess / requirePlanAccess — 404)
//   9. GET /inventory/coverage       (filterRecommendationsByAllowedAccounts on recs leg)

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
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
	mockSched.On("ListRecommendations", ctx, mock.Anything).Return([]config.RecommendationRecord{recB}, nil)

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
	mockSched.On("ListRecommendations", ctx, mock.Anything).Return([]config.RecommendationRecord{recA}, nil)

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
	mockClient.On("QueryHistory", ctx, permsAccA, mock.Anything, mock.Anything, mock.Anything).
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
	mockClient.AssertCalled(t, "QueryHistory", ctx, permsAccA, mock.Anything, mock.Anything, mock.Anything)
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
	mockClient.On("QueryBreakdown", ctx, permsAccA, mock.Anything, mock.Anything, mock.Anything).
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
	mockClient.AssertCalled(t, "QueryBreakdown", ctx, permsAccA, mock.Anything, mock.Anything, mock.Anything)
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
	// calculateCommitmentMetrics calls GetPurchaseHistory for YTD/committed totals.
	mockStore.On("GetPurchaseHistory", ctx, mock.Anything, mock.Anything).Return([]config.PurchaseHistoryRecord{}, nil)
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
// wired in this test. That is the correct behaviour per the existing
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
		MonthlyCost: 200.0,
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
	mockStore.On("GetAllPurchaseHistory", ctx, config.MaxListLimit).
		Return([]config.PurchaseHistoryRecord{purchaseA}, nil)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return permsAccountList(), nil
	}

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
		MonthlyCost: 200.0,
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
	mockStore.On("GetAllPurchaseHistory", ctx, config.MaxListLimit).
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
	mockStore.On("TransitionExecutionStatus", ctx, executionID, mock.Anything, "paused").
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
	mockStore.AssertCalled(t, "TransitionExecutionStatus", ctx, executionID, mock.Anything, "paused")
}
