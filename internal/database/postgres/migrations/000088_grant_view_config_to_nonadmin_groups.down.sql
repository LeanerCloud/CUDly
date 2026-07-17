-- Reverse: remove view:config from Standard Users and Read-Only Users.
--
-- After this rollback non-admin users will again receive 403 on
-- GET /api/config and GET /api/ri-exchange/config, restoring the
-- broken behaviour that issues #1401, #1410, and #1413 describe.
-- Apply only when explicitly rolling back 000088.

UPDATE groups
SET
    permissions = (
        SELECT COALESCE(
            jsonb_agg(elem ORDER BY elem->>'action', elem->>'resource'),
            '[]'::jsonb
        )
        FROM jsonb_array_elements(permissions) AS elem
        WHERE NOT (
            elem->>'action' = 'view'
            AND elem->>'resource' = 'config'
        )
    ),
    updated_at = NOW()
WHERE id IN (
    '00000000-0000-5000-8000-000000000005',  -- Standard Users
    '00000000-0000-5000-8000-000000000006'   -- Read-Only Users
);
