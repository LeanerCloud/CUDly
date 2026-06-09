-- Restore the original `gen_random_uuid()` default to match the state
-- migration 000009 left the table in.
ALTER TABLE ri_exchange_history ALTER COLUMN id SET DEFAULT gen_random_uuid();
