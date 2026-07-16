-- Remove approve-own:purchases from the Standard Users group (issue #1407).
--
-- Four-eyes principle: the same user who submits a purchase must not be
-- able to approve it by default. The approve-own verb was seeded in
-- migration 000057 alongside cancel-own and retry-own, but unlike those
-- verbs it creates a self-approval path that violates four-eyes.
--
-- After this migration:
--   * Standard Users (Plan Authors, Viewer-equivalent custom groups that
--     inherited the Standard Users permission set) cannot approve any
--     purchase, including ones they created themselves.
--   * The Purchaser group (approve-any:purchases, migration 000059) is
--     unchanged: Purchaser members can still approve any pending purchase.
--   * approve-own can be added to a CUSTOM group for organisations that
--     deliberately permit self-approval as an explicit policy choice.
--
-- The permissions column is a JSONB array; this statement uses
-- jsonb_agg + jsonb_array_elements to filter out the target element
-- without touching any other permission entries in any other group.

UPDATE groups
SET
    permissions = (
        SELECT COALESCE(
            jsonb_agg(elem ORDER BY elem->>'action', elem->>'resource'),
            '[]'::jsonb
        )
        FROM jsonb_array_elements(permissions) AS elem
        WHERE NOT (
            elem->>'action' = 'approve-own'
            AND elem->>'resource' = 'purchases'
        )
    ),
    updated_at = NOW()
WHERE id = '00000000-0000-5000-8000-000000000005';  -- Standard Users
