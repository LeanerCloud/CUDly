package config

// store_postgres_pgxmock_test.go — pgxmock tests for PostgresStore (the real struct).
// Each test injects pgxmock.PgxPoolIface directly into PostgresStore.db via the dbConn interface.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMock creates a pgxmock pool with regexp query matching.
func newMock(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool(pgxmock.QueryMatcherOption(pgxmock.QueryMatcherRegexp))
	require.NoError(t, err)
	return mock
}

// anyArgs returns n AnyArg() matchers.
func anyArgsCfg(n int) []interface{} {
	args := make([]interface{}, n)
	for i := range args {
		args[i] = pgxmock.AnyArg()
	}
	return args
}

// storeWith creates a real PostgresStore backed by mock.
func storeWith(mock pgxmock.PgxPoolIface) *PostgresStore {
	return &PostgresStore{db: mock}
}

func f64Ptr(f float64) *float64 { return &f }

// ─── GetGlobalConfig ──────────────────────────────────────────────────────────

func TestPGXMock_GetGlobalConfig_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	cols := []string{
		"enabled_providers", "notification_email", "approval_required",
		"default_term", "default_payment", "default_coverage", "default_ramp_schedule",
		"ri_exchange_enabled", "ri_exchange_mode", "ri_exchange_utilization_threshold",
		"ri_exchange_max_per_exchange_usd", "ri_exchange_max_daily_usd", "ri_exchange_lookback_days",
		"auto_collect", "collection_schedule", "notification_days_before",
	}
	rows := pgxmock.NewRows(cols).AddRow(
		[]string{"aws"}, strPtr("ops@example.com"), true,
		3, "no-upfront", 70.0, RampImmediate,
		true, "manual", 95.0,
		0.0, 0.0, 30,
		true, "daily", 3,
	)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	cfg, err := store.GetGlobalConfig(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"aws"}, cfg.EnabledProviders)
	require.NotNil(t, cfg.NotificationEmail)
	assert.Equal(t, "ops@example.com", *cfg.NotificationEmail)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_GetGlobalConfig_ScanError(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("db error"))

	_, err := store.GetGlobalConfig(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get global config")
}

// ─── GetServiceConfig ─────────────────────────────────────────────────────────

func TestPGXMock_GetServiceConfig_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	cols := []string{
		"provider", "service", "enabled", "term", "payment", "coverage", "ramp_schedule",
		"include_engines", "exclude_engines", "include_regions", "exclude_regions",
		"include_types", "exclude_types",
	}
	rows := pgxmock.NewRows(cols).AddRow(
		"aws", "ec2", true, 1, "no-upfront", 80.0, RampImmediate,
		[]string{"mysql"}, []string{}, []string{"us-east-1"}, []string{}, []string{}, []string{},
	)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnRows(rows)

	cfg, err := store.GetServiceConfig(ctx, "aws", "ec2")
	require.NoError(t, err)
	assert.Equal(t, "aws", cfg.Provider)
	assert.Equal(t, "ec2", cfg.Service)
	assert.Equal(t, []string{"mysql"}, cfg.IncludeEngines)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── ListServiceConfigs ───────────────────────────────────────────────────────

func TestPGXMock_ListServiceConfigs_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	cols := []string{
		"provider", "service", "enabled", "term", "payment", "coverage", "ramp_schedule",
		"include_engines", "exclude_engines", "include_regions", "exclude_regions",
		"include_types", "exclude_types",
	}
	rows := pgxmock.NewRows(cols).
		AddRow("aws", "ec2", true, 1, "no-upfront", 80.0, RampImmediate,
			[]string{}, []string{}, []string{}, []string{}, []string{}, []string{}).
		AddRow("aws", "rds", true, 3, "all-upfront", 70.0, RampImmediate,
			[]string{}, []string{}, []string{}, []string{}, []string{}, []string{})
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	cfgs, err := store.ListServiceConfigs(ctx)
	require.NoError(t, err)
	assert.Len(t, cfgs, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_ListServiceConfigs_Error(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("query error"))

	_, err := store.ListServiceConfigs(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list service configs")
}

// ─── GetPurchasePlan ──────────────────────────────────────────────────────────

func TestPGXMock_GetPurchasePlan_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	svcJSON, _ := json.Marshal(map[string]ServiceConfig{})
	rampJSON, _ := json.Marshal(RampSchedule{})
	cols := []string{
		"id", "name", "enabled", "auto_purchase", "notification_days_before",
		"services", "ramp_schedule", "created_at", "updated_at",
		"next_execution_date", "last_execution_date", "last_notification_sent",
	}
	rows := pgxmock.NewRows(cols).AddRow(
		"plan-id", "My Plan", true, false, 3,
		svcJSON, rampJSON, now, now,
		sql.NullTime{Valid: false}, sql.NullTime{Valid: false}, sql.NullTime{Valid: false},
	)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	plan, err := store.GetPurchasePlan(ctx, "plan-id")
	require.NoError(t, err)
	assert.Equal(t, "plan-id", plan.ID)
	assert.Equal(t, "My Plan", plan.Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_GetPurchasePlan_WithAllTimestamps(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	svcJSON, _ := json.Marshal(map[string]ServiceConfig{})
	rampJSON, _ := json.Marshal(RampSchedule{})
	cols := []string{
		"id", "name", "enabled", "auto_purchase", "notification_days_before",
		"services", "ramp_schedule", "created_at", "updated_at",
		"next_execution_date", "last_execution_date", "last_notification_sent",
	}
	rows := pgxmock.NewRows(cols).AddRow(
		"plan-id", "My Plan", true, false, 3,
		svcJSON, rampJSON, now, now,
		sql.NullTime{Valid: true, Time: now},
		sql.NullTime{Valid: true, Time: now},
		sql.NullTime{Valid: true, Time: now},
	)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	plan, err := store.GetPurchasePlan(ctx, "plan-id")
	require.NoError(t, err)
	require.NotNil(t, plan.NextExecutionDate)
	require.NotNil(t, plan.LastExecutionDate)
	require.NotNil(t, plan.LastNotificationSent)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── ListPurchasePlans ────────────────────────────────────────────────────────

func TestPGXMock_ListPurchasePlans_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	svcJSON, _ := json.Marshal(map[string]ServiceConfig{})
	rampJSON, _ := json.Marshal(RampSchedule{})
	cols := []string{
		"id", "name", "enabled", "auto_purchase", "notification_days_before",
		"services", "ramp_schedule", "created_at", "updated_at",
		"next_execution_date", "last_execution_date", "last_notification_sent",
	}
	rows := pgxmock.NewRows(cols).
		AddRow("p1", "Plan 1", true, false, 3, svcJSON, rampJSON, now, now,
			sql.NullTime{}, sql.NullTime{}, sql.NullTime{}).
		AddRow("p2", "Plan 2", false, true, 7, svcJSON, rampJSON, now, now,
			sql.NullTime{}, sql.NullTime{}, sql.NullTime{})
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	plans, err := store.ListPurchasePlans(ctx)
	require.NoError(t, err)
	assert.Len(t, plans, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_ListPurchasePlans_Error(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("db error"))

	_, err := store.ListPurchasePlans(ctx)
	require.Error(t, err)
}

// ─── UpdatePurchasePlan ───────────────────────────────────────────────────────

func TestPGXMock_UpdatePurchasePlan_NotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	plan := &PurchasePlan{ID: "missing-id", Services: map[string]ServiceConfig{}, RampSchedule: RampSchedule{}}
	mock.ExpectExec("UPDATE").WithArgs(anyArgsCfg(11)...).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err := store.UpdatePurchasePlan(ctx, plan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ─── DeletePurchasePlan ───────────────────────────────────────────────────────

func TestPGXMock_DeletePurchasePlan_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectExec("DELETE").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	err := store.DeletePurchasePlan(ctx, "plan-id")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_DeletePurchasePlan_NotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectExec("DELETE").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	err := store.DeletePurchasePlan(ctx, "plan-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ─── queryExecutions (via GetExecutionByID, GetExecutionByPlanAndDate) ────────

func TestPGXMock_GetExecutionByID_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	recsJSON, _ := json.Marshal([]RecommendationRecord{})
	now := time.Now().Truncate(time.Second)
	cols := []string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
		"cloud_account_id",
	}
	rows := pgxmock.NewRows(cols).AddRow(
		"plan-1", "exec-1", "pending", 1, now,
		sql.NullTime{}, "tok-123", recsJSON,
		100.0, 200.0, sql.NullTime{}, "", sql.NullTime{},
		nil,
	)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	exec, err := store.GetExecutionByID(ctx, "exec-1")
	require.NoError(t, err)
	assert.Equal(t, "exec-1", exec.ExecutionID)
	assert.Equal(t, "tok-123", exec.ApprovalToken)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_GetExecutionByID_WithTimestamps(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	recsJSON, _ := json.Marshal([]RecommendationRecord{})
	now := time.Now().Truncate(time.Second)
	future := now.Add(24 * time.Hour)
	cols := []string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
		"cloud_account_id",
	}
	rows := pgxmock.NewRows(cols).AddRow(
		"plan-1", "exec-2", "completed", 1, now,
		sql.NullTime{Valid: true, Time: now}, "tok", recsJSON,
		100.0, 200.0, sql.NullTime{Valid: true, Time: now}, "some error",
		sql.NullTime{Valid: true, Time: future},
		nil,
	)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	exec, err := store.GetExecutionByID(ctx, "exec-2")
	require.NoError(t, err)
	require.NotNil(t, exec.NotificationSent)
	require.NotNil(t, exec.CompletedAt)
	assert.True(t, exec.TTL > 0)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── queryPurchaseHistory (via GetPurchaseHistory) ────────────────────────────

func TestPGXMock_GetPurchaseHistory_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	cols := []string{
		"account_id", "purchase_id", "timestamp", "provider", "service", "region",
		"resource_type", "count", "term", "payment", "upfront_cost", "monthly_cost",
		"estimated_savings", "plan_id", "plan_name", "ramp_step", "cloud_account_id",
	}
	rows := pgxmock.NewRows(cols).
		AddRow("acc-1", "pur-1", now, "aws", "ec2", "us-east-1",
			"m5.large", 2, 1, "no-upfront", 100.0, 50.0, 200.0,
			sql.NullString{Valid: true, String: "plan-1"},
			sql.NullString{Valid: true, String: "My Plan"},
			1, sql.NullString{Valid: true, String: "cloud-acct-1"}).
		AddRow("acc-1", "pur-2", now, "aws", "rds", "us-west-2",
			"db.t3.medium", 1, 3, "all-upfront", 200.0, 0.0, 100.0,
			sql.NullString{}, sql.NullString{}, 0, sql.NullString{})
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnRows(rows)

	records, err := store.GetPurchaseHistory(ctx, "acc-1", 10)
	require.NoError(t, err)
	assert.Len(t, records, 2)
	assert.Equal(t, "plan-1", records[0].PlanID)
	assert.Equal(t, "", records[1].PlanID) // null → empty
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── GetRIExchangeRecord / queryRIExchangeRecords ─────────────────────────────

func riExchangeRow(now time.Time) []interface{} {
	return []interface{}{
		"ri-id", "acc-1", "exch-1", "us-east-1",
		[]string{"ri-1", "ri-2"}, "m5.large", 2, "offering-1",
		"m5.xlarge", 2, "100.00",
		"pending",
		sql.NullString{Valid: true, String: "tok-123"},
		sql.NullString{},
		"manual",
		now, now, sql.NullTime{}, sql.NullTime{},
	}
}

var riExchangeCols = []string{
	"id", "account_id", "exchange_id", "region", "source_ri_ids",
	"source_instance_type", "source_count", "target_offering_id",
	"target_instance_type", "target_count", "payment_due",
	"status", "approval_token", "error", "mode",
	"created_at", "updated_at", "completed_at", "expires_at",
}

func TestPGXMock_GetRIExchangeRecord_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(riExchangeCols).AddRow(riExchangeRow(now)...)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	rec, err := store.GetRIExchangeRecord(ctx, "ri-id")
	require.NoError(t, err)
	assert.Equal(t, "ri-id", rec.ID)
	assert.Equal(t, "tok-123", rec.ApprovalToken)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_GetRIExchangeRecord_WithTimestamps(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	row := []interface{}{
		"ri-id", "acc-1", "exch-1", "us-east-1",
		[]string{"ri-1"}, "m5.large", 2, "offering-1",
		"m5.xlarge", 2, "100.00",
		"completed",
		sql.NullString{},
		sql.NullString{Valid: true, String: "some error"},
		"auto",
		now, now, sql.NullTime{Valid: true, Time: now}, sql.NullTime{Valid: true, Time: now},
	}
	rows := pgxmock.NewRows(riExchangeCols).AddRow(row...)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	rec, err := store.GetRIExchangeRecord(ctx, "ri-id")
	require.NoError(t, err)
	assert.NotNil(t, rec.CompletedAt)
	assert.NotNil(t, rec.ExpiresAt)
	assert.Equal(t, "some error", rec.Error)
}

func TestPGXMock_GetRIExchangeRecordByToken_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(riExchangeCols).AddRow(riExchangeRow(now)...)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	rec, err := store.GetRIExchangeRecordByToken(ctx, "tok-123")
	require.NoError(t, err)
	assert.Equal(t, "ri-id", rec.ID)
}

func TestPGXMock_GetRIExchangeHistory_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(riExchangeCols).
		AddRow(riExchangeRow(now)...).
		AddRow(riExchangeRow(now)...)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnRows(rows)

	records, err := store.GetRIExchangeHistory(ctx, now.Add(-24*time.Hour), 10)
	require.NoError(t, err)
	assert.Len(t, records, 2)
}

// ─── TransitionRIExchangeStatus ───────────────────────────────────────────────

func TestPGXMock_TransitionRIExchangeStatus_WrongStatus(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// UPDATE returns empty (status mismatch) → triggers diagnostic SELECT
	emptyRows := pgxmock.NewRows(riExchangeCols)
	mock.ExpectQuery("UPDATE").WithArgs(anyArgsCfg(3)...).WillReturnRows(emptyRows)

	// Diagnostic query returns current status
	diagRows := pgxmock.NewRows([]string{"status", "expired"}).AddRow("completed", false)
	mock.ExpectQuery("SELECT status").WithArgs(pgxmock.AnyArg()).WillReturnRows(diagRows)

	_, err := store.TransitionRIExchangeStatus(ctx, "ri-id", "pending", "processing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected status")
}

func TestPGXMock_TransitionRIExchangeStatus_RecordNotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// UPDATE returns empty (not found) → triggers diagnostic SELECT
	emptyRows := pgxmock.NewRows(riExchangeCols)
	mock.ExpectQuery("UPDATE").WithArgs(anyArgsCfg(3)...).WillReturnRows(emptyRows)

	// Diagnostic query returns ErrNoRows (record not found)
	mock.ExpectQuery("SELECT status").WithArgs(pgxmock.AnyArg()).WillReturnError(errNoRows())

	_, err := store.TransitionRIExchangeStatus(ctx, "ri-id", "pending", "processing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestPGXMock_TransitionRIExchangeStatus_Expired(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// UPDATE returns empty (expired) → triggers diagnostic SELECT
	emptyRows := pgxmock.NewRows(riExchangeCols)
	mock.ExpectQuery("UPDATE").WithArgs(anyArgsCfg(3)...).WillReturnRows(emptyRows)

	// Diagnostic query: record exists but expired
	diagRows := pgxmock.NewRows([]string{"status", "expired"}).AddRow("pending", true)
	mock.ExpectQuery("SELECT status").WithArgs(pgxmock.AnyArg()).WillReturnRows(diagRows)

	_, err := store.TransitionRIExchangeStatus(ctx, "ri-id", "pending", "processing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestPGXMock_TransitionRIExchangeStatus_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)

	// UPDATE succeeds → returns the updated record
	updateRows := pgxmock.NewRows(riExchangeCols).AddRow(riExchangeRow(now)...)
	mock.ExpectQuery("UPDATE").WithArgs(anyArgsCfg(3)...).WillReturnRows(updateRows)

	rec, err := store.TransitionRIExchangeStatus(ctx, "ri-id", "pending", "processing")
	require.NoError(t, err)
	assert.Equal(t, "ri-id", rec.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── CompleteRIExchange / FailRIExchange ─────────────────────────────────────

func TestPGXMock_CompleteRIExchange_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectExec("UPDATE").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	err := store.CompleteRIExchange(ctx, "ri-id", "exch-id")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_CompleteRIExchange_NotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectExec("UPDATE").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err := store.CompleteRIExchange(ctx, "ri-id", "exch-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestPGXMock_FailRIExchange_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectExec("UPDATE").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	err := store.FailRIExchange(ctx, "ri-id", "something failed")
	require.NoError(t, err)
}

func TestPGXMock_FailRIExchange_NotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectExec("UPDATE").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err := store.FailRIExchange(ctx, "ri-id", "err")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ─── GetRIExchangeDailySpend ──────────────────────────────────────────────────

func TestPGXMock_GetRIExchangeDailySpend_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"total"}).AddRow("250.00")
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	total, err := store.GetRIExchangeDailySpend(ctx, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "250.00", total)
}

// ─── GetCloudAccount ──────────────────────────────────────────────────────────

var cloudAccountCols = []string{
	"id", "name", "description", "contact_email",
	"enabled", "provider", "external_id",
	"aws_auth_mode", "aws_role_arn", "aws_external_id", "aws_bastion_id",
	"aws_web_identity_token_file",
	"aws_is_org_root",
	"azure_subscription_id", "azure_tenant_id", "azure_client_id", "azure_auth_mode",
	"gcp_project_id", "gcp_client_email", "gcp_auth_mode", "gcp_wif_audience",
	"created_at", "updated_at", "created_by",
	"credentials_configured",
}

func cloudAccountRow(now time.Time) []interface{} {
	return []interface{}{
		"acct-id", "Test Account", "desc", "test@example.com",
		true, "aws", "123456789012",
		"access_keys", "", "", "", "", false,
		"", "", "", "",
		"", "", "", "",
		now, now, "admin",
		true,
	}
}

func TestPGXMock_GetCloudAccount_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(cloudAccountCols).AddRow(cloudAccountRow(now)...)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	acct, err := store.GetCloudAccount(ctx, "acct-id")
	require.NoError(t, err)
	require.NotNil(t, acct)
	assert.Equal(t, "acct-id", acct.ID)
	assert.True(t, acct.CredentialsConfigured)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_GetCloudAccount_NotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).
		WillReturnError(errNoRows())

	acct, err := store.GetCloudAccount(ctx, "missing")
	require.NoError(t, err)
	assert.Nil(t, acct)
}

func TestPGXMock_GetCloudAccount_Error(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).
		WillReturnError(errors.New("db error"))

	_, err := store.GetCloudAccount(ctx, "acct-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get cloud account")
}

// ─── UpdateCloudAccount ───────────────────────────────────────────────────────

func TestPGXMock_UpdateCloudAccount_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	acct := &CloudAccount{ID: "acct-id", Name: "Updated", Enabled: true, Provider: "aws"}
	mock.ExpectExec("UPDATE").WithArgs(anyArgsCfg(21)...).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	err := store.UpdateCloudAccount(ctx, acct)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_UpdateCloudAccount_NotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	acct := &CloudAccount{ID: "missing-id", Name: "X", Provider: "aws"}
	mock.ExpectExec("UPDATE").WithArgs(anyArgsCfg(21)...).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err := store.UpdateCloudAccount(ctx, acct)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ─── DeleteCloudAccount ───────────────────────────────────────────────────────

func TestPGXMock_DeleteCloudAccount_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE account_registrations").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectExec("DELETE FROM cloud_accounts").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	err := store.DeleteCloudAccount(ctx, "acct-id")
	require.NoError(t, err)
}

func TestPGXMock_DeleteCloudAccount_WithLinkedRegistration(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE account_registrations").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("DELETE FROM cloud_accounts").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	err := store.DeleteCloudAccount(ctx, "acct-id")
	require.NoError(t, err)
}

func TestPGXMock_DeleteCloudAccount_NotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE account_registrations").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectExec("DELETE FROM cloud_accounts").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectRollback()

	err := store.DeleteCloudAccount(ctx, "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ─── ListCloudAccounts ────────────────────────────────────────────────────────

func TestPGXMock_ListCloudAccounts_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(cloudAccountCols).
		AddRow(cloudAccountRow(now)...).
		AddRow(cloudAccountRow(now)...)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	accts, err := store.ListCloudAccounts(ctx, CloudAccountFilter{})
	require.NoError(t, err)
	assert.Len(t, accts, 2)
}

func TestPGXMock_ListCloudAccounts_WithProviderFilter(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	provider := "aws"
	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(cloudAccountCols).AddRow(cloudAccountRow(now)...)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	accts, err := store.ListCloudAccounts(ctx, CloudAccountFilter{Provider: &provider})
	require.NoError(t, err)
	assert.Len(t, accts, 1)
}

func TestPGXMock_ListCloudAccounts_WithSearchFilter(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(cloudAccountCols).AddRow(cloudAccountRow(now)...)
	// Search binds once and references the same $1 twice in the ILIKE clause.
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	accts, err := store.ListCloudAccounts(ctx, CloudAccountFilter{Search: "test"})
	require.NoError(t, err)
	assert.Len(t, accts, 1)
}

func TestPGXMock_ListCloudAccounts_WithAllFilters(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	provider := "aws"
	enabled := true
	bastionID := "bastion-1"
	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(cloudAccountCols).AddRow(cloudAccountRow(now)...)
	// 4 bound args: provider, enabled, search (bound once, referenced twice
	// in the ILIKE clause), and bastion_id.
	mock.ExpectQuery("SELECT").WithArgs(
		pgxmock.AnyArg(), pgxmock.AnyArg(),
		pgxmock.AnyArg(), pgxmock.AnyArg(),
	).WillReturnRows(rows)

	accts, err := store.ListCloudAccounts(ctx, CloudAccountFilter{
		Provider: &provider, Enabled: &enabled, Search: "test", BastionID: &bastionID,
	})
	require.NoError(t, err)
	assert.Len(t, accts, 1)
}

// ─── GetAccountCredential ─────────────────────────────────────────────────────

func TestPGXMock_GetAccountCredential_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"encrypted_blob"}).AddRow("encrypted-data")
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnRows(rows)

	blob, err := store.GetAccountCredential(ctx, "acct-1", "aws_access_keys")
	require.NoError(t, err)
	assert.Equal(t, "encrypted-data", blob)
}

func TestPGXMock_GetAccountCredential_NotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(errNoRows())

	blob, err := store.GetAccountCredential(ctx, "acct-1", "aws_access_keys")
	require.NoError(t, err)
	assert.Equal(t, "", blob)
}

func TestPGXMock_GetAccountCredential_Error(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(errors.New("db error"))

	_, err := store.GetAccountCredential(ctx, "acct-1", "aws_access_keys")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get account credential")
}

// ─── DeleteAccountCredentials ─────────────────────────────────────────────────

func TestPGXMock_DeleteAccountCredentials_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectExec("DELETE").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	err := store.DeleteAccountCredentials(ctx, "acct-1")
	require.NoError(t, err)
}

// ─── HasAccountCredentials ────────────────────────────────────────────────────

func TestPGXMock_HasAccountCredentials_True(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"exists"}).AddRow(true)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	exists, err := store.HasAccountCredentials(ctx, "acct-1")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestPGXMock_HasAccountCredentials_False(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"exists"}).AddRow(false)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	exists, err := store.HasAccountCredentials(ctx, "acct-1")
	require.NoError(t, err)
	assert.False(t, exists)
}

// ─── GetAccountServiceOverride ────────────────────────────────────────────────

var overrideCols = []string{
	"id", "account_id", "provider", "service",
	"enabled", "term", "payment", "coverage", "ramp_schedule",
	"include_engines", "exclude_engines", "include_regions", "exclude_regions",
	"include_types", "exclude_types",
	"created_at", "updated_at",
}

func TestPGXMock_GetAccountServiceOverride_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(overrideCols).AddRow(
		"ov-id", "acct-1", "aws", "ec2",
		boolPtr(true), intPtr(1), strPtr("no-upfront"), f64Ptr(80.0), strPtr(RampImmediate),
		[]string{}, []string{}, []string{}, []string{}, []string{}, []string{},
		now, now,
	)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(rows)

	o, err := store.GetAccountServiceOverride(ctx, "acct-1", "aws", "ec2")
	require.NoError(t, err)
	require.NotNil(t, o)
	assert.Equal(t, "ov-id", o.ID)
	assert.Equal(t, "acct-1", o.AccountID)
}

func TestPGXMock_GetAccountServiceOverride_NotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WithArgs(anyArgsCfg(3)...).WillReturnError(errNoRows())

	o, err := store.GetAccountServiceOverride(ctx, "acct-1", "aws", "ec2")
	require.NoError(t, err)
	assert.Nil(t, o)
}

func TestPGXMock_GetAccountServiceOverride_Error(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WithArgs(anyArgsCfg(3)...).WillReturnError(errors.New("db error"))

	_, err := store.GetAccountServiceOverride(ctx, "acct-1", "aws", "ec2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get service override")
}

// ─── ListAccountServiceOverrides ─────────────────────────────────────────────

func TestPGXMock_ListAccountServiceOverrides_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(overrideCols).
		AddRow(
			"ov-1", "acct-1", "aws", "ec2",
			boolPtr(true), intPtr(1), strPtr("no-upfront"), f64Ptr(80.0), strPtr(RampImmediate),
			[]string{}, []string{}, []string{}, []string{}, []string{}, []string{},
			now, now,
		).
		AddRow(
			"ov-2", "acct-1", "aws", "rds",
			boolPtr(false), intPtr(3), strPtr("all-upfront"), f64Ptr(70.0), strPtr(RampImmediate),
			[]string{"mysql"}, []string{}, []string{"us-east-1"}, []string{}, []string{}, []string{},
			now, now,
		)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	overrides, err := store.ListAccountServiceOverrides(ctx, "acct-1")
	require.NoError(t, err)
	assert.Len(t, overrides, 2)
	assert.Equal(t, []string{"mysql"}, overrides[1].IncludeEngines)
}

func TestPGXMock_ListAccountServiceOverrides_Error(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnError(errors.New("db error"))

	_, err := store.ListAccountServiceOverrides(ctx, "acct-1")
	require.Error(t, err)
}

// ─── SetPlanAccounts ──────────────────────────────────────────────────────────

func TestPGXMock_SetPlanAccounts_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM plan_accounts").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec("INSERT INTO plan_accounts").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO plan_accounts").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	err := store.SetPlanAccounts(ctx, "plan-1", []string{"acct-1", "acct-2"})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_SetPlanAccounts_Empty(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM plan_accounts").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectCommit()

	err := store.SetPlanAccounts(ctx, "plan-1", []string{})
	require.NoError(t, err)
}

func TestPGXMock_SetPlanAccounts_BeginError(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectBegin().WillReturnError(errors.New("conn error"))

	err := store.SetPlanAccounts(ctx, "plan-1", []string{"acct-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to begin transaction")
}

// ─── GetPlanAccounts ──────────────────────────────────────────────────────────

func TestPGXMock_GetPlanAccounts_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(cloudAccountCols).
		AddRow(cloudAccountRow(now)...).
		AddRow(cloudAccountRow(now)...)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	accts, err := store.GetPlanAccounts(ctx, "plan-1")
	require.NoError(t, err)
	assert.Len(t, accts, 2)
}

func TestPGXMock_GetPlanAccounts_Error(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnError(errors.New("db error"))

	_, err := store.GetPlanAccounts(ctx, "plan-1")
	require.Error(t, err)
}

// ─── GetStaleProcessingExchanges ─────────────────────────────────────────────

func TestPGXMock_GetStaleProcessingExchanges_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(riExchangeCols).AddRow(riExchangeRow(now)...)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	records, err := store.GetStaleProcessingExchanges(ctx, 30*time.Minute)
	require.NoError(t, err)
	assert.Len(t, records, 1)
}

// ─── CancelAllPendingExchanges ────────────────────────────────────────────────

func TestPGXMock_CancelAllPendingExchanges_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectExec("UPDATE").WillReturnResult(pgxmock.NewResult("UPDATE", 3))

	n, err := store.CancelAllPendingExchanges(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
}

// ─── CleanupOldExecutions ─────────────────────────────────────────────────────

func TestPGXMock_CleanupOldExecutions_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectExec("DELETE").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("DELETE", 5))

	n, err := store.CleanupOldExecutions(ctx, 90)
	require.NoError(t, err)
	assert.Equal(t, int64(5), n)
}

// ─── SavePurchaseHistory ──────────────────────────────────────────────────────

func TestPGXMock_SavePurchaseHistory_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO purchase_history").WithArgs(anyArgsCfg(17)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err := store.SavePurchaseHistory(ctx, &PurchaseHistoryRecord{
		AccountID:  "acc-1",
		PurchaseID: "pur-1",
		Timestamp:  time.Now(),
		Provider:   "aws",
		Service:    "ec2",
	})
	require.NoError(t, err)
}

// ─── errNoRows helper ────────────────────────────────────────────────────────

func errNoRows() error {
	return pgx.ErrNoRows
}
