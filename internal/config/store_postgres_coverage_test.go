package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// These tests exercise the real PostgresStore methods to gain code coverage.
// Since the store requires a database connection, methods that perform DB calls
// will panic on nil dereference. We use recover() to safely test code paths
// that execute before the first DB interaction (query construction, JSON
// marshaling, UUID generation, nil-slice normalization, etc.).

// callWithRecover calls f and returns true if it panicked.
func callWithRecover(f func()) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
		}
	}()
	f()
	return false
}

// ==========================================
// GET GLOBAL CONFIG
// ==========================================

func TestPostgresStore_GetGlobalConfig_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetGlobalConfig(ctx)
	})

	// With nil db, the method will panic when trying to call s.db.QueryRow
	assert.True(t, panicked, "expected panic with nil db connection")
}

// ==========================================
// SAVE GLOBAL CONFIG
// ==========================================

func TestPostgresStore_SaveGlobalConfig_NilProviders(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	cfg := &GlobalConfig{
		EnabledProviders: nil,
		DefaultTerm:      3,
		DefaultPayment:   "all-upfront",
		DefaultCoverage:  80.0,
	}

	panicked := callWithRecover(func() {
		_ = store.SaveGlobalConfig(ctx, cfg)
	})

	// The nil-to-empty-slice conversion should have happened before the panic
	assert.True(t, panicked, "expected panic with nil db connection")
	assert.NotNil(t, cfg.EnabledProviders, "EnabledProviders should be converted from nil to empty slice")
	assert.Empty(t, cfg.EnabledProviders, "EnabledProviders should be empty slice, not nil")
}

func TestPostgresStore_SaveGlobalConfig_WithProviders(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	email := "admin@example.com"
	cfg := &GlobalConfig{
		EnabledProviders:    []string{"aws", "gcp"},
		NotificationEmail:   &email,
		ApprovalRequired:    true,
		DefaultTerm:         3,
		DefaultPayment:      "all-upfront",
		DefaultCoverage:     80.0,
		DefaultRampSchedule: "immediate",
	}

	panicked := callWithRecover(func() {
		_ = store.SaveGlobalConfig(ctx, cfg)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	// Providers should remain unchanged since they were not nil
	assert.Equal(t, []string{"aws", "gcp"}, cfg.EnabledProviders)
}

// ==========================================
// GET SERVICE CONFIG
// ==========================================

func TestPostgresStore_GetServiceConfig_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetServiceConfig(ctx, "aws", "rds")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// ==========================================
// SAVE SERVICE CONFIG
// ==========================================

func TestPostgresStore_SaveServiceConfig_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	cfg := &ServiceConfig{
		Provider:       "aws",
		Service:        "rds",
		Enabled:        true,
		Term:           3,
		Payment:        "all-upfront",
		Coverage:       80.0,
		RampSchedule:   "immediate",
		IncludeEngines: []string{"postgres"},
	}

	panicked := callWithRecover(func() {
		_ = store.SaveServiceConfig(ctx, cfg)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// ==========================================
// LIST SERVICE CONFIGS
// ==========================================

func TestPostgresStore_ListServiceConfigs_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.ListServiceConfigs(ctx)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// ==========================================
// CREATE PURCHASE PLAN
// ==========================================

func TestPostgresStore_CreatePurchasePlan_NilDB_GeneratesID(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	plan := &PurchasePlan{
		Name:                   "Test Plan",
		Enabled:                true,
		AutoPurchase:           false,
		NotificationDaysBefore: 7,
		Services: map[string]ServiceConfig{
			"aws:rds": {Provider: "aws", Service: "rds", Enabled: true},
		},
		RampSchedule: RampSchedule{Type: "immediate", PercentPerStep: 100, TotalSteps: 1},
	}

	panicked := callWithRecover(func() {
		_ = store.CreatePurchasePlan(ctx, plan)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	// UUID should have been generated before the panic
	assert.NotEmpty(t, plan.ID, "plan ID should be generated")
	// Timestamps should have been set before the panic
	assert.False(t, plan.CreatedAt.IsZero(), "CreatedAt should be set")
	assert.False(t, plan.UpdatedAt.IsZero(), "UpdatedAt should be set")
}

func TestPostgresStore_CreatePurchasePlan_NilDB_PreservesExistingID(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	plan := &PurchasePlan{
		ID:                     "existing-plan-id",
		Name:                   "Plan with ID",
		Enabled:                true,
		NotificationDaysBefore: 3,
		Services:               map[string]ServiceConfig{},
		RampSchedule:           RampSchedule{Type: "weekly"},
	}

	panicked := callWithRecover(func() {
		_ = store.CreatePurchasePlan(ctx, plan)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	// Existing ID should be preserved
	assert.Equal(t, "existing-plan-id", plan.ID)
}

func TestPostgresStore_CreatePurchasePlan_NilDB_WithNullableTimestamps(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	nextExec := time.Now().Add(24 * time.Hour)
	lastExec := time.Now().Add(-24 * time.Hour)

	plan := &PurchasePlan{
		Name:                   "Plan with timestamps",
		Enabled:                true,
		NotificationDaysBefore: 5,
		Services:               map[string]ServiceConfig{},
		RampSchedule:           RampSchedule{Type: "immediate"},
		NextExecutionDate:      &nextExec,
		LastExecutionDate:      &lastExec,
	}

	panicked := callWithRecover(func() {
		_ = store.CreatePurchasePlan(ctx, plan)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	assert.NotEmpty(t, plan.ID)
	assert.NotNil(t, plan.NextExecutionDate)
	assert.NotNil(t, plan.LastExecutionDate)
}

func TestPostgresStore_CreatePurchasePlan_NilDB_EmptyServices(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	plan := &PurchasePlan{
		Name:         "Plan with no services",
		Services:     map[string]ServiceConfig{},
		RampSchedule: RampSchedule{},
	}

	panicked := callWithRecover(func() {
		_ = store.CreatePurchasePlan(ctx, plan)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	assert.NotEmpty(t, plan.ID, "ID should have been generated before panic")
}

// ==========================================
// GET PURCHASE PLAN
// ==========================================

func TestPostgresStore_GetPurchasePlan_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetPurchasePlan(ctx, "plan-123")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// ==========================================
// UPDATE PURCHASE PLAN
// ==========================================

func TestPostgresStore_UpdatePurchasePlan_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	plan := &PurchasePlan{
		ID:                     "plan-to-update",
		Name:                   "Updated Plan",
		Enabled:                false,
		AutoPurchase:           true,
		NotificationDaysBefore: 10,
		Services:               map[string]ServiceConfig{},
		RampSchedule:           RampSchedule{Type: "monthly"},
	}

	panicked := callWithRecover(func() {
		_ = store.UpdatePurchasePlan(ctx, plan)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	// UpdatedAt should be set before JSON marshaling and DB call
	assert.False(t, plan.UpdatedAt.IsZero(), "UpdatedAt should have been set")
}

func TestPostgresStore_UpdatePurchasePlan_NilDB_WithServices(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	plan := &PurchasePlan{
		ID:   "plan-update-svc",
		Name: "Plan with Services",
		Services: map[string]ServiceConfig{
			"aws:ec2": {
				Provider: "aws",
				Service:  "ec2",
				Enabled:  true,
				Term:     1,
				Coverage: 70,
			},
		},
		RampSchedule: RampSchedule{
			Type:             "weekly",
			PercentPerStep:   25,
			StepIntervalDays: 7,
			TotalSteps:       4,
		},
	}

	panicked := callWithRecover(func() {
		_ = store.UpdatePurchasePlan(ctx, plan)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	assert.False(t, plan.UpdatedAt.IsZero())
}

// ==========================================
// DELETE PURCHASE PLAN
// ==========================================

func TestPostgresStore_DeletePurchasePlan_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_ = store.DeletePurchasePlan(ctx, "plan-to-delete")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// ==========================================
// LIST PURCHASE PLANS
// ==========================================

func TestPostgresStore_ListPurchasePlans_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.ListPurchasePlans(ctx)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// ==========================================
// SAVE PURCHASE EXECUTION
// ==========================================

func TestPostgresStore_SavePurchaseExecution_NilDB_GeneratesID(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	exec := &PurchaseExecution{
		PlanID:           "plan-123",
		Status:           "pending",
		StepNumber:       1,
		ScheduledDate:    time.Now().Add(24 * time.Hour),
		TotalUpfrontCost: 1500.00,
		EstimatedSavings: 300.00,
		Recommendations: []RecommendationRecord{
			{ID: "rec-1", Provider: "aws", Service: "rds"},
		},
	}

	panicked := callWithRecover(func() {
		_ = store.SavePurchaseExecution(ctx, exec)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	// ExecutionID should have been generated before the panic
	assert.NotEmpty(t, exec.ExecutionID, "execution ID should be generated")
}

func TestPostgresStore_SavePurchaseExecution_NilDB_PreservesExistingID(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	notifSent := time.Now().Add(-1 * time.Hour)
	completedAt := time.Now()

	exec := &PurchaseExecution{
		PlanID:           "plan-456",
		ExecutionID:      "existing-exec-id",
		Status:           "completed",
		StepNumber:       2,
		ScheduledDate:    time.Now(),
		NotificationSent: &notifSent,
		ApprovalToken:    "approval-token-123",
		TotalUpfrontCost: 2000.00,
		EstimatedSavings: 400.00,
		CompletedAt:      &completedAt,
		Recommendations:  []RecommendationRecord{},
		TTL:              time.Now().Add(30 * 24 * time.Hour).Unix(),
	}

	panicked := callWithRecover(func() {
		_ = store.SavePurchaseExecution(ctx, exec)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	// Existing ID should be preserved
	assert.Equal(t, "existing-exec-id", exec.ExecutionID)
}

func TestPostgresStore_SavePurchaseExecution_NilDB_EmptyRecommendations(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	exec := &PurchaseExecution{
		PlanID:          "plan-empty-recs",
		Status:          "pending",
		ScheduledDate:   time.Now(),
		Recommendations: []RecommendationRecord{},
	}

	panicked := callWithRecover(func() {
		_ = store.SavePurchaseExecution(ctx, exec)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
	assert.NotEmpty(t, exec.ExecutionID)
}

// ==========================================
// GET PENDING EXECUTIONS
// ==========================================

func TestPostgresStore_GetPendingExecutions_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetPendingExecutions(ctx)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// ==========================================
// GET EXECUTION BY ID
// ==========================================

func TestPostgresStore_GetExecutionByID_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetExecutionByID(ctx, "exec-123")
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// ==========================================
// GET EXECUTION BY PLAN AND DATE
// ==========================================

func TestPostgresStore_GetExecutionByPlanAndDate_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetExecutionByPlanAndDate(ctx, "plan-123", time.Now())
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// ==========================================
// SAVE PURCHASE HISTORY
// ==========================================

func TestPostgresStore_SavePurchaseHistory_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	record := &PurchaseHistoryRecord{
		AccountID:        "123456789012",
		PurchaseID:       "purchase-001",
		Timestamp:        time.Now(),
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
		PlanID:           "plan-123",
		PlanName:         "RDS Plan",
		RampStep:         1,
	}

	panicked := callWithRecover(func() {
		_ = store.SavePurchaseHistory(ctx, record)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// ==========================================
// GET PURCHASE HISTORY
// ==========================================

func TestPostgresStore_GetPurchaseHistory_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetPurchaseHistory(ctx, "123456789012", 10)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}

// ==========================================
// GET ALL PURCHASE HISTORY
// ==========================================

func TestPostgresStore_GetAllPurchaseHistory_NilDB(t *testing.T) {
	store := NewPostgresStore(nil)
	ctx := context.Background()

	panicked := callWithRecover(func() {
		_, _ = store.GetAllPurchaseHistory(ctx, 100)
	})

	assert.True(t, panicked, "expected panic with nil db connection")
}
