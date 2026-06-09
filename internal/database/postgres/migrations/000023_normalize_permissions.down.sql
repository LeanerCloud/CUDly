-- Reverse the permission vocabulary normalization.
-- Maps new actions back to the old compound frontend format.

DO $$
DECLARE
    mapping JSONB := '[
        {"new_action": "execute", "new_resource": "purchases",       "old_action": "purchase:execute"},
        {"new_action": "approve", "new_resource": "purchases",       "old_action": "purchase:approve"},
        {"new_action": "create",  "new_resource": "plans",           "old_action": "plan:create"},
        {"new_action": "update",  "new_resource": "plans",           "old_action": "plan:update"},
        {"new_action": "delete",  "new_resource": "plans",           "old_action": "plan:delete"},
        {"new_action": "view",    "new_resource": "recommendations", "old_action": "recommendation:view"},
        {"new_action": "update",  "new_resource": "config",          "old_action": "config:update"}
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
                            WHEN elem->>''action'' = $1 AND elem->>''resource'' = $2
                            THEN jsonb_set(elem, ''{action}'', to_jsonb($3::TEXT))
                            ELSE elem
                        END
                    )
                    FROM jsonb_array_elements(permissions) AS elem
                )
                WHERE permissions @> jsonb_build_array(
                    jsonb_build_object(''action'', $1, ''resource'', $2)
                )',
                tbl
            ) USING
                m->>'new_action',
                m->>'new_resource',
                m->>'old_action';
        END LOOP;
    END LOOP;
END;
$$;
