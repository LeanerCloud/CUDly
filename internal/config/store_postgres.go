package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PostgresStore implements StoreInterface using PostgreSQL
type PostgresStore struct {
	db *database.Connection
}

// NewPostgresStore creates a new PostgreSQL-backed config store
func NewPostgresStore(db *database.Connection) *PostgresStore {
	return &PostgresStore{db: db}
}

// Verify PostgresStore implements StoreInterface
var _ StoreInterface = (*PostgresStore)(nil)

// ==========================================
// GLOBAL CONFIGURATION
// ==========================================

// GetGlobalConfig retrieves the global configuration
func (s *PostgresStore) GetGlobalConfig(ctx context.Context) (*GlobalConfig, error) {
	query := `
		SELECT enabled_providers, notification_email, approval_required,
		       default_term, default_payment, default_coverage, default_ramp_schedule,
		       ri_exchange_enabled, ri_exchange_mode, ri_exchange_utilization_threshold,
		       ri_exchange_max_per_exchange_usd, ri_exchange_max_daily_usd, ri_exchange_lookback_days
		FROM global_config
		WHERE id = 1
	`

	var config GlobalConfig
	var enabledProviders []string

	err := s.db.QueryRow(ctx, query).Scan(
		&enabledProviders,
		&config.NotificationEmail,
		&config.ApprovalRequired,
		&config.DefaultTerm,
		&config.DefaultPayment,
		&config.DefaultCoverage,
		&config.DefaultRampSchedule,
		&config.RIExchangeEnabled,
		&config.RIExchangeMode,
		&config.RIExchangeUtilizationThreshold,
		&config.RIExchangeMaxPerExchangeUSD,
		&config.RIExchangeMaxDailyUSD,
		&config.RIExchangeLookbackDays,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			// Return default config if none exists
			return &GlobalConfig{
				EnabledProviders:               []string{},
				ApprovalRequired:               true,
				DefaultTerm:                    3,
				DefaultPayment:                 "all-upfront",
				DefaultCoverage:                80.0,
				DefaultRampSchedule:            "immediate",
				RIExchangeMode:                 "manual",
				RIExchangeUtilizationThreshold: 95.0,
				RIExchangeLookbackDays:         30,
			}, nil
		}
		return nil, fmt.Errorf("failed to get global config: %w", err)
	}

	config.EnabledProviders = enabledProviders
	return &config, nil
}

// SaveGlobalConfig saves the global configuration
func (s *PostgresStore) SaveGlobalConfig(ctx context.Context, config *GlobalConfig) error {
	// Ensure EnabledProviders is never nil (empty slice is ok, nil is not)
	if config.EnabledProviders == nil {
		config.EnabledProviders = []string{}
	}

	query := `
		INSERT INTO global_config (
			id, enabled_providers, notification_email, approval_required,
			default_term, default_payment, default_coverage, default_ramp_schedule,
			ri_exchange_enabled, ri_exchange_mode, ri_exchange_utilization_threshold,
			ri_exchange_max_per_exchange_usd, ri_exchange_max_daily_usd, ri_exchange_lookback_days
		) VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (id) DO UPDATE SET
			enabled_providers = $1,
			notification_email = $2,
			approval_required = $3,
			default_term = $4,
			default_payment = $5,
			default_coverage = $6,
			default_ramp_schedule = $7,
			ri_exchange_enabled = $8,
			ri_exchange_mode = $9,
			ri_exchange_utilization_threshold = $10,
			ri_exchange_max_per_exchange_usd = $11,
			ri_exchange_max_daily_usd = $12,
			ri_exchange_lookback_days = $13,
			updated_at = NOW()
	`

	if config.RIExchangeMode == "" {
		config.RIExchangeMode = "manual"
	}
	if config.RIExchangeLookbackDays == 0 {
		config.RIExchangeLookbackDays = 30
	}
	if config.RIExchangeUtilizationThreshold == 0 {
		config.RIExchangeUtilizationThreshold = 95.0
	}

	_, err := s.db.Exec(ctx, query,
		config.EnabledProviders,
		config.NotificationEmail,
		config.ApprovalRequired,
		config.DefaultTerm,
		config.DefaultPayment,
		config.DefaultCoverage,
		config.DefaultRampSchedule,
		config.RIExchangeEnabled,
		config.RIExchangeMode,
		config.RIExchangeUtilizationThreshold,
		config.RIExchangeMaxPerExchangeUSD,
		config.RIExchangeMaxDailyUSD,
		config.RIExchangeLookbackDays,
	)

	if err != nil {
		return fmt.Errorf("failed to save global config: %w", err)
	}

	return nil
}

// ==========================================
// SERVICE CONFIGURATION
// ==========================================

// GetServiceConfig retrieves configuration for a specific service
func (s *PostgresStore) GetServiceConfig(ctx context.Context, provider, service string) (*ServiceConfig, error) {
	query := `
		SELECT provider, service, enabled, term, payment, coverage, ramp_schedule,
		       include_engines, exclude_engines, include_regions, exclude_regions,
		       include_types, exclude_types
		FROM service_configs
		WHERE provider = $1 AND service = $2
	`

	var config ServiceConfig
	var includeEngines, excludeEngines, includeRegions, excludeRegions, includeTypes, excludeTypes []string

	err := s.db.QueryRow(ctx, query, provider, service).Scan(
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
			return nil, fmt.Errorf("service config not found for %s:%s", provider, service)
		}
		return nil, fmt.Errorf("failed to get service config: %w", err)
	}

	// Map arrays (handle nil)
	config.IncludeEngines = includeEngines
	config.ExcludeEngines = excludeEngines
	config.IncludeRegions = includeRegions
	config.ExcludeRegions = excludeRegions
	config.IncludeTypes = includeTypes
	config.ExcludeTypes = excludeTypes

	return &config, nil
}

// SaveServiceConfig saves configuration for a service
func (s *PostgresStore) SaveServiceConfig(ctx context.Context, config *ServiceConfig) error {
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

	_, err := s.db.Exec(ctx, query,
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

	if err != nil {
		return fmt.Errorf("failed to save service config: %w", err)
	}

	return nil
}

// ListServiceConfigs lists all service configurations
func (s *PostgresStore) ListServiceConfigs(ctx context.Context) ([]ServiceConfig, error) {
	query := `
		SELECT provider, service, enabled, term, payment, coverage, ramp_schedule,
		       include_engines, exclude_engines, include_regions, exclude_regions,
		       include_types, exclude_types
		FROM service_configs
		ORDER BY provider, service
	`

	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list service configs: %w", err)
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
			return nil, fmt.Errorf("failed to scan service config: %w", err)
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

// ==========================================
// PURCHASE PLANS
// ==========================================

// CreatePurchasePlan creates a new purchase plan
func (s *PostgresStore) CreatePurchasePlan(ctx context.Context, plan *PurchasePlan) error {
	// Generate UUID if not provided
	if plan.ID == "" {
		plan.ID = uuid.New().String()
	}

	// Set timestamps
	now := time.Now()
	plan.CreatedAt = now
	plan.UpdatedAt = now

	// Marshal services and ramp_schedule to JSONB
	servicesJSON, err := json.Marshal(plan.Services)
	if err != nil {
		return fmt.Errorf("failed to marshal services: %w", err)
	}

	rampScheduleJSON, err := json.Marshal(plan.RampSchedule)
	if err != nil {
		return fmt.Errorf("failed to marshal ramp_schedule: %w", err)
	}

	query := `
		INSERT INTO purchase_plans (
			id, name, enabled, auto_purchase, notification_days_before,
			services, ramp_schedule, created_at, updated_at,
			next_execution_date, last_execution_date, last_notification_sent
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	_, err = s.db.Exec(ctx, query,
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

	if err != nil {
		return fmt.Errorf("failed to create purchase plan: %w", err)
	}

	return nil
}

// GetPurchasePlan retrieves a purchase plan by ID
func (s *PostgresStore) GetPurchasePlan(ctx context.Context, planID string) (*PurchasePlan, error) {
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

	err := s.db.QueryRow(ctx, query, planID).Scan(
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
			return nil, fmt.Errorf("purchase plan not found: %s", planID)
		}
		return nil, fmt.Errorf("failed to get purchase plan: %w", err)
	}

	// Unmarshal JSONB fields
	if err := json.Unmarshal(servicesJSON, &plan.Services); err != nil {
		return nil, fmt.Errorf("failed to unmarshal services: %w", err)
	}

	if err := json.Unmarshal(rampScheduleJSON, &plan.RampSchedule); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ramp_schedule: %w", err)
	}

	// Handle nullable timestamps
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

// UpdatePurchasePlan updates an existing purchase plan
func (s *PostgresStore) UpdatePurchasePlan(ctx context.Context, plan *PurchasePlan) error {
	plan.UpdatedAt = time.Now()

	// Marshal services and ramp_schedule to JSONB
	servicesJSON, err := json.Marshal(plan.Services)
	if err != nil {
		return fmt.Errorf("failed to marshal services: %w", err)
	}

	rampScheduleJSON, err := json.Marshal(plan.RampSchedule)
	if err != nil {
		return fmt.Errorf("failed to marshal ramp_schedule: %w", err)
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

	result, err := s.db.Exec(ctx, query,
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
		return fmt.Errorf("failed to update purchase plan: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("purchase plan not found: %s", plan.ID)
	}

	return nil
}

// DeletePurchasePlan deletes a purchase plan
func (s *PostgresStore) DeletePurchasePlan(ctx context.Context, planID string) error {
	query := `DELETE FROM purchase_plans WHERE id = $1`

	result, err := s.db.Exec(ctx, query, planID)
	if err != nil {
		return fmt.Errorf("failed to delete purchase plan: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("purchase plan not found: %s", planID)
	}

	return nil
}

// ListPurchasePlans lists all purchase plans
func (s *PostgresStore) ListPurchasePlans(ctx context.Context) ([]PurchasePlan, error) {
	query := `
		SELECT id, name, enabled, auto_purchase, notification_days_before,
		       services, ramp_schedule, created_at, updated_at,
		       next_execution_date, last_execution_date, last_notification_sent
		FROM purchase_plans
		ORDER BY created_at DESC
	`

	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list purchase plans: %w", err)
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
			return nil, fmt.Errorf("failed to scan purchase plan: %w", err)
		}

		// Unmarshal JSONB fields
		if err := json.Unmarshal(servicesJSON, &plan.Services); err != nil {
			return nil, fmt.Errorf("failed to unmarshal services: %w", err)
		}

		if err := json.Unmarshal(rampScheduleJSON, &plan.RampSchedule); err != nil {
			return nil, fmt.Errorf("failed to unmarshal ramp_schedule: %w", err)
		}

		// Handle nullable timestamps
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

// ==========================================
// PURCHASE EXECUTIONS
// ==========================================

// SavePurchaseExecution saves a purchase execution record
func (s *PostgresStore) SavePurchaseExecution(ctx context.Context, execution *PurchaseExecution) error {
	// Generate execution ID if not provided
	if execution.ExecutionID == "" {
		execution.ExecutionID = uuid.New().String()
	}

	// Marshal recommendations to JSONB
	recommendationsJSON, err := json.Marshal(execution.Recommendations)
	if err != nil {
		return fmt.Errorf("failed to marshal recommendations: %w", err)
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

	_, err = s.db.Exec(ctx, query,
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

	if err != nil {
		return fmt.Errorf("failed to save purchase execution: %w", err)
	}

	return nil
}

// GetPendingExecutions retrieves all pending purchase executions
func (s *PostgresStore) GetPendingExecutions(ctx context.Context) ([]PurchaseExecution, error) {
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

// GetExecutionByID retrieves a purchase execution by execution ID
func (s *PostgresStore) GetExecutionByID(ctx context.Context, executionID string) (*PurchaseExecution, error) {
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
		return nil, fmt.Errorf("execution not found: %s", executionID)
	}

	return &executions[0], nil
}

// GetExecutionByPlanAndDate retrieves execution for a specific plan and date
func (s *PostgresStore) GetExecutionByPlanAndDate(ctx context.Context, planID string, scheduledDate time.Time) (*PurchaseExecution, error) {
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
		return nil, fmt.Errorf("execution not found for plan %s at %v", planID, scheduledDate)
	}

	return &executions[0], nil
}

// queryExecutions is a helper to query and scan purchase executions
func (s *PostgresStore) queryExecutions(ctx context.Context, query string, args ...any) ([]PurchaseExecution, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query executions: %w", err)
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
			return nil, fmt.Errorf("failed to scan execution: %w", err)
		}

		// Unmarshal recommendations
		if err := json.Unmarshal(recommendationsJSON, &exec.Recommendations); err != nil {
			return nil, fmt.Errorf("failed to unmarshal recommendations: %w", err)
		}

		// Handle nullable timestamps
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

// CleanupOldExecutions deletes purchase executions older than retentionDays
func (s *PostgresStore) CleanupOldExecutions(ctx context.Context, retentionDays int) (int64, error) {
	query := `
		DELETE FROM purchase_executions
		WHERE scheduled_date < NOW() - INTERVAL '1 day' * $1
		AND status IN ('completed', 'cancelled', 'expired')
	`

	result, err := s.db.Exec(ctx, query, retentionDays)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old executions: %w", err)
	}

	return result.RowsAffected(), nil
}

// ==========================================
// PURCHASE HISTORY
// ==========================================

// SavePurchaseHistory saves a purchase history record
func (s *PostgresStore) SavePurchaseHistory(ctx context.Context, record *PurchaseHistoryRecord) error {
	query := `
		INSERT INTO purchase_history (
			account_id, purchase_id, timestamp, provider, service, region,
			resource_type, count, term, payment, upfront_cost, monthly_cost,
			estimated_savings, plan_id, plan_name, ramp_step
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`

	_, err := s.db.Exec(ctx, query,
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

	if err != nil {
		return fmt.Errorf("failed to save purchase history: %w", err)
	}

	return nil
}

// GetPurchaseHistory retrieves purchase history for an account
func (s *PostgresStore) GetPurchaseHistory(ctx context.Context, accountID string, limit int) ([]PurchaseHistoryRecord, error) {
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

// GetAllPurchaseHistory retrieves all purchase history
func (s *PostgresStore) GetAllPurchaseHistory(ctx context.Context, limit int) ([]PurchaseHistoryRecord, error) {
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

// queryPurchaseHistory is a helper to query and scan purchase history
func (s *PostgresStore) queryPurchaseHistory(ctx context.Context, query string, args ...any) ([]PurchaseHistoryRecord, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query purchase history: %w", err)
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
			return nil, fmt.Errorf("failed to scan purchase history: %w", err)
		}

		// Handle nullable strings
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
// RI EXCHANGE HISTORY
// ==========================================

// SaveRIExchangeRecord saves an RI exchange record
func (s *PostgresStore) SaveRIExchangeRecord(ctx context.Context, record *RIExchangeRecord) error {
	if record.ID == "" {
		record.ID = uuid.New().String()
	}

	query := `
		INSERT INTO ri_exchange_history (
			id, account_id, exchange_id, region, source_ri_ids,
			source_instance_type, source_count, target_offering_id,
			target_instance_type, target_count, payment_due,
			status, approval_token, error, mode, completed_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`

	_, err := s.db.Exec(ctx, query,
		record.ID,
		record.AccountID,
		record.ExchangeID,
		record.Region,
		record.SourceRIIDs,
		record.SourceInstanceType,
		record.SourceCount,
		record.TargetOfferingID,
		record.TargetInstanceType,
		record.TargetCount,
		record.PaymentDue,
		record.Status,
		nullStringFromString(record.ApprovalToken),
		nullStringFromString(record.Error),
		record.Mode,
		record.CompletedAt,
		record.ExpiresAt,
	)

	if err != nil {
		return fmt.Errorf("failed to save ri exchange record: %w", err)
	}

	return nil
}

// GetRIExchangeRecord retrieves an RI exchange record by ID
func (s *PostgresStore) GetRIExchangeRecord(ctx context.Context, id string) (*RIExchangeRecord, error) {
	query := `
		SELECT id, account_id, exchange_id, region, source_ri_ids,
		       source_instance_type, source_count, target_offering_id,
		       target_instance_type, target_count, payment_due::text,
		       status, approval_token, error, mode,
		       created_at, updated_at, completed_at, expires_at
		FROM ri_exchange_history
		WHERE id = $1
	`

	records, err := s.queryRIExchangeRecords(ctx, query, id)
	if err != nil {
		return nil, err
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("ri exchange record not found: %s", id)
	}

	return &records[0], nil
}

// GetRIExchangeRecordByToken retrieves an RI exchange record by approval token
func (s *PostgresStore) GetRIExchangeRecordByToken(ctx context.Context, token string) (*RIExchangeRecord, error) {
	query := `
		SELECT id, account_id, exchange_id, region, source_ri_ids,
		       source_instance_type, source_count, target_offering_id,
		       target_instance_type, target_count, payment_due::text,
		       status, approval_token, error, mode,
		       created_at, updated_at, completed_at, expires_at
		FROM ri_exchange_history
		WHERE approval_token = $1
	`

	records, err := s.queryRIExchangeRecords(ctx, query, token)
	if err != nil {
		return nil, err
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("ri exchange record not found for token")
	}

	return &records[0], nil
}

// GetRIExchangeHistory retrieves RI exchange history records
func (s *PostgresStore) GetRIExchangeHistory(ctx context.Context, since time.Time, limit int) ([]RIExchangeRecord, error) {
	query := `
		SELECT id, account_id, exchange_id, region, source_ri_ids,
		       source_instance_type, source_count, target_offering_id,
		       target_instance_type, target_count, payment_due::text,
		       status, approval_token, error, mode,
		       created_at, updated_at, completed_at, expires_at
		FROM ri_exchange_history
		WHERE created_at >= $1
		ORDER BY created_at DESC
		LIMIT $2
	`

	return s.queryRIExchangeRecords(ctx, query, since, limit)
}

// TransitionRIExchangeStatus atomically transitions an RI exchange record status
func (s *PostgresStore) TransitionRIExchangeStatus(ctx context.Context, id string, fromStatus string, toStatus string) (*RIExchangeRecord, error) {
	// First check if the record exists
	checkQuery := `SELECT status FROM ri_exchange_history WHERE id = $1`
	var currentStatus string
	err := s.db.QueryRow(ctx, checkQuery, id).Scan(&currentStatus)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("ri exchange record not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to check ri exchange status: %w", err)
	}

	if currentStatus != fromStatus {
		return nil, fmt.Errorf("ri exchange status transition failed: expected status %q but current status is %q", fromStatus, currentStatus)
	}

	// Check if expired
	expiredQuery := `SELECT 1 FROM ri_exchange_history WHERE id = $1 AND expires_at IS NOT NULL AND expires_at <= NOW()`
	var isExpired int
	err = s.db.QueryRow(ctx, expiredQuery, id).Scan(&isExpired)
	if err == nil {
		return nil, fmt.Errorf("ri exchange has expired")
	}
	if err != pgx.ErrNoRows {
		return nil, fmt.Errorf("failed to check expiration: %w", err)
	}

	query := `
		UPDATE ri_exchange_history
		SET status = $3
		WHERE id = $1 AND status = $2 AND (expires_at IS NULL OR expires_at > NOW())
		RETURNING id, account_id, exchange_id, region, source_ri_ids,
		          source_instance_type, source_count, target_offering_id,
		          target_instance_type, target_count, payment_due::text,
		          status, approval_token, error, mode,
		          created_at, updated_at, completed_at, expires_at
	`

	records, err := s.queryRIExchangeRecords(ctx, query, id, fromStatus, toStatus)
	if err != nil {
		return nil, err
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("ri exchange status transition failed: record not found or expired")
	}

	return &records[0], nil
}

// CompleteRIExchange marks an RI exchange as completed
func (s *PostgresStore) CompleteRIExchange(ctx context.Context, id string, exchangeID string) error {
	query := `
		UPDATE ri_exchange_history
		SET status = 'completed', exchange_id = $2, completed_at = NOW()
		WHERE id = $1
	`

	result, err := s.db.Exec(ctx, query, id, exchangeID)
	if err != nil {
		return fmt.Errorf("failed to complete ri exchange: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("ri exchange record not found: %s", id)
	}

	return nil
}

// FailRIExchange marks an RI exchange as failed
func (s *PostgresStore) FailRIExchange(ctx context.Context, id string, errorMsg string) error {
	query := `
		UPDATE ri_exchange_history
		SET status = 'failed', error = $2
		WHERE id = $1
	`

	result, err := s.db.Exec(ctx, query, id, errorMsg)
	if err != nil {
		return fmt.Errorf("failed to fail ri exchange: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("ri exchange record not found: %s", id)
	}

	return nil
}

// GetRIExchangeDailySpend returns total payment_due for completed exchanges on a given date (UTC)
func (s *PostgresStore) GetRIExchangeDailySpend(ctx context.Context, date time.Time) (string, error) {
	query := `
		SELECT COALESCE(SUM(payment_due), 0)::text
		FROM ri_exchange_history
		WHERE status = 'completed'
		  AND completed_at >= date_trunc('day', $1::timestamptz AT TIME ZONE 'UTC')
		  AND completed_at < date_trunc('day', $1::timestamptz AT TIME ZONE 'UTC') + INTERVAL '1 day'
	`

	var total string
	err := s.db.QueryRow(ctx, query, date).Scan(&total)
	if err != nil {
		return "", fmt.Errorf("failed to get ri exchange daily spend: %w", err)
	}

	return total, nil
}

// CancelAllPendingExchanges cancels all pending RI exchange records
func (s *PostgresStore) CancelAllPendingExchanges(ctx context.Context) (int64, error) {
	query := `
		UPDATE ri_exchange_history
		SET status = 'cancelled'
		WHERE status = 'pending'
	`

	result, err := s.db.Exec(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("failed to cancel pending exchanges: %w", err)
	}

	return result.RowsAffected(), nil
}

// GetStaleProcessingExchanges returns processing exchanges older than the given duration
func (s *PostgresStore) GetStaleProcessingExchanges(ctx context.Context, olderThan time.Duration) ([]RIExchangeRecord, error) {
	query := `
		SELECT id, account_id, exchange_id, region, source_ri_ids,
		       source_instance_type, source_count, target_offering_id,
		       target_instance_type, target_count, payment_due::text,
		       status, approval_token, error, mode,
		       created_at, updated_at, completed_at, expires_at
		FROM ri_exchange_history
		WHERE status = 'processing' AND updated_at < NOW() - $1::interval
	`

	return s.queryRIExchangeRecords(ctx, query, fmt.Sprintf("%d seconds", int(olderThan.Seconds())))
}

// queryRIExchangeRecords is a helper to query and scan RI exchange records
func (s *PostgresStore) queryRIExchangeRecords(ctx context.Context, query string, args ...any) ([]RIExchangeRecord, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query ri exchange records: %w", err)
	}
	defer rows.Close()

	records := make([]RIExchangeRecord, 0)
	for rows.Next() {
		var record RIExchangeRecord
		var approvalToken, errStr sql.NullString
		var completedAt, expiresAt sql.NullTime

		err := rows.Scan(
			&record.ID,
			&record.AccountID,
			&record.ExchangeID,
			&record.Region,
			&record.SourceRIIDs,
			&record.SourceInstanceType,
			&record.SourceCount,
			&record.TargetOfferingID,
			&record.TargetInstanceType,
			&record.TargetCount,
			&record.PaymentDue,
			&record.Status,
			&approvalToken,
			&errStr,
			&record.Mode,
			&record.CreatedAt,
			&record.UpdatedAt,
			&completedAt,
			&expiresAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan ri exchange record: %w", err)
		}

		if approvalToken.Valid {
			record.ApprovalToken = approvalToken.String
		}
		if errStr.Valid {
			record.Error = errStr.String
		}
		if completedAt.Valid {
			record.CompletedAt = &completedAt.Time
		}
		if expiresAt.Valid {
			record.ExpiresAt = &expiresAt.Time
		}

		records = append(records, record)
	}

	return records, rows.Err()
}

// ==========================================
// HELPER FUNCTIONS
// ==========================================

// timeFromTTL converts a Unix timestamp (TTL) to a nullable time.Time
func timeFromTTL(ttl int64) any {
	if ttl == 0 {
		return nil
	}
	t := time.Unix(ttl, 0)
	return &t
}

// ttlFromTime converts a time.Time to Unix timestamp
func ttlFromTime(t time.Time) int64 {
	return t.Unix()
}

// nullStringFromString converts a string to sql.NullString
func nullStringFromString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
