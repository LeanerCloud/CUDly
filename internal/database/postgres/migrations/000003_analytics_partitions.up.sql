-- ==========================================
-- ANALYTICS TIME-SERIES PARTITIONING
-- ==========================================

-- Function to create monthly partition for savings_snapshots
CREATE OR REPLACE FUNCTION create_savings_snapshot_partition(partition_date DATE)
RETURNS void AS $$
DECLARE
    partition_name TEXT;
    start_date DATE;
    end_date DATE;
BEGIN
    -- Calculate partition boundaries
    start_date := DATE_TRUNC('month', partition_date);
    end_date := start_date + INTERVAL '1 month';

    -- Generate partition table name (e.g., savings_snapshots_2024_01)
    partition_name := 'savings_snapshots_' || TO_CHAR(start_date, 'YYYY_MM');

    -- Check if partition already exists
    IF NOT EXISTS (
        SELECT 1 FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relname = partition_name AND n.nspname = 'public'
    ) THEN
        -- Create partition
        EXECUTE format(
            'CREATE TABLE %I PARTITION OF savings_snapshots FOR VALUES FROM (%L) TO (%L)',
            partition_name,
            start_date,
            end_date
        );

        -- Create partition-specific index for faster queries
        EXECUTE format(
            'CREATE INDEX %I ON %I (timestamp DESC, account_id)',
            'idx_' || partition_name || '_timestamp',
            partition_name
        );

        RAISE NOTICE 'Created partition: %', partition_name;
    ELSE
        RAISE NOTICE 'Partition % already exists', partition_name;
    END IF;
END;
$$ LANGUAGE plpgsql;

-- Function to automatically create partitions for the next N months
CREATE OR REPLACE FUNCTION create_future_savings_partitions(months_ahead INTEGER DEFAULT 3)
RETURNS void AS $$
DECLARE
    i INTEGER;
    partition_date DATE;
BEGIN
    -- Create partitions for current month + N months ahead
    FOR i IN 0..months_ahead LOOP
        partition_date := DATE_TRUNC('month', CURRENT_DATE) + (i || ' months')::INTERVAL;
        PERFORM create_savings_snapshot_partition(partition_date);
    END LOOP;
END;
$$ LANGUAGE plpgsql;

-- Function to drop old partitions (data retention policy)
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
        -- Extract date from partition name (e.g., savings_snapshots_2022_01 -> 2022-01-01)
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

-- ==========================================
-- MATERIALIZED VIEWS FOR ANALYTICS
-- ==========================================

-- Monthly savings summary (aggregated view for dashboards)
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

-- Create unique index for concurrent refresh
CREATE UNIQUE INDEX idx_monthly_savings_summary_unique
    ON monthly_savings_summary(month, account_id, provider, service);

-- Daily savings trend (for charts)
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

-- Provider summary (overall provider performance)
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

-- ==========================================
-- REFRESH FUNCTIONS
-- ==========================================

-- Function to refresh all materialized views
CREATE OR REPLACE FUNCTION refresh_savings_materialized_views()
RETURNS void AS $$
BEGIN
    REFRESH MATERIALIZED VIEW CONCURRENTLY monthly_savings_summary;
    REFRESH MATERIALIZED VIEW CONCURRENTLY daily_savings_trend;
    REFRESH MATERIALIZED VIEW CONCURRENTLY provider_savings_summary;
    RAISE NOTICE 'Refreshed all savings materialized views';
END;
$$ LANGUAGE plpgsql;

-- ==========================================
-- INITIALIZE PARTITIONS
-- ==========================================

-- Create partitions for current month + 3 months ahead
SELECT create_future_savings_partitions(3);

-- Initial refresh of materialized views (will be empty at first)
SELECT refresh_savings_materialized_views();

-- Add comment explaining partition maintenance
COMMENT ON FUNCTION create_savings_snapshot_partition IS
    'Creates a monthly partition for savings_snapshots. Should be called monthly via cron/scheduler.';

COMMENT ON FUNCTION create_future_savings_partitions IS
    'Creates partitions for the next N months. Run monthly to ensure partitions exist.';

COMMENT ON FUNCTION drop_old_savings_partitions IS
    'Drops partitions older than retention period (default 24 months). Run monthly for data retention.';

COMMENT ON FUNCTION refresh_savings_materialized_views IS
    'Refreshes all analytics materialized views. Run daily or after bulk data loads.';
