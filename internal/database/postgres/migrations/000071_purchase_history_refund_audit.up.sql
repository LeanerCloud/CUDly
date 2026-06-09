-- Migration 000071: add refund-quote audit columns to purchase_history
--
-- Two-step quote-then-confirm revoke flow (issue #290 Finding #4):
-- when a user clicks Revoke, the frontend first calls
-- GET /api/purchases/revoke/calculate/{id} to fetch Azure's refund quote
-- (amount + currency), shows a confirmation modal, and only then POSTs the
-- revoke with expected_refund_amount. These columns capture the quoted values
-- for audit and TOCTOU-divergence detection.
--
-- calc_refund_amount: the amount Azure quoted at CalculateRefund time.
--   NULL means the revocation predates this feature or Azure did not return
--   an amount (e.g. zero-cost reservation).
-- calc_refund_currency: the currency code (e.g. "USD") from the quote.
--   NULL when calc_refund_amount is NULL.

ALTER TABLE purchase_history
    ADD COLUMN IF NOT EXISTS calc_refund_amount   NUMERIC(14, 4),
    ADD COLUMN IF NOT EXISTS calc_refund_currency TEXT;

-- Consistency: currency must be present whenever amount is.
DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE table_name = 'purchase_history'
          AND constraint_name = 'purchase_history_refund_currency_pair_chk'
    ) THEN
        ALTER TABLE purchase_history
            ADD CONSTRAINT purchase_history_refund_currency_pair_chk
                CHECK (
                    (calc_refund_amount IS NULL AND calc_refund_currency IS NULL) OR
                    (calc_refund_amount IS NOT NULL AND calc_refund_currency IS NOT NULL)
                );
    END IF;
END $$;
