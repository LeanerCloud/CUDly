-- Migration 000054: add approved_by and created_by_user_id to ri_exchange_history
-- Mirrors the same columns on purchase_executions added for issues #286 and #46.
-- Both columns are nullable so existing rows are unaffected.
ALTER TABLE ri_exchange_history
    ADD COLUMN IF NOT EXISTS approved_by       TEXT,
    ADD COLUMN IF NOT EXISTS created_by_user_id TEXT;
