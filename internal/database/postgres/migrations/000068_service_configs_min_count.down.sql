-- Reverse 000068: drop the per-service min-count filter column.
ALTER TABLE service_configs
    DROP COLUMN IF EXISTS min_count;
