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
	CleanupOldExecutions(ctx context.Context, retentionDays int) (int64, error)
	TransitionExecutionStatus(ctx context.Context, executionID string, fromStatuses []string, toStatus string) (*PurchaseExecution, error)

	// Purchase history
	SavePurchaseHistory(ctx context.Context, record *PurchaseHistoryRecord) error
	GetPurchaseHistory(ctx context.Context, accountID string, limit int) ([]PurchaseHistoryRecord, error)
	GetAllPurchaseHistory(ctx context.Context, limit int) ([]PurchaseHistoryRecord, error)

	// RI Exchange history
	SaveRIExchangeRecord(ctx context.Context, record *RIExchangeRecord) error
	GetRIExchangeRecord(ctx context.Context, id string) (*RIExchangeRecord, error)
	GetRIExchangeRecordByToken(ctx context.Context, token string) (*RIExchangeRecord, error)
	GetRIExchangeHistory(ctx context.Context, since time.Time, limit int) ([]RIExchangeRecord, error)
	TransitionRIExchangeStatus(ctx context.Context, id string, fromStatus string, toStatus string) (*RIExchangeRecord, error)
	CompleteRIExchange(ctx context.Context, id string, exchangeID string) error
	FailRIExchange(ctx context.Context, id string, errorMsg string) error
	GetRIExchangeDailySpend(ctx context.Context, date time.Time) (string, error)
	CancelAllPendingExchanges(ctx context.Context) (int64, error)
	GetStaleProcessingExchanges(ctx context.Context, olderThan time.Duration) ([]RIExchangeRecord, error)

	// Cloud accounts
	CreateCloudAccount(ctx context.Context, account *CloudAccount) error
	GetCloudAccount(ctx context.Context, id string) (*CloudAccount, error)
	UpdateCloudAccount(ctx context.Context, account *CloudAccount) error
	DeleteCloudAccount(ctx context.Context, id string) error
	ListCloudAccounts(ctx context.Context, filter CloudAccountFilter) ([]CloudAccount, error)

	// Account credentials (encrypted blobs; never returned via API)
	SaveAccountCredential(ctx context.Context, accountID, credentialType, encryptedBlob string) error
	GetAccountCredential(ctx context.Context, accountID, credentialType string) (string, error)
	DeleteAccountCredentials(ctx context.Context, accountID string) error
	HasAccountCredentials(ctx context.Context, accountID string) (bool, error)

	// Account service overrides
	GetAccountServiceOverride(ctx context.Context, accountID, provider, service string) (*AccountServiceOverride, error)
	SaveAccountServiceOverride(ctx context.Context, override *AccountServiceOverride) error
	DeleteAccountServiceOverride(ctx context.Context, accountID, provider, service string) error
	ListAccountServiceOverrides(ctx context.Context, accountID string) ([]AccountServiceOverride, error)

	// Plan ↔ account association
	SetPlanAccounts(ctx context.Context, planID string, accountIDs []string) error
	GetPlanAccounts(ctx context.Context, planID string) ([]CloudAccount, error)

	// Recommendations cache (ADR: store recommendations in Postgres so the
	// dashboard serves provider-switch clicks from SQL instead of live cloud
	// API calls). ReplaceRecommendations is the "force full resync" path;
	// UpsertRecommendations is the steady-state write path and takes a list
	// of (provider, account) pairs that successfully collected this cycle.
	// Stale-row eviction is scoped to that union so a partially-failed
	// provider preserves the failed accounts' previous-cycle rows. See
	// SuccessfulCollect for the per-row semantics.
	ReplaceRecommendations(ctx context.Context, collectedAt time.Time, recs []RecommendationRecord) error
	UpsertRecommendations(ctx context.Context, collectedAt time.Time, recs []RecommendationRecord, successfulCollects []SuccessfulCollect) error
	ListStoredRecommendations(ctx context.Context, filter RecommendationFilter) ([]RecommendationRecord, error)
	GetRecommendationsFreshness(ctx context.Context) (*RecommendationsFreshness, error)
	SetRecommendationsCollectionError(ctx context.Context, errMsg string) error

	// RI utilization cache. Postgres-backed TTL cache for Cost Explorer
	// GetReservationUtilization; shared across Lambda containers so
	// dashboard loads don't each fan out to a paid CE API call. A
	// per-process in-memory cache effectively never hits because each
	// cold container starts empty. GetRIUtilizationCache returns nil
	// when the (region, lookback_days) key is absent — callers treat
	// that as a miss and re-fetch.
	GetRIUtilizationCache(ctx context.Context, region string, lookbackDays int) (*RIUtilizationCacheEntry, error)
	UpsertRIUtilizationCache(ctx context.Context, region string, lookbackDays int, payload []byte, fetchedAt time.Time) error

	// Account registrations (self-service enrollment via federation IaC)
	CreateAccountRegistration(ctx context.Context, reg *AccountRegistration) error
	GetAccountRegistration(ctx context.Context, id string) (*AccountRegistration, error)
	GetAccountRegistrationByToken(ctx context.Context, token string) (*AccountRegistration, error)
	ListAccountRegistrations(ctx context.Context, filter AccountRegistrationFilter) ([]AccountRegistration, error)
	UpdateAccountRegistration(ctx context.Context, reg *AccountRegistration) error
	TransitionRegistrationStatus(ctx context.Context, reg *AccountRegistration, fromStatus string) error
	DeleteAccountRegistration(ctx context.Context, id string) error
}
