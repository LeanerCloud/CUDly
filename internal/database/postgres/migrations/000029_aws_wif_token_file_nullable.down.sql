-- Restore the NOT NULL DEFAULT '' shape from migration 000016.
-- Convert any NULLs back to empty strings before re-adding the constraint
-- so the ALTER doesn't fail on existing rows that the up migration NULL'd.
UPDATE cloud_accounts
   SET aws_web_identity_token_file = ''
 WHERE aws_web_identity_token_file IS NULL;

ALTER TABLE cloud_accounts
    ALTER COLUMN aws_web_identity_token_file SET DEFAULT '',
    ALTER COLUMN aws_web_identity_token_file SET NOT NULL;
