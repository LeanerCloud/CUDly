package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockAnalyticsStore implements AnalyticsStore for testing
type mockAnalyticsStore struct {
	saveSnapshotFunc             func(ctx context.Context, snapshot *SavingsSnapshot) error
	bulkInsertSnapshotsFunc      func(ctx context.Context, snapshots []SavingsSnapshot) error
	querySavingsFunc             func(ctx context.Context, req QueryRequest) ([]SavingsSnapshot, error)
	queryMonthlyTotalsFunc       func(ctx context.Context, accountID string, months int) ([]MonthlySummary, error)
	queryByProviderFunc          func(ctx context.Context, accountID string, startDate, endDate time.Time) ([]ProviderBreakdown, error)
	queryByServiceFunc           func(ctx context.Context, accountID string, provider string, startDate, endDate time.Time) ([]ServiceBreakdown, error)
	createPartitionFunc          func(ctx context.Context, forMonth time.Time) error
	dropOldPartitionsFunc        func(ctx context.Context, retentionMonths int) error
	createPartitionsForRangeFunc func(ctx context.Context, startDate, endDate time.Time) error
	refreshMaterializedViewsFunc func(ctx context.Context) error
	closeFunc                    func() error

	savedSnapshots []SavingsSnapshot
}

func (m *mockAnalyticsStore) SaveSnapshot(ctx context.Context, snapshot *SavingsSnapshot) error {
	if m.saveSnapshotFunc != nil {
		return m.saveSnapshotFunc(ctx, snapshot)
	}
	m.savedSnapshots = append(m.savedSnapshots, *snapshot)
	return nil
}

func (m *mockAnalyticsStore) BulkInsertSnapshots(ctx context.Context, snapshots []SavingsSnapshot) error {
	if m.bulkInsertSnapshotsFunc != nil {
		return m.bulkInsertSnapshotsFunc(ctx, snapshots)
	}
	m.savedSnapshots = append(m.savedSnapshots, snapshots...)
	return nil
}

func (m *mockAnalyticsStore) QuerySavings(ctx context.Context, req QueryRequest) ([]SavingsSnapshot, error) {
	if m.querySavingsFunc != nil {
		return m.querySavingsFunc(ctx, req)
	}
	return nil, nil
}

func (m *mockAnalyticsStore) QueryMonthlyTotals(ctx context.Context, accountID string, months int) ([]MonthlySummary, error) {
	if m.queryMonthlyTotalsFunc != nil {
		return m.queryMonthlyTotalsFunc(ctx, accountID, months)
	}
	return nil, nil
}

func (m *mockAnalyticsStore) QueryByProvider(ctx context.Context, accountID string, startDate, endDate time.Time) ([]ProviderBreakdown, error) {
	if m.queryByProviderFunc != nil {
		return m.queryByProviderFunc(ctx, accountID, startDate, endDate)
	}
	return nil, nil
}

func (m *mockAnalyticsStore) QueryByService(ctx context.Context, accountID string, provider string, startDate, endDate time.Time) ([]ServiceBreakdown, error) {
	if m.queryByServiceFunc != nil {
		return m.queryByServiceFunc(ctx, accountID, provider, startDate, endDate)
	}
	return nil, nil
}

func (m *mockAnalyticsStore) CreatePartition(ctx context.Context, forMonth time.Time) error {
	if m.createPartitionFunc != nil {
		return m.createPartitionFunc(ctx, forMonth)
	}
	return nil
}

func (m *mockAnalyticsStore) DropOldPartitions(ctx context.Context, retentionMonths int) error {
	if m.dropOldPartitionsFunc != nil {
		return m.dropOldPartitionsFunc(ctx, retentionMonths)
	}
	return nil
}

func (m *mockAnalyticsStore) CreatePartitionsForRange(ctx context.Context, startDate, endDate time.Time) error {
	if m.createPartitionsForRangeFunc != nil {
		return m.createPartitionsForRangeFunc(ctx, startDate, endDate)
	}
	return nil
}

func (m *mockAnalyticsStore) RefreshMaterializedViews(ctx context.Context) error {
	if m.refreshMaterializedViewsFunc != nil {
		return m.refreshMaterializedViewsFunc(ctx)
	}
	return nil
}

func (m *mockAnalyticsStore) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

// mockConfigStore implements config.StoreInterface for testing
type mockConfigStore struct {
	getPurchaseHistoryFunc func(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error)
}

func (m *mockConfigStore) GetGlobalConfig(ctx context.Context) (*config.GlobalConfig, error) {
	return nil, nil
}

func (m *mockConfigStore) SaveGlobalConfig(ctx context.Context, cfg *config.GlobalConfig) error {
	return nil
}

func (m *mockConfigStore) GetServiceConfig(ctx context.Context, provider, service string) (*config.ServiceConfig, error) {
	return nil, nil
}

func (m *mockConfigStore) SaveServiceConfig(ctx context.Context, cfg *config.ServiceConfig) error {
	return nil
}

func (m *mockConfigStore) ListServiceConfigs(ctx context.Context) ([]config.ServiceConfig, error) {
	return nil, nil
}

func (m *mockConfigStore) CreatePurchasePlan(ctx context.Context, plan *config.PurchasePlan) error {
	return nil
}

func (m *mockConfigStore) GetPurchasePlan(ctx context.Context, planID string) (*config.PurchasePlan, error) {
	return nil, nil
}

func (m *mockConfigStore) UpdatePurchasePlan(ctx context.Context, plan *config.PurchasePlan) error {
	return nil
}

func (m *mockConfigStore) DeletePurchasePlan(ctx context.Context, planID string) error {
	return nil
}

func (m *mockConfigStore) ListPurchasePlans(ctx context.Context) ([]config.PurchasePlan, error) {
	return nil, nil
}

func (m *mockConfigStore) SavePurchaseExecution(ctx context.Context, execution *config.PurchaseExecution) error {
	return nil
}

func (m *mockConfigStore) GetPendingExecutions(ctx context.Context) ([]config.PurchaseExecution, error) {
	return nil, nil
}

func (m *mockConfigStore) GetExecutionByID(ctx context.Context, executionID string) (*config.PurchaseExecution, error) {
	return nil, nil
}

func (m *mockConfigStore) GetExecutionByPlanAndDate(ctx context.Context, planID string, scheduledDate time.Time) (*config.PurchaseExecution, error) {
	return nil, nil
}

func (m *mockConfigStore) SavePurchaseHistory(ctx context.Context, record *config.PurchaseHistoryRecord) error {
	return nil
}

func (m *mockConfigStore) GetPurchaseHistory(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
	if m.getPurchaseHistoryFunc != nil {
		return m.getPurchaseHistoryFunc(ctx, accountID, limit)
	}
	return nil, nil
}

func (m *mockConfigStore) GetAllPurchaseHistory(ctx context.Context, limit int) ([]config.PurchaseHistoryRecord, error) {
	return nil, nil
}

func (m *mockConfigStore) CleanupOldExecutions(ctx context.Context, retentionDays int) (int64, error) {
	return 0, nil
}

func (m *mockConfigStore) SaveRIExchangeRecord(ctx context.Context, record *config.RIExchangeRecord) error {
	return nil
}
func (m *mockConfigStore) GetRIExchangeRecord(ctx context.Context, id string) (*config.RIExchangeRecord, error) {
	return nil, nil
}
func (m *mockConfigStore) GetRIExchangeRecordByToken(ctx context.Context, token string) (*config.RIExchangeRecord, error) {
	return nil, nil
}
func (m *mockConfigStore) GetRIExchangeHistory(ctx context.Context, since time.Time, limit int) ([]config.RIExchangeRecord, error) {
	return nil, nil
}
func (m *mockConfigStore) TransitionRIExchangeStatus(ctx context.Context, id string, fromStatus string, toStatus string) (*config.RIExchangeRecord, error) {
	return nil, nil
}
func (m *mockConfigStore) CompleteRIExchange(ctx context.Context, id string, exchangeID string) error {
	return nil
}
func (m *mockConfigStore) FailRIExchange(ctx context.Context, id string, errorMsg string) error {
	return nil
}
func (m *mockConfigStore) GetRIExchangeDailySpend(ctx context.Context, date time.Time) (string, error) {
	return "0", nil
}
func (m *mockConfigStore) CancelAllPendingExchanges(ctx context.Context) (int64, error) {
	return 0, nil
}
func (m *mockConfigStore) GetStaleProcessingExchanges(ctx context.Context, olderThan time.Duration) ([]config.RIExchangeRecord, error) {
	return nil, nil
}

func (m *mockConfigStore) CreateCloudAccount(ctx context.Context, account *config.CloudAccount) error {
	return nil
}
func (m *mockConfigStore) GetCloudAccount(ctx context.Context, id string) (*config.CloudAccount, error) {
	return nil, nil
}
func (m *mockConfigStore) UpdateCloudAccount(ctx context.Context, account *config.CloudAccount) error {
	return nil
}
func (m *mockConfigStore) DeleteCloudAccount(ctx context.Context, id string) error {
	return nil
}
func (m *mockConfigStore) ListCloudAccounts(ctx context.Context, filter config.CloudAccountFilter) ([]config.CloudAccount, error) {
	return nil, nil
}
func (m *mockConfigStore) SaveAccountCredential(ctx context.Context, accountID, credentialType, encryptedBlob string) error {
	return nil
}
func (m *mockConfigStore) GetAccountCredential(ctx context.Context, accountID, credentialType string) (string, error) {
	return "", nil
}
func (m *mockConfigStore) DeleteAccountCredentials(ctx context.Context, accountID string) error {
	return nil
}
func (m *mockConfigStore) HasAccountCredentials(ctx context.Context, accountID string) (bool, error) {
	return false, nil
}
func (m *mockConfigStore) GetAccountServiceOverride(ctx context.Context, accountID, provider, service string) (*config.AccountServiceOverride, error) {
	return nil, nil
}
func (m *mockConfigStore) SaveAccountServiceOverride(ctx context.Context, override *config.AccountServiceOverride) error {
	return nil
}
func (m *mockConfigStore) DeleteAccountServiceOverride(ctx context.Context, accountID, provider, service string) error {
	return nil
}
func (m *mockConfigStore) ListAccountServiceOverrides(ctx context.Context, accountID string) ([]config.AccountServiceOverride, error) {
	return nil, nil
}
func (m *mockConfigStore) SetPlanAccounts(ctx context.Context, planID string, accountIDs []string) error {
	return nil
}
func (m *mockConfigStore) GetPlanAccounts(ctx context.Context, planID string) ([]config.CloudAccount, error) {
	return nil, nil
}

// TestNewCollector tests the NewCollector function
func TestNewCollector(t *testing.T) {
	t.Run("returns error when analytics store is nil", func(t *testing.T) {
		cfg := CollectorConfig{
			AnalyticsStore: nil,
			AccountID:      "test-account",
		}
		configStore := &mockConfigStore{}

		collector, err := NewCollector(cfg, configStore)

		assert.Nil(t, collector)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "analytics store is required")
	})

	t.Run("returns error when config store is nil", func(t *testing.T) {
		cfg := CollectorConfig{
			AnalyticsStore: &mockAnalyticsStore{},
			AccountID:      "test-account",
		}

		collector, err := NewCollector(cfg, nil)

		assert.Nil(t, collector)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "config store is required")
	})

	t.Run("creates collector successfully with valid inputs", func(t *testing.T) {
		analyticsStore := &mockAnalyticsStore{}
		configStore := &mockConfigStore{}
		cfg := CollectorConfig{
			AnalyticsStore: analyticsStore,
			AccountID:      "test-account-123",
		}

		collector, err := NewCollector(cfg, configStore)

		require.NoError(t, err)
		assert.NotNil(t, collector)
		assert.Equal(t, "test-account-123", collector.accountID)
	})

	t.Run("creates collector with empty account ID", func(t *testing.T) {
		analyticsStore := &mockAnalyticsStore{}
		configStore := &mockConfigStore{}
		cfg := CollectorConfig{
			AnalyticsStore: analyticsStore,
			AccountID:      "",
		}

		collector, err := NewCollector(cfg, configStore)

		require.NoError(t, err)
		assert.NotNil(t, collector)
		assert.Equal(t, "", collector.accountID)
	})
}

// TestCollectorCollect tests the Collect method
func TestCollectorCollect(t *testing.T) {
	t.Run("returns error when GetPurchaseHistory fails", func(t *testing.T) {
		analyticsStore := &mockAnalyticsStore{}
		configStore := &mockConfigStore{
			getPurchaseHistoryFunc: func(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
				return nil, errors.New("database connection failed")
			},
		}
		cfg := CollectorConfig{
			AnalyticsStore: analyticsStore,
			AccountID:      "test-account",
		}
		collector, err := NewCollector(cfg, configStore)
		require.NoError(t, err)

		err = collector.Collect(context.Background())

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get purchase history")
		assert.Contains(t, err.Error(), "database connection failed")
	})

	t.Run("handles empty purchase history", func(t *testing.T) {
		analyticsStore := &mockAnalyticsStore{}
		configStore := &mockConfigStore{
			getPurchaseHistoryFunc: func(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
				return []config.PurchaseHistoryRecord{}, nil
			},
		}
		cfg := CollectorConfig{
			AnalyticsStore: analyticsStore,
			AccountID:      "test-account",
		}
		collector, err := NewCollector(cfg, configStore)
		require.NoError(t, err)

		err = collector.Collect(context.Background())

		assert.NoError(t, err)
		assert.Empty(t, analyticsStore.savedSnapshots)
	})

	t.Run("skips expired purchases", func(t *testing.T) {
		analyticsStore := &mockAnalyticsStore{}
		// Create a purchase that expired 2 years ago
		expiredTime := time.Now().AddDate(-3, 0, 0) // 3 years ago
		configStore := &mockConfigStore{
			getPurchaseHistoryFunc: func(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
				return []config.PurchaseHistoryRecord{
					{
						AccountID:        "test-account",
						PurchaseID:       "purchase-1",
						Timestamp:        expiredTime,
						Provider:         "aws",
						Service:          "rds",
						Region:           "us-east-1",
						Term:             1, // 1 year term, so expired
						EstimatedSavings: 100.0,
						UpfrontCost:      500.0,
					},
				}, nil
			},
		}
		cfg := CollectorConfig{
			AnalyticsStore: analyticsStore,
			AccountID:      "test-account",
		}
		collector, err := NewCollector(cfg, configStore)
		require.NoError(t, err)

		err = collector.Collect(context.Background())

		assert.NoError(t, err)
		assert.Empty(t, analyticsStore.savedSnapshots)
	})

	t.Run("processes active purchases and creates snapshots", func(t *testing.T) {
		analyticsStore := &mockAnalyticsStore{}
		// Create an active purchase (purchased 6 months ago with 1 year term)
		activeTime := time.Now().AddDate(0, -6, 0)
		configStore := &mockConfigStore{
			getPurchaseHistoryFunc: func(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
				return []config.PurchaseHistoryRecord{
					{
						AccountID:        "test-account",
						PurchaseID:       "purchase-1",
						Timestamp:        activeTime,
						Provider:         "aws",
						Service:          "rds",
						Region:           "us-east-1",
						ResourceType:     "db.m5.large",
						Term:             1,     // 1 year term
						EstimatedSavings: 720.0, // Monthly savings
						UpfrontCost:      1000.0,
					},
				}, nil
			},
		}
		cfg := CollectorConfig{
			AnalyticsStore: analyticsStore,
			AccountID:      "test-account",
		}
		collector, err := NewCollector(cfg, configStore)
		require.NoError(t, err)

		err = collector.Collect(context.Background())

		assert.NoError(t, err)
		require.Len(t, analyticsStore.savedSnapshots, 1)

		snapshot := analyticsStore.savedSnapshots[0]
		assert.Equal(t, "test-account", snapshot.AccountID)
		assert.Equal(t, "aws", snapshot.Provider)
		assert.Equal(t, "rds", snapshot.Service)
		assert.Equal(t, "us-east-1", snapshot.Region)
		assert.Equal(t, "RI", snapshot.CommitmentType)
		assert.Greater(t, snapshot.TotalSavings, 0.0)
		assert.Greater(t, snapshot.TotalCommitment, 0.0)
	})

	t.Run("aggregates multiple purchases for same service/provider/region", func(t *testing.T) {
		analyticsStore := &mockAnalyticsStore{}
		activeTime := time.Now().AddDate(0, -3, 0)
		configStore := &mockConfigStore{
			getPurchaseHistoryFunc: func(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
				return []config.PurchaseHistoryRecord{
					{
						AccountID:        "test-account",
						PurchaseID:       "purchase-1",
						Timestamp:        activeTime,
						Provider:         "aws",
						Service:          "rds",
						Region:           "us-east-1",
						Term:             1,
						EstimatedSavings: 100.0,
						UpfrontCost:      500.0,
					},
					{
						AccountID:        "test-account",
						PurchaseID:       "purchase-2",
						Timestamp:        activeTime,
						Provider:         "aws",
						Service:          "rds",
						Region:           "us-east-1",
						Term:             1,
						EstimatedSavings: 200.0,
						UpfrontCost:      1000.0,
					},
				}, nil
			},
		}
		cfg := CollectorConfig{
			AnalyticsStore: analyticsStore,
			AccountID:      "test-account",
		}
		collector, err := NewCollector(cfg, configStore)
		require.NoError(t, err)

		err = collector.Collect(context.Background())

		assert.NoError(t, err)
		// Should only create one snapshot for the aggregated data
		require.Len(t, analyticsStore.savedSnapshots, 1)

		snapshot := analyticsStore.savedSnapshots[0]
		// Verify metadata shows 2 active purchases
		assert.Equal(t, 2, snapshot.Metadata["active_purchases"])
	})

	t.Run("creates separate snapshots for different regions", func(t *testing.T) {
		analyticsStore := &mockAnalyticsStore{}
		activeTime := time.Now().AddDate(0, -3, 0)
		configStore := &mockConfigStore{
			getPurchaseHistoryFunc: func(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
				return []config.PurchaseHistoryRecord{
					{
						AccountID:        "test-account",
						PurchaseID:       "purchase-1",
						Timestamp:        activeTime,
						Provider:         "aws",
						Service:          "rds",
						Region:           "us-east-1",
						Term:             1,
						EstimatedSavings: 100.0,
						UpfrontCost:      500.0,
					},
					{
						AccountID:        "test-account",
						PurchaseID:       "purchase-2",
						Timestamp:        activeTime,
						Provider:         "aws",
						Service:          "rds",
						Region:           "us-west-2",
						Term:             1,
						EstimatedSavings: 150.0,
						UpfrontCost:      700.0,
					},
				}, nil
			},
		}
		cfg := CollectorConfig{
			AnalyticsStore: analyticsStore,
			AccountID:      "test-account",
		}
		collector, err := NewCollector(cfg, configStore)
		require.NoError(t, err)

		err = collector.Collect(context.Background())

		assert.NoError(t, err)
		assert.Len(t, analyticsStore.savedSnapshots, 2)

		regions := make(map[string]bool)
		for _, s := range analyticsStore.savedSnapshots {
			regions[s.Region] = true
		}
		assert.True(t, regions["us-east-1"])
		assert.True(t, regions["us-west-2"])
	})

	t.Run("creates separate snapshots for different providers", func(t *testing.T) {
		analyticsStore := &mockAnalyticsStore{}
		activeTime := time.Now().AddDate(0, -3, 0)
		configStore := &mockConfigStore{
			getPurchaseHistoryFunc: func(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
				return []config.PurchaseHistoryRecord{
					{
						AccountID:        "test-account",
						PurchaseID:       "purchase-1",
						Timestamp:        activeTime,
						Provider:         "aws",
						Service:          "rds",
						Region:           "us-east-1",
						Term:             1,
						EstimatedSavings: 100.0,
						UpfrontCost:      500.0,
					},
					{
						AccountID:        "test-account",
						PurchaseID:       "purchase-2",
						Timestamp:        activeTime,
						Provider:         "gcp",
						Service:          "cloudsql",
						Region:           "us-east1",
						Term:             1,
						EstimatedSavings: 150.0,
						UpfrontCost:      700.0,
					},
				}, nil
			},
		}
		cfg := CollectorConfig{
			AnalyticsStore: analyticsStore,
			AccountID:      "test-account",
		}
		collector, err := NewCollector(cfg, configStore)
		require.NoError(t, err)

		err = collector.Collect(context.Background())

		assert.NoError(t, err)
		assert.Len(t, analyticsStore.savedSnapshots, 2)

		providers := make(map[string]bool)
		for _, s := range analyticsStore.savedSnapshots {
			providers[s.Provider] = true
		}
		assert.True(t, providers["aws"])
		assert.True(t, providers["gcp"])
	})

	t.Run("sets SavingsPlan commitment type for SavingsPlans service", func(t *testing.T) {
		analyticsStore := &mockAnalyticsStore{}
		activeTime := time.Now().AddDate(0, -3, 0)
		configStore := &mockConfigStore{
			getPurchaseHistoryFunc: func(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
				return []config.PurchaseHistoryRecord{
					{
						AccountID:        "test-account",
						PurchaseID:       "purchase-1",
						Timestamp:        activeTime,
						Provider:         "aws",
						Service:          "SavingsPlans",
						Region:           "us-east-1",
						Term:             1,
						EstimatedSavings: 500.0,
						UpfrontCost:      2000.0,
					},
				}, nil
			},
		}
		cfg := CollectorConfig{
			AnalyticsStore: analyticsStore,
			AccountID:      "test-account",
		}
		collector, err := NewCollector(cfg, configStore)
		require.NoError(t, err)

		err = collector.Collect(context.Background())

		assert.NoError(t, err)
		require.Len(t, analyticsStore.savedSnapshots, 1)
		assert.Equal(t, "SavingsPlan", analyticsStore.savedSnapshots[0].CommitmentType)
	})

	t.Run("returns error when BulkInsertSnapshots fails", func(t *testing.T) {
		analyticsStore := &mockAnalyticsStore{
			bulkInsertSnapshotsFunc: func(ctx context.Context, snapshots []SavingsSnapshot) error {
				return errors.New("bulk insert failed")
			},
		}
		activeTime := time.Now().AddDate(0, -3, 0)
		configStore := &mockConfigStore{
			getPurchaseHistoryFunc: func(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
				return []config.PurchaseHistoryRecord{
					{
						AccountID:        "test-account",
						PurchaseID:       "purchase-1",
						Timestamp:        activeTime,
						Provider:         "aws",
						Service:          "rds",
						Region:           "us-east-1",
						Term:             1,
						EstimatedSavings: 100.0,
						UpfrontCost:      500.0,
					},
				}, nil
			},
		}
		cfg := CollectorConfig{
			AnalyticsStore: analyticsStore,
			AccountID:      "test-account",
		}
		collector, err := NewCollector(cfg, configStore)
		require.NoError(t, err)

		err = collector.Collect(context.Background())

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to save snapshots")
	})

	t.Run("handles 3-year term purchases correctly", func(t *testing.T) {
		analyticsStore := &mockAnalyticsStore{}
		// Purchase made 2 years ago with 3-year term (still active)
		activeTime := time.Now().AddDate(-2, 0, 0)
		configStore := &mockConfigStore{
			getPurchaseHistoryFunc: func(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
				return []config.PurchaseHistoryRecord{
					{
						AccountID:        "test-account",
						PurchaseID:       "purchase-1",
						Timestamp:        activeTime,
						Provider:         "aws",
						Service:          "rds",
						Region:           "us-east-1",
						Term:             3, // 3 year term
						EstimatedSavings: 1000.0,
						UpfrontCost:      5000.0,
					},
				}, nil
			},
		}
		cfg := CollectorConfig{
			AnalyticsStore: analyticsStore,
			AccountID:      "test-account",
		}
		collector, err := NewCollector(cfg, configStore)
		require.NoError(t, err)

		err = collector.Collect(context.Background())

		assert.NoError(t, err)
		require.Len(t, analyticsStore.savedSnapshots, 1)
	})

	t.Run("calculates hourly savings correctly", func(t *testing.T) {
		analyticsStore := &mockAnalyticsStore{}
		activeTime := time.Now().AddDate(0, -1, 0) // 1 month ago
		monthlySavings := 720.0                    // $720/month
		expectedHourlySavings := monthlySavings / HoursPerMonth

		configStore := &mockConfigStore{
			getPurchaseHistoryFunc: func(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
				return []config.PurchaseHistoryRecord{
					{
						AccountID:        "test-account",
						PurchaseID:       "purchase-1",
						Timestamp:        activeTime,
						Provider:         "aws",
						Service:          "rds",
						Region:           "us-east-1",
						Term:             1,
						EstimatedSavings: monthlySavings,
						UpfrontCost:      0,
					},
				}, nil
			},
		}
		cfg := CollectorConfig{
			AnalyticsStore: analyticsStore,
			AccountID:      "test-account",
		}
		collector, err := NewCollector(cfg, configStore)
		require.NoError(t, err)

		err = collector.Collect(context.Background())

		assert.NoError(t, err)
		require.Len(t, analyticsStore.savedSnapshots, 1)
		assert.InDelta(t, expectedHourlySavings, analyticsStore.savedSnapshots[0].TotalSavings, 0.001)
	})

	t.Run("calculates amortized hourly commitment correctly", func(t *testing.T) {
		analyticsStore := &mockAnalyticsStore{}
		activeTime := time.Now().AddDate(0, -1, 0)
		upfrontCost := 8760.0 // $8760 for 1 year
		term := 1
		expectedHourlyCommitment := upfrontCost / (float64(term) * HoursPerYear) // Should be $1/hour

		configStore := &mockConfigStore{
			getPurchaseHistoryFunc: func(ctx context.Context, accountID string, limit int) ([]config.PurchaseHistoryRecord, error) {
				return []config.PurchaseHistoryRecord{
					{
						AccountID:        "test-account",
						PurchaseID:       "purchase-1",
						Timestamp:        activeTime,
						Provider:         "aws",
						Service:          "rds",
						Region:           "us-east-1",
						Term:             term,
						EstimatedSavings: 100.0,
						UpfrontCost:      upfrontCost,
					},
				}, nil
			},
		}
		cfg := CollectorConfig{
			AnalyticsStore: analyticsStore,
			AccountID:      "test-account",
		}
		collector, err := NewCollector(cfg, configStore)
		require.NoError(t, err)

		err = collector.Collect(context.Background())

		assert.NoError(t, err)
		require.Len(t, analyticsStore.savedSnapshots, 1)
		assert.InDelta(t, expectedHourlyCommitment, analyticsStore.savedSnapshots[0].TotalCommitment, 0.001)
	})
}

// TestConstants tests the exported constants
func TestConstants(t *testing.T) {
	t.Run("HoursPerYear is correct", func(t *testing.T) {
		assert.Equal(t, 365*24, HoursPerYear)
		assert.Equal(t, 8760, HoursPerYear)
	})

	t.Run("HoursPerMonth is correct", func(t *testing.T) {
		assert.Equal(t, 30*24, HoursPerMonth)
		assert.Equal(t, 720, HoursPerMonth)
	})
}
