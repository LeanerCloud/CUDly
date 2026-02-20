package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockDBInterface defines the interface that matches database.Connection methods
type MockDBInterface interface {
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
}

// testablePostgresStore is a test-only wrapper that allows mocking
type testablePostgresStore struct {
	mock pgxmock.PgxPoolIface
}

func (s *testablePostgresStore) GetGlobalConfig(ctx context.Context) (*GlobalConfig, error) {
	query := `
		SELECT enabled_providers, notification_email, approval_required,
		       default_term, default_payment, default_coverage, default_ramp_schedule
		FROM global_config
		WHERE id = 1
	`

	var config GlobalConfig
	var enabledProviders []string

	err := s.mock.QueryRow(ctx, query).Scan(
		&enabledProviders,
		&config.NotificationEmail,
		&config.ApprovalRequired,
		&config.DefaultTerm,
		&config.DefaultPayment,
		&config.DefaultCoverage,
		&config.DefaultRampSchedule,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return &GlobalConfig{
				EnabledProviders:    []string{},
				ApprovalRequired:    true,
				DefaultTerm:         3,
				DefaultPayment:      "all-upfront",
				DefaultCoverage:     80.0,
				DefaultRampSchedule: "immediate",
			}, nil
		}
		return nil, err
	}

	config.EnabledProviders = enabledProviders
	return &config, nil
}

func (s *testablePostgresStore) SaveGlobalConfig(ctx context.Context, config *GlobalConfig) error {
	if config.EnabledProviders == nil {
		config.EnabledProviders = []string{}
	}

	query := `
		INSERT INTO global_config (
			id, enabled_providers, notification_email, approval_required,
			default_term, default_payment, default_coverage, default_ramp_schedule
		) VALUES (1, $1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			enabled_providers = $1,
			notification_email = $2,
			approval_required = $3,
			default_term = $4,
			default_payment = $5,
			default_coverage = $6,
			default_ramp_schedule = $7,
			updated_at = NOW()
	`

	_, err := s.mock.Exec(ctx, query,
		config.EnabledProviders,
		config.NotificationEmail,
		config.ApprovalRequired,
		config.DefaultTerm,
		config.DefaultPayment,
		config.DefaultCoverage,
		config.DefaultRampSchedule,
	)

	return err
}

func (s *testablePostgresStore) GetServiceConfig(ctx context.Context, provider, service string) (*ServiceConfig, error) {
	query := `
		SELECT provider, service, enabled, term, payment, coverage, ramp_schedule,
		       include_engines, exclude_engines, include_regions, exclude_regions,
		       include_types, exclude_types
		FROM service_configs
		WHERE provider = $1 AND service = $2
	`

	var config ServiceConfig
	var includeEngines, excludeEngines, includeRegions, excludeRegions, includeTypes, excludeTypes []string

	err := s.mock.QueryRow(ctx, query, provider, service).Scan(
		&config.Provider,
		&config.Service,
		&config.Enabled,
		&config.Term,
		&config.Payment,
		&config.Coverage,
		&config.RampSchedule,
		&includeEngines,
		&excludeEngines,
		&includeRegions,
		&excludeRegions,
		&includeTypes,
		&excludeTypes,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, errors.New("service config not found")
		}
		return nil, err
	}

	config.IncludeEngines = includeEngines
	config.ExcludeEngines = excludeEngines
	config.IncludeRegions = includeRegions
	config.ExcludeRegions = excludeRegions
	config.IncludeTypes = includeTypes
	config.ExcludeTypes = excludeTypes

	return &config, nil
}

func (s *testablePostgresStore) SaveServiceConfig(ctx context.Context, config *ServiceConfig) error {
	query := `
		INSERT INTO service_configs (
			provider, service, enabled, term, payment, coverage, ramp_schedule,
			include_engines, exclude_engines, include_regions, exclude_regions,
			include_types, exclude_types
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (provider, service) DO UPDATE SET
			enabled = $3,
			term = $4,
			payment = $5,
			coverage = $6,
			ramp_schedule = $7,
			include_engines = $8,
			exclude_engines = $9,
			include_regions = $10,
			exclude_regions = $11,
			include_types = $12,
			exclude_types = $13,
			updated_at = NOW()
	`

	_, err := s.mock.Exec(ctx, query,
		config.Provider,
		config.Service,
		config.Enabled,
		config.Term,
		config.Payment,
		config.Coverage,
		config.RampSchedule,
		config.IncludeEngines,
		config.ExcludeEngines,
		config.IncludeRegions,
		config.ExcludeRegions,
		config.IncludeTypes,
		config.ExcludeTypes,
	)

	return err
}

func (s *testablePostgresStore) ListServiceConfigs(ctx context.Context) ([]ServiceConfig, error) {
	query := `
		SELECT provider, service, enabled, term, payment, coverage, ramp_schedule,
		       include_engines, exclude_engines, include_regions, exclude_regions,
		       include_types, exclude_types
		FROM service_configs
		ORDER BY provider, service
	`

	rows, err := s.mock.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configs := make([]ServiceConfig, 0)
	for rows.Next() {
		var config ServiceConfig
		var includeEngines, excludeEngines, includeRegions, excludeRegions, includeTypes, excludeTypes []string

		err := rows.Scan(
			&config.Provider,
			&config.Service,
			&config.Enabled,
			&config.Term,
			&config.Payment,
			&config.Coverage,
			&config.RampSchedule,
			&includeEngines,
			&excludeEngines,
			&includeRegions,
			&excludeRegions,
			&includeTypes,
			&excludeTypes,
		)
		if err != nil {
			return nil, err
		}

		config.IncludeEngines = includeEngines
		config.ExcludeEngines = excludeEngines
		config.IncludeRegions = includeRegions
		config.ExcludeRegions = excludeRegions
		config.IncludeTypes = includeTypes
		config.ExcludeTypes = excludeTypes

		configs = append(configs, config)
	}

	return configs, rows.Err()
}

func (s *testablePostgresStore) DeletePurchasePlan(ctx context.Context, planID string) error {
	query := `DELETE FROM purchase_plans WHERE id = $1`

	result, err := s.mock.Exec(ctx, query, planID)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return errors.New("purchase plan not found")
	}

	return nil
}

func (s *testablePostgresStore) GetPurchasePlan(ctx context.Context, planID string) (*PurchasePlan, error) {
	query := `
		SELECT id, name, enabled, auto_purchase, notification_days_before,
		       services, ramp_schedule, created_at, updated_at,
		       next_execution_date, last_execution_date, last_notification_sent
		FROM purchase_plans
		WHERE id = $1
	`

	var plan PurchasePlan
	var servicesJSON, rampScheduleJSON []byte
	var nextExecDate, lastExecDate, lastNotifSent sql.NullTime

	err := s.mock.QueryRow(ctx, query, planID).Scan(
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
		if err == pgx.ErrNoRows {
			return nil, errors.New("purchase plan not found")
		}
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

	return &plan, nil
}

// TestGetGlobalConfig_NoRows tests that default config is returned when no rows exist
func TestGetGlobalConfig_NoRows(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	mock.ExpectQuery(`SELECT enabled_providers, notification_email, approval_required`).
		WillReturnError(pgx.ErrNoRows)

	config, err := store.GetGlobalConfig(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, config)
	assert.Empty(t, config.EnabledProviders)
	assert.True(t, config.ApprovalRequired)
	assert.Equal(t, 3, config.DefaultTerm)
	assert.Equal(t, "all-upfront", config.DefaultPayment)
	assert.Equal(t, 80.0, config.DefaultCoverage)
	assert.Equal(t, "immediate", config.DefaultRampSchedule)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetGlobalConfig_Success tests successful retrieval of global config
func TestGetGlobalConfig_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	email := "test@example.com"
	rows := pgxmock.NewRows([]string{
		"enabled_providers", "notification_email", "approval_required",
		"default_term", "default_payment", "default_coverage", "default_ramp_schedule",
	}).AddRow(
		[]string{"aws", "gcp"}, &email, false, 3, "no-upfront", 90.0, "weekly-25pct",
	)

	mock.ExpectQuery(`SELECT enabled_providers, notification_email, approval_required`).
		WillReturnRows(rows)

	config, err := store.GetGlobalConfig(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, []string{"aws", "gcp"}, config.EnabledProviders)
	assert.Equal(t, &email, config.NotificationEmail)
	assert.False(t, config.ApprovalRequired)
	assert.Equal(t, 3, config.DefaultTerm)
	assert.Equal(t, "no-upfront", config.DefaultPayment)
	assert.Equal(t, 90.0, config.DefaultCoverage)
	assert.Equal(t, "weekly-25pct", config.DefaultRampSchedule)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetGlobalConfig_Error tests error handling
func TestGetGlobalConfig_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	mock.ExpectQuery(`SELECT enabled_providers, notification_email, approval_required`).
		WillReturnError(errors.New("database error"))

	config, err := store.GetGlobalConfig(context.Background())
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "database error")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSaveGlobalConfig_Success tests successful save of global config
func TestSaveGlobalConfig_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	email := "test@example.com"
	config := &GlobalConfig{
		EnabledProviders:    []string{"aws"},
		NotificationEmail:   &email,
		ApprovalRequired:    true,
		DefaultTerm:         3,
		DefaultPayment:      "all-upfront",
		DefaultCoverage:     80.0,
		DefaultRampSchedule: "immediate",
	}

	mock.ExpectExec(`INSERT INTO global_config`).
		WithArgs(
			config.EnabledProviders,
			config.NotificationEmail,
			config.ApprovalRequired,
			config.DefaultTerm,
			config.DefaultPayment,
			config.DefaultCoverage,
			config.DefaultRampSchedule,
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = store.SaveGlobalConfig(context.Background(), config)
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSaveGlobalConfig_NilEnabledProviders tests that nil EnabledProviders gets converted to empty slice
func TestSaveGlobalConfig_NilEnabledProviders(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	config := &GlobalConfig{
		EnabledProviders: nil,
		DefaultTerm:      1,
		DefaultPayment:   "no-upfront",
		DefaultCoverage:  70.0,
	}

	mock.ExpectExec(`INSERT INTO global_config`).
		WithArgs(
			[]string{}, // Should be converted from nil to empty slice
			config.NotificationEmail,
			config.ApprovalRequired,
			config.DefaultTerm,
			config.DefaultPayment,
			config.DefaultCoverage,
			config.DefaultRampSchedule,
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = store.SaveGlobalConfig(context.Background(), config)
	assert.NoError(t, err)
	assert.NotNil(t, config.EnabledProviders)
	assert.Empty(t, config.EnabledProviders)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSaveGlobalConfig_Error tests error handling
func TestSaveGlobalConfig_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	config := &GlobalConfig{
		EnabledProviders: []string{"aws"},
	}

	mock.ExpectExec(`INSERT INTO global_config`).
		WithArgs(
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
		).
		WillReturnError(errors.New("database error"))

	err = store.SaveGlobalConfig(context.Background(), config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database error")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetServiceConfig_Success tests successful retrieval of service config
func TestGetServiceConfig_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	rows := pgxmock.NewRows([]string{
		"provider", "service", "enabled", "term", "payment", "coverage", "ramp_schedule",
		"include_engines", "exclude_engines", "include_regions", "exclude_regions",
		"include_types", "exclude_types",
	}).AddRow(
		"aws", "rds", true, 3, "all-upfront", 80.0, "immediate",
		[]string{"postgres", "mysql"}, []string{}, []string{"us-east-1"}, []string{},
		[]string{"db.r5.large"}, []string{},
	)

	mock.ExpectQuery(`SELECT provider, service, enabled, term, payment, coverage`).
		WithArgs("aws", "rds").
		WillReturnRows(rows)

	config, err := store.GetServiceConfig(context.Background(), "aws", "rds")
	require.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "aws", config.Provider)
	assert.Equal(t, "rds", config.Service)
	assert.True(t, config.Enabled)
	assert.Equal(t, 3, config.Term)
	assert.Equal(t, "all-upfront", config.Payment)
	assert.Equal(t, 80.0, config.Coverage)
	assert.Equal(t, []string{"postgres", "mysql"}, config.IncludeEngines)
	assert.Equal(t, []string{"us-east-1"}, config.IncludeRegions)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetServiceConfig_NotFound tests service config not found
func TestGetServiceConfig_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	mock.ExpectQuery(`SELECT provider, service, enabled, term, payment, coverage`).
		WithArgs("aws", "nonexistent").
		WillReturnError(pgx.ErrNoRows)

	config, err := store.GetServiceConfig(context.Background(), "aws", "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "not found")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetServiceConfig_Error tests error handling
func TestGetServiceConfig_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	mock.ExpectQuery(`SELECT provider, service, enabled, term, payment, coverage`).
		WithArgs("aws", "rds").
		WillReturnError(errors.New("database error"))

	config, err := store.GetServiceConfig(context.Background(), "aws", "rds")
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "database error")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSaveServiceConfig_Success tests successful save of service config
func TestSaveServiceConfig_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	config := &ServiceConfig{
		Provider:       "aws",
		Service:        "rds",
		Enabled:        true,
		Term:           3,
		Payment:        "all-upfront",
		Coverage:       80.0,
		RampSchedule:   "immediate",
		IncludeEngines: []string{"postgres"},
		ExcludeEngines: []string{},
		IncludeRegions: []string{"us-east-1"},
		ExcludeRegions: []string{},
		IncludeTypes:   []string{},
		ExcludeTypes:   []string{},
	}

	mock.ExpectExec(`INSERT INTO service_configs`).
		WithArgs(
			config.Provider, config.Service, config.Enabled, config.Term,
			config.Payment, config.Coverage, config.RampSchedule,
			config.IncludeEngines, config.ExcludeEngines,
			config.IncludeRegions, config.ExcludeRegions,
			config.IncludeTypes, config.ExcludeTypes,
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = store.SaveServiceConfig(context.Background(), config)
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSaveServiceConfig_Error tests error handling
func TestSaveServiceConfig_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	config := &ServiceConfig{
		Provider: "aws",
		Service:  "rds",
	}

	mock.ExpectExec(`INSERT INTO service_configs`).
		WithArgs(
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
			pgxmock.AnyArg(),
		).
		WillReturnError(errors.New("database error"))

	err = store.SaveServiceConfig(context.Background(), config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database error")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListServiceConfigs_Success tests successful listing of service configs
func TestListServiceConfigs_Success(t *testing.T) {
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
		AddRow("aws", "elasticache", true, 1, "no-upfront", 70.0, "weekly",
			[]string{}, []string{}, []string{"us-west-2"}, []string{}, []string{}, []string{})

	mock.ExpectQuery(`SELECT provider, service, enabled, term, payment, coverage`).
		WillReturnRows(rows)

	configs, err := store.ListServiceConfigs(context.Background())
	require.NoError(t, err)
	assert.Len(t, configs, 2)
	assert.Equal(t, "aws", configs[0].Provider)
	assert.Equal(t, "rds", configs[0].Service)
	assert.Equal(t, "elasticache", configs[1].Service)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListServiceConfigs_Empty tests listing when no configs exist
func TestListServiceConfigs_Empty(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	rows := pgxmock.NewRows([]string{
		"provider", "service", "enabled", "term", "payment", "coverage", "ramp_schedule",
		"include_engines", "exclude_engines", "include_regions", "exclude_regions",
		"include_types", "exclude_types",
	})

	mock.ExpectQuery(`SELECT provider, service, enabled, term, payment, coverage`).
		WillReturnRows(rows)

	configs, err := store.ListServiceConfigs(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, configs)
	assert.Empty(t, configs)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListServiceConfigs_Error tests error handling
func TestListServiceConfigs_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	mock.ExpectQuery(`SELECT provider, service, enabled, term, payment, coverage`).
		WillReturnError(errors.New("database error"))

	configs, err := store.ListServiceConfigs(context.Background())
	assert.Error(t, err)
	assert.Nil(t, configs)
	assert.Contains(t, err.Error(), "database error")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestDeletePurchasePlan_Success tests successful deletion
func TestDeletePurchasePlan_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	mock.ExpectExec(`DELETE FROM purchase_plans WHERE id = \$1`).
		WithArgs("plan-123").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	err = store.DeletePurchasePlan(context.Background(), "plan-123")
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestDeletePurchasePlan_NotFound tests deletion of non-existent plan
func TestDeletePurchasePlan_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	mock.ExpectExec(`DELETE FROM purchase_plans WHERE id = \$1`).
		WithArgs("nonexistent").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	err = store.DeletePurchasePlan(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestDeletePurchasePlan_Error tests error handling
func TestDeletePurchasePlan_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	mock.ExpectExec(`DELETE FROM purchase_plans WHERE id = \$1`).
		WithArgs("plan-123").
		WillReturnError(errors.New("database error"))

	err = store.DeletePurchasePlan(context.Background(), "plan-123")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database error")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetPurchasePlan_Success tests successful retrieval of purchase plan
func TestGetPurchasePlan_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	servicesJSON, _ := json.Marshal(map[string]ServiceConfig{
		"aws:rds": {Provider: "aws", Service: "rds", Enabled: true},
	})
	rampScheduleJSON, _ := json.Marshal(RampSchedule{Type: "immediate", PercentPerStep: 100, TotalSteps: 1})
	now := time.Now()
	nextExec := now.Add(24 * time.Hour)

	rows := pgxmock.NewRows([]string{
		"id", "name", "enabled", "auto_purchase", "notification_days_before",
		"services", "ramp_schedule", "created_at", "updated_at",
		"next_execution_date", "last_execution_date", "last_notification_sent",
	}).AddRow(
		"plan-123", "Test Plan", true, false, 7,
		servicesJSON, rampScheduleJSON, now, now,
		sql.NullTime{Time: nextExec, Valid: true}, sql.NullTime{}, sql.NullTime{},
	)

	mock.ExpectQuery(`SELECT id, name, enabled, auto_purchase, notification_days_before`).
		WithArgs("plan-123").
		WillReturnRows(rows)

	plan, err := store.GetPurchasePlan(context.Background(), "plan-123")
	require.NoError(t, err)
	assert.NotNil(t, plan)
	assert.Equal(t, "plan-123", plan.ID)
	assert.Equal(t, "Test Plan", plan.Name)
	assert.True(t, plan.Enabled)
	assert.False(t, plan.AutoPurchase)
	assert.Equal(t, 7, plan.NotificationDaysBefore)
	assert.NotNil(t, plan.NextExecutionDate)
	assert.Nil(t, plan.LastExecutionDate)
	assert.Nil(t, plan.LastNotificationSent)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetPurchasePlan_NotFound tests retrieval of non-existent plan
func TestGetPurchasePlan_NotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	mock.ExpectQuery(`SELECT id, name, enabled, auto_purchase, notification_days_before`).
		WithArgs("nonexistent").
		WillReturnError(pgx.ErrNoRows)

	plan, err := store.GetPurchasePlan(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, plan)
	assert.Contains(t, err.Error(), "not found")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetPurchasePlan_Error tests error handling
func TestGetPurchasePlan_Error(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	mock.ExpectQuery(`SELECT id, name, enabled, auto_purchase, notification_days_before`).
		WithArgs("plan-123").
		WillReturnError(errors.New("database error"))

	plan, err := store.GetPurchasePlan(context.Background(), "plan-123")
	assert.Error(t, err)
	assert.Nil(t, plan)
	assert.Contains(t, err.Error(), "database error")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetPurchasePlan_InvalidJSON tests handling of invalid JSON in services field
func TestGetPurchasePlan_InvalidServicesJSON(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	now := time.Now()
	rampScheduleJSON, _ := json.Marshal(RampSchedule{Type: "immediate"})

	rows := pgxmock.NewRows([]string{
		"id", "name", "enabled", "auto_purchase", "notification_days_before",
		"services", "ramp_schedule", "created_at", "updated_at",
		"next_execution_date", "last_execution_date", "last_notification_sent",
	}).AddRow(
		"plan-123", "Test Plan", true, false, 7,
		[]byte("invalid json"), rampScheduleJSON, now, now,
		sql.NullTime{}, sql.NullTime{}, sql.NullTime{},
	)

	mock.ExpectQuery(`SELECT id, name, enabled, auto_purchase, notification_days_before`).
		WithArgs("plan-123").
		WillReturnRows(rows)

	plan, err := store.GetPurchasePlan(context.Background(), "plan-123")
	assert.Error(t, err)
	assert.Nil(t, plan)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetPurchasePlan_InvalidRampScheduleJSON tests handling of invalid JSON in ramp_schedule field
func TestGetPurchasePlan_InvalidRampScheduleJSON(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	now := time.Now()
	servicesJSON, _ := json.Marshal(map[string]ServiceConfig{})

	rows := pgxmock.NewRows([]string{
		"id", "name", "enabled", "auto_purchase", "notification_days_before",
		"services", "ramp_schedule", "created_at", "updated_at",
		"next_execution_date", "last_execution_date", "last_notification_sent",
	}).AddRow(
		"plan-123", "Test Plan", true, false, 7,
		servicesJSON, []byte("invalid json"), now, now,
		sql.NullTime{}, sql.NullTime{}, sql.NullTime{},
	)

	mock.ExpectQuery(`SELECT id, name, enabled, auto_purchase, notification_days_before`).
		WithArgs("plan-123").
		WillReturnRows(rows)

	plan, err := store.GetPurchasePlan(context.Background(), "plan-123")
	assert.Error(t, err)
	assert.Nil(t, plan)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetPurchasePlan_AllNullableTimestampsSet tests when all nullable timestamps are set
func TestGetPurchasePlan_AllNullableTimestampsSet(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	store := &testablePostgresStore{mock: mock}

	servicesJSON, _ := json.Marshal(map[string]ServiceConfig{})
	rampScheduleJSON, _ := json.Marshal(RampSchedule{Type: "immediate"})
	now := time.Now()
	nextExec := now.Add(24 * time.Hour)
	lastExec := now.Add(-24 * time.Hour)
	lastNotif := now.Add(-12 * time.Hour)

	rows := pgxmock.NewRows([]string{
		"id", "name", "enabled", "auto_purchase", "notification_days_before",
		"services", "ramp_schedule", "created_at", "updated_at",
		"next_execution_date", "last_execution_date", "last_notification_sent",
	}).AddRow(
		"plan-123", "Test Plan", true, false, 7,
		servicesJSON, rampScheduleJSON, now, now,
		sql.NullTime{Time: nextExec, Valid: true},
		sql.NullTime{Time: lastExec, Valid: true},
		sql.NullTime{Time: lastNotif, Valid: true},
	)

	mock.ExpectQuery(`SELECT id, name, enabled, auto_purchase, notification_days_before`).
		WithArgs("plan-123").
		WillReturnRows(rows)

	plan, err := store.GetPurchasePlan(context.Background(), "plan-123")
	require.NoError(t, err)
	assert.NotNil(t, plan)
	assert.NotNil(t, plan.NextExecutionDate)
	assert.NotNil(t, plan.LastExecutionDate)
	assert.NotNil(t, plan.LastNotificationSent)

	assert.NoError(t, mock.ExpectationsWereMet())
}
