-- Reverse 000096: detach every user from RI Exchanger, then drop the group.
--
-- Order matters: group_ids is a plain UUID array with no FK, so a dropped
-- group would otherwise leave dangling ids that collectGroupsAndAccounts
-- silently skips, making the rollback look clean while leaving debris.

DO $$
DECLARE
    v_uuid UUID := '00000000-0000-5000-8000-000000000008';
BEGIN
    UPDATE users
    SET group_ids = array_remove(COALESCE(group_ids, '{}'), v_uuid)
    WHERE v_uuid = ANY(COALESCE(group_ids, '{}'));

    -- Only remove the seeded row. A group an operator renamed onto this id
    -- is not ours to drop.
    DELETE FROM groups WHERE id = v_uuid AND name = 'RI Exchanger';
END $$;
