-- Migration 000083: ladder_execution_enabled global kill-switch.
--
-- Stacks on laddering_enabled (migration 000079): BOTH must be true for
-- the ladder capability write side (PurchaseLayer / ReshapeBuffer) to be
-- wired with real AWS clients. Default FALSE means existing deployments
-- that enable laddering produce plans but never call AWS purchase APIs until
-- an operator explicitly opts in (fail-loud, no silent fallback).
--
-- Idempotent: ADD COLUMN IF NOT EXISTS.

ALTER TABLE global_config
    ADD COLUMN IF NOT EXISTS ladder_execution_enabled BOOLEAN NOT NULL DEFAULT FALSE;
