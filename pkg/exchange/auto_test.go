package exchange

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockExchangeStore implements RIExchangeStore for testing.
type mockExchangeStore struct {
	savedRecords   []*ExchangeRecord
	cancelledCount int64
	staleRecords   []ExchangeRecord
	dailySpend     string
	dailySpendErr  error
}

func (m *mockExchangeStore) SaveRIExchangeRecord(_ context.Context, record *ExchangeRecord) error {
	if record.ID == "" {
		record.ID = fmt.Sprintf("test-id-%d", len(m.savedRecords))
	}
	m.savedRecords = append(m.savedRecords, record)
	return nil
}

func (m *mockExchangeStore) CancelAllPendingExchanges(_ context.Context) (int64, error) {
	return m.cancelledCount, nil
}

func (m *mockExchangeStore) GetStaleProcessingExchanges(_ context.Context, _ time.Duration) ([]ExchangeRecord, error) {
	return m.staleRecords, nil
}

func (m *mockExchangeStore) GetRIExchangeDailySpend(_ context.Context, _ time.Time) (string, error) {
	if m.dailySpendErr != nil {
		return "", m.dailySpendErr
	}
	return m.dailySpend, nil
}

func (m *mockExchangeStore) CompleteRIExchange(_ context.Context, _ string, _ string) error {
	return nil
}

func (m *mockExchangeStore) FailRIExchange(_ context.Context, _ string, _ string) error {
	return nil
}

// mockExchangeClient implements ExchangeClientInterface for testing.
type mockExchangeClient struct {
	quoteResult   *ExchangeQuoteSummary
	quoteErr      error
	executeResult string
	executeErr    error
}

func (m *mockExchangeClient) GetQuote(_ context.Context, _ ExchangeQuoteRequest) (*ExchangeQuoteSummary, error) {
	return m.quoteResult, m.quoteErr
}

func (m *mockExchangeClient) Execute(_ context.Context, _ ExchangeExecuteRequest) (string, *ExchangeQuoteSummary, error) {
	return m.executeResult, m.quoteResult, m.executeErr
}

func defaultQuote() *ExchangeQuoteSummary {
	due, _ := ParseDecimalRat("0.000000")
	return &ExchangeQuoteSummary{
		IsValidExchange:  true,
		PaymentDueRaw:    "0.000000",
		PaymentDueUSD:    due,
		PaymentDueUSDStr: "0.000000",
		CurrencyCode:     "USD",
	}
}

func defaultParams(store RIExchangeStore, client ExchangeClientInterface) RunAutoExchangeParams {
	return RunAutoExchangeParams{
		Store:          store,
		ExchangeClient: client,
		LookupOffering: func(_ context.Context, _, _, _, _ string, _ int64) (string, error) {
			return "offering-123", nil
		},
		RIs: []RIInfo{
			{ID: "ri-001", InstanceType: "m5.xlarge", InstanceCount: 1, OfferingClass: "convertible", NormalizationFactor: 8},
		},
		Utilization: []UtilizationInfo{
			{RIID: "ri-001", UtilizationPercent: 50.0},
		},
		Config: RIExchangeConfig{
			Mode:                     "manual",
			UtilizationThreshold:     95.0,
			MaxPaymentPerExchangeUSD: 100.0,
			MaxPaymentDailyUSD:       500.0,
			LookbackDays:             30,
		},
		AccountID:    "123456789012",
		Region:       "us-east-1",
		DashboardURL: "https://cudly.example.com",
		RIMetadata: map[string]RIMetadataInfo{
			"ri-001": {ProductDescription: "Linux/UNIX", InstanceTenancy: "default", Scope: "Region", Duration: 31536000},
		},
	}
}

func TestRunAutoExchange_ManualMode_CreatesPendingRecords(t *testing.T) {
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{quoteResult: defaultQuote()}
	params := defaultParams(store, client)

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	assert.Equal(t, "manual", result.Mode)
	assert.Len(t, result.Pending, 1)
	assert.Empty(t, result.Completed)
	assert.Empty(t, result.Failed)

	assert.Equal(t, "ri-001", result.Pending[0].SourceRIID)
	assert.NotEmpty(t, result.Pending[0].ApprovalToken)
	assert.NotEmpty(t, result.Pending[0].RecordID)

	// Store should have one saved record
	require.Len(t, store.savedRecords, 1)
	assert.Equal(t, "pending", store.savedRecords[0].Status)
	assert.Equal(t, "manual", store.savedRecords[0].Mode)
	assert.NotNil(t, store.savedRecords[0].ExpiresAt)
}

func TestRunAutoExchange_AutoMode_ExecutesExchange(t *testing.T) {
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{quoteResult: defaultQuote(), executeResult: "exch-abc-123"}
	params := defaultParams(store, client)
	params.Config.Mode = "auto"

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	assert.Equal(t, "auto", result.Mode)
	assert.Len(t, result.Completed, 1)
	assert.Empty(t, result.Pending)
	assert.Empty(t, result.Failed)

	assert.Equal(t, "exch-abc-123", result.Completed[0].ExchangeID)

	// Store should have a completed record
	require.Len(t, store.savedRecords, 1)
	assert.Equal(t, "completed", store.savedRecords[0].Status)
	assert.Equal(t, "auto", store.savedRecords[0].Mode)
}

func TestRunAutoExchange_NoRecommendations(t *testing.T) {
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{quoteResult: defaultQuote()}
	params := defaultParams(store, client)
	// All RIs well-utilized
	params.Utilization = []UtilizationInfo{{RIID: "ri-001", UtilizationPercent: 99.0}}

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	assert.Empty(t, result.Pending)
	assert.Empty(t, result.Completed)
	assert.Empty(t, result.Failed)
	assert.Empty(t, result.Skipped)
	assert.Empty(t, store.savedRecords)
}

func TestRunAutoExchange_OfferingLookupFails(t *testing.T) {
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{quoteResult: defaultQuote()}
	params := defaultParams(store, client)
	params.LookupOffering = func(_ context.Context, _, _, _, _ string, _ int64) (string, error) {
		return "", fmt.Errorf("no offering found")
	}

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	assert.Len(t, result.Skipped, 1)
	assert.Contains(t, result.Skipped[0].Reason, "no matching offering found")
}

func TestRunAutoExchange_QuoteFails(t *testing.T) {
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{quoteErr: fmt.Errorf("API throttled")}
	params := defaultParams(store, client)

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	assert.Len(t, result.Skipped, 1)
	assert.Contains(t, result.Skipped[0].Reason, "quote failed")
}

func TestRunAutoExchange_PerExchangeCapExceeded(t *testing.T) {
	due, _ := ParseDecimalRat("200.00")
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{quoteResult: &ExchangeQuoteSummary{
		IsValidExchange:  true,
		PaymentDueRaw:    "200.00",
		PaymentDueUSD:    due,
		PaymentDueUSDStr: "200.000000",
		CurrencyCode:     "USD",
	}}
	params := defaultParams(store, client)
	params.Config.MaxPaymentPerExchangeUSD = 100.0

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	assert.Len(t, result.Skipped, 1)
	assert.Contains(t, result.Skipped[0].Reason, "exceeds per-exchange cap")
}

func TestRunAutoExchange_AutoMode_DailyCapExceeded(t *testing.T) {
	store := &mockExchangeStore{dailySpend: "450.00"}
	due, _ := ParseDecimalRat("60.00")
	client := &mockExchangeClient{quoteResult: &ExchangeQuoteSummary{
		IsValidExchange:  true,
		PaymentDueRaw:    "60.00",
		PaymentDueUSD:    due,
		PaymentDueUSDStr: "60.000000",
		CurrencyCode:     "USD",
	}}
	params := defaultParams(store, client)
	params.Config.Mode = "auto"
	params.Config.MaxPaymentPerExchangeUSD = 100.0
	params.Config.MaxPaymentDailyUSD = 500.0

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	assert.Len(t, result.Failed, 1)
	assert.Contains(t, result.Failed[0].Error, "daily cap exceeded")
}

func TestRunAutoExchange_AutoMode_DailySpendDBError_FailsClosed(t *testing.T) {
	store := &mockExchangeStore{dailySpendErr: fmt.Errorf("connection refused")}
	client := &mockExchangeClient{quoteResult: defaultQuote()}
	params := defaultParams(store, client)
	params.Config.Mode = "auto"

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	assert.Len(t, result.Failed, 1)
	assert.Contains(t, result.Failed[0].Error, "daily cap check failed")
}

func TestRunAutoExchange_AutoMode_ExecutionFails(t *testing.T) {
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{
		quoteResult: defaultQuote(),
		executeErr:  fmt.Errorf("exchange not valid"),
	}
	params := defaultParams(store, client)
	params.Config.Mode = "auto"

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	assert.Len(t, result.Failed, 1)
	assert.Contains(t, result.Failed[0].Error, "exchange not valid")
	assert.Empty(t, result.Completed)

	// Failed record should be saved
	require.Len(t, store.savedRecords, 1)
	assert.Equal(t, "failed", store.savedRecords[0].Status)
}

func TestRunAutoExchange_InvalidExchange(t *testing.T) {
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{quoteResult: &ExchangeQuoteSummary{
		IsValidExchange:         false,
		ValidationFailureReason: "source RI expired",
	}}
	params := defaultParams(store, client)

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	assert.Len(t, result.Skipped, 1)
	assert.Contains(t, result.Skipped[0].Reason, "invalid exchange")
}

func TestRunAutoExchange_IdleRI_Skipped(t *testing.T) {
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{quoteResult: defaultQuote()}
	params := defaultParams(store, client)
	params.Utilization = []UtilizationInfo{{RIID: "ri-001", UtilizationPercent: 0.0}}

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	assert.Len(t, result.Skipped, 1)
	assert.Contains(t, result.Skipped[0].Reason, "idle")
}
