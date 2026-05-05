ALTER TABLE global_config
    DROP COLUMN IF EXISTS recommendations_cache_stale_hours,
    DROP COLUMN IF EXISTS recommendations_lookback_days;
