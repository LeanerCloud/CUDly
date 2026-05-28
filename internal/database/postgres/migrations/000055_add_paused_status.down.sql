-- Remove 'paused' from the valid_status check constraint.
-- Any rows currently in 'paused' state must be manually transitioned before
-- rolling back this migration.

ALTER TABLE purchase_executions
    DROP CONSTRAINT valid_status;

ALTER TABLE purchase_executions
    ADD CONSTRAINT valid_status
        CHECK (status IN ('pending', 'running', 'notified', 'approved', 'cancelled', 'completed', 'failed'));
