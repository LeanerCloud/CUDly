-- No rollback: truncated recommendation rows cannot be restored. The
-- scheduler will re-collect from cloud APIs on the next API read, which
-- restores the cache regardless of the migration direction. Setting
-- last_collected_at to NULL here would trigger another cold-start collect
-- on the next read, which is the correct behaviour after a rollback that
-- leaves the table empty.
UPDATE recommendations_state
   SET last_collected_at = NULL
 WHERE id = 1;
