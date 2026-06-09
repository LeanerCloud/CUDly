ALTER TABLE global_config
    DROP COLUMN IF EXISTS ri_exchange_enabled,
    DROP COLUMN IF EXISTS ri_exchange_mode,
    DROP COLUMN IF EXISTS ri_exchange_utilization_threshold,
    DROP COLUMN IF EXISTS ri_exchange_max_per_exchange_usd,
    DROP COLUMN IF EXISTS ri_exchange_max_daily_usd,
    DROP COLUMN IF EXISTS ri_exchange_lookback_days;
