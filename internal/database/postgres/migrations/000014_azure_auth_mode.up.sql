-- Add azure_auth_mode to cloud_accounts.
-- Supported values:
--   client_secret                — store a client secret in the credential store (default)
--   managed_identity             — use the host platform managed identity (Azure-deployed CUDly)
--   workload_identity_federation — use a private-key JWT assertion; no secret on Azure side

ALTER TABLE cloud_accounts
    ADD COLUMN azure_auth_mode VARCHAR(32)
        CHECK (azure_auth_mode IN ('client_secret', 'managed_identity', 'workload_identity_federation'));
