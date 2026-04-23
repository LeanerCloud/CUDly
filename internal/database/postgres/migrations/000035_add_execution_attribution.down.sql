ALTER TABLE purchase_executions
    DROP COLUMN IF EXISTS approved_by,
    DROP COLUMN IF EXISTS cancelled_by;
