-- Issue #907: collapse the dual authorization model (role + groups) down to
-- group-membership-only. Every authorization decision now derives from the
-- union of a user's groups' permissions; the `role` column is removed.
--
-- Ordering is load-bearing:
--   1. Seed groups that exactly mirror the legacy non-admin role permission
--      sets, so users mapped off `user` / `readonly` keep identical access.
--   2. Backfill group_ids from role for any user whose group_ids is empty.
--   3. Guarantee no user is left with zero groups (fail-safe net).
--   4. Only THEN add the >= 1-group CHECK (it would fail on a zero-group row)
--      and drop the role column LAST.

-- 1. Seed "Standard Users" and "Read-Only Users" groups mirroring
--    DefaultUserPermissions() and DefaultReadOnlyPermissions() respectively.
--    allowed_accounts = ['*'] (all-access marker, same as the 000024 seeds).
INSERT INTO groups (id, name, description, permissions, allowed_accounts)
VALUES
    ('00000000-0000-5000-8000-000000000005',
     'Standard Users',
     'Standard users: view + manage plans, manage own purchase executions. Mirrors the legacy "user" role.',
     '[
        {"action":"view","resource":"recommendations"},
        {"action":"view","resource":"plans"},
        {"action":"view","resource":"purchases"},
        {"action":"view","resource":"history"},
        {"action":"create","resource":"plans"},
        {"action":"update","resource":"plans"},
        {"action":"delete","resource":"plans"},
        {"action":"update","resource":"purchases"},
        {"action":"cancel-own","resource":"purchases"},
        {"action":"retry-own","resource":"purchases"},
        {"action":"approve-own","resource":"purchases"}
     ]'::jsonb,
     ARRAY['*']),
    ('00000000-0000-5000-8000-000000000006',
     'Read-Only Users',
     'Read-only access to recommendations, plans, and history. Mirrors the legacy "readonly" role.',
     '[
        {"action":"view","resource":"recommendations"},
        {"action":"view","resource":"plans"},
        {"action":"view","resource":"history"}
     ]'::jsonb,
     ARRAY['*'])
ON CONFLICT DO NOTHING;  -- handles both id and name UNIQUE conflicts

-- 2. Backfill group_ids from role for users whose group_ids is empty/null.
--    DISTINCT unnest dedupes; the WHERE clause only touches empty rows so
--    operator-assigned group_ids are preserved. The EXISTS guard prevents a
--    dangling reference if the corresponding group seed was skipped.
UPDATE users
SET group_ids = ARRAY(
        SELECT DISTINCT unnest(
            COALESCE(group_ids, '{}') || ARRAY['00000000-0000-5000-8000-000000000001']::UUID[]
        )
    ),
    updated_at = NOW()
WHERE role = 'admin'
  AND (group_ids IS NULL OR cardinality(group_ids) = 0)
  AND EXISTS (SELECT 1 FROM groups WHERE id = '00000000-0000-5000-8000-000000000001');

UPDATE users
SET group_ids = ARRAY(
        SELECT DISTINCT unnest(
            COALESCE(group_ids, '{}') || ARRAY['00000000-0000-5000-8000-000000000005']::UUID[]
        )
    ),
    updated_at = NOW()
WHERE role = 'user'
  AND (group_ids IS NULL OR cardinality(group_ids) = 0)
  AND EXISTS (SELECT 1 FROM groups WHERE id = '00000000-0000-5000-8000-000000000005');

UPDATE users
SET group_ids = ARRAY(
        SELECT DISTINCT unnest(
            COALESCE(group_ids, '{}') || ARRAY['00000000-0000-5000-8000-000000000006']::UUID[]
        )
    ),
    updated_at = NOW()
WHERE role = 'readonly'
  AND (group_ids IS NULL OR cardinality(group_ids) = 0)
  AND EXISTS (SELECT 1 FROM groups WHERE id = '00000000-0000-5000-8000-000000000006');

-- 3. Fail-safe: any user STILL left with zero groups (unknown role value, or a
--    seed that failed to insert) gets the least-privileged Read-Only Users
--    group so the >= 1-group CHECK below cannot fail and no account is left
--    inert. This never grants more than read-only access.
UPDATE users
SET group_ids = ARRAY['00000000-0000-5000-8000-000000000006']::UUID[],
    updated_at = NOW()
WHERE (group_ids IS NULL OR cardinality(group_ids) = 0)
  AND EXISTS (SELECT 1 FROM groups WHERE id = '00000000-0000-5000-8000-000000000006');

-- 4. Enforce the invariant at the schema level, then drop role.
--    group_ids becomes NOT NULL DEFAULT '{}' with a >= 1 element CHECK so the
--    database can never represent a zero-group user (issue #907).
ALTER TABLE users ALTER COLUMN group_ids SET DEFAULT '{}';
UPDATE users SET group_ids = '{}' WHERE group_ids IS NULL;
ALTER TABLE users ALTER COLUMN group_ids SET NOT NULL;
ALTER TABLE users ADD CONSTRAINT users_min_one_group CHECK (cardinality(group_ids) >= 1);

-- Drop role-dependent objects, then the role column itself, LAST.
DROP INDEX IF EXISTS idx_users_role;
DROP INDEX IF EXISTS idx_users_one_admin;  -- 000025 partial unique idx (already dropped by 000050 on most DBs; IF EXISTS makes this idempotent)
ALTER TABLE users DROP CONSTRAINT IF EXISTS valid_role;
ALTER TABLE sessions DROP COLUMN IF EXISTS role;
ALTER TABLE users DROP COLUMN IF EXISTS role;
