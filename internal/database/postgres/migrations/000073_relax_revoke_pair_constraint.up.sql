-- Migration 000073: relax revocation pair constraint + tighten support-case check
--
-- The original purchase_history_revoked_pair_chk required that revoked_at and
-- revoked_via are always set or unset together:
--
--   (revoked_at IS NULL AND revoked_via IS NULL) OR
--   (revoked_at IS NOT NULL AND revoked_via IS NOT NULL)
--
-- This is too strict for the AWS support-case revocation path (issue #291
-- wave-2): when a support case is filed, revoked_via = 'support-case' is
-- recorded immediately so the audit trail shows that a case is pending, but
-- revoked_at remains NULL until AWS confirms the refund. The pair check fires
-- as a constraint violation in that state.
--
-- Fix: drop the pair check entirely. The meaningful invariant -- that we do
-- not end up with a dangling revoked_at without a provider -- is covered by
-- the existing purchase_history_revoked_via_chk (revoked_via IN known values).
--
-- Also tighten purchase_history_support_case_chk to enforce that every row
-- with revoked_via = 'support-case' carries a non-null support_case_id. The
-- old check only prevented support_case_id from appearing on non-support-case
-- rows; the new check is the converse and is strictly stronger.

ALTER TABLE purchase_history
    DROP CONSTRAINT IF EXISTS purchase_history_revoked_pair_chk;

-- Replace the support-case constraint with the stricter bidirectional check.
ALTER TABLE purchase_history
    DROP CONSTRAINT IF EXISTS purchase_history_support_case_chk;

ALTER TABLE purchase_history
    ADD CONSTRAINT purchase_history_support_case_chk
        CHECK (
            revoked_via != 'support-case' OR support_case_id IS NOT NULL
        );
