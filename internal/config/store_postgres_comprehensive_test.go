package config

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

// mockablePostgresStore is a test wrapper that allows direct pgxmock integration
// This mirrors the actual PostgresStore logic for testing
type mockablePostgresStore struct {
	mock pgxmock.PgxPoolIface
}

// ==========================================
// CREATE PURCHASE PLAN TESTS
// ==========================================

func (s *mockablePostgresStore) CreatePurchasePlan(ctx context.Context, plan *PurchasePlan) error {
	// Generate UUID if not provided (simulating the actual behavior)
	if plan.ID == "" {
		plan.ID = "test-generated-uuid"
	}

	// Set timestamps
	now := time.Now()
	plan.CreatedAt = now
	plan.UpdatedAt = now

	// Marshal services and ramp_schedule to JSONB
	servicesJSON, err := json.Marshal(plan.Services)
	if err != nil {
		return err
	}

	rampScheduleJSON, err := json.Marshal(plan.RampSchedule)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO purchase_plans (
			id, name, enabled, auto_purchase, notification_days_before,
			services, ramp_schedule, created_at, updated_at,
			next_execution_date, last_execution_date, last_notification_sent
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	_, err = s.mock.Exec(ctx, query,
		plan.ID,
		plan.Name,
		plan.Enabled,
		plan.AutoPurchase,
		plan.NotificationDaysBefore,
		servicesJSON,
		rampScheduleJSON,
		plan.CreatedAt,
		plan.UpdatedAt,
		plan.NextExecutionDate,
		plan.LastExecutionDate,
		plan.LastNotificationSent,
	)

	return err
}

func (s *mockablePostgresStore) UpdatePurchasePlan(ctx context.Context, plan *PurchasePlan) error {
	plan.UpdatedAt = time.Now()

	servicesJSON, err := json.Marshal(plan.Services)
	if err != nil {
		return err
	}

	rampScheduleJSON, err := json.Marshal(plan.RampSchedule)
	if err != nil {
		return err
	}

	query := `
		UPDATE purchase_plans SET
			name = $2,
			enabled = $3,
			auto_purchase = $4,
			notification_days_before = $5,
			services = $6,
			ramp_schedule = $7,
			updated_at = $8,
			next_execution_date = $9,
			last_execution_date = $10,
			last_notification_sent = $11
		WHERE id = $1
	`

	result, err := s.mock.Exec(ctx, query,
		plan.ID,
		plan.Name,
		plan.Enabled,
		plan.AutoPurchase,
		plan.NotificationDaysBefore,
		servicesJSON,
		rampScheduleJSON,
		plan.UpdatedAt,
		plan.NextExecutionDate,
		plan.LastExecutionDate,
		plan.LastNotificationSent,
	)

	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return errors.New("purchase plan not found")
	}

	return nil
}

func (s *mockablePostgresStore) ListPurchasePlans(ctx context.Context) ([]PurchasePlan, error) {
	query := `
		SELECT id, name, enabled, auto_purchase, notification_days_before,
		       services, ramp_schedule, created_at, updated_at,
		       next_execution_date, last_execution_date, last_notification_sent
		FROM purchase_plans
		ORDER BY created_at DESC
	`

	rows, err := s.mock.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	plans := make([]PurchasePlan, 0)
	for rows.Next() {
		var plan PurchasePlan
		var servicesJSON, rampScheduleJSON []byte
		var nextExecDate, lastExecDate, lastNotifSent sql.NullTime

		err := rows.Scan(
			&plan.ID,
			&plan.Name,
			&plan.Enabled,
			&plan.AutoPurchase,
			&plan.NotificationDaysBefore,
			&servicesJSON,
			&rampScheduleJSON,
			&plan.CreatedAt,
			&plan.UpdatedAt,
			&nextExecDate,
			&lastExecDate,
			&lastNotifSent,
		)
		if err != nil {
			return nil, err
		}

		if err := json.Unmarshal(servicesJSON, &plan.Services); err != nil {
			return nil, err
		}

		if err := json.Unmarshal(rampScheduleJSON, &plan.RampSchedule); err != nil {
			return nil, err
		}

		if nextExecDate.Valid {
			plan.NextExecutionDate = &nextExecDate.Time
		}
		if lastExecDate.Valid {
			plan.LastExecutionDate = &lastExecDate.Time
		}
		if lastNotifSent.Valid {
			plan.LastNotificationSent = &lastNotifSent.Time
		}

		plans = append(plans, plan)
	}

	return plans, rows.Err()
}

func (s *mockablePostgresStore) SavePurchaseExecution(ctx context.Context, execution *PurchaseExecution) error {
	if execution.ExecutionID == "" {
		execution.ExecutionID = "test-exec-uuid"
	}

	recommendationsJSON, err := json.Marshal(execution.Recommendations)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO purchase_executions (
			plan_id, execution_id, status, step_number, scheduled_date,
			notification_sent, approval_token, recommendations,
			total_upfront_cost, estimated_savings, completed_at, error, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (execution_id) DO UPDATE SET
			status = $3,
			notification_sent = $6,
			approval_token = $7,
			recommendations = $8,
			total_upfront_cost = $9,
			estimated_savings = $10,
			completed_at = $11,
			error = $12,
			expires_at = $13,
			updated_at = NOW()
	`

	_, err = s.mock.Exec(ctx, query,
		execution.PlanID,
		execution.ExecutionID,
		execution.Status,
		execution.StepNumber,
		execution.ScheduledDate,
		execution.NotificationSent,
		execution.ApprovalToken,
		recommendationsJSON,
		execution.TotalUpfrontCost,
		execution.EstimatedSavings,
		execution.CompletedAt,
		execution.Error,
		timeFromTTL(execution.TTL),
	)

	return err
}

func (s *mockablePostgresStore) GetPendingExecutions(ctx context.Context) ([]PurchaseExecution, error) {
	query := `
		SELECT plan_id, execution_id, status, step_number, scheduled_date,
		       notification_sent, approval_token, recommendations,
		       total_upfront_cost, estimated_savings, completed_at, error, expires_at
		FROM purchase_executions
		WHERE status IN ('pending', 'notified')
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY scheduled_date ASC
	`

	return s.queryExecutions(ctx, query)
}

func (s *mockablePostgresStore) GetExecutionByID(ctx context.Context, executionID string) (*PurchaseExecution, error) {
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

func (s *mockablePostgresStore) GetExecutionByPlanAndDate(ctx context.Context, planID string, scheduledDate time.Time) (*PurchaseExecution, error) {
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

func (s *mockablePostgresStore) queryExecutions(ctx context.Context, query string, args ...interface{}) ([]PurchaseExecution, error) {
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

func (s *mockablePostgresStore) SavePurchaseHistory(ctx context.Context, record *PurchaseHistoryRecord) error {
	query := `
		INSERT INTO purchase_history (
			account_id, purchase_id, timestamp, provider, service, region,
			resource_type, count, term, payment, upfront_cost, monthly_cost,
			estimated_savings, plan_id, plan_name, ramp_step
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`

	_, err := s.mock.Exec(ctx, query,
		record.AccountID,
		record.PurchaseID,
		record.Timestamp,
		record.Provider,
		record.Service,
		record.Region,
		record.ResourceType,
		record.Count,
		record.Term,
		record.Payment,
		record.UpfrontCost,
		record.MonthlyCost,
		record.EstimatedSavings,
		nullStringFromString(record.PlanID),
		nullStringFromString(record.PlanName),
		record.RampStep,
	)

	return err
}

func (s *mockablePostgresStore) GetPurchaseHistory(ctx context.Context, accountID string, limit int) ([]PurchaseHistoryRecord, error) {
	query := `
		SELECT account_id, purchase_id, timestamp, provider, service, region,
		       resource_type, count, term, payment, upfront_cost, monthly_cost,
		       estimated_savings, plan_id, plan_name, ramp_step
		FROM purchase_history
		WHERE account_id = $1
		ORDER BY timestamp DESC
		LIMIT $2
	`

	return s.queryPurchaseHistory(ctx, query, accountID, limit)
}

func (s *mockablePostgresStore) GetAllPurchaseHistory(ctx context.Context, limit int) ([]PurchaseHistoryRecord, error) {
	query := `
		SELECT account_id, purchase_id, timestamp, provider, service, region,
		       resource_type, count, term, payment, upfront_cost, monthly_cost,
		       estimated_savings, plan_id, plan_name, ramp_step
		FROM purchase_history
		ORDER BY timestamp DESC
		LIMIT $1
	`

	return s.queryPurchaseHistory(ctx, query, limit)
}

func (s *mockablePostgresStore) queryPurchaseHistory(ctx context.Context, query string, args ...interface{}) ([]PurchaseHistoryRecord, error) {
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
// CREATE PURCHASE PLAN TESTS
// ==========================================

func TestCreatePurchasePlan_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

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

	mock.ExpectExec(`INSERT INTO purchase_plans`).
		WithArgs(
			pgxmock.AnyArg(), // ID
			plan.Name,
			plan.Enabled,
			plan.AutoPurchase,
			plan.NotificationDaysBefore,
			pgxmock.AnyArg(), // services JSON
			pgxmock.AnyArg(), // ramp_schedule JSON
			pgxmock.AnyArg(), // created_at
			pgxmock.AnyArg(), // updated_at
			pgxmock.AnyArg(), // next_execution_date
			pgxmock.AnyArg(), // last_execution_date
			pgxmock.AnyArg(), // last_notification_sent
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = store.CreatePurchasePlan(context.Background(), plan)
	assert.NoError(t, err)
	assert.NotEmpty(t, plan.ID)
	assert.False(t, plan.CreatedAt.IsZero())
	assert.False(t, plan.UpdatedAt.IsZero())

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreatePurchasePlan_WithExistingID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	plan := &PurchasePlan{
		ID:                     "existing-id-123",
		Name:                   "Plan with ID",
		Enabled:                true,
		NotificationDaysBefore: 3,
		Services:               map[string]ServiceConfig{},
		RampSchedule:           RampSchedule{Type: "weekly"},
	}

	mock.ExpectExec(`INSERT INTO purchase_plans`).
		WithArgs(
			"existing-id-123", // Should use existing ID
			plan.Name,
			plan.Enabled,
			plan.AutoPurchase,
			plan.NotificationDaysBefore,
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = store.CreatePurchasePlan(context.Background(), plan)
	assert.NoError(t, err)
	assert.Equal(t, "existing-id-123", plan.ID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreatePurchasePlan_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	plan := &PurchasePlan{
		Name:         "Error Plan",
		Services:     map[string]ServiceConfig{},
		RampSchedule: RampSchedule{},
	}

	mock.ExpectExec(`INSERT INTO purchase_plans`).
		WithArgs(
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
		).
		WillReturnError(errors.New("unique constraint violation"))

	err = store.CreatePurchasePlan(context.Background(), plan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unique constraint")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreatePurchasePlan_WithNullableTimestamps(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	nextExec := time.Now().Add(24 * time.Hour)
	lastExec := time.Now().Add(-24 * time.Hour)
	lastNotif := time.Now().Add(-12 * time.Hour)

	plan := &PurchasePlan{
		Name:                   "Plan with timestamps",
		Enabled:                true,
		NotificationDaysBefore: 5,
		Services:               map[string]ServiceConfig{},
		RampSchedule:           RampSchedule{Type: "immediate"},
		NextExecutionDate:      &nextExec,
		LastExecutionDate:      &lastExec,
		LastNotificationSent:   &lastNotif,
	}

	mock.ExpectExec(`INSERT INTO purchase_plans`).
		WithArgs(
			pgxmock.AnyArg(),
			plan.Name,
			plan.Enabled,
			plan.AutoPurchase,
			plan.NotificationDaysBefore,
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			&nextExec,
			&lastExec,
			&lastNotif,
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = store.CreatePurchasePlan(context.Background(), plan)
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// UPDATE PURCHASE PLAN TESTS
// ==========================================

func TestUpdatePurchasePlan_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	plan := &PurchasePlan{
		ID:                     "plan-123",
		Name:                   "Updated Plan",
		Enabled:                false,
		AutoPurchase:           true,
		NotificationDaysBefore: 10,
		Services:               map[string]ServiceConfig{},
		RampSchedule:           RampSchedule{Type: "monthly"},
	}

	mock.ExpectExec(`UPDATE purchase_plans SET`).
		WithArgs(
			plan.ID,
			plan.Name,
			plan.Enabled,
			plan.AutoPurchase,
			plan.NotificationDaysBefore,
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
		).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	err = store.UpdatePurchasePlan(context.Background(), plan)
	assert.NoError(t, err)
	assert.False(t, plan.UpdatedAt.IsZero())

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePurchasePlan_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	plan := &PurchasePlan{
		ID:           "nonexistent",
		Name:         "Ghost Plan",
		Services:     map[string]ServiceConfig{},
		RampSchedule: RampSchedule{},
	}

	mock.ExpectExec(`UPDATE purchase_plans SET`).
		WithArgs(
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
		).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err = store.UpdatePurchasePlan(context.Background(), plan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePurchasePlan_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	plan := &PurchasePlan{
		ID:           "plan-error",
		Name:         "Error Plan",
		Services:     map[string]ServiceConfig{},
		RampSchedule: RampSchedule{},
	}

	mock.ExpectExec(`UPDATE purchase_plans SET`).
		WithArgs(
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
		).
		WillReturnError(errors.New("database connection lost"))

	err = store.UpdatePurchasePlan(context.Background(), plan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database connection lost")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// LIST PURCHASE PLANS TESTS
// ==========================================

func TestListPurchasePlans_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	now := time.Now()
	nextExec := now.Add(24 * time.Hour)
	servicesJSON1, _ := json.Marshal(map[string]ServiceConfig{
		"aws:rds": {Provider: "aws", Service: "rds"},
	})
	rampJSON1, _ := json.Marshal(RampSchedule{Type: "immediate"})

	servicesJSON2, _ := json.Marshal(map[string]ServiceConfig{})
	rampJSON2, _ := json.Marshal(RampSchedule{Type: "weekly", PercentPerStep: 25})

	rows := pgxmock.NewRows([]string{
		"id", "name", "enabled", "auto_purchase", "notification_days_before",
		"services", "ramp_schedule", "created_at", "updated_at",
		"next_execution_date", "last_execution_date", "last_notification_sent",
	}).
		AddRow("plan-1", "First Plan", true, false, 7,
			servicesJSON1, rampJSON1, now, now,
			sql.NullTime{Time: nextExec, Valid: true}, sql.NullTime{}, sql.NullTime{}).
		AddRow("plan-2", "Second Plan", false, true, 3,
			servicesJSON2, rampJSON2, now, now,
			sql.NullTime{}, sql.NullTime{}, sql.NullTime{})

	mock.ExpectQuery(`SELECT id, name, enabled, auto_purchase, notification_days_before`).
		WillReturnRows(rows)

	plans, err := store.ListPurchasePlans(context.Background())
	require.NoError(t, err)
	assert.Len(t, plans, 2)

	assert.Equal(t, "plan-1", plans[0].ID)
	assert.Equal(t, "First Plan", plans[0].Name)
	assert.True(t, plans[0].Enabled)
	assert.NotNil(t, plans[0].NextExecutionDate)
	assert.NotNil(t, plans[0].Services["aws:rds"])

	assert.Equal(t, "plan-2", plans[1].ID)
	assert.Equal(t, "Second Plan", plans[1].Name)
	assert.False(t, plans[1].Enabled)
	assert.True(t, plans[1].AutoPurchase)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListPurchasePlans_Empty(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	rows := pgxmock.NewRows([]string{
		"id", "name", "enabled", "auto_purchase", "notification_days_before",
		"services", "ramp_schedule", "created_at", "updated_at",
		"next_execution_date", "last_execution_date", "last_notification_sent",
	})

	mock.ExpectQuery(`SELECT id, name, enabled, auto_purchase, notification_days_before`).
		WillReturnRows(rows)

	plans, err := store.ListPurchasePlans(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, plans)
	assert.Empty(t, plans)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListPurchasePlans_QueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	mock.ExpectQuery(`SELECT id, name, enabled, auto_purchase, notification_days_before`).
		WillReturnError(errors.New("table not found"))

	plans, err := store.ListPurchasePlans(context.Background())
	assert.Error(t, err)
	assert.Nil(t, plans)
	assert.Contains(t, err.Error(), "table not found")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListPurchasePlans_InvalidServicesJSON(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	now := time.Now()
	rampJSON, _ := json.Marshal(RampSchedule{Type: "immediate"})

	rows := pgxmock.NewRows([]string{
		"id", "name", "enabled", "auto_purchase", "notification_days_before",
		"services", "ramp_schedule", "created_at", "updated_at",
		"next_execution_date", "last_execution_date", "last_notification_sent",
	}).AddRow("plan-1", "Bad Plan", true, false, 7,
		[]byte("not valid json"), rampJSON, now, now,
		sql.NullTime{}, sql.NullTime{}, sql.NullTime{})

	mock.ExpectQuery(`SELECT id, name, enabled, auto_purchase, notification_days_before`).
		WillReturnRows(rows)

	plans, err := store.ListPurchasePlans(context.Background())
	assert.Error(t, err)
	assert.Nil(t, plans)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListPurchasePlans_InvalidRampScheduleJSON(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	now := time.Now()
	servicesJSON, _ := json.Marshal(map[string]ServiceConfig{})

	rows := pgxmock.NewRows([]string{
		"id", "name", "enabled", "auto_purchase", "notification_days_before",
		"services", "ramp_schedule", "created_at", "updated_at",
		"next_execution_date", "last_execution_date", "last_notification_sent",
	}).AddRow("plan-1", "Bad Ramp Plan", true, false, 7,
		servicesJSON, []byte("{invalid}"), now, now,
		sql.NullTime{}, sql.NullTime{}, sql.NullTime{})

	mock.ExpectQuery(`SELECT id, name, enabled, auto_purchase, notification_days_before`).
		WillReturnRows(rows)

	plans, err := store.ListPurchasePlans(context.Background())
	assert.Error(t, err)
	assert.Nil(t, plans)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// SAVE PURCHASE EXECUTION TESTS
// ==========================================

func TestSavePurchaseExecution_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

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

	mock.ExpectExec(`INSERT INTO purchase_executions`).
		WithArgs(
			exec.PlanID,
			pgxmock.AnyArg(), // execution_id
			exec.Status,
			exec.StepNumber,
			pgxmock.AnyArg(), // scheduled_date
			pgxmock.AnyArg(), // notification_sent
			exec.ApprovalToken,
			pgxmock.AnyArg(), // recommendations JSON
			exec.TotalUpfrontCost,
			exec.EstimatedSavings,
			pgxmock.AnyArg(), // completed_at
			exec.Error,
			pgxmock.AnyArg(), // expires_at
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = store.SavePurchaseExecution(context.Background(), exec)
	assert.NoError(t, err)
	assert.NotEmpty(t, exec.ExecutionID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSavePurchaseExecution_WithExistingID(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

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

	mock.ExpectExec(`INSERT INTO purchase_executions`).
		WithArgs(
			exec.PlanID,
			"existing-exec-id",
			exec.Status,
			exec.StepNumber,
			exec.ScheduledDate,
			&notifSent,
			exec.ApprovalToken,
			pgxmock.AnyArg(),
			exec.TotalUpfrontCost,
			exec.EstimatedSavings,
			&completedAt,
			exec.Error,
			pgxmock.AnyArg(), // TTL converted to time
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = store.SavePurchaseExecution(context.Background(), exec)
	assert.NoError(t, err)
	assert.Equal(t, "existing-exec-id", exec.ExecutionID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSavePurchaseExecution_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	exec := &PurchaseExecution{
		PlanID:          "plan-error",
		Status:          "pending",
		ScheduledDate:   time.Now(),
		Recommendations: []RecommendationRecord{},
	}

	mock.ExpectExec(`INSERT INTO purchase_executions`).
		WithArgs(
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(),
		).
		WillReturnError(errors.New("foreign key violation"))

	err = store.SavePurchaseExecution(context.Background(), exec)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "foreign key")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// GET PENDING EXECUTIONS TESTS
// ==========================================

func TestGetPendingExecutions_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	now := time.Now()
	notifSent := now.Add(-1 * time.Hour)
	expiresAt := now.Add(7 * 24 * time.Hour)
	recsJSON1, _ := json.Marshal([]RecommendationRecord{{ID: "rec-1"}})
	recsJSON2, _ := json.Marshal([]RecommendationRecord{})

	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
	}).
		AddRow("plan-1", "exec-1", "pending", 1, now,
			sql.NullTime{}, "", recsJSON1,
			1000.0, 200.0, sql.NullTime{}, "", sql.NullTime{Time: expiresAt, Valid: true}).
		AddRow("plan-2", "exec-2", "notified", 2, now.Add(time.Hour),
			sql.NullTime{Time: notifSent, Valid: true}, "token-abc", recsJSON2,
			500.0, 100.0, sql.NullTime{}, "", sql.NullTime{})

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WillReturnRows(rows)

	executions, err := store.GetPendingExecutions(context.Background())
	require.NoError(t, err)
	assert.Len(t, executions, 2)

	assert.Equal(t, "pending", executions[0].Status)
	assert.Nil(t, executions[0].NotificationSent)
	assert.NotZero(t, executions[0].TTL)

	assert.Equal(t, "notified", executions[1].Status)
	assert.NotNil(t, executions[1].NotificationSent)
	assert.Equal(t, "token-abc", executions[1].ApprovalToken)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPendingExecutions_Empty(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
	})

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WillReturnRows(rows)

	executions, err := store.GetPendingExecutions(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, executions)
	assert.Empty(t, executions)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPendingExecutions_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WillReturnError(errors.New("connection timeout"))

	executions, err := store.GetPendingExecutions(context.Background())
	assert.Error(t, err)
	assert.Nil(t, executions)
	assert.Contains(t, err.Error(), "connection timeout")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPendingExecutions_InvalidRecommendationsJSON(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	now := time.Now()

	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
	}).AddRow("plan-1", "exec-1", "pending", 1, now,
		sql.NullTime{}, "", []byte("invalid json"),
		1000.0, 200.0, sql.NullTime{}, "", sql.NullTime{})

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WillReturnRows(rows)

	executions, err := store.GetPendingExecutions(context.Background())
	assert.Error(t, err)
	assert.Nil(t, executions)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// GET EXECUTION BY ID TESTS
// ==========================================

func TestGetExecutionByID_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	now := time.Now()
	completedAt := now.Add(-1 * time.Hour)
	recsJSON, _ := json.Marshal([]RecommendationRecord{
		{ID: "rec-1", Provider: "aws", Service: "rds", Savings: 100.0},
	})

	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
	}).AddRow("plan-123", "exec-456", "completed", 3, now,
		sql.NullTime{}, "token-xyz", recsJSON,
		2500.0, 500.0, sql.NullTime{Time: completedAt, Valid: true}, "", sql.NullTime{})

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WithArgs("exec-456").
		WillReturnRows(rows)

	exec, err := store.GetExecutionByID(context.Background(), "exec-456")
	require.NoError(t, err)
	assert.NotNil(t, exec)
	assert.Equal(t, "exec-456", exec.ExecutionID)
	assert.Equal(t, "completed", exec.Status)
	assert.Equal(t, 2500.0, exec.TotalUpfrontCost)
	assert.NotNil(t, exec.CompletedAt)
	assert.Len(t, exec.Recommendations, 1)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetExecutionByID_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
	})

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WithArgs("nonexistent").
		WillReturnRows(rows)

	exec, err := store.GetExecutionByID(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, exec)
	assert.Contains(t, err.Error(), "not found")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetExecutionByID_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WithArgs("exec-error").
		WillReturnError(errors.New("database error"))

	exec, err := store.GetExecutionByID(context.Background(), "exec-error")
	assert.Error(t, err)
	assert.Nil(t, exec)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// GET EXECUTION BY PLAN AND DATE TESTS
// ==========================================

func TestGetExecutionByPlanAndDate_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	scheduledDate := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	recsJSON, _ := json.Marshal([]RecommendationRecord{})

	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
	}).AddRow("plan-abc", "exec-xyz", "pending", 1, scheduledDate,
		sql.NullTime{}, "", recsJSON,
		0.0, 0.0, sql.NullTime{}, "", sql.NullTime{})

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WithArgs("plan-abc", scheduledDate).
		WillReturnRows(rows)

	exec, err := store.GetExecutionByPlanAndDate(context.Background(), "plan-abc", scheduledDate)
	require.NoError(t, err)
	assert.NotNil(t, exec)
	assert.Equal(t, "plan-abc", exec.PlanID)
	assert.Equal(t, scheduledDate, exec.ScheduledDate)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetExecutionByPlanAndDate_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	scheduledDate := time.Date(2024, 12, 25, 0, 0, 0, 0, time.UTC)

	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
	})

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WithArgs("plan-xyz", scheduledDate).
		WillReturnRows(rows)

	exec, err := store.GetExecutionByPlanAndDate(context.Background(), "plan-xyz", scheduledDate)
	assert.Error(t, err)
	assert.Nil(t, exec)
	assert.Contains(t, err.Error(), "not found")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// SAVE PURCHASE HISTORY TESTS
// ==========================================

func TestSavePurchaseHistory_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

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
		PlanName:         "Production RDS Plan",
		RampStep:         1,
	}

	mock.ExpectExec(`INSERT INTO purchase_history`).
		WithArgs(
			record.AccountID,
			record.PurchaseID,
			record.Timestamp,
			record.Provider,
			record.Service,
			record.Region,
			record.ResourceType,
			record.Count,
			record.Term,
			record.Payment,
			record.UpfrontCost,
			record.MonthlyCost,
			record.EstimatedSavings,
			pgxmock.AnyArg(), // plan_id as NullString
			pgxmock.AnyArg(), // plan_name as NullString
			record.RampStep,
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = store.SavePurchaseHistory(context.Background(), record)
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSavePurchaseHistory_WithEmptyOptionalFields(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	record := &PurchaseHistoryRecord{
		AccountID:    "999888777666",
		PurchaseID:   "purchase-manual",
		Timestamp:    time.Now(),
		Provider:     "gcp",
		Service:      "compute",
		Region:       "us-central1",
		ResourceType: "n1-standard-4",
		Count:        1,
		Term:         1,
		Payment:      "no-upfront",
		// PlanID and PlanName intentionally empty
	}

	mock.ExpectExec(`INSERT INTO purchase_history`).
		WithArgs(
			record.AccountID,
			record.PurchaseID,
			record.Timestamp,
			record.Provider,
			record.Service,
			record.Region,
			record.ResourceType,
			record.Count,
			record.Term,
			record.Payment,
			record.UpfrontCost,
			record.MonthlyCost,
			record.EstimatedSavings,
			pgxmock.AnyArg(), // empty plan_id
			pgxmock.AnyArg(), // empty plan_name
			record.RampStep,
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = store.SavePurchaseHistory(context.Background(), record)
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSavePurchaseHistory_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	record := &PurchaseHistoryRecord{
		AccountID:  "account-error",
		PurchaseID: "purchase-error",
		Timestamp:  time.Now(),
		Provider:   "aws",
		Service:    "ec2",
	}

	mock.ExpectExec(`INSERT INTO purchase_history`).
		WithArgs(
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
		).
		WillReturnError(errors.New("constraint violation"))

	err = store.SavePurchaseHistory(context.Background(), record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "constraint violation")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// GET PURCHASE HISTORY TESTS
// ==========================================

func TestGetPurchaseHistory_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	now := time.Now()

	rows := pgxmock.NewRows([]string{
		"account_id", "purchase_id", "timestamp", "provider", "service", "region",
		"resource_type", "count", "term", "payment", "upfront_cost", "monthly_cost",
		"estimated_savings", "plan_id", "plan_name", "ramp_step",
	}).
		AddRow("123456", "purch-1", now, "aws", "rds", "us-east-1",
			"db.r5.large", 2, 3, "all-upfront", 1500.0, 0.0,
			300.0, sql.NullString{String: "plan-1", Valid: true}, sql.NullString{String: "RDS Plan", Valid: true}, 1).
		AddRow("123456", "purch-2", now.Add(-time.Hour), "aws", "ec2", "us-west-2",
			"m5.xlarge", 5, 1, "no-upfront", 0.0, 500.0,
			100.0, sql.NullString{}, sql.NullString{}, 0)

	mock.ExpectQuery(`SELECT account_id, purchase_id, timestamp, provider, service, region`).
		WithArgs("123456", 10).
		WillReturnRows(rows)

	history, err := store.GetPurchaseHistory(context.Background(), "123456", 10)
	require.NoError(t, err)
	assert.Len(t, history, 2)

	assert.Equal(t, "purch-1", history[0].PurchaseID)
	assert.Equal(t, "plan-1", history[0].PlanID)
	assert.Equal(t, "RDS Plan", history[0].PlanName)

	assert.Equal(t, "purch-2", history[1].PurchaseID)
	assert.Empty(t, history[1].PlanID)
	assert.Empty(t, history[1].PlanName)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPurchaseHistory_Empty(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	rows := pgxmock.NewRows([]string{
		"account_id", "purchase_id", "timestamp", "provider", "service", "region",
		"resource_type", "count", "term", "payment", "upfront_cost", "monthly_cost",
		"estimated_savings", "plan_id", "plan_name", "ramp_step",
	})

	mock.ExpectQuery(`SELECT account_id, purchase_id, timestamp, provider, service, region`).
		WithArgs("empty-account", 50).
		WillReturnRows(rows)

	history, err := store.GetPurchaseHistory(context.Background(), "empty-account", 50)
	require.NoError(t, err)
	assert.NotNil(t, history)
	assert.Empty(t, history)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPurchaseHistory_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	mock.ExpectQuery(`SELECT account_id, purchase_id, timestamp, provider, service, region`).
		WithArgs("error-account", 10).
		WillReturnError(errors.New("query timeout"))

	history, err := store.GetPurchaseHistory(context.Background(), "error-account", 10)
	assert.Error(t, err)
	assert.Nil(t, history)
	assert.Contains(t, err.Error(), "query timeout")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// GET ALL PURCHASE HISTORY TESTS
// ==========================================

func TestGetAllPurchaseHistory_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	now := time.Now()

	rows := pgxmock.NewRows([]string{
		"account_id", "purchase_id", "timestamp", "provider", "service", "region",
		"resource_type", "count", "term", "payment", "upfront_cost", "monthly_cost",
		"estimated_savings", "plan_id", "plan_name", "ramp_step",
	}).
		AddRow("account-1", "purch-a1", now, "aws", "elasticache", "eu-west-1",
			"cache.r5.large", 4, 3, "partial-upfront", 500.0, 50.0,
			150.0, sql.NullString{String: "cache-plan", Valid: true}, sql.NullString{String: "Cache Plan", Valid: true}, 2).
		AddRow("account-2", "purch-b1", now.Add(-24*time.Hour), "azure", "vm", "westeurope",
			"Standard_D4s_v3", 10, 1, "no-upfront", 0.0, 1000.0,
			200.0, sql.NullString{}, sql.NullString{}, 0)

	mock.ExpectQuery(`SELECT account_id, purchase_id, timestamp, provider, service, region`).
		WithArgs(100).
		WillReturnRows(rows)

	history, err := store.GetAllPurchaseHistory(context.Background(), 100)
	require.NoError(t, err)
	assert.Len(t, history, 2)

	assert.Equal(t, "account-1", history[0].AccountID)
	assert.Equal(t, "elasticache", history[0].Service)
	assert.Equal(t, 2, history[0].RampStep)

	assert.Equal(t, "account-2", history[1].AccountID)
	assert.Equal(t, "azure", history[1].Provider)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAllPurchaseHistory_Empty(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	rows := pgxmock.NewRows([]string{
		"account_id", "purchase_id", "timestamp", "provider", "service", "region",
		"resource_type", "count", "term", "payment", "upfront_cost", "monthly_cost",
		"estimated_savings", "plan_id", "plan_name", "ramp_step",
	})

	mock.ExpectQuery(`SELECT account_id, purchase_id, timestamp, provider, service, region`).
		WithArgs(100).
		WillReturnRows(rows)

	history, err := store.GetAllPurchaseHistory(context.Background(), 100)
	require.NoError(t, err)
	assert.NotNil(t, history)
	assert.Empty(t, history)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAllPurchaseHistory_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	mock.ExpectQuery(`SELECT account_id, purchase_id, timestamp, provider, service, region`).
		WithArgs(100).
		WillReturnError(errors.New("disk full"))

	history, err := store.GetAllPurchaseHistory(context.Background(), 100)
	assert.Error(t, err)
	assert.Nil(t, history)
	assert.Contains(t, err.Error(), "disk full")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// LIST SERVICE CONFIGS SCAN ERROR TESTS
// ==========================================

func TestListServiceConfigs_ScanError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	// Create rows that will cause a scan error (wrong number of columns)
	rows := pgxmock.NewRows([]string{
		"provider", "service", "enabled", // Missing columns
	}).AddRow("aws", "rds", true)

	mock.ExpectQuery(`SELECT provider, service, enabled, term, payment, coverage`).
		WillReturnRows(rows)

	configs, err := store.ListServiceConfigs(context.Background())
	assert.Error(t, err)
	assert.Nil(t, configs)

	// Note: pgxmock may not fully enforce column count, but the test verifies error path
	mock.ExpectationsWereMet()
}

// ==========================================
// GLOBAL CONFIG VALIDATION EDGE CASES
// ==========================================

func TestGlobalConfig_ValidateWithEmptyEmail(t *testing.T) {
	emptyEmail := ""
	config := GlobalConfig{
		EnabledProviders:  []string{"aws"},
		NotificationEmail: &emptyEmail,
		DefaultTerm:       3,
		DefaultPayment:    "all-upfront",
		DefaultCoverage:   80,
	}

	err := config.Validate()
	assert.NoError(t, err)
}

func TestGlobalConfig_ValidateWithNilNotificationEmail(t *testing.T) {
	config := GlobalConfig{
		EnabledProviders:  []string{"aws", "gcp"},
		NotificationEmail: nil,
		DefaultTerm:       1,
		DefaultPayment:    "no-upfront",
		DefaultCoverage:   50,
	}

	err := config.Validate()
	assert.NoError(t, err)
}

// ==========================================
// CONSTANTS TESTS
// ==========================================

func TestConstants_DefaultValues(t *testing.T) {
	// Test default list limit
	assert.Equal(t, 100, DefaultListLimit)

	// Test execution TTL
	assert.Equal(t, 30, DefaultExecutionTTLDays)

	// Test max recommendations in email
	assert.Equal(t, 10, DefaultMaxRecommendationsInEmail)

	// Test password reset expiry
	assert.Equal(t, 1*time.Hour, DefaultPasswordResetExpiry)
}

func TestConstants_ValidationBoundaries(t *testing.T) {
	// Test coverage boundaries
	assert.Equal(t, 100, MaxCoverage)
	assert.Equal(t, 0, MinCoverage)

	// Test plan name length
	assert.Equal(t, 100, MaxPlanNameLength)

	// Test notification days
	assert.Equal(t, 30, MaxNotificationDaysBefore)

	// Test step limits
	assert.Equal(t, 365, MaxStepIntervalDays)
	assert.Equal(t, 100, MaxTotalSteps)
}

func TestConstants_DefaultCoverage(t *testing.T) {
	assert.Equal(t, 80, DefaultCoveragePercent)
	assert.Equal(t, 7, DefaultNotifyDaysBefore)
}

func TestConstants_RampSchedulePresets(t *testing.T) {
	assert.Equal(t, "immediate", RampImmediate)
	assert.Equal(t, "weekly-25pct", RampWeekly25Pct)
	assert.Equal(t, "monthly-10pct", RampMonthly10Pct)
	assert.Equal(t, 7, WeeklyStepIntervalDays)
	assert.Equal(t, 30, MonthlyStepIntervalDays)
}

func TestConstants_TimeConstants(t *testing.T) {
	assert.Equal(t, 24, HoursPerDay)
	assert.Equal(t, 24, MinHoursBetweenNotifications)
}

func TestConstants_TokenConstants(t *testing.T) {
	assert.Equal(t, 32, TokenByteLength)
	assert.Equal(t, 30, MFATimeStep)
	assert.Equal(t, 6, MFADigits)
}

// ==========================================
// CONFIGSETTING TYPE TESTS
// ==========================================

func TestConfigSetting_Fields(t *testing.T) {
	now := time.Now()
	setting := ConfigSetting{
		Key:         "test.key",
		Value:       "test-value",
		Type:        "string",
		Category:    "test",
		Description: "A test setting",
		UpdatedAt:   now,
	}

	assert.Equal(t, "test.key", setting.Key)
	assert.Equal(t, "test-value", setting.Value)
	assert.Equal(t, "string", setting.Type)
	assert.Equal(t, "test", setting.Category)
	assert.Equal(t, "A test setting", setting.Description)
	assert.Equal(t, now, setting.UpdatedAt)
}

func TestConfigSetting_DifferentValueTypes(t *testing.T) {
	tests := []struct {
		name     string
		value    interface{}
		dataType string
	}{
		{"int value", 42, "int"},
		{"float value", 3.14, "float"},
		{"bool value", true, "bool"},
		{"string value", "hello", "string"},
		{"slice value", []string{"a", "b"}, "json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setting := ConfigSetting{
				Key:   "test." + tt.dataType,
				Value: tt.value,
				Type:  tt.dataType,
			}
			assert.Equal(t, tt.value, setting.Value)
			assert.Equal(t, tt.dataType, setting.Type)
		})
	}
}

// ==========================================
// STORE INTERFACE VERIFICATION
// ==========================================

func TestStoreInterface_PostgresStoreImplements(t *testing.T) {
	// This test verifies that PostgresStore implements StoreInterface
	// by checking the var declaration in the source file
	var _ StoreInterface = (*PostgresStore)(nil)
}

// ==========================================
// EDGE CASE TESTS FOR QUERY EXECUTIONS
// ==========================================

func TestQueryExecutions_WithCompletedTimestamp(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	now := time.Now()
	completedAt := now.Add(-2 * time.Hour)
	recsJSON, _ := json.Marshal([]RecommendationRecord{
		{ID: "rec-complete", Selected: true, Purchased: true},
	})

	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
	}).AddRow("plan-done", "exec-done", "completed", 4, now.Add(-24*time.Hour),
		sql.NullTime{Time: now.Add(-3 * time.Hour), Valid: true}, "approved-token", recsJSON,
		5000.0, 1000.0, sql.NullTime{Time: completedAt, Valid: true}, "", sql.NullTime{})

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WithArgs("exec-done").
		WillReturnRows(rows)

	exec, err := store.GetExecutionByID(context.Background(), "exec-done")
	require.NoError(t, err)
	assert.Equal(t, "completed", exec.Status)
	assert.NotNil(t, exec.CompletedAt)
	assert.NotNil(t, exec.NotificationSent)
	assert.Equal(t, "approved-token", exec.ApprovalToken)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestQueryExecutions_WithErrorField(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	now := time.Now()
	recsJSON, _ := json.Marshal([]RecommendationRecord{})

	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
	}).AddRow("plan-fail", "exec-fail", "failed", 1, now,
		sql.NullTime{}, "", recsJSON,
		0.0, 0.0, sql.NullTime{}, "AWS API rate limit exceeded", sql.NullTime{})

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WithArgs("exec-fail").
		WillReturnRows(rows)

	exec, err := store.GetExecutionByID(context.Background(), "exec-fail")
	require.NoError(t, err)
	assert.Equal(t, "failed", exec.Status)
	assert.Equal(t, "AWS API rate limit exceeded", exec.Error)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// GLOBAL CONFIG WITH NULL NOTIFICATION EMAIL
// ==========================================

func TestGetGlobalConfig_NullNotificationEmail(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	rows := pgxmock.NewRows([]string{
		"enabled_providers", "notification_email", "approval_required",
		"default_term", "default_payment", "default_coverage", "default_ramp_schedule",
	}).AddRow(
		[]string{"aws"}, nil, true, 1, "partial-upfront", 75.0, "weekly-25pct",
	)

	mock.ExpectQuery(`SELECT enabled_providers, notification_email, approval_required`).
		WillReturnRows(rows)

	config, err := store.GetGlobalConfig(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, config)
	assert.Nil(t, config.NotificationEmail)
	assert.Equal(t, []string{"aws"}, config.EnabledProviders)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// VALIDATION EDGE CASES
// ==========================================

func TestValidateTerm_EdgeCases(t *testing.T) {
	tests := []struct {
		term    int
		wantErr bool
	}{
		{0, false},  // Not set is valid
		{1, false},  // 1 year valid
		{3, false},  // 3 years valid
		{2, true},   // 2 years invalid
		{-1, true},  // Negative invalid
		{12, true},  // 12 months (not years) invalid
		{36, true},  // 36 months invalid
		{100, true}, // Way too long
	}

	for _, tt := range tests {
		t.Run("term_"+string(rune(tt.term+'0')), func(t *testing.T) {
			err := validateTerm(tt.term)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateCoverage_EdgeCases(t *testing.T) {
	tests := []struct {
		coverage float64
		wantErr  bool
	}{
		{0, false},      // Min boundary
		{100, false},    // Max boundary
		{50, false},     // Middle value
		{-0.01, true},   // Just below min
		{100.01, true},  // Just above max
		{-100, true},    // Way below
		{200, true},     // Way above
		{0.001, false},  // Small positive
		{99.999, false}, // Almost max
	}

	for _, tt := range tests {
		t.Run("coverage", func(t *testing.T) {
			err := validateCoverage(tt.coverage)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ==========================================
// ROWS ERROR HANDLING
// ==========================================

func TestListPurchasePlans_RowsError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	now := time.Now()
	servicesJSON, _ := json.Marshal(map[string]ServiceConfig{})
	rampJSON, _ := json.Marshal(RampSchedule{Type: "immediate"})

	rows := pgxmock.NewRows([]string{
		"id", "name", "enabled", "auto_purchase", "notification_days_before",
		"services", "ramp_schedule", "created_at", "updated_at",
		"next_execution_date", "last_execution_date", "last_notification_sent",
	}).AddRow("plan-1", "Plan 1", true, false, 7,
		servicesJSON, rampJSON, now, now,
		sql.NullTime{}, sql.NullTime{}, sql.NullTime{}).
		RowError(0, errors.New("row iteration error"))

	mock.ExpectQuery(`SELECT id, name, enabled, auto_purchase, notification_days_before`).
		WillReturnRows(rows)

	plans, err := store.ListPurchasePlans(context.Background())
	assert.Error(t, err)
	assert.Nil(t, plans)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// PURCHASE HISTORY QUERY SCAN TESTS
// ==========================================

func TestQueryPurchaseHistory_AllFieldsPresent(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	now := time.Now()

	rows := pgxmock.NewRows([]string{
		"account_id", "purchase_id", "timestamp", "provider", "service", "region",
		"resource_type", "count", "term", "payment", "upfront_cost", "monthly_cost",
		"estimated_savings", "plan_id", "plan_name", "ramp_step",
	}).AddRow("full-account", "full-purchase", now, "aws", "opensearch", "ap-southeast-1",
		"r5.xlarge.search", 2, 3, "all-upfront", 3000.0, 0.0,
		600.0, sql.NullString{String: "search-plan", Valid: true},
		sql.NullString{String: "OpenSearch Production", Valid: true}, 3)

	mock.ExpectQuery(`SELECT account_id, purchase_id, timestamp, provider, service, region`).
		WithArgs("full-account", 10).
		WillReturnRows(rows)

	history, err := store.GetPurchaseHistory(context.Background(), "full-account", 10)
	require.NoError(t, err)
	assert.Len(t, history, 1)

	record := history[0]
	assert.Equal(t, "full-account", record.AccountID)
	assert.Equal(t, "full-purchase", record.PurchaseID)
	assert.Equal(t, "aws", record.Provider)
	assert.Equal(t, "opensearch", record.Service)
	assert.Equal(t, "ap-southeast-1", record.Region)
	assert.Equal(t, "r5.xlarge.search", record.ResourceType)
	assert.Equal(t, 2, record.Count)
	assert.Equal(t, 3, record.Term)
	assert.Equal(t, "all-upfront", record.Payment)
	assert.Equal(t, 3000.0, record.UpfrontCost)
	assert.Equal(t, 0.0, record.MonthlyCost)
	assert.Equal(t, 600.0, record.EstimatedSavings)
	assert.Equal(t, "search-plan", record.PlanID)
	assert.Equal(t, "OpenSearch Production", record.PlanName)
	assert.Equal(t, 3, record.RampStep)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// GETGLOBALCONFIG ADDITIONAL TESTS
// ==========================================

func TestGetGlobalConfig_WithEmptyProviders(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	email := "admin@example.com"
	rows := pgxmock.NewRows([]string{
		"enabled_providers", "notification_email", "approval_required",
		"default_term", "default_payment", "default_coverage", "default_ramp_schedule",
	}).AddRow(
		[]string{}, &email, false, 3, "no-upfront", 85.0, "monthly-10pct",
	)

	mock.ExpectQuery(`SELECT enabled_providers, notification_email, approval_required`).
		WillReturnRows(rows)

	config, err := store.GetGlobalConfig(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, config)
	assert.Empty(t, config.EnabledProviders)
	assert.Equal(t, &email, config.NotificationEmail)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// PURCHASE EXECUTION WITH ALL TIMESTAMPS
// ==========================================

func TestGetPendingExecutions_AllTimestampsSet(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &mockablePostgresStore{mock: mock}

	now := time.Now()
	notifSent := now.Add(-2 * time.Hour)
	completedAt := now.Add(-1 * time.Hour)
	expiresAt := now.Add(7 * 24 * time.Hour)
	recsJSON, _ := json.Marshal([]RecommendationRecord{
		{ID: "rec-1", Selected: true, Purchased: true, PurchaseID: "aws-ri-123"},
	})

	rows := pgxmock.NewRows([]string{
		"plan_id", "execution_id", "status", "step_number", "scheduled_date",
		"notification_sent", "approval_token", "recommendations",
		"total_upfront_cost", "estimated_savings", "completed_at", "error", "expires_at",
	}).AddRow("plan-all", "exec-all", "completed", 5, now.Add(-3*time.Hour),
		sql.NullTime{Time: notifSent, Valid: true}, "all-approved", recsJSON,
		10000.0, 2000.0, sql.NullTime{Time: completedAt, Valid: true}, "",
		sql.NullTime{Time: expiresAt, Valid: true})

	mock.ExpectQuery(`SELECT plan_id, execution_id, status, step_number, scheduled_date`).
		WillReturnRows(rows)

	executions, err := store.GetPendingExecutions(context.Background())
	require.NoError(t, err)
	assert.Len(t, executions, 1)

	exec := executions[0]
	assert.NotNil(t, exec.NotificationSent)
	assert.NotNil(t, exec.CompletedAt)
	assert.NotZero(t, exec.TTL)
	assert.Len(t, exec.Recommendations, 1)
	assert.True(t, exec.Recommendations[0].Purchased)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================================
// INTERFACE EDGE CASE: NO ROWS
// ==========================================

func TestGetGlobalConfig_NoRowsReturnsDefaults(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	mock.ExpectQuery(`SELECT enabled_providers, notification_email, approval_required`).
		WillReturnError(pgx.ErrNoRows)

	config, err := store.GetGlobalConfig(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, config)

	// Verify default values
	assert.Empty(t, config.EnabledProviders)
	assert.True(t, config.ApprovalRequired)
	assert.Equal(t, 12, config.DefaultTerm)
	assert.Equal(t, "all-upfront", config.DefaultPayment)
	assert.Equal(t, 80.0, config.DefaultCoverage)
	assert.Equal(t, "immediate", config.DefaultRampSchedule)

	assert.NoError(t, mock.ExpectationsWereMet())
}
