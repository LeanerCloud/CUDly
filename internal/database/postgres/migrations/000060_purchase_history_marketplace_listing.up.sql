-- Migration 000060: add RI Marketplace listing columns to purchase_history.
--
-- offering_class: 'convertible' or 'standard'; NULL for pre-migration rows.
-- The Sell button renders only when offering_class = 'standard' and the RI is
-- active with remaining term.
--
-- listing_id: the AWS ReservedInstancesListingId returned by
-- CreateReservedInstancesListing. NULL when not listed.
-- listing_state: mirrors the AWS listing state (active/cancelled/closed). NULL
-- when not listed.
--
-- All three columns are nullable and added with IF NOT EXISTS so the
-- migration is idempotent on re-apply.

ALTER TABLE purchase_history
    ADD COLUMN IF NOT EXISTS offering_class           TEXT,
    ADD COLUMN IF NOT EXISTS listing_id               TEXT,
    ADD COLUMN IF NOT EXISTS listing_state            TEXT,
    ADD COLUMN IF NOT EXISTS listed_at                TIMESTAMP WITH TIME ZONE,
    ADD COLUMN IF NOT EXISTS listing_price_schedule   JSONB,
    ADD COLUMN IF NOT EXISTS listing_proceeds_received NUMERIC,
    ADD COLUMN IF NOT EXISTS listing_fee_paid         NUMERIC;
