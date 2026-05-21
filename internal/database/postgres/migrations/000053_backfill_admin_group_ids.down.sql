-- No-op rollback. The up migration is an additive, idempotent backfill that
-- only appends the Administrators group to admin rows whose group_ids were
-- empty. There is no safe reverse: once applied, a backfilled row is
-- indistinguishable from a row an operator deliberately assigned to the
-- Administrators group, so removing the group on rollback could revoke
-- legitimately-assigned permissions. Migration 000024's UPDATE is reversed by
-- its own down migration; this follow-up backfill leaves the data in place.
SELECT 1;
