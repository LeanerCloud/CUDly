package config

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
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
	// UpdatePurchasePlanTx is the tx-accepting variant of UpdatePurchasePlan.
	// Used from createPlannedPurchases' WithTx block so the per-row
	// SavePurchaseExecutionTx writes and the plan's next_execution_date
	// bump commit atomically — a partial failure leaves no orphaned
	// rows and no stale plan pointer.
	UpdatePurchasePlanTx(ctx context.Context, tx pgx.Tx, plan *PurchasePlan) error
	DeletePurchasePlan(ctx context.Context, planID string) error
	ListPurchasePlans(ctx context.Context, filter PurchasePlanFilter) ([]PurchasePlan, error)

	// Purchase executions
	SavePurchaseExecution(ctx context.Context, execution *PurchaseExecution) error
	GetPendingExecutions(ctx context.Context) ([]PurchaseExecution, error)
	// GetExecutionsByStatuses returns executions in any of the given states,
	// newest first, capped at `limit`. Used by the History handler to render
	// pending + failed + expired alongside completed purchases; the scheduler
	// keeps using GetPendingExecutions (which is narrower and doesn't share
	// this method's status filter) to avoid accidental double-processing of
	// failed / expired rows.
	GetExecutionsByStatuses(ctx context.Context, statuses []string, limit int) ([]PurchaseExecution, error)
	// GetPlannedExecutions returns executions in any of the given states
	// ordered by scheduled_date ASC (soonest first), the order the Planned
	// Purchases UI lists rows so the user acts on imminent purchases first.
	// Distinct from GetExecutionsByStatuses (which is DESC for History's
	// "newest first" semantics): when the result set exceeds `limit`, an
	// ORDER-BY-DESC + LIMIT in SQL truncates away the soonest rows, exactly
	// the rows this list must surface. Secondary sort by id ASC stabilises
	// ordering when multiple rows share a scheduled_date.
	GetPlannedExecutions(ctx context.Context, statuses []string, limit int) ([]PurchaseExecution, error)
	// GetStaleApprovedExecutions returns executions stuck in the "approved"
	// status with updated_at older than olderThan — strands left behind when a
	// synchronous purchase run was interrupted before finalizing (issue #632).
	// The recovery sweep in the purchase manager re-drives these into a
	// terminal "failed" state so they can never sit permanently approved.
	GetStaleApprovedExecutions(ctx context.Context, olderThan time.Duration) ([]PurchaseExecution, error)
	GetExecutionByID(ctx context.Context, executionID string) (*PurchaseExecution, error)
	GetExecutionByPlanAndDate(ctx context.Context, planID string, scheduledDate time.Time) (*PurchaseExecution, error)
	// CountPendingExecutionsForAccount returns the number of purchase_executions
	// in status 'pending' or 'notified' that reference the given cloud account.
	// Used by the deleteAccount handler to preflight DB-level FK violations
	// (migration 000053 tightened the FK to ON DELETE RESTRICT) and emit a
	// 409 with a count so the frontend can offer Cancel-All-Then-Delete UX
	// instead of surfacing a raw constraint error. See issue #606.
	CountPendingExecutionsForAccount(ctx context.Context, accountID string) (int, error)
	// ListPendingExecutionIDsForAccount returns the execution IDs of all
	// pending / notified executions referencing this account, used by the
	// frontend's Cancel-All-Then-Delete flow when the operator opts to
	// cancel everything in one go after a 409. Capped at 1000 rows; if a
	// single account has more pending executions than that, the cleanup
	// is a one-off operator task rather than a button click anyway.
	ListPendingExecutionIDsForAccount(ctx context.Context, accountID string) ([]string, error)
	CleanupOldExecutions(ctx context.Context, retentionDays int) (int64, error)
	TransitionExecutionStatus(ctx context.Context, executionID string, fromStatuses []string, toStatus string) (*PurchaseExecution, error)
	// CancelExecutionAtomic atomically flips status from pending/notified to
	// cancelled, setting cancelled_by. Returns (true, "cancelled", nil) on
	// success and (false, currentStatus, nil) when zero rows were affected
	// (the execution had already been approved or otherwise transitioned).
	// Must be called inside a WithTx block so the suppression cleanup and
	// the status flip commit atomically.
	CancelExecutionAtomic(ctx context.Context, tx pgx.Tx, executionID string, cancelledBy *string) (cancelled bool, currentStatus string, err error)
	// ListStuckExecutions returns executions in any of the given statuses
	// whose updated_at is older than the given duration. Used by the
	// reaper sweep (issue #678) to find rows stuck in approved/running
	// after the synchronous executor failed mid-flight without flipping
	// them to a terminal state. Oldest-stuck-first (ORDER BY updated_at
	// ASC), capped at MaxListLimit per sweep.
	ListStuckExecutions(ctx context.Context, statuses []string, olderThan time.Duration) ([]PurchaseExecution, error)

	// Purchase history
	SavePurchaseHistory(ctx context.Context, record *PurchaseHistoryRecord) error
	GetPurchaseHistory(ctx context.Context, accountID string, limit int) ([]PurchaseHistoryRecord, error)
	GetAllPurchaseHistory(ctx context.Context, limit int) ([]PurchaseHistoryRecord, error)
	// GetPurchaseHistoryFiltered reads purchase_history rows matching the
	// supplied filter set, newest-first, capped at limit. Each filter is
	// applied independently and only when non-empty:
	//   - providerFilter: matches purchase_history.provider exactly. Empty
	//     skips the clause.
	//   - accountIDs: matches purchase_history.cloud_account_id (UUID) with
	//     ANY($). Empty/nil skips the clause; non-empty excludes legacy
	//     ambient rows whose cloud_account_id IS NULL (mirrors the
	//     recommendations filter semantics on issue #211).
	//   - start/end: bounds purchase_history.timestamp with a BETWEEN. nil
	//     for both skips the clause; nil for either sets that side open
	//     (caller is responsible for any range cap, see
	//     api.MaxHistoryDateRangeDays).
	// Added for issue #701: the legacy GetPurchaseHistory /
	// GetAllPurchaseHistory pair only accepted a single account_id and the
	// limit, so the /api/history handler silently dropped the
	// provider/account_ids/start/end query params the frontend was sending.
	GetPurchaseHistoryFiltered(ctx context.Context, providerFilter string, accountIDs []string, start, end *time.Time, limit int) ([]PurchaseHistoryRecord, error)

	// RI Exchange history
	SaveRIExchangeRecord(ctx context.Context, record *RIExchangeRecord) error
	GetRIExchangeRecord(ctx context.Context, id string) (*RIExchangeRecord, error)
	GetRIExchangeRecordByToken(ctx context.Context, token string) (*RIExchangeRecord, error)
	GetRIExchangeHistory(ctx context.Context, since time.Time, limit int) ([]RIExchangeRecord, error)
	TransitionRIExchangeStatus(ctx context.Context, id string, fromStatus string, toStatus string) (*RIExchangeRecord, error)
	CompleteRIExchange(ctx context.Context, id string, exchangeID string) error
	// StampRIExchangeApprovedBy sets the approved_by column on a completed
	// exchange row (issue #300). Called after CompleteRIExchange when the
	// approval came from a session-authed user rather than an email token.
	StampRIExchangeApprovedBy(ctx context.Context, id string, approverEmail string) error
	FailRIExchange(ctx context.Context, id string, errorMsg string) error
	GetRIExchangeDailySpend(ctx context.Context, date time.Time) (string, error)
	CancelAllPendingExchanges(ctx context.Context) (int64, error)
	GetStaleProcessingExchanges(ctx context.Context, olderThan time.Duration) ([]RIExchangeRecord, error)

	// Cloud accounts
	CreateCloudAccount(ctx context.Context, account *CloudAccount) error
	GetCloudAccount(ctx context.Context, id string) (*CloudAccount, error)
	// GetCloudAccountByExternalID looks up a cloud account by its
	// (provider, external_id) pair. Used by the scheduler's ambient
	// collector path to tag rec rows with the registered host-account's
	// UUID when the Lambda's STS identity matches a registered (but
	// possibly disabled) account — so the approve-modal Account column
	// shows the account name instead of `(ambient)`. Returns
	// (nil, nil) when no row matches (the caller treats this as the
	// genuine orphan case). The underlying
	// `UNIQUE(provider, external_id)` constraint on cloud_accounts
	// guarantees the lookup hits an index.
	GetCloudAccountByExternalID(ctx context.Context, provider, externalID string) (*CloudAccount, error)
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
	// MarkCollectionStarted atomically sets last_collection_started_at = now
	// only when no in-flight collection is running (last_collection_started_at IS NULL
	// OR older than 5 minutes). Returns true when this caller won the race and
	// should proceed with the async invoke; false when another collection is
	// already in flight and the caller should return 409.
	MarkCollectionStarted(ctx context.Context) (bool, error)
	// ClearCollectionStarted clears last_collection_started_at. Called by the
	// scheduler at the end of every CollectRecommendations run, whether it
	// succeeded or failed, so the UI knows the collection has finished.
	ClearCollectionStarted(ctx context.Context) error

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

	// Purchase suppressions. Written inside a WithTx block during bulk
	// purchase submit so the execution insert + the suppression rows
	// commit atomically. Deleted on cancel/expire of the execution,
	// also inside a WithTx block paired with the status update.
	//
	// The plain variants (no Tx) open their own single-call transaction
	// — useful for tests and one-off admin operations. The Tx variants
	// reuse a caller-provided transaction so multi-write operations
	// can roll back atomically.
	CreateSuppression(ctx context.Context, sup *PurchaseSuppression) error
	CreateSuppressionTx(ctx context.Context, tx pgx.Tx, sup *PurchaseSuppression) error
	DeleteSuppressionsByExecution(ctx context.Context, executionID string) error
	DeleteSuppressionsByExecutionTx(ctx context.Context, tx pgx.Tx, executionID string) error
	ListActiveSuppressions(ctx context.Context) ([]PurchaseSuppression, error)

	// SavePurchaseExecutionTx is the tx-accepting variant of
	// SavePurchaseExecution. Used from executePurchase's WithTx block
	// so the execution insert + suppression writes commit atomically.
	SavePurchaseExecutionTx(ctx context.Context, tx pgx.Tx, execution *PurchaseExecution) error

	// GetPendingExecutionsTx is the tx-accepting variant of
	// GetPendingExecutions. Used inside the executePurchase WithTx block
	// so the duplicate-detection read and the new-execution insert are
	// atomic under the same transaction, eliminating the TOCTOU race (#643).
	GetPendingExecutionsTx(ctx context.Context, tx pgx.Tx) ([]PurchaseExecution, error)

	// WithTx opens a pgx transaction, runs fn, and commits on success or
	// rolls back on error. fn can call any *Tx method on the store to
	// participate in the transaction. Nested transactions are not
	// supported — fn must not call WithTx recursively.
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}
