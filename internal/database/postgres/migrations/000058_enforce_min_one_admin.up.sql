-- Issue #919: close the TOCTOU window in the last-admin protection.
--
-- The application layer (service_user.go) calls CountGroupMembers and then
-- writes in a separate statement. Two concurrent privileged requests each
-- removing a different one of the last two admin-group members can both
-- observe count == 2, both pass the soft guard, and leave zero admins.
--
-- This migration adds a deferred constraint trigger that fires at COMMIT
-- time for writes that could reduce the Administrators-group membership.
-- If the group has zero members when the transaction commits, the trigger
-- raises an exception and rolls back the whole transaction. Combined with
-- the existing application-level soft check (which rejects early with a
-- user-friendly 409), the DB trigger is the hard, race-free backstop.
--
-- The WHEN clause limits execution to rows that previously held the
-- Administrators-group UUID, so unrelated user writes (e.g. updating
-- last_login_at or failed_login_attempts) never pay the COUNT(*) cost.
--
-- Constraint triggers must be FOR EACH ROW in PostgreSQL. DEFERRABLE
-- INITIALLY DEFERRED means the function runs at commit time, after all
-- writes in the transaction are visible, giving the correct post-commit
-- view of the admin count.

CREATE OR REPLACE FUNCTION check_min_one_admin()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    admin_count INTEGER;
BEGIN
    SELECT COUNT(*) INTO admin_count
    FROM users
    WHERE group_ids @> ARRAY['00000000-0000-5000-8000-000000000001']::UUID[];

    IF admin_count < 1 THEN
        RAISE EXCEPTION 'last_admin_constraint_violation: at least one member of the Administrators group must remain';
    END IF;

    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_min_one_admin ON users;
CREATE CONSTRAINT TRIGGER trg_min_one_admin
    AFTER UPDATE OR DELETE ON users
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    WHEN (OLD.group_ids @> ARRAY['00000000-0000-5000-8000-000000000001']::UUID[])
    EXECUTE FUNCTION check_min_one_admin();
