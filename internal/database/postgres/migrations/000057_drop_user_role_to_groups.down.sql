-- Reverse of 000057: restore the `role` column and its constraints, and remove
-- the group-membership invariant. Role values are reconstructed best-effort
-- from group membership so the rolled-back system keeps a coherent role per
-- user. The two seeded groups (Standard Users / Read-Only Users) are left in
-- place: dropping them could orphan group_ids that operators have since
-- assigned, and they are harmless if unused (mirrors the no-op rollback
-- rationale of migration 000056).

-- Drop the >= 1-group invariant first so the column can be relaxed.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_min_one_group;
ALTER TABLE users ALTER COLUMN group_ids DROP NOT NULL;

-- Re-add the sessions.role column (nullable; populated on next login).
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS role VARCHAR(32);

-- Re-add users.role. Add as nullable first so existing rows don't violate
-- NOT NULL before backfill, then backfill, then enforce NOT NULL + CHECK.
ALTER TABLE users ADD COLUMN IF NOT EXISTS role VARCHAR(32);

-- Reconstruct role from group membership:
--   * Administrators group  -> admin
--   * Read-Only Users group (and not admin) -> readonly
--   * everything else -> user
UPDATE users
SET role = CASE
    WHEN group_ids @> ARRAY['00000000-0000-5000-8000-000000000001']::UUID[] THEN 'admin'
    WHEN group_ids @> ARRAY['00000000-0000-5000-8000-000000000006']::UUID[] THEN 'readonly'
    ELSE 'user'
END
WHERE role IS NULL;

ALTER TABLE users ALTER COLUMN role SET DEFAULT 'user';
ALTER TABLE users ALTER COLUMN role SET NOT NULL;
ALTER TABLE users ADD CONSTRAINT valid_role CHECK (role IN ('admin', 'user', 'readonly'));
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
