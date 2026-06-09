-- Drop the deprecated `(aws, sagemaker)` ServiceConfig row left behind
-- by migration 000040.
--
-- Background: PR #71 introduced an interim `(aws, sagemaker)` row with
-- its own term/payment selects. Migration 000040 (PR #123) split the
-- legacy `(aws, savings-plans)` umbrella into four per-plan-type rows
-- and copied the sagemaker row's term/payment forward into the new
-- `(aws, savings-plans-sagemaker)` row, but intentionally KEPT the
-- `(aws, sagemaker)` row for one stable release as a backward-compat
-- safety net for users mid-rollout. See the `Deprecation follow-up`
-- comment at the bottom of 000040_split_savingsplans.up.sql.
--
-- This migration removes that deprecated row now that the per-plan-type
-- cards are the canonical source of SageMaker SP defaults.
--
-- Idempotent: the WHERE clause matches zero rows on installations that
-- never had PR #71's row (or that already ran this migration), so the
-- DELETE is a safe no-op.
--
-- Tracked in issue #133.

DELETE FROM service_configs
    WHERE provider = 'aws' AND service = 'sagemaker';
