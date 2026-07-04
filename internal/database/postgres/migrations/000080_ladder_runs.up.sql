-- Migration 000080: ladder_runs immutable audit table.
--
-- Each ladder engine invocation writes one row here before taking any
-- action. Status tracks the lifecycle:
--   planned -> awaiting_approval -> approved -> executing -> completed
--   or -> failed | cancelled | expired (terminal states)
--
-- Monetary baseline columns are all NULLABLE. NULL means "not yet computed"
-- or "run failed before this could be measured" -- never coerced to $0
-- (project rule: absent numbers are NULL/pointer, never 0).
--
-- Also adds ladder_run_id FK columns to purchase_executions and
-- ri_exchange_history so every commitment touched by a ladder run can be
-- traced back to the run that triggered it.
--
-- Idempotent: CREATE TABLE IF NOT EXISTS and ADD COLUMN IF NOT EXISTS
-- throughout.

CREATE TABLE IF NOT EXISTS ladder_runs (
    id                          UUID            PRIMARY KEY DEFAULT uuid_generate_v4(),
    config_id                   UUID            REFERENCES ladder_configs(id) ON DELETE SET NULL,
    started_at                  TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    completed_at                TIMESTAMPTZ,
    status                      TEXT            NOT NULL,   -- planned|awaiting_approval|approved|executing|completed|failed|cancelled|expired
    mode                        TEXT,
    cadence                     TEXT,

    -- Monetary baseline snapshot; all NULLABLE (NULL != $0, see docstring above).
    baseline_usd_hr             NUMERIC(20,6),
    target_usd_hr               NUMERIC(20,6),
    existing_usd_hr             NUMERIC(20,6),
    gap_usd_hr                  NUMERIC(20,6),

    plan                        JSONB           NOT NULL DEFAULT '{}',

    -- Running totals. NOT NULL DEFAULT 0 because these are accumulators
    -- initialised to 0 at row creation, not baseline measurements.
    total_hourly_commit         NUMERIC(20,6)   NOT NULL DEFAULT 0,
    total_upfront_cost          NUMERIC(20,6)   NOT NULL DEFAULT 0,
    estimated_savings           NUMERIC(20,6)   NOT NULL DEFAULT 0,

    -- Stores the SHA-256 hex digest of the run approval token, never the raw
    -- token. The raw token is sent only in the approval email link; the
    -- email-approval PR (phase-3 PR-3) must hash-on-write and use a
    -- constant-time compare against this column. (The pre-existing
    -- purchase_executions / ri_exchange_history approval_token columns still
    -- store raw tokens; hashing those is tracked as a separate follow-up.)
    approval_token_hash         TEXT,
    approval_token_expires_at   TIMESTAMPTZ,
    approved_by                 TEXT,
    cancelled_by                TEXT,
    fire_at                     TIMESTAMPTZ,

    created_at                  TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ladder_runs_config_id
    ON ladder_runs(config_id);

CREATE INDEX IF NOT EXISTS idx_ladder_runs_status
    ON ladder_runs(status);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger WHERE tgname = 'update_ladder_runs_updated_at'
    ) THEN
        CREATE TRIGGER update_ladder_runs_updated_at
            BEFORE UPDATE ON ladder_runs
            FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
    END IF;
END $$;

-- Link purchase_executions to the ladder run that triggered them.
-- Partial index: only ladder-initiated executions carry the FK so the
-- index stays narrow and the NULL-check is free.
ALTER TABLE purchase_executions
    ADD COLUMN IF NOT EXISTS ladder_run_id UUID REFERENCES ladder_runs(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_purchase_executions_ladder_run
    ON purchase_executions(ladder_run_id)
    WHERE ladder_run_id IS NOT NULL;

-- Link ri_exchange_history to the ladder run that triggered them.
-- ri_exchange_history was introduced in migration 000001 and is confirmed
-- present on main (most recently altered in migration 000077).
ALTER TABLE ri_exchange_history
    ADD COLUMN IF NOT EXISTS ladder_run_id UUID REFERENCES ladder_runs(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_ri_exchange_history_ladder_run
    ON ri_exchange_history(ladder_run_id)
    WHERE ladder_run_id IS NOT NULL;
