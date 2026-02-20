package config

import (
	"context"
	"time"
)

// StoreInterface defines the methods required for configuration storage
type StoreInterface interface {
	// Global configuration
	GetGlobalConfig(ctx context.Context) (*GlobalConfig, error)
	SaveGlobalConfig(ctx context.Context, config *GlobalConfig) error

	// Service configuration
	GetServiceConfig(ctx context.Context, provider, service string) (*ServiceConfig, error)
	SaveServiceConfig(ctx context.Context, config *ServiceConfig) error
	ListServiceConfigs(ctx context.Context) ([]ServiceConfig, error)

	// Purchase plans
	CreatePurchasePlan(ctx context.Context, plan *PurchasePlan) error
	GetPurchasePlan(ctx context.Context, planID string) (*PurchasePlan, error)
	UpdatePurchasePlan(ctx context.Context, plan *PurchasePlan) error
	DeletePurchasePlan(ctx context.Context, planID string) error
	ListPurchasePlans(ctx context.Context) ([]PurchasePlan, error)

	// Purchase executions
	SavePurchaseExecution(ctx context.Context, execution *PurchaseExecution) error
	GetPendingExecutions(ctx context.Context) ([]PurchaseExecution, error)
	GetExecutionByID(ctx context.Context, executionID string) (*PurchaseExecution, error)
	GetExecutionByPlanAndDate(ctx context.Context, planID string, scheduledDate time.Time) (*PurchaseExecution, error)

	// Purchase history
	SavePurchaseHistory(ctx context.Context, record *PurchaseHistoryRecord) error
	GetPurchaseHistory(ctx context.Context, accountID string, limit int) ([]PurchaseHistoryRecord, error)
	GetAllPurchaseHistory(ctx context.Context, limit int) ([]PurchaseHistoryRecord, error)
}
