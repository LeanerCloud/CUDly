-- Add `source` column to purchase_executions and purchase_history so we can
-- distinguish between CLI-initiated and web-initiated purchases for the
-- purchase-automation tag that CUDly stamps onto each bought commitment.
-- Historical rows default to '' (unknown) — we don't know where they came from.

ALTER TABLE purchase_executions
    ADD COLUMN source VARCHAR(32) NOT NULL DEFAULT '';

ALTER TABLE purchase_history
    ADD COLUMN source VARCHAR(32) NOT NULL DEFAULT '';
