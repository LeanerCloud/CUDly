-- GCP federated WIF audience: the full workload identity provider
-- resource name used by CUDly as the STS audience when exchanging a
-- KMS-signed JWT for a GCP access token. Shape:
--   //iam.googleapis.com/projects/<project-number>/locations/global/workloadIdentityPools/<pool>/providers/<provider>
-- Added to both cloud_accounts (runtime read by the credential
-- resolver) and account_registrations (carries through self-service
-- registration before approval).

ALTER TABLE cloud_accounts
  ADD COLUMN IF NOT EXISTS gcp_wif_audience VARCHAR(512);

ALTER TABLE account_registrations
  ADD COLUMN IF NOT EXISTS gcp_wif_audience VARCHAR(512);
