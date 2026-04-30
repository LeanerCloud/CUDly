-- 000044: idempotent corrective for the retry-linkage schema dropped
-- by migration drift between #168 / #189 / #195. Repairs every partial
-- state we know to be reachable, instead of just no-op'ing when columns
-- exist (per CR pass-1 feedback on PR #206).
--
-- Background — see the commit message of this migration's pair-PR for the
-- full chain. Short version: deployed DBs have schema_migrations.version =
-- 42 recorded against #189's "engine" content; on-disk file 42 is now
-- #168's retry_linkage which migrate skips because version 42 is already
-- marked applied. Result: the retry_execution_id / retry_attempt_n columns
-- were never created, but the Go SELECT in GetExecutionByID references
-- them, so /api/purchases/* 500s with "column does not exist" (SQLSTATE
-- 42703) — surfaced as "Failed to load purchase details" / "Failed to
-- cancel purchase" toasts on the dashboard's Upcoming Scheduled Purchases
-- panel (issues #204 + #205).
--
-- Reachable partial states this migration handles:
--
--   (a) Both columns missing, no FK, no index — the production state on
--       deployed dev/staging Lambdas as of 2026-04-30. Each step adds.
--   (b) Columns added by hand without the FK — possible after operator
--       surgery; step 2 re-adds the FK conditionally.
--   (c) retry_attempt_n added without DEFAULT 0 / NOT NULL — same; step
--       4 backfills NULLs and re-asserts the constraints (these ALTERs
--       are safe to run unconditionally — Postgres treats them as no-op
--       when the constraint is already in place).
--   (d) Index missing — step 5's CREATE INDEX IF NOT EXISTS adds it.
--   (e) Everything already present from a fresh deploy that took the
--       post-#195 migration order correctly — every step skips.
--
-- The pg_catalog / information_schema queries match by column name and
-- FK target, NOT by constraint name, so a partially-fixed DB with the
-- auto-generated `purchase_executions_retry_execution_id_fkey` (the name
-- 000042's inline FK clause would have produced) is recognised as
-- already-FK'd and step 2 skips.

-- Step 1: ensure retry_execution_id column exists.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'purchase_executions'
          AND column_name = 'retry_execution_id'
    ) THEN
        ALTER TABLE purchase_executions ADD COLUMN retry_execution_id UUID;
    END IF;
END $$;

-- Step 2: ensure a FK on retry_execution_id -> execution_id exists. Match
-- by column relationship, not constraint name, so a fresh-deploy DB whose
-- 000042 ran cleanly (FK named `purchase_executions_retry_execution_id_fkey`)
-- is recognised as already-FK'd and we don't add a second.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint c
        JOIN pg_attribute a
          ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
        WHERE c.conrelid = 'purchase_executions'::regclass
          AND c.contype = 'f'
          AND a.attname = 'retry_execution_id'
    ) THEN
        ALTER TABLE purchase_executions
            ADD CONSTRAINT fk_purchase_executions_retry_execution_id
            FOREIGN KEY (retry_execution_id)
            REFERENCES purchase_executions (execution_id)
            ON DELETE SET NULL;
    END IF;
END $$;

-- Step 3: ensure retry_attempt_n column exists. Add as nullable here so
-- step 4 can backfill before flipping to NOT NULL — guards against a
-- partially-fixed DB that has the column with NULLs.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'purchase_executions'
          AND column_name = 'retry_attempt_n'
    ) THEN
        ALTER TABLE purchase_executions ADD COLUMN retry_attempt_n INTEGER;
    END IF;
END $$;

-- Step 4: enforce the NOT NULL DEFAULT 0 contract on retry_attempt_n.
-- Safe to run unconditionally: SET DEFAULT and SET NOT NULL are no-ops
-- when the constraint is already as requested. The UPDATE only touches
-- rows that have NULL, which exists only on partially-fixed DBs.
UPDATE purchase_executions SET retry_attempt_n = 0 WHERE retry_attempt_n IS NULL;
ALTER TABLE purchase_executions ALTER COLUMN retry_attempt_n SET DEFAULT 0;
ALTER TABLE purchase_executions ALTER COLUMN retry_attempt_n SET NOT NULL;

-- Step 5: partial index. CREATE INDEX IF NOT EXISTS skips silently if a
-- collision name exists, so a fresh-deploy DB that already has it is
-- left alone.
CREATE INDEX IF NOT EXISTS idx_executions_retry_target
    ON purchase_executions (retry_execution_id)
    WHERE retry_execution_id IS NOT NULL;
