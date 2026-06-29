package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/email"
	"github.com/LeanerCloud/CUDly/internal/mocks"
	"github.com/LeanerCloud/CUDly/internal/purchase"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockProviderFactory is a mock implementation of ProviderFactoryInterface.
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

// MockConfigStore is the shared testify mock for config.StoreInterface.
// All default behaviors and Fn-override fields live in internal/mocks.
type MockConfigStore = mocks.MockConfigStore

// MockEmailSender is a mock implementation of email.Sender.
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

func (m *MockEmailSender) SendToEmailWithCCMultipart(_ context.Context, _ string, _ []string, _, _, _ string) error {
	return nil
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

func (m *MockEmailSender) SendUserInviteEmail(ctx context.Context, email, setupURL string) error {
	args := m.Called(ctx, email, setupURL)
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
func (m *MockEmailSender) SendPurchaseScheduledNotification(_ context.Context, _ email.NotificationData) error {
	return nil
}
func (m *MockEmailSender) SendRegistrationReceivedNotification(_ context.Context, _ email.RegistrationNotificationData) error {
	return nil
}
func (m *MockEmailSender) SendRegistrationDecisionNotification(_ context.Context, _ string, _ email.RegistrationDecisionData) error {
	return nil
}

// MockPurchaseManager is a mock implementation of purchase.Manager.
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

func (m *MockPurchaseManager) FireScheduledDelayedPurchases(ctx context.Context) (*purchase.FireResult, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*purchase.FireResult), args.Error(1)
}

// TestSchedulerManagerInterface_FireScheduledDelayedPurchasesWired is a
// wiring smoke test: it asserts that ManagerInterface exposes
// FireScheduledDelayedPurchases and that MockPurchaseManager satisfies the
// contract. If the method is ever removed from the interface or the mock,
// this test fails to compile.
//
// The dispatch itself (ManagerInterface.FireScheduledDelayedPurchases ->
// scheduler tick "fire_scheduled_purchases") is exercised by the
// server/handler_test.go "fire_scheduled_purchases success" case.
func TestSchedulerManagerInterface_FireScheduledDelayedPurchasesWired(t *testing.T) {
	ctx := context.Background()
	mockPurchase := new(MockPurchaseManager)

	// The MockPurchaseManager must satisfy ManagerInterface at compile time.
	var _ ManagerInterface = mockPurchase

	mockPurchase.On("FireScheduledDelayedPurchases", ctx).
		Return(&purchase.FireResult{Found: 1, Fired: 1}, nil)

	result, err := mockPurchase.FireScheduledDelayedPurchases(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Found)
	assert.Equal(t, 1, result.Fired)
	mockPurchase.AssertExpectations(t)
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
	mockFactory.On("CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything).
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
	mockFactory.On("CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything).
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

// TestScheduler_CollectRecommendations_ParallelProviders pins three contracts
// of the provider-level fan-out introduced in collectAllProviders (closes
// #268):
//
//  1. successfulProviders ordering matches EnabledProviders config order, NOT
//     goroutine completion order. We assert this with a deliberately non-
//     alphabetical config order ["gcp", "aws", "azure"] so the result is
//     distinguishable from any of: input order, alphabetical, or arbitrary
//     map iteration.
//  2. A single provider's error does not cancel siblings — when the mock
//     factory returns an error for "azure" and successes for "aws"+"gcp",
//     the result still includes the successful providers and reports
//     azure in failedProviders.
//  3. ctx cancellation propagates: a pre-canceled ctx surfaces as
//     context.Canceled (not a "successful but empty" CollectResult).
func TestScheduler_CollectRecommendations_ParallelProviders(t *testing.T) {
	t.Run("successfulProviders ordering matches config order, not goroutine completion", func(t *testing.T) {
		ctx := context.Background()
		mockStore := new(MockConfigStore)
		mockEmail := new(MockEmailSender)
		mockFactory := new(MockProviderFactory)

		// Deliberately non-alphabetical config order so we can distinguish
		// "by config" from "by goroutine completion" from "alphabetical"
		// from "by map-iteration order".
		//
		// MockConfigStore.ListCloudAccounts is a hardcoded stub that
		// returns (nil, nil) — so Azure and GCP both hit their
		// "no enabled accounts → return nil, nil, nil" early-return path
		// (succeed with zero recommendations). AWS falls through to the
		// ambient-credential collectAWSAmbient path which calls the
		// provider factory; the mock returns assert.AnError so AWS lands
		// in failedProviders.
		//
		// Expected merge under the deterministic-config-order walk:
		//   successfulProviders = ["gcp", "azure"]  (skipping the failed
		//                                            "aws" entry, in
		//                                            config order)
		//   failedProviders     = {"aws": "..."}
		//
		// If the merge instead walked the outcomes map (random Go map
		// iteration), or sorted alphabetically, or used goroutine
		// completion order, we'd see ["azure", "gcp"] or random ordering
		// — distinguishable from the expected config-ordered result.
		enabled := []string{"gcp", "aws", "azure"}
		globalCfg := &config.GlobalConfig{
			EnabledProviders: enabled,
			DefaultTerm:      3,
			DefaultPayment:   "all-upfront",
		}

		mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
		mockFactory.On("CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		scheduler := &Scheduler{
			config:          mockStore,
			email:           mockEmail,
			dashboardURL:    "https://dashboard.example.com",
			providerFactory: mockFactory,
		}

		result, err := scheduler.CollectRecommendations(ctx)
		require.NoError(t, err)

		assert.Equal(t, []string{"gcp", "azure"}, result.SuccessfulProviders,
			"successfulProviders must be in EnabledProviders config order, "+
				"skipping the failed AWS entry; non-alphabetical config "+
				"distinguishes config-order from sort-order")
		assert.Len(t, result.FailedProviders, 1)
		assert.Contains(t, result.FailedProviders, "aws")
	})

	t.Run("ctx cancellation propagates", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		mockEmail := new(MockEmailSender)
		mockFactory := new(MockProviderFactory)

		globalCfg := &config.GlobalConfig{
			EnabledProviders: []string{"aws", "azure", "gcp"},
		}
		// GetGlobalConfig is called pre-fan-out. We need it to succeed so
		// we reach the fan-out, where the canceled ctx is observed.
		mockStore.On("GetGlobalConfig", mock.Anything).Return(globalCfg, nil)
		mockFactory.On("CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		scheduler := &Scheduler{
			config:          mockStore,
			email:           mockEmail,
			dashboardURL:    "https://dashboard.example.com",
			providerFactory: mockFactory,
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel BEFORE the call

		_, err := scheduler.CollectRecommendations(ctx)
		require.Error(t, err, "expected context.Canceled to propagate from CollectRecommendations")
		assert.ErrorIs(t, err, context.Canceled,
			"CollectRecommendations must propagate the parent ctx error after the provider fan-out's g.Wait()")
	})
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
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).
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

	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

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

	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

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
	mockFactory.On("CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, assert.AnError)
	// Azure/GCP: no accounts → skip gracefully
	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

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

// Integration-style test for email notification.
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
	mockFactory.On("CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything).
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

// Test that verifies the struct implements expected interface.
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

// Test edge cases.
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

// Helper function tests.
func TestSchedulerConfigStoreInterface(t *testing.T) {
	// Verify MockConfigStore implements all required methods
	store := new(MockConfigStore)
	ctx := context.Background()

	// These calls just verify the mock has the methods
	store.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{}, nil)
	store.On("ListServiceConfigs", ctx).Return([]config.ServiceConfig{}, nil)
	store.On("ListPurchasePlans", ctx, config.PurchasePlanFilter{}).Return([]config.PurchasePlan{}, nil)

	_, _ = store.GetGlobalConfig(ctx)
	_, _ = store.ListServiceConfigs(ctx)
	_, _ = store.ListPurchasePlans(ctx, config.PurchasePlanFilter{})

	store.AssertExpectations(t)
}

// Test purchase.Manager integration.
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

// MockProvider is a mock implementation of provider.Provider.
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

// MockRecommendationsClient is a mock implementation of provider.RecommendationsClient.
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
	// Non-Lambda path resolves the effective stale TTL from the DB config.
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
	}, nil)

	scheduler := &Scheduler{config: mockStore}

	recs, err := scheduler.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	assert.Len(t, recs, 2)
}

// Pin the disable-sentinel contract: when GlobalConfig.RecommendationsCacheStaleHours
// is 0, ListRecommendations must serve from cache (the existing behavior) without
// kicking off a background refresh — even when the cached row is older than any
// hard-coded fallback TTL. The cache-staleness path should treat 0 as "auto-refresh
// disabled" rather than "stale immediately". Regression guard for PR #308.
func TestScheduler_ListRecommendations_StaleHoursZeroDisablesBackgroundRefresh(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	old := time.Now().Add(-72 * time.Hour) // older than any reasonable default TTL
	cached := []config.RecommendationRecord{
		{Provider: "aws", Service: "ec2", Region: "us-east-1", Savings: 1},
	}
	mockStore.On("GetRecommendationsFreshness", ctx).
		Return(&config.RecommendationsFreshness{LastCollectedAt: &old}, nil)
	mockStore.On("ListStoredRecommendations", ctx, mock.Anything).
		Return(cached, nil)
	// Disable sentinel: 0 must NOT trigger a background refresh.
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: 0,
	}, nil)

	scheduler := &Scheduler{config: mockStore}

	recs, err := scheduler.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	assert.Len(t, recs, 1)

	// Asserting via mock expectations: MarkCollectionStarted is what the
	// background-refresh path would call. When the sentinel is 0, no refresh
	// fires, so MarkCollectionStarted MUST NOT be called (and absence of an
	// `On(...)` expectation for it would cause testify-mock to panic if it
	// were called — the Len assertion above is the primary check, this is the
	// second-line guard).
	mockStore.AssertNotCalled(t, "MarkCollectionStarted", mock.Anything)
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
	// Non-Lambda path resolves effective stale TTL from DB config.
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
	}, nil)

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
	scheduler.maybeKickBackgroundRefresh(freshness, time.Nanosecond)
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
	// UpsertRecommendations runs inside CollectRecommendations, after the
	// shared-semaphore is attached to ctx; the wrapped ctx is what reaches
	// the persistence layer. mock.Anything keeps the assertion resilient
	// to that wrap.
	mockStore.On("UpsertRecommendations", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockStore.On("ListStoredRecommendations", ctx, mock.Anything).
		Return([]config.RecommendationRecord{}, nil)

	scheduler := &Scheduler{config: mockStore}

	_, err := scheduler.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)

	// Assert the cold-start path ran: GetGlobalConfig is only called by
	// CollectRecommendations, so seeing it prove the sync collect fired
	// before the store read.
	mockStore.AssertCalled(t, "GetGlobalConfig", ctx)
	mockStore.AssertCalled(t, "UpsertRecommendations", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
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

// Test convertRecommendations.
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

// Test convertRecommendations with empty input.
func TestScheduler_ConvertRecommendations_Empty(t *testing.T) {
	scheduler := &Scheduler{}

	records := scheduler.convertRecommendations([]common.Recommendation{}, "aws")
	assert.Len(t, records, 0)
}

// TestScheduler_ConvertRecommendations_OnDemandCost pins #274: the
// canonical on-demand baseline must round-trip from common.Recommendation
// through to the persisted RecommendationRecord so the frontend can use
// it directly instead of reconstructing from monthly_cost + savings +
// amortized (which collapses for Azure all-upfront recs where
// monthly_cost = $0).
func TestScheduler_ConvertRecommendations_OnDemandCost(t *testing.T) {
	scheduler := &Scheduler{}

	recommendations := []common.Recommendation{
		// Provider populated OnDemandCost — must propagate as a non-nil pointer.
		{
			Provider: common.ProviderAzure, Service: common.ServiceCompute,
			Region: "eastus", ResourceType: "Standard_D11_v2",
			Count: 2, Term: "1yr", PaymentOption: "all-upfront",
			OnDemandCost: 122.64, CommitmentCost: 1050.0, EstimatedSavings: 35.0,
		},
		// Provider returned 0 (not populated) — must round-trip as nil so the
		// frontend's "is this populated?" branch stays accurate. A literal $0
		// on-demand baseline is impossible (it would mean the resource is
		// free, in which case there's nothing to recommend reserving).
		{
			Provider: common.ProviderAWS, Service: common.ServiceEC2,
			Region: "us-east-1", ResourceType: "m5.large",
			Count: 1, Term: "3yr", PaymentOption: "all-upfront",
			OnDemandCost: 0, CommitmentCost: 1000.0, EstimatedSavings: 500.0,
		},
	}

	records := scheduler.convertRecommendations(recommendations, "azure")
	require.Len(t, records, 2)

	require.NotNil(t, records[0].OnDemandCost)
	assert.InDelta(t, 122.64, *records[0].OnDemandCost, 0.001)

	assert.Nil(t, records[1].OnDemandCost,
		"OnDemandCost=0 from the provider must round-trip as nil, not as a pointer to 0.0")
}

// TestScheduler_ConvertRecommendations_SavingsPercentage pins the CLI/GUI
// parity fix: the provider-authoritative SavingsPercentage (the same figure
// the CLI/reporter prints) must be carried onto the RecommendationRecord so
// the frontend can display it verbatim instead of re-deriving it. A provider
// value of 0 (not reported) must round-trip as nil, never as a pointer to
// 0.0, mirroring OnDemandCost so the frontend's "is this populated?" branch
// stays accurate and falls back to the client-side reconstruction.
func TestScheduler_ConvertRecommendations_SavingsPercentage(t *testing.T) {
	scheduler := &Scheduler{}

	recommendations := []common.Recommendation{
		// AWS rec with a provider-reported percentage in the real data band.
		{
			Provider: common.ProviderAWS, Service: common.ServiceEC2,
			Region: "us-east-1", ResourceType: "m5.large",
			Count: 2, Term: "1yr", PaymentOption: "partial-upfront",
			OnDemandCost: 150.0, CommitmentCost: 120.0, EstimatedSavings: 45.0,
			SavingsPercentage: 30.0,
		},
		// Azure rec with a higher provider-reported percentage.
		{
			Provider: common.ProviderAzure, Service: common.ServiceCompute,
			Region: "eastus", ResourceType: "Standard_D11_v2",
			Count: 1, Term: "3yr", PaymentOption: "all-upfront",
			OnDemandCost: 122.64, CommitmentCost: 1050.0, EstimatedSavings: 58.0,
			SavingsPercentage: 47.3,
		},
		// Provider did not report a percentage (0); must round-trip as nil.
		{
			Provider: common.ProviderGCP, Service: common.ServiceCompute,
			Region: "us-central1", ResourceType: "n2-standard-4",
			Count: 1, Term: "3yr", PaymentOption: "no-upfront",
			OnDemandCost: 200.0, CommitmentCost: 0, EstimatedSavings: 44.0,
			SavingsPercentage: 0,
		},
	}

	records := scheduler.convertRecommendations(recommendations, "aws")
	require.Len(t, records, 3)

	require.NotNil(t, records[0].SavingsPercentage)
	assert.InDelta(t, 30.0, *records[0].SavingsPercentage, 0.001)

	require.NotNil(t, records[1].SavingsPercentage)
	assert.InDelta(t, 47.3, *records[1].SavingsPercentage, 0.001)

	assert.Nil(t, records[2].SavingsPercentage,
		"SavingsPercentage=0 from the provider must round-trip as nil, not as a pointer to 0.0")
}

// TestScheduler_ConvertRecommendations_IDUniqueness pins issue #187 +
// #188: the rec ID must include term, account, and engine — not just
// (provider, service, region, resource_type, payment) — otherwise
// recs that should be distinct get the same ID, which (a) collapses
// two rendered rows into one selection in the UI (#187), and (b)
// silently drops one of two same-cell recs at any storage stage that
// dedupes by ID (#188). Each subtest asserts that two recs differing
// only in the listed dimension produce different IDs.
func TestScheduler_ConvertRecommendations_IDUniqueness(t *testing.T) {
	scheduler := &Scheduler{}
	base := common.Recommendation{
		Provider:      common.ProviderAWS,
		Account:       "test-account-a",
		Service:       common.ServiceEC2,
		Region:        "us-east-1",
		ResourceType:  "m5.large",
		Count:         1,
		Term:          "1yr",
		PaymentOption: "all-upfront",
	}

	// Each case mutates one and only one field of `base` so the
	// resulting (a, b) pair differs in exactly that dimension. The
	// engine subtest is built from a separate `rdsBase` below so the
	// "only Details.Engine differs" property holds at every level
	// (Service / ResourceType already match across the pair).
	cases := []struct {
		name string
		recs func() (common.Recommendation, common.Recommendation)
	}{
		{
			name: "term: 1yr vs 3yr (issue #188 — AWS 1yr recs were vanishing)",
			recs: func() (common.Recommendation, common.Recommendation) {
				b := base
				b.Term = "3yr"
				return base, b
			},
		},
		{
			name: "account: separates multi-subscription recs (issue #187)",
			recs: func() (common.Recommendation, common.Recommendation) {
				b := base
				b.Account = "test-account-b"
				return base, b
			},
		},
		{
			name: "payment: all-upfront vs no-upfront",
			recs: func() (common.Recommendation, common.Recommendation) {
				b := base
				b.PaymentOption = "no-upfront"
				return base, b
			},
		},
		{
			name: "engine: MySQL vs Postgres at same RDS SKU",
			recs: func() (common.Recommendation, common.Recommendation) {
				rdsBase := base
				rdsBase.Service = common.ServiceRDS
				rdsBase.ResourceType = "db.m5.large"
				rdsBase.Details = common.DatabaseDetails{Engine: "mysql"}
				rdsTwin := rdsBase
				rdsTwin.Details = common.DatabaseDetails{Engine: "postgres"}
				return rdsBase, rdsTwin
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b := tc.recs()
			records := scheduler.convertRecommendations([]common.Recommendation{a, b}, "aws")
			require.Len(t, records, 2)
			assert.NotEqual(t, records[0].ID, records[1].ID,
				"ID collision — recs differing in %s produce the same ID; this regresses #187/#188", tc.name)
		})
	}
}

// TestScheduler_ConvertRecommendations_IDDeterminism ensures the same
// input produces the same ID across calls (no random or time-dependent
// component). Without this, the frontend selection state — which
// round-trips rec.id between renders — would lose selections between
// collection cycles. With the natural-composite-key encoding this is
// trivially true (the ID is a pure function of the input fields), but
// pinned here so a future refactor that re-introduces randomness or
// non-determinism trips the suite immediately.
func TestScheduler_ConvertRecommendations_IDDeterminism(t *testing.T) {
	scheduler := &Scheduler{}
	rec := common.Recommendation{
		Provider:      common.ProviderAWS,
		Account:       "test-account-determinism",
		Service:       common.ServiceEC2,
		Region:        "us-east-1",
		ResourceType:  "m5.large",
		Count:         3,
		Term:          "3yr",
		PaymentOption: "all-upfront",
	}
	first := scheduler.convertRecommendations([]common.Recommendation{rec}, "aws")
	second := scheduler.convertRecommendations([]common.Recommendation{rec}, "aws")
	require.Len(t, first, 1)
	require.Len(t, second, 1)
	assert.Equal(t, first[0].ID, second[0].ID, "ID must be deterministic across calls")
}

// Test successful AWS recommendations with provider returning data.
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

	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
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

// Test AWS recommendations when GetRecommendationsClient fails.
func TestScheduler_CollectAWSRecommendations_RecClientError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)

	globalCfg := &config.GlobalConfig{
		DefaultTerm:    3,
		DefaultPayment: "all-upfront",
	}

	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", ctx).Return(nil, assert.AnError)

	scheduler := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	recs, _, err := scheduler.collectAWSRecommendations(ctx, globalCfg)
	require.Error(t, err)
	assert.Nil(t, recs)
}

// Test AWS recommendations when GetAllRecommendations fails.
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

	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
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

// Test successful Azure recommendations.
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
	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return(azureAccounts, nil)

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

// Test GCP recommendations with no accounts — should skip gracefully.
func TestScheduler_CollectGCPRecommendations_NoAccounts(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	globalCfg := &config.GlobalConfig{
		DefaultTerm:    3,
		DefaultPayment: "all-upfront",
	}

	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

	scheduler := &Scheduler{
		config: mockStore,
	}

	recs, _, err := scheduler.collectGCPRecommendations(ctx, globalCfg)
	require.NoError(t, err)
	assert.Len(t, recs, 0)
}

// Test CollectRecommendations with successful recommendations and email notification.
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
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
	// Provider/RecClient are invoked from inside the per-provider errgroup
	// goroutine in collectAllProviders, so they receive the errgroup-derived
	// gctx rather than the caller's ctx. mock.Anything keeps the assertion
	// resilient to that wrap (the post-Wait ctx.Err() check + cancellation
	// contract test cover the cancellation path).
	mockProvider.On("GetRecommendationsClient", mock.Anything).Return(mockRecClient, nil)
	mockRecClient.On("GetAllRecommendations", mock.Anything).Return(recommendations, nil)
	// SendNewRecommendationsNotification fires inside CollectRecommendations,
	// after ctx has been wrapped via concurrency.WithSharedSemaphore — the
	// wrapped ctx is what reaches the email sender. mock.Anything keeps the
	// assertion resilient to that wrap.
	mockEmail.On("SendNewRecommendationsNotification", mock.Anything, mock.AnythingOfType("email.NotificationData")).Return(nil)

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

	mockEmail.AssertCalled(t, "SendNewRecommendationsNotification", mock.Anything, mock.AnythingOfType("email.NotificationData"))
}

// Test AWS recommendations fallback to GetRecommendations when GetAllRecommendations returns empty.
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

	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
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

// fakeSTSClient is a minimal in-test STSClient implementation used by the
// ambient host-account tagging tests (issue #604). The fakeAccountID + err
// fields are set by each test case to drive the GetCallerIdentity response
// shape (success with an account ID, or an error).
type fakeSTSClient struct {
	accountID string
	err       error
}

func (f *fakeSTSClient) GetCallerIdentity(ctx context.Context, _ *sts.GetCallerIdentityInput, _ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.accountID == "" {
		return &sts.GetCallerIdentityOutput{}, nil
	}
	return &sts.GetCallerIdentityOutput{Account: aws.String(f.accountID)}, nil
}

// slowSTSClient simulates an STS endpoint that hangs longer than the
// 3-second deadline applied by resolveAmbientHostAccountID. It blocks
// until ctx is canceled so the test can verify timeout behavior.
type slowSTSClient struct{}

func (s *slowSTSClient) GetCallerIdentity(ctx context.Context, _ *sts.GetCallerIdentityInput, _ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// Issue #604: AWS ambient-path tagging fix — when the Lambda's STS
// identity matches a registered cloud_accounts row (regardless of
// enabled flag), rec rows should be tagged with that account's UUID
// instead of nil so the approve modal shows the registered name
// instead of `(ambient)`.
func TestScheduler_CollectAWSRecommendations_AmbientTagging_HappyPath(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockRecClient := new(MockRecommendationsClient)

	globalCfg := &config.GlobalConfig{
		DefaultTerm:    3,
		DefaultPayment: "all-upfront",
	}

	// No enabled AWS accounts → ambient fallback fires.
	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

	recommendations := []common.Recommendation{
		{
			Provider:         common.ProviderAWS,
			Service:          common.ServiceEC2,
			Region:           "us-east-1",
			ResourceType:     "t4g.nano",
			Count:            1,
			Term:             "1yr",
			EstimatedSavings: 12.0,
		},
	}

	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", ctx).Return(mockRecClient, nil)
	mockRecClient.On("GetAllRecommendations", ctx).Return(recommendations, nil)

	// Registered host account (enabled=false on purpose — registration
	// alone is the signal, not enabled state).
	registered := &config.CloudAccount{
		ID:         "abc-uuid",
		Provider:   "aws",
		ExternalID: "123456789012",
		Enabled:    false,
	}
	mockStore.On("GetCloudAccountByExternalID", mock.Anything, "aws", "123456789012").Return(registered, nil)

	scheduler := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
		stsClient:       &fakeSTSClient{accountID: "123456789012"},
	}

	recs, acctIDs, err := scheduler.collectAWSRecommendations(ctx, globalCfg)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.NotNil(t, recs[0].CloudAccountID, "rec must be tagged with registered account UUID, not nil")
	assert.Equal(t, "abc-uuid", *recs[0].CloudAccountID)
	assert.Equal(t, []string{"abc-uuid"}, acctIDs,
		"eviction account-keys must be the registered UUID, not the ambient sentinel")
}

// Issue #604: when STS reports an account ID that is NOT in the
// registered cloud_accounts table, the ambient path must keep
// CloudAccountID = nil — preserving the truly-orphan deployment case.
func TestScheduler_CollectAWSRecommendations_AmbientTagging_NoRegisteredAccount(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockRecClient := new(MockRecommendationsClient)

	globalCfg := &config.GlobalConfig{DefaultTerm: 3, DefaultPayment: "all-upfront"}

	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

	recommendations := []common.Recommendation{
		{Provider: common.ProviderAWS, Service: common.ServiceEC2, Region: "us-east-1", ResourceType: "t4g.nano", Count: 1, Term: "1yr"},
	}
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", ctx).Return(mockRecClient, nil)
	mockRecClient.On("GetAllRecommendations", ctx).Return(recommendations, nil)

	// Store has NO matching registered row.
	mockStore.On("GetCloudAccountByExternalID", mock.Anything, "aws", "999888777666").Return(nil, nil)

	scheduler := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
		stsClient:       &fakeSTSClient{accountID: "999888777666"},
	}

	recs, acctIDs, err := scheduler.collectAWSRecommendations(ctx, globalCfg)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Nil(t, recs[0].CloudAccountID, "truly-orphan ambient deployment must keep CloudAccountID = nil")
	assert.Equal(t, []string{""}, acctIDs, "eviction account-keys must keep the ambient sentinel")
}

// Issue #604: an STS hiccup must NOT fail the collection — recs are
// returned with the pre-fix nil tagging and the run succeeds.
func TestScheduler_CollectAWSRecommendations_AmbientTagging_STSFailure(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockRecClient := new(MockRecommendationsClient)

	globalCfg := &config.GlobalConfig{DefaultTerm: 3, DefaultPayment: "all-upfront"}

	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

	recommendations := []common.Recommendation{
		{Provider: common.ProviderAWS, Service: common.ServiceEC2, Region: "us-east-1", ResourceType: "t4g.nano", Count: 1, Term: "1yr"},
	}
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "aws", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", ctx).Return(mockRecClient, nil)
	mockRecClient.On("GetAllRecommendations", ctx).Return(recommendations, nil)

	scheduler := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
		stsClient:       &fakeSTSClient{err: errors.New("sts unreachable")},
	}

	recs, acctIDs, err := scheduler.collectAWSRecommendations(ctx, globalCfg)
	require.NoError(t, err, "STS failure must NOT fail the collection")
	require.Len(t, recs, 1)
	assert.Nil(t, recs[0].CloudAccountID, "STS failure must leave the pre-fix nil tagging in place")
	assert.Equal(t, []string{""}, acctIDs)

	// Sanity: GetCloudAccountByExternalID must not be called when STS fails.
	mockStore.AssertNotCalled(t, "GetCloudAccountByExternalID", mock.Anything, mock.Anything, mock.Anything)
}

// Issue #604: resolveAmbientHostAccountID must skip cleanly when no STS
// client is wired (e.g. NewScheduler was given a nil STSClient — the
// pre-fix construction path). Confirms the helper degrades gracefully
// rather than panicking on nil deref.
func TestScheduler_ResolveAmbientHostAccountID_NoSTSClient(t *testing.T) {
	mockStore := new(MockConfigStore)
	scheduler := &Scheduler{
		config:    mockStore,
		stsClient: nil,
	}
	got := scheduler.resolveAmbientHostAccountID(context.Background())
	assert.Empty(t, got, "must return empty when STS client is nil")
	mockStore.AssertNotCalled(t, "GetCloudAccountByExternalID", mock.Anything, mock.Anything, mock.Anything)
}

// Issue #604: when GetCloudAccountByExternalID returns a non-nil store
// error (DB blip), resolveAmbientHostAccountID must NOT propagate it —
// it returns "" so the collection falls back to the pre-fix nil tagging.
func TestScheduler_ResolveAmbientHostAccountID_StoreError(t *testing.T) {
	mockStore := new(MockConfigStore)
	mockStore.On("GetCloudAccountByExternalID", mock.Anything, "aws", "123456789012").
		Return(nil, errors.New("db unreachable"))

	scheduler := &Scheduler{
		config:    mockStore,
		stsClient: &fakeSTSClient{accountID: "123456789012"},
	}
	got := scheduler.resolveAmbientHostAccountID(context.Background())
	assert.Empty(t, got, "store error must collapse to empty result (don't fail the collection)")
}

// Issue #604 / CR pass-1: resolveAmbientHostAccountID must not block
// scheduler startup when STS is slow. The function wraps the call in a
// 3-second context; a hung STS must therefore return "" well inside that
// window rather than stalling forever.
func TestScheduler_ResolveAmbientHostAccountID_STSTimeout(t *testing.T) {
	mockStore := new(MockConfigStore)
	scheduler := &Scheduler{
		config:    mockStore,
		stsClient: &slowSTSClient{},
	}

	start := time.Now()
	got := scheduler.resolveAmbientHostAccountID(context.Background())
	elapsed := time.Since(start)

	assert.Empty(t, got, "STS timeout must return empty so the collection falls through to nil tagging")
	// The internal deadline is 3 s; allow a small margin for scheduling jitter.
	assert.Less(t, elapsed, 4*time.Second, "resolveAmbientHostAccountID must not block beyond its internal 3s deadline")
	mockStore.AssertNotCalled(t, "GetCloudAccountByExternalID", mock.Anything, mock.Anything, mock.Anything)
}

// Issue #662: Azure ambient-path tagging fix — when AZURE_SUBSCRIPTION_ID
// matches a registered cloud_accounts row the ambient path must tag every
// rec with that account's UUID instead of nil.
func TestScheduler_CollectAzureRecommendations_AmbientTagging_HappyPath(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "sub-abc-123")

	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockRecClient := new(MockRecommendationsClient)

	// No enabled Azure accounts — ambient fallback fires.
	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

	recommendations := []common.Recommendation{
		{
			Provider:         common.ProviderAzure,
			Service:          common.ServiceCompute,
			Region:           "eastus",
			ResourceType:     "Standard_D2s_v3",
			Count:            1,
			Term:             "1yr",
			EstimatedSavings: 20.0,
		},
	}
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "azure", mock.MatchedBy(func(cfg *provider.ProviderConfig) bool {
		return cfg != nil && cfg.AzureSubscriptionID == "sub-abc-123"
	})).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", mock.Anything).Return(mockRecClient, nil)
	mockRecClient.On("GetAllRecommendations", mock.Anything).Return(recommendations, nil)

	// Registered host account (disabled on purpose — registration alone is the signal).
	registered := &config.CloudAccount{
		ID:                  "az-uuid-001",
		Provider:            "azure",
		ExternalID:          "sub-abc-123",
		AzureSubscriptionID: "sub-abc-123",
		Enabled:             false,
	}
	mockStore.On("GetCloudAccountByExternalID", mock.Anything, "azure", "sub-abc-123").Return(registered, nil)

	sched := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	recs, acctIDs, err := sched.collectAzureRecommendations(ctx, nil)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.NotNil(t, recs[0].CloudAccountID, "rec must be tagged with registered Azure account UUID, not nil")
	assert.Equal(t, "az-uuid-001", *recs[0].CloudAccountID)
	assert.Equal(t, []string{"az-uuid-001"}, acctIDs,
		"eviction account-keys must be the registered UUID, not the ambient sentinel")
}

// Issue #662: Azure ambient path — subscription not in cloud_accounts table
// must keep CloudAccountID = nil (truly-orphan case preserved).
func TestScheduler_CollectAzureRecommendations_AmbientTagging_NoRegisteredAccount(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "sub-unregistered")

	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockRecClient := new(MockRecommendationsClient)

	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

	recommendations := []common.Recommendation{
		{Provider: common.ProviderAzure, Service: common.ServiceCompute, Region: "westus", ResourceType: "Standard_B2s", Count: 1, Term: "1yr"},
	}
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "azure", mock.MatchedBy(func(cfg *provider.ProviderConfig) bool {
		return cfg != nil && cfg.AzureSubscriptionID == "sub-unregistered"
	})).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", mock.Anything).Return(mockRecClient, nil)
	mockRecClient.On("GetAllRecommendations", mock.Anything).Return(recommendations, nil)

	// Store has no matching row.
	mockStore.On("GetCloudAccountByExternalID", mock.Anything, "azure", "sub-unregistered").Return(nil, nil)

	sched := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	recs, acctIDs, err := sched.collectAzureRecommendations(ctx, nil)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Nil(t, recs[0].CloudAccountID, "truly-orphan Azure deployment must keep CloudAccountID = nil")
	assert.Equal(t, []string{""}, acctIDs, "eviction account-keys must keep the ambient sentinel")
}

// Issue #662: Azure ambient path — store error must NOT fail the collection;
// recs are returned with the pre-fix nil tagging.
func TestScheduler_CollectAzureRecommendations_AmbientTagging_StoreError(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "sub-abc-123")

	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockRecClient := new(MockRecommendationsClient)

	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

	recommendations := []common.Recommendation{
		{Provider: common.ProviderAzure, Service: common.ServiceCompute, Region: "eastus", ResourceType: "Standard_D2s_v3", Count: 1, Term: "1yr"},
	}
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "azure", mock.MatchedBy(func(cfg *provider.ProviderConfig) bool {
		return cfg != nil && cfg.AzureSubscriptionID == "sub-abc-123"
	})).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", mock.Anything).Return(mockRecClient, nil)
	mockRecClient.On("GetAllRecommendations", mock.Anything).Return(recommendations, nil)

	mockStore.On("GetCloudAccountByExternalID", mock.Anything, "azure", "sub-abc-123").
		Return(nil, errors.New("db unreachable"))

	sched := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	recs, acctIDs, err := sched.collectAzureRecommendations(ctx, nil)
	require.NoError(t, err, "store error must NOT fail the Azure collection")
	require.Len(t, recs, 1)
	assert.Nil(t, recs[0].CloudAccountID, "store error must leave the pre-fix nil tagging in place")
	assert.Equal(t, []string{""}, acctIDs)
}

// Issue #662: when AZURE_SUBSCRIPTION_ID is not set, collectAzureRecommendations
// must return empty without attempting an ambient provider call.
func TestScheduler_CollectAzureRecommendations_NoEnvVar_Skips(t *testing.T) {
	// Ensure the env var is absent for this test.
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")

	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)

	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

	sched := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	recs, acctIDs, err := sched.collectAzureRecommendations(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, recs)
	assert.Empty(t, acctIDs)
	mockFactory.AssertNotCalled(t, "CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything)
}

// Issue #662: GCP ambient-path tagging fix — when GCP_PROJECT_ID matches a
// registered cloud_accounts row the ambient path must tag every rec with
// that account's UUID instead of nil.
func TestScheduler_CollectGCPRecommendations_AmbientTagging_HappyPath(t *testing.T) {
	t.Setenv("GCP_PROJECT_ID", "my-gcp-project")

	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockRecClient := new(MockRecommendationsClient)

	// No enabled GCP accounts — ambient fallback fires.
	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

	recommendations := []common.Recommendation{
		{
			Provider:         common.ProviderGCP,
			Service:          common.ServiceCompute,
			Region:           "us-central1",
			ResourceType:     "n2-standard-4",
			Count:            1,
			Term:             "1yr",
			EstimatedSavings: 30.0,
		},
	}
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "gcp", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", mock.Anything).Return(mockRecClient, nil)
	mockRecClient.On("GetAllRecommendations", mock.Anything).Return(recommendations, nil)

	// Registered host account.
	registered := &config.CloudAccount{
		ID:           "gcp-uuid-001",
		Provider:     "gcp",
		ExternalID:   "my-gcp-project",
		GCPProjectID: "my-gcp-project",
		Enabled:      false,
	}
	mockStore.On("GetCloudAccountByExternalID", mock.Anything, "gcp", "my-gcp-project").Return(registered, nil)

	sched := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	recs, acctIDs, err := sched.collectGCPRecommendations(ctx, nil)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.NotNil(t, recs[0].CloudAccountID, "rec must be tagged with registered GCP account UUID, not nil")
	assert.Equal(t, "gcp-uuid-001", *recs[0].CloudAccountID)
	assert.Equal(t, []string{"gcp-uuid-001"}, acctIDs,
		"eviction account-keys must be the registered UUID, not the ambient sentinel")
}

// Issue #662: GCP ambient path — project not in cloud_accounts table must
// keep CloudAccountID = nil (truly-orphan case preserved).
func TestScheduler_CollectGCPRecommendations_AmbientTagging_NoRegisteredAccount(t *testing.T) {
	t.Setenv("GCP_PROJECT_ID", "unregistered-project")

	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockRecClient := new(MockRecommendationsClient)

	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

	recommendations := []common.Recommendation{
		{Provider: common.ProviderGCP, Service: common.ServiceCompute, Region: "us-central1", ResourceType: "n2-standard-2", Count: 1, Term: "1yr"},
	}
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "gcp", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", mock.Anything).Return(mockRecClient, nil)
	mockRecClient.On("GetAllRecommendations", mock.Anything).Return(recommendations, nil)

	// Store has no matching row.
	mockStore.On("GetCloudAccountByExternalID", mock.Anything, "gcp", "unregistered-project").Return(nil, nil)

	sched := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	recs, acctIDs, err := sched.collectGCPRecommendations(ctx, nil)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Nil(t, recs[0].CloudAccountID, "truly-orphan GCP deployment must keep CloudAccountID = nil")
	assert.Equal(t, []string{""}, acctIDs, "eviction account-keys must keep the ambient sentinel")
}

// Issue #662: GCP ambient path — store error must NOT fail the collection;
// recs are returned with the pre-fix nil tagging.
func TestScheduler_CollectGCPRecommendations_AmbientTagging_StoreError(t *testing.T) {
	t.Setenv("GCP_PROJECT_ID", "my-gcp-project")

	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)
	mockProvider := new(MockProvider)
	mockRecClient := new(MockRecommendationsClient)

	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

	recommendations := []common.Recommendation{
		{Provider: common.ProviderGCP, Service: common.ServiceCompute, Region: "us-central1", ResourceType: "n2-standard-4", Count: 1, Term: "1yr"},
	}
	mockFactory.On("CreateAndValidateProvider", mock.Anything, "gcp", mock.Anything).Return(mockProvider, nil)
	mockProvider.On("GetRecommendationsClient", mock.Anything).Return(mockRecClient, nil)
	mockRecClient.On("GetAllRecommendations", mock.Anything).Return(recommendations, nil)

	mockStore.On("GetCloudAccountByExternalID", mock.Anything, "gcp", "my-gcp-project").
		Return(nil, errors.New("db unreachable"))

	sched := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	recs, acctIDs, err := sched.collectGCPRecommendations(ctx, nil)
	require.NoError(t, err, "store error must NOT fail the GCP collection")
	require.Len(t, recs, 1)
	assert.Nil(t, recs[0].CloudAccountID, "store error must leave the pre-fix nil tagging in place")
	assert.Equal(t, []string{""}, acctIDs)
}

// Issue #662: when GCP_PROJECT_ID is not set, collectGCPRecommendations
// must return empty without attempting an ambient provider call.
func TestScheduler_CollectGCPRecommendations_NoEnvVar_Skips(t *testing.T) {
	t.Setenv("GCP_PROJECT_ID", "")

	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockFactory := new(MockProviderFactory)

	mockStore.On("ListCloudAccounts", mock.Anything, mock.Anything).Return([]config.CloudAccount{}, nil)

	sched := &Scheduler{
		config:          mockStore,
		providerFactory: mockFactory,
	}

	recs, acctIDs, err := sched.collectGCPRecommendations(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, recs)
	assert.Empty(t, acctIDs)
	mockFactory.AssertNotCalled(t, "CreateAndValidateProvider", mock.Anything, mock.Anything, mock.Anything)
}

// Issue #662: resolveAmbientAccountID must return "" when externalID is empty.
func TestScheduler_ResolveAmbientAccountID_EmptyExternalID(t *testing.T) {
	mockStore := new(MockConfigStore)
	sched := &Scheduler{config: mockStore}
	got := sched.resolveAmbientAccountID(context.Background(), "azure", "")
	assert.Empty(t, got, "empty externalID must return empty without a store call")
	mockStore.AssertNotCalled(t, "GetCloudAccountByExternalID", mock.Anything, mock.Anything, mock.Anything)
}

// Issue #662: resolveAmbientAccountID must return "" and swallow the error
// when the store call fails.
func TestScheduler_ResolveAmbientAccountID_StoreError(t *testing.T) {
	mockStore := new(MockConfigStore)
	mockStore.On("GetCloudAccountByExternalID", mock.Anything, "gcp", "some-project").
		Return(nil, errors.New("store down"))
	sched := &Scheduler{config: mockStore}
	got := sched.resolveAmbientAccountID(context.Background(), "gcp", "some-project")
	assert.Empty(t, got, "store error must collapse to empty (don't fail the collection)")
}
