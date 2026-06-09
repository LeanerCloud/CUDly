-- Add gcp_auth_mode to cloud_accounts.
-- Supported values:
--   service_account_key          — store a service account JSON key in the credential store (default)
--   application_default          — use Application Default Credentials (works on GCP or where ADC is configured)
--   workload_identity_federation — use a GCP external-account credential config (no long-lived key)

ALTER TABLE cloud_accounts
    ADD COLUMN gcp_auth_mode VARCHAR(32)
        CHECK (gcp_auth_mode IN ('service_account_key', 'application_default', 'workload_identity_federation'));
