-- Add last_collection_owner_id to recommendations_state.
--
-- Issue #261: the scheduler's defer-clear of last_collection_started_at
-- (added in 000047) unconditionally wipes the marker on every run,
-- including cron runs that never called MarkCollectionStarted. If a cron
-- run overlaps a user-triggered async collection, the cron run's clear
-- silently erases the user run's in-flight marker, letting a second
-- concurrent collection start.
--
-- This column pairs with last_collection_started_at as a compare-and-clear
-- guard: MarkCollectionStarted stamps a fresh owner token alongside the
-- timestamp, and ClearCollectionStarted only clears when the caller's
-- token still matches (WHERE last_collection_owner_id = $1). A caller that
-- never owned a marker (cron, cold-start) carries no token and skips the
-- clear entirely rather than clearing unconditionally.
ALTER TABLE recommendations_state
    ADD COLUMN IF NOT EXISTS last_collection_owner_id UUID;
