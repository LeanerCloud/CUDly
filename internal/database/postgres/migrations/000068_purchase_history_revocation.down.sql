-- Rollback 000068: remove revocation columns from purchase_history
DROP INDEX IF EXISTS idx_purchase_history_revocation_window;

-- Drop audit consistency constraints before dropping the columns.
ALTER TABLE purchase_history
    DROP CONSTRAINT IF EXISTS purchase_history_revoked_pair_chk,
    DROP CONSTRAINT IF EXISTS purchase_history_support_case_chk,
    DROP CONSTRAINT IF EXISTS purchase_history_revoked_via_chk;

ALTER TABLE purchase_history
    DROP COLUMN IF EXISTS support_case_id,
    DROP COLUMN IF EXISTS revoked_via,
    DROP COLUMN IF EXISTS revoked_at,
    DROP COLUMN IF EXISTS revocation_window_closes_at;

-- Rollback Gmail-style pre-fire delay columns (issue #291 wave-2).
-- Restore CHECK constraint without 'scheduled'.
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'purchase_executions'
          AND constraint_name = 'purchase_executions_status_check'
    ) THEN
        ALTER TABLE purchase_executions DROP CONSTRAINT purchase_executions_status_check;
    END IF;
    ALTER TABLE purchase_executions ADD CONSTRAINT purchase_executions_status_check
        CHECK (status IN (
            'pending','notified','approved','running','completed',
            'partially_completed','failed','cancelled','expired','paused',
            'revocation_requested'
        ));
END $$;

ALTER TABLE purchase_executions
    DROP COLUMN IF EXISTS scheduled_execution_at;

ALTER TABLE global_config
    DROP COLUMN IF EXISTS purchase_delay_hours;
