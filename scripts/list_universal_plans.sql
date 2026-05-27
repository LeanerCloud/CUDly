-- list_universal_plans.sql
--
-- Read-only diagnostic for the "universal plans" cleanup (PR #739 follow-up):
-- enumerates every row in purchase_plans that has no matching row in
-- plan_accounts. After PR "fix(plans): eliminate universal plans" lands the
-- API can no longer create such rows, but pre-existing rows from the legacy
-- "leave Target Account blank" behaviour remain until an operator decides
-- what to do with each one.
--
-- Run this against the live DB (read-only — no DELETE/UPDATE) to see the
-- scope:
--
--   psql "$DB_URL" -f scripts/list_universal_plans.sql
--
-- Three operator-driven cleanup options for each row returned, decided per
-- plan (see the follow-up ops issue linked from the PR body):
--
--   (a) Delete the plan if it is genuinely orphaned (no consumer, no
--       executions, never used). Safest when the row predates any
--       intentional use:
--
--         DELETE FROM purchase_plans WHERE id = '<uuid>';
--
--   (b) Fan-out: attach every enabled cloud_account whose provider matches
--       the plan's services map (mimics the historical "blank == all
--       provider accounts" intent). Concrete; explicit; recoverable:
--
--         INSERT INTO plan_accounts (plan_id, account_id)
--         SELECT pp.id, ca.id
--         FROM purchase_plans pp
--         CROSS JOIN LATERAL jsonb_each(pp.services) AS svc(k, v)
--         JOIN cloud_accounts ca ON ca.provider = v->>'provider'
--         WHERE pp.id = '<uuid>'
--         ON CONFLICT DO NOTHING;
--
--   (c) Manual review: pull the plan up in the UI, talk to the owner if
--       known, and assign the right account(s) via the New/Edit Plan modal
--       (which now requires target_accounts). Recommended for plans that
--       had clear intent the SQL can't infer.

SELECT pp.id,
       pp.name,
       pp.created_at,
       pp.updated_at,
       pp.enabled,
       pp.services
FROM purchase_plans pp
LEFT JOIN plan_accounts pa ON pa.plan_id = pp.id
WHERE pa.plan_id IS NULL
ORDER BY pp.created_at ASC;
