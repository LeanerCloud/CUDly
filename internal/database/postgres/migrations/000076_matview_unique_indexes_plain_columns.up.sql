-- 000076: recreate the materialized-view unique indexes on plain columns so
-- REFRESH MATERIALIZED VIEW CONCURRENTLY works again.
--
-- Migration 000074 recreated idx_monthly_savings_summary_unique,
-- idx_daily_savings_trend_unique and idx_provider_savings_summary_unique as
-- COALESCE(cloud_account_id, ...) expression indexes. PostgreSQL requires the
-- unique index backing REFRESH ... CONCURRENTLY to use only column names (no
-- expressions, no WHERE clause), so refresh_savings_materialized_views() has
-- failed with SQLSTATE 55000 ("cannot refresh materialized view ...
-- concurrently") ever since.
--
-- The COALESCE existed to collapse NULL cloud_account_id values into a single
-- key; NULLS NOT DISTINCT (PostgreSQL 15+) preserves that dedup intent while
-- satisfying the CONCURRENTLY prerequisite. Row uniqueness at this grain is
-- already guaranteed by each view's GROUP BY, so the index swap does not
-- change view contents.

DROP INDEX IF EXISTS idx_monthly_savings_summary_unique;
CREATE UNIQUE INDEX idx_monthly_savings_summary_unique
    ON monthly_savings_summary (month, account_id, cloud_account_id, provider, service)
    NULLS NOT DISTINCT;

DROP INDEX IF EXISTS idx_daily_savings_trend_unique;
CREATE UNIQUE INDEX idx_daily_savings_trend_unique
    ON daily_savings_trend (day, account_id, cloud_account_id, provider)
    NULLS NOT DISTINCT;

DROP INDEX IF EXISTS idx_provider_savings_summary_unique;
CREATE UNIQUE INDEX idx_provider_savings_summary_unique
    ON provider_savings_summary (provider, account_id, cloud_account_id)
    NULLS NOT DISTINCT;
