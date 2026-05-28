-- Revert migration 000060
ALTER TABLE purchase_history
    DROP COLUMN IF EXISTS offering_class,
    DROP COLUMN IF EXISTS listing_id,
    DROP COLUMN IF EXISTS listing_state;
