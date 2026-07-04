-- Rollback migration 000081: drop the ladder_tranches table.
--
-- The run_id column and its partial index are dropped explicitly first, in
-- FK-safe order (index, then the FK-holding column), before the table itself.
-- DROP TABLE would remove both implicitly, but listing them keeps the down
-- migration an exact inverse of the up. Remaining indexes are dropped
-- automatically with the table.

DROP INDEX IF EXISTS idx_ladder_tranches_run_id;

ALTER TABLE ladder_tranches
    DROP COLUMN IF EXISTS run_id;

DROP TABLE IF EXISTS ladder_tranches;
