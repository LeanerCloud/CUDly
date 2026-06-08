-- Revert migration 000073: restore the original revocation pair constraint
-- and revert purchase_history_support_case_chk to its original definition.

-- Restore the stricter support-case check to its original (weaker) form.
ALTER TABLE purchase_history
    DROP CONSTRAINT IF EXISTS purchase_history_support_case_chk;

ALTER TABLE purchase_history
    ADD CONSTRAINT purchase_history_support_case_chk
        CHECK (support_case_id IS NULL OR revoked_via = 'support-case');

-- Restore the pair constraint. Note: if any rows currently have
-- revoked_via IS NOT NULL and revoked_at IS NULL (support-case in-flight),
-- this re-add will fail. Those rows must be resolved first.
ALTER TABLE purchase_history
    ADD CONSTRAINT purchase_history_revoked_pair_chk
        CHECK (
            (revoked_at IS NULL AND revoked_via IS NULL) OR
            (revoked_at IS NOT NULL AND revoked_via IS NOT NULL)
        );
