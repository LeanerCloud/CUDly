-- Restore approve-own:purchases to the Standard Users group (down migration
-- for 000086_remove_approve_own_from_standard_users).
--
-- NOTE: this down migration re-enables the self-approval path for all
-- Standard Users members, which violates the four-eyes principle patched
-- by issue #1407. Apply only when explicitly rolling back.

UPDATE groups
SET
    permissions = permissions || '[{"action":"approve-own","resource":"purchases"}]'::jsonb,
    updated_at = NOW()
WHERE id = '00000000-0000-5000-8000-000000000005'  -- Standard Users
  AND NOT (permissions @> '[{"action":"approve-own","resource":"purchases"}]');
