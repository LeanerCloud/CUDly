-- Rollback for the per-plan-type Savings Plans split.
--
-- Lossy by design: the four per-plan-type rows may carry divergent
-- term/payment values, but the legacy umbrella was a single row with
-- one (term, payment) pair. We restore the umbrella from
-- `savings-plans-compute` (the canonical source — most accounts'
-- commitment is dominated by Compute SP, so its values are the safest
-- single representative).
--
-- Idempotent: if no `savings-plans-compute` row exists for a given
-- provider (fresh install never had SP config), the down migration
-- no-ops gracefully. The PR #71 `(aws, sagemaker)` row, if present, is
-- left untouched because its lifecycle is governed by the up
-- migration's deprecation comment, not this rollback.

BEGIN;

-- Restore the umbrella row from savings-plans-compute, if present.
INSERT INTO service_configs (
    provider, service, enabled, term, payment, coverage, ramp_schedule,
    include_engines, exclude_engines, include_regions, exclude_regions,
    include_types, exclude_types, created_at, updated_at
)
SELECT provider, 'savings-plans',
    enabled, term, payment, coverage, ramp_schedule,
    include_engines, exclude_engines, include_regions, exclude_regions,
    include_types, exclude_types,
    NOW(), NOW()
FROM service_configs
WHERE provider = 'aws' AND service = 'savings-plans-compute'
ON CONFLICT (provider, service) DO NOTHING;

-- Delete the four per-plan-type rows.
DELETE FROM service_configs
    WHERE provider = 'aws'
    AND service IN (
        'savings-plans-compute',
        'savings-plans-ec2instance',
        'savings-plans-sagemaker',
        'savings-plans-database'
    );

-- Rewrite purchase_plans.services back to the umbrella key. Pick the
-- savings-plans-compute value as the deterministic representative for
-- the same lossy-but-predictable reason as the service_configs row
-- above.
UPDATE purchase_plans
SET services = (
    SELECT jsonb_object_agg(new_key, new_val)
    FROM (
        -- Keep all non-SP entries unchanged.
        SELECT k AS new_key, v AS new_val
        FROM jsonb_each(services) AS e(k, v)
        WHERE k NOT IN (
            'aws:savings-plans-compute',
            'aws:savings-plans-ec2instance',
            'aws:savings-plans-sagemaker',
            'aws:savings-plans-database'
        )
        UNION ALL
        -- Collapse the four per-plan-type entries back into a single
        -- `aws:savings-plans` entry. Prefer the compute slot (most
        -- common, mirrors the service_configs down rule) but fall back
        -- to whichever per-plan-type key is present so a plan that was
        -- created post-split with only sagemaker/database/ec2instance
        -- still gets a usable umbrella key on rollback.
        SELECT 'aws:savings-plans' AS new_key, v AS new_val
        FROM jsonb_each(services) AS e(k, v)
        WHERE k IN (
            'aws:savings-plans-compute',
            'aws:savings-plans-ec2instance',
            'aws:savings-plans-sagemaker',
            'aws:savings-plans-database'
        )
        ORDER BY CASE k
            WHEN 'aws:savings-plans-compute'     THEN 1
            WHEN 'aws:savings-plans-ec2instance' THEN 2
            WHEN 'aws:savings-plans-sagemaker'   THEN 3
            ELSE 4
        END
        LIMIT 1
    ) merged
)
WHERE services ?| ARRAY[
    'aws:savings-plans-compute',
    'aws:savings-plans-ec2instance',
    'aws:savings-plans-sagemaker',
    'aws:savings-plans-database'
];

COMMIT;
