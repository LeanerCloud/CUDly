-- Add 'paused' to the valid_status check constraint on purchase_executions.
-- The pausePlannedPurchase handler sets status = 'paused' when a user clicks
-- Pause on a scheduled execution; the constraint omitted this value, causing
-- a CHECK violation (SQLSTATE 23514) at runtime.

ALTER TABLE purchase_executions
    DROP CONSTRAINT valid_status;

ALTER TABLE purchase_executions
    ADD CONSTRAINT valid_status
        CHECK (status IN ('pending', 'running', 'notified', 'approved', 'cancelled', 'completed', 'failed', 'paused'));
