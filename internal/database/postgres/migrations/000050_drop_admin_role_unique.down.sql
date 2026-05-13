-- Restore the single-admin index for rollback compatibility.
-- SetupAdmin's atomic INSERT-WHERE-NOT-EXISTS keeps working with
-- the index in place — the DB constraint becomes a second line of
-- defence. Rolling back will, however, surface the original 500
-- bug when an operator tries to add a second admin via the UI;
-- callers must roll back issue #349 as a unit, not just this one
-- migration.
CREATE UNIQUE INDEX IF NOT EXISTS users_one_admin
    ON users (role)
    WHERE role = 'admin';
