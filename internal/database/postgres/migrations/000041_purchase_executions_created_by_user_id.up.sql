-- 000041: track who created a purchase execution
--
-- Adds purchase_executions.created_by_user_id (nullable UUID, FK to users.id
-- with ON DELETE SET NULL) so the new session-authed Cancel button on
-- pending History rows can enforce the cancel:own_executions RBAC rule:
-- a non-admin may cancel a pending execution only when they themselves
-- created it.
--
-- Nullable because:
--   * existing rows pre-date attribution and cannot be backfilled — the
--     direct-execute Recommendations flow ran without recording the
--     creator's UUID;
--   * scheduler-driven executions (ramp schedule, cron) have no human
--     creator and should record NULL;
--   * NULL is treated as "not the current user" by the cancel handler,
--     so legacy rows fall through to the cancel:any_execution path
--     (admins) or the existing email-token path. Either way they
--     remain reachable by the email token already in the inbox; we do
--     NOT lose access to existing pending approvals.
--
-- ON DELETE SET NULL mirrors the approved_by / cancelled_by columns from
-- migration 000035 — deleting a user must not cascade-delete their audit
-- trail of executions.

ALTER TABLE purchase_executions
    ADD COLUMN created_by_user_id UUID
        REFERENCES users(id) ON DELETE SET NULL;
