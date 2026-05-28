package api

import (
	"context"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/credentials"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/mock"
)

var _ credentials.CredentialStore = (*MockCredentialStore)(nil) // compile-time interface check

// MockConfigStore is a mock implementation of config.Store
type MockConfigStore struct {
	mock.Mock
	// GetCloudAccountFn overrides GetCloudAccount when non-nil (used in not-found tests).
	GetCloudAccountFn func(ctx context.Context, id string) (*config.CloudAccount, error)
	// GetCloudAccountByExternalIDFn overrides GetCloudAccountByExternalID when non-nil.
	GetCloudAccountByExternalIDFn func(ctx context.Context, provider, externalID string) (*config.CloudAccount, error)
	// DeleteCloudAccountFn overrides DeleteCloudAccount when non-nil (used to assert
	// delete was/was not invoked).
	DeleteCloudAccountFn func(ctx context.Context, id string) error
	// ListCloudAccountsFn overrides ListCloudAccounts when non-nil (used by
	// org-discovery dedupe tests to inject a known-roster fixture).
	ListCloudAccountsFn func(ctx context.Context, filter config.CloudAccountFilter) ([]config.CloudAccount, error)
	// CreateCloudAccountFn overrides CreateCloudAccount when non-nil (used by
	// org-discovery tests to capture the new rows the handler persists).
	CreateCloudAccountFn func(ctx context.Context, account *config.CloudAccount) error
	// GetPurchasePlanFn overrides GetPurchasePlan when non-nil. Used by the
	// setPlanAccounts provider-validation tests (issue #209) to seed the
	// plan without registering a testify expectation — see the fall-through
	// comment in GetPurchasePlan below for why the default no-expectation
	// path returns a minimal stub instead of panicking via m.Called.
	GetPurchasePlanFn func(ctx context.Context, planID string) (*config.PurchasePlan, error)
	// SetPlanAccountsFn overrides SetPlanAccounts when non-nil. The
	// provider-validation tests use it to assert whether the underlying
	// store write was invoked (mismatched assignments must NOT call it).
	SetPlanAccountsFn func(ctx context.Context, planID string, accountIDs []string) error
	// SaveAccountServiceOverrideFn overrides SaveAccountServiceOverride when
	// non-nil. Tests use it to assert whether the persist path was (or was
	// not) reached — e.g. confirming invalid-combo rejections short-circuit
	// before the store write.
	SaveAccountServiceOverrideFn func(ctx context.Context, override *config.AccountServiceOverride) error
	// CountPendingExecutionsForAccountFn overrides CountPendingExecutionsForAccount.
	// Used by the deleteAccount preflight tests (issue #606) to seed a
	// pending-execution count without standing up a real Postgres mock —
	// see TestDeleteAccount_PendingExecutions_Returns409.
	CountPendingExecutionsForAccountFn func(ctx context.Context, accountID string) (int, error)
	// ListPendingExecutionIDsForAccountFn overrides ListPendingExecutionIDsForAccount.
	// Currently unused at the api layer (the handler only needs the count),
	// but exported so future tests covering Cancel-All-Then-Delete server-side
	// helpers can wire it without re-extending this struct.
	ListPendingExecutionIDsForAccountFn func(ctx context.Context, accountID string) ([]string, error)
}

func (m *MockConfigStore) GetGlobalConfig(ctx context.Context) (*config.GlobalConfig, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.GlobalConfig), args.Error(1)
}

func (m *MockConfigStore) SaveGlobalConfig(ctx context.Context, cfg *config.GlobalConfig) error {
	args := m.Called(ctx, cfg)
	return args.Error(0)
}

func (m *MockConfigStore) GetServiceConfig(ctx context.Context, provider, service string) (*config.ServiceConfig, error) {
	args := m.Called(ctx, provider, service)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.ServiceConfig), args.Error(1)
}

func (m *MockConfigStore) SaveServiceConfig(ctx context.Context, cfg *config.ServiceConfig) error {
	args := m.Called(ctx, cfg)
	return args.Error(0)
}

func (m *MockConfigStore) ListServiceConfigs(ctx context.Context) ([]config.ServiceConfig, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.ServiceConfig), args.Error(1)
}

func (m *MockConfigStore) CreatePurchasePlan(ctx context.Context, plan *config.PurchasePlan) error {
	args := m.Called(ctx, plan)
	return args.Error(0)
}

// GetPurchasePlan resolves to (in order): an explicit GetPurchasePlanFn
// override, a registered testify expectation, or a default minimal plan
// (`{ID: planID}` with empty Services). The default-fallback path lets
// tests written before the issue-#209 provider-validation block (e.g.
// TestSetPlanAccounts_Success) keep working without setting up the
// new mock call — the empty Services map trips the defensive "no
// parseable services, skip provider validation" branch in
// setPlanAccounts so behaviour is unchanged for those legacy tests.
func (m *MockConfigStore) GetPurchasePlan(ctx context.Context, planID string) (*config.PurchasePlan, error) {
	if m.GetPurchasePlanFn != nil {
		return m.GetPurchasePlanFn(ctx, planID)
	}
	if !m.isExpected("GetPurchasePlan") {
		return &config.PurchasePlan{ID: planID}, nil
	}
	args := m.Called(ctx, planID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.PurchasePlan), args.Error(1)
}

func (m *MockConfigStore) UpdatePurchasePlan(ctx context.Context, plan *config.PurchasePlan) error {
	args := m.Called(ctx, plan)
	return args.Error(0)
}

// UpdatePurchasePlanTx falls back to UpdatePurchasePlan when no
// expectation is registered so tests that only assert on the un-tx
// variant stay green — same pattern as SavePurchaseExecutionTx.
func (m *MockConfigStore) UpdatePurchasePlanTx(ctx context.Context, tx pgx.Tx, plan *config.PurchasePlan) error {
	if !m.isExpected("UpdatePurchasePlanTx") {
		return m.UpdatePurchasePlan(ctx, plan)
	}
	args := m.Called(ctx, tx, plan)
	return args.Error(0)
}

func (m *MockConfigStore) DeletePurchasePlan(ctx context.Context, planID string) error {
	args := m.Called(ctx, planID)
	return args.Error(0)
}

func (m *MockConfigStore) ListPurchasePlans(ctx context.Context, filter config.PurchasePlanFilter) ([]config.PurchasePlan, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.PurchasePlan), args.Error(1)
}

func (m *MockConfigStore) SavePurchaseExecution(ctx context.Context, exec *config.PurchaseExecution) error {
	args := m.Called(ctx, exec)
	return args.Error(0)
}

func (m *MockConfigStore) GetPendingExecutions(ctx context.Context) ([]config.PurchaseExecution, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.PurchaseExecution), args.Error(1)
}

func (m *MockConfigStore) GetExecutionsByStatuses(ctx context.Context, statuses []string, limit int) ([]config.PurchaseExecution, error) {
	args := m.Called(ctx, statuses, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.PurchaseExecution), args.Error(1)
}

func (m *MockConfigStore) GetStaleApprovedExecutions(ctx context.Context, olderThan time.Duration) ([]config.PurchaseExecution, error) {
	args := m.Called(ctx, olderThan)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.PurchaseExecution), args.Error(1)
}

func (m *MockConfigStore) SavePurchaseHistory(ctx context.Context, record *config.PurchaseHistoryRecord) error {
	args := m.Called(ctx, record)
	return args.Error(0)
}

func (m *MockConfigStore) GetPurchaseHistory(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
	args := m.Called(ctx, accountID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.PurchaseHistoryRecord), args.Error(1)
}

func (m *MockConfigStore) GetAllPurchaseHistory(ctx context.Context, limit int) ([]config.PurchaseHistoryRecord, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.PurchaseHistoryRecord), args.Error(1)
}

func (m *MockConfigStore) GetPurchaseHistoryFiltered(ctx context.Context, providerFilter string, accountIDs []string, start, end *time.Time, limit int) ([]config.PurchaseHistoryRecord, error) {
	args := m.Called(ctx, providerFilter, accountIDs, start, end, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.PurchaseHistoryRecord), args.Error(1)
}

func (m *MockConfigStore) GetExecutionByID(ctx context.Context, executionID string) (*config.PurchaseExecution, error) {
	args := m.Called(ctx, executionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.PurchaseExecution), args.Error(1)
}

func (m *MockConfigStore) GetExecutionByPlanAndDate(ctx context.Context, planID string, scheduledDate time.Time) (*config.PurchaseExecution, error) {
	args := m.Called(ctx, planID, scheduledDate)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.PurchaseExecution), args.Error(1)
}

func (m *MockConfigStore) CleanupOldExecutions(ctx context.Context, retentionDays int) (int64, error) {
	args := m.Called(ctx, retentionDays)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockConfigStore) CountPendingExecutionsForAccount(ctx context.Context, accountID string) (int, error) {
	if m.CountPendingExecutionsForAccountFn != nil {
		return m.CountPendingExecutionsForAccountFn(ctx, accountID)
	}
	// Default: zero pending, no error. Lets every existing test that doesn't
	// care about the preflight continue compiling without explicit setup.
	return 0, nil
}

func (m *MockConfigStore) ListPendingExecutionIDsForAccount(ctx context.Context, accountID string) ([]string, error) {
	if m.ListPendingExecutionIDsForAccountFn != nil {
		return m.ListPendingExecutionIDsForAccountFn(ctx, accountID)
	}
	return nil, nil
}

func (m *MockConfigStore) TransitionExecutionStatus(ctx context.Context, executionID string, fromStatuses []string, toStatus string) (*config.PurchaseExecution, error) {
	args := m.Called(ctx, executionID, fromStatuses, toStatus)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.PurchaseExecution), args.Error(1)
}

func (m *MockConfigStore) ListStuckExecutions(ctx context.Context, statuses []string, olderThan time.Duration) ([]config.PurchaseExecution, error) {
	args := m.Called(ctx, statuses, olderThan)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.PurchaseExecution), args.Error(1)
}

func (m *MockConfigStore) SaveRIExchangeRecord(ctx context.Context, record *config.RIExchangeRecord) error {
	args := m.Called(ctx, record)
	return args.Error(0)
}

func (m *MockConfigStore) GetRIExchangeRecord(ctx context.Context, id string) (*config.RIExchangeRecord, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.RIExchangeRecord), args.Error(1)
}

func (m *MockConfigStore) GetRIExchangeRecordByToken(ctx context.Context, token string) (*config.RIExchangeRecord, error) {
	args := m.Called(ctx, token)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.RIExchangeRecord), args.Error(1)
}

func (m *MockConfigStore) GetRIExchangeHistory(ctx context.Context, since time.Time, limit int) ([]config.RIExchangeRecord, error) {
	args := m.Called(ctx, since, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.RIExchangeRecord), args.Error(1)
}

func (m *MockConfigStore) TransitionRIExchangeStatus(ctx context.Context, id string, fromStatus string, toStatus string) (*config.RIExchangeRecord, error) {
	args := m.Called(ctx, id, fromStatus, toStatus)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.RIExchangeRecord), args.Error(1)
}

func (m *MockConfigStore) CompleteRIExchange(ctx context.Context, id string, exchangeID string) error {
	args := m.Called(ctx, id, exchangeID)
	return args.Error(0)
}

func (m *MockConfigStore) StampRIExchangeApprovedBy(ctx context.Context, id string, approverEmail string) error {
	args := m.Called(ctx, id, approverEmail)
	return args.Error(0)
}

func (m *MockConfigStore) FailRIExchange(ctx context.Context, id string, errorMsg string) error {
	args := m.Called(ctx, id, errorMsg)
	return args.Error(0)
}

func (m *MockConfigStore) GetRIExchangeDailySpend(ctx context.Context, date time.Time) (string, error) {
	args := m.Called(ctx, date)
	return args.String(0), args.Error(1)
}

func (m *MockConfigStore) CancelAllPendingExchanges(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockConfigStore) GetStaleProcessingExchanges(ctx context.Context, olderThan time.Duration) ([]config.RIExchangeRecord, error) {
	args := m.Called(ctx, olderThan)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.RIExchangeRecord), args.Error(1)
}

func (m *MockConfigStore) CreateCloudAccount(ctx context.Context, account *config.CloudAccount) error {
	if m.CreateCloudAccountFn != nil {
		return m.CreateCloudAccountFn(ctx, account)
	}
	return nil
}
func (m *MockConfigStore) GetCloudAccount(ctx context.Context, id string) (*config.CloudAccount, error) {
	if m.GetCloudAccountFn != nil {
		return m.GetCloudAccountFn(ctx, id)
	}
	return &config.CloudAccount{ID: id, Provider: "aws", AWSAuthMode: "access_keys"}, nil
}
func (m *MockConfigStore) GetCloudAccountByExternalID(ctx context.Context, provider, externalID string) (*config.CloudAccount, error) {
	if m.GetCloudAccountByExternalIDFn != nil {
		return m.GetCloudAccountByExternalIDFn(ctx, provider, externalID)
	}
	return nil, nil
}
func (m *MockConfigStore) UpdateCloudAccount(ctx context.Context, account *config.CloudAccount) error {
	return nil
}
func (m *MockConfigStore) DeleteCloudAccount(ctx context.Context, id string) error {
	if m.DeleteCloudAccountFn != nil {
		return m.DeleteCloudAccountFn(ctx, id)
	}
	return nil
}
func (m *MockConfigStore) ListCloudAccounts(ctx context.Context, filter config.CloudAccountFilter) ([]config.CloudAccount, error) {
	if m.ListCloudAccountsFn != nil {
		return m.ListCloudAccountsFn(ctx, filter)
	}
	return nil, nil
}
func (m *MockConfigStore) SaveAccountCredential(ctx context.Context, accountID, credentialType, encryptedBlob string) error {
	return nil
}
func (m *MockConfigStore) GetAccountCredential(ctx context.Context, accountID, credentialType string) (string, error) {
	return "", nil
}
func (m *MockConfigStore) DeleteAccountCredentials(ctx context.Context, accountID string) error {
	return nil
}
func (m *MockConfigStore) HasAccountCredentials(ctx context.Context, accountID string) (bool, error) {
	return false, nil
}
func (m *MockConfigStore) GetAccountServiceOverride(ctx context.Context, accountID, provider, service string) (*config.AccountServiceOverride, error) {
	return nil, nil
}
func (m *MockConfigStore) SaveAccountServiceOverride(ctx context.Context, override *config.AccountServiceOverride) error {
	if m.SaveAccountServiceOverrideFn != nil {
		return m.SaveAccountServiceOverrideFn(ctx, override)
	}
	return nil
}
func (m *MockConfigStore) DeleteAccountServiceOverride(ctx context.Context, accountID, provider, service string) error {
	return nil
}
func (m *MockConfigStore) ListAccountServiceOverrides(ctx context.Context, accountID string) ([]config.AccountServiceOverride, error) {
	return nil, nil
}

// SetPlanAccounts uses SetPlanAccountsFn when non-nil so tests can
// capture and assert on the call (the issue-#209 mismatch tests verify
// the underlying store write is NOT invoked when validation fails).
// Falls back to m.Called when a testify expectation is registered so
// .On("SetPlanAccounts", ...) works correctly. The no-op is preserved
// only when neither path applies (tests that don't care).
func (m *MockConfigStore) SetPlanAccounts(ctx context.Context, planID string, accountIDs []string) error {
	if m.SetPlanAccountsFn != nil {
		return m.SetPlanAccountsFn(ctx, planID, accountIDs)
	}
	if m.isExpected("SetPlanAccounts") {
		return m.Called(ctx, planID, accountIDs).Error(0)
	}
	return nil
}
func (m *MockConfigStore) GetPlanAccounts(ctx context.Context, planID string) ([]config.CloudAccount, error) {
	return nil, nil
}
func (m *MockConfigStore) hasRecExpectation(method string) bool {
	for i := range m.ExpectedCalls {
		if m.ExpectedCalls[i].Method == method {
			return true
		}
	}
	return false
}
func (m *MockConfigStore) ReplaceRecommendations(ctx context.Context, collectedAt time.Time, recs []config.RecommendationRecord) error {
	if !m.hasRecExpectation("ReplaceRecommendations") {
		return nil
	}
	return m.Called(ctx, collectedAt, recs).Error(0)
}
func (m *MockConfigStore) UpsertRecommendations(ctx context.Context, collectedAt time.Time, recs []config.RecommendationRecord, successfulCollects []config.SuccessfulCollect) error {
	if !m.hasRecExpectation("UpsertRecommendations") {
		return nil
	}
	return m.Called(ctx, collectedAt, recs, successfulCollects).Error(0)
}
func (m *MockConfigStore) ListStoredRecommendations(ctx context.Context, filter config.RecommendationFilter) ([]config.RecommendationRecord, error) {
	if !m.hasRecExpectation("ListStoredRecommendations") {
		return nil, nil
	}
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.RecommendationRecord), args.Error(1)
}
func (m *MockConfigStore) GetRecommendationsFreshness(ctx context.Context) (*config.RecommendationsFreshness, error) {
	if !m.hasRecExpectation("GetRecommendationsFreshness") {
		return &config.RecommendationsFreshness{}, nil
	}
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.RecommendationsFreshness), args.Error(1)
}
func (m *MockConfigStore) SetRecommendationsCollectionError(ctx context.Context, errMsg string) error {
	if !m.hasRecExpectation("SetRecommendationsCollectionError") {
		return nil
	}
	return m.Called(ctx, errMsg).Error(0)
}
func (m *MockConfigStore) MarkCollectionStarted(ctx context.Context) (bool, error) {
	if !m.hasRecExpectation("MarkCollectionStarted") {
		return true, nil
	}
	args := m.Called(ctx)
	return args.Bool(0), args.Error(1)
}
func (m *MockConfigStore) ClearCollectionStarted(ctx context.Context) error {
	if !m.hasRecExpectation("ClearCollectionStarted") {
		return nil
	}
	return m.Called(ctx).Error(0)
}
func (m *MockConfigStore) GetRIUtilizationCache(ctx context.Context, region string, lookbackDays int) (*config.RIUtilizationCacheEntry, error) {
	if !m.hasRecExpectation("GetRIUtilizationCache") {
		return nil, nil
	}
	args := m.Called(ctx, region, lookbackDays)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.RIUtilizationCacheEntry), args.Error(1)
}
func (m *MockConfigStore) UpsertRIUtilizationCache(ctx context.Context, region string, lookbackDays int, payload []byte, fetchedAt time.Time) error {
	if !m.hasRecExpectation("UpsertRIUtilizationCache") {
		return nil
	}
	return m.Called(ctx, region, lookbackDays, payload, fetchedAt).Error(0)
}
func (m *MockConfigStore) CreateAccountRegistration(ctx context.Context, reg *config.AccountRegistration) error {
	args := m.Called(ctx, reg)
	return args.Error(0)
}
func (m *MockConfigStore) GetAccountRegistration(ctx context.Context, id string) (*config.AccountRegistration, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.AccountRegistration), args.Error(1)
}
func (m *MockConfigStore) GetAccountRegistrationByToken(ctx context.Context, token string) (*config.AccountRegistration, error) {
	args := m.Called(ctx, token)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.AccountRegistration), args.Error(1)
}
func (m *MockConfigStore) ListAccountRegistrations(ctx context.Context, filter config.AccountRegistrationFilter) ([]config.AccountRegistration, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.AccountRegistration), args.Error(1)
}
func (m *MockConfigStore) UpdateAccountRegistration(ctx context.Context, reg *config.AccountRegistration) error {
	args := m.Called(ctx, reg)
	return args.Error(0)
}
func (m *MockConfigStore) TransitionRegistrationStatus(ctx context.Context, reg *config.AccountRegistration, fromStatus string) error {
	args := m.Called(ctx, reg, fromStatus)
	return args.Error(0)
}
func (m *MockConfigStore) DeleteAccountRegistration(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

// ── Purchase suppressions (Commit 2 of bulk-purchase-with-grace)
// These mocks default to pass-through success so existing tests (which
// don't care about the suppression lifecycle) don't need to set up
// expectations for every call site. Tests that specifically exercise
// the suppression write/delete/list paths register .On(...) Return(...)
// expectations and those override the defaults via pgxmock ordering.

func (m *MockConfigStore) CreateSuppression(ctx context.Context, sup *config.PurchaseSuppression) error {
	if !m.isExpected("CreateSuppression") {
		return nil
	}
	args := m.Called(ctx, sup)
	return args.Error(0)
}

func (m *MockConfigStore) CreateSuppressionTx(ctx context.Context, tx pgx.Tx, sup *config.PurchaseSuppression) error {
	if !m.isExpected("CreateSuppressionTx") {
		return nil
	}
	args := m.Called(ctx, tx, sup)
	return args.Error(0)
}

func (m *MockConfigStore) DeleteSuppressionsByExecution(ctx context.Context, executionID string) error {
	if !m.isExpected("DeleteSuppressionsByExecution") {
		return nil
	}
	args := m.Called(ctx, executionID)
	return args.Error(0)
}

func (m *MockConfigStore) DeleteSuppressionsByExecutionTx(ctx context.Context, tx pgx.Tx, executionID string) error {
	if !m.isExpected("DeleteSuppressionsByExecutionTx") {
		return nil
	}
	args := m.Called(ctx, tx, executionID)
	return args.Error(0)
}

func (m *MockConfigStore) ListActiveSuppressions(ctx context.Context) ([]config.PurchaseSuppression, error) {
	if !m.isExpected("ListActiveSuppressions") {
		return nil, nil
	}
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.PurchaseSuppression), args.Error(1)
}

func (m *MockConfigStore) CancelExecutionAtomic(ctx context.Context, tx pgx.Tx, executionID string, cancelledBy *string) (bool, string, error) {
	if !m.isExpected("CancelExecutionAtomic") {
		// Default: succeed, returning "cancelled". Tests that exercise the
		// race (zero-rows) path register an explicit expectation that
		// returns (false, <racing status>, nil).
		return true, "cancelled", nil
	}
	args := m.Called(ctx, tx, executionID, cancelledBy)
	return args.Bool(0), args.String(1), args.Error(2)
}

func (m *MockConfigStore) SavePurchaseExecutionTx(ctx context.Context, tx pgx.Tx, execution *config.PurchaseExecution) error {
	if !m.isExpected("SavePurchaseExecutionTx") {
		// Default to calling SavePurchaseExecution so tests that only
		// assert on the un-tx variant still see the write.
		return m.SavePurchaseExecution(ctx, execution)
	}
	args := m.Called(ctx, tx, execution)
	return args.Error(0)
}

// GetPendingExecutionsTx falls back to GetPendingExecutions when no explicit
// expectation is registered so existing tests that run the WithTx path still
// see the same pending-execution list without needing to set up a new mock.
func (m *MockConfigStore) GetPendingExecutionsTx(ctx context.Context, tx pgx.Tx) ([]config.PurchaseExecution, error) {
	if !m.isExpected("GetPendingExecutionsTx") {
		return m.GetPendingExecutions(ctx)
	}
	args := m.Called(ctx, tx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.PurchaseExecution), args.Error(1)
}

// WithTx invokes fn with a sentinel nil tx so callers get to exercise
// their full tx callback (saving execution, creating suppressions, etc.)
// and tests assert on the individual *Tx mock methods rather than on
// WithTx itself. Real tests pass their own tx value via .On("WithTx") if
// they need to assert on it, but for the vast majority of consumers
// forwarding through is the cleanest default.
func (m *MockConfigStore) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if m.isExpected("WithTx") {
		args := m.Called(ctx, fn)
		return args.Error(0)
	}
	return fn(nil)
}

// isExpected returns true when at least one .On(method, ...) expectation
// has been registered on this mock. Lets us write "default no-op" stubs
// above that route through m.Called only when the test explicitly cares.
func (m *MockConfigStore) isExpected(method string) bool {
	for _, call := range m.ExpectedCalls {
		if call.Method == method {
			return true
		}
	}
	return false
}

// MockCredentialStore is a simple stub implementing credentials.CredentialStore.
// SaveCredential always returns nil; other methods are no-ops.
type MockCredentialStore struct{}

func (m *MockCredentialStore) SaveCredential(_ context.Context, _, _ string, _ []byte) error {
	return nil
}
func (m *MockCredentialStore) LoadRaw(_ context.Context, _, _ string) ([]byte, error) {
	return nil, nil
}
func (m *MockCredentialStore) DeleteCredential(_ context.Context, _, _ string) error {
	return nil
}
func (m *MockCredentialStore) HasCredential(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

func (m *MockCredentialStore) EncryptPayload(plaintext []byte) (string, error) {
	return string(plaintext), nil // no-op: return plaintext as "encrypted" for tests
}

func (m *MockCredentialStore) DecryptPayload(ciphertext string) ([]byte, error) {
	return []byte(ciphertext), nil // no-op: return ciphertext as "decrypted" for tests
}

// MockPurchaseManager is a mock implementation of purchase.Manager
type MockPurchaseManager struct {
	mock.Mock
}

func (m *MockPurchaseManager) ApproveExecution(ctx context.Context, execID, token, actor string) error {
	args := m.Called(ctx, execID, token, actor)
	return args.Error(0)
}

func (m *MockPurchaseManager) ApproveAndExecute(ctx context.Context, execID, actor string) error {
	args := m.Called(ctx, execID, actor)
	return args.Error(0)
}

func (m *MockPurchaseManager) CancelExecution(ctx context.Context, execID, token, actor string) error {
	args := m.Called(ctx, execID, token, actor)
	return args.Error(0)
}

// MockScheduler is a mock implementation of scheduler.Scheduler
type MockScheduler struct {
	mock.Mock
}

func (m *MockScheduler) CollectRecommendations(ctx context.Context) (*scheduler.CollectResult, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*scheduler.CollectResult), args.Error(1)
}

func (m *MockScheduler) ListRecommendations(ctx context.Context, filter config.RecommendationFilter) ([]config.RecommendationRecord, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.RecommendationRecord), args.Error(1)
}

func (m *MockScheduler) GetRecommendationByID(ctx context.Context, id string) (*config.RecommendationRecord, []string, error) {
	args := m.Called(ctx, id)
	var rec *config.RecommendationRecord
	if args.Get(0) != nil {
		rec = args.Get(0).(*config.RecommendationRecord)
	}
	var hiddenBy []string
	if args.Get(1) != nil {
		hiddenBy = args.Get(1).([]string)
	}
	return rec, hiddenBy, args.Error(2)
}

// MockAuthService is a mock implementation of the auth service
type MockAuthService struct {
	mock.Mock
}

func (m *MockAuthService) Login(ctx context.Context, req LoginRequest) (*LoginResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*LoginResponse), args.Error(1)
}

func (m *MockAuthService) Logout(ctx context.Context, token string) error {
	args := m.Called(ctx, token)
	return args.Error(0)
}

func (m *MockAuthService) ValidateSession(ctx context.Context, token string) (*Session, error) {
	args := m.Called(ctx, token)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Session), args.Error(1)
}

func (m *MockAuthService) ValidateCSRFToken(ctx context.Context, sessionToken, csrfToken string) error {
	args := m.Called(ctx, sessionToken, csrfToken)
	return args.Error(0)
}

func (m *MockAuthService) SetupAdmin(ctx context.Context, req SetupAdminRequest) (*LoginResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*LoginResponse), args.Error(1)
}

func (m *MockAuthService) CheckAdminExists(ctx context.Context) (bool, error) {
	args := m.Called(ctx)
	return args.Bool(0), args.Error(1)
}

func (m *MockAuthService) RequestPasswordReset(ctx context.Context, email string) error {
	args := m.Called(ctx, email)
	return args.Error(0)
}

func (m *MockAuthService) ConfirmPasswordReset(ctx context.Context, req PasswordResetConfirm) error {
	args := m.Called(ctx, req)
	return args.Error(0)
}

func (m *MockAuthService) ResetTokenStatus(ctx context.Context, token string) (string, string, error) {
	args := m.Called(ctx, token)
	return args.String(0), args.String(1), args.Error(2)
}

func (m *MockAuthService) GetUser(ctx context.Context, userID string) (*User, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*User), args.Error(1)
}

func (m *MockAuthService) UpdateUserProfile(ctx context.Context, userID string, email string, currentPassword string, newPassword string) error {
	args := m.Called(ctx, userID, email, currentPassword, newPassword)
	return args.Error(0)
}

// User management mock methods
func (m *MockAuthService) CreateUserAPI(ctx context.Context, req interface{}) (interface{}, error) {
	args := m.Called(ctx, req)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) UpdateUserAPI(ctx context.Context, userID string, req interface{}) (interface{}, error) {
	args := m.Called(ctx, userID, req)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) DeleteUser(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func (m *MockAuthService) ListUsersAPI(ctx context.Context) (interface{}, error) {
	args := m.Called(ctx)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) ChangePasswordAPI(ctx context.Context, userID, currentPassword, newPassword string) error {
	args := m.Called(ctx, userID, currentPassword, newPassword)
	return args.Error(0)
}

// MFA lifecycle mock methods (issue #497).
func (m *MockAuthService) MFASetupAPI(ctx context.Context, userID, password string) (string, string, error) {
	args := m.Called(ctx, userID, password)
	return args.String(0), args.String(1), args.Error(2)
}

func (m *MockAuthService) MFAEnableAPI(ctx context.Context, userID, code string) ([]string, error) {
	args := m.Called(ctx, userID, code)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockAuthService) MFADisableAPI(ctx context.Context, userID, password, codeOrRecovery string) error {
	args := m.Called(ctx, userID, password, codeOrRecovery)
	return args.Error(0)
}

func (m *MockAuthService) MFARegenerateRecoveryCodesAPI(ctx context.Context, userID, code string) ([]string, error) {
	args := m.Called(ctx, userID, code)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

// Group management mock methods
func (m *MockAuthService) CreateGroupAPI(ctx context.Context, req interface{}) (interface{}, error) {
	args := m.Called(ctx, req)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) UpdateGroupAPI(ctx context.Context, groupID string, req interface{}) (interface{}, error) {
	args := m.Called(ctx, groupID, req)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) DeleteGroup(ctx context.Context, groupID string) error {
	args := m.Called(ctx, groupID)
	return args.Error(0)
}

func (m *MockAuthService) GetGroupAPI(ctx context.Context, groupID string) (interface{}, error) {
	args := m.Called(ctx, groupID)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) ListGroupsAPI(ctx context.Context) (interface{}, error) {
	args := m.Called(ctx)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) HasPermissionAPI(ctx context.Context, userID, action, resource string) (bool, error) {
	args := m.Called(ctx, userID, action, resource)
	return args.Bool(0), args.Error(1)
}

func (m *MockAuthService) GetAllowedAccountsAPI(ctx context.Context, userID string) ([]string, error) {
	args := m.Called(ctx, userID)
	if v := args.Get(0); v != nil {
		return v.([]string), args.Error(1)
	}
	return nil, args.Error(1)
}

// API Key management mock methods
func (m *MockAuthService) CreateAPIKeyAPI(ctx context.Context, userID string, req interface{}) (interface{}, error) {
	args := m.Called(ctx, userID, req)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) ListUserAPIKeysAPI(ctx context.Context, userID string) (interface{}, error) {
	args := m.Called(ctx, userID)
	return args.Get(0), args.Error(1)
}

func (m *MockAuthService) DeleteAPIKeyAPI(ctx context.Context, userID, keyID string) error {
	args := m.Called(ctx, userID, keyID)
	return args.Error(0)
}

func (m *MockAuthService) RevokeAPIKeyAPI(ctx context.Context, userID, keyID string) error {
	args := m.Called(ctx, userID, keyID)
	return args.Error(0)
}

func (m *MockAuthService) ValidateUserAPIKeyAPI(ctx context.Context, apiKey string) (interface{}, interface{}, error) {
	args := m.Called(ctx, apiKey)
	return args.Get(0), args.Get(1), args.Error(2)
}
