-- Reverse 000096: detach every user from RI Exchanger, then drop the group.
--
-- Order matters: group_ids is a plain UUID array with no FK, so a dropped
-- group would otherwise leave dangling ids that collectGroupsAndAccounts
-- silently skips, making the rollback look clean while leaving debris.

DO $$
DECLARE
    v_uuid UUID := '00000000-0000-5000-8000-000000000008';
BEGIN
    -- Only touch membership for the row we would also be willing to drop.
    -- Before this guard, the UPDATE ran unconditionally while the DELETE
    -- below was already guarded on name = 'RI Exchanger' ("a group an
    -- operator renamed onto this id is not ours to drop") -- so if that
    -- guard ever fired, the rollback had already detached every member from
    -- a group it then refused to delete: a half-rollback, worse than doing
    -- all of it or none of it (PR #1758 review). Sharing one guard for both
    -- statements keeps them atomic: either both run, or neither does.
    IF EXISTS (SELECT 1 FROM groups WHERE id = v_uuid AND name = 'RI Exchanger') THEN
        UPDATE users
        SET group_ids = array_remove(COALESCE(group_ids, '{}'), v_uuid)
        WHERE v_uuid = ANY(COALESCE(group_ids, '{}'));

        DELETE FROM groups WHERE id = v_uuid AND name = 'RI Exchanger';
    END IF;
END $$;
