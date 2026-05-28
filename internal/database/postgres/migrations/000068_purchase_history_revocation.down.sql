-- Rollback 000057: remove revocation columns from purchase_history
DROP INDEX IF EXISTS idx_purchase_history_revocation_window;

ALTER TABLE purchase_history
    DROP COLUMN IF EXISTS support_case_id,
    DROP COLUMN IF EXISTS revoked_via,
    DROP COLUMN IF EXISTS revoked_at,
    DROP COLUMN IF EXISTS revocation_window_closes_at;
