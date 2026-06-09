-- Multi-admin support (issue #349). The TOCTOU race that motivated
-- 000025_admin_role_unique is now closed in the service layer
-- (SetupAdmin uses an atomic INSERT-WHERE-NOT-EXISTS), so the
-- partial unique index is no longer needed and actively prevents
-- the supported "add a second admin via /api/users" flow.
DROP INDEX IF EXISTS users_one_admin;
