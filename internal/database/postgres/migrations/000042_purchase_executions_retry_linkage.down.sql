DROP INDEX IF EXISTS idx_executions_retry_target;

ALTER TABLE purchase_executions
    DROP COLUMN IF EXISTS retry_attempt_n;

ALTER TABLE purchase_executions
    DROP COLUMN IF EXISTS retry_execution_id;
