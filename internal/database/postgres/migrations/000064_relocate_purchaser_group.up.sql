-- Relocate the Purchaser system-managed group to a free UUID.
--
-- Root cause (issue #942): migration 000057_drop_user_role_to_groups.up.sql
-- claimed UUID 00000000-0000-5000-8000-000000000005 for "Standard Users".
-- Migration 000059_seed_purchaser_group.up.sql tried to seed "Purchaser"
-- at the same UUID and ended with ON CONFLICT (id) DO NOTHING, so the
-- Purchaser INSERT was silently no-op'd on every database that ran both
-- migrations. The admin-backfill in 000059 then attached admins to
-- "Standard Users" (the row at ...000005), not to Purchaser, leaving full
-- admins without execute:purchases / approve-any:purchases / retry-any:purchases
-- despite holding admin:*.
--
-- Fix: assign Purchaser to the next free UUID in the seeded namespace:
--   00000000-0000-5000-8000-000000000007
-- (000005 = Standard Users, 000006 = Read-Only Users, 000007 = Purchaser)
-- Update DefaultPurchaserGroupID in internal/auth/types.go and
-- PURCHASER_GROUP_ID in frontend/src/permissions.ts to match.

DO $$
DECLARE
    v_new_uuid UUID := '00000000-0000-5000-8000-000000000007';
    v_old_uuid UUID;
    v_occupant TEXT;
BEGIN
    -- Guard: fail hard if the target UUID is already taken by a
    -- non-Purchaser row.  This prevents the same class of silent UUID
    -- collision that caused the original bug.
    SELECT name INTO v_occupant
    FROM groups
    WHERE id = v_new_uuid AND name <> 'Purchaser';

    IF FOUND THEN
        RAISE EXCEPTION
            'migration 000064: UUID % is already claimed by group ''%''; '
            'choose a different UUID for Purchaser before applying this migration',
            v_new_uuid, v_occupant;
    END IF;

    -- Case 1: "Purchaser" exists but at a non-target UUID (e.g. a partial
    -- repair attempt).  Capture the current id, swap user references, then
    -- relocate the group row to the canonical UUID.
    SELECT id INTO v_old_uuid
    FROM groups
    WHERE name = 'Purchaser' AND id <> v_new_uuid;

    IF FOUND THEN
        -- Swap the stale UUID for the canonical one in every user's
        -- group_ids array before updating the groups PK to avoid FK issues.
        UPDATE users
        SET group_ids = ARRAY(
            SELECT DISTINCT unnest(
                array_remove(COALESCE(group_ids, '{}'), v_old_uuid) ||
                ARRAY[v_new_uuid]
            )
        )
        WHERE v_old_uuid = ANY(COALESCE(group_ids, '{}'));

        UPDATE groups SET id = v_new_uuid WHERE id = v_old_uuid;

    -- Case 2: No "Purchaser" row exists at all (the common bug-case on
    -- any DB that ran 000057 before 000059).  Insert a fresh one.
    ELSIF NOT EXISTS (SELECT 1 FROM groups WHERE name = 'Purchaser') THEN
        INSERT INTO groups (id, name, description, permissions, allowed_accounts, system_managed)
        VALUES (
            v_new_uuid,
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
        );

    END IF;

    -- Case 3: "Purchaser" already sits at v_new_uuid.
    -- Nothing to relocate; fall through to the backfill below.

    -- Admin-backfill: ensure every member of Administrators
    -- (00000000-0000-5000-8000-000000000001) is also in Purchaser.
    -- Idempotent: the NOT (v_new_uuid = ANY(...)) guard skips rows that are
    -- already members; DISTINCT(unnest) deduplicates any remaining overlap.
    -- The EXISTS guard skips the UPDATE entirely if Purchaser still does not
    -- exist for some reason, leaving no orphaned array entries.
    UPDATE users
    SET group_ids = ARRAY(
        SELECT DISTINCT unnest(
            COALESCE(group_ids, '{}') || ARRAY[v_new_uuid]
        )
    )
    WHERE '00000000-0000-5000-8000-000000000001'::UUID = ANY(COALESCE(group_ids, '{}'))
      AND NOT (v_new_uuid = ANY(COALESCE(group_ids, '{}')))
      AND EXISTS (SELECT 1 FROM groups WHERE id = v_new_uuid);

END $$;
