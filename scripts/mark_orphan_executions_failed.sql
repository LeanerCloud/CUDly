-- mark_orphan_executions_failed.sql
--
-- One-shot cleanup for issue #606: before migration 000053 tightened the
-- purchase_executions.cloud_account_id FK to ON DELETE RESTRICT, deleting a
-- cloud account silently set cloud_account_id = NULL on every pending /
-- notified execution that referenced it. Those rows can never execute
-- (the executor has no account to dial credentials against) yet still
-- show as "pending" in the dashboard.
--
-- Run this script ONCE per deployment after migration 000053 lands to
-- mark the existing orphan rows as failed so operators stop seeing them
-- in the approve queue. The migration itself does NOT do this — it's a
-- one-time data cleanup, not a schema change, so it lives here.
--
--   psql "$DATABASE_URL" -f scripts/mark_orphan_executions_failed.sql
--
-- Idempotent: re-running is a no-op because all affected rows will be
-- 'failed' after the first run. Wrapped in a transaction so a syntax
-- error or partial failure rolls back cleanly.

BEGIN;

-- Show the rows we're about to update so the operator can sanity-check.
SELECT execution_id,
       status,
       scheduled_date,
       plan_id,
       'will mark failed' AS action
FROM purchase_executions
WHERE cloud_account_id IS NULL
  AND status IN ('pending', 'notified');

UPDATE purchase_executions
   SET status = 'failed',
       error  = COALESCE(
                  NULLIF(error, ''),
                  'account deleted, cannot execute (issue #606 cleanup)'
                )
 WHERE cloud_account_id IS NULL
   AND status IN ('pending', 'notified');

-- Confirm the row count for the operator.
SELECT COUNT(*) AS rows_updated
FROM purchase_executions
WHERE cloud_account_id IS NULL
  AND status = 'failed'
  AND error = 'account deleted, cannot execute (issue #606 cleanup)';

COMMIT;
