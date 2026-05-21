-- Revert migration 000053
ALTER TABLE ri_exchange_history
    DROP COLUMN IF EXISTS approved_by,
    DROP COLUMN IF EXISTS created_by_user_id;
