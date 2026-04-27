-- Split the umbrella `(aws, savings-plans)` ServiceConfig row into four
-- per-plan-type rows so users can pin term/payment defaults independently
-- per AWS Savings Plans product family (Compute, EC2 Instance, SageMaker,
-- Database). The umbrella row is deleted atomically in the same
-- transaction.
--
-- Background: AWS Cost Explorer's GetSavingsPlansPurchaseRecommendation
-- exposes four distinct SavingsPlansType values (ComputeSp, Ec2InstanceSp,
-- SagemakerSp, DatabaseSp) but the codebase historically collapsed them
-- under a single `savings-plans` slug, forcing one shared term/payment
-- default for all four. The split lets a SageMaker-heavy account choose
-- 1-yr no-upfront for SageMaker while keeping 3-yr all-upfront for
-- Compute, etc.
--
-- PR #71 landed an interim `(aws, sagemaker)` ServiceConfig row with its
-- own term/payment selects. We treat that row asymmetrically:
--   - The umbrella `(aws, savings-plans)` row IS deleted in this
--     migration (its values seed the four new rows verbatim — no
--     information loss; only the row identity is replaced).
--   - The `(aws, sagemaker)` row is KEPT for one release behind a SQL
--     deprecation comment so a user mid-rollout doesn't lose their save.
--     Its term/payment seeds the new `savings-plans-sagemaker` row,
--     overriding the umbrella's value for that one slot. A follow-up
--     migration removes it after one stable release; see the deprecation
--     follow-up issue cited at the bottom of this file.
--
-- Strategy:
--   1. Pre-flight DO block: detect three states — empty (no SP rows),
--      already-split (has `savings-plans-compute`), and inconsistent
--      (umbrella + split rows coexist). Empty no-ops cleanly;
--      already-split skips the INSERT half and only deletes the
--      umbrella; inconsistent RAISE EXCEPTION rather than double-write.
--   2. INSERT four new rows per existing umbrella row, picking per-row
--      term/payment from `(aws, sagemaker)` for the sagemaker slot when
--      that row exists (otherwise fall back to umbrella). All other
--      columns (enabled, coverage, ramp_schedule, include/exclude_*,
--      timestamps) are copied verbatim from the umbrella.
--   3. DELETE the umbrella `(aws, savings-plans)` row. The
--      `(aws, sagemaker)` row stays.
--   4. Rewrite `purchase_plans.services` JSONB keys: any `aws:savings-plans`
--      entry fans out into four `aws:savings-plans-<type>` keys
--      carrying the same value object, and the source key is removed.
--      We use jsonb_object_agg over jsonb_each because jsonb_set can't
--      atomically delete-and-insert multiple keys in one pass.
--
-- Idempotency: ON CONFLICT DO NOTHING on the INSERTs and the
-- already-split detection make re-running safe.
--
-- Deprecation follow-up: drop `(aws, sagemaker)` after one release —
-- TODO: file follow-up issue, then cite it here.

BEGIN;

DO $$
DECLARE
    umbrella_count INT;
    split_count INT;
BEGIN
    SELECT COUNT(*) INTO umbrella_count FROM service_configs
        WHERE provider = 'aws' AND service IN ('savings-plans', 'savingsplans');
    SELECT COUNT(*) INTO split_count FROM service_configs
        WHERE provider = 'aws'
          AND service IN (
              'savings-plans-compute',
              'savings-plans-ec2instance',
              'savings-plans-sagemaker',
              'savings-plans-database'
          );

    IF umbrella_count = 0 AND split_count = 0 THEN
        RAISE NOTICE 'split_savingsplans: no SP rows present, migration is a no-op';
    ELSIF umbrella_count = 0 AND split_count > 0 THEN
        RAISE NOTICE 'split_savingsplans: per-plan-type rows already present, no INSERT needed';
    ELSIF umbrella_count > 0 AND split_count > 0 THEN
        RAISE EXCEPTION 'split_savingsplans: inconsistent state — both umbrella savings-plans row(s) (%) AND per-plan-type rows (%) exist. Manual cleanup required before this migration can run.', umbrella_count, split_count;
    END IF;
END $$;

-- Insert four per-plan-type rows. Each pulls per-row term/payment from
-- (aws, sagemaker) for the sagemaker slot (when that row exists) and
-- falls back to the umbrella's values otherwise. Other columns are
-- always copied from the umbrella.
INSERT INTO service_configs (
    provider, service, enabled, term, payment, coverage, ramp_schedule,
    include_engines, exclude_engines, include_regions, exclude_regions,
    include_types, exclude_types, created_at, updated_at
)
SELECT 'aws', svc.target_service,
    u.enabled,
    COALESCE(sm.term, u.term),
    COALESCE(sm.payment, u.payment),
    u.coverage, u.ramp_schedule,
    u.include_engines, u.exclude_engines, u.include_regions, u.exclude_regions,
    u.include_types, u.exclude_types,
    NOW(), NOW()
FROM service_configs u
CROSS JOIN (VALUES
    ('savings-plans-compute',     FALSE),
    ('savings-plans-ec2instance', FALSE),
    ('savings-plans-sagemaker',   TRUE),
    ('savings-plans-database',    FALSE)
) AS svc(target_service, is_sagemaker_slot)
LEFT JOIN service_configs sm
    ON sm.provider = 'aws' AND sm.service = 'sagemaker' AND svc.is_sagemaker_slot
WHERE u.provider = 'aws' AND u.service IN ('savings-plans', 'savingsplans')
ON CONFLICT (provider, service) DO NOTHING;

-- Delete the umbrella row. The `(aws, sagemaker)` row from PR #71 is
-- intentionally kept for one release as a backward-compat readback path.
DELETE FROM service_configs
    WHERE provider = 'aws' AND service IN ('savings-plans', 'savingsplans');

-- Rewrite purchase_plans.services JSONB keys. For each plan whose
-- services map contains `aws:savings-plans` (or its dash-free
-- spelling), fan that single entry out into four
-- `aws:savings-plans-<type>` entries carrying the same value, then drop
-- the source key. Plans without an SP entry are left untouched.
UPDATE purchase_plans
SET services = (
    SELECT jsonb_object_agg(new_key, new_val)
    FROM (
        -- Keep all non-SP entries unchanged.
        SELECT k AS new_key, v AS new_val
        FROM jsonb_each(services) AS e(k, v)
        WHERE k NOT IN ('aws:savings-plans', 'aws:savingsplans')
        UNION ALL
        -- Fan the SP entry out into four. CROSS JOIN with a literal
        -- VALUES list of the four target keys; the source value is
        -- copied into each.
        -- COALESCE prefers the canonical hyphenated key when both
        -- spellings are present so the rewrite is deterministic.
        -- jsonb_each + LIMIT 1 would pick non-deterministically.
        SELECT 'aws:' || target_service AS new_key, sp_val AS new_val
        FROM (
            SELECT COALESCE(
                services -> 'aws:savings-plans',
                services -> 'aws:savingsplans'
            ) AS sp_val
        ) src
        CROSS JOIN (VALUES
            ('savings-plans-compute'),
            ('savings-plans-ec2instance'),
            ('savings-plans-sagemaker'),
            ('savings-plans-database')
        ) AS targets(target_service)
        WHERE src.sp_val IS NOT NULL
    ) merged
)
WHERE services ?| ARRAY['aws:savings-plans', 'aws:savingsplans'];

COMMIT;
