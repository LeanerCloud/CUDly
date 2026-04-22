-- Revert 000033: restore purchase_executions.plan_id to NOT NULL + CASCADE.
--
-- WARNING: if any rows have been written with NULL plan_id (direct
-- executions from the Recommendations page), the NOT NULL restoration
-- will fail. Delete or back-fill those rows before running this down.
ALTER TABLE purchase_executions
    DROP CONSTRAINT IF EXISTS purchase_executions_plan_id_fkey;

ALTER TABLE purchase_executions
    ALTER COLUMN plan_id SET NOT NULL;

ALTER TABLE purchase_executions
    ADD CONSTRAINT purchase_executions_plan_id_fkey
        FOREIGN KEY (plan_id) REFERENCES purchase_plans(id) ON DELETE CASCADE;
