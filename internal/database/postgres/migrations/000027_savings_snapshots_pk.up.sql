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
