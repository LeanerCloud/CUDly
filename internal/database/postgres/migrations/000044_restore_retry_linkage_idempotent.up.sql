-- 000044: idempotent corrective for the missing retry_execution_id column.
--
-- Background: PR #168 added 000042_purchase_executions_retry_linkage.
-- Concurrently, PR #189 had landed 000042_recommendations_add_engine_to_key
-- earlier the same day. Both files claimed version 42; deployed Lambdas
-- that ran migrate.Up() between the two merges recorded version=42 in
-- schema_migrations as "engine" content. PR #195 resolved the on-disk
-- duplicate by renaming recommendations to 000043 (instead of renaming
-- the later #168 migration to 000043), so deployed DBs ended up with:
--
--   schema_migrations.version = 42  (= engine content, applied)
--   on-disk file 42             (= retry_linkage content, NEVER applied)
--   on-disk file 43             (= engine content again, applied as no-op
--                                  thanks to ADD COLUMN IF NOT EXISTS)
--
-- Result: the retry_execution_id and retry_attempt_n columns from #168
-- were never added in production, but the Go code (`internal/config/types.go`
-- + `GetExecutionByID` SELECT) references them. Every /api/purchases/*
-- request 500'd with `column "retry_execution_id" does not exist
-- (SQLSTATE 42703)`. Surfaced as "Failed to load purchase details" /
-- "Failed to cancel purchase" toasts on the dashboard's Upcoming
-- Scheduled Purchases panel — see issues #204 and #205.
--
-- Fix strategy: replay 000042's schema changes wrapped in IF NOT EXISTS
-- so this migration is a no-op on any DB that already has the columns
-- (e.g., a fresh deploy that took the post-#195 migration order
-- correctly, or one manually fixed via DDL). Forward-only: we don't
-- attempt to repair schema_migrations history because (a) golang-migrate
-- only cares about the high-water-version, (b) we can't know without
-- DB inspection whether a given environment skipped 42 or applied
-- 43-as-engine-twice. The IF NOT EXISTS guards close both cases.
--
-- Edge case acknowledged: a DB that has the column but is missing the
-- self-FK (only possible via manual `ALTER TABLE ... DROP CONSTRAINT`)
-- won't get the FK re-added by this migration; ADD COLUMN IF NOT EXISTS
-- skips the entire clause when the column exists. That state is not
-- reachable from any path through 000042/000043/000044 themselves, so
-- it would only arise from operator surgery — fix manually if it
-- happens.

ALTER TABLE purchase_executions
    ADD COLUMN IF NOT EXISTS retry_execution_id UUID
        REFERENCES purchase_executions(execution_id) ON DELETE SET NULL;

ALTER TABLE purchase_executions
    ADD COLUMN IF NOT EXISTS retry_attempt_n INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_executions_retry_target
    ON purchase_executions(retry_execution_id)
    WHERE retry_execution_id IS NOT NULL;
