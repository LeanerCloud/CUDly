ALTER TABLE cloud_accounts ADD COLUMN IF NOT EXISTS aws_web_identity_token_file TEXT NOT NULL DEFAULT '';
