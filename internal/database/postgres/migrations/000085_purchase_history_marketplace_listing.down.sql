-- Revert migration 000085
ALTER TABLE purchase_history
    DROP COLUMN IF EXISTS offering_class,
    DROP COLUMN IF EXISTS listing_id,
    DROP COLUMN IF EXISTS listing_state;
