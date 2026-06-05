-- Migration 000068: add revocation columns to purchase_history
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

-- Audit consistency constraints on the revocation columns.
-- revoked_via must be a known value when present.
DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'purchase_history'
          AND constraint_name = 'purchase_history_revoked_via_chk'
    ) THEN
        ALTER TABLE purchase_history
            ADD CONSTRAINT purchase_history_revoked_via_chk
                CHECK (revoked_via IS NULL OR revoked_via IN ('direct-api', 'support-case'));
    END IF;
    -- support_case_id must only be populated when revoked_via = 'support-case'.
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'purchase_history'
          AND constraint_name = 'purchase_history_support_case_chk'
    ) THEN
        ALTER TABLE purchase_history
            ADD CONSTRAINT purchase_history_support_case_chk
                CHECK (support_case_id IS NULL OR revoked_via = 'support-case');
    END IF;
    -- revoked_at and revoked_via must be set or unset together (no partial revocation state).
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'purchase_history'
          AND constraint_name = 'purchase_history_revoked_pair_chk'
    ) THEN
        ALTER TABLE purchase_history
            ADD CONSTRAINT purchase_history_revoked_pair_chk
                CHECK (
                    (revoked_at IS NULL AND revoked_via IS NULL) OR
                    (revoked_at IS NOT NULL AND revoked_via IS NOT NULL)
                );
    END IF;
END $$;

-- Gmail-style pre-fire delay (issue #291 wave-2): approve defers the cloud
-- SDK call by a configurable window so the user can revoke at $0 cost.
-- scheduled_execution_at: when the scheduler will fire the SDK call.
-- purchase_delay_hours: configurable delay in hours (0=immediate, default 48).
ALTER TABLE purchase_executions
    ADD COLUMN IF NOT EXISTS scheduled_execution_at TIMESTAMPTZ NULL;

ALTER TABLE global_config
    ADD COLUMN IF NOT EXISTS purchase_delay_hours INT NOT NULL DEFAULT 0;

-- Extend the status CHECK constraint to include 'scheduled'.
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'purchase_executions'
          AND constraint_name = 'purchase_executions_status_check'
    ) THEN
        ALTER TABLE purchase_executions DROP CONSTRAINT purchase_executions_status_check;
    END IF;
    ALTER TABLE purchase_executions ADD CONSTRAINT purchase_executions_status_check
        CHECK (status IN (
            'pending','notified','approved','running','completed',
            'partially_completed','failed','cancelled','expired','paused',
            'revocation_requested','scheduled'
        ));
END $$;
