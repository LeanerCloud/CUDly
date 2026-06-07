-- Reverse 000066: drop the idempotency lineage key column.
ALTER TABLE purchase_executions
    DROP COLUMN IF EXISTS idempotency_key;
