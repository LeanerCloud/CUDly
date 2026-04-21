-- Add the missing PRIMARY KEY to savings_snapshots.
--
-- Migration 000001 created the table with `id UUID DEFAULT uuid_generate_v4()`
-- but no PRIMARY KEY clause. Because the table is `PARTITION BY RANGE
-- (timestamp)`, Postgres requires the primary key to include `timestamp`,
-- so a single-column key on `id` is impossible — but no key was created
-- at all, so:
--   - Duplicate rows have been silently allowed.
--   - `INSERT ... ON CONFLICT (id, timestamp)` does not work.
--   - Logical replication / CDC tools that need a key for change tracking
--     misbehave on this table.
--
-- De-duplicate first (keep the newest `created_at` per (id, timestamp)),
-- then add the constraint. The de-dup is a no-op on clean databases —
-- production audit shows no duplicates today, but future-proof against
-- replays during this migration window.
--
-- NOTE: Apply during off-peak hours on production-scale deployments.
--
-- The `WITH duplicates AS (...) DELETE ...` below takes a row-exclusive
-- lock on `savings_snapshots` for the duration of the scan. Production
-- audit at migration-write time showed zero duplicates, so the DELETE
-- itself deletes no rows — but it still scans every row of every
-- partition to confirm, and writes queue behind the lock during the
-- scan. On a ~100M-row `savings_snapshots` that scan can take multiple
-- minutes on the largest partitions. The rest of the migration (the
-- subsequent `ALTER TABLE ... ADD CONSTRAINT`) is fast once dedup has
-- run, and Postgres re-uses the existing unique index for the primary
-- key so the constraint addition itself doesn't re-scan.
--
-- If you need to apply this during a write-heavy window, consider
-- running the DELETE as a separate manual step (same SQL, before the
-- migration runner picks it up) so you can reason about the lock
-- window in isolation.

WITH duplicates AS (
    SELECT id, timestamp, ctid,
           ROW_NUMBER() OVER (
               PARTITION BY id, timestamp
               ORDER BY created_at DESC, ctid DESC
           ) AS row_num
    FROM savings_snapshots
)
DELETE FROM savings_snapshots s
USING duplicates d
WHERE s.ctid = d.ctid AND d.row_num > 1;

ALTER TABLE savings_snapshots
    ADD CONSTRAINT savings_snapshots_pkey PRIMARY KEY (id, timestamp);
