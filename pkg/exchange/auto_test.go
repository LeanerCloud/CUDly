package exchange

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// mockExchangeStore implements RIExchangeStore for testing.
type mockExchangeStore struct {
	savedRecords   []*ExchangeRecord
	cancelledCount int64
	staleRecords   []ExchangeRecord
	dailySpend     string
	dailySpendErr  error
	// cancelByOriginLast captures the origin argument of the last
	// CancelPendingExchangesByOrigin call for assertion in scoping tests.
	cancelByOriginLast *common.ExchangeOrigin
	// saveErrFor, when non-nil, is called for each SaveRIExchangeRecord call.
	// Returning a non-nil error simulates a DB write failure for that record.
	// Use this to inject ledger-write failures without affecting other saves.
	saveErrFor func(record *ExchangeRecord) error
}

func (m *mockExchangeStore) SaveRIExchangeRecord(_ context.Context, record *ExchangeRecord) error {
	if m.saveErrFor != nil {
		if err := m.saveErrFor(record); err != nil {
			return err
		}
	}
	if record.ID == "" {
		record.ID = fmt.Sprintf("test-id-%d", len(m.savedRecords))
	}
	m.savedRecords = append(m.savedRecords, record)
	return nil
}

func (m *mockExchangeStore) CancelAllPendingExchanges(_ context.Context) (int64, error) {
	return m.cancelledCount, nil
}

func (m *mockExchangeStore) CancelPendingExchangesByOrigin(_ context.Context, origin common.ExchangeOrigin) (int64, error) {
	m.cancelByOriginLast = &origin
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

// testifyExchangeStore is a testify mock.Mock-based store for tests that need
// mock.AssertExpectations enforcement (e.g. the dry-run test that must assert
// zero mutations were attempted).
type testifyExchangeStore struct {
	mock.Mock
}

func (m *testifyExchangeStore) SaveRIExchangeRecord(ctx context.Context, record *ExchangeRecord) error {
	args := m.Called(ctx, record)
	return args.Error(0)
}

func (m *testifyExchangeStore) CancelAllPendingExchanges(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *testifyExchangeStore) CancelPendingExchangesByOrigin(ctx context.Context, origin common.ExchangeOrigin) (int64, error) {
	args := m.Called(ctx, origin)
	return args.Get(0).(int64), args.Error(1)
}

func (m *testifyExchangeStore) GetStaleProcessingExchanges(ctx context.Context, olderThan time.Duration) ([]ExchangeRecord, error) {
	args := m.Called(ctx, olderThan)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]ExchangeRecord), args.Error(1)
}

func (m *testifyExchangeStore) GetRIExchangeDailySpend(ctx context.Context, date time.Time) (string, error) {
	args := m.Called(ctx, date)
	return args.String(0), args.Error(1)
}

func (m *testifyExchangeStore) CompleteRIExchange(ctx context.Context, id string, exchangeID string) error {
	args := m.Called(ctx, id, exchangeID)
	return args.Error(0)
}

func (m *testifyExchangeStore) FailRIExchange(ctx context.Context, id string, errMsg string) error {
	args := m.Called(ctx, id, errMsg)
	return args.Error(0)
}

// mockExchangeClient implements ExchangeClientInterface for testing.
type mockExchangeClient struct {
	quoteResult   *ExchangeQuoteSummary
	quoteErr      error
	executeResult string
	executeErr    error
	executeCalls  int
	// executeRequests captures each ExchangeExecuteRequest passed to Execute.
	// Use this to assert that MaxPaymentDueUSD is bounded correctly (H2).
	executeRequests []ExchangeExecuteRequest
	// executeQuoteResult, when non-nil, is returned as the quote from Execute
	// instead of quoteResult. Use this to simulate a fresh quote whose amount
	// differs from the pre-execution GetQuote result (H3 test).
	executeQuoteResult *ExchangeQuoteSummary
}

func (m *mockExchangeClient) GetQuote(_ context.Context, _ ExchangeQuoteRequest) (*ExchangeQuoteSummary, error) {
	return m.quoteResult, m.quoteErr
}

func (m *mockExchangeClient) Execute(_ context.Context, req ExchangeExecuteRequest) (string, *ExchangeQuoteSummary, error) {
	m.executeCalls++
	m.executeRequests = append(m.executeRequests, req)
	q := m.quoteResult
	if m.executeQuoteResult != nil {
		q = m.executeQuoteResult
	}
	return m.executeResult, q, m.executeErr
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{quoteErr: fmt.Errorf("API throttled")}
	params := defaultParams(store, client)

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	assert.Len(t, result.Skipped, 1)
	assert.Contains(t, result.Skipped[0].Reason, "quote failed")
}

func TestRunAutoExchange_PerExchangeCapExceeded(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	store := &mockExchangeStore{dailySpendErr: fmt.Errorf("connection refused")}
	client := &mockExchangeClient{quoteResult: defaultQuote()}
	params := defaultParams(store, client)
	params.Config.Mode = "auto"

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	assert.Len(t, result.Failed, 1)
	assert.Contains(t, result.Failed[0].Error, "daily cap check failed")
}

// TestProcessAutoExchange_UnparseablePaymentDue_FailsClosed is the regression
// test for COR-04: an unparseable or empty paymentDueStr must abort the
// exchange and persist a failed record, never count as $0 toward the
// MaxPaymentDailyUSD cap. RunAutoExchange always builds a parseable string
// today, so processAutoExchange is exercised directly with the bad inputs.
func TestProcessAutoExchange_UnparseablePaymentDue_FailsClosed(t *testing.T) {
	t.Parallel()
	for _, paymentDueStr := range []string{"not-a-number", ""} {
		t.Run(fmt.Sprintf("paymentDue=%q", paymentDueStr), func(t *testing.T) {
			t.Parallel()
			store := &mockExchangeStore{dailySpend: "0"}
			client := &mockExchangeClient{executeResult: "exch-should-not-happen"}
			params := defaultParams(store, client)
			params.Config.Mode = "auto"

			rec := ReshapeRecommendation{
				SourceRIID:         "ri-001",
				SourceInstanceType: "m5.xlarge",
				TargetInstanceType: "m5.large",
				SourceCount:        1,
				TargetCount:        2,
				UtilizationPercent: 50.0,
			}
			perExchangeCap := new(big.Rat).SetFloat64(params.Config.MaxPaymentPerExchangeUSD)

			outcome, halt := processAutoExchange(context.Background(), params, rec, "offering-123", paymentDueStr, perExchangeCap)

			assert.Contains(t, outcome.Error, "failed to parse payment due")
			assert.Empty(t, outcome.ExchangeID)
			assert.False(t, halt, "parse failure should not halt subsequent exchanges")
			assert.Zero(t, client.executeCalls, "exchange must not execute on unparseable payment due")

			// A failed record must be persisted for the audit trail,
			// mirroring the dailySpent parse-failure branch.
			require.Len(t, store.savedRecords, 1)
			assert.Equal(t, "failed", store.savedRecords[0].Status)
			assert.Contains(t, store.savedRecords[0].Error, "failed to parse payment due")
		})
	}
}

func TestRunAutoExchange_AutoMode_ExecutionFails(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{quoteResult: defaultQuote()}
	params := defaultParams(store, client)
	params.Utilization = []UtilizationInfo{{RIID: "ri-001", UtilizationPercent: 0.0}}

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	assert.Len(t, result.Skipped, 1)
	assert.Contains(t, result.Skipped[0].Reason, "idle")
}

// ─── Scoped cancellation tests ────────────────────────────────────────────────

// TestRunAutoExchange_StandaloneDoesNotCancelLadderPendings is the regression
// test for gap G10: before this fix, RunAutoExchange called
// CancelAllPendingExchanges (store-wide), so the standalone ri_exchange_reshape
// task would silently wipe out ladder-linked pending reshapes. After the fix,
// the standalone run calls CancelPendingExchangesByOrigin with
// common.ExchangeOriginStandalone, which only cancels records where
// ladder_run_id IS NULL.
func TestRunAutoExchange_StandaloneDoesNotCancelLadderPendings(t *testing.T) {
	t.Parallel()
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{quoteResult: defaultQuote()}
	params := defaultParams(store, client)
	// LadderRunID=nil means standalone origin.
	params.LadderRunID = nil

	_, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	// Must have called the scoped cancel with the standalone origin, NOT the
	// store-wide CancelAllPendingExchanges. cancelByOriginLast tracks the
	// argument passed to CancelPendingExchangesByOrigin.
	require.NotNil(t, store.cancelByOriginLast,
		"CancelPendingExchangesByOrigin must have been called (not the store-wide variant)")
	assert.Equal(t, common.ExchangeOriginStandalone, *store.cancelByOriginLast,
		"standalone run must cancel only non-ladder pendings (origin=standalone)")
}

// TestRunAutoExchange_LadderDoesNotCancelStandalonePendings verifies that a
// ladder-originated run calls CancelPendingExchangesByOrigin with
// common.ExchangeOriginLadder, which only cancels records where ladder_run_id
// IS NOT NULL, leaving standalone pending records intact.
func TestRunAutoExchange_LadderDoesNotCancelStandalonePendings(t *testing.T) {
	t.Parallel()
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{quoteResult: defaultQuote()}
	params := defaultParams(store, client)
	ladderRunID := "ladder-run-abc-123"
	params.LadderRunID = &ladderRunID

	_, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	require.NotNil(t, store.cancelByOriginLast,
		"CancelPendingExchangesByOrigin must have been called")
	assert.Equal(t, common.ExchangeOriginLadder, *store.cancelByOriginLast,
		"ladder run must cancel only ladder-linked pendings (origin=ladder)")
}

// ─── Dry-run tests ────────────────────────────────────────────────────────────

// TestRunAutoExchange_DryRun_ManualMode_ZeroMutations verifies that a dry-run
// in manual mode produces simulated pending outcomes without any store writes
// or cancellation calls. mock.AssertExpectations enforces the zero-mutation
// contract (fail-loud gate: any registered-but-uncalled or called-but-unregistered
// method on a testify mock fails the test).
func TestRunAutoExchange_DryRun_ManualMode_ZeroMutations(t *testing.T) {
	t.Parallel()
	store := &testifyExchangeStore{}
	// Only GetStaleProcessingExchanges is a read-only call expected in dry-run.
	store.On("GetStaleProcessingExchanges", mock.Anything, staleProcessingThreshold).
		Return(nil, nil)
	t.Cleanup(func() { store.AssertExpectations(t) })

	client := &mockExchangeClient{quoteResult: defaultQuote()}
	params := defaultParams(store, client)
	params.DryRun = true

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	// Outcome must be simulated — no real token, no record ID.
	require.Len(t, result.Pending, 1)
	assert.True(t, result.Pending[0].Simulated, "outcome must be tagged Simulated in dry-run")
	assert.Empty(t, result.Pending[0].ApprovalToken, "no live approval token must be generated in dry-run")
	assert.Empty(t, result.Pending[0].RecordID, "no record must be saved in dry-run")
}

// TestRunAutoExchange_DryRun_AutoMode_ZeroMutations verifies that a dry-run
// in auto mode produces simulated completed outcomes without executing any
// exchange or saving any record.
func TestRunAutoExchange_DryRun_AutoMode_ZeroMutations(t *testing.T) {
	t.Parallel()
	store := &testifyExchangeStore{}
	store.On("GetStaleProcessingExchanges", mock.Anything, staleProcessingThreshold).
		Return(nil, nil)
	t.Cleanup(func() { store.AssertExpectations(t) })

	client := &mockExchangeClient{quoteResult: defaultQuote(), executeResult: "exch-dryrun-ignored"}
	params := defaultParams(store, client)
	params.Config.Mode = "auto"
	params.DryRun = true

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	// Auto mode dry-run lands in Completed (would have executed) but Simulated.
	require.Len(t, result.Completed, 1)
	assert.True(t, result.Completed[0].Simulated, "auto dry-run outcome must be tagged Simulated")
	assert.Empty(t, result.Completed[0].ExchangeID, "no real exchange must be executed in dry-run")
}

// ─── LadderRunID round-trip test ──────────────────────────────────────────────

// TestRunAutoExchange_LadderRunID_StampedOnRecords verifies that when
// RunAutoExchangeParams.LadderRunID is set, every saved record (pending,
// completed, failed) carries that ID so the DB column is actually written.
func TestRunAutoExchange_LadderRunID_StampedOnRecords(t *testing.T) {
	t.Parallel()
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{quoteResult: defaultQuote()}
	params := defaultParams(store, client)
	ladderRunID := "ladder-run-999"
	params.LadderRunID = &ladderRunID

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	require.Len(t, result.Pending, 1, "expected one pending record in manual mode")
	require.Len(t, store.savedRecords, 1)
	require.NotNil(t, store.savedRecords[0].LadderRunID, "LadderRunID must be non-nil")
	assert.Equal(t, ladderRunID, *store.savedRecords[0].LadderRunID,
		"saved record must carry the LadderRunID from params")
}

// TestRunAutoExchange_NoLadderRunID_RecordHasNilLadderRunID verifies that
// standalone (LadderRunID=nil) runs save records with nil LadderRunID, so
// the DB column remains NULL and CancelPendingExchangesByOrigin(false) can
// correctly target them.
func TestRunAutoExchange_NoLadderRunID_RecordHasNilLadderRunID(t *testing.T) {
	t.Parallel()
	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{quoteResult: defaultQuote()}
	params := defaultParams(store, client)
	params.LadderRunID = nil

	_, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	require.Len(t, store.savedRecords, 1)
	assert.Nil(t, store.savedRecords[0].LadderRunID,
		"standalone run must save record with nil LadderRunID")
}

// ─── H2: effective cap bounded by daily headroom ──────────────────────────────

// TestProcessAutoExchange_EffectiveCap_BoundedByDailyHeadroom is the regression
// test for H2: Execute's MaxPaymentDueUSD must be the minimum of perExchangeCap
// and (dailyCap - dailySpent) so a fresh re-quote cannot accept an amount that
// would exceed daily headroom.
//
// Setup: dailyCap=$500, dailySpent=$450, perExchangeCap=$100.
// Headroom = $500-$450 = $50 < $100 perExchangeCap.
// Expected: MaxPaymentDueUSD passed to Execute = $50 (headroom).
func TestProcessAutoExchange_EffectiveCap_BoundedByDailyHeadroom(t *testing.T) {
	t.Parallel()

	due, _ := ParseDecimalRat("40.000000")
	store := &mockExchangeStore{dailySpend: "450.00"}
	client := &mockExchangeClient{
		quoteResult:   defaultQuote(),
		executeResult: "exch-h2-test",
		// The fresh Execute quote is $40, which is under the effective cap ($50).
		executeQuoteResult: &ExchangeQuoteSummary{
			IsValidExchange:  true,
			PaymentDueRaw:    "40.000000",
			PaymentDueUSD:    due,
			PaymentDueUSDStr: "40.000000",
			CurrencyCode:     "USD",
		},
	}
	params := defaultParams(store, client)
	params.Config.Mode = "auto"
	params.Config.MaxPaymentDailyUSD = 500.0
	params.Config.MaxPaymentPerExchangeUSD = 100.0

	rec := ReshapeRecommendation{
		SourceRIID:         "ri-001",
		SourceInstanceType: "m5.xlarge",
		TargetInstanceType: "m5.large",
		SourceCount:        1,
		TargetCount:        2,
		UtilizationPercent: 50.0,
	}
	perExchangeCap := new(big.Rat).SetFloat64(params.Config.MaxPaymentPerExchangeUSD)

	outcome, halt := processAutoExchange(context.Background(), params, rec, "offering-123", "40.000000", perExchangeCap)

	require.Empty(t, outcome.Error, "exchange should succeed within effective cap")
	assert.False(t, halt)
	assert.Equal(t, "exch-h2-test", outcome.ExchangeID)

	// Assert that Execute was called with MaxPaymentDueUSD = $50 (headroom),
	// not $100 (perExchangeCap).
	require.Len(t, client.executeRequests, 1)
	gotCap := client.executeRequests[0].MaxPaymentDueUSD
	require.NotNil(t, gotCap)
	expectedCap := new(big.Rat) // $50
	expectedCap.SetFrac64(50, 1)
	assert.Equal(t, 0, gotCap.Cmp(expectedCap),
		"MaxPaymentDueUSD passed to Execute must equal dailyHeadroom ($50), got $%s", gotCap.FloatString(2))
}

// TestProcessAutoExchange_EffectiveCap_UsesPerExchangeCapWhenSmaller verifies
// that when perExchangeCap < dailyHeadroom, perExchangeCap is used as-is.
func TestProcessAutoExchange_EffectiveCap_UsesPerExchangeCapWhenSmaller(t *testing.T) {
	t.Parallel()

	store := &mockExchangeStore{dailySpend: "0"} // headroom = $500
	client := &mockExchangeClient{
		quoteResult:   defaultQuote(),
		executeResult: "exch-h2b-test",
	}
	params := defaultParams(store, client)
	params.Config.Mode = "auto"
	params.Config.MaxPaymentDailyUSD = 500.0
	params.Config.MaxPaymentPerExchangeUSD = 100.0

	rec := ReshapeRecommendation{
		SourceRIID:         "ri-001",
		SourceInstanceType: "m5.xlarge",
		TargetInstanceType: "m5.large",
		SourceCount:        1,
		TargetCount:        2,
		UtilizationPercent: 50.0,
	}
	perExchangeCap := new(big.Rat).SetFloat64(params.Config.MaxPaymentPerExchangeUSD)

	_, halt := processAutoExchange(context.Background(), params, rec, "offering-123", "0.000000", perExchangeCap)
	assert.False(t, halt)

	require.Len(t, client.executeRequests, 1)
	gotCap := client.executeRequests[0].MaxPaymentDueUSD
	require.NotNil(t, gotCap)
	// Headroom ($500) > perExchangeCap ($100), so effectiveCap = perExchangeCap.
	expectedCap := new(big.Rat).SetFloat64(100.0)
	assert.Equal(t, 0, gotCap.Cmp(expectedCap),
		"MaxPaymentDueUSD must equal perExchangeCap ($100) when headroom is larger, got $%s", gotCap.FloatString(2))
}

// ─── H3: accepted amount from fresh Execute quote ─────────────────────────────

// TestProcessAutoExchange_AcceptedAmountFromFreshQuote is the regression test
// for H3: the completed record's PaymentDue must reflect the amount AWS
// confirmed during Execute, not the stale pre-execution GetQuote amount.
func TestProcessAutoExchange_AcceptedAmountFromFreshQuote(t *testing.T) {
	t.Parallel()

	preQuoteDue, _ := ParseDecimalRat("30.000000")
	freshDue, _ := ParseDecimalRat("35.500000") // AWS re-quoted higher (within cap)

	store := &mockExchangeStore{dailySpend: "0"}
	client := &mockExchangeClient{
		quoteResult: &ExchangeQuoteSummary{
			IsValidExchange:  true,
			PaymentDueRaw:    "30.000000",
			PaymentDueUSD:    preQuoteDue,
			PaymentDueUSDStr: "30.000000",
			CurrencyCode:     "USD",
		},
		executeResult: "exch-h3-test",
		// Execute returns a higher accepted amount.
		executeQuoteResult: &ExchangeQuoteSummary{
			IsValidExchange:  true,
			PaymentDueRaw:    "35.500000",
			PaymentDueUSD:    freshDue,
			PaymentDueUSDStr: "35.500000",
			CurrencyCode:     "USD",
		},
	}
	params := defaultParams(store, client)
	params.Config.Mode = "auto"
	params.Config.MaxPaymentDailyUSD = 500.0
	params.Config.MaxPaymentPerExchangeUSD = 100.0

	rec := ReshapeRecommendation{
		SourceRIID:         "ri-001",
		SourceInstanceType: "m5.xlarge",
		TargetInstanceType: "m5.large",
		SourceCount:        1,
		TargetCount:        2,
		UtilizationPercent: 50.0,
	}
	perExchangeCap := new(big.Rat).SetFloat64(params.Config.MaxPaymentPerExchangeUSD)

	outcome, halt := processAutoExchange(context.Background(), params, rec, "offering-123", "30.000000", perExchangeCap)

	require.Empty(t, outcome.Error)
	assert.False(t, halt)

	// The saved record must carry the FRESH accepted amount, not the pre-quote.
	require.Len(t, store.savedRecords, 1)
	assert.Equal(t, "35.500000", store.savedRecords[0].PaymentDue,
		"completed record must store the amount AWS actually accepted, not the pre-execution quote")
}

// ─── H4: halt on persistent ledger-save failure ──────────────────────────────

// TestProcessAutoExchange_LedgerSaveFailure_HaltsAndReturnsError is the
// regression test for H4: when SaveRIExchangeRecord fails for a completed
// exchange (money has already moved), processAutoExchange must retry and, on
// persistent failure, return halt=true to stop further exchanges.
func TestProcessAutoExchange_LedgerSaveFailure_HaltsAndReturnsError(t *testing.T) {
	t.Parallel()

	store := &mockExchangeStore{
		dailySpend: "0",
		saveErrFor: func(r *ExchangeRecord) error {
			if r.Status == "completed" {
				return fmt.Errorf("DB connection refused")
			}
			return nil
		},
	}
	client := &mockExchangeClient{
		quoteResult:   defaultQuote(),
		executeResult: "exch-h4-test",
	}
	params := defaultParams(store, client)
	params.Config.Mode = "auto"

	rec := ReshapeRecommendation{
		SourceRIID:         "ri-001",
		SourceInstanceType: "m5.xlarge",
		TargetInstanceType: "m5.large",
		SourceCount:        1,
		TargetCount:        2,
		UtilizationPercent: 50.0,
	}
	perExchangeCap := new(big.Rat).SetFloat64(params.Config.MaxPaymentPerExchangeUSD)

	outcome, halt := processAutoExchange(context.Background(), params, rec, "offering-123", "0.000000", perExchangeCap)

	// Execute succeeded but ledger write failed: outcome must carry an error
	// and halt must be true to prevent cap bypass.
	assert.Contains(t, outcome.Error, "ledger save failed")
	assert.Equal(t, "exch-h4-test", outcome.ExchangeID,
		"ExchangeID must be set even when ledger write fails (money moved)")
	assert.True(t, halt, "persistent ledger failure must signal halt to stop further exchanges")
}

// TestRunAutoExchange_LedgerSaveFailure_StopsAfterFirstExchange verifies that
// when the first exchange's ledger write fails (H4), RunAutoExchange does not
// proceed to a second exchange recommendation.
func TestRunAutoExchange_LedgerSaveFailure_StopsAfterFirstExchange(t *testing.T) {
	t.Parallel()

	store := &mockExchangeStore{
		dailySpend: "0",
		saveErrFor: func(r *ExchangeRecord) error {
			if r.Status == "completed" {
				return fmt.Errorf("DB write failed")
			}
			return nil
		},
	}
	client := &mockExchangeClient{
		quoteResult:   defaultQuote(),
		executeResult: "exch-stop-test",
	}
	params := defaultParams(store, client)
	params.Config.Mode = "auto"
	// Add a second RI so there are two recommendations.
	params.RIs = append(params.RIs, RIInfo{
		ID: "ri-002", InstanceType: "m5.xlarge", InstanceCount: 1,
		OfferingClass: "convertible", NormalizationFactor: 8,
	})
	params.Utilization = append(params.Utilization,
		UtilizationInfo{RIID: "ri-002", UtilizationPercent: 50.0})
	params.RIMetadata["ri-002"] = RIMetadataInfo{
		ProductDescription: "Linux/UNIX", InstanceTenancy: "default",
		Scope: "Region", Duration: 31536000,
	}

	result, err := RunAutoExchange(context.Background(), params)
	require.NoError(t, err)

	// First exchange executes but ledger fails -> appears in Failed.
	// Second exchange must NOT have been attempted.
	assert.Len(t, result.Failed, 1, "exactly one failed outcome (the ledger failure)")
	assert.Empty(t, result.Completed, "no completed outcomes when ledger fails")
	assert.Equal(t, 1, client.executeCalls,
		"Execute must be called exactly once; second exchange must be halted")
}
