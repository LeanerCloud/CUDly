-- Reverse of 000032: restore the original 5-column natural key.
--
-- Order matters: DROP INDEX (the new 7-tuple) → DROP COLUMN (otherwise
-- column-drop fails because the index references them) → CREATE INDEX
-- (the original 5-tuple shape). Reuses the same index name as the up
-- migration, matching the pre-000032 schema exactly.
--
-- Note: rows written under the broadened key with non-default `term`
-- or `payment_option` may collapse onto duplicates of the old narrower
-- key after the down. If that's a concern, snapshot the table first.

DROP INDEX IF EXISTS recommendations_natural_key_idx;

ALTER TABLE recommendations
    DROP COLUMN IF EXISTS payment_option,
    DROP COLUMN IF EXISTS term;

CREATE UNIQUE INDEX recommendations_natural_key_idx
    ON recommendations (account_key, provider, service, region, resource_type);
