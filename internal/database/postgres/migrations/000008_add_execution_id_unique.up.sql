-- Add UNIQUE constraint on execution_id to support ON CONFLICT (execution_id)
-- used in store_postgres.go for upsert operations on purchase_executions.
ALTER TABLE purchase_executions ADD CONSTRAINT unique_execution_id UNIQUE (execution_id);
