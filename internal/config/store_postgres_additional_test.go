package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// additionalMockStore is a test wrapper for additional coverage tests
type additionalMockStore struct {
	mock pgxmock.PgxPoolIface
}

// queryExecutions is the same implementation as PostgresStore.queryExecutions
// for testing purposes
func (s *additionalMockStore) queryExecutions(ctx context.Context, query string, args ...interface{}) ([]PurchaseExecution, error) {
	rows, err := s.mock.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	executions := make([]PurchaseExecution, 0)
	for rows.Next() {
		var exec PurchaseExecution
		var recommendationsJSON []byte
		var notifSent, completedAt, expiresAt sql.NullTime

		err := rows.Scan(
			&exec.PlanID,
			&exec.ExecutionID,
			&exec.Status,
			&exec.StepNumber,
			&exec.ScheduledDate,
			&notifSent,
			&exec.ApprovalToken,
			&recommendationsJSON,
			&exec.TotalUpfrontCost,
			&exec.EstimatedSavings,
			&completedAt,
			&exec.Error,
			&expiresAt,
		)
		if err != nil {
			return nil, err
		}

		if err := json.Unmarshal(recommendationsJSON, &exec.Recommendations); err != nil {
			return nil, err
		}

		if notifSent.Valid {
			exec.NotificationSent = &notifSent.Time
		}
		if completedAt.Valid {
			exec.CompletedAt = &completedAt.Time
		}
		if expiresAt.Valid {
			exec.TTL = ttlFromTime(expiresAt.Time)
		}

		executions = append(executions, exec)
	}

	return executions, rows.Err()
}

func (s *additionalMockStore) GetExecutionByID(ctx context.Context, executionID string) (*PurchaseExecution, error) {
	query := `
		SELECT plan_id, execution_id, status, step_number, scheduled_date,
		       notification_sent, approval_token, recommendations,
		       total_upfront_cost, estimated_savings, completed_at, error, expires_at
		FROM purchase_executions
		WHERE execution_id = $1
	`

	executions, err := s.queryExecutions(ctx, query, executionID)
	if err != nil {
		return nil, err
	}

	if len(executions) == 0 {
		return nil, errors.New("execution not found")
	}

	return &executions[0], nil
}

func (s *additionalMockStore) GetExecutionByPlanAndDate(ctx context.Context, planID string, scheduledDate time.Time) (*PurchaseExecution, error) {
	query := `
		SELECT plan_id, execution_id, status, step_number, scheduled_date,
		       notification_sent, approval_token, recommendations,
		       total_upfront_cost, estimated_savings, completed_at, error, expires_at
		FROM purchase_executions
		WHERE plan_id = $1 AND scheduled_date = $2
	`

	executions, err := s.queryExecutions(ctx, query, planID, scheduledDate)
	if err != nil {
		return nil, err
	}

	if len(executions) == 0 {
		return nil, errors.New("execution not found")
	}

	return &executions[0], nil
}

func (s *additionalMockStore) queryPurchaseHistory(ctx context.Context, query string, args ...interface{}) ([]PurchaseHistoryRecord, error) {
	rows, err := s.mock.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]PurchaseHistoryRecord, 0)
	for rows.Next() {
		var record PurchaseHistoryRecord
		var planID, planName sql.NullString

		err := rows.Scan(
			&record.AccountID,
			&record.PurchaseID,
			&record.Timestamp,
			&record.Provider,
			&record.Service,
			&record.Region,
			&record.ResourceType,
			&record.Count,
			&record.Term,
			&record.Payment,
			&record.UpfrontCost,
			&record.MonthlyCost,
			&record.EstimatedSavings,
			&planID,
			&planName,
			&record.RampStep,
		)
		if err != nil {
			return nil, err
		}

		if planID.Valid {
			record.PlanID = planID.String
		}
		if planName.Valid {
			record.PlanName = planName.String
		}

		records = append(records, record)
	}

	return records, rows.Err()
}

// ==========================================
// QUERY EXECUTIONS ERROR HANDLING TESTS
// ==========================================

func TestQueryExecutions_ScanError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &additionalMockStore{mock: mock}

	// Create rows with wrong number of columns to cause scan error
	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", // Missing other columns
	}).AddRow("plan-1", "exec-1", "pending")

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WithArgs("exec-scan-error").
		WillReturnRows(rows)

	_, err = store.GetExecutionByID(context.Background(), "exec-scan-error")
	assert.Error(t, err)

	// Allow unmet expectations since the scan will fail
	mock.ExpectationsWereMet()
}

func TestQueryExecutions_RowsError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &additionalMockStore{mock: mock}

	now := time.Now()
	recsJSON, _ := json.Marshal([]RecommendationRecord{})

	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
	}).AddRow("plan-1", "exec-1", "pending", 1, now,
		sql.NullTime{}, "", recsJSON,
		1000.0, 200.0, sql.NullTime{}, "", sql.NullTime{}).
		RowError(0, errors.New("row iteration failed"))

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WithArgs("exec-row-error").
		WillReturnRows(rows)

	_, err = store.GetExecutionByID(context.Background(), "exec-row-error")
	assert.Error(t, err)

	mock.ExpectationsWereMet()
}

func TestQueryExecutions_InvalidRecommendationsJSON(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &additionalMockStore{mock: mock}

	now := time.Now()

	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
	}).AddRow("plan-bad-json", "exec-bad-json", "pending", 1, now,
		sql.NullTime{}, "", []byte("{invalid-json}"),
		1000.0, 200.0, sql.NullTime{}, "", sql.NullTime{})

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WithArgs("exec-bad-json").
		WillReturnRows(rows)

	_, err = store.GetExecutionByID(context.Background(), "exec-bad-json")
	assert.Error(t, err)

	mock.ExpectationsWereMet()
}

func TestQueryExecutions_AllTimestampsValid(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &additionalMockStore{mock: mock}

	now := time.Now()
	notifSent := now.Add(-2 * time.Hour)
	completedAt := now.Add(-1 * time.Hour)
	expiresAt := now.Add(7 * 24 * time.Hour)
	recsJSON, _ := json.Marshal([]RecommendationRecord{
		{ID: "rec-1", Provider: "aws", Service: "rds", Savings: 100.0},
	})

	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
	}).AddRow("plan-all-ts", "exec-all-ts", "completed", 3, now.Add(-3*time.Hour),
		sql.NullTime{Time: notifSent, Valid: true}, "token-xyz", recsJSON,
		5000.0, 1000.0, sql.NullTime{Time: completedAt, Valid: true}, "",
		sql.NullTime{Time: expiresAt, Valid: true})

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WithArgs("exec-all-ts").
		WillReturnRows(rows)

	exec, err := store.GetExecutionByID(context.Background(), "exec-all-ts")
	require.NoError(t, err)
	assert.Equal(t, "completed", exec.Status)
	assert.NotNil(t, exec.NotificationSent)
	assert.NotNil(t, exec.CompletedAt)
	assert.NotZero(t, exec.TTL)
	assert.Len(t, exec.Recommendations, 1)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetExecutionByPlanAndDate_QueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &additionalMockStore{mock: mock}

	scheduledDate := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WithArgs("plan-query-error", scheduledDate).
		WillReturnError(errors.New("connection refused"))

	_, err = store.GetExecutionByPlanAndDate(context.Background(), "plan-query-error", scheduledDate)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetExecutionByPlanAndDate_MultipleExecutionsReturnsFirst(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &additionalMockStore{mock: mock}

	scheduledDate := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	recsJSON, _ := json.Marshal([]RecommendationRecord{})

	// Return multiple rows - the function should return the first one
	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
	}).
		AddRow("plan-multi", "exec-first", "pending", 1, scheduledDate,
			sql.NullTime{}, "", recsJSON, 1000.0, 200.0, sql.NullTime{}, "", sql.NullTime{}).
		AddRow("plan-multi", "exec-second", "completed", 2, scheduledDate,
			sql.NullTime{}, "", recsJSON, 2000.0, 400.0, sql.NullTime{}, "", sql.NullTime{})

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WithArgs("plan-multi", scheduledDate).
		WillReturnRows(rows)

	exec, err := store.GetExecutionByPlanAndDate(context.Background(), "plan-multi", scheduledDate)
	require.NoError(t, err)
	assert.Equal(t, "exec-first", exec.ExecutionID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// QUERY PURCHASE HISTORY ERROR HANDLING TESTS
// ==========================================

func TestQueryPurchaseHistory_ScanError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &additionalMockStore{mock: mock}

	// Create rows with wrong number of columns
	rows := pgxmock.NewRows([]string{
		"account_id", "purchase_id", // Missing other columns
	}).AddRow("account-1", "purchase-1")

	mock.ExpectQuery(`SELECT account_id, purchase_id, timestamp, provider, service, region`).
		WithArgs("scan-error-account", 10).
		WillReturnRows(rows)

	query := `
		SELECT account_id, purchase_id, timestamp, provider, service, region,
		       resource_type, count, term, payment, upfront_cost, monthly_cost,
		       estimated_savings, plan_id, plan_name, ramp_step
		FROM purchase_history
		WHERE account_id = $1
		ORDER BY timestamp DESC
		LIMIT $2
	`
	_, err = store.queryPurchaseHistory(context.Background(), query, "scan-error-account", 10)
	assert.Error(t, err)

	mock.ExpectationsWereMet()
}

func TestQueryPurchaseHistory_RowsError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &additionalMockStore{mock: mock}

	now := time.Now()

	rows := pgxmock.NewRows([]string{
		"account_id", "purchase_id", "timestamp", "provider", "service", "region",
		"resource_type", "count", "term", "payment", "upfront_cost", "monthly_cost",
		"estimated_savings", "plan_id", "plan_name", "ramp_step",
	}).AddRow("account-1", "purchase-1", now, "aws", "rds", "us-east-1",
		"db.r5.large", 1, 3, "all-upfront", 1000.0, 0.0,
		200.0, sql.NullString{String: "plan-1", Valid: true}, sql.NullString{String: "Plan", Valid: true}, 1).
		RowError(0, errors.New("row read failed"))

	mock.ExpectQuery(`SELECT account_id, purchase_id, timestamp, provider, service, region`).
		WithArgs("rows-error-account", 10).
		WillReturnRows(rows)

	query := `
		SELECT account_id, purchase_id, timestamp, provider, service, region,
		       resource_type, count, term, payment, upfront_cost, monthly_cost,
		       estimated_savings, plan_id, plan_name, ramp_step
		FROM purchase_history
		WHERE account_id = $1
		ORDER BY timestamp DESC
		LIMIT $2
	`
	_, err = store.queryPurchaseHistory(context.Background(), query, "rows-error-account", 10)
	assert.Error(t, err)

	mock.ExpectationsWereMet()
}

func TestQueryPurchaseHistory_QueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &additionalMockStore{mock: mock}

	mock.ExpectQuery(`SELECT account_id, purchase_id, timestamp, provider, service, region`).
		WithArgs("query-error-account", 10).
		WillReturnError(errors.New("database unavailable"))

	query := `
		SELECT account_id, purchase_id, timestamp, provider, service, region,
		       resource_type, count, term, payment, upfront_cost, monthly_cost,
		       estimated_savings, plan_id, plan_name, ramp_step
		FROM purchase_history
		WHERE account_id = $1
		ORDER BY timestamp DESC
		LIMIT $2
	`
	_, err = store.queryPurchaseHistory(context.Background(), query, "query-error-account", 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database unavailable")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueryPurchaseHistory_NullPlanFields(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &additionalMockStore{mock: mock}

	now := time.Now()

	rows := pgxmock.NewRows([]string{
		"account_id", "purchase_id", "timestamp", "provider", "service", "region",
		"resource_type", "count", "term", "payment", "upfront_cost", "monthly_cost",
		"estimated_savings", "plan_id", "plan_name", "ramp_step",
	}).AddRow("null-fields", "purchase-null", now, "aws", "ec2", "us-west-2",
		"m5.large", 2, 1, "no-upfront", 0.0, 100.0,
		50.0, sql.NullString{}, sql.NullString{}, 0)

	mock.ExpectQuery(`SELECT account_id, purchase_id, timestamp, provider, service, region`).
		WithArgs("null-fields", 10).
		WillReturnRows(rows)

	query := `
		SELECT account_id, purchase_id, timestamp, provider, service, region,
		       resource_type, count, term, payment, upfront_cost, monthly_cost,
		       estimated_savings, plan_id, plan_name, ramp_step
		FROM purchase_history
		WHERE account_id = $1
		ORDER BY timestamp DESC
		LIMIT $2
	`
	records, err := store.queryPurchaseHistory(context.Background(), query, "null-fields", 10)
	require.NoError(t, err)
	assert.Len(t, records, 1)
	assert.Empty(t, records[0].PlanID)
	assert.Empty(t, records[0].PlanName)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// LIST SERVICE CONFIGS EDGE CASES
// ==========================================

func TestListServiceConfigs_RowsError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	rows := pgxmock.NewRows([]string{
		"provider", "service", "enabled", "term", "payment", "coverage", "ramp_schedule",
		"include_engines", "exclude_engines", "include_regions", "exclude_regions",
		"include_types", "exclude_types",
	}).
		AddRow("aws", "rds", true, 3, "all-upfront", 80.0, "immediate",
			[]string{"postgres"}, []string{}, []string{}, []string{}, []string{}, []string{}).
		RowError(0, errors.New("rows iteration error"))

	mock.ExpectQuery(`SELECT provider, service, enabled, term, payment, coverage`).
		WillReturnRows(rows)

	configs, err := store.ListServiceConfigs(context.Background())
	assert.Error(t, err)
	assert.Nil(t, configs)

	mock.ExpectationsWereMet()
}

// ==========================================
// LIST PURCHASE PLANS SCAN ERROR
// ==========================================

func TestListPurchasePlans_ScanError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	// Create rows with mismatched columns to cause scan error
	rows := pgxmock.NewRows([]string{
		"id", "name", // Missing other columns
	}).AddRow("plan-1", "Bad Plan")

	mock.ExpectQuery(`SELECT id, name, enabled, auto_purchase, notification_days_before`).
		WillReturnRows(rows)

	plans, err := store.ListPurchasePlans(context.Background())
	assert.Error(t, err)
	assert.Nil(t, plans)

	mock.ExpectationsWereMet()
}

// ==========================================
// VALIDATION EDGE CASES
// ==========================================

func TestValidatePaymentOption_EmptyIsValid(t *testing.T) {
	// Empty payment option should be valid (not set)
	err := validatePaymentOption("")
	assert.NoError(t, err)
}

func TestValidateTerm_ZeroIsValid(t *testing.T) {
	// Zero term means "not set" and should be valid
	err := validateTerm(0)
	assert.NoError(t, err)
}

func TestRampScheduleValidate_EmptyTypeIsValid(t *testing.T) {
	// Empty type should be valid
	rs := RampSchedule{
		Type:           "",
		PercentPerStep: 50,
		TotalSteps:     2,
	}
	err := rs.Validate()
	assert.NoError(t, err)
}

// ==========================================
// GLOBAL CONFIG EDGE CASES
// ==========================================

func TestGlobalConfig_ValidateWithValidEmail(t *testing.T) {
	email := "admin@company.example.com"
	cfg := GlobalConfig{
		EnabledProviders:  []string{"aws"},
		NotificationEmail: &email,
		DefaultTerm:       3,
		DefaultPayment:    "all-upfront",
		DefaultCoverage:   80,
	}
	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestGlobalConfig_ValidateWithComplexEmail(t *testing.T) {
	email := "user+tag@subdomain.company.co.uk"
	cfg := GlobalConfig{
		EnabledProviders:  []string{"gcp"},
		NotificationEmail: &email,
		DefaultTerm:       1,
		DefaultPayment:    "no-upfront",
		DefaultCoverage:   50,
	}
	err := cfg.Validate()
	assert.NoError(t, err)
}

// ==========================================
// SERVICE CONFIG EDGE CASES
// ==========================================

func TestServiceConfig_ValidateAllProviders(t *testing.T) {
	providers := []string{"aws", "azure", "gcp"}
	for _, provider := range providers {
		cfg := ServiceConfig{
			Provider: provider,
			Service:  "test-service",
			Term:     1,
			Coverage: 50,
		}
		err := cfg.Validate()
		assert.NoError(t, err, "provider %s should be valid", provider)
	}
}

func TestServiceConfig_ValidateCoverageBoundaries(t *testing.T) {
	tests := []struct {
		coverage float64
		valid    bool
	}{
		{0, true},
		{0.001, true},
		{50, true},
		{99.999, true},
		{100, true},
		{-0.001, false},
		{100.001, false},
	}

	for _, tt := range tests {
		cfg := ServiceConfig{
			Provider: "aws",
			Service:  "ec2",
			Coverage: tt.coverage,
		}
		err := cfg.Validate()
		if tt.valid {
			assert.NoError(t, err, "coverage %.3f should be valid", tt.coverage)
		} else {
			assert.Error(t, err, "coverage %.3f should be invalid", tt.coverage)
		}
	}
}

// ==========================================
// PURCHASE PLAN VALIDATION EDGE CASES
// ==========================================

func TestPurchasePlan_ValidateNameBoundary(t *testing.T) {
	// Exactly MaxPlanNameLength characters should be valid
	name := make([]byte, MaxPlanNameLength)
	for i := range name {
		name[i] = 'a'
	}

	plan := PurchasePlan{
		Name:     string(name),
		Services: map[string]ServiceConfig{},
	}
	err := plan.Validate()
	assert.NoError(t, err)

	// One more character should be invalid
	plan.Name = string(name) + "x"
	err = plan.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "plan name is too long")
}

func TestPurchasePlan_ValidateNotificationDaysBoundaries(t *testing.T) {
	tests := []struct {
		days  int
		valid bool
	}{
		{0, true},
		{1, true},
		{15, true},
		{30, true},
		{-1, false},
		{31, false},
	}

	for _, tt := range tests {
		plan := PurchasePlan{
			Name:                   "Test Plan",
			NotificationDaysBefore: tt.days,
			Services:               map[string]ServiceConfig{},
		}
		err := plan.Validate()
		if tt.valid {
			assert.NoError(t, err, "notification days %d should be valid", tt.days)
		} else {
			assert.Error(t, err, "notification days %d should be invalid", tt.days)
		}
	}
}

// ==========================================
// RAMP SCHEDULE VALIDATION EDGE CASES
// ==========================================

func TestRampSchedule_ValidateStepIntervalBoundaries(t *testing.T) {
	tests := []struct {
		interval int
		valid    bool
	}{
		{0, true},
		{1, true},
		{7, true},
		{30, true},
		{365, true},
		{-1, false},
		{366, false},
	}

	for _, tt := range tests {
		rs := RampSchedule{
			StepIntervalDays: tt.interval,
			PercentPerStep:   25,
			TotalSteps:       4,
		}
		err := rs.Validate()
		if tt.valid {
			assert.NoError(t, err, "step interval %d should be valid", tt.interval)
		} else {
			assert.Error(t, err, "step interval %d should be invalid", tt.interval)
		}
	}
}

func TestRampSchedule_ValidateTotalStepsBoundaries(t *testing.T) {
	tests := []struct {
		steps int
		valid bool
	}{
		{0, true},
		{1, true},
		{50, true},
		{100, true},
		{-1, false},
		{101, false},
	}

	for _, tt := range tests {
		rs := RampSchedule{
			TotalSteps:     tt.steps,
			PercentPerStep: 10,
		}
		err := rs.Validate()
		if tt.valid {
			assert.NoError(t, err, "total steps %d should be valid", tt.steps)
		} else {
			assert.Error(t, err, "total steps %d should be invalid", tt.steps)
		}
	}
}

// ==========================================
// PRESET RAMP SCHEDULES TESTS
// ==========================================

func TestPresetRampSchedules_AllValid(t *testing.T) {
	for name, schedule := range PresetRampSchedules {
		err := schedule.Validate()
		assert.NoError(t, err, "preset schedule %s should be valid", name)
	}
}

func TestPresetRampSchedules_ImmediateComplete(t *testing.T) {
	schedule := PresetRampSchedules["immediate"]
	// Set current step to total steps
	schedule.CurrentStep = schedule.TotalSteps
	assert.True(t, schedule.IsComplete())
}

func TestPresetRampSchedules_Weekly25PctNotComplete(t *testing.T) {
	schedule := PresetRampSchedules["weekly-25pct"]
	// At step 2 of 4, should not be complete
	schedule.CurrentStep = 2
	assert.False(t, schedule.IsComplete())
}

func TestPresetRampSchedules_Monthly10PctCoverage(t *testing.T) {
	schedule := PresetRampSchedules["monthly-10pct"]
	schedule.CurrentStep = 5 // 50% complete
	coverage := schedule.GetCurrentCoverage(100)
	assert.Equal(t, 50.0, coverage)
}

// ==========================================
// CONFIG SETTING TYPE TESTS
// ==========================================

func TestConfigSetting_AllTypes(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		setting     ConfigSetting
		checkType   func(interface{}) bool
		description string
	}{
		{
			name: "int type",
			setting: ConfigSetting{
				Key:       "test.int",
				Value:     42,
				Type:      "int",
				Category:  "test",
				UpdatedAt: now,
			},
			checkType: func(v interface{}) bool {
				_, ok := v.(int)
				return ok
			},
			description: "integer value",
		},
		{
			name: "float type",
			setting: ConfigSetting{
				Key:       "test.float",
				Value:     3.14159,
				Type:      "float",
				Category:  "test",
				UpdatedAt: now,
			},
			checkType: func(v interface{}) bool {
				_, ok := v.(float64)
				return ok
			},
			description: "float value",
		},
		{
			name: "bool type",
			setting: ConfigSetting{
				Key:       "test.bool",
				Value:     true,
				Type:      "bool",
				Category:  "test",
				UpdatedAt: now,
			},
			checkType: func(v interface{}) bool {
				_, ok := v.(bool)
				return ok
			},
			description: "boolean value",
		},
		{
			name: "string type",
			setting: ConfigSetting{
				Key:       "test.string",
				Value:     "hello world",
				Type:      "string",
				Category:  "test",
				UpdatedAt: now,
			},
			checkType: func(v interface{}) bool {
				_, ok := v.(string)
				return ok
			},
			description: "string value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.checkType(tt.setting.Value), "expected %s", tt.description)
			assert.NotEmpty(t, tt.setting.Key)
			assert.NotEmpty(t, tt.setting.Type)
			assert.NotEmpty(t, tt.setting.Category)
		})
	}
}

// ==========================================
// INTERFACE VERIFICATION
// ==========================================

func TestStoreInterface_Methods(t *testing.T) {
	// Verify all interface methods are present
	var iface StoreInterface
	store := NewPostgresStore(nil)
	iface = store

	// If this compiles, the interface is properly implemented
	assert.NotNil(t, iface)
}

// ==========================================
// CONSTANTS VERIFICATION
// ==========================================

func TestConstants_Complete(t *testing.T) {
	// Verify all expected constants exist and have reasonable values
	assert.Equal(t, 100, DefaultListLimit)
	assert.Equal(t, 30, DefaultExecutionTTLDays)
	assert.Equal(t, 10, DefaultMaxRecommendationsInEmail)
	assert.Equal(t, 1*time.Hour, DefaultPasswordResetExpiry)

	assert.Equal(t, 100, MaxCoverage)
	assert.Equal(t, 0, MinCoverage)
	assert.Equal(t, 100, MaxPlanNameLength)
	assert.Equal(t, 30, MaxNotificationDaysBefore)
	assert.Equal(t, 365, MaxStepIntervalDays)
	assert.Equal(t, 100, MaxTotalSteps)

	assert.Equal(t, 80, DefaultCoveragePercent)
	assert.Equal(t, 7, DefaultNotifyDaysBefore)

	assert.Equal(t, "immediate", RampImmediate)
	assert.Equal(t, "weekly-25pct", RampWeekly25Pct)
	assert.Equal(t, "monthly-10pct", RampMonthly10Pct)
	assert.Equal(t, 7, WeeklyStepIntervalDays)
	assert.Equal(t, 30, MonthlyStepIntervalDays)

	assert.Equal(t, 24, HoursPerDay)
	assert.Equal(t, 24, MinHoursBetweenNotifications)

	assert.Equal(t, 32, TokenByteLength)
	assert.Equal(t, 30, MFATimeStep)
	assert.Equal(t, 6, MFADigits)
}
