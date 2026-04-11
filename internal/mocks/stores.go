package mocks

import (
	"context"
	"time"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/mock"
)

// MockConfigStore is a mock implementation of config.Store
type MockConfigStore struct {
	mock.Mock
}

// GetGlobalConfig mocks the GetGlobalConfig operation
func (m *MockConfigStore) GetGlobalConfig(ctx context.Context) (*config.GlobalConfig, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.GlobalConfig), args.Error(1)
}

// SaveGlobalConfig mocks the SaveGlobalConfig operation
func (m *MockConfigStore) SaveGlobalConfig(ctx context.Context, cfg *config.GlobalConfig) error {
	args := m.Called(ctx, cfg)
	return args.Error(0)
}

// GetServiceConfig mocks the GetServiceConfig operation
func (m *MockConfigStore) GetServiceConfig(ctx context.Context, provider, service string) (*config.ServiceConfig, error) {
	args := m.Called(ctx, provider, service)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.ServiceConfig), args.Error(1)
}

// SaveServiceConfig mocks the SaveServiceConfig operation
func (m *MockConfigStore) SaveServiceConfig(ctx context.Context, cfg *config.ServiceConfig) error {
	args := m.Called(ctx, cfg)
	return args.Error(0)
}

// ListServiceConfigs mocks the ListServiceConfigs operation
func (m *MockConfigStore) ListServiceConfigs(ctx context.Context) ([]config.ServiceConfig, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.ServiceConfig), args.Error(1)
}

// CreatePurchasePlan mocks the CreatePurchasePlan operation
func (m *MockConfigStore) CreatePurchasePlan(ctx context.Context, plan *config.PurchasePlan) error {
	args := m.Called(ctx, plan)
	return args.Error(0)
}

// GetPurchasePlan mocks the GetPurchasePlan operation
func (m *MockConfigStore) GetPurchasePlan(ctx context.Context, planID string) (*config.PurchasePlan, error) {
	args := m.Called(ctx, planID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.PurchasePlan), args.Error(1)
}

// UpdatePurchasePlan mocks the UpdatePurchasePlan operation
func (m *MockConfigStore) UpdatePurchasePlan(ctx context.Context, plan *config.PurchasePlan) error {
	args := m.Called(ctx, plan)
	return args.Error(0)
}

// DeletePurchasePlan mocks the DeletePurchasePlan operation
func (m *MockConfigStore) DeletePurchasePlan(ctx context.Context, planID string) error {
	args := m.Called(ctx, planID)
	return args.Error(0)
}

// ListPurchasePlans mocks the ListPurchasePlans operation
func (m *MockConfigStore) ListPurchasePlans(ctx context.Context) ([]config.PurchasePlan, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.PurchasePlan), args.Error(1)
}

// SavePurchaseExecution mocks the SavePurchaseExecution operation
func (m *MockConfigStore) SavePurchaseExecution(ctx context.Context, exec *config.PurchaseExecution) error {
	args := m.Called(ctx, exec)
	return args.Error(0)
}

// TransitionExecutionStatus mocks the TransitionExecutionStatus operation
func (m *MockConfigStore) TransitionExecutionStatus(ctx context.Context, executionID string, fromStatuses []string, toStatus string) (*config.PurchaseExecution, error) {
	args := m.Called(ctx, executionID, fromStatuses, toStatus)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.PurchaseExecution), args.Error(1)
}

// GetPendingExecutions mocks the GetPendingExecutions operation
func (m *MockConfigStore) GetPendingExecutions(ctx context.Context) ([]config.PurchaseExecution, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.PurchaseExecution), args.Error(1)
}

// GetExecutionByID mocks the GetExecutionByID operation
func (m *MockConfigStore) GetExecutionByID(ctx context.Context, executionID string) (*config.PurchaseExecution, error) {
	args := m.Called(ctx, executionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.PurchaseExecution), args.Error(1)
}

// GetExecutionByPlanAndDate mocks the GetExecutionByPlanAndDate operation
func (m *MockConfigStore) GetExecutionByPlanAndDate(ctx context.Context, planID string, scheduledDate time.Time) (*config.PurchaseExecution, error) {
	args := m.Called(ctx, planID, scheduledDate)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.PurchaseExecution), args.Error(1)
}

// SavePurchaseHistory mocks the SavePurchaseHistory operation
func (m *MockConfigStore) SavePurchaseHistory(ctx context.Context, record *config.PurchaseHistoryRecord) error {
	args := m.Called(ctx, record)
	return args.Error(0)
}

// GetPurchaseHistory mocks the GetPurchaseHistory operation
func (m *MockConfigStore) GetPurchaseHistory(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
	args := m.Called(ctx, accountID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.PurchaseHistoryRecord), args.Error(1)
}

// GetAllPurchaseHistory mocks the GetAllPurchaseHistory operation
func (m *MockConfigStore) GetAllPurchaseHistory(ctx context.Context, limit int) ([]config.PurchaseHistoryRecord, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.PurchaseHistoryRecord), args.Error(1)
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

// MockAuthStore is a mock implementation of auth.Store
type MockAuthStore struct {
	mock.Mock
}

// GetUserByID mocks the GetUserByID operation
func (m *MockAuthStore) GetUserByID(ctx context.Context, userID string) (*auth.User, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.User), args.Error(1)
}

// GetUserByEmail mocks the GetUserByEmail operation
func (m *MockAuthStore) GetUserByEmail(ctx context.Context, email string) (*auth.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.User), args.Error(1)
}

// CreateUser mocks the CreateUser operation
func (m *MockAuthStore) CreateUser(ctx context.Context, user *auth.User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

// UpdateUser mocks the UpdateUser operation
func (m *MockAuthStore) UpdateUser(ctx context.Context, user *auth.User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

// DeleteUser mocks the DeleteUser operation
func (m *MockAuthStore) DeleteUser(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

// ListUsers mocks the ListUsers operation
func (m *MockAuthStore) ListUsers(ctx context.Context) ([]auth.User, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]auth.User), args.Error(1)
}

// GetUserByResetToken mocks the GetUserByResetToken operation
func (m *MockAuthStore) GetUserByResetToken(ctx context.Context, token string) (*auth.User, error) {
	args := m.Called(ctx, token)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.User), args.Error(1)
}

// AdminExists mocks the AdminExists operation
func (m *MockAuthStore) AdminExists(ctx context.Context) (bool, error) {
	args := m.Called(ctx)
	return args.Bool(0), args.Error(1)
}

// GetGroup mocks the GetGroup operation
func (m *MockAuthStore) GetGroup(ctx context.Context, groupID string) (*auth.Group, error) {
	args := m.Called(ctx, groupID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.Group), args.Error(1)
}

// CreateGroup mocks the CreateGroup operation
func (m *MockAuthStore) CreateGroup(ctx context.Context, group *auth.Group) error {
	args := m.Called(ctx, group)
	return args.Error(0)
}

// UpdateGroup mocks the UpdateGroup operation
func (m *MockAuthStore) UpdateGroup(ctx context.Context, group *auth.Group) error {
	args := m.Called(ctx, group)
	return args.Error(0)
}

// DeleteGroup mocks the DeleteGroup operation
func (m *MockAuthStore) DeleteGroup(ctx context.Context, groupID string) error {
	args := m.Called(ctx, groupID)
	return args.Error(0)
}

// ListGroups mocks the ListGroups operation
func (m *MockAuthStore) ListGroups(ctx context.Context) ([]auth.Group, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]auth.Group), args.Error(1)
}

// CreateSession mocks the CreateSession operation
func (m *MockAuthStore) CreateSession(ctx context.Context, session *auth.Session) error {
	args := m.Called(ctx, session)
	return args.Error(0)
}

// GetSession mocks the GetSession operation
func (m *MockAuthStore) GetSession(ctx context.Context, token string) (*auth.Session, error) {
	args := m.Called(ctx, token)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.Session), args.Error(1)
}

// DeleteSession mocks the DeleteSession operation
func (m *MockAuthStore) DeleteSession(ctx context.Context, token string) error {
	args := m.Called(ctx, token)
	return args.Error(0)
}

// DeleteUserSessions mocks the DeleteUserSessions operation
func (m *MockAuthStore) DeleteUserSessions(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

// CleanupExpiredSessions mocks the CleanupExpiredSessions operation
func (m *MockAuthStore) CleanupExpiredSessions(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// API Key operations

// CreateAPIKey mocks the CreateAPIKey operation
func (m *MockAuthStore) CreateAPIKey(ctx context.Context, key *auth.UserAPIKey) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

// GetAPIKeyByID mocks the GetAPIKeyByID operation
func (m *MockAuthStore) GetAPIKeyByID(ctx context.Context, keyID string) (*auth.UserAPIKey, error) {
	args := m.Called(ctx, keyID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.UserAPIKey), args.Error(1)
}

// GetAPIKeyByHash mocks the GetAPIKeyByHash operation
func (m *MockAuthStore) GetAPIKeyByHash(ctx context.Context, keyHash string) (*auth.UserAPIKey, error) {
	args := m.Called(ctx, keyHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.UserAPIKey), args.Error(1)
}

// ListAPIKeysByUser mocks the ListAPIKeysByUser operation
func (m *MockAuthStore) ListAPIKeysByUser(ctx context.Context, userID string) ([]*auth.UserAPIKey, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*auth.UserAPIKey), args.Error(1)
}

// UpdateAPIKey mocks the UpdateAPIKey operation
func (m *MockAuthStore) UpdateAPIKey(ctx context.Context, key *auth.UserAPIKey) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

// UpdateAPIKeyLastUsed mocks the UpdateAPIKeyLastUsed operation
func (m *MockAuthStore) UpdateAPIKeyLastUsed(ctx context.Context, keyID string) error {
	args := m.Called(ctx, keyID)
	return args.Error(0)
}

// DeleteAPIKey mocks the DeleteAPIKey operation
func (m *MockAuthStore) DeleteAPIKey(ctx context.Context, keyID string) error {
	args := m.Called(ctx, keyID)
	return args.Error(0)
}

// Ping mocks the Ping operation
func (m *MockAuthStore) Ping(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// Cloud accounts

func (m *MockConfigStore) CreateCloudAccount(ctx context.Context, account *config.CloudAccount) error {
	args := m.Called(ctx, account)
	return args.Error(0)
}

func (m *MockConfigStore) GetCloudAccount(ctx context.Context, id string) (*config.CloudAccount, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.CloudAccount), args.Error(1)
}

func (m *MockConfigStore) UpdateCloudAccount(ctx context.Context, account *config.CloudAccount) error {
	args := m.Called(ctx, account)
	return args.Error(0)
}

func (m *MockConfigStore) DeleteCloudAccount(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockConfigStore) ListCloudAccounts(ctx context.Context, filter config.CloudAccountFilter) ([]config.CloudAccount, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.CloudAccount), args.Error(1)
}

// Account credentials

func (m *MockConfigStore) SaveAccountCredential(ctx context.Context, accountID, credentialType, encryptedBlob string) error {
	args := m.Called(ctx, accountID, credentialType, encryptedBlob)
	return args.Error(0)
}

func (m *MockConfigStore) GetAccountCredential(ctx context.Context, accountID, credentialType string) (string, error) {
	args := m.Called(ctx, accountID, credentialType)
	return args.String(0), args.Error(1)
}

func (m *MockConfigStore) DeleteAccountCredentials(ctx context.Context, accountID string) error {
	args := m.Called(ctx, accountID)
	return args.Error(0)
}

func (m *MockConfigStore) HasAccountCredentials(ctx context.Context, accountID string) (bool, error) {
	args := m.Called(ctx, accountID)
	return args.Bool(0), args.Error(1)
}

// Account service overrides

func (m *MockConfigStore) GetAccountServiceOverride(ctx context.Context, accountID, provider, service string) (*config.AccountServiceOverride, error) {
	args := m.Called(ctx, accountID, provider, service)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.AccountServiceOverride), args.Error(1)
}

func (m *MockConfigStore) SaveAccountServiceOverride(ctx context.Context, override *config.AccountServiceOverride) error {
	args := m.Called(ctx, override)
	return args.Error(0)
}

func (m *MockConfigStore) DeleteAccountServiceOverride(ctx context.Context, accountID, provider, service string) error {
	args := m.Called(ctx, accountID, provider, service)
	return args.Error(0)
}

func (m *MockConfigStore) ListAccountServiceOverrides(ctx context.Context, accountID string) ([]config.AccountServiceOverride, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.AccountServiceOverride), args.Error(1)
}

// Plan ↔ account association

func (m *MockConfigStore) SetPlanAccounts(ctx context.Context, planID string, accountIDs []string) error {
	args := m.Called(ctx, planID, accountIDs)
	return args.Error(0)
}

func (m *MockConfigStore) GetPlanAccounts(ctx context.Context, planID string) ([]config.CloudAccount, error) {
	args := m.Called(ctx, planID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.CloudAccount), args.Error(1)
}

// CleanupOldExecutions mocks the CleanupOldExecutions operation
func (m *MockConfigStore) CleanupOldExecutions(ctx context.Context, retentionDays int) (int64, error) {
	args := m.Called(ctx, retentionDays)
	return args.Get(0).(int64), args.Error(1)
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

// Compile-time interface compliance check
var _ auth.StoreInterface = (*MockAuthStore)(nil)
