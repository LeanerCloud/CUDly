ALTER TABLE account_registrations
  ADD COLUMN IF NOT EXISTS reg_credential_type VARCHAR(32),
  ADD COLUMN IF NOT EXISTS reg_credential_payload TEXT;  -- AES-256-GCM encrypted blob
