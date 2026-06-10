-- 000076: monthly_savings_summary nested SUM-then-AVG rollup (COR-02).
--
-- Snapshot rows are run-rates written at (account, provider, service, region,
-- commitment_type, timestamp) grain, so a (month, account, provider, service)
-- bucket contains several rows sharing the SAME timestamp (one per region /
-- commitment type). The 000067/000074 definition used a flat AVG across all
-- rows in the bucket, which returns the mean per-row run-rate instead of the
-- bucket's total: $100/mo in each of 5 regions reported total_savings=$100
-- instead of $500.
--
-- Apply the same H5 shape daily_savings_trend and provider_savings_summary
-- already use: the inner query sums the per-row run-rates into the bucket's
-- instant total at each collection timestamp, the outer query averages those
-- instant totals over the month so the result stays invariant to the
-- collection frequency. snapshot_count keeps its raw-row semantics via
-- SUM(ts_row_count) (cast back to BIGINT because SUM(bigint) yields NUMERIC).
--
-- DROP + CREATE because a materialized view's defining query cannot be
-- altered in place. The unique index is recreated for the CONCURRENTLY
-- refresh path, and the view is created populated (no WITH NO DATA) so
-- refresh_savings_materialized_views() can keep using CONCURRENTLY.
DROP MATERIALIZED VIEW IF EXISTS monthly_savings_summary CASCADE;

CREATE MATERIALIZED VIEW monthly_savings_summary AS
SELECT
    month,
    account_id,
    cloud_account_id,
    provider,
    service,
    AVG(ts_savings) as total_savings,
    AVG(ts_coverage) as avg_coverage,
    AVG(ts_commitment) as total_commitment,
    AVG(ts_usage) as total_usage,
    SUM(ts_row_count)::BIGINT as snapshot_count,
    MAX(timestamp) as last_updated
FROM (
    SELECT
        DATE_TRUNC('month', timestamp) as month,
        timestamp,
        account_id,
        cloud_account_id,
        provider,
        service,
        SUM(total_savings) as ts_savings,
        AVG(coverage_percentage) as ts_coverage,
        SUM(total_commitment) as ts_commitment,
        SUM(total_usage) as ts_usage,
        COUNT(*) as ts_row_count
    FROM savings_snapshots
    GROUP BY DATE_TRUNC('month', timestamp), timestamp,
             account_id, cloud_account_id, provider, service
) per_ts
GROUP BY month, account_id, cloud_account_id, provider, service;

-- cloud_account_id is nullable, so COALESCE it to the nil UUID inside the
-- unique index to keep the (month, account, provider, service) grain unique
-- under CONCURRENTLY refresh even when cloud_account_id IS NULL.
CREATE UNIQUE INDEX idx_monthly_savings_summary_unique
    ON monthly_savings_summary(
        month, account_id,
        COALESCE(cloud_account_id, '00000000-0000-0000-0000-000000000000'::uuid),
        provider, service);
