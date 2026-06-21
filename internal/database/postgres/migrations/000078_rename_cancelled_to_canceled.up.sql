-- Migration 000078: expand-contract rename 'cancelled' -> 'canceled' (US spelling)
--
-- This is the EXPAND + BACKFILL step only.  The destructive CONTRACT step
-- (dropping 'cancelled' from all constraints and dropping cancelled_by) is
-- deferred to a follow-up migration once this deploy is verified in prod.
--
-- Tables affected:
--   purchase_executions  -- status CHECK + cancelled_by column
--   ri_exchange_history  -- status CHECK
--
-- Expand-contract strategy (deploy safety):
--   1. Widen every CHECK constraint to accept BOTH 'cancelled' AND 'canceled'.
--      Old code writing 'cancelled' and new code writing 'canceled' are both
--      valid throughout the rolling deploy window.
--   2. Add canceled_by column and backfill from cancelled_by.
--   3. Backfill existing 'cancelled' rows to 'canceled'.
--
-- DEPLOY ORDER: this migration MUST run before or with the code deploy.
-- Because the constraints accept both spellings the order is forgiving: if
-- the code deploy races ahead briefly, it will write 'canceled' into the
-- already-widened constraint.  If the migration runs first and old code is
-- still live, 'cancelled' continues to satisfy the widened constraint.
--
-- The follow-up CONTRACT migration (see GitHub issue filed alongside this PR)
-- will drop 'cancelled' from constraints and drop cancelled_by only after
-- this deploy has been verified stable.
--
-- Idempotency: all DDL is wrapped in DO blocks with existence checks so the
-- migration is safe to re-run on a partially-migrated database.

-- ===========================================================================
-- 1a. purchase_executions: widen status CHECK to accept both spellings.
--
--     Constraint history:
--       migration 001: named 'valid_status' (initial schema)
--       migrations 013, 055: renamed/recreated as 'valid_status'
--       migration 070: conditionally replaced with 'purchase_executions_status_check'
--         BUT only if 'purchase_executions_status_check' already existed;
--         'valid_status' is NOT dropped by migration 070.
--     In a DB that ran every migration sequentially both constraints may exist.
--     We drop whichever are present and recreate only 'purchase_executions_status_check'.
-- ===========================================================================
DO $$ BEGIN
    -- Drop the old 'valid_status' constraint if it still exists (from migrations 001/013/055).
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'purchase_executions'
          AND constraint_name = 'valid_status'
    ) THEN
        ALTER TABLE purchase_executions DROP CONSTRAINT valid_status;
    END IF;

    -- Drop the newer 'purchase_executions_status_check' constraint if it exists (from migration 070).
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'purchase_executions'
          AND constraint_name = 'purchase_executions_status_check'
    ) THEN
        ALTER TABLE purchase_executions DROP CONSTRAINT purchase_executions_status_check;
    END IF;

    -- Re-create as 'purchase_executions_status_check' accepting BOTH spellings.
    -- The contract follow-up migration will drop 'cancelled' from this list.
    ALTER TABLE purchase_executions ADD CONSTRAINT purchase_executions_status_check
        CHECK (status IN (
            'pending','notified','approved','running','completed',
            'partially_completed','failed',
            'cancelled','canceled',
            'expired','paused','revocation_requested','scheduled'
        ));
END $$;

-- ===========================================================================
-- 1b. ri_exchange_history: widen status CHECK to accept both spellings.
--
--     The inline unnamed CHECK from migration 009 is auto-named by Postgres
--     (typically 'ri_exchange_history_status_check').  We locate it via
--     information_schema and drop it, then create a named constraint.
-- ===========================================================================
DO $$ DECLARE
    v_constraint TEXT;
BEGIN
    -- Find any auto-generated or existing CHECK on the status column by pattern.
    SELECT constraint_name INTO v_constraint
    FROM information_schema.table_constraints
    WHERE table_name = 'ri_exchange_history'
      AND constraint_type = 'CHECK'
      AND constraint_name LIKE 'ri_exchange_history_status%'
      AND constraint_name <> 'ri_exchange_history_status_check'
    LIMIT 1;

    IF v_constraint IS NOT NULL THEN
        EXECUTE 'ALTER TABLE ri_exchange_history DROP CONSTRAINT ' || quote_ident(v_constraint);
    END IF;

    -- Also drop by the explicit name we use, in case a prior partial run created it.
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'ri_exchange_history'
          AND constraint_name = 'ri_exchange_history_status_check'
    ) THEN
        ALTER TABLE ri_exchange_history DROP CONSTRAINT ri_exchange_history_status_check;
    END IF;

    -- Re-create with both spellings.
    -- The contract follow-up migration will drop 'cancelled' from this list.
    ALTER TABLE ri_exchange_history ADD CONSTRAINT ri_exchange_history_status_check
        CHECK (status IN ('pending', 'processing', 'completed', 'failed', 'cancelled', 'canceled'));
END $$;

-- ===========================================================================
-- 2. purchase_executions: add canceled_by column and backfill from cancelled_by.
--    The old cancelled_by column is kept; the contract follow-up migration
--    will drop it after this deploy is verified.
-- ===========================================================================
ALTER TABLE purchase_executions
    ADD COLUMN IF NOT EXISTS canceled_by TEXT;

UPDATE purchase_executions
SET canceled_by = cancelled_by
WHERE canceled_by IS NULL
  AND cancelled_by IS NOT NULL;

-- ===========================================================================
-- 3. Backfill existing rows: 'cancelled' -> 'canceled' in both tables.
--    After this step all rows use the US spelling; the dual-accept constraint
--    from step 1 allows old code to continue writing 'cancelled' during the
--    rolling deploy window without causing CHECK violations.
-- ===========================================================================
UPDATE purchase_executions
SET status = 'canceled'
WHERE status = 'cancelled';

UPDATE ri_exchange_history
SET status = 'canceled'
WHERE status = 'cancelled';
