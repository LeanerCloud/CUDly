-- Seed the Purchaser system-managed group with a fixed UUID so the
-- seeding is idempotent and the ID is stable across deployments.
-- The three money-spending verbs (execute, approve-any, retry-any on
-- purchases) are carved out of the admin:* wildcard by the backend
-- HasPermission change in this same PR (issue #923). A user must be
-- a member of this group (or a custom group granting these verbs) to
-- spend money, even if they are an administrator.
--
-- The groups table does not yet have a system_managed column, so we
-- add it here and backfill existing seed groups as system-managed.

ALTER TABLE groups ADD COLUMN IF NOT EXISTS system_managed BOOLEAN NOT NULL DEFAULT FALSE;

-- Mark the four existing seed groups as system-managed.
UPDATE groups
SET system_managed = TRUE
WHERE id IN (
    '00000000-0000-5000-8000-000000000001',
    '00000000-0000-5000-8000-000000000002',
    '00000000-0000-5000-8000-000000000003',
    '00000000-0000-5000-8000-000000000004'
);

-- Fail closed if a pre-existing group already owns the name "Purchaser"
-- under a different UUID. Without this guard the bare ON CONFLICT DO
-- NOTHING below would silently skip the seed and leave
-- DefaultPurchaserGroupID absent, breaking the admin-backfill UPDATE
-- further down and every callsite that keys off the fixed UUID.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM groups
        WHERE name = 'Purchaser'
          AND id <> '00000000-0000-5000-8000-000000000005'
    ) THEN
        RAISE EXCEPTION
            'migration 000059: a group named ''Purchaser'' already exists with a different id; rename it before applying this migration so the seeded id (00000000-0000-5000-8000-000000000005) can be created';
    END IF;
END $$;

-- Insert the Purchaser group. Idempotent on the seeded id only -- a
-- name-collision on a different id is caught by the DO block above so
-- the seed never silently goes missing.
INSERT INTO groups (id, name, description, permissions, allowed_accounts, system_managed)
VALUES (
    '00000000-0000-5000-8000-000000000005',
    'Purchaser',
    'Execute, approve, and retry purchases. Membership is required even for admins to spend money (separation of duties, issue #923).',
    '[
       {"action":"execute","resource":"purchases"},
       {"action":"approve-any","resource":"purchases"},
       {"action":"retry-any","resource":"purchases"},
       {"action":"view","resource":"recommendations"},
       {"action":"view","resource":"plans"},
       {"action":"view","resource":"purchases"},
       {"action":"view","resource":"history"}
    ]'::jsonb,
    ARRAY['*'],
    TRUE
)
ON CONFLICT (id) DO NOTHING;

-- Auto-assign every existing admin-group member to the Purchaser group
-- so upgrade preserves current behavior. Admins can later remove
-- themselves to enforce strict separation of duties.
-- We drive off group membership (Administrators group UUID) rather than
-- the legacy role column (which has been dropped in migration 000057).
UPDATE users
SET group_ids = ARRAY(
    SELECT DISTINCT unnest(
        COALESCE(group_ids, '{}') || ARRAY['00000000-0000-5000-8000-000000000005']::UUID[]
    )
)
WHERE '00000000-0000-5000-8000-000000000001'::UUID = ANY(COALESCE(group_ids, '{}'))
  AND EXISTS (SELECT 1 FROM groups WHERE id = '00000000-0000-5000-8000-000000000005');
