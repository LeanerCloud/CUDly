-- Restore the historical default of 3 — the value 000001_initial_schema set
-- on this column. The previous version of this down migration restored
-- DEFAULT 12 instead, which is invalid: the term CHECK constraint only
-- accepts 0 (None), 1 (1yr), and 3 (3yr). A rollback through 007 followed
-- by an INSERT without an explicit `term` would have produced a row with
-- term=12 that no application code path knows how to handle.
--
-- Note: This does NOT revert data changes from the UPDATE in the up
-- migration; we can't know which rows originally had term=12 vs term=3.
ALTER TABLE service_configs ALTER COLUMN term SET DEFAULT 3;
