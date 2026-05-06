-- Rewrite stale pre-PR-#312 AWS recommendation payloads that lack
-- on_demand_cost in their JSONB payload.
--
-- PR #312 added on_demand_cost population for AWS Savings Plans rows
-- (via CurrentAverageHourlyOnDemandSpend × 730). PR #277 added it for
-- Azure rows. Neither shipped a cache-invalidation migration for the
-- existing AWS rows already in the DB.
--
-- Pre-#312 AWS rows have no on_demand_cost key, so the frontend falls
-- back to reconstructing the on-demand denominator from
-- monthly_cost + savings + amortized — a formula that double-counts
-- amortization vs. how AWS computes its savings amounts, producing
-- implausibly high Effective % values.
--
-- Setting monthly_cost to null here causes the frontend to render "—"
-- instead, which is accurate ("data not yet collected") until the next
-- scheduler tick re-collects with correct on_demand_cost values.
--
-- NOTE: We scope this strictly to AWS rows that lack on_demand_cost.
-- Azure rows were handled by migration 000046. GCP rows are not affected.
--
-- NOTE: This UPDATE is idempotent — once a row has on_demand_cost
-- (written by the next scheduler tick), the NOT (payload ? 'on_demand_cost')
-- condition no longer matches it, so re-running this migration is safe.
--
-- See GitHub issue #321 for the full root-cause investigation.
UPDATE recommendations
   SET payload = jsonb_set(payload, '{monthly_cost}', 'null'::jsonb)
 WHERE payload->>'provider' = 'aws'
   AND NOT (payload ? 'on_demand_cost');
