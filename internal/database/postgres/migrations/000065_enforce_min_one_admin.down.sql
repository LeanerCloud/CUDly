-- Reverse of 000065: remove the deferred constraint triggers and their
-- supporting function that enforce at least one active Administrators-group
-- member. The legacy single-trigger name is dropped too so re-runs across the
-- up/down boundary stay clean.
DROP TRIGGER IF EXISTS trg_min_one_admin ON users;
DROP TRIGGER IF EXISTS trg_min_one_admin_delete ON users;
DROP TRIGGER IF EXISTS trg_min_one_admin_update ON users;
DROP FUNCTION IF EXISTS check_min_one_admin();
