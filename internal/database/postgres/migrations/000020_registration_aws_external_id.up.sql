ALTER TABLE account_registrations
  ADD COLUMN IF NOT EXISTS aws_external_id VARCHAR(255);
