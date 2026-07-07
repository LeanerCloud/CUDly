-- Rollback migration 000081: drop the ladder_tranches table.
-- run_id and every index on the table (including idx_ladder_tranches_run_id)
-- live ON this table, so DROP TABLE removes them all; no separate DROP COLUMN
-- is needed. The run_id -> ladder_runs foreign key is on the table being
-- dropped, so there is no drop-ordering constraint here.

DROP TABLE IF EXISTS ladder_tranches;
