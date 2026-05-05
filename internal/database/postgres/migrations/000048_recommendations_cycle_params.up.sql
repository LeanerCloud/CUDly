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
