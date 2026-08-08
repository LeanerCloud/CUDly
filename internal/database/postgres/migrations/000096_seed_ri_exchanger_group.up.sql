-- Seed the RI Exchanger system-managed group (issue #1644).
--
-- execute:ri-exchange is added to adminCarvedOuts in the same change, so
-- admin:* alone no longer grants it. Without a group that DOES grant it the
-- verb would be unreachable for every principal: PR #1737's grant ceiling
-- refuses to add a carved-out verb to any group through the API, and no
-- migration seeded it. Both routed execute endpoints
-- (POST /api/ri-exchange/execute, POST /api/ri-exchange/azure-instances/exchange)
-- would then 403 for everyone. This migration is what keeps the verb
-- grantable, mirroring what 000059/000064 do for execute:purchases (#923).
--
-- 000008 is the next free id in the seeded namespace:
--   000005 = Standard Users, 000006 = Read-Only Users, 000007 = Purchaser.
-- Keep DefaultRIExchangerGroupID (internal/auth/types.go) and
-- RI_EXCHANGER_GROUP_ID (frontend/src/permissions.ts) in step with it.

DO $$
DECLARE
    v_uuid UUID := '00000000-0000-5000-8000-000000000008';
    v_admin_uuid UUID := '00000000-0000-5000-8000-000000000001';
    v_occupant TEXT;
BEGIN
    -- Guard: fail hard rather than silently no-op if the target UUID is
    -- already held by a different group. Migration 000059 shipped a bare
    -- ON CONFLICT (id) DO NOTHING onto an occupied id and the seed was
    -- skipped on every database, which is the bug 000064 had to repair
    -- (#942). Fail loudly instead.
    SELECT name INTO v_occupant
    FROM groups
    WHERE id = v_uuid AND name <> 'RI Exchanger';

    IF FOUND THEN
        RAISE EXCEPTION
            'migration 000096: UUID % is already claimed by group ''%''; '
            'choose a different UUID for RI Exchanger before applying this migration',
            v_uuid, v_occupant;
    END IF;

    -- Same guard on the name: a pre-existing "RI Exchanger" under a
    -- different id would leave two rows competing for the same meaning.
    IF EXISTS (SELECT 1 FROM groups WHERE name = 'RI Exchanger' AND id <> v_uuid) THEN
        RAISE EXCEPTION
            'migration 000096: a group named ''RI Exchanger'' already exists with a different id; '
            'rename it before applying this migration so the seeded id (%) can be created',
            v_uuid;
    END IF;

    -- The grant is deliberately UNCONSTRAINED (no providers/regions/
    -- MaxPurchaseAmount). A migration cannot know an operator's accounts,
    -- regions or spend ceiling, and an over-narrow seed would refuse
    -- legitimate exchanges on upgrade. The consequence is stated plainly in
    -- the PR and issue #1644: permissionsAllow short-circuits on an
    -- unconstrained grant, so members bypass the constraint dimensions
    -- exactly as admin:* does today. Operators who want those dimensions
    -- enforced must grant execute:ri-exchange through a custom group with
    -- constraints instead of relying on this seed.
    INSERT INTO groups (id, name, description, permissions, allowed_accounts, system_managed)
    VALUES (
        v_uuid,
        'RI Exchanger',
        'Execute RI exchanges. Membership is required even for admins, because an exchange consumes existing commitments and cannot be rolled back (separation of duties, issues #923 and #1644).',
        '[
           {"action":"execute","resource":"ri-exchange"},
           {"action":"view","resource":"recommendations"},
           {"action":"view","resource":"purchases"},
           {"action":"view","resource":"history"}
        ]'::jsonb,
        ARRAY['*'],
        TRUE
    )
    ON CONFLICT (id) DO NOTHING;

    -- Admin-backfill: every member of Administrators also joins RI Exchanger.
    --
    -- Without this, every existing admin loses RI-exchange execute the moment
    -- this deploys, discovered in production. That mirrors 000059/000064 for
    -- Purchaser, and keeps the two carve-outs in the same set behaving the
    -- same way. It does mean the carve-out changes little operationally for
    -- the existing admin population; what it does buy is that membership is
    -- now revocable and auditable independently of the admin role, that new
    -- principals need a deliberate grant rather than inheriting the verb from
    -- admin:*, and that the API grant path is closed.
    --
    -- Idempotent: the NOT (...) guard skips existing members and
    -- DISTINCT(unnest) deduplicates. The EXISTS guard leaves no orphaned
    -- array entries if the INSERT above was skipped.
    UPDATE users
    SET group_ids = ARRAY(
        SELECT DISTINCT unnest(
            COALESCE(group_ids, '{}') || ARRAY[v_uuid]
        )
    )
    WHERE v_admin_uuid = ANY(COALESCE(group_ids, '{}'))
      AND NOT (v_uuid = ANY(COALESCE(group_ids, '{}')))
      AND EXISTS (SELECT 1 FROM groups WHERE id = v_uuid);
END $$;
