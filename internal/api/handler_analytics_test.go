package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/analytics"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockAnalyticsClient is a mock implementation of AnalyticsClientInterface
type MockAnalyticsClient struct {
	mock.Mock
}

func (m *MockAnalyticsClient) QueryHistory(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string, start, end time.Time, interval string) ([]HistoryDataPoint, *HistorySummary, error) {
	args := m.Called(ctx, accountUUIDs, accountExternalIDsByProvider, start, end, interval)
	if args.Get(0) == nil {
		return nil, nil, args.Error(2)
	}
	var summary *HistorySummary
	if args.Get(1) != nil {
		summary = args.Get(1).(*HistorySummary)
	}
	return args.Get(0).([]HistoryDataPoint), summary, args.Error(2)
}

func (m *MockAnalyticsClient) QueryBreakdown(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string, start, end time.Time, dimension string) (map[string]BreakdownValue, error) {
	args := m.Called(ctx, accountUUIDs, accountExternalIDsByProvider, start, end, dimension)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]BreakdownValue), args.Error(1)
}

// TestHandler_getHistoryAnalytics_ExternalIDOnlyAccount is the analytics
// keystone (issue #701/#498/#866): selecting an account by its cloud_accounts
// UUID resolves to (UUID, external_id) and both reach QueryHistory so rows that
// carry only the external account_id (cloud_account_id NULL) are aggregated.
func TestHandler_getHistoryAnalytics_ExternalIDOnlyAccount(t *testing.T) {
	ctx := context.Background()
	accountUUID := "bbbbbbbb-1111-2222-3333-444444444444"
	accountExternal := "999988887777"

	mockClient := new(MockAnalyticsClient)
	mockClient.On("QueryHistory", ctx, []string{accountUUID}, map[string][]string{"aws": {accountExternal}}, mock.Anything, mock.Anything, "hourly").
		Return([]HistoryDataPoint{{TotalSavings: 50.0, PurchaseCount: 1}}, &HistorySummary{TotalPurchases: 1}, nil)

	mockStore := new(MockConfigStore)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return []config.CloudAccount{{ID: accountUUID, Name: "Account B", Provider: "aws", ExternalID: accountExternal}}, nil
	}

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient, config: mockStore}

	result, err := handler.getHistoryAnalytics(ctx, req, map[string]string{"account_id": accountUUID})
	require.NoError(t, err)
	require.NotNil(t, result)
	mockClient.AssertCalled(t, "QueryHistory", ctx, []string{accountUUID}, map[string][]string{"aws": {accountExternal}}, mock.Anything, mock.Anything, "hourly")
}

// MockAnalyticsCollector is a mock implementation of AnalyticsCollectorInterface
type MockAnalyticsCollector struct {
	mock.Mock
}

func (m *MockAnalyticsCollector) Collect(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// adminAnalyticsReq returns (mocked auth with admin session, request with admin token).
// All analytics handlers are permission-gated — this short-circuits the gate so the
// existing tests can exercise the analytics-specific behaviour without rewriting auth.
func adminAnalyticsReq(ctx context.Context) (*MockAuthService, *events.LambdaFunctionURLRequest) {
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

func TestHandler_getHistoryAnalytics_Success(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAnalyticsClient)

	dataPoints := []HistoryDataPoint{
		{Timestamp: time.Now(), TotalSavings: 100.0, PurchaseCount: 5},
		{Timestamp: time.Now().Add(-time.Hour), TotalSavings: 90.0, PurchaseCount: 3},
	}
	summary := &HistorySummary{TotalPurchases: 8, TotalMonthlySavings: 190.0}

	// "account-123" is not a known UUID, so resolveSingleAccountFilterIDs
	// treats it as an external account number: QueryHistory is called with no
	// UUIDs and account-123 as the external id.
	mockClient.On("QueryHistory", ctx, []string(nil), map[string][]string{"": {"account-123"}}, mock.Anything, mock.Anything, "hourly").Return(dataPoints, summary, nil)

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient, config: new(MockConfigStore)}

	params := map[string]string{
		"account_id": "account-123",
		"interval":   "hourly",
	}

	result, err := handler.getHistoryAnalytics(ctx, req, params)
	require.NoError(t, err)

	response, ok := result.(*AnalyticsResponse)
	require.True(t, ok)
	assert.Equal(t, "hourly", response.Interval)
	assert.Len(t, response.DataPoints, 2)
	assert.Equal(t, 190.0, response.Summary.TotalMonthlySavings)
}

func TestHandler_getHistoryAnalytics_NoClient(t *testing.T) {
	ctx := context.Background()
	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth}

	_, err := handler.getHistoryAnalytics(ctx, req, map[string]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics not configured")
}

func TestHandler_getHistoryAnalytics_DefaultInterval(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAnalyticsClient)

	dataPoints := []HistoryDataPoint{}
	// Empty account_id: resolver returns no UUIDs and no external ids.
	mockClient.On("QueryHistory", ctx, []string(nil), map[string][]string(nil), mock.Anything, mock.Anything, "hourly").Return(dataPoints, (*HistorySummary)(nil), nil)

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient, config: new(MockConfigStore)}

	params := map[string]string{} // No interval specified

	_, err := handler.getHistoryAnalytics(ctx, req, params)
	require.NoError(t, err)

	mockClient.AssertCalled(t, "QueryHistory", ctx, []string(nil), map[string][]string(nil), mock.Anything, mock.Anything, "hourly")
}

func TestHandler_getHistoryAnalytics_InvalidDateRange(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAnalyticsClient)

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient}

	params := map[string]string{
		"start": "invalid-date",
	}

	_, err := handler.getHistoryAnalytics(ctx, req, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid start date")
}

func TestHandler_getHistoryAnalytics_QueryError(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAnalyticsClient)

	mockClient.On("QueryHistory", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil, errors.New("query failed"))

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient, config: new(MockConfigStore)}

	params := map[string]string{}

	_, err := handler.getHistoryAnalytics(ctx, req, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to query analytics")
}

// TestHandler_getHistoryAnalytics_ScopedUser_RequiresAccountID asserts that a
// non-admin user with restricted allowed_accounts cannot issue an unscoped
// analytics query.
func TestHandler_getHistoryAnalytics_ScopedUser_RequiresAccountID(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAnalyticsClient)
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(&Session{
		UserID: "viewer-1",
	}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "view", "purchases").Return(true, nil)
	mockAuth.On("GetAllowedAccountsAPI", ctx, "viewer-1").Return([]string{"Production"}, nil)

	handler := &Handler{auth: mockAuth, analyticsClient: mockClient}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer viewer-token"},
	}
	_, err := handler.getHistoryAnalytics(ctx, req, map[string]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account_id is required")
	mockClient.AssertNotCalled(t, "QueryHistory")
}

func TestHandler_getHistoryBreakdown_Success(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAnalyticsClient)

	breakdownData := map[string]BreakdownValue{
		"rds":    {PurchaseCount: 10, TotalSavings: 500.0},
		"ec2":    {PurchaseCount: 20, TotalSavings: 1000.0},
		"lambda": {PurchaseCount: 5, TotalSavings: 100.0},
	}

	mockClient.On("QueryBreakdown", ctx, []string(nil), map[string][]string{"": {"account-123"}}, mock.Anything, mock.Anything, "service").Return(breakdownData, nil)

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient, config: new(MockConfigStore)}

	params := map[string]string{
		"account_id": "account-123",
		"dimension":  "service",
	}

	result, err := handler.getHistoryBreakdown(ctx, req, params)
	require.NoError(t, err)

	response, ok := result.(*BreakdownResponse)
	require.True(t, ok)
	assert.Equal(t, "service", response.Dimension)
	assert.Len(t, response.Data, 3)
	assert.Equal(t, 500.0, response.Data["rds"].TotalSavings)
}

func TestHandler_getHistoryBreakdown_NoClient(t *testing.T) {
	ctx := context.Background()
	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth}

	_, err := handler.getHistoryBreakdown(ctx, req, map[string]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics not configured")
}

func TestHandler_getHistoryBreakdown_DefaultDimension(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAnalyticsClient)

	breakdownData := map[string]BreakdownValue{}
	mockClient.On("QueryBreakdown", ctx, []string(nil), map[string][]string(nil), mock.Anything, mock.Anything, "service").Return(breakdownData, nil)

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient, config: new(MockConfigStore)}

	params := map[string]string{} // No dimension specified

	_, err := handler.getHistoryBreakdown(ctx, req, params)
	require.NoError(t, err)

	mockClient.AssertCalled(t, "QueryBreakdown", ctx, []string(nil), map[string][]string(nil), mock.Anything, mock.Anything, "service")
}

func TestHandler_getHistoryBreakdown_InvalidDateRange(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAnalyticsClient)

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient}

	params := map[string]string{
		"end": "bad-date",
	}

	_, err := handler.getHistoryBreakdown(ctx, req, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid end date")
}

func TestHandler_getHistoryBreakdown_QueryError(t *testing.T) {
	ctx := context.Background()
	mockClient := new(MockAnalyticsClient)

	mockClient.On("QueryBreakdown", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("breakdown failed"))

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient, config: new(MockConfigStore)}

	params := map[string]string{}

	_, err := handler.getHistoryBreakdown(ctx, req, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to query breakdown")
}

func TestHandler_triggerAnalyticsCollection_Success(t *testing.T) {
	ctx := context.Background()
	mockCollector := new(MockAnalyticsCollector)

	mockCollector.On("Collect", ctx).Return(nil)

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsCollector: mockCollector}

	result, err := handler.triggerAnalyticsCollection(ctx, req, nil)
	require.NoError(t, err)

	response, ok := result.(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "success", response["status"])
	assert.Contains(t, response["message"], "completed")
}

func TestHandler_triggerAnalyticsCollection_NoCollector(t *testing.T) {
	ctx := context.Background()
	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth}

	_, err := handler.triggerAnalyticsCollection(ctx, req, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics collector not configured")
}

func TestHandler_triggerAnalyticsCollection_Error(t *testing.T) {
	ctx := context.Background()
	mockCollector := new(MockAnalyticsCollector)

	mockCollector.On("Collect", ctx).Return(errors.New("collection error"))

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsCollector: mockCollector}

	_, err := handler.triggerAnalyticsCollection(ctx, req, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "collection failed")
}

// TestHandler_triggerAnalyticsCollection_NonAdmin asserts that a non-admin user
// is rejected by requireAdmin, regardless of their broader permission set.
func TestHandler_triggerAnalyticsCollection_NonAdmin(t *testing.T) {
	ctx := context.Background()
	mockCollector := new(MockAnalyticsCollector)
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "user-token").Return(&Session{
		UserID: "user-1",
	}, nil)
	// Not an Administrators-group member: HasPermissionAPI(admin,*) is false,
	// so requireAdmin rejects with 403 (issue #907 group-only authz).
	mockAuth.On("HasPermissionAPI", ctx, "user-1", "admin", "*").Return(false, nil)

	handler := &Handler{auth: mockAuth, analyticsCollector: mockCollector}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer user-token"},
	}
	_, err := handler.triggerAnalyticsCollection(ctx, req, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin access required")
	mockCollector.AssertNotCalled(t, "Collect")
}

func TestParseDateRange(t *testing.T) {
	t.Run("empty strings use defaults", func(t *testing.T) {
		start, end, err := parseDateRange("", "")
		require.NoError(t, err)

		// End should be close to now
		assert.WithinDuration(t, time.Now().UTC(), end, time.Minute)
		// Start should be 7 days before end
		expectedStart := end.AddDate(0, 0, -7)
		assert.WithinDuration(t, expectedStart, start, time.Minute)
	})

	t.Run("RFC3339 format for both dates", func(t *testing.T) {
		startStr := "2024-01-01T00:00:00Z"
		endStr := "2024-01-31T23:59:59Z"

		start, end, err := parseDateRange(startStr, endStr)
		require.NoError(t, err)

		expectedStart, _ := time.Parse(time.RFC3339, startStr)
		expectedEnd, _ := time.Parse(time.RFC3339, endStr)

		assert.Equal(t, expectedStart, start)
		assert.Equal(t, expectedEnd, end)
	})

	t.Run("date-only format for start", func(t *testing.T) {
		startStr := "2024-01-15"
		endStr := "2024-01-31T23:59:59Z"

		start, end, err := parseDateRange(startStr, endStr)
		require.NoError(t, err)

		expectedStart, _ := time.Parse("2006-01-02", startStr)
		assert.Equal(t, expectedStart, start)
		assert.NotEqual(t, time.Time{}, end)
	})

	t.Run("date-only format for end sets end of day", func(t *testing.T) {
		startStr := "2024-01-01T00:00:00Z"
		endStr := "2024-01-15"

		start, end, err := parseDateRange(startStr, endStr)
		require.NoError(t, err)

		assert.NotEqual(t, time.Time{}, start)
		// End should be set to end of day (23:59:59)
		assert.Equal(t, 15, end.Day())
		assert.Equal(t, 23, end.Hour())
	})

	// CR #1049: malformed/ordered-date validation must surface as a 400
	// ClientError so handler.go does not map it to HTTP 500. The plain-error
	// pre-fix code would have passed the substring asserts below but failed the
	// IsClientError/400 asserts.
	t.Run("invalid start date format returns 400 client error", func(t *testing.T) {
		_, _, err := parseDateRange("not-a-date", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid start date")
		ce, ok := IsClientError(err)
		require.True(t, ok, "invalid start date must return a ClientError, got %T", err)
		assert.Equal(t, 400, ce.code)
	})

	t.Run("invalid end date format returns 400 client error", func(t *testing.T) {
		_, _, err := parseDateRange("", "also-not-a-date")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid end date")
		ce, ok := IsClientError(err)
		require.True(t, ok, "invalid end date must return a ClientError, got %T", err)
		assert.Equal(t, 400, ce.code)
	})

	t.Run("start after end returns 400 client error", func(t *testing.T) {
		startStr := "2024-01-31T00:00:00Z"
		endStr := "2024-01-01T00:00:00Z"

		_, _, err := parseDateRange(startStr, endStr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "start date must be before end date")
		ce, ok := IsClientError(err)
		require.True(t, ok, "reversed range must return a ClientError, got %T", err)
		assert.Equal(t, 400, ce.code)
	})

	// Regression #414: unbounded date ranges caused full-table scans (DoS).
	// The cap is 366 days; anything larger must be rejected with 400.
	t.Run("regression #414: range exceeding 366 days is rejected", func(t *testing.T) {
		startStr := "1970-01-01T00:00:00Z"
		endStr := "2100-12-31T00:00:00Z"

		_, _, err := parseDateRange(startStr, endStr)
		require.Error(t, err)
		ce, ok := IsClientError(err)
		require.True(t, ok, "oversized range must return a ClientError")
		assert.Equal(t, 400, ce.code)
		assert.Contains(t, err.Error(), "366")
	})

	t.Run("range of exactly 366 days is accepted", func(t *testing.T) {
		startStr := "2024-01-01T00:00:00Z"
		endStr := "2024-12-31T00:00:00Z" // 365 days later (2024 is a leap year, 366 days total)

		_, _, err := parseDateRange(startStr, endStr)
		require.NoError(t, err)
	})

	t.Run("range of 367 days is rejected", func(t *testing.T) {
		startStr := "2024-01-01T00:00:00Z"
		endStr := "2025-01-02T00:00:00Z" // 366 days + 1 day over

		_, _, err := parseDateRange(startStr, endStr)
		require.Error(t, err)
		ce, ok := IsClientError(err)
		require.True(t, ok, "range of 367 days must return a ClientError")
		assert.Equal(t, 400, ce.code)
	})
}

// MockAnalyticsSnapshotStore implements AnalyticsSnapshotStoreInterface.
type MockAnalyticsSnapshotStore struct {
	mock.Mock
}

func (m *MockAnalyticsSnapshotStore) QuerySavings(ctx context.Context, req analytics.QueryRequest) ([]analytics.SavingsSnapshot, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]analytics.SavingsSnapshot), args.Error(1)
}

func (m *MockAnalyticsSnapshotStore) QueryMonthlyTotals(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string, months int) ([]analytics.MonthlySummary, error) {
	args := m.Called(ctx, accountUUIDs, accountExternalIDsByProvider, months)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]analytics.MonthlySummary), args.Error(1)
}

func (m *MockAnalyticsSnapshotStore) QueryByProvider(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string, start, end time.Time) ([]analytics.ProviderBreakdown, error) {
	args := m.Called(ctx, accountUUIDs, accountExternalIDsByProvider, start, end)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]analytics.ProviderBreakdown), args.Error(1)
}

func (m *MockAnalyticsSnapshotStore) QueryByService(ctx context.Context, accountUUIDs []string, accountExternalIDsByProvider map[string][]string, provider string, start, end time.Time) ([]analytics.ServiceBreakdown, error) {
	args := m.Called(ctx, accountUUIDs, accountExternalIDsByProvider, provider, start, end)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]analytics.ServiceBreakdown), args.Error(1)
}

// TestHandler_getAnalyticsTrends_Success_ScopesByAccount verifies the trends
// endpoint resolves the requested account UUID into the dual-column filter and
// passes it to every Query* call.
func TestHandler_getAnalyticsTrends_Success_ScopesByAccount(t *testing.T) {
	ctx := context.Background()
	accountUUID := "bbbbbbbb-1111-2222-3333-444444444444"
	accountExternal := "999988887777"

	mockSnap := new(MockAnalyticsSnapshotStore)
	wantUUIDs := []string{accountUUID}
	wantExt := map[string][]string{"aws": {accountExternal}}
	mockSnap.On("QueryMonthlyTotals", ctx, wantUUIDs, wantExt, mock.Anything).
		Return([]analytics.MonthlySummary{{Provider: "aws", Service: "rds", TotalSavings: 100}}, nil)
	mockSnap.On("QueryByProvider", ctx, wantUUIDs, wantExt, mock.Anything, mock.Anything).
		Return([]analytics.ProviderBreakdown{{Provider: "aws", TotalSavings: 100}}, nil)
	mockSnap.On("QueryByService", ctx, wantUUIDs, wantExt, "", mock.Anything, mock.Anything).
		Return([]analytics.ServiceBreakdown{{Service: "rds", TotalSavings: 100}}, nil)
	t.Cleanup(func() { mockSnap.AssertExpectations(t) })

	mockStore := new(MockConfigStore)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return []config.CloudAccount{{ID: accountUUID, Name: "Account B", Provider: "aws", ExternalID: accountExternal}}, nil
	}

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsSnapshots: mockSnap, config: mockStore}

	result, err := handler.getAnalyticsTrends(ctx, req, map[string]string{"account_id": accountUUID})
	require.NoError(t, err)
	resp, ok := result.(*TrendsResponse)
	require.True(t, ok)
	assert.Len(t, resp.Monthly, 1)
	assert.Len(t, resp.Provider, 1)
	assert.Len(t, resp.Service, 1)
}

// TestHandler_getAnalyticsTrends_NoStore returns 503 when the snapshot store is
// not configured.
func TestHandler_getAnalyticsTrends_NoStore(t *testing.T) {
	ctx := context.Background()
	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth}

	_, err := handler.getAnalyticsTrends(ctx, req, map[string]string{})
	require.Error(t, err)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 503, ce.code)
}

// TestHandler_getAnalyticsTrends_ScopedUser_RequiresAccountID asserts a scoped
// user cannot issue an unscoped trends query, and the store is never called.
func TestHandler_getAnalyticsTrends_ScopedUser_RequiresAccountID(t *testing.T) {
	ctx := context.Background()
	mockSnap := new(MockAnalyticsSnapshotStore)
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(&Session{UserID: "viewer-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "view", "purchases").Return(true, nil)
	mockAuth.On("GetAllowedAccountsAPI", ctx, "viewer-1").Return([]string{"Production"}, nil)

	handler := &Handler{auth: mockAuth, analyticsSnapshots: mockSnap, config: new(MockConfigStore)}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"Authorization": "Bearer viewer-token"}}

	_, err := handler.getAnalyticsTrends(ctx, req, map[string]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account_id is required")
	mockSnap.AssertNotCalled(t, "QueryMonthlyTotals")
}

// TestHandler_getAnalyticsTrends_ScopedUser_OutsideScope returns not-found when
// a scoped user requests an account outside their allowed_accounts.
func TestHandler_getAnalyticsTrends_ScopedUser_OutsideScope(t *testing.T) {
	ctx := context.Background()
	mockSnap := new(MockAnalyticsSnapshotStore)
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "viewer-token").Return(&Session{UserID: "viewer-1"}, nil)
	mockAuth.On("HasPermissionAPI", ctx, "viewer-1", "view", "purchases").Return(true, nil)
	mockAuth.On("GetAllowedAccountsAPI", ctx, "viewer-1").Return([]string{"Production"}, nil)

	mockStore := new(MockConfigStore)
	mockStore.ListCloudAccountsFn = func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
		return []config.CloudAccount{{ID: "other-acct", Name: "Staging", Provider: "aws", ExternalID: "111122223333"}}, nil
	}

	handler := &Handler{auth: mockAuth, analyticsSnapshots: mockSnap, config: mockStore}
	req := &events.LambdaFunctionURLRequest{Headers: map[string]string{"Authorization": "Bearer viewer-token"}}

	_, err := handler.getAnalyticsTrends(ctx, req, map[string]string{"account_id": "other-acct"})
	require.Error(t, err)
	mockSnap.AssertNotCalled(t, "QueryMonthlyTotals")
}
