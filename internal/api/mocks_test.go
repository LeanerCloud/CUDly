package api

import (
	"context"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/scheduler"
	"github.com/stretchr/testify/mock"
)

// MockConfigStore is a mock implementation of config.Store
type MockConfigStore struct {
	mock.Mock
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

// MockPurchaseManager is a mock implementation of purchase.Manager
type MockPurchaseManager struct {
	mock.Mock
}

func (m *MockPurchaseManager) ApproveExecution(ctx context.Context, execID, token string) error {
	args := m.Called(ctx, execID, token)
	return args.Error(0)
}

func (m *MockPurchaseManager) CancelExecution(ctx context.Context, execID, token string) error {
	args := m.Called(ctx, execID, token)
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

func (m *MockScheduler) GetRecommendations(ctx context.Context, params scheduler.RecommendationQueryParams) ([]config.RecommendationRecord, error) {
	args := m.Called(ctx, params)
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
