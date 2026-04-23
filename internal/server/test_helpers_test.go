package server

import (
	"context"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/database"
	"github.com/jackc/pgx/v5"
)

// databaseConfigStub is a type alias used in health tests to simulate pending DB config
type databaseConfigStub = database.Config

// mockConfigStoreForHealth implements config.StoreInterface for health check tests
type mockConfigStoreForHealth struct{}

func (m *mockConfigStoreForHealth) GetGlobalConfig(ctx context.Context) (*config.GlobalConfig, error) {
	return &config.GlobalConfig{}, nil
}

func (m *mockConfigStoreForHealth) SaveGlobalConfig(ctx context.Context, cfg *config.GlobalConfig) error {
	return nil
}

func (m *mockConfigStoreForHealth) GetServiceConfig(ctx context.Context, provider, service string) (*config.ServiceConfig, error) {
	return &config.ServiceConfig{}, nil
}

func (m *mockConfigStoreForHealth) SaveServiceConfig(ctx context.Context, cfg *config.ServiceConfig) error {
	return nil
}

func (m *mockConfigStoreForHealth) ListServiceConfigs(ctx context.Context) ([]config.ServiceConfig, error) {
	return nil, nil
}

func (m *mockConfigStoreForHealth) CreatePurchasePlan(ctx context.Context, plan *config.PurchasePlan) error {
	return nil
}

func (m *mockConfigStoreForHealth) GetPurchasePlan(ctx context.Context, planID string) (*config.PurchasePlan, error) {
	return nil, nil
}

func (m *mockConfigStoreForHealth) UpdatePurchasePlan(ctx context.Context, plan *config.PurchasePlan) error {
	return nil
}

func (m *mockConfigStoreForHealth) DeletePurchasePlan(ctx context.Context, planID string) error {
	return nil
}

func (m *mockConfigStoreForHealth) ListPurchasePlans(ctx context.Context) ([]config.PurchasePlan, error) {
	return nil, nil
}

func (m *mockConfigStoreForHealth) SavePurchaseExecution(ctx context.Context, execution *config.PurchaseExecution) error {
	return nil
}

func (m *mockConfigStoreForHealth) GetPendingExecutions(ctx context.Context) ([]config.PurchaseExecution, error) {
	return nil, nil
}

func (m *mockConfigStoreForHealth) GetExecutionsByStatuses(ctx context.Context, statuses []string, limit int) ([]config.PurchaseExecution, error) {
	return nil, nil
}

func (m *mockConfigStoreForHealth) GetExecutionByID(ctx context.Context, executionID string) (*config.PurchaseExecution, error) {
	return nil, nil
}

func (m *mockConfigStoreForHealth) GetExecutionByPlanAndDate(ctx context.Context, planID string, scheduledDate time.Time) (*config.PurchaseExecution, error) {
	return nil, nil
}

func (m *mockConfigStoreForHealth) SavePurchaseHistory(ctx context.Context, record *config.PurchaseHistoryRecord) error {
	return nil
}

func (m *mockConfigStoreForHealth) GetPurchaseHistory(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
	return nil, nil
}

func (m *mockConfigStoreForHealth) GetAllPurchaseHistory(ctx context.Context, limit int) ([]config.PurchaseHistoryRecord, error) {
	return nil, nil
}

func (m *mockConfigStoreForHealth) CleanupOldExecutions(ctx context.Context, retentionDays int) (int64, error) {
	return 0, nil
}

func (m *mockConfigStoreForHealth) TransitionExecutionStatus(ctx context.Context, executionID string, fromStatuses []string, toStatus string) (*config.PurchaseExecution, error) {
	return nil, nil
}

func (m *mockConfigStoreForHealth) SaveRIExchangeRecord(ctx context.Context, record *config.RIExchangeRecord) error {
	return nil
}
func (m *mockConfigStoreForHealth) GetRIExchangeRecord(ctx context.Context, id string) (*config.RIExchangeRecord, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) GetRIExchangeRecordByToken(ctx context.Context, token string) (*config.RIExchangeRecord, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) GetRIExchangeHistory(ctx context.Context, since time.Time, limit int) ([]config.RIExchangeRecord, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) TransitionRIExchangeStatus(ctx context.Context, id string, fromStatus string, toStatus string) (*config.RIExchangeRecord, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) CompleteRIExchange(ctx context.Context, id string, exchangeID string) error {
	return nil
}
func (m *mockConfigStoreForHealth) FailRIExchange(ctx context.Context, id string, errorMsg string) error {
	return nil
}
func (m *mockConfigStoreForHealth) GetRIExchangeDailySpend(ctx context.Context, date time.Time) (string, error) {
	return "0", nil
}
func (m *mockConfigStoreForHealth) CancelAllPendingExchanges(ctx context.Context) (int64, error) {
	return 0, nil
}
func (m *mockConfigStoreForHealth) GetStaleProcessingExchanges(ctx context.Context, olderThan time.Duration) ([]config.RIExchangeRecord, error) {
	return nil, nil
}

func (m *mockConfigStoreForHealth) CreateCloudAccount(ctx context.Context, account *config.CloudAccount) error {
	return nil
}
func (m *mockConfigStoreForHealth) GetCloudAccount(ctx context.Context, id string) (*config.CloudAccount, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) UpdateCloudAccount(ctx context.Context, account *config.CloudAccount) error {
	return nil
}
func (m *mockConfigStoreForHealth) DeleteCloudAccount(ctx context.Context, id string) error {
	return nil
}
func (m *mockConfigStoreForHealth) ListCloudAccounts(ctx context.Context, filter config.CloudAccountFilter) ([]config.CloudAccount, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) SaveAccountCredential(ctx context.Context, accountID, credentialType, encryptedBlob string) error {
	return nil
}
func (m *mockConfigStoreForHealth) GetAccountCredential(ctx context.Context, accountID, credentialType string) (string, error) {
	return "", nil
}
func (m *mockConfigStoreForHealth) DeleteAccountCredentials(ctx context.Context, accountID string) error {
	return nil
}
func (m *mockConfigStoreForHealth) HasAccountCredentials(ctx context.Context, accountID string) (bool, error) {
	return false, nil
}
func (m *mockConfigStoreForHealth) GetAccountServiceOverride(ctx context.Context, accountID, provider, service string) (*config.AccountServiceOverride, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) SaveAccountServiceOverride(ctx context.Context, override *config.AccountServiceOverride) error {
	return nil
}
func (m *mockConfigStoreForHealth) DeleteAccountServiceOverride(ctx context.Context, accountID, provider, service string) error {
	return nil
}
func (m *mockConfigStoreForHealth) ListAccountServiceOverrides(ctx context.Context, accountID string) ([]config.AccountServiceOverride, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) SetPlanAccounts(ctx context.Context, planID string, accountIDs []string) error {
	return nil
}
func (m *mockConfigStoreForHealth) GetPlanAccounts(ctx context.Context, planID string) ([]config.CloudAccount, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) CreateAccountRegistration(_ context.Context, _ *config.AccountRegistration) error {
	return nil
}
func (m *mockConfigStoreForHealth) GetAccountRegistration(_ context.Context, _ string) (*config.AccountRegistration, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) GetAccountRegistrationByToken(_ context.Context, _ string) (*config.AccountRegistration, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) ListAccountRegistrations(_ context.Context, _ config.AccountRegistrationFilter) ([]config.AccountRegistration, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) UpdateAccountRegistration(_ context.Context, _ *config.AccountRegistration) error {
	return nil
}
func (m *mockConfigStoreForHealth) TransitionRegistrationStatus(_ context.Context, _ *config.AccountRegistration, _ string) error {
	return nil
}
func (m *mockConfigStoreForHealth) DeleteAccountRegistration(_ context.Context, _ string) error {
	return nil
}
func (m *mockConfigStoreForHealth) ReplaceRecommendations(_ context.Context, _ time.Time, _ []config.RecommendationRecord) error {
	return nil
}
func (m *mockConfigStoreForHealth) UpsertRecommendations(_ context.Context, _ time.Time, _ []config.RecommendationRecord, _ []config.SuccessfulCollect) error {
	return nil
}
func (m *mockConfigStoreForHealth) ListStoredRecommendations(_ context.Context, _ config.RecommendationFilter) ([]config.RecommendationRecord, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) GetRecommendationsFreshness(_ context.Context) (*config.RecommendationsFreshness, error) {
	return &config.RecommendationsFreshness{}, nil
}
func (m *mockConfigStoreForHealth) SetRecommendationsCollectionError(_ context.Context, _ string) error {
	return nil
}
func (m *mockConfigStoreForHealth) GetRIUtilizationCache(_ context.Context, _ string, _ int) (*config.RIUtilizationCacheEntry, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) UpsertRIUtilizationCache(_ context.Context, _ string, _ int, _ []byte, _ time.Time) error {
	return nil
}

// ── Purchase suppressions (Commit 2 of bulk-purchase-with-grace)
func (m *mockConfigStoreForHealth) CreateSuppression(_ context.Context, _ *config.PurchaseSuppression) error {
	return nil
}
func (m *mockConfigStoreForHealth) CreateSuppressionTx(_ context.Context, _ pgx.Tx, _ *config.PurchaseSuppression) error {
	return nil
}
func (m *mockConfigStoreForHealth) DeleteSuppressionsByExecution(_ context.Context, _ string) error {
	return nil
}
func (m *mockConfigStoreForHealth) DeleteSuppressionsByExecutionTx(_ context.Context, _ pgx.Tx, _ string) error {
	return nil
}
func (m *mockConfigStoreForHealth) ListActiveSuppressions(_ context.Context) ([]config.PurchaseSuppression, error) {
	return nil, nil
}
func (m *mockConfigStoreForHealth) SavePurchaseExecutionTx(ctx context.Context, _ pgx.Tx, e *config.PurchaseExecution) error {
	return m.SavePurchaseExecution(ctx, e)
}
func (m *mockConfigStoreForHealth) WithTx(_ context.Context, fn func(tx pgx.Tx) error) error {
	return fn(nil)
}
