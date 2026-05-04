-- Add last_collection_started_at to recommendations_state.
--
-- This column enables async recommendation collection: when a refresh is
-- triggered, the API Lambda sets this to NOW() and fire-and-forgets an
-- async invoke of the scheduler Lambda (same function, InvocationType=Event).
-- The scheduler clears this column when it finishes (success or failure).
--
-- If the scheduler Lambda crashes mid-run and never clears this column, the
-- POST /api/recommendations/refresh handler treats any value older than 5
-- minutes as stale and allows a new collection to start — silent recovery
-- rather than a permanent stuck state.
ALTER TABLE recommendations_state
    ADD COLUMN IF NOT EXISTS last_collection_started_at TIMESTAMPTZ;
