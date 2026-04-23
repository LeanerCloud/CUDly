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
	// DeleteCloudAccountFn overrides DeleteCloudAccount when non-nil (used to assert
	// delete was/was not invoked).
	DeleteCloudAccountFn func(ctx context.Context, id string) error
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

func (m *MockConfigStore) GetPurchasePlan(ctx context.Context, planID string) (*config.PurchasePlan, error) {
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

func (m *MockConfigStore) DeletePurchasePlan(ctx context.Context, planID string) error {
	args := m.Called(ctx, planID)
	return args.Error(0)
}

func (m *MockConfigStore) ListPurchasePlans(ctx context.Context) ([]config.PurchasePlan, error) {
	args := m.Called(ctx)
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

func (m *MockConfigStore) TransitionExecutionStatus(ctx context.Context, executionID string, fromStatuses []string, toStatus string) (*config.PurchaseExecution, error) {
	args := m.Called(ctx, executionID, fromStatuses, toStatus)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.PurchaseExecution), args.Error(1)
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
	return nil
}
func (m *MockConfigStore) GetCloudAccount(ctx context.Context, id string) (*config.CloudAccount, error) {
	if m.GetCloudAccountFn != nil {
		return m.GetCloudAccountFn(ctx, id)
	}
	return &config.CloudAccount{ID: id, Provider: "aws", AWSAuthMode: "access_keys"}, nil
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
	return nil
}
func (m *MockConfigStore) DeleteAccountServiceOverride(ctx context.Context, accountID, provider, service string) error {
	return nil
}
func (m *MockConfigStore) ListAccountServiceOverrides(ctx context.Context, accountID string) ([]config.AccountServiceOverride, error) {
	return nil, nil
}
func (m *MockConfigStore) SetPlanAccounts(ctx context.Context, planID string, accountIDs []string) error {
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

func (m *MockConfigStore) SavePurchaseExecutionTx(ctx context.Context, tx pgx.Tx, execution *config.PurchaseExecution) error {
	if !m.isExpected("SavePurchaseExecutionTx") {
		// Default to calling SavePurchaseExecution so tests that only
		// assert on the un-tx variant still see the write.
		return m.SavePurchaseExecution(ctx, execution)
	}
	args := m.Called(ctx, tx, execution)
	return args.Error(0)
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
