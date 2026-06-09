-- Reverse the group seeding. Order: clear the group_id reference from
-- users first, then delete the groups. (group_ids is a plain UUID[] with
-- no FK enforcement, so the order is for hygiene, not correctness.)

UPDATE users
SET group_ids = array_remove(group_ids, '00000000-0000-5000-8000-000000000001'::UUID)
WHERE '00000000-0000-5000-8000-000000000001'::UUID = ANY(group_ids);

DELETE FROM groups
WHERE id IN (
    '00000000-0000-5000-8000-000000000001',
    '00000000-0000-5000-8000-000000000002',
    '00000000-0000-5000-8000-000000000003',
    '00000000-0000-5000-8000-000000000004'
);
