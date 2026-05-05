-- Add recommendations cycle parameters to global_config.
--
-- recommendations_cache_stale_hours:
--   Age (hours) at which the recommendation cache is considered stale and a
--   background refresh fires automatically (stale-while-revalidate pattern).
--   0 disables automatic background refresh entirely; the cron scheduler and
--   the manual Refresh button still work regardless of this setting.
--   Valid range: 0–8760 (up to one year). Default: 24.
--
-- recommendations_lookback_days:
--   AWS Cost Explorer lookback window used when fetching fresh recommendations.
--   Must be one of 7, 30, or 60 (matches the AWS Cost Explorer
--   LookbackPeriodInDays enum). Default: 7.
--   GCP CUD Recommender has no equivalent lookback parameter; this value
--   applies to AWS only. Azure support is tracked as a follow-up.
ALTER TABLE global_config
    ADD COLUMN IF NOT EXISTS recommendations_cache_stale_hours INT NOT NULL DEFAULT 24,
    ADD COLUMN IF NOT EXISTS recommendations_lookback_days INT NOT NULL DEFAULT 7;

-- Enforce the documented ranges at the DB layer too. Application-side
-- validation catches the API path, but a manual SQL update or a future
-- direct-DB writer would otherwise be able to persist out-of-range values
-- that GetGlobalConfig() then reads back verbatim and hands to the
-- scheduler. NOT VALID + VALIDATE keeps the migration online-safe in
-- case any pre-default rows ever slipped through (defaults guarantee
-- validity for rows added by this migration, but defensive anyway).
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_global_config_recommendations_cache_stale_hours_range'
    ) THEN
        ALTER TABLE global_config
            ADD CONSTRAINT chk_global_config_recommendations_cache_stale_hours_range
                CHECK (recommendations_cache_stale_hours BETWEEN 0 AND 8760);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_global_config_recommendations_lookback_days_allowed'
    ) THEN
        ALTER TABLE global_config
            ADD CONSTRAINT chk_global_config_recommendations_lookback_days_allowed
                CHECK (recommendations_lookback_days IN (7, 30, 60));
    END IF;
END $$;
