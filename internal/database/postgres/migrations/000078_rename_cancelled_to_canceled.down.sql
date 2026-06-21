-- Rollback 000078: reverse the expand-contract rename 'canceled' -> 'cancelled'.
--
-- Restores the single-spelling constraints (British 'cancelled' only),
-- removes the canceled_by column added in the up migration, and
-- backfills 'canceled' rows back to 'cancelled'.
--
-- Note: the CONTRACT step was not applied (it lives in a separate follow-up
-- migration), so this rollback only needs to undo the expand + backfill.

-- ===========================================================================
-- 1. Restore purchase_executions status CHECK to 'cancelled' only.
-- ===========================================================================
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
            'revocation_requested','scheduled'
        ));
END $$;

-- ===========================================================================
-- 2. Restore ri_exchange_history status CHECK to 'cancelled' only.
-- ===========================================================================
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'ri_exchange_history'
          AND constraint_name = 'ri_exchange_history_status_check'
    ) THEN
        ALTER TABLE ri_exchange_history DROP CONSTRAINT ri_exchange_history_status_check;
    END IF;

    ALTER TABLE ri_exchange_history ADD CONSTRAINT ri_exchange_history_status_check
        CHECK (status IN ('pending', 'processing', 'completed', 'failed', 'cancelled'));
END $$;

-- ===========================================================================
-- 3. Remove canceled_by column added by the up migration.
-- ===========================================================================
ALTER TABLE purchase_executions
    DROP COLUMN IF EXISTS canceled_by;

-- ===========================================================================
-- 4. Backfill rows back: 'canceled' -> 'cancelled'.
-- ===========================================================================
UPDATE purchase_executions
SET status = 'cancelled'
WHERE status = 'canceled';

UPDATE ri_exchange_history
SET status = 'cancelled'
WHERE status = 'canceled';
