-- 000078 down: restore the flat-AVG monthly_savings_summary definition from
-- 000067/000074 (which understates multi-row buckets, per COR-02), and
-- recreate the unique index using NULLS NOT DISTINCT (as 000076 left it).
DROP MATERIALIZED VIEW IF EXISTS monthly_savings_summary CASCADE;

CREATE MATERIALIZED VIEW monthly_savings_summary AS
SELECT
    DATE_TRUNC('month', timestamp) as month,
    account_id,
    cloud_account_id,
    provider,
    service,
    AVG(total_savings) as total_savings,
    AVG(coverage_percentage) as avg_coverage,
    AVG(total_commitment) as total_commitment,
    AVG(total_usage) as total_usage,
    COUNT(*) as snapshot_count,
    MAX(timestamp) as last_updated
FROM savings_snapshots
GROUP BY DATE_TRUNC('month', timestamp), account_id, cloud_account_id, provider, service;

CREATE UNIQUE INDEX idx_monthly_savings_summary_unique
    ON monthly_savings_summary (month, account_id, cloud_account_id, provider, service)
    NULLS NOT DISTINCT;
