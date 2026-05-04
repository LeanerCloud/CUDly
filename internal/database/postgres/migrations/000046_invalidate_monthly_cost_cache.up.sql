-- Rewrite stale pre-PR-#254 recommendation payloads that store
-- monthly_cost as a literal JSON 0 (from the old float64 struct field).
-- After PR #254, monthly_cost is *float64 so a real zero means "no
-- recurring charge" (e.g. all-upfront), while null means "provider API
-- did not return a monthly breakdown".
--
-- Pre-deploy rows have monthly_cost=0 from the old float64 zero — they
-- are stale and would render as "$0" in the UI even after the code fix.
-- Setting them to null here causes the frontend to render "—" instead,
-- which is at least accurate ("data not yet collected") until the next
-- scheduler tick re-collects with correct values.
--
-- NOTE: We do NOT truncate the table or reset last_collected_at because
-- Azure recommendation collection alone exceeds the API Lambda's 60s
-- timeout — a forced cold-start collect would cause a 502 on the next
-- request. The daily scheduled collector will write correct values on
-- its next run. See GitHub issue #256 companion issue for the Lambda
-- timeout root cause.
UPDATE recommendations
   SET payload = jsonb_set(payload, '{monthly_cost}', 'null'::jsonb)
 WHERE (payload->>'monthly_cost')::text = '0';
