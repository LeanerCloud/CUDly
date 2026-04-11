-- Add primary key to savings_snapshots (partitioned table requires partition column in PK).
ALTER TABLE savings_snapshots ADD CONSTRAINT savings_snapshots_pkey PRIMARY KEY (id, timestamp);

-- Add index on cloud_account_id for filtering analytics by account.
CREATE INDEX IF NOT EXISTS idx_savings_snapshots_cloud_account
    ON savings_snapshots(cloud_account_id)
    WHERE cloud_account_id IS NOT NULL;

-- Change aws_web_identity_token_file from NOT NULL DEFAULT '' to nullable,
-- matching the pattern of all other optional provider-specific fields.
ALTER TABLE cloud_accounts ALTER COLUMN aws_web_identity_token_file DROP NOT NULL;
ALTER TABLE cloud_accounts ALTER COLUMN aws_web_identity_token_file DROP DEFAULT;
