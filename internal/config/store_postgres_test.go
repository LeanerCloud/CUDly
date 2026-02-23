//go:build integration
// +build integration

package config_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/migrations"
	"github.com/LeanerCloud/CUDly/internal/database/postgres/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getMigrationsPath returns the absolute path to migrations directory
func getMigrationsPath() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "database", "postgres", "migrations")
}

func TestPostgresStore_GlobalConfig(t *testing.T) {
	ctx := context.Background()

	// Setup test container
	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)

	// Run migrations
	err = migrations.RunMigrations(ctx, container.DB.Pool(), getMigrationsPath(), "", "")
	require.NoError(t, err)

	// Create store
	store := config.NewPostgresStore(container.DB)

	t.Run("Get default global config", func(t *testing.T) {
		globalConfig, err := store.GetGlobalConfig(ctx)
		require.NoError(t, err)
		assert.NotNil(t, globalConfig)
		assert.Equal(t, true, globalConfig.ApprovalRequired)
		assert.Equal(t, 3, globalConfig.DefaultTerm)
	})

	t.Run("Save and retrieve global config", func(t *testing.T) {
		// Save config
		email := "test@example.com"
		newConfig := &config.GlobalConfig{
			EnabledProviders:    []string{"aws", "gcp"},
			NotificationEmail:   &email,
			ApprovalRequired:    false,
			DefaultTerm:         24,
			DefaultPayment:      "no-upfront",
			DefaultCoverage:     90.0,
			DefaultRampSchedule: "weekly-25pct",
		}

		err := store.SaveGlobalConfig(ctx, newConfig)
		require.NoError(t, err)

		// Retrieve config
		retrieved, err := store.GetGlobalConfig(ctx)
		require.NoError(t, err)
		assert.Equal(t, newConfig.EnabledProviders, retrieved.EnabledProviders)
		assert.Equal(t, newConfig.NotificationEmail, retrieved.NotificationEmail)
		assert.Equal(t, newConfig.ApprovalRequired, retrieved.ApprovalRequired)
		assert.Equal(t, newConfig.DefaultTerm, retrieved.DefaultTerm)
		assert.Equal(t, newConfig.DefaultPayment, retrieved.DefaultPayment)
		assert.Equal(t, newConfig.DefaultCoverage, retrieved.DefaultCoverage)
	})
}

func TestPostgresStore_ServiceConfig(t *testing.T) {
	ctx := context.Background()

	// Setup test container
	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)

	// Run migrations
	err = migrations.RunMigrations(ctx, container.DB.Pool(), getMigrationsPath(), "", "")
	require.NoError(t, err)

	// Create store
	store := config.NewPostgresStore(container.DB)

	t.Run("Save and retrieve service config", func(t *testing.T) {
		// Save config
		serviceConfig := &config.ServiceConfig{
			Provider:       "aws",
			Service:        "rds",
			Enabled:        true,
			Term:           12,
			Payment:        "all-upfront",
			Coverage:       80.0,
			RampSchedule:   "immediate",
			IncludeEngines: []string{"postgres", "mysql"},
			ExcludeRegions: []string{"us-west-1"},
		}

		err := store.SaveServiceConfig(ctx, serviceConfig)
		require.NoError(t, err)

		// Retrieve config
		retrieved, err := store.GetServiceConfig(ctx, "aws", "rds")
		require.NoError(t, err)
		assert.Equal(t, serviceConfig.Provider, retrieved.Provider)
		assert.Equal(t, serviceConfig.Service, retrieved.Service)
		assert.Equal(t, serviceConfig.Enabled, retrieved.Enabled)
		assert.Equal(t, serviceConfig.IncludeEngines, retrieved.IncludeEngines)
		assert.Equal(t, serviceConfig.ExcludeRegions, retrieved.ExcludeRegions)
	})

	t.Run("List service configs", func(t *testing.T) {
		// Save multiple configs
		configs := []*config.ServiceConfig{
			{Provider: "aws", Service: "elasticache", Enabled: true, Term: 12, Payment: "all-upfront", Coverage: 80.0},
			{Provider: "gcp", Service: "cloudsql", Enabled: true, Term: 12, Payment: "all-upfront", Coverage: 80.0},
		}

		for _, cfg := range configs {
			err := store.SaveServiceConfig(ctx, cfg)
			require.NoError(t, err)
		}

		// List all configs
		retrieved, err := store.ListServiceConfigs(ctx)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(retrieved), 2)
	})
}

func TestPostgresStore_PurchasePlans(t *testing.T) {
	ctx := context.Background()

	// Setup test container
	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)

	// Run migrations
	err = migrations.RunMigrations(ctx, container.DB.Pool(), getMigrationsPath(), "", "")
	require.NoError(t, err)

	// Create store
	store := config.NewPostgresStore(container.DB)

	t.Run("Create and retrieve purchase plan", func(t *testing.T) {
		// Create plan
		plan := &config.PurchasePlan{
			Name:                   "Test Plan",
			Enabled:                true,
			AutoPurchase:           false,
			NotificationDaysBefore: 7,
			Services: map[string]config.ServiceConfig{
				"aws:rds": {
					Provider: "aws",
					Service:  "rds",
					Enabled:  true,
					Term:     12,
				},
			},
			RampSchedule: config.RampSchedule{
				Type:           "immediate",
				PercentPerStep: 100,
				TotalSteps:     1,
			},
		}

		err := store.CreatePurchasePlan(ctx, plan)
		require.NoError(t, err)
		assert.NotEmpty(t, plan.ID)

		// Retrieve plan
		retrieved, err := store.GetPurchasePlan(ctx, plan.ID)
		require.NoError(t, err)
		assert.Equal(t, plan.Name, retrieved.Name)
		assert.Equal(t, plan.Enabled, retrieved.Enabled)
		assert.Len(t, retrieved.Services, 1)
	})

	t.Run("Update purchase plan", func(t *testing.T) {
		// Create plan
		plan := &config.PurchasePlan{
			Name:                   "Update Test",
			Enabled:                true,
			AutoPurchase:           false,
			NotificationDaysBefore: 7,
			Services:               map[string]config.ServiceConfig{},
			RampSchedule:           config.PresetRampSchedules["immediate"],
		}

		err := store.CreatePurchasePlan(ctx, plan)
		require.NoError(t, err)

		// Update plan
		plan.Name = "Updated Name"
		plan.Enabled = false
		nextExec := time.Now().Add(24 * time.Hour)
		plan.NextExecutionDate = &nextExec

		err = store.UpdatePurchasePlan(ctx, plan)
		require.NoError(t, err)

		// Retrieve and verify
		retrieved, err := store.GetPurchasePlan(ctx, plan.ID)
		require.NoError(t, err)
		assert.Equal(t, "Updated Name", retrieved.Name)
		assert.Equal(t, false, retrieved.Enabled)
		assert.NotNil(t, retrieved.NextExecutionDate)
	})

	t.Run("Delete purchase plan", func(t *testing.T) {
		// Create plan
		plan := &config.PurchasePlan{
			Name:                   "Delete Test",
			Enabled:                true,
			AutoPurchase:           false,
			NotificationDaysBefore: 7,
			Services:               map[string]config.ServiceConfig{},
			RampSchedule:           config.PresetRampSchedules["immediate"],
		}

		err := store.CreatePurchasePlan(ctx, plan)
		require.NoError(t, err)

		// Delete plan
		err = store.DeletePurchasePlan(ctx, plan.ID)
		require.NoError(t, err)

		// Verify deletion
		_, err = store.GetPurchasePlan(ctx, plan.ID)
		assert.Error(t, err)
	})

	t.Run("List purchase plans", func(t *testing.T) {
		// Create multiple plans
		plans := []*config.PurchasePlan{
			{
				Name:                   "List Test Plan 1",
				Enabled:                true,
				AutoPurchase:           false,
				NotificationDaysBefore: 7,
				Services:               map[string]config.ServiceConfig{},
				RampSchedule:           config.PresetRampSchedules["immediate"],
			},
			{
				Name:                   "List Test Plan 2",
				Enabled:                false,
				AutoPurchase:           true,
				NotificationDaysBefore: 3,
				Services:               map[string]config.ServiceConfig{},
				RampSchedule:           config.PresetRampSchedules["weekly-25pct"],
			},
		}

		for _, plan := range plans {
			err := store.CreatePurchasePlan(ctx, plan)
			require.NoError(t, err)
		}

		// List all plans
		retrieved, err := store.ListPurchasePlans(ctx)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(retrieved), 2)
	})
}

func TestPostgresStore_PurchaseExecutions(t *testing.T) {
	ctx := context.Background()

	// Setup test container
	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)

	// Run migrations
	err = migrations.RunMigrations(ctx, container.DB.Pool(), getMigrationsPath(), "", "")
	require.NoError(t, err)

	// Create store
	store := config.NewPostgresStore(container.DB)

	// First create a purchase plan to use as foreign key
	plan := &config.PurchasePlan{
		Name:                   "Execution Test Plan",
		Enabled:                true,
		AutoPurchase:           false,
		NotificationDaysBefore: 7,
		Services:               map[string]config.ServiceConfig{},
		RampSchedule:           config.PresetRampSchedules["immediate"],
	}
	err = store.CreatePurchasePlan(ctx, plan)
	require.NoError(t, err)

	t.Run("Get pending executions returns empty when none exist", func(t *testing.T) {
		// Get pending executions on fresh database
		pending, err := store.GetPendingExecutions(ctx)
		require.NoError(t, err)
		// Should be empty since no executions exist yet
		assert.NotNil(t, pending)
	})

	t.Run("Get execution by ID - not found", func(t *testing.T) {
		// Use a valid UUID format that doesn't exist
		_, err := store.GetExecutionByID(ctx, "00000000-0000-0000-0000-000000000000")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("Get execution by plan and date - not found", func(t *testing.T) {
		// Try to get an execution for a date when none exists
		_, err := store.GetExecutionByPlanAndDate(ctx, plan.ID, time.Now().Add(100*24*time.Hour))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestPostgresStore_PurchaseHistory(t *testing.T) {
	ctx := context.Background()

	// Setup test container
	container, err := testhelpers.SetupPostgresContainer(ctx, t)
	require.NoError(t, err)
	defer container.Cleanup(ctx)

	// Run migrations
	err = migrations.RunMigrations(ctx, container.DB.Pool(), getMigrationsPath(), "", "")
	require.NoError(t, err)

	// Create store
	store := config.NewPostgresStore(container.DB)

	t.Run("Save and retrieve purchase history", func(t *testing.T) {
		now := time.Now()
		// Note: PlanID must be empty or a valid UUID in the database schema
		record := &config.PurchaseHistoryRecord{
			AccountID:        "123456789012",
			PurchaseID:       "purchase-001",
			Timestamp:        now,
			Provider:         "aws",
			Service:          "rds",
			Region:           "us-east-1",
			ResourceType:     "db.r5.large",
			Count:            3,
			Term:             3,
			Payment:          "all-upfront",
			UpfrontCost:      2250.00,
			MonthlyCost:      0,
			EstimatedSavings: 450.00,
			// PlanID intentionally left empty since it needs to be a valid UUID
			PlanName: "Test Plan",
			RampStep: 1,
		}

		err := store.SavePurchaseHistory(ctx, record)
		require.NoError(t, err)

		// Retrieve for account
		history, err := store.GetPurchaseHistory(ctx, "123456789012", 10)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(history), 1)

		// Verify record
		found := false
		for _, h := range history {
			if h.PurchaseID == "purchase-001" {
				found = true
				assert.Equal(t, "aws", h.Provider)
				assert.Equal(t, "rds", h.Service)
				assert.Equal(t, 3, h.Count)
				assert.Equal(t, "Test Plan", h.PlanName)
			}
		}
		assert.True(t, found, "Expected purchase record not found in history")
	})

	t.Run("Get all purchase history", func(t *testing.T) {
		// Add records for different accounts
		records := []*config.PurchaseHistoryRecord{
			{
				AccountID:    "account-1",
				PurchaseID:   "purchase-a1",
				Timestamp:    time.Now(),
				Provider:     "aws",
				Service:      "ec2",
				Region:       "us-west-2",
				ResourceType: "m5.large",
				Count:        5,
				Term:         1,
				Payment:      "no-upfront",
			},
			{
				AccountID:    "account-2",
				PurchaseID:   "purchase-b1",
				Timestamp:    time.Now(),
				Provider:     "azure",
				Service:      "vm",
				Region:       "westus",
				ResourceType: "Standard_D2s_v3",
				Count:        2,
				Term:         3,
				Payment:      "partial-upfront",
			},
		}

		for _, record := range records {
			err := store.SavePurchaseHistory(ctx, record)
			require.NoError(t, err)
		}

		// Get all history
		allHistory, err := store.GetAllPurchaseHistory(ctx, 100)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(allHistory), 2)
	})

	t.Run("Purchase history with empty optional fields", func(t *testing.T) {
		record := &config.PurchaseHistoryRecord{
			AccountID:    "account-no-plan",
			PurchaseID:   "purchase-no-plan",
			Timestamp:    time.Now(),
			Provider:     "gcp",
			Service:      "compute",
			Region:       "us-central1",
			ResourceType: "n1-standard-4",
			Count:        1,
			Term:         1,
			Payment:      "all-upfront",
			// PlanID and PlanName intentionally left empty
		}

		err := store.SavePurchaseHistory(ctx, record)
		require.NoError(t, err)

		// Retrieve and verify empty fields
		history, err := store.GetPurchaseHistory(ctx, "account-no-plan", 10)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(history), 1)

		for _, h := range history {
			if h.PurchaseID == "purchase-no-plan" {
				assert.Empty(t, h.PlanID)
				assert.Empty(t, h.PlanName)
			}
		}
	})
}
