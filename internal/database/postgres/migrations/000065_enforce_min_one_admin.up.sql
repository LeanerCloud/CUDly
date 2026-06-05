-- Issue #919: close the TOCTOU window in the last-admin protection.
--
-- The application layer (service_user.go) calls CountGroupMembers and then
-- writes in a separate statement. Two concurrent privileged requests each
-- removing/deactivating a different one of the last two admin-group members
-- can both observe count == 2, both pass the soft guard, and leave zero
-- active admins.
--
-- This migration adds deferred constraint triggers that fire at COMMIT time
-- for writes that could reduce the number of *active* Administrators-group
-- members. If the group would have zero active members when the transaction
-- commits, the trigger raises an exception and rolls back the whole
-- transaction. Combined with the existing application-level soft check (which
-- rejects early with a user-friendly 409), the DB triggers are the hard,
-- race-free backstop.
--
-- Concurrency safety (CR #921, critical): a deferred trigger alone is NOT
-- race-free. Under READ COMMITTED/REPEATABLE READ, each transaction's
-- COUNT(*) runs against its own MVCC snapshot and cannot see a concurrent
-- transaction's uncommitted delete, so two transactions can each observe
-- count >= 1 and both commit, leaving zero admins. To serialize the check we
-- take a transaction-scoped advisory lock on a fixed key at the start of the
-- function. Concurrent admin-affecting commits then run the COUNT one at a
-- time: the second waiter blocks until the first commits (releasing the lock),
-- then re-counts and sees the post-commit state. The lock auto-releases at
-- COMMIT or ROLLBACK, so it cannot leak.
--
-- Active semantics (CR #921, minor): the count mirrors AdminExists
-- (AND active = true), so deactivating the last admin (a plain UPDATE with
-- group_ids unchanged) is also blocked -- not just deletes and group removals.
--
-- Trigger scope (CR #921, major): UPDATE and DELETE are handled by separate
-- triggers so the COUNT(*) only runs when an admin is actually losing admin
-- standing. The DELETE trigger fires when the deleted row was an admin. The
-- UPDATE trigger fires only when the row was an active admin and the new row
-- either drops the Administrators group or is deactivated -- so the
-- high-frequency login path (updating last_login_at / failed_login_attempts on
-- an admin that stays an active admin) never pays the COUNT(*) cost.
--
-- Constraint triggers must be FOR EACH ROW in PostgreSQL. DEFERRABLE
-- INITIALLY DEFERRED means the function runs at commit time, after all writes
-- in the transaction are visible, giving the correct post-commit view.

CREATE OR REPLACE FUNCTION check_min_one_admin()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    admin_count INTEGER;
BEGIN
    -- Serialize the count across concurrent admin-affecting transactions.
    -- Without this, two deferred checks run against independent MVCC
    -- snapshots and both can pass while jointly removing the last admins.
    -- The key is an arbitrary fixed constant scoped to this invariant.
    PERFORM pg_advisory_xact_lock(8059058058580001);

    SELECT COUNT(*) INTO admin_count
    FROM users
    WHERE group_ids @> ARRAY['00000000-0000-5000-8000-000000000001']::UUID[]
      AND active = true;

    IF admin_count < 1 THEN
        RAISE EXCEPTION 'last_admin_constraint_violation: at least one active member of the Administrators group must remain';
    END IF;

    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_min_one_admin ON users;
DROP TRIGGER IF EXISTS trg_min_one_admin_delete ON users;
DROP TRIGGER IF EXISTS trg_min_one_admin_update ON users;

-- DELETE: fire when the removed row was an Administrators-group member.
CREATE CONSTRAINT TRIGGER trg_min_one_admin_delete
    AFTER DELETE ON users
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    WHEN (OLD.group_ids @> ARRAY['00000000-0000-5000-8000-000000000001']::UUID[])
    EXECUTE FUNCTION check_min_one_admin();

-- UPDATE: fire only when an active admin loses admin standing, i.e. the old
-- row was an active admin and the new row either drops the Administrators
-- group or is no longer active. Updates that keep an admin active and in the
-- group (e.g. last_login_at / failed_login_attempts) do not fire.
CREATE CONSTRAINT TRIGGER trg_min_one_admin_update
    AFTER UPDATE ON users
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    WHEN (
        OLD.group_ids @> ARRAY['00000000-0000-5000-8000-000000000001']::UUID[]
        AND OLD.active = true
        AND (
            NOT (NEW.group_ids @> ARRAY['00000000-0000-5000-8000-000000000001']::UUID[])
            OR NEW.active = false
        )
    )
    EXECUTE FUNCTION check_min_one_admin();
