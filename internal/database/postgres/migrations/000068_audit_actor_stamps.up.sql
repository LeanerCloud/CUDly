-- Migration 000066: audit actor stamps on state transitions
-- Adds transitioned_by + transitioned_at to the three financial state machines:
-- purchase_executions, ri_exchanges (ri_exchange_history), account_registrations.
-- Idempotent: uses ADD COLUMN IF NOT EXISTS throughout.
-- Existing rows receive NULL for both columns (retroactive attribution is impossible).

ALTER TABLE purchase_executions
    ADD COLUMN IF NOT EXISTS transitioned_by  UUID          NULL REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS transitioned_at  TIMESTAMPTZ   NULL;

ALTER TABLE ri_exchange_history
    ADD COLUMN IF NOT EXISTS transitioned_by  UUID          NULL REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS transitioned_at  TIMESTAMPTZ   NULL;

ALTER TABLE account_registrations
    ADD COLUMN IF NOT EXISTS transitioned_by  UUID          NULL REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS transitioned_at  TIMESTAMPTZ   NULL;
