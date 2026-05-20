-- 000051: add approval_token_expires_at to purchase_executions (issue #397)
--
-- Approval tokens previously had no expiry, allowing a token leaked via email
-- forwarding, a phished mailbox, or an access-log export to remain valid
-- indefinitely. This migration adds a nullable TIMESTAMPTZ column that
-- creation paths now populate; the application layer rejects tokens whose
-- deadline has passed (ApproveExecution / loadCancelableExecution in
-- internal/purchase/approvals.go).
--
-- Column is nullable so existing rows (created before this migration) are
-- unaffected: a NULL ApprovalTokenExpiresAt is treated as "no deadline" in
-- the application layer for backward compatibility. All new executions
-- created after this migration carry a non-null value (7-day TTL from
-- creation time, per config.ApprovalTokenTTL).

ALTER TABLE purchase_executions
    ADD COLUMN approval_token_expires_at TIMESTAMPTZ;

-- Partial index: only rows that actually have a deadline benefit from
-- range scans. On healthy deployments with routine approval windows, nearly
-- all active rows will be non-null post-migration, so the partial index
-- keeps the index compact during legacy rows' gradual retirement.
CREATE INDEX idx_executions_token_expires_at
    ON purchase_executions(approval_token_expires_at)
    WHERE approval_token_expires_at IS NOT NULL;
