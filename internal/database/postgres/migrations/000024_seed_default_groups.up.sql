-- Seed four default groups with fixed UUIDs so the seeding is idempotent
-- and the IDs are stable across deployments. Each group uses
-- allowed_accounts = ['*'] (explicit all-access marker, recognized by the
-- matcher added in the preceding commit).

INSERT INTO groups (id, name, description, permissions, allowed_accounts)
VALUES
    ('00000000-0000-5000-8000-000000000001',
     'Administrators',
     'Full access to all resources. Admin users are auto-assigned to this group.',
     '[{"action":"admin","resource":"*"}]'::jsonb,
     ARRAY['*']),
    ('00000000-0000-5000-8000-000000000002',
     'Purchase Approvers',
     'Approve purchases and view recommendations, plans, purchases, history, and accounts.',
     '[
        {"action":"view","resource":"recommendations"},
        {"action":"view","resource":"plans"},
        {"action":"view","resource":"purchases"},
        {"action":"view","resource":"history"},
        {"action":"view","resource":"accounts"},
        {"action":"approve","resource":"purchases"}
     ]'::jsonb,
     ARRAY['*']),
    ('00000000-0000-5000-8000-000000000003',
     'Plan Authors',
     'Create, update, and delete plans. Read-only everywhere else.',
     '[
        {"action":"view","resource":"recommendations"},
        {"action":"view","resource":"plans"},
        {"action":"view","resource":"purchases"},
        {"action":"view","resource":"history"},
        {"action":"view","resource":"accounts"},
        {"action":"create","resource":"plans"},
        {"action":"update","resource":"plans"},
        {"action":"delete","resource":"plans"}
     ]'::jsonb,
     ARRAY['*']),
    ('00000000-0000-5000-8000-000000000004',
     'Viewers',
     'Read-only access to recommendations, plans, purchases, history, and accounts.',
     '[
        {"action":"view","resource":"recommendations"},
        {"action":"view","resource":"plans"},
        {"action":"view","resource":"purchases"},
        {"action":"view","resource":"history"},
        {"action":"view","resource":"accounts"}
     ]'::jsonb,
     ARRAY['*'])
ON CONFLICT DO NOTHING;  -- handles both id and name UNIQUE conflicts

-- Auto-assign existing admin user(s) to the Administrators group (idempotent
-- — DISTINCT unnest avoids duplicate entries on re-runs).
-- The EXISTS guard prevents a dangling group_id reference if the INSERT
-- above was skipped due to a name conflict (e.g. an operator pre-created
-- a group named "Administrators" with a different UUID).
UPDATE users
SET group_ids = ARRAY(
    SELECT DISTINCT unnest(
        COALESCE(group_ids, '{}') || ARRAY['00000000-0000-5000-8000-000000000001']::UUID[]
    )
)
WHERE role = 'admin'
  AND EXISTS (SELECT 1 FROM groups WHERE id = '00000000-0000-5000-8000-000000000001');
