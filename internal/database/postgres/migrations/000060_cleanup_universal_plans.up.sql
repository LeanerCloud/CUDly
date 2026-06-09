-- 000057: delete purchase_plans rows that have no plan_accounts entry.
--
-- Background: before PR "fix(plans): eliminate universal plans" (closes #743),
-- the API accepted a POST /api/plans body with an empty target_accounts list
-- and wrote a purchase_plans row with no corresponding plan_accounts rows.
-- Those "universal plans" were never correctly scoped to any account;
-- migration 000057 removes them permanently.
--
-- Cleanup semantic: DELETE.
-- The three options discussed in issue #742 are (a) delete, (b) fan-out to
-- all matching accounts, and (c) manual review per plan.  A SQL migration
-- can only act uniformly across all rows; fan-out (b) would silently attach
-- every account to plans the operator may never have intended to run, and
-- manual review (c) cannot be encoded in a migration.  Deletion is the only
-- safe default: the rows predate account-scoping enforcement, have no
-- explicit account assignment, and leaving them in place could trigger
-- unintended purchases once the scheduler resumes.
--
-- Referential safety:
--   purchase_executions.plan_id  FK is ON DELETE SET NULL (migration 000033).
--   purchase_history.plan_id     FK is ON DELETE SET NULL (initial schema).
--   plan_accounts.plan_id        FK is ON DELETE CASCADE  (migration 000011).
-- Deleting from purchase_plans therefore nullifies the plan_id in any
-- linked execution/history rows and removes the (already-absent) plan_accounts
-- rows -- no orphaned child rows remain.
--
-- Idempotency: re-running after all universal plans are already gone is a
-- no-op (zero rows match the WHERE NOT EXISTS predicate).
--
-- Down migration note: the .down.sql is intentionally a no-op.  Deleted rows
-- cannot be restored by a migration because the original data is gone.
-- Operators who need to recover a specific plan must restore from a backup
-- taken before this migration ran.

DELETE FROM purchase_plans
WHERE NOT EXISTS (
    SELECT 1
    FROM plan_accounts pa
    WHERE pa.plan_id = purchase_plans.id
);
