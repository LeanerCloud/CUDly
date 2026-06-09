ALTER TABLE purchase_executions
    DROP CONSTRAINT valid_status;

ALTER TABLE purchase_executions
    ADD CONSTRAINT valid_status
        CHECK (status IN ('pending', 'notified', 'approved', 'cancelled', 'completed', 'failed'));
