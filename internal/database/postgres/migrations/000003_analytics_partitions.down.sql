-- Drop materialized views
DROP MATERIALIZED VIEW IF EXISTS provider_savings_summary CASCADE;
DROP MATERIALIZED VIEW IF EXISTS daily_savings_trend CASCADE;
DROP MATERIALIZED VIEW IF EXISTS monthly_savings_summary CASCADE;

-- Drop partition management functions
DROP FUNCTION IF EXISTS refresh_savings_materialized_views CASCADE;
DROP FUNCTION IF EXISTS drop_old_savings_partitions CASCADE;
DROP FUNCTION IF EXISTS create_future_savings_partitions CASCADE;
DROP FUNCTION IF EXISTS create_savings_snapshot_partition CASCADE;

-- Drop all partition tables (they CASCADE from savings_snapshots in schema down migration)
-- This is just cleanup in case partitions were created
DO $$
DECLARE
    partition_record RECORD;
BEGIN
    FOR partition_record IN
        SELECT tablename FROM pg_tables
        WHERE schemaname = 'public'
        AND tablename LIKE 'savings_snapshots_%'
        AND tablename != 'savings_snapshots'
    LOOP
        EXECUTE format('DROP TABLE IF EXISTS %I CASCADE', partition_record.tablename);
    END LOOP;
END $$;
