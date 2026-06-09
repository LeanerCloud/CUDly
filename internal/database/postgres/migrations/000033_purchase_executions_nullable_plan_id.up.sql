-- 000033: make purchase_executions.plan_id nullable
--
-- The POST /api/purchases/execute handler (direct execution from the
-- Recommendations page, no Plan involved) writes rows with an empty
-- plan_id. The original schema declared plan_id as
-- `UUID NOT NULL REFERENCES purchase_plans(id) ON DELETE CASCADE`,
-- which rejected the INSERT with "invalid input syntax for type uuid:
-- \"\"" and the handler surfaced it as a generic 500 to the frontend.
--
-- Fix: allow NULL plan_id for direct-execute rows. Keep the FK intact
-- but switch its delete behaviour to SET NULL so deleting an
-- originating plan doesn't cascade-delete its history.
ALTER TABLE purchase_executions
    ALTER COLUMN plan_id DROP NOT NULL;

ALTER TABLE purchase_executions
    DROP CONSTRAINT IF EXISTS purchase_executions_plan_id_fkey;

ALTER TABLE purchase_executions
    ADD CONSTRAINT purchase_executions_plan_id_fkey
        FOREIGN KEY (plan_id) REFERENCES purchase_plans(id) ON DELETE SET NULL;
