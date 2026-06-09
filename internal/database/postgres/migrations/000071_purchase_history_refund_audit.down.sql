-- Migration 000071 (down): remove refund-quote audit columns from purchase_history
ALTER TABLE purchase_history
    DROP CONSTRAINT IF EXISTS purchase_history_refund_currency_pair_chk,
    DROP COLUMN IF EXISTS calc_refund_amount,
    DROP COLUMN IF EXISTS calc_refund_currency;
