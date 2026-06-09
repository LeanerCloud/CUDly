-- Normalize permission vocabulary in groups and api_keys tables.
-- Maps old compound frontend actions and old backend actions to the new
-- fine-grained vocabulary introduced in the auth package refactor.
--
-- Old compound actions (stored by frontend):
--   purchase:execute  -> {action: "execute",  resource: "purchases"}
--   purchase:approve  -> {action: "approve",  resource: "purchases"}
--   plan:create       -> {action: "create",   resource: "plans"}
--   plan:update       -> {action: "update",   resource: "plans"}
--   plan:delete       -> {action: "delete",   resource: "plans"}
--   recommendation:view -> {action: "view",   resource: "recommendations"}
--   config:update     -> {action: "update",   resource: "config"}
--
-- Old backend actions:
--   purchase          -> {action: "execute",  resource: "purchases"}
--   configure         -> {action: "update",   resource: "config"}

DO $$
DECLARE
    mapping JSONB := '[
        {"old_action": "purchase:execute",    "new_action": "execute", "new_resource": "purchases"},
        {"old_action": "purchase:approve",    "new_action": "approve", "new_resource": "purchases"},
        {"old_action": "plan:create",         "new_action": "create",  "new_resource": "plans"},
        {"old_action": "plan:update",         "new_action": "update",  "new_resource": "plans"},
        {"old_action": "plan:delete",         "new_action": "delete",  "new_resource": "plans"},
        {"old_action": "recommendation:view", "new_action": "view",    "new_resource": "recommendations"},
        {"old_action": "config:update",       "new_action": "update",  "new_resource": "config"},
        {"old_action": "purchase",            "new_action": "execute", "new_resource": "purchases"},
        {"old_action": "configure",           "new_action": "update",  "new_resource": "config"}
    ]'::JSONB;
    tbl TEXT;
    m JSONB;
BEGIN
    FOREACH tbl IN ARRAY ARRAY['groups', 'api_keys'] LOOP
        FOR m IN SELECT jsonb_array_elements(mapping) LOOP
            EXECUTE format(
                'UPDATE %I SET permissions = (
                    SELECT jsonb_agg(
                        CASE
                            WHEN elem->>''action'' = $1
                            THEN jsonb_set(
                                jsonb_set(elem, ''{action}'', to_jsonb($2::TEXT)),
                                ''{resource}'', to_jsonb($3::TEXT)
                            )
                            ELSE elem
                        END
                    )
                    FROM jsonb_array_elements(permissions) AS elem
                )
                WHERE permissions @> jsonb_build_array(jsonb_build_object(''action'', $1))',
                tbl
            ) USING
                m->>'old_action',
                m->>'new_action',
                m->>'new_resource';
        END LOOP;
    END LOOP;
END;
$$;
