-- Broaden the `recommendations` natural key to include `term` and
-- `payment_option` so the per-rec ON CONFLICT upsert in
-- internal/config/store_postgres_recommendations.go::insertRecommendationsBatch
-- can store every term × payment variant per (account, provider,
-- service, region, resource_type) SKU.
--
-- Background: Azure's reservation recommendations API returns multiple
-- variants per SKU (1yr-upfront, 3yr-upfront, 3yr-partial, etc.). The
-- old 5-tuple natural key collapsed them, which produced
--   ERROR: ON CONFLICT DO UPDATE command cannot affect row a second
--   time (SQLSTATE 21000)
-- because two rows in one batch had the same key. Commit 9fa4170a1
-- worked around this with `dedupeByNaturalKey` (silently drop the
-- non-winning variants); this migration eliminates the workaround by
-- making the key wide enough to distinguish them.
--
-- Strategy:
--   1. Add `term` and `payment_option` columns with literal NOT NULL
--      defaults — metadata-only in modern Postgres (no rewrite).
--   2. Pre-flight check: count groups that would collide on the new
--      7-tuple. RAISE NOTICE on any collisions so the operator sees
--      them in the migration log; the subsequent CREATE UNIQUE INDEX
--      enforces the constraint and FAILS if there are duplicates,
--      which is the desired safety: the operator gets the warning
--      before the failure.
--   3. Drop the old 5-column unique index and create the new 7-column
--      one with the same name. Brief table-level lock during the swap
--      is acceptable for this ~15-min-cadence write target.
--
-- The new write path produces real `term` / `payment_option` values
-- from the start. Pre-migration rows land at defaults `(0, '')` and
-- naturally age out via UpsertRecommendations' eviction-by-collected_at
-- on the next scheduler tick — no in-migration backfill needed.

ALTER TABLE recommendations
    ADD COLUMN IF NOT EXISTS term INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS payment_option TEXT NOT NULL DEFAULT '';

DO $$
DECLARE
    g INT;
BEGIN
    SELECT COUNT(*) INTO g FROM (
        SELECT 1 FROM recommendations
        GROUP BY account_key, provider, service, region, resource_type, term, payment_option
        HAVING COUNT(*) > 1
    ) s;
    IF g > 0 THEN
        RAISE NOTICE 'WARNING: % natural-key groups have duplicate rows on the new 7-tuple key — the unique index creation below will FAIL until they are deduped', g;
    END IF;
END $$;

DROP INDEX IF EXISTS recommendations_natural_key_idx;

CREATE UNIQUE INDEX recommendations_natural_key_idx
    ON recommendations (account_key, provider, service, region, resource_type, term, payment_option);
