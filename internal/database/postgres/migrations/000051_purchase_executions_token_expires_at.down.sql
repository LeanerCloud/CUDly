-- 000051 rollback: remove approval_token_expires_at from purchase_executions

DROP INDEX IF EXISTS idx_executions_token_expires_at;

ALTER TABLE purchase_executions
    DROP COLUMN IF EXISTS approval_token_expires_at;
