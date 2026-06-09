-- Add 'running' to the valid_status check constraint on purchase_executions.
-- The runPlannedPurchase handler sets status = 'running' when an execution is
-- triggered; the original constraint omitted this value.

ALTER TABLE purchase_executions
    DROP CONSTRAINT valid_status;

ALTER TABLE purchase_executions
    ADD CONSTRAINT valid_status
        CHECK (status IN ('pending', 'running', 'notified', 'approved', 'cancelled', 'completed', 'failed'));
