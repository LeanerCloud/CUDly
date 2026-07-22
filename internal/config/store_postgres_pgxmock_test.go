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

	"github.com/LeanerCloud/CUDly/pkg/common"
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
		"grace_period_days",
		"recommendations_cache_stale_hours", "recommendations_lookback_days",
		"purchase_delay_hours",
		"laddering_enabled",
		"ladder_execution_enabled",
		"offering_class",
	}
	rows := pgxmock.NewRows(cols).AddRow(
		[]string{"aws"}, strPtr("ops@example.com"), true,
		3, "no-upfront", 70.0, RampImmediate,
		true, "manual", 95.0,
		0.0, 0.0, 30,
		true, "daily", 3,
		"{}",
		24, 7,
		0,
		false,
		false,
		"convertible",
	)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	cfg, err := store.GetGlobalConfig(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"aws"}, cfg.EnabledProviders)
	require.NotNil(t, cfg.NotificationEmail)
	assert.Equal(t, "ops@example.com", *cfg.NotificationEmail)
	assert.Equal(t, 24, cfg.RecommendationsCacheStaleHours)
	assert.Equal(t, 7, cfg.RecommendationsLookbackDays)
	assert.Equal(t, "convertible", cfg.OfferingClass)
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

// TestPGXMock_GetGlobalConfig_GracePeriodDays covers the new
// grace_period_days TEXT column: "{}" → nil/empty map + default 7
// per provider, a populated JSON → map round-trips faithfully,
// malformed JSON → error surfaced to caller (don't silently swallow
// a corrupt DB cell).
func TestPGXMock_GetGlobalConfig_GracePeriodDays(t *testing.T) {
	baseCols := []string{
		"enabled_providers", "notification_email", "approval_required",
		"default_term", "default_payment", "default_coverage", "default_ramp_schedule",
		"ri_exchange_enabled", "ri_exchange_mode", "ri_exchange_utilization_threshold",
		"ri_exchange_max_per_exchange_usd", "ri_exchange_max_daily_usd", "ri_exchange_lookback_days",
		"auto_collect", "collection_schedule", "notification_days_before",
		"grace_period_days",
		"recommendations_cache_stale_hours", "recommendations_lookback_days",
		"purchase_delay_hours",
		"laddering_enabled",
		"ladder_execution_enabled",
		"offering_class",
	}
	baseRow := func(graceJSON string) []any {
		return []any{
			[]string{}, (*string)(nil), true,
			3, "no-upfront", 70.0, RampImmediate,
			false, "manual", 95.0,
			0.0, 0.0, 30,
			true, "daily", 3,
			graceJSON,
			24, 7,
			0,
			false,
			false,
			"convertible",
		}
	}

	t.Run("empty json object", func(t *testing.T) {
		mock := newMock(t)
		store := storeWith(mock)
		mock.ExpectQuery("SELECT").WillReturnRows(
			pgxmock.NewRows(baseCols).AddRow(baseRow("{}")...),
		)
		cfg, err := store.GetGlobalConfig(context.Background())
		require.NoError(t, err)
		assert.Empty(t, cfg.GracePeriodDays)
		// GracePeriodFor returns the default for every provider.
		assert.Equal(t, DefaultGracePeriodDays, cfg.GracePeriodFor("aws"))
		assert.Equal(t, DefaultGracePeriodDays, cfg.GracePeriodFor("azure"))
		assert.Equal(t, 24, cfg.RecommendationsCacheStaleHours)
		assert.Equal(t, 7, cfg.RecommendationsLookbackDays)
	})

	t.Run("populated json round-trips", func(t *testing.T) {
		mock := newMock(t)
		store := storeWith(mock)
		mock.ExpectQuery("SELECT").WillReturnRows(
			pgxmock.NewRows(baseCols).AddRow(baseRow(`{"aws":7,"azure":0,"gcp":14}`)...),
		)
		cfg, err := store.GetGlobalConfig(context.Background())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"aws": 7, "azure": 0, "gcp": 14}, cfg.GracePeriodDays)
		// Explicit 0 preserved (feature disabled for azure).
		assert.Equal(t, 0, cfg.GracePeriodFor("azure"))
		assert.Equal(t, 14, cfg.GracePeriodFor("gcp"))
	})

	t.Run("malformed json surfaces error", func(t *testing.T) {
		mock := newMock(t)
		store := storeWith(mock)
		mock.ExpectQuery("SELECT").WillReturnRows(
			pgxmock.NewRows(baseCols).AddRow(baseRow(`not-json`)...),
		)
		_, err := store.GetGlobalConfig(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "grace_period_days")
	})
}

// globalConfigCols is the column list returned by the GetGlobalConfig SELECT,
// in scan order.
var globalConfigCols = []string{
	"enabled_providers", "notification_email", "approval_required",
	"default_term", "default_payment", "default_coverage", "default_ramp_schedule",
	"ri_exchange_enabled", "ri_exchange_mode", "ri_exchange_utilization_threshold",
	"ri_exchange_max_per_exchange_usd", "ri_exchange_max_daily_usd", "ri_exchange_lookback_days",
	"auto_collect", "collection_schedule", "notification_days_before",
	"grace_period_days",
	"recommendations_cache_stale_hours", "recommendations_lookback_days",
	"purchase_delay_hours",
	"laddering_enabled",
	"ladder_execution_enabled",
	"offering_class",
}

// TestPGXMock_UpdateGlobalConfigAtomic_LockedReadModifyWrite proves the F2
// lost-update fix: UpdateGlobalConfigAtomic performs the read and the write in
// ONE transaction, guarded by a transaction-scoped advisory lock, in strict
// order: BEGIN -> pg_advisory_xact_lock -> SELECT global_config -> UPSERT ->
// COMMIT. Because pgxmock enforces expectation ORDER, a passing run guarantees
// the read-modify-write is serialized under the lock rather than split across
// two unsynchronized statements (the original TOCTOU). The apply closure
// mutates only one field; the round-tripped result must keep every field the
// closure did not touch, i.e. a concurrent partial update cannot lose another's
// change once the lock serializes them.
func TestPGXMock_UpdateGlobalConfigAtomic_LockedReadModifyWrite(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// Existing row carries automation settings a partial PUT must not clobber.
	seeded := pgxmock.NewRows(globalConfigCols).AddRow(
		[]string{"aws"}, strPtr("ops@example.com"), true, // approval_required = true
		3, "all-upfront", 80.0, RampImmediate,
		true, "automatic", 95.0, // ri_exchange_enabled = true, mode = automatic
		1000.0, 5000.0, 30,
		true, "daily", 3,
		"{}",
		24, 7,
		48,
		false,         // laddering_enabled = false
		false,         // ladder_execution_enabled = false
		"convertible", // offering_class
	)

	// Strict order: the SELECT and the UPSERT must sit between the same
	// BEGIN/COMMIT, after the advisory lock.
	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("FROM global_config").WillReturnRows(seeded)
	mock.ExpectExec("INSERT INTO global_config").WithArgs(anyArgsCfg(23)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	// apply flips only laddering_enabled, exactly as the kill-switch partial
	// PUT does; everything else must be preserved from the seeded row.
	applied := false
	merged, err := store.UpdateGlobalConfigAtomic(ctx, func(existing *GlobalConfig) error {
		applied = true
		existing.LadderingEnabled = true
		return nil
	})
	require.NoError(t, err)
	require.True(t, applied, "apply closure must run against the locked-read config")
	require.NotNil(t, merged)

	// Applied change survives.
	assert.True(t, merged.LadderingEnabled)
	// Fields the closure did NOT touch are preserved (no lost update).
	assert.True(t, merged.ApprovalRequired, "approval_required must survive")
	assert.True(t, merged.RIExchangeEnabled, "ri_exchange_enabled must survive")
	assert.Equal(t, "automatic", merged.RIExchangeMode)
	assert.Equal(t, 1000.0, merged.RIExchangeMaxPerExchangeUSD)
	assert.Equal(t, 48, merged.PurchaseDelayHours)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_UpdateGlobalConfigAtomic_ApplyErrorRollsBack asserts that when the
// apply closure fails (e.g. validation), the transaction rolls back and no
// UPSERT is issued, and the closure's error propagates unchanged.
func TestPGXMock_UpdateGlobalConfigAtomic_ApplyErrorRollsBack(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	seeded := pgxmock.NewRows(globalConfigCols).AddRow(
		[]string{"aws"}, (*string)(nil), true,
		3, "all-upfront", 80.0, RampImmediate,
		false, "manual", 95.0,
		0.0, 0.0, 30,
		true, "daily", 3,
		"{}",
		24, 7,
		0,
		false,
		false,
		"convertible",
	)

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("FROM global_config").WillReturnRows(seeded)
	// No ExpectExec("INSERT...") — the UPSERT must not run.
	mock.ExpectRollback()

	sentinel := errors.New("validation failed")
	_, err := store.UpdateGlobalConfigAtomic(ctx, func(_ *GlobalConfig) error {
		return sentinel
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── GetServiceConfig ─────────────────────────────────────────────────────────

func TestPGXMock_GetServiceConfig_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	cols := []string{
		"provider", "service", "enabled", "term", "payment", "coverage", "ramp_schedule",
		"include_engines", "exclude_engines", "include_regions", "exclude_regions",
		"include_types", "exclude_types", "min_count",
	}
	rows := pgxmock.NewRows(cols).AddRow(
		"aws", "ec2", true, 1, "no-upfront", 80.0, RampImmediate,
		[]string{"mysql"}, []string{}, []string{"us-east-1"}, []string{}, []string{}, []string{}, 2,
	)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnRows(rows)

	cfg, err := store.GetServiceConfig(ctx, "aws", "ec2")
	require.NoError(t, err)
	assert.Equal(t, "aws", cfg.Provider)
	assert.Equal(t, "ec2", cfg.Service)
	assert.Equal(t, []string{"mysql"}, cfg.IncludeEngines)
	assert.Equal(t, 2, cfg.MinCount)
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
		"include_types", "exclude_types", "min_count",
	}
	rows := pgxmock.NewRows(cols).
		AddRow("aws", "ec2", true, 1, "no-upfront", 80.0, RampImmediate,
			[]string{}, []string{}, []string{}, []string{}, []string{}, []string{}, 0).
		AddRow("aws", "rds", true, 3, "all-upfront", 70.0, RampImmediate,
			[]string{}, []string{}, []string{}, []string{}, []string{}, []string{}, 0)
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
		"unassigned",
	}
	rows := pgxmock.NewRows(cols).
		AddRow("p1", "Plan 1", true, false, 3, svcJSON, rampJSON, now, now,
			sql.NullTime{}, sql.NullTime{}, sql.NullTime{}, false).
		AddRow("p2", "Plan 2", false, true, 7, svcJSON, rampJSON, now, now,
			sql.NullTime{}, sql.NullTime{}, sql.NullTime{}, false)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	plans, err := store.ListPurchasePlans(ctx, PurchasePlanFilter{})
	require.NoError(t, err)
	assert.Len(t, plans, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_ListPurchasePlans_Error(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("db error"))

	_, err := store.ListPurchasePlans(ctx, PurchasePlanFilter{})
	require.Error(t, err)
}

// TestPGXMock_ListPurchasePlans_UnassignedIncluded verifies that when an
// account filter is active, plans with zero plan_accounts rows are returned
// alongside the matched-account plans, flagged with Unassigned=true.
//
// This is the regression guard for issue #973: before the fix the INNER JOIN
// on plan_accounts silently excluded zero-account legacy plans from every
// account-filtered response.
func TestPGXMock_ListPurchasePlans_UnassignedIncluded(t *testing.T) {
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
		"unassigned",
	}
	// The query returns two rows: one assigned (unassigned=false) and one
	// legacy zero-account plan (unassigned=true).
	rows := pgxmock.NewRows(cols).
		AddRow("assigned-id", "Assigned Plan", true, false, 3, svcJSON, rampJSON, now, now,
			sql.NullTime{}, sql.NullTime{}, sql.NullTime{}, false).
		AddRow("legacy-id", "Legacy Plan", true, false, 3, svcJSON, rampJSON, now, now,
			sql.NullTime{}, sql.NullTime{}, sql.NullTime{}, true)
	mock.ExpectQuery("SELECT").WithArgs("acc-uuid").WillReturnRows(rows)

	plans, err := store.ListPurchasePlans(ctx, PurchasePlanFilter{AccountIDs: []string{"acc-uuid"}})
	require.NoError(t, err)
	require.Len(t, plans, 2)

	// The assigned plan must NOT be flagged unassigned.
	assert.Equal(t, "assigned-id", plans[0].ID)
	assert.False(t, plans[0].Unassigned, "assigned plan should have Unassigned=false")

	// The legacy zero-account plan must be flagged unassigned.
	assert.Equal(t, "legacy-id", plans[1].ID)
	assert.True(t, plans[1].Unassigned, "zero-account plan should have Unassigned=true")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── UpdatePurchasePlan ───────────────────────────────────────────────────────

func TestPGXMock_UpdatePurchasePlan_NotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	plan := &PurchasePlan{ID: "missing-id", Services: map[string]ServiceConfig{}, RampSchedule: RampSchedule{}}
	// UpdatePurchasePlan now wraps the UPDATE in a WithTx so it can be
	// composed with other writes (e.g. createPlannedPurchases bundling
	// per-row execution inserts with the plan's next_execution_date
	// bump). The pgxmock script needs the matching Begin / Commit
	// frame; the inner Exec returns 0 rows-affected, which the store
	// surfaces as a "not found" error after the rollback.
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE").WithArgs(anyArgsCfg(11)...).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

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
		"cloud_account_id", "source", "approved_by", "cancelled_by", "capacity_percent",
		"created_by_user_id", "retry_execution_id", "retry_attempt_n",
		"approval_token_expires_at",
		"executed_by_user_id", "executed_at", "pre_approval_skip_reason",
		"idempotency_key",
		"scheduled_execution_at",
	}
	rows := pgxmock.NewRows(cols).AddRow(
		"plan-1", "exec-1", "pending", 1, now,
		sql.NullTime{}, "tok-123", recsJSON,
		100.0, 200.0, sql.NullTime{}, "", sql.NullTime{},
		nil, "", nil, nil, 100,
		nil, nil, 0,
		sql.NullTime{},
		nil, sql.NullTime{}, nil,
		nil,            // idempotency_key (NULL: legacy-row scan path, migration 000066)
		sql.NullTime{}, // scheduled_execution_at (NULL: not on the pre-fire delay path)
	)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	exec, err := store.GetExecutionByID(ctx, "exec-1")
	require.NoError(t, err)
	assert.Equal(t, "exec-1", exec.ExecutionID)
	assert.Equal(t, "tok-123", exec.ApprovalToken)
	// Retry-linkage scan-order regression guard (CR #168 nit). NULL FK +
	// default 0 attempt count are the legacy-row case after migration
	// 000042 — a column reorder upstream would surface as a wrong-type
	// scan into the wrong field, which assertions on these specific
	// columns will catch.
	assert.Nil(t, exec.RetryExecutionID)
	assert.Equal(t, 0, exec.RetryAttemptN)
	// idempotency_key scan-outcome guard (CR): the NULL path (legacy rows
	// before migration 000066) must leave IdempotencyKey empty so derivation
	// falls back to ExecutionID (issue #1012).
	assert.Equal(t, "", exec.IdempotencyKey)
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
		"cloud_account_id", "source", "approved_by", "cancelled_by", "capacity_percent",
		"created_by_user_id", "retry_execution_id", "retry_attempt_n",
		"approval_token_expires_at",
		"executed_by_user_id", "executed_at", "pre_approval_skip_reason",
		"idempotency_key",
		"scheduled_execution_at",
	}
	successorID := "exec-3"
	rows := pgxmock.NewRows(cols).AddRow(
		"plan-1", "exec-2", "completed", 1, now,
		sql.NullTime{Valid: true, Time: now}, "tok", recsJSON,
		100.0, 200.0, sql.NullTime{Valid: true, Time: now}, "some error",
		sql.NullTime{Valid: true, Time: future},
		nil, "cudly-web", nil, nil, 100,
		// Populated retry-linkage fields (CR #168 nit) — NON-zero
		// values exercise the scan path for both new columns.
		nil, &successorID, 2,
		sql.NullTime{},
		nil, sql.NullTime{}, nil,
		"idem-key-exec-2", // idempotency_key non-NULL: exercises the scan path
		sql.NullTime{},    // scheduled_execution_at (NULL: not on the pre-fire delay path)
	)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	exec, err := store.GetExecutionByID(ctx, "exec-2")
	require.NoError(t, err)
	require.NotNil(t, exec.NotificationSent)
	require.NotNil(t, exec.CompletedAt)
	assert.True(t, exec.TTL > 0)
	// Retry-linkage scan-order regression guard (CR #168 nit). The
	// populated case — non-NULL successor pointer + non-zero attempt
	// count — exercises the scan path for both new columns; a column
	// reorder upstream would scan into the wrong destination and the
	// assertions below would fail.
	require.NotNil(t, exec.RetryExecutionID)
	assert.Equal(t, "exec-3", *exec.RetryExecutionID)
	assert.Equal(t, 2, exec.RetryAttemptN)
	// idempotency_key scan-outcome guard (CR): the non-NULL path must scan
	// the stored key into IdempotencyKey verbatim.
	assert.Equal(t, "idem-key-exec-2", exec.IdempotencyKey)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_GetPlannedExecutions_ProjectsAllScanColumns guards against the
// SELECT projection in GetPlannedExecutions drifting out of sync with the
// scanExecutionRows Scan target. When migration 000066 added idempotency_key
// (and earlier migrations added executed_by_user_id, executed_at,
// pre_approval_skip_reason) every execution-reading SELECT must project them,
// or scanExecutionRows fails at runtime with "failed to scan execution" on the
// planned-purchase list path (handler_purchases.go GetPlannedExecutions).
//
// The mock uses regexp query matching, so ExpectQuery requires the issued SQL
// to contain idempotency_key; with the column missing from the projection the
// query does not match and GetPlannedExecutions returns an error, which is what
// this test asserts against. It fails on the pre-fix projection and passes once
// the four trailing columns are added.
func TestPGXMock_GetPlannedExecutions_ProjectsAllScanColumns(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	recsJSON, _ := json.Marshal([]RecommendationRecord{})
	now := time.Now().Truncate(time.Second)
	cols := []string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
		"cloud_account_id", "source", "approved_by", "cancelled_by", "capacity_percent",
		"created_by_user_id", "retry_execution_id", "retry_attempt_n",
		"approval_token_expires_at",
		"executed_by_user_id", "executed_at", "pre_approval_skip_reason",
		"idempotency_key", "scheduled_execution_at",
	}
	schedAt := now.Add(2 * time.Hour)
	rows := pgxmock.NewRows(cols).AddRow(
		"plan-1", "exec-1", "pending", 1, now,
		sql.NullTime{}, "tok-123", recsJSON,
		100.0, 200.0, sql.NullTime{}, "", sql.NullTime{},
		nil, "", nil, nil, 100,
		nil, nil, 0,
		sql.NullTime{},
		nil, sql.NullTime{}, nil,
		"idem-key-planned",
		sql.NullTime{}, // scheduled_execution_at (NULL: not on the pre-fire delay path)
	).AddRow(
		"plan-1", "exec-2", "pending", 1, now,
		sql.NullTime{}, "tok-456", recsJSON,
		100.0, 200.0, sql.NullTime{}, "", sql.NullTime{},
		nil, "", nil, nil, 100,
		nil, nil, 0,
		sql.NullTime{},
		nil, sql.NullTime{}, nil,
		"idem-key-delayed",
		sql.NullTime{Time: schedAt, Valid: true}, // scheduled_execution_at populated (pre-fire delay path)
	)
	// Regexp matcher: only matches if the issued SELECT projects both
	// idempotency_key and scheduled_execution_at. The alternation forces both
	// names to appear; a projection missing either column fails to match, the
	// mock returns no rows, and the test catches the column-count drift.
	mock.ExpectQuery(`idempotency_key.*scheduled_execution_at|scheduled_execution_at.*idempotency_key`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(rows)

	execs, err := store.GetPlannedExecutions(ctx, []string{"pending"}, 10)
	require.NoError(t, err)
	require.Len(t, execs, 2)
	assert.Equal(t, "idem-key-planned", execs[0].IdempotencyKey)
	// NULL scheduled_execution_at must deserialise as nil (*time.Time), not a zero
	// value; applyNullTimesToExecution only sets the pointer when Valid is true.
	assert.Nil(t, execs[0].ScheduledExecutionAt, "NULL scheduled_execution_at must be nil, not zero time")
	// Non-NULL scheduled_execution_at must round-trip into the pointer field. This
	// is the direct regression guard for the fix: with the column absent from the
	// SELECT projection the value never reaches ScheduledExecutionAt and every
	// delayed execution reads back as unscheduled.
	require.NotNil(t, execs[1].ScheduledExecutionAt, "populated scheduled_execution_at must round-trip, not be dropped")
	assert.Equal(t, schedAt, *execs[1].ScheduledExecutionAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_GetExecutionByID_ProjectsCoalescedCancelledBy guards the #1277 /
// #1453 read-side follow-up: CancelExecutionAtomic and
// CancelScheduledExecutionAtomic write the canceling actor to the NEW
// canceled_by column, but migration 000089's expand-contract contract states
// every SELECT must read back COALESCE(canceled_by, cancelled_by) so a row
// written by either the old or the new column is attributed correctly. Before
// this fix GetExecutionByID selected the bare legacy cancelled_by, so an
// actor written only to canceled_by (the atomic-cancel paths) scanned back as
// NULL and the History page lost the attribution.
//
// The mock uses regexp query matching, so ExpectQuery requires the issued
// SELECT to contain the COALESCE expression; a bare `cancelled_by` projection
// (the pre-fix query) does not match, the mock returns no rows, and
// GetExecutionByID surfaces ErrNotFound instead of the expected execution --
// that is how this test fails against the pre-fix code and passes once the
// projection is fixed.
func TestPGXMock_GetExecutionByID_ProjectsCoalescedCancelledBy(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	recsJSON, _ := json.Marshal([]RecommendationRecord{})
	now := time.Now().Truncate(time.Second)
	cols := []string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
		"cloud_account_id", "source", "approved_by", "cancelled_by", "capacity_percent",
		"created_by_user_id", "retry_execution_id", "retry_attempt_n",
		"approval_token_expires_at",
		"executed_by_user_id", "executed_at", "pre_approval_skip_reason",
		"idempotency_key",
		"scheduled_execution_at",
	}
	// Simulates the exact row shape the atomic-cancel paths produce: the
	// actor lives only in canceled_by (legacy cancelled_by is NULL). What
	// Postgres would actually return for COALESCE(canceled_by, cancelled_by)
	// is the canceled_by value, so the mocked "cancelled_by" scan slot below
	// carries that same value to stand in for the server-side COALESCE result.
	rows := pgxmock.NewRows(cols).AddRow(
		"plan-1", "exec-1", "canceled", 1, now,
		sql.NullTime{}, "tok-123", recsJSON,
		100.0, 200.0, sql.NullTime{}, "", sql.NullTime{},
		nil, "", nil, strPtr("cancelling-actor@example.com"), 100,
		nil, nil, 0,
		sql.NullTime{},
		nil, sql.NullTime{}, nil,
		nil,
		sql.NullTime{},
	)
	mock.ExpectQuery(`COALESCE\(canceled_by,\s*cancelled_by\)`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(rows)

	exec, err := store.GetExecutionByID(ctx, "exec-1")
	require.NoError(t, err)
	require.NotNil(t, exec.CancelledBy, "actor written to canceled_by must round-trip via the COALESCE read projection")
	assert.Equal(t, "cancelling-actor@example.com", *exec.CancelledBy)
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
		// revocation columns (issue #290)
		"revocation_window_closes_at", "revoked_at", "revoked_via", "support_case_id",
		// marketplace columns (issue #292)
		"offering_class", "listing_id", "listing_state",
	}
	rows := pgxmock.NewRows(cols).
		AddRow("acc-1", "pur-1", now, "aws", "ec2", "us-east-1",
			"m5.large", 2, 1, "no-upfront", 100.0, 50.0, 200.0,
			sql.NullString{Valid: true, String: "plan-1"},
			sql.NullString{Valid: true, String: "My Plan"},
			1, sql.NullString{Valid: true, String: "cloud-acct-1"},
			// revocation columns (issue #290)
			nil, nil, sql.NullString{}, sql.NullString{},
			// marketplace columns (issue #292)
			sql.NullString{Valid: true, String: "standard"},
			sql.NullString{}, sql.NullString{}).
		AddRow("acc-1", "pur-2", now, "aws", "rds", "us-west-2",
			"db.t3.medium", 1, 3, "all-upfront", 200.0, 0.0, 100.0,
			sql.NullString{}, sql.NullString{}, 0, sql.NullString{},
			nil, nil, sql.NullString{}, sql.NullString{},
			sql.NullString{}, sql.NullString{}, sql.NullString{})
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnRows(rows)

	records, err := store.GetPurchaseHistory(ctx, "acc-1", 10)
	require.NoError(t, err)
	assert.Len(t, records, 2)
	assert.Equal(t, "plan-1", records[0].PlanID)
	assert.Equal(t, "", records[1].PlanID) // null → empty
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_GetPurchaseHistoryByPurchaseID_Success asserts the DISTINCT
// 25-column scan order used by GetPurchaseHistoryByPurchaseID. Unlike the
// 24-column GetPurchaseHistory reader it adds revocation_in_flight (a plain
// bool, NOT a nullable) at position 22, between support_case_id and the
// marketplace columns (issue #290 Finding #6, migration 000072). A regression
// here -- e.g. dropping the column or scanning it through the nullables struct
// -- would shift every marketplace column by one and silently mis-read
// offering_class / listing_id / listing_state.
func TestPGXMock_GetPurchaseHistoryByPurchaseID_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	cols := []string{
		"account_id", "purchase_id", "timestamp", "provider", "service", "region",
		"resource_type", "count", "term", "payment", "upfront_cost", "monthly_cost",
		"estimated_savings", "plan_id", "plan_name", "ramp_step", "cloud_account_id",
		// revocation columns (issue #290)
		"revocation_window_closes_at", "revoked_at", "revoked_via", "support_case_id",
		// partial-success reconciliation bool at position 22 (issue #290 Finding #6)
		"revocation_in_flight",
		// marketplace columns (issue #292)
		"offering_class", "listing_id", "listing_state",
	}
	// monthly_cost scans straight into the *float64 r.MonthlyCost (not through
	// the nullables struct), so the mock value must be a *float64.
	monthly := 50.0
	rows := pgxmock.NewRows(cols).
		AddRow("acc-1", "pur-1", now, "aws", "ec2", "us-east-1",
			"m5.large", 2, 1, "no-upfront", 100.0, &monthly, 200.0,
			sql.NullString{Valid: true, String: "plan-1"},
			sql.NullString{Valid: true, String: "My Plan"},
			1, sql.NullString{Valid: true, String: "cloud-acct-1"},
			// revocation columns (issue #290)
			nil, nil, sql.NullString{}, sql.NullString{},
			// revocation_in_flight bool at position 22
			true,
			// marketplace columns (issue #292)
			sql.NullString{Valid: true, String: "standard"},
			sql.NullString{Valid: true, String: "listing-1"},
			sql.NullString{Valid: true, String: "active"})
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	record, err := store.GetPurchaseHistoryByPurchaseID(ctx, "pur-1")
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, "pur-1", record.PurchaseID)
	assert.Equal(t, "plan-1", record.PlanID)
	require.NotNil(t, record.MonthlyCost)
	assert.Equal(t, 50.0, *record.MonthlyCost)
	assert.True(t, record.RevocationInFlight)
	// Marketplace columns must land in their own fields, not shifted by the
	// extra bool.
	assert.Equal(t, "standard", record.OfferingClass)
	assert.Equal(t, "listing-1", record.ListingID)
	assert.Equal(t, "active", record.ListingState)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_GetPurchaseHistoryByPurchaseID_NotFound returns (nil, nil) when
// no row matches, so the revoke / marketplace handlers can distinguish "absent"
// from a scan/query error.
func TestPGXMock_GetPurchaseHistoryByPurchaseID_NotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"account_id"}) // no rows added
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	record, err := store.GetPurchaseHistoryByPurchaseID(ctx, "missing")
	require.NoError(t, err)
	assert.Nil(t, record)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── GetPurchaseHistoryFiltered (issue #701) ─────────────────────────────────

// purchaseHistoryCols lists the SELECT columns for purchase_history rows in
// the order GetPurchaseHistoryFiltered scans them. Keep in sync with
// queryPurchaseHistory in store_postgres.go (issue #290 added the 4 revocation
// columns at positions 18-21; issue #292 added the 3 marketplace columns at
// positions 22-24).
var purchaseHistoryCols = []string{
	"account_id", "purchase_id", "timestamp", "provider", "service", "region",
	"resource_type", "count", "term", "payment", "upfront_cost", "monthly_cost",
	"estimated_savings", "plan_id", "plan_name", "ramp_step", "cloud_account_id",
	"revocation_window_closes_at", "revoked_at", "revoked_via", "support_case_id",
	"offering_class", "listing_id", "listing_state",
}

// purchaseHistoryRow builds a single AddRow tuple matching purchaseHistoryCols.
func purchaseHistoryRow(now time.Time, provider, acct string) []interface{} {
	return []interface{}{
		acct, "pur-1", now, provider, "ec2", "us-east-1",
		"m5.large", 1, 1, "no-upfront", 100.0, 50.0, 200.0,
		sql.NullString{}, sql.NullString{}, 0, sql.NullString{},
		// revocation columns (issue #290): all null for non-revoked rows
		nil, nil, sql.NullString{}, sql.NullString{},
		// marketplace columns (issue #292): all null for unlisted rows
		sql.NullString{}, sql.NullString{}, sql.NullString{},
	}
}

// TestPGXMock_GetPurchaseHistoryFiltered_AllFilters asserts the SQL emitted
// when every filter is set: WHERE provider = $1 AND (cloud_account_id =
// ANY($2) OR (provider = $3 AND account_id = ANY($4))) AND timestamp >= $5 AND
// timestamp <= $6, ORDER BY timestamp DESC, LIMIT $7. The account predicate is
// dual-column with a provider-scoped external-id half so a row carrying only one
// identifier is still matched without leaking across providers (issue
// #701/#498/#866 + provider-coupling).
func TestPGXMock_GetPurchaseHistoryFiltered_AllFilters(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	start := now.Add(-24 * time.Hour)
	end := now

	rows := pgxmock.NewRows(purchaseHistoryCols).AddRow(purchaseHistoryRow(now, "aws", "acct-1")...)
	mock.ExpectQuery(
		`SELECT account_id, purchase_id, timestamp, provider, service, region.*FROM purchase_history WHERE provider = \$1 AND \(cloud_account_id = ANY\(\$2\) OR \(provider = \$3 AND account_id = ANY\(\$4\)\)\) AND timestamp >= \$5 AND timestamp <= \$6.*ORDER BY timestamp DESC.*LIMIT \$7`,
	).WithArgs("aws", []string{"acct-uuid-1"}, "aws", []string{"111122223333"}, start, end, 50).WillReturnRows(rows)

	records, err := store.GetPurchaseHistoryFiltered(ctx, PurchaseHistoryFilter{
		Provider:              "aws",
		AccountIDs:            []string{"acct-uuid-1"},
		ExternalIDsByProvider: map[string][]string{"aws": {"111122223333"}},
		Start:                 &start,
		End:                   &end,
		Limit:                 50,
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "aws", records[0].Provider)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_GetPurchaseHistoryFiltered_ExternalIDOnly is the keystone
// regression test for issue #701/#498/#866: a UUID-known account whose
// purchase_history rows carry cloud_account_id IS NULL + a populated
// account_id (external number) MUST still be returned when its UUID is
// requested. The handler resolves the UUID to its external id and passes BOTH;
// the dual-column predicate then matches via account_id. Before the fix the
// store filtered cloud_account_id only, so the row was silently dropped and the
// user saw "No purchase history yet" for that account.
func TestPGXMock_GetPurchaseHistoryFiltered_ExternalIDOnly(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	// The matched row has account_id="999988887777" and cloud_account_id NULL
	// (purchaseHistoryRow already writes a NULL cloud_account_id).
	rows := pgxmock.NewRows(purchaseHistoryCols).AddRow(purchaseHistoryRow(now, "aws", "999988887777")...)
	mock.ExpectQuery(
		`FROM purchase_history WHERE \(cloud_account_id = ANY\(\$1\) OR \(provider = \$2 AND account_id = ANY\(\$3\)\)\).*ORDER BY timestamp DESC.*LIMIT \$4`,
	).WithArgs([]string{"acct-uuid-B"}, "aws", []string{"999988887777"}, 100).WillReturnRows(rows)

	records, err := store.GetPurchaseHistoryFiltered(ctx, PurchaseHistoryFilter{
		AccountIDs:            []string{"acct-uuid-B"},
		ExternalIDsByProvider: map[string][]string{"aws": {"999988887777"}},
		Limit:                 100,
	})
	require.NoError(t, err)
	require.Len(t, records, 1, "external-id-only row must be returned when its account is requested")
	assert.Equal(t, "999988887777", records[0].AccountID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_GetPurchaseHistoryFiltered_MultiProviderExternalIDs asserts that
// when external ids are grouped across two providers, the predicate emits one
// provider-gated OR branch per provider (sorted) so an external id only matches
// rows of its own provider. This is the SQL-side guarantee that a reused
// external number (aws/123 vs azure/123) cannot leak across providers (issue
// #956 CR finding #1). Providers are emitted in sorted order: aws before azure.
func TestPGXMock_GetPurchaseHistoryFiltered_MultiProviderExternalIDs(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(purchaseHistoryCols).AddRow(purchaseHistoryRow(now, "aws", "123")...)
	mock.ExpectQuery(
		`FROM purchase_history WHERE \(\(provider = \$1 AND account_id = ANY\(\$2\)\) OR \(provider = \$3 AND account_id = ANY\(\$4\)\)\).*ORDER BY timestamp DESC.*LIMIT \$5`,
	).WithArgs("aws", []string{"123"}, "azure", []string{"123"}, 100).WillReturnRows(rows)

	records, err := store.GetPurchaseHistoryFiltered(ctx, PurchaseHistoryFilter{
		ExternalIDsByProvider: map[string][]string{"aws": {"123"}, "azure": {"123"}},
		Limit:                 100,
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_GetPurchaseHistoryFiltered_UUIDOnly asserts a UUID-only account
// filter (no resolvable external id) emits just the cloud_account_id half of
// the predicate, bound to the UUID set.
func TestPGXMock_GetPurchaseHistoryFiltered_UUIDOnly(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(purchaseHistoryCols).AddRow(purchaseHistoryRow(now, "aws", "acct-1")...)
	mock.ExpectQuery(
		`FROM purchase_history WHERE \(cloud_account_id = ANY\(\$1\)\).*ORDER BY timestamp DESC.*LIMIT \$2`,
	).WithArgs([]string{"acct-uuid-1"}, 100).WillReturnRows(rows)

	records, err := store.GetPurchaseHistoryFiltered(ctx, PurchaseHistoryFilter{
		AccountIDs: []string{"acct-uuid-1"},
		Limit:      100,
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_GetPurchaseHistoryFiltered_NoFilters asserts that an
// all-defaults call (empty filter) emits a WHERE-less query identical in shape
// to GetAllPurchaseHistory. This proves the filtered variant degrades
// gracefully when no fields are set.
func TestPGXMock_GetPurchaseHistoryFiltered_NoFilters(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(purchaseHistoryCols).AddRow(purchaseHistoryRow(now, "aws", "acct-1")...)
	// No WHERE clause; the only argument bound is the LIMIT.
	mock.ExpectQuery(`SELECT.*FROM purchase_history\s+ORDER BY timestamp DESC.*LIMIT \$1`).
		WithArgs(100).WillReturnRows(rows)

	records, err := store.GetPurchaseHistoryFiltered(ctx, PurchaseHistoryFilter{Limit: 100})
	require.NoError(t, err)
	assert.Len(t, records, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── GetActivePurchaseHistory (issue #1140) ──────────────────────────────────

// TestPGXMock_GetActivePurchaseHistory_Unscoped asserts the emitted SQL pushes
// the active filter into the WHERE clause with NO LIMIT (the `$` anchor after
// ORDER BY proves no trailing LIMIT clause), so the result is bounded by the
// number of live commitments and a newest-first row cap can never silently
// drop the oldest still-active 1y/3y commitments (issue #1140). The pinned
// `>= $1` expiry comparison is inclusive so a commitment expiring exactly at
// asOf stays active, matching the API layer's isActiveCommitment. It also pins
// the full 21-column SELECT including the issue-#290 revocation columns:
// before this fix the query selected only 17 columns while
// queryPurchaseHistory scans 21 destinations, so every call failed at Scan.
// The `revoked_at IS NULL` clause (defect #2 fix) ensures revoked commitments
// are never returned by this path.
func TestPGXMock_GetActivePurchaseHistory_Unscoped(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(purchaseHistoryCols).AddRow(purchaseHistoryRow(now, "aws", "acct-1")...)
	mock.ExpectQuery(
		`SELECT account_id, purchase_id, .*revocation_window_closes_at, revoked_at, revoked_via, support_case_id, offering_class, listing_id, listing_state FROM purchase_history WHERE term > 0 AND timestamp \+ make_interval\(hours => term \* 8760\) >= \$1 AND revoked_at IS NULL ORDER BY timestamp DESC$`,
	).WithArgs(now).WillReturnRows(rows)

	records, err := store.GetActivePurchaseHistory(ctx, now, nil, nil)
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_GetActivePurchaseHistory_AccountScoped asserts the dual-column
// account predicate (same shape as GetPurchaseHistoryFiltered, issues
// #701/#498/#866) composes with the active filter, again with no LIMIT, so the
// dashboard KPI path and the inventory endpoints get the complete active set
// for the selected account scope (issue #1140). The `revoked_at IS NULL` clause
// (defect #2 fix) is also present so revoked commitments never appear even for
// account-scoped queries.
func TestPGXMock_GetActivePurchaseHistory_AccountScoped(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(purchaseHistoryCols).AddRow(purchaseHistoryRow(now, "aws", "111122223333")...)
	mock.ExpectQuery(
		`FROM purchase_history WHERE term > 0 AND timestamp \+ make_interval\(hours => term \* 8760\) >= \$1 AND revoked_at IS NULL AND \(cloud_account_id = ANY\(\$2\) OR \(provider = \$3 AND account_id = ANY\(\$4\)\)\) ORDER BY timestamp DESC$`,
	).WithArgs(now, []string{"acct-uuid-1"}, "aws", []string{"111122223333"}).WillReturnRows(rows)

	records, err := store.GetActivePurchaseHistory(ctx, now,
		[]string{"acct-uuid-1"}, map[string][]string{"aws": {"111122223333"}})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "111122223333", records[0].AccountID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_GetPurchaseHistoryFiltered_PartialFilters asserts that
// supplying only a subset of filters (here: provider + start, no account ids,
// no end) emits exactly two AND clauses and binds the right positional
// arguments. Guards against an off-by-one in the placeholder counter.
func TestPGXMock_GetPurchaseHistoryFiltered_PartialFilters(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	start := now.Add(-7 * 24 * time.Hour)

	rows := pgxmock.NewRows(purchaseHistoryCols)
	mock.ExpectQuery(
		`FROM purchase_history WHERE provider = \$1 AND timestamp >= \$2.*ORDER BY timestamp DESC.*LIMIT \$3`,
	).WithArgs("azure", start, 25).WillReturnRows(rows)

	_, err := store.GetPurchaseHistoryFiltered(ctx, PurchaseHistoryFilter{
		Provider: "azure",
		Start:    &start,
		Limit:    25,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_GetPurchaseHistoryFiltered_LimitClamp asserts the store-side
// limit clamp: an out-of-range limit (negative or above MaxListLimit) is
// normalised before the query runs so a malicious or buggy caller can't
// exfiltrate the entire table by passing limit=2_000_000.
func TestPGXMock_GetPurchaseHistoryFiltered_LimitClamp(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	rows := pgxmock.NewRows(purchaseHistoryCols)
	// Negative limit must be replaced with DefaultListLimit.
	mock.ExpectQuery(`FROM purchase_history\s+ORDER BY timestamp DESC.*LIMIT \$1`).
		WithArgs(DefaultListLimit).WillReturnRows(rows)

	_, err := store.GetPurchaseHistoryFiltered(ctx, PurchaseHistoryFilter{Limit: -5})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())

	// Over-MaxListLimit must be clamped to MaxListLimit.
	mock2 := newMock(t)
	store2 := storeWith(mock2)
	rows2 := pgxmock.NewRows(purchaseHistoryCols)
	mock2.ExpectQuery(`FROM purchase_history\s+ORDER BY timestamp DESC.*LIMIT \$1`).
		WithArgs(MaxListLimit).WillReturnRows(rows2)

	_, err = store2.GetPurchaseHistoryFiltered(ctx, PurchaseHistoryFilter{Limit: MaxListLimit + 1})
	require.NoError(t, err)
	assert.NoError(t, mock2.ExpectationsWereMet())
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
		sql.NullString{}, sql.NullString{}, // created_by_user_id, approved_by
		sql.NullString{}, // ladder_run_id (NULL for standalone)
	}
}

var riExchangeCols = []string{
	"id", "account_id", "exchange_id", "region", "source_ri_ids",
	"source_instance_type", "source_count", "target_offering_id",
	"target_instance_type", "target_count", "payment_due",
	"status", "approval_token", "error", "mode",
	"created_at", "updated_at", "completed_at", "expires_at",
	"created_by_user_id", "approved_by", "ladder_run_id",
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
		sql.NullString{}, sql.NullString{}, // created_by_user_id, approved_by
		sql.NullString{}, // ladder_run_id
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
	mock.ExpectQuery("UPDATE").WithArgs(anyArgsCfg(4)...).WillReturnRows(emptyRows)

	// Diagnostic query returns current status
	diagRows := pgxmock.NewRows([]string{"status", "expired"}).AddRow("completed", false)
	mock.ExpectQuery("SELECT status").WithArgs(pgxmock.AnyArg()).WillReturnRows(diagRows)

	_, err := store.TransitionRIExchangeStatus(ctx, "ri-id", "pending", "processing", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected status")
}

func TestPGXMock_TransitionRIExchangeStatus_RecordNotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// UPDATE returns empty (not found) → triggers diagnostic SELECT
	emptyRows := pgxmock.NewRows(riExchangeCols)
	mock.ExpectQuery("UPDATE").WithArgs(anyArgsCfg(4)...).WillReturnRows(emptyRows)

	// Diagnostic query returns ErrNoRows (record not found)
	mock.ExpectQuery("SELECT status").WithArgs(pgxmock.AnyArg()).WillReturnError(errNoRows())

	_, err := store.TransitionRIExchangeStatus(ctx, "ri-id", "pending", "processing", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestPGXMock_TransitionRIExchangeStatus_Expired(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// UPDATE returns empty (expired) → triggers diagnostic SELECT
	emptyRows := pgxmock.NewRows(riExchangeCols)
	mock.ExpectQuery("UPDATE").WithArgs(anyArgsCfg(4)...).WillReturnRows(emptyRows)

	// Diagnostic query: record exists but expired
	diagRows := pgxmock.NewRows([]string{"status", "expired"}).AddRow("pending", true)
	mock.ExpectQuery("SELECT status").WithArgs(pgxmock.AnyArg()).WillReturnRows(diagRows)

	_, err := store.TransitionRIExchangeStatus(ctx, "ri-id", "pending", "processing", nil)
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
	mock.ExpectQuery("UPDATE").WithArgs(anyArgsCfg(4)...).WillReturnRows(updateRows)

	rec, err := store.TransitionRIExchangeStatus(ctx, "ri-id", "pending", "processing", nil)
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

// ─── CompleteRIExchangeWithPayment ───────────────────────────────────────────

func TestPGXMock_CompleteRIExchangeWithPayment_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// Three args: id, exchangeID, acceptedPaymentDue
	mock.ExpectExec("UPDATE").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	err := store.CompleteRIExchangeWithPayment(ctx, "ri-id", "exch-id", "42.000000")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_CompleteRIExchangeWithPayment_NotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectExec("UPDATE").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err := store.CompleteRIExchangeWithPayment(ctx, "ri-id", "exch-id", "42.000000")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ─── GetRIExchangeDailySpend (M5: includes processing rows) ──────────────────

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

// TestPGXMock_GetRIExchangeDailySpend_IncludesProcessingStatus verifies that
// the query includes 'processing' rows in addition to 'completed' ones (M5
// fix) by asserting the SQL contains the expected status filter text.
// Because pgxmock matches the full query string, we confirm the SQL sent to
// the DB driver contains "processing".
func TestPGXMock_GetRIExchangeDailySpend_IncludesProcessingStatus(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// Match any SELECT that goes to the DB; the important assertion is below.
	rows := pgxmock.NewRows([]string{"total"}).AddRow("0")
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg()).WillReturnRows(rows)

	_, err := store.GetRIExchangeDailySpend(ctx, time.Now())
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
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

// ─── GetCloudAccountByExternalID (issue #604) ─────────────────────────────────

func TestPGXMock_GetCloudAccountByExternalID_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(cloudAccountCols).AddRow(cloudAccountRow(now)...)
	mock.ExpectQuery("SELECT").WithArgs("aws", "123456789012").WillReturnRows(rows)

	acct, err := store.GetCloudAccountByExternalID(ctx, "aws", "123456789012")
	require.NoError(t, err)
	require.NotNil(t, acct)
	assert.Equal(t, "acct-id", acct.ID)
	assert.Equal(t, "123456789012", acct.ExternalID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_GetCloudAccountByExternalID_NotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").WithArgs("aws", "missing").
		WillReturnError(errNoRows())

	acct, err := store.GetCloudAccountByExternalID(ctx, "aws", "missing")
	require.NoError(t, err, "not-found must return (nil, nil), not an error")
	assert.Nil(t, acct)
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
	servicesJSON, err := json.Marshal(map[string]ServiceConfig{
		"aws/ec2": {Provider: "aws", Service: "ec2"},
	})
	require.NoError(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT services").WithArgs("plan-1").WillReturnRows(
		pgxmock.NewRows([]string{"services"}).AddRow(servicesJSON),
	)
	mock.ExpectQuery("SELECT name, provider").WithArgs("acct-1").WillReturnRows(
		pgxmock.NewRows([]string{"name", "provider"}).AddRow("Account 1", "aws"),
	)
	mock.ExpectQuery("SELECT name, provider").WithArgs("acct-2").WillReturnRows(
		pgxmock.NewRows([]string{"name", "provider"}).AddRow("Account 2", "aws"),
	)
	mock.ExpectExec("DELETE FROM plan_accounts").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec("INSERT INTO plan_accounts").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO plan_accounts").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	err = store.SetPlanAccounts(ctx, "plan-1", []string{"acct-1", "acct-2"})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_SetPlanAccounts_ProviderMismatchRollsBackBeforeDelete(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()
	servicesJSON, err := json.Marshal(map[string]ServiceConfig{
		"aws/ec2": {Provider: "aws", Service: "ec2"},
	})
	require.NoError(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT services").WithArgs("plan-1").WillReturnRows(
		pgxmock.NewRows([]string{"services"}).AddRow(servicesJSON),
	)
	mock.ExpectQuery("SELECT name, provider").WithArgs("acct-1").WillReturnRows(
		pgxmock.NewRows([]string{"name", "provider"}).AddRow("Azure Account", "azure"),
	)
	mock.ExpectRollback()

	err = store.SetPlanAccounts(ctx, "plan-1", []string{"acct-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan provider mismatch")
	assert.Contains(t, err.Error(), "Azure Account")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_SetPlanAccounts_Empty(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()
	servicesJSON, err := json.Marshal(map[string]ServiceConfig{
		"aws/ec2": {Provider: "aws", Service: "ec2"},
	})
	require.NoError(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT services").WithArgs("plan-1").WillReturnRows(
		pgxmock.NewRows([]string{"services"}).AddRow(servicesJSON),
	)
	mock.ExpectExec("DELETE FROM plan_accounts").WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectCommit()

	err = store.SetPlanAccounts(ctx, "plan-1", []string{})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_SetPlanAccounts_EmptyMissingPlanReturnsNotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT services").WithArgs("missing-plan").WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	err := store.SetPlanAccounts(ctx, "missing-plan", []string{})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Contains(t, err.Error(), "missing-plan")
	assert.NoError(t, mock.ExpectationsWereMet())
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

	// Now parameterized: $1 = status value.
	mock.ExpectExec("UPDATE").WithArgs(pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 3))

	n, err := store.CancelAllPendingExchanges(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
}

// ─── CancelPendingExchangesByOrigin ──────────────────────────────────────────

// The regex pins each test to its own WHERE clause so a branch mix-up (both
// branches emitting the same SQL) fails the test. "ladder_run_id IS NULL" is
// NOT a substring of "ladder_run_id IS NOT NULL", so the two matchers are
// mutually exclusive.
func TestPGXMock_CancelPendingExchangesByOrigin_Standalone(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// Standalone origin must target WHERE ladder_run_id IS NULL only.
	// Now parameterized: $1 = status value.
	mock.ExpectExec("ladder_run_id IS NULL").WithArgs(pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 2))

	n, err := store.CancelPendingExchangesByOrigin(ctx, common.ExchangeOriginStandalone)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_CancelPendingExchangesByOrigin_Ladder(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// Ladder origin must target WHERE ladder_run_id IS NOT NULL only.
	// Now parameterized: $1 = status value.
	mock.ExpectExec("ladder_run_id IS NOT NULL").WithArgs(pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	n, err := store.CancelPendingExchangesByOrigin(ctx, common.ExchangeOriginLadder)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_CancelPendingExchangesByOrigin_UnknownOrigin_FailsLoud(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// An unknown origin must fail at the boundary WITHOUT issuing any query
	// (no ExpectExec registered → ExpectationsWereMet stays satisfied).
	n, err := store.CancelPendingExchangesByOrigin(ctx, common.ExchangeOrigin("bogus"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown exchange origin")
	assert.Equal(t, int64(0), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── SaveRIExchangeRecord ─────────────────────────────────────────────────────

func TestPGXMock_SaveRIExchangeRecord_InsertColumnAlignment(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// 21 columns/placeholders: original 20 + ladder_run_id ($21, issue #1348).
	// A column/placeholder count drift makes WithArgs(anyArgsCfg(21)) fail.
	mock.ExpectExec("INSERT INTO ri_exchange_history").WithArgs(anyArgsCfg(21)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	ladderRunID := "run-123"
	err := store.SaveRIExchangeRecord(ctx, &RIExchangeRecord{
		AccountID:   "acc-1",
		Region:      "us-east-1",
		Status:      "pending",
		Mode:        "manual",
		LadderRunID: &ladderRunID,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
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

	// 20 columns: original 18 + revocation_window_closes_at (issue #290)
	// + offering_class (issue #292).
	mock.ExpectExec("INSERT INTO purchase_history").WithArgs(anyArgsCfg(20)...).
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

// ─── CountPendingExecutionsForAccount / ListPendingExecutionIDsForAccount ───
// Regression coverage for the issue #606 deleteAccount preflight. The handler
// uses these to convert what would otherwise be a raw ON DELETE RESTRICT
// FK violation (post-migration 000053) into a structured 409 response.

func TestPGXMock_CountPendingExecutionsForAccount_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"count"}).AddRow(4)
	mock.ExpectQuery("SELECT COUNT").WithArgs("acct-1").WillReturnRows(rows)

	n, err := store.CountPendingExecutionsForAccount(ctx, "acct-1")
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_CountPendingExecutionsForAccount_Zero(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"count"}).AddRow(0)
	mock.ExpectQuery("SELECT COUNT").WithArgs("acct-1").WillReturnRows(rows)

	n, err := store.CountPendingExecutionsForAccount(ctx, "acct-1")
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_ListPendingExecutionIDsForAccount_Success(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"execution_id"}).
		AddRow("exec-1").
		AddRow("exec-2")
	mock.ExpectQuery("SELECT execution_id FROM purchase_executions").
		WithArgs("acct-1").
		WillReturnRows(rows)

	ids, err := store.ListPendingExecutionIDsForAccount(ctx, "acct-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"exec-1", "exec-2"}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_ListPendingExecutionIDsForAccount_Empty(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	rows := pgxmock.NewRows([]string{"execution_id"})
	mock.ExpectQuery("SELECT execution_id FROM purchase_executions").
		WithArgs("acct-1").
		WillReturnRows(rows)

	ids, err := store.ListPendingExecutionIDsForAccount(ctx, "acct-1")
	require.NoError(t, err)
	assert.Empty(t, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ─── GetPlannedExecutions ────────────────────────────────────────────────────

// TestPGXMock_GetPlannedExecutions_UsesASCOrdering is the regression guard for
// the planned-purchases list truncation bug. GetExecutionsByStatuses uses
// ORDER BY scheduled_date DESC + LIMIT, which drops the SOONEST rows when the
// pending/notified/paused set exceeds MaxListLimit, exactly the rows the UI
// must surface. GetPlannedExecutions must order ASC at the DB level so LIMIT
// keeps the soonest rows. pgxmock's regexp matcher fails the test if the SQL
// uses DESC, regresses to the GetExecutionsByStatuses query, or drops the
// stable secondary sort.
func TestPGXMock_GetPlannedExecutions_UsesASCOrdering(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	soon := now.Add(2 * time.Hour)
	later := now.Add(48 * time.Hour)
	rows := pgxmock.NewRows(stuckExecCols()).
		AddRow(stuckExecRow("exec-soon", "pending", soon)...).
		AddRow(stuckExecRow("exec-later", "paused", later)...)
	// Strict regex anchors:
	//   1. ASC ordering on scheduled_date (NOT DESC),
	//   2. NULLS LAST guard so a future-relaxed schema can't hide rows,
	//   3. id ASC secondary sort for stable ordering at equal scheduled_date,
	//   4. LIMIT $2 so callers can bound result size.
	// If a future refactor regresses to DESC or drops the secondary sort, the
	// expectation goes unmet and the test fails.
	mock.ExpectQuery(`(?s)SELECT.*FROM purchase_executions.*status = ANY\(\$1\).*ORDER BY scheduled_date ASC NULLS LAST, id ASC.*LIMIT \$2`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(rows)

	execs, err := store.GetPlannedExecutions(ctx, []string{"pending", "notified", "paused"}, 100)
	require.NoError(t, err)
	require.Len(t, execs, 2)
	assert.Equal(t, "exec-soon", execs[0].ExecutionID)
	assert.Equal(t, "exec-later", execs[1].ExecutionID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_GetPlannedExecutions_EmptyStatuses guards the short-circuit:
// nil/empty status list returns nil with no SQL roundtrip (pgxmock fails on
// any unexpected query since none is registered here).
func TestPGXMock_GetPlannedExecutions_EmptyStatuses(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	execs, err := store.GetPlannedExecutions(ctx, nil, 100)
	require.NoError(t, err)
	assert.Nil(t, execs)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_GetPlannedExecutions_LimitClamping asserts limit <= 0 falls
// back to DefaultListLimit and limit > MaxListLimit is clamped to MaxListLimit.
// Mirrors GetExecutionsByStatuses' clamping so callers can pass user-supplied
// values without sanitizing upstream.
func TestPGXMock_GetPlannedExecutions_LimitClamping(t *testing.T) {
	t.Run("negative falls back to DefaultListLimit", func(t *testing.T) {
		mock := newMock(t)
		store := storeWith(mock)
		ctx := context.Background()

		rows := pgxmock.NewRows(stuckExecCols())
		mock.ExpectQuery(`ORDER BY scheduled_date ASC`).
			WithArgs(pgxmock.AnyArg(), DefaultListLimit).
			WillReturnRows(rows)

		_, err := store.GetPlannedExecutions(ctx, []string{"pending"}, -1)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("over-max clamped to MaxListLimit", func(t *testing.T) {
		mock := newMock(t)
		store := storeWith(mock)
		ctx := context.Background()

		rows := pgxmock.NewRows(stuckExecCols())
		mock.ExpectQuery(`ORDER BY scheduled_date ASC`).
			WithArgs(pgxmock.AnyArg(), MaxListLimit).
			WillReturnRows(rows)

		_, err := store.GetPlannedExecutions(ctx, []string{"pending"}, MaxListLimit+5000)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// ─── ListStuckExecutions ─────────────────────────────────────────────────────

// stuckExecRow builds a pgxmock row that matches the queryExecutions scan
// order. Mirrors the inline row construction in TestPGXMock_GetExecutionByID_*
// but factored out for the reaper sweep tests below which need 3 rows.
func stuckExecRow(execID, status string, scheduled time.Time) []any {
	recsJSON, _ := json.Marshal([]RecommendationRecord{})
	return []any{
		"plan-1", execID, status, 1, scheduled,
		sql.NullTime{}, "tok-" + execID, recsJSON,
		100.0, 200.0, sql.NullTime{}, "", sql.NullTime{},
		nil, "cudly-web", nil, nil, 100,
		nil, nil, 0,
		sql.NullTime{},
		nil, sql.NullTime{}, nil,
		nil,            // idempotency_key (NULL: legacy-row scan path, migration 000066)
		sql.NullTime{}, // scheduled_execution_at (NULL: not on the pre-fire delay path)
	}
}

func stuckExecCols() []string {
	return []string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
		"cloud_account_id", "source", "approved_by", "cancelled_by", "capacity_percent",
		"created_by_user_id", "retry_execution_id", "retry_attempt_n",
		"approval_token_expires_at",
		"executed_by_user_id", "executed_at", "pre_approval_skip_reason",
		"idempotency_key",
		"scheduled_execution_at",
	}
}

func TestPGXMock_ListStuckExecutions_ReturnsMultiple(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	rows := pgxmock.NewRows(stuckExecCols()).
		AddRow(stuckExecRow("exec-1", "approved", now)...).
		AddRow(stuckExecRow("exec-2", "running", now)...).
		AddRow(stuckExecRow("exec-3", "approved", now)...)
	mock.ExpectQuery("SELECT.*FROM purchase_executions.*status = ANY.*updated_at < NOW").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(rows)

	execs, err := store.ListStuckExecutions(ctx, []string{"approved", "running"}, 10*time.Minute)
	require.NoError(t, err)
	assert.Len(t, execs, 3)
	assert.Equal(t, "exec-1", execs[0].ExecutionID)
	assert.Equal(t, "approved", execs[0].Status)
	assert.Equal(t, "running", execs[1].Status)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_ListStuckExecutions_EmptyStatuses(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// No statuses → caller wants nothing — short-circuit returns nil with no
	// SQL roundtrip. pgxmock will fail if any expectation is unmet (we
	// register none) so this also guards against an accidental query.
	execs, err := store.ListStuckExecutions(ctx, nil, 10*time.Minute)
	require.NoError(t, err)
	assert.Nil(t, execs)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPGXMock_ListStuckExecutions_QueryError(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectQuery("SELECT").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(errors.New("db down"))

	_, err := store.ListStuckExecutions(ctx, []string{"approved"}, 10*time.Minute)
	require.Error(t, err)
}

// ─── ListAccountRegistrations — LIKE wildcard escaping ───────────────────────

// registrationCols returns the column slice that mirrors registrationColumns().
func registrationCols() []string {
	return []string{
		"id", "reference_token", "status",
		"provider", "external_id", "account_name", "contact_email", "description",
		"source_provider",
		"aws_role_arn", "aws_auth_mode", "aws_external_id",
		"azure_subscription_id", "azure_tenant_id", "azure_client_id", "azure_auth_mode",
		"gcp_project_id", "gcp_client_email", "gcp_auth_mode", "gcp_wif_audience",
		"reg_credential_type", "reg_credential_payload",
		"rejection_reason", "cloud_account_id", "reviewed_by", "reviewed_at",
		"created_at", "updated_at",
	}
}

// minimalRegRow returns a single pgxmock row with only the required non-null
// columns filled in and the rest as nil (matching the sql.NullString scan path).
func minimalRegRow(id, accountName, contactEmail string) []interface{} {
	now := time.Now()
	return []interface{}{
		id, "tok-" + id, "pending",
		"aws", "ext-" + id, accountName, contactEmail, nil,
		nil,
		nil, nil, nil,
		nil, nil, nil, nil,
		nil, nil, nil, nil,
		nil, nil,
		nil, nil, nil, sql.NullTime{},
		now, now,
	}
}

// TestPGXMock_ListAccountRegistrations_SearchEscapesPercent verifies that a
// filter.Search value beginning with "%" is forwarded to the DB as "\%..."
// (escaped) so it matches the literal substring, not every row.
func TestPGXMock_ListAccountRegistrations_SearchEscapesPercent(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// The raw input contains a leading %; after escaping it must become \%foo.
	rawSearch := "%foo"
	escapedArg := `\%foo`

	rows := pgxmock.NewRows(registrationCols()).
		AddRow(minimalRegRow("reg-1", "%foo Corp", "%foo@example.com")...)
	// The query must contain ESCAPE to prove the clause was updated.
	mock.ExpectQuery(`ESCAPE`).
		WithArgs("%" + escapedArg + "%").
		WillReturnRows(rows)

	filter := AccountRegistrationFilter{Search: rawSearch}
	regs, err := store.ListAccountRegistrations(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, regs, 1, "expected exactly the literal-match row")
	assert.Equal(t, "reg-1", regs[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_ListAccountRegistrations_SearchEscapesUnderscore verifies that
// "_" in filter.Search is sent as "\_" so it is not treated as the SQL
// single-character wildcard.
func TestPGXMock_ListAccountRegistrations_SearchEscapesUnderscore(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	rawSearch := "foo_bar"
	escapedArg := `foo\_bar`

	rows := pgxmock.NewRows(registrationCols()).
		AddRow(minimalRegRow("reg-2", "foo_bar Inc", "foo_bar@example.com")...)
	mock.ExpectQuery(`ESCAPE`).
		WithArgs("%" + escapedArg + "%").
		WillReturnRows(rows)

	filter := AccountRegistrationFilter{Search: rawSearch}
	regs, err := store.ListAccountRegistrations(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, regs, 1, "expected exactly the literal-match row")
	assert.Equal(t, "reg-2", regs[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_ListAccountRegistrations_SearchEscapesBackslash verifies that a
// literal "\" in filter.Search is doubled to "\\" before being bound, so the
// backslash is treated as data rather than as the LIKE escape character.
func TestPGXMock_ListAccountRegistrations_SearchEscapesBackslash(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// Raw input contains a literal backslash; after escaping it must be doubled.
	rawSearch := `foo\bar`
	escapedArg := `foo\\bar`

	rows := pgxmock.NewRows(registrationCols()).
		AddRow(minimalRegRow("reg-3", `foo\bar Ltd`, `foo\bar@example.com`)...)
	// The query must contain ESCAPE to prove the clause was updated.
	mock.ExpectQuery(`ESCAPE`).
		WithArgs("%" + escapedArg + "%").
		WillReturnRows(rows)

	filter := AccountRegistrationFilter{Search: rawSearch}
	regs, err := store.ListAccountRegistrations(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, regs, 1, "expected exactly the literal-match row")
	assert.Equal(t, "reg-3", regs[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── errNoRows helper ────────────────────────────────────────────────────────

func errNoRows() error {
	return pgx.ErrNoRows
}

// ─── TransitionExecutionStatus CAS probe ─────────────────────────────────────

// TestPGXMock_TransitionExecutionStatus_ProbeHardErrorNotMappedToNotFound pins
// the CAS-probe contract: when the UPDATE matches zero rows and the follow-up
// GetExecutionByID probe fails with a hard DB error (outage), the error must
// propagate as-is and NOT be mapped to ErrNotFound, which callers like the
// purchase reaper treat as a benign race-loss.
func TestPGXMock_TransitionExecutionStatus_ProbeHardErrorNotMappedToNotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	execCols := []string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
		"cloud_account_id", "source", "approved_by", "cancelled_by", "capacity_percent",
		"created_by_user_id", "retry_execution_id", "retry_attempt_n",
		"approval_token_expires_at",
		"executed_by_user_id", "executed_at", "pre_approval_skip_reason",
		"idempotency_key", "scheduled_execution_at",
	}

	// CAS UPDATE matches zero rows (status already transitioned or row gone).
	mock.ExpectQuery(`UPDATE purchase_executions`).
		WithArgs(anyArgsCfg(4)...).
		WillReturnRows(pgxmock.NewRows(execCols))

	// Probe fails with a hard DB error, not an empty result.
	dbErr := errors.New("connection refused")
	mock.ExpectQuery(`SELECT plan_id, execution_id, status`).
		WithArgs("exec-probe-err").
		WillReturnError(dbErr)

	_, err := store.TransitionExecutionStatus(ctx, "exec-probe-err", []string{"pending"}, "approved", nil)
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrNotFound), "hard probe error must not be mapped to ErrNotFound")
	assert.False(t, errors.Is(err, ErrExecutionNotInExpectedStatus), "hard probe error must not read as CAS rejection")
	assert.ErrorIs(t, err, dbErr)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── F2 regression: GetExecutionByPlanAndDate zero-rows wraps ErrNotFound ────

// TestPGXMock_GetExecutionByPlanAndDate_NotFoundWrapsErrNotFound is the
// regression test for F2: a zero-row result from GetExecutionByPlanAndDate must
// return an error wrapping config.ErrNotFound so that getOrCreateExecution can
// distinguish "no existing execution" from a real store failure and create a new
// one. Before the fix the function returned a plain fmt.Errorf, which caused
// getOrCreateExecution to treat the not-found case as a fatal error, making the
// create-execution branch unreachable against the real store.
func TestPGXMock_GetExecutionByPlanAndDate_NotFoundWrapsErrNotFound(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	scheduledDate := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)

	// Return an empty row set — simulates the "no existing execution" case.
	cols := []string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
		"cloud_account_id", "source", "approved_by", "cancelled_by", "capacity_percent",
		"created_by_user_id", "retry_execution_id", "retry_attempt_n",
		"approval_token_expires_at",
		"executed_by_user_id", "executed_at", "pre_approval_skip_reason",
		"idempotency_key", "scheduled_execution_at",
	}
	emptyRows := pgxmock.NewRows(cols)
	mock.ExpectQuery("SELECT").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnRows(emptyRows)

	_, err := store.GetExecutionByPlanAndDate(ctx, "plan-missing", scheduledDate)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound),
		"zero-row GetExecutionByPlanAndDate must wrap ErrNotFound so getOrCreateExecution can create a new execution; got: %v", err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ─── Canonical-cancel write regression tests (#1277 follow-up) ───────────────
//
// These tests assert that every cancel write path emits the canonical US
// spelling 'canceled' (StatusCanceled) rather than the legacy 'cancelled'.
// They fail on the pre-fix code (which hardcoded 'cancelled') and pass only
// when the parameterized $N arg receives StatusCanceled = "canceled".

// TestPGXMock_CancelExecutionAtomic_WritesCanonicalStatus verifies that
// CancelExecutionAtomic sends StatusCanceled ("canceled") as the status
// parameter and sets canceled_by (not cancelled_by).
// Regression guard for the #1277 follow-up fix.
func TestPGXMock_CancelExecutionAtomic_WritesCanonicalStatus(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	actor := "user@example.com"

	// The mock uses regexp matching. We pin on:
	//   - "canceled_by" to confirm the new column is targeted (not cancelled_by)
	//   - WithArgs: $1=execID, $2=&actor, $3=StatusCanceled
	// The RETURNING row also carries "canceled" so the caller's returned
	// status is the canonical spelling end-to-end.
	rows := pgxmock.NewRows([]string{"status"}).AddRow(StatusCanceled)
	mock.ExpectQuery("canceled_by").
		WithArgs("exec-1", &actor, StatusCanceled).
		WillReturnRows(rows)

	// Pass mock directly as pgx.Tx (PgxPoolIface embeds pgx.Tx).
	ok, status, err := store.CancelExecutionAtomic(ctx, mock, "exec-1", &actor)
	require.NoError(t, err)
	assert.True(t, ok, "row should have been updated")
	assert.Equal(t, StatusCanceled, status, "returned status must be canonical 'canceled'")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_CancelScheduledExecutionAtomic_WritesCanonicalStatus mirrors the
// above for the scheduled-execution cancel path.
func TestPGXMock_CancelScheduledExecutionAtomic_WritesCanonicalStatus(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	actor := "scheduler@system"

	rows := pgxmock.NewRows([]string{"status"}).AddRow(StatusCanceled)
	mock.ExpectQuery("canceled_by").
		WithArgs("exec-sched", &actor, StatusCanceled).
		WillReturnRows(rows)

	ok, status, err := store.CancelScheduledExecutionAtomic(ctx, mock, "exec-sched", &actor)
	require.NoError(t, err)
	assert.True(t, ok, "row should have been updated")
	assert.Equal(t, StatusCanceled, status, "returned status must be canonical 'canceled'")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_CancelAllPendingExchanges_WritesCanonicalStatus pins
// CancelAllPendingExchanges to emit StatusCanceled as the $1 argument.
// A pre-fix version of the code sent no parameter (the value was inlined as
// the literal 'cancelled'), so this test double-checks that the parameterized
// form is used and the value is the canonical spelling.
func TestPGXMock_CancelAllPendingExchanges_WritesCanonicalStatus(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectExec("UPDATE ri_exchange_history").
		WithArgs(StatusCanceled).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))

	n, err := store.CancelAllPendingExchanges(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_CancelPendingExchangesByOrigin_Standalone_WritesCanonicalStatus
// asserts the standalone branch passes StatusCanceled.
func TestPGXMock_CancelPendingExchangesByOrigin_Standalone_WritesCanonicalStatus(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectExec("ladder_run_id IS NULL").
		WithArgs(StatusCanceled).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	n, err := store.CancelPendingExchangesByOrigin(ctx, common.ExchangeOriginStandalone)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_CancelPendingExchangesByOrigin_Ladder_WritesCanonicalStatus
// asserts the ladder branch passes StatusCanceled.
func TestPGXMock_CancelPendingExchangesByOrigin_Ladder_WritesCanonicalStatus(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	mock.ExpectExec("ladder_run_id IS NOT NULL").
		WithArgs(StatusCanceled).
		WillReturnResult(pgxmock.NewResult("UPDATE", 3))

	n, err := store.CancelPendingExchangesByOrigin(ctx, common.ExchangeOriginLadder)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPGXMock_CleanupOldExecutions_IncludesCanonicalStatus verifies that the
// cleanup DELETE includes 'canceled' (canonical) in the terminal-status filter,
// so Plans-canceled rows written by new code are not missed.
// Regression guard for the #1277 follow-up Defect 2 fix.
func TestPGXMock_CleanupOldExecutions_IncludesCanonicalStatus(t *testing.T) {
	mock := newMock(t)
	store := storeWith(mock)
	ctx := context.Background()

	// The regex pins on both 'cancelled' (legacy) and 'canceled' (canonical)
	// appearing together in the SQL. If either is missing the mock won't
	// match, causing an "unexpected query" error from ExpectationsWereMet.
	mock.ExpectExec(`'cancelled'.*'canceled'`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("DELETE", 4))

	n, err := store.CleanupOldExecutions(ctx, 90)
	require.NoError(t, err)
	assert.Equal(t, int64(4), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}
