package server

import (
	"context"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/database"
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
