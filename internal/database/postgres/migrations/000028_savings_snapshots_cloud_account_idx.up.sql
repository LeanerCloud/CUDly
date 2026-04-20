-- Add the missing partial index on savings_snapshots(cloud_account_id).
--
-- Migration 000011 added the cloud_account_id FK to savings_snapshots in
-- parallel with the same FK on purchase_executions and purchase_history,
-- but only the latter two got partial indexes — savings_snapshots was
-- left unindexed. Analytics queries filtering savings by cloud account
-- end up scanning the largest table sequentially.
--
-- Mirrors the partial-index pattern used on the sibling tables (only
-- index rows where the FK is set; bulk legacy rows have it NULL).
-- Postgres ≥11 propagates indexes on a partitioned parent to all
-- existing and future partitions automatically — no per-partition work
-- needed.
CREATE INDEX IF NOT EXISTS idx_savings_snapshots_cloud_account
    ON savings_snapshots(cloud_account_id)
    WHERE cloud_account_id IS NOT NULL;
