-- Migration 000079: commitment-laddering per-scope configuration table
-- and global_config kill-switch column.
--
-- Default-off: laddering_enabled = false on the singleton global_config row;
-- per-account rows default to enabled = false. No laddering behaviour
-- activates until an operator explicitly enables both the global kill-switch
-- and at least one per-account row.
--
-- Idempotent: ADD COLUMN IF NOT EXISTS and CREATE TABLE IF NOT EXISTS
-- throughout, so re-running after a partial failure is safe.

-- Kill-switch on the global_config singleton row.
ALTER TABLE global_config
    ADD COLUMN IF NOT EXISTS laddering_enabled BOOLEAN NOT NULL DEFAULT false;

-- Per-account ladder configuration.
-- One row per (cloud_account_id, provider) pair: UNIQUE enforces the constraint.
-- ON DELETE CASCADE removes the config when the cloud account is deleted.
CREATE TABLE IF NOT EXISTS ladder_configs (
    id                              UUID            PRIMARY KEY DEFAULT uuid_generate_v4(),
    cloud_account_id                UUID            NOT NULL
                                                    REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    provider                        TEXT            NOT NULL,
    enabled                         BOOLEAN         NOT NULL DEFAULT false,
    mode                            TEXT            NOT NULL,   -- email_approval | auto_approve
    cadence                         TEXT            NOT NULL,   -- daily | weekly
    target_coverage                 NUMERIC(5,2)    NOT NULL,
    buffer_fraction                 NUMERIC(5,4)    NOT NULL,
    baseline_percentile             NUMERIC(5,2)    NOT NULL,
    lookback_days                   INTEGER         NOT NULL,
    buffer_utilization_threshold    NUMERIC(5,2)    NOT NULL,
    max_hourly_commit_per_run       NUMERIC(20,6),              -- NULL = no cap
    max_actions_per_run             INTEGER         NOT NULL,
    ramp_schedule                   JSONB           NOT NULL,
    created_at                      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at                      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),

    UNIQUE(cloud_account_id, provider)
);

CREATE INDEX IF NOT EXISTS idx_ladder_configs_account
    ON ladder_configs(cloud_account_id);

CREATE INDEX IF NOT EXISTS idx_ladder_configs_provider
    ON ladder_configs(provider);

-- Trigger: keep updated_at current on every UPDATE.
-- Guard with DO block so re-runs are idempotent.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger WHERE tgname = 'update_ladder_configs_updated_at'
    ) THEN
        CREATE TRIGGER update_ladder_configs_updated_at
            BEFORE UPDATE ON ladder_configs
            FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;
END $$;
