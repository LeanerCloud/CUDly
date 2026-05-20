-- Idempotent SQL-level backfill of the Administrators group onto any admin
-- user whose group_ids drifted to empty (issue #351, follow-up #546).
--
-- Migration 000024 already runs this same backfill, but only once at its own
-- version. A database restored from a backup, or upgraded without the
-- ADMIN_EMAIL env var set, never re-runs the Go-level backfill in
-- assignAdminGroupAndWarn (it only fires when RunMigrations is called with a
-- non-empty admin email). This migration closes that path: any admin row with
-- empty group_ids gets the Administrators group on the next `migrate up`,
-- regardless of how the deployment invokes migrations.
--
-- Idempotent: DISTINCT(unnest(...)) dedupes so a re-run never duplicates the
-- entry, and the WHERE clause only touches rows that are actually empty, so
-- operator-customised group_ids are left untouched. The EXISTS guard makes the
-- statement a no-op if the Administrators group row is somehow absent.
UPDATE users
SET group_ids = ARRAY(
    SELECT DISTINCT unnest(
        COALESCE(group_ids, '{}') || ARRAY['00000000-0000-5000-8000-000000000001']::UUID[]
    )
),
    updated_at = NOW()
WHERE role = 'admin'
  AND (group_ids IS NULL OR cardinality(group_ids) = 0)
  AND EXISTS (SELECT 1 FROM groups WHERE id = '00000000-0000-5000-8000-000000000001');
