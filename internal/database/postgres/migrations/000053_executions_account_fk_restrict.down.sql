-- 000053 rollback: revert purchase_executions.cloud_account_id FK to ON DELETE SET NULL
--
-- This restores the original migration-000011 behaviour. Note that operators
-- rolling back should be aware they are re-introducing the silent-orphan
-- behaviour described in issue #606; the cleanup script
-- scripts/mark_orphan_executions_failed.sql will need to be re-run after any
-- subsequent account deletes.

ALTER TABLE purchase_executions
    DROP CONSTRAINT IF EXISTS purchase_executions_cloud_account_id_fkey;

ALTER TABLE purchase_executions
    ADD CONSTRAINT purchase_executions_cloud_account_id_fkey
    FOREIGN KEY (cloud_account_id)
    REFERENCES cloud_accounts(id)
    ON DELETE SET NULL;
