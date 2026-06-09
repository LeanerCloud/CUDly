-- Historical drift cleanup: when a cloud_account linked to an approved
-- registration was deleted, the FK's ON DELETE SET NULL left the registration
-- in status='approved' with cloud_account_id=NULL and no recovery path in the
-- UI. Flip those rows back to pending so the admin can re-approve through the
-- normal flow. Going forward, PostgresStore.DeleteCloudAccount resets the
-- linked registration atomically, so this state should not recur.
UPDATE account_registrations
   SET status      = 'pending',
       reviewed_by = NULL,
       reviewed_at = NULL
 WHERE status           = 'approved'
   AND cloud_account_id IS NULL;
