-- Idempotent rollback. CASCADE on DROP COLUMN removes the FK constraint
-- (whatever its name — auto-generated `*_fkey` from a fresh-deploy 000042
-- run, or `fk_purchase_executions_retry_execution_id` from this 000044's
-- step 2) and the partial index in one go. DROP INDEX IF EXISTS in front
-- of the column drops handles the case where the index outlived its
-- column (operator surgery only — not reachable from any path through
-- 000042/000043/000044).

DROP INDEX IF EXISTS idx_executions_retry_target;

ALTER TABLE purchase_executions DROP COLUMN IF EXISTS retry_attempt_n;

ALTER TABLE purchase_executions DROP COLUMN IF EXISTS retry_execution_id CASCADE;
