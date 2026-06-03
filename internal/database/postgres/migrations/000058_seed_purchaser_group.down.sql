-- Reverse migration: remove Purchaser group memberships from users,
-- then delete the Purchaser group, then drop the system_managed column.

-- Remove the Purchaser group from all user group_ids arrays.
UPDATE users
SET group_ids = array_remove(group_ids, '00000000-0000-5000-8000-000000000005'::UUID)
WHERE '00000000-0000-5000-8000-000000000005'::UUID = ANY(group_ids);

-- Delete the Purchaser group.
DELETE FROM groups
WHERE id = '00000000-0000-5000-8000-000000000005';

-- Drop the system_managed column (rolls back the ALTER TABLE above).
ALTER TABLE groups DROP COLUMN IF EXISTS system_managed;
