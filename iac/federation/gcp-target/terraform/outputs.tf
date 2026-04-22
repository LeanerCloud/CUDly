output "pool_name" {
  description = "Full resource name of the Workload Identity Pool"
  value       = google_iam_workload_identity_pool.cudly.name
}

output "service_account_email" {
  description = "Email of the GCP service account CUDly impersonates (created by Terraform when var.service_account_email is empty, otherwise passed through)."
  value       = local.service_account_email
}

output "provider_resource_name" {
  description = "Full resource name of the Workload Identity Pool Provider"
  value       = google_iam_workload_identity_pool_provider.cudly.name
}

output "project_number" {
  description = "Numeric project number (needed for the credential config)"
  value       = data.google_project.current.number
}

output "gcloud_command" {
  description = <<-EOT
    Optional: generates the external account credential config JSON locally.
    When auto-registration is enabled (cudly_api_url + contact_email set),
    Terraform already sends both gcp_wif_audience AND the credential JSON
    in the registration payload — this command is only needed for manual
    setups or debugging.

    For provider_type = "oidc", the command includes the --credential-source-*
    flags driven by var.oidc_credential_source{,_type,_format}. When
    oidc_credential_source is unset, the emitted command is placeholder
    text explaining what to fill in.
  EOT
  value = var.provider_type == "aws" ? (
    "gcloud iam workload-identity-pools create-cred-config ${google_iam_workload_identity_pool_provider.cudly.name} --service-account=${local.service_account_email} --aws --output-file=cudly-wif-config.json"
    ) : (
    var.oidc_credential_source == "" ? (
      "# Set var.oidc_credential_source (and optionally oidc_credential_source_type / _format) to emit a complete gcloud command."
      ) : format(
      "gcloud iam workload-identity-pools create-cred-config %s --service-account=%s --%s=%s --credential-source-type=%s --output-file=cudly-wif-config.json",
      google_iam_workload_identity_pool_provider.cudly.name,
      local.service_account_email,
      var.oidc_credential_source_type == "file" ? "credential-source-file" : "credential-source-url",
      var.oidc_credential_source,
      var.oidc_credential_source_format,
    )
  )
}

output "cudly_account_registration" {
  description = "Values to use when registering this account in CUDly (auto-sent when cudly_api_url is set)"
  value       = <<-EOT
    provider                        : gcp
    gcp_auth_mode                   : workload_identity_federation
    gcp_project_id                  : ${local.project}
    gcp_client_email                : ${local.service_account_email}
    gcp_wif_audience                : ${local.wif_audience}
    If auto-registration is enabled, credential JSON + wif_audience are sent automatically.
  EOT
}

output "registration_response" {
  description = "CUDly registration API response (contains reference_token for status checks)"
  value       = local.do_register ? data.http.cudly_registration[0].response_body : "Skipped (cudly_api_url or contact_email not set)"
  sensitive   = false
}
