-- Per-service min-count recommendation filter (GUI exposure of the CLI
-- --min-count flag).
--
-- min_count is the minimum instance/node count a recommendation must carry
-- to be surfaced. The scheduler read path
-- (scheduler.filterRecsByResolvedConfigs) drops recs whose persisted
-- RecommendationRecord.Count is below this threshold. 0 (the default and the
-- NULL backfill) means "no floor", matching the CLI flag's 0-disables
-- semantics. NOT NULL with a 0 default so existing rows and new inserts share
-- the disabled-by-default behaviour without a separate backfill pass.
ALTER TABLE service_configs
    ADD COLUMN IF NOT EXISTS min_count INTEGER NOT NULL DEFAULT 0;
