ALTER TABLE cloud_accounts
  DROP COLUMN IF EXISTS gcp_wif_audience;

ALTER TABLE account_registrations
  DROP COLUMN IF EXISTS gcp_wif_audience;
