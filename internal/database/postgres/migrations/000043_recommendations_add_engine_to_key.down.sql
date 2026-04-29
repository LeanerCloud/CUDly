-- Reverse 000042: drop engine from the natural key and remove the
-- column. NOTE: this is destructive — when two records differ only
-- by engine (e.g., MySQL vs Postgres at the same RDS SKU), the
-- narrower 7-tuple index cannot accommodate both, so we DELETE the
-- losers BEFORE recreating the index. Without this dedupe step the
-- CREATE UNIQUE INDEX below would fail with a duplicate-key error
-- on any database where 000042 actually distinguished engine-
-- variant rows.
--
-- Strategy:
--   1. Pre-flight RAISE NOTICE on engine-divergent groups so the
--      operator sees the destructive count in the migration log
--      before rows disappear.
--   2. DELETE all but one row per 7-tuple (account_key, provider,
--      service, region, resource_type, term, payment_option). The
--      ROW_NUMBER() over `id` ordering picks an arbitrary winner —
--      this is a rollback, the loser data is being thrown away
--      either way; deterministic ordering keeps the migration
--      idempotent (re-running picks the same winner).
--   3. DROP the 8-column unique index.
--   4. DROP the engine column.
--   5. Recreate the 7-column unique index.

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
        RAISE NOTICE 'WARNING: % natural-key groups will lose engine-variant rows during rollback (one row per 7-tuple is kept)', g;
    END IF;
END $$;

DELETE FROM recommendations
WHERE id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (
            PARTITION BY account_key, provider, service, region, resource_type, term, payment_option
            ORDER BY id
        ) AS rn
        FROM recommendations
    ) ranked
    WHERE rn > 1
);

DROP INDEX IF EXISTS recommendations_natural_key_idx;

ALTER TABLE recommendations
    DROP COLUMN IF EXISTS engine;

CREATE UNIQUE INDEX recommendations_natural_key_idx
    ON recommendations (account_key, provider, service, region, resource_type, term, payment_option);
