-- ==========================================
-- ANALYTICS SNAPSHOT CORRECTNESS (wire-up prerequisites)
-- ==========================================
--
-- Prepares savings_snapshots + its materialized views for the now-live
-- collector (issues #1023 / #1033). Three correctness fixes ship here; the
-- collector and Query* changes ship in the same PR:
--
--   H4  account_id VARCHAR(20) is too small for Azure subscription IDs (36) and
--       GCP project IDs (<=30). Widen to VARCHAR(255) to match
--       cloud_accounts.external_id so non-AWS snapshot inserts stop erroring
--       with "value too long for type character varying(20)".
--
--   H2  total_usage / coverage_percentage were NOT NULL DEFAULT 0. The collector
--       could only ever derive usage when the source recurring cost is present
--       and coverage when an on-demand baseline is present; coercing the absent
--       case to 0 drags AVG(coverage_percentage) toward zero (project rule
--       feedback_nullable_not_zero). Make both columns NULLABLE with no default
--       so "unknown" stays NULL and AVG/SUM skip it.
--
--   H3  Scoping must key on cloud_account_id (the multi-tenant FK), not the
--       provider account string. The three materialized views grouped by
--       account_id only, so a cloud_account_id-scoped query could not read them.
--       Recreate the views carrying cloud_account_id alongside account_id.
--
--   H5  total_savings / total_commitment / total_usage are point-in-time
--       run-rates (the collector now stores a monthly run-rate per snapshot),
--       not accrued totals, so SUM-ing them over a window double-counts by the
--       snapshot frequency: a $720/mo commitment summed over ~30 daily snapshots
--       read as ~$21,600, and changing the schedule changed the number. Aggregate
--       these columns with AVG so each period reports the representative monthly
--       run-rate, invariant to how often the collector runs. coverage stays AVG.
--
-- Idempotent throughout: re-running on a partially-applied DB converges to the
-- target state rather than no-op'ing over a wrong column type (project rule
-- feedback_migration_full_restore).

-- ------------------------------------------------------------------
-- H4: widen account_id on the partitioned parent.
-- Postgres propagates the type change to all existing partitions.
-- ------------------------------------------------------------------
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

-- ------------------------------------------------------------------
-- H2: total_usage / coverage_percentage become NULLABLE, no DEFAULT.
-- Drop the NOT NULL and the DEFAULT 0 so absent metrics stay NULL.
-- Existing 0.00 rows are left as-is (those were emitted by the old
-- collector; they are indistinguishable from real zeros and the
-- materialized-view AVG already absorbed them. New rows write NULL).
-- ------------------------------------------------------------------
ALTER TABLE savings_snapshots ALTER COLUMN total_usage DROP NOT NULL;
ALTER TABLE savings_snapshots ALTER COLUMN total_usage DROP DEFAULT;
ALTER TABLE savings_snapshots ALTER COLUMN coverage_percentage DROP NOT NULL;
ALTER TABLE savings_snapshots ALTER COLUMN coverage_percentage DROP DEFAULT;

-- ------------------------------------------------------------------
-- H3: recreate the materialized views carrying cloud_account_id so
-- cloud_account_id-scoped Query* can read the pre-aggregated rows.
-- DROP + CREATE because a materialized view's column list / GROUP BY
-- cannot be altered in place. Unique indexes are recreated for the
-- CONCURRENTLY refresh path. AVG(coverage_percentage) now skips NULLs
-- automatically (H2).
-- ------------------------------------------------------------------
DROP MATERIALIZED VIEW IF EXISTS provider_savings_summary CASCADE;
DROP MATERIALIZED VIEW IF EXISTS daily_savings_trend CASCADE;
DROP MATERIALIZED VIEW IF EXISTS monthly_savings_summary CASCADE;

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
GROUP BY DATE_TRUNC('month', timestamp), account_id, cloud_account_id, provider, service;

-- cloud_account_id is nullable, so COALESCE it to the nil UUID inside the
-- unique index to keep the (month, account, provider, service) grain unique
-- under CONCURRENTLY refresh even when cloud_account_id IS NULL.
CREATE UNIQUE INDEX idx_monthly_savings_summary_unique
    ON monthly_savings_summary(
        month, account_id,
        COALESCE(cloud_account_id, '00000000-0000-0000-0000-000000000000'::uuid),
        provider, service);

-- daily_savings_trend rolls up across services, so it keeps the legitimate SUM
-- across services but, per H5, replaces the SUM across time with an AVG: the
-- inner query sums the per-service run-rates into a provider total at each
-- collection timestamp, and the outer query averages those instant-totals over
-- the day so the trend is invariant to the collection frequency.
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
GROUP BY day, account_id, cloud_account_id, provider;

CREATE UNIQUE INDEX idx_daily_savings_trend_unique
    ON daily_savings_trend(
        day, account_id,
        COALESCE(cloud_account_id, '00000000-0000-0000-0000-000000000000'::uuid),
        provider);

-- provider_savings_summary also rolls up across services: keep the SUM across
-- services, AVG across time (H5). Inner query = per-timestamp provider totals,
-- outer query = average of those instant-totals over the 90-day window.
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
GROUP BY provider, account_id, cloud_account_id;

CREATE UNIQUE INDEX idx_provider_savings_summary_unique
    ON provider_savings_summary(
        provider, account_id,
        COALESCE(cloud_account_id, '00000000-0000-0000-0000-000000000000'::uuid));

-- Repopulate the freshly-created views non-concurrently (CONCURRENTLY cannot
-- run against a never-populated view). The runtime refresh function keeps
-- using CONCURRENTLY.
REFRESH MATERIALIZED VIEW monthly_savings_summary;
REFRESH MATERIALIZED VIEW daily_savings_trend;
REFRESH MATERIALIZED VIEW provider_savings_summary;

-- ------------------------------------------------------------------
-- M5: drop_old_savings_partitions swallowed every error via WHEN OTHERS,
-- downgrading lock-timeout / dependency / permission failures to a warning
-- so retention could silently fail. Narrow the handler to the expected
-- name-parse error and surface SQLERRM in the warning (N4).
-- ------------------------------------------------------------------
CREATE OR REPLACE FUNCTION drop_old_savings_partitions(retention_months INTEGER DEFAULT 24)
RETURNS void AS $$
DECLARE
    partition_record RECORD;
    partition_date DATE;
    cutoff_date DATE;
BEGIN
    cutoff_date := DATE_TRUNC('month', CURRENT_DATE) - (retention_months || ' months')::INTERVAL;

    FOR partition_record IN
        SELECT tablename FROM pg_tables
        WHERE schemaname = 'public'
        AND tablename LIKE 'savings_snapshots_%'
        AND tablename != 'savings_snapshots_default'
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
