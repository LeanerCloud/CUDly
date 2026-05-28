-- Migration 000057: add revocation columns to purchase_history
--
-- AWS EC2 RIs: no direct cancel API; the 24h free-cancel window requires an
-- AWS Support case (support:CreateCase). AWS support-case revocation is
-- tracked but not executed via the direct API path (button hidden for AWS).
--
-- Azure reservations: return via armreservations.ReturnClient within a 7-day
-- window. Window and result captured in these columns.
--
-- GCP commitments: no free-cancel window; button hidden.
--
-- revocation_window_closes_at: computed at purchase-time from provider policy
--   + the row's timestamp. NULL means "not revocable" (GCP or unsupported).
-- revoked_at: timestamp when the in-app revocation was confirmed via the
--   provider API (or the support-case was filed, when that path is taken).
-- revoked_via: audit enum — "direct-api" (provider cancel API returned 2xx)
--   or "support-case" (AWS Support CreateCase filed).
-- support_case_id: non-null only for revoked_via='support-case' rows.

ALTER TABLE purchase_history
    ADD COLUMN IF NOT EXISTS revocation_window_closes_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS revoked_at                  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS revoked_via                 VARCHAR(32),
    ADD COLUMN IF NOT EXISTS support_case_id             TEXT;

-- Partial index speeds the History endpoint's per-row window check:
-- only rows where the window has not yet closed need the computation.
CREATE INDEX IF NOT EXISTS idx_purchase_history_revocation_window
    ON purchase_history (revocation_window_closes_at)
    WHERE revocation_window_closes_at IS NOT NULL AND revoked_at IS NULL;
