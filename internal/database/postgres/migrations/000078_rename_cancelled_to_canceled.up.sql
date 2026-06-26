-- Migration 000078: expand-contract rename 'cancelled' -> 'canceled' (US spelling)
--
-- This is the EXPAND step only, and it is intentionally NON-DESTRUCTIVE: it
-- widens constraints and adds a column, but it does NOT normalize existing
-- legacy values. Value normalization ('cancelled' -> 'canceled' for status,
-- and draining any late cancelled_by-only writes into canceled_by) and the
-- destructive drops are BOTH deferred to the CONTRACT migration (#1278),
-- which runs AFTER every old code instance is gone.
--
-- Tables affected:
--   purchase_executions  -- status CHECK + cancelled_by column
--   ri_exchange_history  -- status CHECK
--
-- WHY NORMALIZATION IS DEFERRED (deploy-safety argument):
-- During the rolling deploy both old and new code run concurrently. Old code
-- keeps writing status='cancelled' and cancelled_by, while new code writes
-- status='canceled' and canceled_by. A one-time UP backfill that normalized
-- 'cancelled' -> 'canceled' could not be complete: old instances would write
-- fresh 'cancelled' rows immediately AFTER the backfill ran. So normalization
-- here would be a false guarantee. Instead this migration makes the schema
-- accept BOTH spellings forever (until contract), and the application reads
-- BOTH at all times:
--   * status: handler_history.historyExecutionStatuses + the cancel/KPI
--     switches accept 'cancelled' and 'canceled'.
--   * cancelled_by/canceled_by: every read projects
--     COALESCE(canceled_by, cancelled_by), so a row written by EITHER old or
--     new code at ANY point in the deploy window reads correctly.
-- This means NO row, whenever written, is mis-read during EXPAND. The
-- CONTRACT migration (#1278) then normalizes every legacy value (the now-
-- complete set, since old code is gone) and drops the legacy spelling/column.
--
-- Expand-contract strategy (what THIS migration does):
--   1. Widen every CHECK constraint to accept BOTH 'cancelled' AND 'canceled'.
--      Old code writing 'cancelled' and new code writing 'canceled' are both
--      valid throughout the rolling deploy window.
--   2. Add canceled_by column. (A convenience copy of existing cancelled_by
--      values is done so new-code reads see attribution immediately, but reads
--      do NOT depend on it: the COALESCE covers any row this copy misses,
--      including rows old code writes after the copy runs.)
--   3. (Deferred to #1278) normalize status values + drain late cancelled_by.
--
-- DEPLOY ORDER: this migration MUST complete before new code can write
-- 'canceled'. Once the widened constraints are installed, old code writing
-- 'cancelled' and new code writing 'canceled' are both accepted throughout
-- the rolling deploy window.
--
-- The follow-up CONTRACT migration (#1278) will normalize all legacy values
-- and drop 'cancelled' from constraints + drop cancelled_by, only after this
-- deploy has been verified stable and all old code instances are gone.
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
-- 2. purchase_executions: add canceled_by column and copy existing
--    cancelled_by values into it as a convenience so new-code reads see
--    attribution immediately.
--
--    IMPORTANT: this copy is best-effort, not authoritative. Old code running
--    during the rolling deploy can write a fresh cancelled_by-only value AFTER
--    this copy runs; that row's canceled_by stays NULL. Reads do NOT depend on
--    this copy -- every SELECT projects COALESCE(canceled_by, cancelled_by),
--    so a late cancelled_by-only write is still read correctly. The CONTRACT
--    migration (#1278) performs the authoritative, complete drain once old
--    code is gone, immediately before dropping cancelled_by.
--
--    The old cancelled_by column is kept; #1278 drops it.
-- ===========================================================================
ALTER TABLE purchase_executions
    ADD COLUMN IF NOT EXISTS canceled_by TEXT;

UPDATE purchase_executions
SET canceled_by = cancelled_by
WHERE canceled_by IS NULL
  AND cancelled_by IS NOT NULL;

-- ===========================================================================
-- 3. Status value normalization is intentionally NOT done here.
--
--    A one-time 'cancelled' -> 'canceled' UPDATE during EXPAND would be a false
--    guarantee: old code still running would write fresh 'cancelled' rows the
--    instant after it ran. The widened CHECK (step 1) keeps both spellings
--    valid, and the application reads both spellings everywhere (status filter
--    + KPI/cancel switches, and COALESCE for the column). The CONTRACT
--    migration (#1278) normalizes every legacy value once old code is gone --
--    at which point the set of legacy rows is final -- and only then narrows
--    the constraints and drops cancelled_by.
-- ===========================================================================
