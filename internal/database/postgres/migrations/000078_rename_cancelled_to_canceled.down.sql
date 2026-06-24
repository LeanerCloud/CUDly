-- Rollback 000078: reverse the expand-contract rename 'canceled' -> 'cancelled'.
--
-- Restores the single-spelling constraints (British 'cancelled' only),
-- removes the canceled_by column added in the up migration, and
-- backfills 'canceled' rows back to 'cancelled'.
--
-- Note: the CONTRACT step was not applied (it lives in a separate follow-up
-- migration), so this rollback only needs to undo the expand + backfill.
--
-- ORDER MATTERS. Every step that re-introduces a 'cancelled'-only CHECK must
-- run AFTER the data is converted back to 'cancelled', otherwise any row left
-- in the new 'canceled' state violates the constraint the instant it is added
-- and the whole rollback transaction fails. Likewise, the canceled_by column
-- must be drained back into cancelled_by BEFORE it is dropped, or the actor
-- attribution recorded by new code during the deploy window is lost forever.
--
-- Sequence:
--   1. Convert data back: 'canceled' -> 'cancelled' in both tables.
--   2. Drain actor attribution: canceled_by -> cancelled_by, then drop canceled_by.
--   3. Re-add the 'cancelled'-only CHECK constraints (now safe -- no 'canceled' rows remain).

-- ===========================================================================
-- 1. Convert data back BEFORE touching constraints.
--    'canceled' -> 'cancelled' in both tables so the narrowed CHECK added in
--    step 3 has no violating rows to reject.
-- ===========================================================================
UPDATE purchase_executions
SET status = 'cancelled'
WHERE status = 'canceled';

UPDATE ri_exchange_history
SET status = 'cancelled'
WHERE status = 'canceled';

-- ===========================================================================
-- 2. Restore actor attribution into cancelled_by, then drop canceled_by.
--    Backfill BEFORE the drop so any rows canceled by new code during the
--    deploy window keep their canceled_by actor in the legacy column.
-- ===========================================================================
UPDATE purchase_executions
SET cancelled_by = canceled_by
WHERE cancelled_by IS NULL
  AND canceled_by IS NOT NULL;

ALTER TABLE purchase_executions
    DROP COLUMN IF EXISTS canceled_by;

-- ===========================================================================
-- 3. Restore purchase_executions status CHECK to 'cancelled' only.
--    Safe now: step 1 removed every 'canceled' row.
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
-- 4. Restore ri_exchange_history status CHECK to 'cancelled' only.
--    Safe now: step 1 removed every 'canceled' row.
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
