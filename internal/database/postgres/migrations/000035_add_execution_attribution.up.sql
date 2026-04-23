-- Track *who* approved or cancelled a purchase execution. Populated by
-- session-authed action endpoints (see ADR in known_issues/); left NULL
-- on the legacy token-only approve/cancel paths, where attribution falls
-- back to the notification email at read time in fetchExecutionsAsHistory.
--
-- Nullable because:
--   * existing rows predate attribution and cannot be backfilled;
--   * the token-only paths above legitimately have no session to attribute;
--   * the non-null case is an explicit signal to the History UI that the
--     row was acted on by a logged-in user (vs inferred from the approver
--     email).

ALTER TABLE purchase_executions
    ADD COLUMN approved_by  TEXT,
    ADD COLUMN cancelled_by TEXT;
