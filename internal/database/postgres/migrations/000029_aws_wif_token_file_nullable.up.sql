-- Make cloud_accounts.aws_web_identity_token_file nullable to match the
-- shape of every other provider-specific optional field.
--
-- Migration 000016 added the column as `TEXT NOT NULL DEFAULT ''`. All
-- sibling AWS-specific optional fields (aws_role_arn, aws_external_id,
-- etc.) are nullable, so application code cannot distinguish "WIF not
-- configured" from "explicitly set to empty" — both come back as `""`.
-- The mismatch also forces every read query to wrap the column in
-- COALESCE(...,'') even though the column is already non-NULL.
--
-- This migration:
--   1. Drops the NOT NULL constraint and the empty-string DEFAULT.
--   2. Converts existing empty strings to NULL so the column's nullability
--      now carries the actual configured/unconfigured semantics.
--
-- Application Go code can either (a) keep using `string` with the read
-- COALESCE — current behaviour, no semantic change — or (b) switch to
-- `*string` / `sql.NullString` to honour the new tri-state. The migration
-- supports both; the choice happens in a follow-up.
ALTER TABLE cloud_accounts
    ALTER COLUMN aws_web_identity_token_file DROP NOT NULL,
    ALTER COLUMN aws_web_identity_token_file DROP DEFAULT;

UPDATE cloud_accounts
   SET aws_web_identity_token_file = NULL
 WHERE aws_web_identity_token_file = '';
