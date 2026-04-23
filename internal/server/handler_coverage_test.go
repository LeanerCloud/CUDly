package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/testutil"
	"github.com/LeanerCloud/CUDly/pkg/exchange"
	"github.com/jackc/pgx/v5"
)

// ----- handleCleanupExpiredRecords -----

func TestHandleCleanupExpiredRecords_WithAuthAndConfig(t *testing.T) {
	ctx := testutil.TestContext(t)

	configStore := &mockConfigStoreForHealth{}
	authService := auth.NewService(auth.ServiceConfig{
		Store: &mockAuthStoreForHealth{},
	})

	app := &Application{
		Config: configStore,
		Auth:   authService,
	}

	result, err := app.handleCleanupExpiredRecords(ctx)
	testutil.AssertNoError(t, err)
	testutil.AssertTrue(t, result != nil, "expected non-nil result map")
	_, hasSessionsKey := result["sessions_deleted"]
	testutil.AssertTrue(t, hasSessionsKey, "expected sessions_deleted key")
}

func TestHandleCleanupExpiredRecords_NilAuthAndConfig(t *testing.T) {
	ctx := testutil.TestContext(t)

	app := &Application{Auth: nil, Config: nil}

	result, err := app.handleCleanupExpiredRecords(ctx)
	testutil.AssertNoError(t, err)
	testutil.AssertTrue(t, result != nil, "expected non-nil result map")
}

// ----- handleRefreshAnalytics -----

type mockAnalyticsStore struct {
	refreshErr error
}

func (m *mockAnalyticsStore) RefreshMaterializedViews(ctx context.Context) error {
	return m.refreshErr
}

func TestHandleRefreshAnalytics_Success(t *testing.T) {
	ctx := testutil.TestContext(t)
	app := &Application{
		Analytics: &mockAnalyticsStore{},
	}

	result, err := app.handleRefreshAnalytics(ctx)
	testutil.AssertNoError(t, err)
	testutil.AssertEqual(t, "success", result["status"])
}

func TestHandleRefreshAnalytics_RefreshError(t *testing.T) {
	ctx := testutil.TestContext(t)
	app := &Application{
		Analytics: &mockAnalyticsStore{refreshErr: errors.New("views locked")},
	}

	result, err := app.handleRefreshAnalytics(ctx)
	testutil.AssertNoError(t, err) // error is logged but not propagated
	testutil.AssertEqual(t, "partial", result["status"])
}

func TestHandleRefreshAnalytics_NilAnalytics(t *testing.T) {
	ctx := testutil.TestContext(t)
	app := &Application{Analytics: nil}

	result, err := app.handleRefreshAnalytics(ctx)
	testutil.AssertNoError(t, err)
	testutil.AssertTrue(t, result != nil, "expected non-nil result")
}

// ----- configExchangeStoreAdapter — CompleteRIExchange / FailRIExchange -----

func TestConfigExchangeStoreAdapter_CompleteRIExchange(t *testing.T) {
	ctx := testutil.TestContext(t)

	var completedID, completedExchangeID string
	store := &mockConfigStoreForExchange{
		mockConfigStoreForHealth: mockConfigStoreForHealth{},
	}
	// Override CompleteRIExchange via embedding: use the base mock which returns nil
	// Then verify the call reached the underlying store via a custom wrapper.
	type customStore struct {
		mockConfigStoreForHealth
		completeFunc func(ctx context.Context, id, exchangeID string) error
	}
	cs := &struct {
		mockConfigStoreForExchange
		completeOverride func(ctx context.Context, id, exchangeID string) error
	}{
		mockConfigStoreForExchange: *store,
		completeOverride: func(ctx context.Context, id, exID string) error {
			completedID = id
			completedExchangeID = exID
			return nil
		},
	}
	_ = cs

	// Use a simpler approach: directly test the adapter with the mock store.
	called := false
	type storeWithComplete struct {
		mockConfigStoreForExchange
	}
	var completeStore config.StoreInterface = &mockConfigStoreForExchangeComplete{
		mockConfigStoreForExchange: mockConfigStoreForExchange{},
		completeFunc: func(ctx context.Context, id, exID string) error {
			called = true
			completedID = id
			completedExchangeID = exID
			return nil
		},
	}

	adapter := newConfigExchangeStoreAdapter(completeStore)
	err := adapter.CompleteRIExchange(ctx, "record-1", "exch-1")
	testutil.AssertNoError(t, err)
	testutil.AssertEqual(t, true, called)
	testutil.AssertEqual(t, "record-1", completedID)
	testutil.AssertEqual(t, "exch-1", completedExchangeID)
}

func TestConfigExchangeStoreAdapter_FailRIExchange(t *testing.T) {
	ctx := testutil.TestContext(t)

	var failedID, failedMsg string
	var failStore config.StoreInterface = &mockConfigStoreForExchangeFail{
		mockConfigStoreForExchange: mockConfigStoreForExchange{},
		failFunc: func(ctx context.Context, id, msg string) error {
			failedID = id
			failedMsg = msg
			return nil
		},
	}

	adapter := newConfigExchangeStoreAdapter(failStore)
	err := adapter.FailRIExchange(ctx, "record-2", "timeout error")
	testutil.AssertNoError(t, err)
	testutil.AssertEqual(t, "record-2", failedID)
	testutil.AssertEqual(t, "timeout error", failedMsg)
}

// ----- configToExchangeRecord -----

func TestConfigToExchangeRecord(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	src := &config.RIExchangeRecord{
		ID:                 "id-1",
		AccountID:          "acc-1",
		ExchangeID:         "exch-1",
		Region:             "us-east-1",
		SourceRIIDs:        []string{"ri-1", "ri-2"},
		SourceInstanceType: "m5.large",
		SourceCount:        2,
		TargetOfferingID:   "off-1",
		TargetInstanceType: "m6i.large",
		TargetCount:        2,
		PaymentDue:         "5.00",
		Status:             "completed",
		ApprovalToken:      "token-abc",
		Error:              "",
		Mode:               "auto",
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	got := configToExchangeRecord(src)

	testutil.AssertEqual(t, src.ID, got.ID)
	testutil.AssertEqual(t, src.AccountID, got.AccountID)
	testutil.AssertEqual(t, src.ExchangeID, got.ExchangeID)
	testutil.AssertEqual(t, src.Region, got.Region)
	testutil.AssertEqual(t, 2, len(got.SourceRIIDs))
	testutil.AssertEqual(t, "ri-1", got.SourceRIIDs[0])
	testutil.AssertEqual(t, src.SourceInstanceType, got.SourceInstanceType)
	testutil.AssertEqual(t, src.TargetInstanceType, got.TargetInstanceType)
	testutil.AssertEqual(t, src.PaymentDue, got.PaymentDue)
	testutil.AssertEqual(t, src.Status, got.Status)
	testutil.AssertEqual(t, src.ApprovalToken, got.ApprovalToken)
	testutil.AssertEqual(t, src.Mode, got.Mode)
}

// ----- GetStaleProcessingExchanges (adapter) -----

func TestConfigExchangeStoreAdapter_GetStaleProcessingExchanges_Empty(t *testing.T) {
	ctx := testutil.TestContext(t)

	store := &mockConfigStoreForExchange{}
	adapter := newConfigExchangeStoreAdapter(store)

	records, err := adapter.GetStaleProcessingExchanges(ctx, 30*time.Minute)
	testutil.AssertNoError(t, err)
	testutil.AssertEqual(t, 0, len(records))
}

func TestConfigExchangeStoreAdapter_GetStaleProcessingExchanges_WithRecords(t *testing.T) {
	ctx := testutil.TestContext(t)
	now := time.Now()

	storeWithRecords := &mockConfigStoreForExchangeStale{
		mockConfigStoreForExchange: mockConfigStoreForExchange{},
		staleFunc: func(ctx context.Context, olderThan time.Duration) ([]config.RIExchangeRecord, error) {
			return []config.RIExchangeRecord{
				{ID: "stale-1", Status: "processing", CreatedAt: now.Add(-time.Hour)},
				{ID: "stale-2", Status: "processing", CreatedAt: now.Add(-2 * time.Hour)},
			}, nil
		},
	}

	adapter := newConfigExchangeStoreAdapter(storeWithStaleRecords(storeWithRecords))

	records, err := adapter.GetStaleProcessingExchanges(ctx, 30*time.Minute)
	testutil.AssertNoError(t, err)
	testutil.AssertEqual(t, 2, len(records))
	testutil.AssertEqual(t, "stale-1", records[0].ID)
	testutil.AssertEqual(t, exchange.ExchangeRecord{}.Status, "")
}

func TestConfigExchangeStoreAdapter_GetStaleProcessingExchanges_Error(t *testing.T) {
	ctx := testutil.TestContext(t)

	storeErr := &mockConfigStoreForExchangeStale{
		mockConfigStoreForExchange: mockConfigStoreForExchange{},
		staleFunc: func(ctx context.Context, olderThan time.Duration) ([]config.RIExchangeRecord, error) {
			return nil, errors.New("db query failed")
		},
	}

	adapter := newConfigExchangeStoreAdapter(storeWithStaleRecords(storeErr))

	_, err := adapter.GetStaleProcessingExchanges(ctx, 30*time.Minute)
	testutil.AssertError(t, err)
	testutil.AssertContains(t, err.Error(), "db query failed")
}

// Helper to satisfy config.StoreInterface with a custom GetStaleProcessingExchanges.
func storeWithStaleRecords(s *mockConfigStoreForExchangeStale) config.StoreInterface {
	return s
}

// ----- helper mock types -----

type mockConfigStoreForExchangeComplete struct {
	mockConfigStoreForExchange
	completeFunc func(ctx context.Context, id, exchangeID string) error
}

func (m *mockConfigStoreForExchangeComplete) CompleteRIExchange(ctx context.Context, id string, exchangeID string) error {
	if m.completeFunc != nil {
		return m.completeFunc(ctx, id, exchangeID)
	}
	return nil
}

type mockConfigStoreForExchangeFail struct {
	mockConfigStoreForExchange
	failFunc func(ctx context.Context, id, errorMsg string) error
}

func (m *mockConfigStoreForExchangeFail) FailRIExchange(ctx context.Context, id string, errorMsg string) error {
	if m.failFunc != nil {
		return m.failFunc(ctx, id, errorMsg)
	}
	return nil
}

type mockConfigStoreForExchangeStale struct {
	mockConfigStoreForExchange
	staleFunc func(ctx context.Context, olderThan time.Duration) ([]config.RIExchangeRecord, error)
}

func (m *mockConfigStoreForExchangeStale) GetStaleProcessingExchanges(ctx context.Context, olderThan time.Duration) ([]config.RIExchangeRecord, error) {
	if m.staleFunc != nil {
		return m.staleFunc(ctx, olderThan)
	}
	return nil, nil
}

// ── Purchase suppressions (Commit 2 of bulk-purchase-with-grace)
func (m *mockConfigStoreForExchangeComplete) CreateSuppression(_ context.Context, _ *config.PurchaseSuppression) error {
	return nil
}
func (m *mockConfigStoreForExchangeComplete) CreateSuppressionTx(_ context.Context, _ pgx.Tx, _ *config.PurchaseSuppression) error {
	return nil
}
func (m *mockConfigStoreForExchangeComplete) DeleteSuppressionsByExecution(_ context.Context, _ string) error {
	return nil
}
func (m *mockConfigStoreForExchangeComplete) DeleteSuppressionsByExecutionTx(_ context.Context, _ pgx.Tx, _ string) error {
	return nil
}
func (m *mockConfigStoreForExchangeComplete) ListActiveSuppressions(_ context.Context) ([]config.PurchaseSuppression, error) {
	return nil, nil
}
func (m *mockConfigStoreForExchangeComplete) SavePurchaseExecutionTx(ctx context.Context, _ pgx.Tx, e *config.PurchaseExecution) error {
	return m.SavePurchaseExecution(ctx, e)
}
func (m *mockConfigStoreForExchangeComplete) WithTx(_ context.Context, fn func(tx pgx.Tx) error) error {
	return fn(nil)
}

// ── Purchase suppressions (Commit 2 of bulk-purchase-with-grace)
func (m *mockConfigStoreForExchangeFail) CreateSuppression(_ context.Context, _ *config.PurchaseSuppression) error {
	return nil
}
func (m *mockConfigStoreForExchangeFail) CreateSuppressionTx(_ context.Context, _ pgx.Tx, _ *config.PurchaseSuppression) error {
	return nil
}
func (m *mockConfigStoreForExchangeFail) DeleteSuppressionsByExecution(_ context.Context, _ string) error {
	return nil
}
func (m *mockConfigStoreForExchangeFail) DeleteSuppressionsByExecutionTx(_ context.Context, _ pgx.Tx, _ string) error {
	return nil
}
func (m *mockConfigStoreForExchangeFail) ListActiveSuppressions(_ context.Context) ([]config.PurchaseSuppression, error) {
	return nil, nil
}
func (m *mockConfigStoreForExchangeFail) SavePurchaseExecutionTx(ctx context.Context, _ pgx.Tx, e *config.PurchaseExecution) error {
	return m.SavePurchaseExecution(ctx, e)
}
func (m *mockConfigStoreForExchangeFail) WithTx(_ context.Context, fn func(tx pgx.Tx) error) error {
	return fn(nil)
}

// ── Purchase suppressions (Commit 2 of bulk-purchase-with-grace)
func (m *mockConfigStoreForExchangeStale) CreateSuppression(_ context.Context, _ *config.PurchaseSuppression) error {
	return nil
}
func (m *mockConfigStoreForExchangeStale) CreateSuppressionTx(_ context.Context, _ pgx.Tx, _ *config.PurchaseSuppression) error {
	return nil
}
func (m *mockConfigStoreForExchangeStale) DeleteSuppressionsByExecution(_ context.Context, _ string) error {
	return nil
}
func (m *mockConfigStoreForExchangeStale) DeleteSuppressionsByExecutionTx(_ context.Context, _ pgx.Tx, _ string) error {
	return nil
}
func (m *mockConfigStoreForExchangeStale) ListActiveSuppressions(_ context.Context) ([]config.PurchaseSuppression, error) {
	return nil, nil
}
func (m *mockConfigStoreForExchangeStale) SavePurchaseExecutionTx(ctx context.Context, _ pgx.Tx, e *config.PurchaseExecution) error {
	return m.SavePurchaseExecution(ctx, e)
}
func (m *mockConfigStoreForExchangeStale) WithTx(_ context.Context, fn func(tx pgx.Tx) error) error {
	return fn(nil)
}
