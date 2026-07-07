-- Rollback migration 000080: remove ladder_run_id columns from
-- ri_exchange_history and purchase_executions, then drop ladder_runs.
-- Order matters: remove the FK-holding columns before dropping the
-- table they reference.

DROP INDEX IF EXISTS idx_ri_exchange_history_ladder_run;
ALTER TABLE ri_exchange_history
    DROP COLUMN IF EXISTS ladder_run_id;

DROP INDEX IF EXISTS idx_purchase_executions_ladder_run;
ALTER TABLE purchase_executions
    DROP COLUMN IF EXISTS ladder_run_id;

DROP TABLE IF EXISTS ladder_runs;
