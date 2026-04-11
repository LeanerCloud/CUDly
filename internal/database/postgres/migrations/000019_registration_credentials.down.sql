ALTER TABLE account_registrations
  DROP COLUMN IF EXISTS reg_credential_type,
  DROP COLUMN IF EXISTS reg_credential_payload;
