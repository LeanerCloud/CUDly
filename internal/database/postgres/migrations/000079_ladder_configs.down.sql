-- Rollback migration 000079: remove ladder_configs table and the
-- laddering_enabled kill-switch column from global_config.
-- DROP TABLE CASCADE removes the trigger automatically.

DROP TABLE IF EXISTS ladder_configs;

ALTER TABLE global_config
    DROP COLUMN IF EXISTS laddering_enabled;
