-- Down migration for 000072: remove revocation_in_flight from purchase_history
DROP INDEX IF EXISTS idx_purchase_history_revocation_in_flight;
ALTER TABLE purchase_history DROP COLUMN IF EXISTS revocation_in_flight;
