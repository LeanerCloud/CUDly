-- Migration 000074: repair a partially-applied 058-067 migration range.
--
-- Root cause: a prior emergency `Force(67)` on a dev/QA database marked
-- migrations 058-067 as applied without actually running their SQL. Several
-- genuinely-unapplied schema changes from that range are therefore MISSING in
-- the affected DB, producing live 500s such as
--   "column idempotency_key does not exist"
-- on planned/pending/stuck purchase-execution queries. Confirmed missing:
-- purchase_executions.idempotency_key (066); likely also groups.system_managed
-- (059) and other 058-067 schema.
--
-- The 058-067 range cannot simply be re-run because migration 000059 has a
-- RAISE EXCEPTION seed-guard that conflicts with the already-relocated
-- 'Purchaser' group (000064), so re-applying it would abort.
--
-- This migration re-asserts ONLY the SCHEMA from the 058-067 range in fully
-- idempotent form, so ANY partially-migrated DB (and a fully-migrated one)
-- converges to the correct schema. It runs atomically: golang-migrate wraps
-- the file in a single transaction and there is no CONCURRENTLY here.
--
-- It INTENTIONALLY OMITS the data seeds from that range -- they already exist
-- (or were deliberately relocated) in any affected DB and re-running them
-- would either be redundant or trip the conflicting guards:
--   * 059 Purchaser INSERT + admin backfill UPDATE + name-collision RAISE
--   * 060 universal-plans DELETE
--   * 064 Purchaser relocate (UUID swap + admin backfill)
-- The schema re-asserted below is what those migrations also add as a
-- side effect (groups.system_managed); the row data is left untouched.
--
-- Schema objects re-asserted, by source migration:
--   058: purchase_executions.executed_by_user_id, .executed_at,
--        .pre_approval_skip_reason; index idx_executions_direct_execute
--   059: groups.system_managed
--   063: purchase_history.monthly_cost DROP NOT NULL
--   065: function check_min_one_admin(); constraint triggers
--        trg_min_one_admin_delete, trg_min_one_admin_update
--   066: purchase_executions.idempotency_key
--   067: savings_snapshots.account_id widen to VARCHAR(255);
--        savings_snapshots.total_usage / .coverage_percentage DROP NOT NULL
--        + DROP DEFAULT; materialized views monthly_savings_summary,
--        daily_savings_trend, provider_savings_summary (+ unique indexes);
--        function drop_old_savings_partitions()

-- ==================================================================
-- 058: direct-execute audit columns + partial index.
-- ==================================================================
ALTER TABLE purchase_executions
    ADD COLUMN IF NOT EXISTS executed_by_user_id  UUID        REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS executed_at          TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS pre_approval_skip_reason TEXT;

CREATE INDEX IF NOT EXISTS idx_executions_direct_execute
    ON purchase_executions (executed_by_user_id)
    WHERE executed_by_user_id IS NOT NULL;

-- ==================================================================
-- 059: groups.system_managed column (schema only; the seed UPDATE/INSERT
-- and Purchaser admin-backfill are intentionally omitted -- see header).
-- ==================================================================
ALTER TABLE groups ADD COLUMN IF NOT EXISTS system_managed BOOLEAN NOT NULL DEFAULT FALSE;

-- ==================================================================
-- 063: allow NULL in purchase_history.monthly_cost. Re-running DROP NOT NULL
-- on an already-nullable column is a no-op, so this is idempotent as-is.
-- ==================================================================
ALTER TABLE purchase_history ALTER COLUMN monthly_cost DROP NOT NULL;

-- ==================================================================
-- 066: stable idempotency lineage key. (Placed before 065's triggers is
-- immaterial; ordering within this file only needs each statement to be
-- self-consistent.)
-- ==================================================================
ALTER TABLE purchase_executions
    ADD COLUMN IF NOT EXISTS idempotency_key TEXT;

-- ==================================================================
-- 065: min-one-admin deferred constraint triggers + their function.
-- CREATE OR REPLACE FUNCTION and DROP TRIGGER IF EXISTS ... CREATE TRIGGER
-- are idempotent. Mirrors the source migration verbatim.
-- ==================================================================
CREATE OR REPLACE FUNCTION check_min_one_admin()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    admin_count INTEGER;
BEGIN
    -- Serialize the count across concurrent admin-affecting transactions.
    -- Without this, two deferred checks run against independent MVCC
    -- snapshots and both can pass while jointly removing the last admins.
    -- The key is an arbitrary fixed constant scoped to this invariant.
    PERFORM pg_advisory_xact_lock(8059058058580001);

    SELECT COUNT(*) INTO admin_count
    FROM users
    WHERE group_ids @> ARRAY['00000000-0000-5000-8000-000000000001']::UUID[]
      AND active = true;

    IF admin_count < 1 THEN
        RAISE EXCEPTION 'last_admin_constraint_violation: at least one active member of the Administrators group must remain';
    END IF;

    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_min_one_admin ON users;
DROP TRIGGER IF EXISTS trg_min_one_admin_delete ON users;
DROP TRIGGER IF EXISTS trg_min_one_admin_update ON users;

-- DELETE: fire when the removed row was an Administrators-group member.
CREATE CONSTRAINT TRIGGER trg_min_one_admin_delete
    AFTER DELETE ON users
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    WHEN (OLD.group_ids @> ARRAY['00000000-0000-5000-8000-000000000001']::UUID[])
    EXECUTE FUNCTION check_min_one_admin();

-- UPDATE: fire only when an active admin loses admin standing.
CREATE CONSTRAINT TRIGGER trg_min_one_admin_update
    AFTER UPDATE ON users
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    WHEN (
        OLD.group_ids @> ARRAY['00000000-0000-5000-8000-000000000001']::UUID[]
        AND OLD.active = true
        AND (
            NOT (NEW.group_ids @> ARRAY['00000000-0000-5000-8000-000000000001']::UUID[])
            OR NEW.active = false
        )
    )
    EXECUTE FUNCTION check_min_one_admin();

-- ==================================================================
-- 067: analytics snapshot correctness.
-- H4 widen account_id; H2 nullable total_usage/coverage_percentage;
-- H3 recreate materialized views carrying cloud_account_id; M5/N4 narrow
-- drop_old_savings_partitions error handling. Idempotent throughout.
-- ==================================================================

-- Repair-specific ordering (differs from source migration 067): drop the
-- materialized views FIRST, before the column alterations below.
--
-- On the corrupted prod DB the Force(67) skipped 067 entirely, so the views
-- still exist from migration 000003 with their OLD definition that selects
-- savings_snapshots.account_id. A view that depends on account_id blocks
-- "ALTER COLUMN account_id TYPE VARCHAR(255)" with
--   "cannot alter type of a column used by a view or rule".
-- Source migration 067 ran the ALTER before the DROP and only avoided this on
-- a clean forward chain by luck of timing; this repair must converge from the
-- view-present state, so we drop the dependent views up front and recreate
-- them with the correct (cloud_account_id-carrying) definition afterwards.
-- DROP ... IF EXISTS ... CASCADE is idempotent.
DROP MATERIALIZED VIEW IF EXISTS provider_savings_summary CASCADE;
DROP MATERIALIZED VIEW IF EXISTS daily_savings_trend CASCADE;
DROP MATERIALIZED VIEW IF EXISTS monthly_savings_summary CASCADE;

-- H4: widen account_id on the partitioned parent (guarded so re-running on an
-- already-widened column is a no-op). Postgres propagates to all partitions.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'savings_snapshots'
          AND column_name = 'account_id'
          AND (character_maximum_length IS NULL OR character_maximum_length < 255)
          AND data_type = 'character varying'
    ) THEN
        ALTER TABLE savings_snapshots
            ALTER COLUMN account_id TYPE VARCHAR(255);
    END IF;
END $$;

-- H2: total_usage / coverage_percentage become NULLABLE, no DEFAULT.
-- DROP NOT NULL / DROP DEFAULT are no-ops when already applied.
ALTER TABLE savings_snapshots ALTER COLUMN total_usage DROP NOT NULL;
ALTER TABLE savings_snapshots ALTER COLUMN total_usage DROP DEFAULT;
ALTER TABLE savings_snapshots ALTER COLUMN coverage_percentage DROP NOT NULL;
ALTER TABLE savings_snapshots ALTER COLUMN coverage_percentage DROP DEFAULT;

-- H3: recreate the materialized views carrying cloud_account_id with the
-- correct AVG-over-time aggregation. (The DROP was hoisted above the column
-- alterations; here we only create the corrected views + unique indexes.)

CREATE MATERIALIZED VIEW monthly_savings_summary AS
SELECT
    DATE_TRUNC('month', timestamp) as month,
    account_id,
    cloud_account_id,
    provider,
    service,
    AVG(total_savings) as total_savings,
    AVG(coverage_percentage) as avg_coverage,
    AVG(total_commitment) as total_commitment,
    AVG(total_usage) as total_usage,
    COUNT(*) as snapshot_count,
    MAX(timestamp) as last_updated
FROM savings_snapshots
GROUP BY DATE_TRUNC('month', timestamp), account_id, cloud_account_id, provider, service
WITH NO DATA;

CREATE UNIQUE INDEX idx_monthly_savings_summary_unique
    ON monthly_savings_summary(
        month, account_id,
        COALESCE(cloud_account_id, '00000000-0000-0000-0000-000000000000'::uuid),
        provider, service);

CREATE MATERIALIZED VIEW daily_savings_trend AS
SELECT
    day,
    account_id,
    cloud_account_id,
    provider,
    AVG(ts_savings) as daily_savings,
    AVG(ts_coverage) as avg_coverage,
    MAX(ts_service_count) as service_count
FROM (
    SELECT
        DATE_TRUNC('day', timestamp) as day,
        timestamp,
        account_id,
        cloud_account_id,
        provider,
        SUM(total_savings) as ts_savings,
        AVG(coverage_percentage) as ts_coverage,
        COUNT(DISTINCT service) as ts_service_count
    FROM savings_snapshots
    GROUP BY DATE_TRUNC('day', timestamp), timestamp, account_id, cloud_account_id, provider
) per_ts
GROUP BY day, account_id, cloud_account_id, provider
WITH NO DATA;

CREATE UNIQUE INDEX idx_daily_savings_trend_unique
    ON daily_savings_trend(
        day, account_id,
        COALESCE(cloud_account_id, '00000000-0000-0000-0000-000000000000'::uuid),
        provider);

CREATE MATERIALIZED VIEW provider_savings_summary AS
SELECT
    provider,
    account_id,
    cloud_account_id,
    MAX(ts_service_count) as service_count,
    AVG(ts_savings) as total_savings,
    AVG(ts_commitment) as total_commitment,
    AVG(ts_coverage) as avg_coverage,
    MAX(timestamp) as last_updated
FROM (
    SELECT
        provider,
        account_id,
        cloud_account_id,
        timestamp,
        SUM(total_savings) as ts_savings,
        SUM(total_commitment) as ts_commitment,
        AVG(coverage_percentage) as ts_coverage,
        COUNT(DISTINCT service) as ts_service_count
    FROM savings_snapshots
    WHERE timestamp > NOW() - INTERVAL '90 days'
    GROUP BY provider, account_id, cloud_account_id, timestamp
) per_ts
GROUP BY provider, account_id, cloud_account_id
WITH NO DATA;

CREATE UNIQUE INDEX idx_provider_savings_summary_unique
    ON provider_savings_summary(
        provider, account_id,
        COALESCE(cloud_account_id, '00000000-0000-0000-0000-000000000000'::uuid));

-- Populate the views (created WITH NO DATA above) exactly once, non-concurrently
-- (CONCURRENTLY cannot run against a never-populated view and cannot run inside a
-- transaction). The runtime refresh function keeps using CONCURRENTLY.
REFRESH MATERIALIZED VIEW monthly_savings_summary;
REFRESH MATERIALIZED VIEW daily_savings_trend;
REFRESH MATERIALIZED VIEW provider_savings_summary;

-- M5/N4: narrow drop_old_savings_partitions error handling.
CREATE OR REPLACE FUNCTION drop_old_savings_partitions(retention_months INTEGER DEFAULT 24)
RETURNS void AS $$
DECLARE
    partition_record RECORD;
    partition_date DATE;
    cutoff_date DATE;
BEGIN
    cutoff_date := DATE_TRUNC('month', CURRENT_DATE) - (retention_months || ' months')::INTERVAL;

    -- Enumerate actual child partitions of savings_snapshots from the catalog
    -- (pg_inherits) rather than name-matching pg_tables, where the LIKE
    -- wildcard '_' would also match unrelated tables such as
    -- savings_snapshots_backup_2024_01 and risk dropping non-partition data.
    FOR partition_record IN
        SELECT child.relname AS tablename
        FROM pg_inherits i
        JOIN pg_class parent ON parent.oid = i.inhparent
        JOIN pg_class child ON child.oid = i.inhrelid
        JOIN pg_namespace ns ON ns.oid = child.relnamespace
        WHERE parent.relname = 'savings_snapshots'
          AND ns.nspname = 'public'
          AND child.relname <> 'savings_snapshots_default'
    LOOP
        BEGIN
            partition_date := TO_DATE(
                SUBSTRING(partition_record.tablename FROM '\d{4}_\d{2}'),
                'YYYY_MM'
            );
        EXCEPTION
            WHEN invalid_datetime_format OR datetime_field_overflow THEN
                RAISE WARNING 'Skipping unparseable partition name %: %',
                    partition_record.tablename, SQLERRM;
                CONTINUE;
        END;

        IF partition_date < cutoff_date THEN
            EXECUTE format('DROP TABLE IF EXISTS %I', partition_record.tablename);
            RAISE NOTICE 'Dropped old partition: %', partition_record.tablename;
        END IF;
    END LOOP;
END;
$$ LANGUAGE plpgsql;
