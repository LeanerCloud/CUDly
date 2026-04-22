package scheduler

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockProviderFactory is a mock implementation of ProviderFactoryInterface
type MockProviderFactory struct {
	mock.Mock
}

func (m *MockProviderFactory) CreateAndValidateProvider(ctx context.Context, name string, cfg *provider.ProviderConfig) (provider.Provider, error) {
	args := m.Called(ctx, name, cfg)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(provider.Provider), args.Error(1)
}

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
	return nil, nil
}
func (m *MockConfigStore) UpdateCloudAccount(ctx context.Context, account *config.CloudAccount) error {
	return nil
}
func (m *MockConfigStore) DeleteCloudAccount(ctx context.Context, id string) error {
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
func (m *MockConfigStore) CreateAccountRegistration(_ context.Context, _ *config.AccountRegistration) error {
	return nil
}
func (m *MockConfigStore) GetAccountRegistration(_ context.Context, _ string) (*config.AccountRegistration, error) {
	return nil, nil
}
func (m *MockConfigStore) GetAccountRegistrationByToken(_ context.Context, _ string) (*config.AccountRegistration, error) {
	return nil, nil
}
func (m *MockConfigStore) ListAccountRegistrations(_ context.Context, _ config.AccountRegistrationFilter) ([]config.AccountRegistration, error) {
	return nil, nil
}
func (m *MockConfigStore) UpdateAccountRegistration(_ context.Context, _ *config.AccountRegistration) error {
	return nil
}
func (m *MockConfigStore) TransitionRegistrationStatus(_ context.Context, _ *config.AccountRegistration, _ string) error {
	return nil
}
func (m *MockConfigStore) DeleteAccountRegistration(_ context.Context, _ string) error {
	return nil
}

// hasExpectation reports whether the test registered an expectation for the
// given method. Lets the recommendations-cache stubs below default to
// returning zero-values for pre-existing tests that predate the cache and
// don't care about these methods, while still letting new tests assert via
// .On(...).Return(...).
func (m *MockConfigStore) hasExpectation(method string) bool {
	for i := range m.ExpectedCalls {
		if m.ExpectedCalls[i].Method == method {
			return true
		}
	}
	return false
}

func (m *MockConfigStore) ReplaceRecommendations(ctx context.Context, collectedAt time.Time, recs []config.RecommendationRecord) error {
	if !m.hasExpectation("ReplaceRecommendations") {
		return nil
	}
	return m.Called(ctx, collectedAt, recs).Error(0)
}
func (m *MockConfigStore) UpsertRecommendations(ctx context.Context, collectedAt time.Time, recs []config.RecommendationRecord, successfulCollects []config.SuccessfulCollect) error {
	if !m.hasExpectation("UpsertRecommendations") {
		return nil
	}
	return m.Called(ctx, collectedAt, recs, successfulCollects).Error(0)
}
func (m *MockConfigStore) ListStoredRecommendations(ctx context.Context, filter config.RecommendationFilter) ([]config.RecommendationRecord, error) {
	if !m.hasExpectation("ListStoredRecommendations") {
		return nil, nil
	}
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.RecommendationRecord), args.Error(1)
}
func (m *MockConfigStore) GetRecommendationsFreshness(ctx context.Context) (*config.RecommendationsFreshness, error) {
	if !m.hasExpectation("GetRecommendationsFreshness") {
		return &config.RecommendationsFreshness{}, nil
	}
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.RecommendationsFreshness), args.Error(1)
}
func (m *MockConfigStore) SetRecommendationsCollectionError(ctx context.Context, errMsg string) error {
	if !m.hasExpectation("SetRecommendationsCollectionError") {
		return nil
	}
	return m.Called(ctx, errMsg).Error(0)
}
func (m *MockConfigStore) GetRIUtilizationCache(ctx context.Context, region string, lookbackDays int) (*config.RIUtilizationCacheEntry, error) {
	if !m.hasExpectation("GetRIUtilizationCache") {
		return nil, nil
	}
	args := m.Called(ctx, region, lookbackDays)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*config.RIUtilizationCacheEntry), args.Error(1)
}
func (m *MockConfigStore) UpsertRIUtilizationCache(ctx context.Context, region string, lookbackDays int, payload []byte, fetchedAt time.Time) error {
	if !m.hasExpectation("UpsertRIUtilizationCache") {
		return nil
	}
	return m.Called(ctx, region, lookbackDays, payload, fetchedAt).Error(0)
}

// MockEmailSender is a mock implementation of email.Sender
type MockEmailSender struct {
	mock.Mock
}

func (m *MockEmailSender) SendNotification(ctx context.Context, subject, message string) error {
	args := m.Called(ctx, subject, message)
	return args.Error(0)
}

func (m *MockEmailSender) SendToEmail(ctx context.Context, toEmail, subject, body string) error {
	args := m.Called(ctx, toEmail, subject, body)
	return args.Error(0)
}

func (m *MockEmailSender) SendNewRecommendationsNotification(ctx context.Context, data email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

func (m *MockEmailSender) SendScheduledPurchaseNotification(ctx context.Context, data email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

func (m *MockEmailSender) SendPurchaseConfirmation(ctx context.Context, data email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

func (m *MockEmailSender) SendPurchaseFailedNotification(ctx context.Context, data email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

func (m *MockEmailSender) SendPasswordResetEmail(ctx context.Context, email, resetURL string) error {
	args := m.Called(ctx, email, resetURL)
	return args.Error(0)
}

func (m *MockEmailSender) SendWelcomeEmail(ctx context.Context, email, dashboardURL, role string) error {
	args := m.Called(ctx, email, dashboardURL, role)
	return args.Error(0)
}

func (m *MockEmailSender) SendRIExchangePendingApproval(ctx context.Context, data email.RIExchangeNotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

func (m *MockEmailSender) SendRIExchangeCompleted(ctx context.Context, data email.RIExchangeNotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

func (m *MockEmailSender) SendPurchaseApprovalRequest(ctx context.Context, data email.NotificationData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}
func (m *MockEmailSender) SendRegistrationReceivedNotification(_ context.Context, _ email.RegistrationNotificationData) error {
	return nil
}
func (m *MockEmailSender) SendRegistrationDecisionNotification(_ context.Context, _ string, _ email.RegistrationDecisionData) error {
	return nil
}

// MockPurchaseManager is a mock implementation of purchase.Manager
type MockPurchaseManager struct {
	mock.Mock
}

func (m *MockPurchaseManager) ProcessScheduledPurchases(ctx context.Context) (*purchase.ProcessResult, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*purchase.ProcessResult), args.Error(1)
}

func (m *MockPurchaseManager) SendUpcomingPurchaseNotifications(ctx context.Context) (*purchase.NotificationResult, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*purchase.NotificationResult), args.Error(1)
}

func TestSchedulerConfig(t *testing.T) {
	mockStore := new(MockConfigStore)
	mockPurchase := new(MockPurchaseManager)
	mockEmail := new(MockEmailSender)

	cfg := SchedulerConfig{
		ConfigStore:     mockStore,
		PurchaseManager: nil, // We'd use mockPurchase but types don't match in test
		EmailSender:     nil, // We'd use mockEmail but types don't match in test
		DashboardURL:    "https://dashboard.example.com",
	}

	assert.NotNil(t, cfg.ConfigStore)
	assert.Equal(t, "https://dashboard.example.com", cfg.DashboardURL)

	// Just to use the mocks
	_ = mockPurchase
	_ = mockEmail
}

func TestNewScheduler(t *testing.T) {
	mockStore := new(MockConfigStore)

	cfg := SchedulerConfig{
		ConfigStore:  mockStore,
		DashboardURL: "https://dashboard.example.com",
	}

	scheduler := NewScheduler(cfg)

	assert.NotNil(t, scheduler)
	assert.Equal(t, "https://dashboard.example.com", scheduler.dashboardURL)
}

func TestScheduler_CollectRecommendations_NoProviders(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	globalCfg := &config.GlobalConfig{
		EnabledProviders: []string{},
	}

	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)

	scheduler := &Scheduler{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := scheduler.CollectRecommendations(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Recommendations)
	assert.Equal(t, float64(0), result.TotalSavings)
}

func TestScheduler_CollectRecommendations_AWSProvider(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)

	globalCfg := &config.GlobalConfig{
		EnabledProviders: []string{"aws"},
		DefaultTerm:      3,
		DefaultPayment:   "all-upfront",
	}

	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	// Mock provider factory to return error (simulating no credentials)
	mockFactory.On("CreateAndValidateProvider", ctx, mock.Anything, mock.Anything).
		Return(nil, assert.AnError)

	scheduler := &Scheduler{
		config:          mockStore,
		email:           mockEmail,
		dashboardURL:    "https://dashboard.example.com",
		providerFactory: mockFactory,
	}

	result, err := scheduler.CollectRecommendations(ctx)
	require.NoError(t, err)

	// Provider returns error, so no recommendations
	assert.Equal(t, 0, result.Recommendations)
}

func TestScheduler_CollectRecommendations_AllProviders(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)

	globalCfg := &config.GlobalConfig{
		EnabledProviders: []string{"aws", "azure", "gcp"},
		DefaultTerm:      3,
		DefaultPayment:   "all-upfront",
	}

	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	// Mock provider factory to return error for all providers (simulating no credentials)
	mockFactory.On("CreateAndValidateProvider", ctx, mock.Anything, mock.Anything).
		Return(nil, assert.AnError)

	scheduler := &Scheduler{
		config:          mockStore,
		email:           mockEmail,
		dashboardURL:    "https://dashboard.example.com",
		providerFactory: mockFactory,
	}

	result, err := scheduler.CollectRecommendations(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Recommendations)
}

func TestScheduler_CollectRecommendations_UnknownProvider(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)

	globalCfg := &config.GlobalConfig{
		EnabledProviders: []string{"unknown_provider"},
	}

	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)

	scheduler := &Scheduler{
		config:          mockStore,
		email:           mockEmail,
		dashboardURL:    "https://dashboard.example.com",
		providerFactory: mockFactory,
	}

	result, err := scheduler.CollectRecommendations(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Recommendations)
}

func TestScheduler_CollectAWSRecommendations(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)

	globalCfg := &config.GlobalConfig{
		DefaultTerm:    3,
		DefaultPayment: "all-upfront",
	}

	// Mock provider factory to return error (simulating no credentials)
	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).
		Return(nil, assert.AnError)

	scheduler := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	recs, _, err := scheduler.collectAWSRecommendations(ctx, globalCfg)
	require.Error(t, err) // Should error due to mock provider failing
	assert.Nil(t, recs)
}

func TestScheduler_CollectAzureRecommendations_NoAccounts(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	globalCfg := &config.GlobalConfig{
		DefaultTerm:    3,
		DefaultPayment: "all-upfront",
	}

	mockStore.On("ListCloudAccounts", ctx, mock.Anything).Return([]config.CloudAccount{}, nil)

	scheduler := &Scheduler{config: mockStore}

	recs, _, err := scheduler.collectAzureRecommendations(ctx, globalCfg)
	require.NoError(t, err)
	assert.Empty(t, recs)
}

func TestScheduler_CollectGCPRecommendations_NoAccounts_Alt(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	globalCfg := &config.GlobalConfig{
		DefaultTerm:    3,
		DefaultPayment: "all-upfront",
	}

	mockStore.On("ListCloudAccounts", ctx, mock.Anything).Return([]config.CloudAccount{}, nil)

	scheduler := &Scheduler{config: mockStore}

	recs, _, err := scheduler.collectGCPRecommendations(ctx, globalCfg)
	require.NoError(t, err)
	assert.Empty(t, recs)
}

func TestScheduler_CollectProviderRecommendations(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)

	globalCfg := &config.GlobalConfig{
		DefaultTerm:    3,
		DefaultPayment: "all-upfront",
	}

	// AWS ambient fallback: factory returns error
	mockFactory.On("CreateAndValidateProvider", ctx, mock.Anything, mock.Anything).
		Return(nil, assert.AnError)
	// Azure/GCP: no accounts → skip gracefully
	mockStore.On("ListCloudAccounts", ctx, mock.Anything).Return([]config.CloudAccount{}, nil)

	scheduler := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	tests := []struct {
		provider    string
		expectError bool
	}{
		{"aws", true},    // ambient fallback fails via factory error
		{"azure", false}, // no accounts → nil, nil
		{"gcp", false},   // no accounts → nil, nil
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			recs, _, err := scheduler.collectProviderRecommendations(ctx, tt.provider, globalCfg)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Empty(t, recs)
		})
	}
}

// Integration-style test for email notification
func TestScheduler_CollectRecommendations_WithNotification(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)

	// This test verifies that when there are no recommendations,
	// no email is sent
	globalCfg := &config.GlobalConfig{
		EnabledProviders: []string{"aws"},
		DefaultTerm:      3,
		DefaultPayment:   "all-upfront",
	}

	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	// Mock provider factory to return error (simulating no credentials)
	mockFactory.On("CreateAndValidateProvider", ctx, mock.Anything, mock.Anything).
		Return(nil, assert.AnError)
	// No expectation for SendNewRecommendationsNotification because
	// there are no recommendations

	scheduler := &Scheduler{
		config:          mockStore,
		email:           mockEmail,
		dashboardURL:    "https://dashboard.example.com",
		providerFactory: mockFactory,
	}

	result, err := scheduler.CollectRecommendations(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Recommendations)

	// Verify no email was sent
	mockEmail.AssertNotCalled(t, "SendNewRecommendationsNotification")
}

// Test that verifies the struct implements expected interface
func TestScheduler_Interface(t *testing.T) {
	mockStore := new(MockConfigStore)

	cfg := SchedulerConfig{
		ConfigStore:  mockStore,
		DashboardURL: "https://test.example.com",
	}

	scheduler := NewScheduler(cfg)

	// Verify scheduler has required fields
	assert.NotNil(t, scheduler.config)
	assert.Equal(t, "https://test.example.com", scheduler.dashboardURL)
}

// Test edge cases
func TestScheduler_CollectRecommendations_ConfigError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)

	mockStore.On("GetGlobalConfig", ctx).Return(nil, assert.AnError)

	scheduler := &Scheduler{
		config:       mockStore,
		email:        mockEmail,
		dashboardURL: "https://dashboard.example.com",
	}

	result, err := scheduler.CollectRecommendations(ctx)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// Helper function tests
func TestSchedulerConfigStoreInterface(t *testing.T) {
	// Verify MockConfigStore implements all required methods
	store := new(MockConfigStore)
	ctx := context.Background()

	// These calls just verify the mock has the methods
	store.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	store.On("ListServiceConfigs", ctx).Return([]config.ServiceConfig{}, nil)
	store.On("ListPurchasePlans", ctx).Return([]config.PurchasePlan{}, nil)

	_, _ = store.GetGlobalConfig(ctx)
	_, _ = store.ListServiceConfigs(ctx)
	_, _ = store.ListPurchasePlans(ctx)

	store.AssertExpectations(t)
}

// Test purchase.Manager integration
func TestSchedulerWithPurchaseManager(t *testing.T) {
	mockStore := new(MockConfigStore)
	mockPurchase := new(MockPurchaseManager)
	mockEmail := new(MockEmailSender)

	// The scheduler should work with a purchase manager (using interface)
	scheduler := &Scheduler{
		config:       mockStore,
		purchase:     mockPurchase,
		email:        mockEmail,
		dashboardURL: "https://test.example.com",
	}

	// Verify scheduler was created with correct fields
	assert.NotNil(t, scheduler)
	assert.NotNil(t, scheduler.config)
	assert.NotNil(t, scheduler.purchase)
	assert.NotNil(t, scheduler.email)
}

// MockProvider is a mock implementation of provider.Provider
type MockProvider struct {
	mock.Mock
}

func (m *MockProvider) Name() string {
	return "mock"
}

func (m *MockProvider) DisplayName() string {
	return "Mock Provider"
}

func (m *MockProvider) IsConfigured() bool {
	return true
}

func (m *MockProvider) GetCredentials() (provider.Credentials, error) {
	return nil, nil
}

func (m *MockProvider) ValidateCredentials(ctx context.Context) error {
	return nil
}

func (m *MockProvider) GetAccounts(ctx context.Context) ([]common.Account, error) {
	return nil, nil
}

func (m *MockProvider) GetRegions(ctx context.Context) ([]common.Region, error) {
	return nil, nil
}

func (m *MockProvider) GetDefaultRegion() string {
	return "us-east-1"
}

func (m *MockProvider) GetSupportedServices() []common.ServiceType {
	return []common.ServiceType{common.ServiceEC2, common.ServiceRDS}
}

func (m *MockProvider) GetServiceClient(ctx context.Context, serviceType common.ServiceType, region string) (provider.ServiceClient, error) {
	args := m.Called(ctx, serviceType, region)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(provider.ServiceClient), args.Error(1)
}

func (m *MockProvider) GetRecommendationsClient(ctx context.Context) (provider.RecommendationsClient, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(provider.RecommendationsClient), args.Error(1)
}

// MockRecommendationsClient is a mock implementation of provider.RecommendationsClient
type MockRecommendationsClient struct {
	mock.Mock
}

func (m *MockRecommendationsClient) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]common.Recommendation), args.Error(1)
}

func (m *MockRecommendationsClient) GetAllRecommendations(ctx context.Context) ([]common.Recommendation, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]common.Recommendation), args.Error(1)
}

func (m *MockRecommendationsClient) GetRecommendationsForService(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error) {
	args := m.Called(ctx, service)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]common.Recommendation), args.Error(1)
}

// Test ListRecommendations method
// ListRecommendations reads from the recommendations cache rather than
// doing live cloud API calls. The tests below cover the cache-read path
// (warm cache) + filter pass-through. Cold-start behavior is covered by
// TestScheduler_ListRecommendations_ColdStart.
func TestScheduler_ListRecommendations(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	now := time.Now().UTC()
	cached := []config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", Region: "us-east-1", Savings: 500},
		{Provider: "aws", Service: "rds", Region: "us-west-2", Savings: 200},
	}

	mockStore.On("GetRecommendationsFreshness", ctx).
		Return(&config.RecommendationsFreshness{LastCollectedAt: &now}, nil)
	mockStore.On("ListStoredRecommendations", ctx, mock.Anything).
		Return(cached, nil)

	scheduler := &Scheduler{config: mockStore}

	recs, err := scheduler.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	assert.Len(t, recs, 2)
}

// Filter pass-through: the handler-level RecommendationQueryParams fields
// map into the DB-facing RecommendationFilter. The SQL pushdown semantics
// themselves are covered by store_postgres_recommendations_test.go.
func TestScheduler_ListRecommendations_PassesFilterToStore(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	now := time.Now().UTC()
	mockStore.On("GetRecommendationsFreshness", ctx).
		Return(&config.RecommendationsFreshness{LastCollectedAt: &now}, nil)

	expected := config.RecommendationFilter{
		Provider:   "aws",
		Service:    "ec2",
		Region:     "us-east-1",
		AccountIDs: []string{"acct-1"},
	}
	mockStore.On("ListStoredRecommendations", ctx, expected).
		Return([]config.RecommendationRecord{}, nil)

	scheduler := &Scheduler{config: mockStore}

	_, err := scheduler.ListRecommendations(ctx, config.RecommendationFilter{
		Provider:   "aws",
		Service:    "ec2",
		Region:     "us-east-1",
		AccountIDs: []string{"acct-1"},
	})
	require.NoError(t, err)
	mockStore.AssertExpectations(t)
}

// Error surface: a store error on the freshness read bubbles up.
func TestScheduler_ListRecommendations_FreshnessError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	mockStore.On("GetRecommendationsFreshness", ctx).Return(nil, assert.AnError)

	scheduler := &Scheduler{config: mockStore}
	recs, err := scheduler.ListRecommendations(ctx, config.RecommendationFilter{})
	require.Error(t, err)
	assert.Nil(t, recs)
}

// Lambda runtime must NOT spawn a background refresh even when the
// cache is stale — goroutines freeze between invocations.
func TestScheduler_ListRecommendations_LambdaSkipsBackgroundRefresh(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	// Cache is stale (older than the 1ns TTL set below).
	old := time.Now().Add(-time.Hour)
	mockStore.On("GetRecommendationsFreshness", ctx).
		Return(&config.RecommendationsFreshness{LastCollectedAt: &old}, nil)
	mockStore.On("ListStoredRecommendations", ctx, mock.Anything).
		Return([]config.RecommendationRecord{}, nil)

	scheduler := &Scheduler{
		config:   mockStore,
		isLambda: true,
		cacheTTL: time.Nanosecond,
	}

	_, err := scheduler.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)

	// Give any (wrongly-spawned) goroutine time to hit the store; none
	// should fire, so no GetGlobalConfig call should be observed.
	time.Sleep(20 * time.Millisecond)

	mockStore.AssertNotCalled(t, "GetGlobalConfig", mock.Anything)
	assert.False(t, scheduler.collecting.Load(), "collecting flag must stay unset on Lambda")
}

// Non-Lambda stale reads kick a single background refresh regardless of
// how many concurrent callers hit the stale cache.
func TestScheduler_ListRecommendations_StaleSingleFlight(t *testing.T) {
	ctx := context.Background()

	scheduler := &Scheduler{
		isLambda: false,
		cacheTTL: time.Nanosecond, // force stale
	}

	old := time.Now().Add(-time.Hour)
	freshness := &config.RecommendationsFreshness{LastCollectedAt: &old}

	// Seed the flag as though a refresh is already in flight. The
	// guard short-circuits and no new goroutine fires.
	scheduler.collecting.Store(true)
	scheduler.maybeKickBackgroundRefresh(freshness)
	assert.True(t, scheduler.collecting.Load(), "in-flight flag must not be cleared by the guard path")
	_ = ctx
}

// Cold-start (LastCollectedAt==nil) triggers a synchronous
// CollectRecommendations before the read so the user sees real data
// rather than an empty table.
func TestScheduler_ListRecommendations_ColdStartSync(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	// Freshness reports cold cache.
	mockStore.On("GetRecommendationsFreshness", ctx).
		Return(&config.RecommendationsFreshness{LastCollectedAt: nil}, nil)

	// Cold-start drills into CollectRecommendations, which needs the
	// global config. Return no enabled providers so the collect is a
	// no-op but still runs the persistence path.
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{EnabledProviders: []string{}}, nil)
	mockStore.On("UpsertRecommendations", ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockStore.On("ListStoredRecommendations", ctx, mock.Anything).
		Return([]config.RecommendationRecord{}, nil)

	scheduler := &Scheduler{config: mockStore}

	_, err := scheduler.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)

	// Assert the cold-start path ran: GetGlobalConfig is only called by
	// CollectRecommendations, so seeing it prove the sync collect fired
	// before the store read.
	mockStore.AssertCalled(t, "GetGlobalConfig", ctx)
	mockStore.AssertCalled(t, "UpsertRecommendations", ctx, mock.Anything, mock.Anything, mock.Anything)
}

// fanOutPerAccount bounds parallel in-flight calls to
// CUDLY_MAX_ACCOUNT_PARALLELISM. Test with a fake fn that increments an
// atomic counter on entry and decrements on exit, assert peak stays at
// or below the limit.
func TestFanOutPerAccount_RespectsParallelismLimit(t *testing.T) {
	t.Setenv("CUDLY_MAX_ACCOUNT_PARALLELISM", "3")

	// 20 accounts to make the bound observable.
	accounts := make([]config.CloudAccount, 20)
	for i := range accounts {
		accounts[i] = config.CloudAccount{ID: fmt.Sprintf("acct-%d", i), Name: fmt.Sprintf("acct-%d", i)}
	}

	var inflight atomic.Int32
	var peak atomic.Int32
	updatePeak := func(cur int32) {
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				return
			}
		}
	}

	fn := func(ctx context.Context, acct config.CloudAccount) ([]config.RecommendationRecord, error) {
		cur := inflight.Add(1)
		updatePeak(cur)
		// Small sleep so concurrent workers genuinely overlap.
		time.Sleep(5 * time.Millisecond)
		inflight.Add(-1)
		return []config.RecommendationRecord{{ID: acct.ID}}, nil
	}

	out, outcome := fanOutPerAccount(context.Background(), "Test", accounts, fn)
	assert.Len(t, out, len(accounts), "all accounts contribute one record")
	assert.LessOrEqual(t, peak.Load(), int32(3), "peak in-flight must not exceed CUDLY_MAX_ACCOUNT_PARALLELISM")
	assert.Equal(t, len(accounts), outcome.SucceededCount)
	assert.Zero(t, outcome.FailedCount)
}

// TestFanOutPerAccount_AllAccountsFail pins the new accountOutcome
// contract: when every account's fn returns an error, the outcome
// reports zero successes, len(accounts) failures, and the most-recent
// error message. This is what the per-provider collect funcs check
// to decide "all accounts failed → return error → flag the provider
// as failed in CollectRecommendations".
func TestFanOutPerAccount_AllAccountsFail(t *testing.T) {
	accounts := []config.CloudAccount{
		{ID: "acct-1", Name: "acct-1", ExternalID: "ext-1"},
		{ID: "acct-2", Name: "acct-2", ExternalID: "ext-2"},
		{ID: "acct-3", Name: "acct-3", ExternalID: "ext-3"},
	}
	fn := func(ctx context.Context, acct config.CloudAccount) ([]config.RecommendationRecord, error) {
		return nil, fmt.Errorf("cred error for %s", acct.ID)
	}

	recs, outcome := fanOutPerAccount(context.Background(), "Test", accounts, fn)
	assert.Empty(t, recs, "no recs should accumulate when every account fails")
	assert.Zero(t, outcome.SucceededCount)
	assert.Equal(t, 3, outcome.FailedCount)
	assert.Contains(t, outcome.LastErr, "cred error for acct-",
		"LastErr should carry one of the per-account errors (any one — order is non-deterministic)")
	assert.Contains(t, outcome.LastErr, "account",
		"LastErr should be formatted with the account context that the freshness banner can show")
}

// TestFanOutPerAccount_PartialSuccess: 2 accounts succeed, 1 fails.
// The succeeded accounts' recs are merged in; outcome reports the
// 2/1 split. The caller then keeps the provider in successfulProviders
// because FailedCount < len(accounts).
func TestFanOutPerAccount_PartialSuccess(t *testing.T) {
	accounts := []config.CloudAccount{
		{ID: "acct-ok-1", Name: "acct-ok-1", ExternalID: "e1"},
		{ID: "acct-ok-2", Name: "acct-ok-2", ExternalID: "e2"},
		{ID: "acct-bad", Name: "acct-bad", ExternalID: "ebad"},
	}
	fn := func(ctx context.Context, acct config.CloudAccount) ([]config.RecommendationRecord, error) {
		if acct.ID == "acct-bad" {
			return nil, fmt.Errorf("transient")
		}
		return []config.RecommendationRecord{{ID: acct.ID, Provider: "test"}}, nil
	}

	recs, outcome := fanOutPerAccount(context.Background(), "Test", accounts, fn)
	assert.Len(t, recs, 2, "only the 2 succeeded accounts contribute recs")
	assert.Equal(t, 2, outcome.SucceededCount)
	assert.Equal(t, 1, outcome.FailedCount)
	assert.Contains(t, outcome.LastErr, "transient",
		"LastErr should reflect the failed account's error")
}

// TestFanOutPerAccount_ZeroAccounts: empty input → empty outcome,
// no errors. The caller's "FailedCount == len(accounts) > 0" guard
// correctly skips the all-failed error path.
func TestFanOutPerAccount_ZeroAccounts(t *testing.T) {
	recs, outcome := fanOutPerAccount(context.Background(), "Test", nil,
		func(ctx context.Context, acct config.CloudAccount) ([]config.RecommendationRecord, error) {
			t.Fatalf("fn must not be called for zero-accounts input")
			return nil, nil
		})
	assert.Empty(t, recs)
	assert.Zero(t, outcome.SucceededCount)
	assert.Zero(t, outcome.FailedCount)
	assert.Empty(t, outcome.LastErr)
}

// persistCollection writes via Upsert and surfaces the per-provider
// error banner when the collection was partial.
func TestScheduler_persistCollection_PartialFailure(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	recs := []config.RecommendationRecord{
		{ID: "r1", Provider: "aws", Service: "ec2", Region: "us-east-1"},
	}
	successful := []config.SuccessfulCollect{{Provider: "aws"}}
	failed := map[string]string{"azure": "auth failed", "gcp": "quota"}

	mockStore.On("UpsertRecommendations", ctx, mock.Anything, recs, successful).Return(nil)
	mockStore.On("SetRecommendationsCollectionError", ctx, mock.Anything).
		Return(nil).
		Run(func(args mock.Arguments) {
			msg := args.String(1)
			// joinProviderErrors sorts alphabetically → azure before gcp.
			assert.Contains(t, msg, "azure: auth failed")
			assert.Contains(t, msg, "gcp: quota")
		})

	s := &Scheduler{config: mockStore}
	s.persistCollection(ctx, recs, successful, failed)

	mockStore.AssertExpectations(t)
}

// On full success (no failed providers), Upsert is called but
// SetRecommendationsCollectionError is NOT — the banner stays dismissed.
func TestScheduler_persistCollection_FullSuccess(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	recs := []config.RecommendationRecord{{ID: "r1", Provider: "aws"}}
	successful := []config.SuccessfulCollect{
		{Provider: "aws"},
		{Provider: "azure"},
		{Provider: "gcp"},
	}

	mockStore.On("UpsertRecommendations", ctx, mock.Anything, recs, successful).Return(nil)

	s := &Scheduler{config: mockStore}
	s.persistCollection(ctx, recs, successful, nil)

	mockStore.AssertExpectations(t)
	mockStore.AssertNotCalled(t, "SetRecommendationsCollectionError", mock.Anything, mock.Anything)
}

// Test convertRecommendations
func TestScheduler_ConvertRecommendations(t *testing.T) {
	scheduler := &Scheduler{}

	recommendations := []common.Recommendation{
		{
			Provider:         common.ProviderAWS,
			Service:          common.ServiceEC2,
			Region:           "us-east-1",
			ResourceType:     "m5.large",
			Count:            5,
			Term:             "3yr",
			PaymentOption:    "all-upfront",
			CommitmentCost:   1000.0,
			EstimatedSavings: 500.0,
		},
		{
			Provider:         common.ProviderAWS,
			Service:          common.ServiceRDS,
			Region:           "us-west-2",
			ResourceType:     "db.m5.large",
			Count:            2,
			Term:             "1yr",
			PaymentOption:    "partial-upfront",
			CommitmentCost:   500.0,
			EstimatedSavings: 200.0,
			Details: common.DatabaseDetails{
				Engine: "mysql",
			},
		},
		{
			Provider:         common.ProviderAWS,
			Service:          common.ServiceElastiCache,
			Region:           "eu-west-1",
			ResourceType:     "cache.m5.large",
			Count:            3,
			Term:             "3yr",
			EstimatedSavings: 300.0,
			Details: &common.CacheDetails{
				Engine: "redis",
			},
		},
	}

	records := scheduler.convertRecommendations(recommendations, "aws")

	require.Len(t, records, 3)

	// Check first record (EC2)
	assert.Equal(t, "aws", records[0].Provider)
	assert.Equal(t, "ec2", records[0].Service)
	assert.Equal(t, "us-east-1", records[0].Region)
	assert.Equal(t, "m5.large", records[0].ResourceType)
	assert.Equal(t, 5, records[0].Count)
	assert.Equal(t, 3, records[0].Term)
	assert.Equal(t, "all-upfront", records[0].Payment)
	assert.Equal(t, 1000.0, records[0].UpfrontCost)
	assert.Equal(t, 500.0, records[0].Savings)
	assert.Equal(t, "", records[0].Engine)
	assert.True(t, records[0].Selected)
	assert.False(t, records[0].Purchased)

	// Check second record (RDS with engine)
	assert.Equal(t, "rds", records[1].Service)
	assert.Equal(t, "mysql", records[1].Engine)
	assert.Equal(t, 1, records[1].Term)

	// Check third record (ElastiCache with pointer details)
	assert.Equal(t, "elasticache", records[2].Service)
	assert.Equal(t, "redis", records[2].Engine)
}

// Test convertRecommendations with empty input
func TestScheduler_ConvertRecommendations_Empty(t *testing.T) {
	scheduler := &Scheduler{}

	records := scheduler.convertRecommendations([]common.Recommendation{}, "aws")
	assert.Len(t, records, 0)
}

// Test successful AWS recommendations with provider returning data
func TestScheduler_CollectAWSRecommendations_Success(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockRecClient := new(MockRecommendationsClient)

	globalCfg := &config.GlobalConfig{
		DefaultTerm:    3,
		DefaultPayment: "all-upfront",
	}

	recommendations := []common.Recommendation{
		{
			Provider:         common.ProviderAWS,
			Service:          common.ServiceEC2,
			Region:           "us-east-1",
			ResourceType:     "m5.large",
			Count:            5,
			Term:             "3yr",
			EstimatedSavings: 500.0,
		},
	}

	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", ctx).Return(mockRecClient, nil)
	mockRecClient.On("GetAllRecommendations", ctx).Return(recommendations, nil)

	scheduler := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	recs, _, err := scheduler.collectAWSRecommendations(ctx, globalCfg)
	require.NoError(t, err)
	assert.Len(t, recs, 1)
	assert.Equal(t, "ec2", recs[0].Service)
}

// Test AWS recommendations when GetRecommendationsClient fails
func TestScheduler_CollectAWSRecommendations_RecClientError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)

	globalCfg := &config.GlobalConfig{
		DefaultTerm:    3,
		DefaultPayment: "all-upfront",
	}

	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", ctx).Return(nil, assert.AnError)

	scheduler := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	recs, _, err := scheduler.collectAWSRecommendations(ctx, globalCfg)
	require.Error(t, err)
	assert.Nil(t, recs)
}

// Test AWS recommendations when GetAllRecommendations fails
func TestScheduler_CollectAWSRecommendations_GetRecsError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockRecClient := new(MockRecommendationsClient)

	globalCfg := &config.GlobalConfig{
		DefaultTerm:    3,
		DefaultPayment: "all-upfront",
	}

	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", ctx).Return(mockRecClient, nil)
	mockRecClient.On("GetAllRecommendations", ctx).Return(nil, assert.AnError)

	scheduler := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	recs, _, err := scheduler.collectAWSRecommendations(ctx, globalCfg)
	require.Error(t, err)
	assert.Nil(t, recs)
}

// Test successful Azure recommendations
func TestScheduler_CollectAzureRecommendations_Success(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	globalCfg := &config.GlobalConfig{
		DefaultTerm:    3,
		DefaultPayment: "all-upfront",
	}

	// Return an enabled Azure account with managed_identity (ambient creds)
	azureAccounts := []config.CloudAccount{
		{
			ID:                  "az-1",
			Provider:            "azure",
			AzureAuthMode:       "managed_identity",
			AzureSubscriptionID: "sub-123",
			Enabled:             true,
		},
	}
	mockStore.On("ListCloudAccounts", ctx, mock.Anything).Return(azureAccounts, nil)

	// The managed_identity path will try DefaultAzureCredential which will
	// fail in tests, so we expect an error log but no crash.
	scheduler := &Scheduler{
		config: mockStore,
	}

	recs, _, err := scheduler.collectAzureRecommendations(ctx, globalCfg)
	require.NoError(t, err)
	// In test environment without Azure credentials, 0 recommendations is expected
	// (the error is logged and skipped). The test validates the per-account loop runs.
	_ = recs
}

// Test GCP recommendations with no accounts — should skip gracefully
func TestScheduler_CollectGCPRecommendations_NoAccounts(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	globalCfg := &config.GlobalConfig{
		DefaultTerm:    3,
		DefaultPayment: "all-upfront",
	}

	mockStore.On("ListCloudAccounts", ctx, mock.Anything).Return([]config.CloudAccount{}, nil)

	scheduler := &Scheduler{
		config: mockStore,
	}

	recs, _, err := scheduler.collectGCPRecommendations(ctx, globalCfg)
	require.NoError(t, err)
	assert.Len(t, recs, 0)
}

// Test CollectRecommendations with successful recommendations and email notification
func TestScheduler_CollectRecommendations_WithSuccessfulRecs(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockEmail := new(MockEmailSender)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockRecClient := new(MockRecommendationsClient)

	globalCfg := &config.GlobalConfig{
		EnabledProviders: []string{"aws"},
		DefaultTerm:      3,
		DefaultPayment:   "all-upfront",
	}

	recommendations := []common.Recommendation{
		{
			Provider:         common.ProviderAWS,
			Service:          common.ServiceEC2,
			Region:           "us-east-1",
			ResourceType:     "m5.large",
			Count:            5,
			Term:             "3yr",
			EstimatedSavings: 500.0,
		},
	}

	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", ctx).Return(mockRecClient, nil)
	mockRecClient.On("GetAllRecommendations", ctx).Return(recommendations, nil)
	mockEmail.On("SendNewRecommendationsNotification", ctx, mock.AnythingOfType("email.NotificationData")).Return(nil)

	scheduler := &Scheduler{
		config:          mockStore,
		email:           mockEmail,
		dashboardURL:    "https://dashboard.example.com",
		providerFactory: mockFactory,
	}

	result, err := scheduler.CollectRecommendations(ctx)
	require.NoError(t, err)

	assert.Equal(t, 1, result.Recommendations)
	assert.Equal(t, 500.0, result.TotalSavings)

	mockEmail.AssertCalled(t, "SendNewRecommendationsNotification", ctx, mock.AnythingOfType("email.NotificationData"))
}

// Test AWS recommendations fallback to GetRecommendations when GetAllRecommendations returns empty
func TestScheduler_CollectAWSRecommendations_FallbackToFiltered(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockRecClient := new(MockRecommendationsClient)

	globalCfg := &config.GlobalConfig{
		DefaultTerm:    3,
		DefaultPayment: "all-upfront",
	}

	filteredRecommendations := []common.Recommendation{
		{
			Provider:         common.ProviderAWS,
			Service:          common.ServiceEC2,
			Region:           "us-east-1",
			ResourceType:     "m5.large",
			Count:            5,
			Term:             "3yr",
			EstimatedSavings: 500.0,
		},
	}

	mockFactory.On("CreateAndValidateProvider", ctx, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", ctx).Return(mockRecClient, nil)
	mockRecClient.On("GetAllRecommendations", ctx).Return([]common.Recommendation{}, nil) // Empty
	mockRecClient.On("GetRecommendations", ctx, mock.AnythingOfType("common.RecommendationParams")).Return(filteredRecommendations, nil)

	scheduler := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	recs, _, err := scheduler.collectAWSRecommendations(ctx, globalCfg)
	require.NoError(t, err)
	assert.Len(t, recs, 1)
}
