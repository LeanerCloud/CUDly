-- 000042: link a failed purchase execution to its retry execution
--
-- Adds two columns to purchase_executions so the History UI's inline
-- Retry button (issue #47) can show provenance and soft-block obvious
-- retry loops:
--
--   * retry_execution_id  — UUID of the new execution that retried this
--     row. Set on the *original failed* row at the moment a retry is
--     accepted; NULL on every other row. Forms a singly-linked chain
--     where each failed→retry edge points forward in time
--     (failed_v1.retry_execution_id = failed_v2.execution_id, etc.).
--     Self-FK with ON DELETE SET NULL: cleaning up an execution must
--     not cascade-delete its predecessor's audit trail.
--
--   * retry_attempt_n     — small int; 0 (default) for a fresh
--     execution, 1 for the first retry of any failed row, n+1 for the
--     n+1-th retry. The handler reads the predecessor's attempt count
--     and stamps n+1 atomically with the new INSERT inside the retry
--     transaction. The History UI uses this to soft-block retries past
--     a threshold (5 by default — see retryThreshold in handler_purchases.go)
--     so an obviously-stuck FROM_EMAIL misconfiguration doesn't accumulate
--     dozens of dead retry rows.
--
-- Both columns nullable / default-zero so existing rows don't need
-- backfilling: legacy failed rows that pre-date this migration look
-- exactly like a fresh first-retry candidate (retry_execution_id NULL,
-- retry_attempt_n = 0), which is correct behaviour.
--
-- Index choice — partial on retry_execution_id IS NOT NULL — keeps the
-- index tiny on healthy deployments (most rows never retry) while
-- still serving the History query "find the descendant of execution X"
-- in O(log n) on the rare populated case.

ALTER TABLE purchase_executions
    ADD COLUMN retry_execution_id UUID
        REFERENCES purchase_executions(execution_id) ON DELETE SET NULL;

ALTER TABLE purchase_executions
    ADD COLUMN retry_attempt_n INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_executions_retry_target
    ON purchase_executions(retry_execution_id)
    WHERE retry_execution_id IS NOT NULL;
