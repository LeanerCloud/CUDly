-- Migration 000072: add revocation_in_flight flag to purchase_history
--
-- Partial-success reconciliation (issue #290 Finding #6):
-- When callAzureReturn succeeds (Azure has already committed the refund) but
-- the subsequent MarkPurchaseRevoked DB write fails, the row is left in an
-- ambiguous state. The revocation_in_flight flag is set to TRUE immediately
-- before the Azure Return API call so that:
--   1. The idempotency check in the revoke endpoint detects the in-progress
--      state and avoids re-calling Azure (preventing a duplicate-refund error).
--   2. The finalize_revocations scheduled sweep can identify rows that need
--      the MarkPurchaseRevoked write to be retried.
--
-- The flag is reset to FALSE (and revoked_at/revoked_via are stamped) by a
-- successful MarkPurchaseRevoked. If retries all fail the flag stays TRUE and
-- the sweep picks the row up on the next tick.

ALTER TABLE purchase_history
    ADD COLUMN IF NOT EXISTS revocation_in_flight BOOLEAN NOT NULL DEFAULT false;

-- Partial index: only in-flight rows need the finalize sweep; the index stays
-- tiny because the normal path flips the flag back to false within seconds.
CREATE INDEX IF NOT EXISTS idx_purchase_history_revocation_in_flight
    ON purchase_history (purchase_id)
    WHERE revocation_in_flight = true;
