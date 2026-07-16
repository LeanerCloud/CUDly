-- Grant view:config to Standard Users and Read-Only Users (issues #1401, #1410, #1413).
--
-- The GET /api/config and GET /api/ri-exchange/config handlers both require
-- view:config. Without this grant the non-admin groups received 403, causing:
--   #1401: Global Configuration form never rendered (only the section header was visible).
--   #1410: Purchasing Policies inputs remained enabled (applyReadOnlySettings was
--          never called because loadGlobalSettings returned early on 403).
--   #1413: A "Failed to load settings: permission denied" error paragraph appeared
--          inside the Exchange Automation container on the Purchasing Policies page.
--
-- Writing config still requires update:config (AuthAdmin-gated route + explicit
-- handler check), which admins hold implicitly via admin:*. Non-admin groups are
-- NOT granted update:config here. The GET handler also withholds the SourceIdentity
-- field from non-admin sessions regardless (issue #407).
--
-- Only inserts the entry when absent; idempotent on repeated apply.

UPDATE groups
SET
    permissions = permissions || '[{"action":"view","resource":"config"}]'::jsonb,
    updated_at  = NOW()
WHERE id IN (
    '00000000-0000-5000-8000-000000000005',  -- Standard Users
    '00000000-0000-5000-8000-000000000006'   -- Read-Only Users
)
  AND NOT (permissions @> '[{"action":"view","resource":"config"}]');
