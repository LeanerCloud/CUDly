-- Reverse: remove primary key from savings_snapshots.
ALTER TABLE savings_snapshots DROP CONSTRAINT IF EXISTS savings_snapshots_pkey;

-- Reverse: remove cloud_account_id index.
DROP INDEX IF EXISTS idx_savings_snapshots_cloud_account;

-- Reverse: restore NOT NULL DEFAULT '' on aws_web_identity_token_file.
ALTER TABLE cloud_accounts ALTER COLUMN aws_web_identity_token_file SET DEFAULT '';
ALTER TABLE cloud_accounts ALTER COLUMN aws_web_identity_token_file SET NOT NULL;
