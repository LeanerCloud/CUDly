-- Revert the materialized views to the account_id-only grain (000003 shape)
-- and restore the NOT NULL DEFAULT 0 columns + the original
-- drop_old_savings_partitions WHEN OTHERS handler.

DROP MATERIALIZED VIEW IF EXISTS provider_savings_summary CASCADE;
DROP MATERIALIZED VIEW IF EXISTS daily_savings_trend CASCADE;
DROP MATERIALIZED VIEW IF EXISTS monthly_savings_summary CASCADE;

CREATE MATERIALIZED VIEW monthly_savings_summary AS
SELECT
    DATE_TRUNC('month', timestamp) as month,
    account_id,
    provider,
    service,
    SUM(total_savings) as total_savings,
    AVG(coverage_percentage) as avg_coverage,
    SUM(total_commitment) as total_commitment,
    SUM(total_usage) as total_usage,
    COUNT(*) as snapshot_count,
    MAX(timestamp) as last_updated
FROM savings_snapshots
GROUP BY DATE_TRUNC('month', timestamp), account_id, provider, service;

CREATE UNIQUE INDEX idx_monthly_savings_summary_unique
    ON monthly_savings_summary(month, account_id, provider, service);

CREATE MATERIALIZED VIEW daily_savings_trend AS
SELECT
    DATE_TRUNC('day', timestamp) as day,
    account_id,
    provider,
    SUM(total_savings) as daily_savings,
    AVG(coverage_percentage) as avg_coverage,
    COUNT(DISTINCT service) as service_count
FROM savings_snapshots
GROUP BY DATE_TRUNC('day', timestamp), account_id, provider;

CREATE UNIQUE INDEX idx_daily_savings_trend_unique
    ON daily_savings_trend(day, account_id, provider);

CREATE MATERIALIZED VIEW provider_savings_summary AS
SELECT
    provider,
    account_id,
    COUNT(DISTINCT service) as service_count,
    SUM(total_savings) as total_savings,
    SUM(total_commitment) as total_commitment,
    AVG(coverage_percentage) as avg_coverage,
    MAX(timestamp) as last_updated
FROM savings_snapshots
WHERE timestamp > NOW() - INTERVAL '90 days'
GROUP BY provider, account_id;

CREATE UNIQUE INDEX idx_provider_savings_summary_unique
    ON provider_savings_summary(provider, account_id);

-- Restore NOT NULL DEFAULT 0 on the metric columns. NULLs are coerced to 0
-- first so the NOT NULL re-add cannot fail.
UPDATE savings_snapshots SET total_usage = 0 WHERE total_usage IS NULL;
UPDATE savings_snapshots SET coverage_percentage = 0 WHERE coverage_percentage IS NULL;
ALTER TABLE savings_snapshots ALTER COLUMN total_usage SET DEFAULT 0.00;
ALTER TABLE savings_snapshots ALTER COLUMN total_usage SET NOT NULL;
ALTER TABLE savings_snapshots ALTER COLUMN coverage_percentage SET DEFAULT 0.00;
ALTER TABLE savings_snapshots ALTER COLUMN coverage_percentage SET NOT NULL;

-- Refresh AFTER the data/schema are restored so the recreated views reflect the
-- coerced (non-NULL) values rather than a stale pre-restore state.
REFRESH MATERIALIZED VIEW monthly_savings_summary;
REFRESH MATERIALIZED VIEW daily_savings_trend;
REFRESH MATERIALIZED VIEW provider_savings_summary;

-- Restore the original (WHEN OTHERS) drop_old_savings_partitions body.
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

            IF partition_date < cutoff_date THEN
                EXECUTE format('DROP TABLE IF EXISTS %I', partition_record.tablename);
                RAISE NOTICE 'Dropped old partition: %', partition_record.tablename;
            END IF;
        EXCEPTION
            WHEN OTHERS THEN
                RAISE WARNING 'Could not process partition: %', partition_record.tablename;
        END;
    END LOOP;
END;
$$ LANGUAGE plpgsql;

-- account_id stays VARCHAR(255): narrowing back to VARCHAR(20) could truncate
-- Azure/GCP ids written while this migration was applied, so the down keeps the
-- wider type (safe, additive). This is a deliberate non-symmetric down.
