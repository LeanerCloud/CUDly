-- Reverse 000042: drop engine from the natural key and remove the
-- column. NOTE: this is destructive of any rows where two records
-- differ only by engine — the down-migration cannot recover the
-- (engine, payment_option, term, account_key, …) row that gets
-- collapsed away by the narrower index.

DROP INDEX IF EXISTS recommendations_natural_key_idx;

CREATE UNIQUE INDEX recommendations_natural_key_idx
    ON recommendations (account_key, provider, service, region, resource_type, term, payment_option);

ALTER TABLE recommendations
    DROP COLUMN IF EXISTS engine;
