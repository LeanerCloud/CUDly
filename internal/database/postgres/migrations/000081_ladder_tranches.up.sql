-- Migration 000081: ladder_tranches table.
--
-- A tranche is a single timed purchase slice within a ladder run's ramp
-- schedule. The scheduler fires each tranche at its scheduled_date, creates
-- a purchase_executions row, and links execution_id back here once the
-- commitment is bought.
--
-- run_id traces a tranche back to the ladder_runs row that created it. It is
-- nullable because a tranche may be planned before its run row exists in later
-- flows, and ON DELETE SET NULL keeps the tranche for audit if the run is
-- later removed (mirrors config_id's semantics). Type matches ladder_runs.id
-- (UUID, see migration 000080).
--
-- execution_id references purchase_executions(execution_id) rather than
-- purchase_executions(id) because callers resolve executions by their
-- execution_id business key. The UNIQUE constraint on execution_id
-- (migration 000008) satisfies the FK referential requirement.
--
-- Idempotent: CREATE TABLE IF NOT EXISTS throughout.

CREATE TABLE IF NOT EXISTS ladder_tranches (
    id              UUID            PRIMARY KEY DEFAULT uuid_generate_v4(),
    config_id       UUID            REFERENCES ladder_configs(id) ON DELETE SET NULL,
    run_id          UUID            REFERENCES ladder_runs(id) ON DELETE SET NULL,
    layer_type      TEXT            NOT NULL,
    amount_usd_hr   NUMERIC(20,6)   NOT NULL,
    term            TEXT            NOT NULL,
    payment_option  TEXT            NOT NULL,
    scheduled_date  TIMESTAMPTZ     NOT NULL,
    status          TEXT            NOT NULL,   -- scheduled|fired|completed|cancelled|failed
    execution_id    UUID            REFERENCES purchase_executions(execution_id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ladder_tranches_config_id
    ON ladder_tranches(config_id);

-- Partial index: only run-linked tranches carry the FK, so the index stays
-- narrow and traces tranches back to their originating run cheaply.
CREATE INDEX IF NOT EXISTS idx_ladder_tranches_run_id
    ON ladder_tranches(run_id)
    WHERE run_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_ladder_tranches_status
    ON ladder_tranches(status);

-- Partial index for the scheduler sweep that fires due tranches.
CREATE INDEX IF NOT EXISTS idx_ladder_tranches_scheduled
    ON ladder_tranches(scheduled_date)
    WHERE status = 'scheduled';
