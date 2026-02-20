-- Revert term default back to 12 (the original incorrect default).
-- Note: This does NOT revert data changes from UPDATE since we can't know
-- which rows originally had term=12 vs term=3.
ALTER TABLE service_configs ALTER COLUMN term SET DEFAULT 12;
