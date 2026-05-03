-- Rollback: recreate the deprecated `(aws, sagemaker)` ServiceConfig
-- row from `(aws, savings-plans-sagemaker)` for emergency rollback.
--
-- Lossy by design: the original PR #71 row's term/payment were copied
-- forward into `(aws, savings-plans-sagemaker)` by migration 000040,
-- but any divergence between that row and a hypothetical edited
-- `(aws, sagemaker)` row at the time of 000045 is not recoverable —
-- only the post-040 term/payment survive in the split row.
--
-- Idempotent in two directions:
--   * If `(aws, savings-plans-sagemaker)` does not exist (e.g. someone
--     rolled back past migration 000040 before invoking this down),
--     the SELECT returns zero rows and the INSERT is a no-op.
--   * If `(aws, sagemaker)` already exists (manual restore, or this
--     down was already run), `ON CONFLICT (provider, service) DO
--     NOTHING` keeps the existing row untouched.

INSERT INTO service_configs (
    provider, service, enabled, term, payment, coverage, ramp_schedule,
    include_engines, exclude_engines, include_regions, exclude_regions,
    include_types, exclude_types, created_at, updated_at
)
SELECT 'aws', 'sagemaker',
    enabled, term, payment, coverage, ramp_schedule,
    include_engines, exclude_engines, include_regions, exclude_regions,
    include_types, exclude_types,
    NOW(), NOW()
FROM service_configs
WHERE provider = 'aws' AND service = 'savings-plans-sagemaker'
ON CONFLICT (provider, service) DO NOTHING;
