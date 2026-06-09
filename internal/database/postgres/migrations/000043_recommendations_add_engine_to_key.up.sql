-- Add `engine` to the `recommendations` table and broaden the natural
-- key to include it. Previously two RDS recommendations differing
-- only by engine (e.g., MySQL vs Postgres at the same db.m5.large
-- SKU) collapsed onto the same row because the 7-tuple key didn't
-- distinguish them — see issues #187 / #188 for the symptom-level
-- impact (UI checkbox collisions and missing recs respectively).
--
-- The Go-side ID encoding (internal/scheduler/scheduler.go) and the
-- per-row INSERT in store_postgres_recommendations.go already
-- include engine; this migration aligns the SQL UNIQUE constraint
-- so ON CONFLICT (..., engine, ...) actually matches.
--
-- Strategy mirrors 000032:
--   1. Add `engine TEXT NOT NULL DEFAULT ''` (metadata-only — no
--      table rewrite in modern Postgres).
--   2. Pre-flight RAISE NOTICE on any rows that would collide on the
--      new 8-tuple key. The subsequent CREATE UNIQUE INDEX enforces
--      the constraint and FAILS on duplicates, so the warning lands
--      before the failure.
--   3. Drop and re-create the unique index with engine included.
--      Brief table-level lock during the swap is acceptable for
--      this ~15-min-cadence write target.
--
-- Pre-migration rows land at default `engine = ''` and naturally
-- age out via UpsertRecommendations' eviction-by-collected_at on
-- the next scheduler tick — no in-migration backfill needed.

ALTER TABLE recommendations
    ADD COLUMN IF NOT EXISTS engine TEXT NOT NULL DEFAULT '';

DO $$
DECLARE
    g INT;
BEGIN
    SELECT COUNT(*) INTO g FROM (
        SELECT 1 FROM recommendations
        GROUP BY account_key, provider, service, region, resource_type, engine, term, payment_option
        HAVING COUNT(*) > 1
    ) s;
    IF g > 0 THEN
        RAISE NOTICE 'WARNING: % natural-key groups have duplicate rows on the new 8-tuple key — the unique index creation below will FAIL until they are deduped', g;
    END IF;
END $$;

DROP INDEX IF EXISTS recommendations_natural_key_idx;

CREATE UNIQUE INDEX recommendations_natural_key_idx
    ON recommendations (account_key, provider, service, region, resource_type, engine, term, payment_option);
