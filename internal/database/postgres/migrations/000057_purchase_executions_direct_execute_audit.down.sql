DROP INDEX IF EXISTS idx_executions_direct_execute;

ALTER TABLE purchase_executions
    DROP COLUMN IF EXISTS pre_approval_skip_reason,
    DROP COLUMN IF EXISTS executed_at,
    DROP COLUMN IF EXISTS executed_by_user_id;
