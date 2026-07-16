-- Migration 000085: add RI Marketplace listing columns to purchase_history.
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
-- Only these three columns are added: they are the full set written and read by
-- the implemented list/cancel flow. The settlement/poller columns (listed_at,
-- listing_price_schedule, listing_proceeds_received, listing_fee_paid) were
-- descoped from this PR because the status poller that would populate them is
-- not implemented yet; they will land with the poller (see the #292 follow-up
-- issue) so the schema never carries columns nothing writes.
--
-- All three columns are nullable and added with IF NOT EXISTS so the
-- migration is idempotent on re-apply.

ALTER TABLE purchase_history
    ADD COLUMN IF NOT EXISTS offering_class TEXT,
    ADD COLUMN IF NOT EXISTS listing_id     TEXT,
    ADD COLUMN IF NOT EXISTS listing_state  TEXT;
