-- Audit columns for direct-execute purchases (issue #289).
--
-- When a session with execute-any:purchases or execute-own:purchases
-- triggers an immediate purchase (bypassing the approval email), the
-- handler stamps these three columns so finance auditors can later ask
-- "who direct-executed this purchase, when, and why was approval skipped?"
--
-- All three are nullable TEXT / TIMESTAMPTZ so:
--   * Existing rows (normal approval flow) are untouched (NULL = approval
--     flow, non-NULL = direct-execute shortcut).
--   * Legacy rows from before this migration read as NULL and are treated
--     as normal-flow rows by the application.
--
-- executed_by_user_id: UUID of the session user who fired the direct-execute.
--   FK to users.id ON DELETE SET NULL mirrors the approved_by / cancelled_by
--   pattern from migration 000035.
-- executed_at:          UTC timestamp the direct-execute fired.
-- pre_approval_skip_reason: short literal, always "direct-execute permission"
--   today; a TEXT column leaves room for future skip reasons without a schema
--   change.

ALTER TABLE purchase_executions
    ADD COLUMN IF NOT EXISTS executed_by_user_id  UUID        REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS executed_at          TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS pre_approval_skip_reason TEXT;

-- Partial index speeds up audit queries that filter for direct-execute rows.
CREATE INDEX IF NOT EXISTS idx_executions_direct_execute
    ON purchase_executions (executed_by_user_id)
    WHERE executed_by_user_id IS NOT NULL;
