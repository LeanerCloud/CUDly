-- 000076 down: restore the COALESCE expression unique indexes from 000074.
-- Note: with these expression indexes in place REFRESH MATERIALIZED VIEW
-- CONCURRENTLY fails (SQLSTATE 55000); this only reverts to the prior state.

DROP INDEX IF EXISTS idx_monthly_savings_summary_unique;
CREATE UNIQUE INDEX idx_monthly_savings_summary_unique
    ON monthly_savings_summary(
        month, account_id,
        COALESCE(cloud_account_id, '00000000-0000-0000-0000-000000000000'::uuid),
        provider, service);

DROP INDEX IF EXISTS idx_daily_savings_trend_unique;
CREATE UNIQUE INDEX idx_daily_savings_trend_unique
    ON daily_savings_trend(
        day, account_id,
        COALESCE(cloud_account_id, '00000000-0000-0000-0000-000000000000'::uuid),
        provider);

DROP INDEX IF EXISTS idx_provider_savings_summary_unique;
CREATE UNIQUE INDEX idx_provider_savings_summary_unique
    ON provider_savings_summary(
        provider, account_id,
        COALESCE(cloud_account_id, '00000000-0000-0000-0000-000000000000'::uuid));
