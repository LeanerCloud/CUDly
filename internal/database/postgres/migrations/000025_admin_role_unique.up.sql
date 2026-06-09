-- Enforce at most one admin user at the database level. Prevents the TOCTOU
-- race in SetupAdmin (which check-then-inserts with no lock) — two concurrent
-- requests on a fresh install could both pass the exists() check and both
-- succeed, leaving the deployment with two admins. This partial unique index
-- makes the second insert fail with a duplicate-key error that the service
-- layer maps to 409 "admin already exists".
--
-- Safe for existing deployments: the index covers only rows where role =
-- 'admin'. If a deployment already has multiple admins (shouldn't but
-- possible), this migration will fail during creation and the operator must
-- demote duplicates first. We prefer this over silently succeeding because
-- the multi-admin state is itself a security concern.
CREATE UNIQUE INDEX IF NOT EXISTS users_one_admin
    ON users (role)
    WHERE role = 'admin';
