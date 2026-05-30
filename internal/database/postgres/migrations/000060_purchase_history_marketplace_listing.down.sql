-- Revert migration 000060
ALTER TABLE purchase_history
    DROP COLUMN IF EXISTS offering_class,
    DROP COLUMN IF EXISTS listing_id,
    DROP COLUMN IF EXISTS listing_state,
    DROP COLUMN IF EXISTS listed_at,
    DROP COLUMN IF EXISTS listing_price_schedule,
    DROP COLUMN IF EXISTS listing_proceeds_received,
    DROP COLUMN IF EXISTS listing_fee_paid;
