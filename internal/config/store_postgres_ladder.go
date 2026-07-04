package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ==========================================
// LADDER CONFIG STORE METHODS
// ==========================================

// GetLadderConfigs returns all ladder_configs rows, newest first.
// Returns an empty slice (not nil) when no rows exist.
func (s *PostgresStore) GetLadderConfigs(ctx context.Context) ([]LadderConfigDB, error) {
	query := `
		SELECT id, cloud_account_id, provider, enabled, mode, cadence,
		       target_coverage, buffer_fraction, baseline_percentile,
		       lookback_days, buffer_utilization_threshold,
		       max_hourly_commit_per_run, max_actions_per_run,
		       ramp_schedule, created_at, updated_at
		FROM ladder_configs
		ORDER BY created_at DESC
	`
	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query ladder_configs: %w", err)
	}
	defer rows.Close()

	var configs []LadderConfigDB
	for rows.Next() {
		cfg, err := scanLadderConfig(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan ladder_config row: %w", err)
		}
		configs = append(configs, cfg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating ladder_config rows: %w", err)
	}
	if configs == nil {
		configs = []LadderConfigDB{}
	}
	return configs, nil
}

// GetLadderConfig returns the ladder_config row for the given
// (cloud_account_id, provider) pair. Returns (nil, nil) when no row exists.
func (s *PostgresStore) GetLadderConfig(ctx context.Context, cloudAccountID, provider string) (*LadderConfigDB, error) {
	query := `
		SELECT id, cloud_account_id, provider, enabled, mode, cadence,
		       target_coverage, buffer_fraction, baseline_percentile,
		       lookback_days, buffer_utilization_threshold,
		       max_hourly_commit_per_run, max_actions_per_run,
		       ramp_schedule, created_at, updated_at
		FROM ladder_configs
		WHERE cloud_account_id = $1 AND provider = $2
	`
	row := s.db.QueryRow(ctx, query, cloudAccountID, provider)
	cfg, err := scanLadderConfig(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get ladder_config for account=%s provider=%s: %w", cloudAccountID, provider, err)
	}
	return &cfg, nil
}

// UpsertLadderConfig inserts or updates the per-account ladder configuration.
// The upsert key is (cloud_account_id, provider). If ID is empty a new UUID
// is generated; existing rows retain their original id and created_at.
//
// Validate() must be called by the API handler before this method; the store
// does not re-validate to avoid duplicating error messages.
func (s *PostgresStore) UpsertLadderConfig(ctx context.Context, cfg *LadderConfigDB) (*LadderConfigDB, error) {
	if cfg.ID == "" {
		cfg.ID = uuid.New().String()
	}

	rampJSON, err := marshalRampSchedule(cfg.RampSchedule)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ramp_schedule: %w", err)
	}

	query := `
		INSERT INTO ladder_configs (
			id, cloud_account_id, provider, enabled, mode, cadence,
			target_coverage, buffer_fraction, baseline_percentile,
			lookback_days, buffer_utilization_threshold,
			max_hourly_commit_per_run, max_actions_per_run,
			ramp_schedule, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW(), NOW())
		ON CONFLICT (cloud_account_id, provider) DO UPDATE SET
			enabled                      = $4,
			mode                         = $5,
			cadence                      = $6,
			target_coverage              = $7,
			buffer_fraction              = $8,
			baseline_percentile          = $9,
			lookback_days                = $10,
			buffer_utilization_threshold = $11,
			max_hourly_commit_per_run    = $12,
			max_actions_per_run          = $13,
			ramp_schedule                = $14,
			updated_at                   = NOW()
		RETURNING id, cloud_account_id, provider, enabled, mode, cadence,
		          target_coverage, buffer_fraction, baseline_percentile,
		          lookback_days, buffer_utilization_threshold,
		          max_hourly_commit_per_run, max_actions_per_run,
		          ramp_schedule, created_at, updated_at
	`

	row := s.db.QueryRow(ctx, query,
		cfg.ID,
		cfg.CloudAccountID,
		cfg.Provider,
		cfg.Enabled,
		cfg.Mode,
		cfg.Cadence,
		cfg.TargetCoverage,
		cfg.BufferFraction,
		cfg.BaselinePercentile,
		cfg.LookbackDays,
		cfg.BufferUtilizationThreshold,
		cfg.MaxHourlyCommitPerRun,
		cfg.MaxActionsPerRun,
		rampJSON,
	)
	result, err := scanLadderConfig(row)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert ladder_config for account=%s provider=%s: %w",
			cfg.CloudAccountID, cfg.Provider, err)
	}
	return &result, nil
}

// scanLadderConfig scans a single ladder_configs row from either a pgx.Row
// or pgx.Rows. Both types satisfy the scannable interface (declared in
// store_postgres_registrations.go). This helper exists to avoid duplicating
// the 16-column scan logic.
func scanLadderConfig(row scannable) (LadderConfigDB, error) {
	var cfg LadderConfigDB
	var rampJSON []byte
	var createdAt, updatedAt time.Time

	err := row.Scan(
		&cfg.ID,
		&cfg.CloudAccountID,
		&cfg.Provider,
		&cfg.Enabled,
		&cfg.Mode,
		&cfg.Cadence,
		&cfg.TargetCoverage,
		&cfg.BufferFraction,
		&cfg.BaselinePercentile,
		&cfg.LookbackDays,
		&cfg.BufferUtilizationThreshold,
		&cfg.MaxHourlyCommitPerRun,
		&cfg.MaxActionsPerRun,
		&rampJSON,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return LadderConfigDB{}, err
	}
	cfg.CreatedAt = createdAt
	cfg.UpdatedAt = updatedAt
	cfg.RampSchedule = json.RawMessage(rampJSON)
	return cfg, nil
}

// marshalRampSchedule ensures the ramp_schedule value stored in the DB is
// valid JSON. Nil/empty input is rejected; already-valid JSON is passed
// through unchanged. Non-JSON input is rejected with a descriptive error.
func marshalRampSchedule(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("ramp_schedule must not be empty")
	}
	// Validate it is parseable JSON before writing.
	if !json.Valid(raw) {
		return nil, fmt.Errorf("ramp_schedule is not valid JSON")
	}
	return raw, nil
}
