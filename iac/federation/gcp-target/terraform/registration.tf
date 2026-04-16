locals {
  do_register      = var.cudly_api_url != "" && var.contact_email != ""
  reg_account_name = var.account_name != "" ? var.account_name : "GCP ${local.project}"

  # WIF audience string — used in the credential config JSON AND in the
  # registration payload (gcp_wif_audience) so the backend's secret-free
  # auto-enable path works without needing the stored credential JSON.
  wif_audience = "//iam.googleapis.com/${google_iam_workload_identity_pool.cudly.name}/providers/${google_iam_workload_identity_pool_provider.cudly.workload_identity_pool_provider_id}"

  # Construct the GCP WIF credential config JSON (replaces manual gcloud command).
  gcp_wif_config = var.provider_type == "aws" ? jsonencode({
    type                              = "external_account"
    audience                          = local.wif_audience
    subject_token_type                = "urn:ietf:params:aws:token-type:aws4_request"
    service_account_impersonation_url = "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/${var.service_account_email}:generateAccessToken"
    token_url                         = "https://sts.googleapis.com/v1/token"
    credential_source = {
      environment_id                 = "aws1"
      region_url                     = "http://169.254.169.254/latest/meta-data/placement/availability-zone"
      url                            = "http://169.254.169.254/latest/meta-data/iam/security-credentials"
      regional_cred_verification_url = "https://sts.{region}.amazonaws.com?Action=GetCallerIdentity&Version=2011-06-15"
    }
    }) : jsonencode({
    type                              = "external_account"
    audience                          = local.wif_audience
    subject_token_type                = "urn:ietf:params:oauth:token-type:id_token"
    service_account_impersonation_url = "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/${var.service_account_email}:generateAccessToken"
    token_url                         = "https://sts.googleapis.com/v1/token"
    credential_source = {
      file = "/var/run/secrets/azure/tokens/azure-identity-token"
    }
  })

  reg_payload = local.do_register ? jsonencode({
    provider           = "gcp"
    external_id        = local.project
    account_name       = local.reg_account_name
    contact_email      = var.contact_email
    description        = "Registered via Terraform federation IaC (gcp-target/wif)"
    gcp_project_id     = local.project
    gcp_client_email   = var.service_account_email
    gcp_auth_mode      = "workload_identity_federation"
    gcp_wif_audience   = local.wif_audience
    credential_type    = "gcp_workload_identity_config"
    credential_payload = local.gcp_wif_config
  }) : ""
}

data "http" "cudly_registration" {
  count  = local.do_register ? 1 : 0
  url    = "${var.cudly_api_url}/api/register"
  method = "POST"

  request_headers = {
    Content-Type = "application/json"
  }

  request_body = local.reg_payload

  # Defer to apply phase — ensure GCP resources are created before registering.
  depends_on = [google_service_account_iam_member.cudly_wif]

  lifecycle {
    postcondition {
      condition     = contains([200, 201, 409], self.status_code)
      error_message = "CUDly registration failed with HTTP ${self.status_code}: ${self.response_body}"
    }
  }
}
