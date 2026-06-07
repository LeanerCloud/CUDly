-- Rollback migration 000066: remove audit actor stamp columns.
-- The FK constraint on transitioned_by is dropped automatically with the column.

ALTER TABLE purchase_executions
    DROP COLUMN IF EXISTS transitioned_by,
    DROP COLUMN IF EXISTS transitioned_at;

ALTER TABLE ri_exchange_history
    DROP COLUMN IF EXISTS transitioned_by,
    DROP COLUMN IF EXISTS transitioned_at;

ALTER TABLE account_registrations
    DROP COLUMN IF EXISTS transitioned_by,
    DROP COLUMN IF EXISTS transitioned_at;
