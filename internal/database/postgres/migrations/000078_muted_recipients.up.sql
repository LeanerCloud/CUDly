-- Per-recipient notification mute table (issue #297).
--
-- A row here means the named recipient has opted out of a notification
-- scope via the List-Unsubscribe one-click link. The send path consults
-- this table before adding any address to To/Cc; muted addresses are
-- silently skipped. The mute is per-scope so opting out of
-- "purchase_approvals" does NOT suppress "ri_exchange_approvals".
--
-- unmute_token is the HMAC-signed token embedded in the unsubscribe URL
-- and is stored here only for auditability / idempotency (the handler
-- derives the same token on every request and compares in constant time;
-- storing it does NOT make the endpoint stateful in the sense of
-- single-use tokens).
CREATE TABLE IF NOT EXISTS muted_recipients (
    recipient_email TEXT        NOT NULL,
    scope           TEXT        NOT NULL,
    muted_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    unmute_token    TEXT        NOT NULL,
    CONSTRAINT muted_recipients_pkey PRIMARY KEY (recipient_email, scope)
);
