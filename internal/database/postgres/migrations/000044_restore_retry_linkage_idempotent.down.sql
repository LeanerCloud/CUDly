-- Idempotent rollback. Inverse of 000044's IF NOT EXISTS additions.
-- DROP COLUMN IF EXISTS leaves DBs that don't have the columns
-- (e.g. an environment that was already correct pre-#168) untouched.

DROP INDEX IF EXISTS idx_executions_retry_target;

ALTER TABLE purchase_executions
    DROP COLUMN IF EXISTS retry_attempt_n;

ALTER TABLE purchase_executions
    DROP COLUMN IF EXISTS retry_execution_id;
