-- Drop the primary key, returning savings_snapshots to its keyless state.
-- The de-duplication performed by the up migration is NOT reversed (we
-- can't reconstruct rows we deleted). Down rollback is intended for
-- schema-shape rollback, not data restoration.
ALTER TABLE savings_snapshots DROP CONSTRAINT IF EXISTS savings_snapshots_pkey;
