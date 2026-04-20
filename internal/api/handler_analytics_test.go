package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockAnalyticsClient is a mock implementation of AnalyticsClientInterface
type MockAnalyticsClient struct {
	mock.Mock
}

func (m *MockAnalyticsClient) QueryHistory(ctx context.Context, accountID string, start, end time.Time, interval string) ([]HistoryDataPoint, *HistorySummary, error) {
	args := m.Called(ctx, accountID, start, end, interval)
	if args.Get(0) == nil {
		return nil, nil, args.Error(2)
	}
	var summary *HistorySummary
	if args.Get(1) != nil {
		summary = args.Get(1).(*HistorySummary)
	}
	return args.Get(0).([]HistoryDataPoint), summary, args.Error(2)
}

func (m *MockAnalyticsClient) QueryBreakdown(ctx context.Context, accountID string, start, end time.Time, dimension string) (map[string]BreakdownValue, error) {
	args := m.Called(ctx, accountID, start, end, dimension)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]BreakdownValue), args.Error(1)
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
		Role:   "admin",
	}, nil)
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

	mockClient.On("QueryHistory", ctx, "account-123", mock.Anything, mock.Anything, "hourly").Return(dataPoints, summary, nil)

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient}

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
	mockClient.On("QueryHistory", ctx, "", mock.Anything, mock.Anything, "hourly").Return(dataPoints, (*HistorySummary)(nil), nil)

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient}

	params := map[string]string{} // No interval specified

	_, err := handler.getHistoryAnalytics(ctx, req, params)
	require.NoError(t, err)

	mockClient.AssertCalled(t, "QueryHistory", ctx, "", mock.Anything, mock.Anything, "hourly")
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

	mockClient.On("QueryHistory", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil, errors.New("query failed"))

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient}

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
		Role:   "user",
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

	mockClient.On("QueryBreakdown", ctx, "account-123", mock.Anything, mock.Anything, "service").Return(breakdownData, nil)

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient}

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
	mockClient.On("QueryBreakdown", ctx, "", mock.Anything, mock.Anything, "service").Return(breakdownData, nil)

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient}

	params := map[string]string{} // No dimension specified

	_, err := handler.getHistoryBreakdown(ctx, req, params)
	require.NoError(t, err)

	mockClient.AssertCalled(t, "QueryBreakdown", ctx, "", mock.Anything, mock.Anything, "service")
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

	mockClient.On("QueryBreakdown", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("breakdown failed"))

	mockAuth, req := adminAnalyticsReq(ctx)
	handler := &Handler{auth: mockAuth, analyticsClient: mockClient}

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
		Role:   "user",
	}, nil)

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

	t.Run("invalid start date format", func(t *testing.T) {
		_, _, err := parseDateRange("not-a-date", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid start date")
	})

	t.Run("invalid end date format", func(t *testing.T) {
		_, _, err := parseDateRange("", "also-not-a-date")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid end date")
	})

	t.Run("start after end returns error", func(t *testing.T) {
		startStr := "2024-01-31T00:00:00Z"
		endStr := "2024-01-01T00:00:00Z"

		_, _, err := parseDateRange(startStr, endStr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "start date must be before end date")
	})
}
