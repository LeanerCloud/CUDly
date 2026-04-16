output "pool_name" {
  description = "Full resource name of the Workload Identity Pool"
  value       = google_iam_workload_identity_pool.cudly.name
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
  EOT
  value = var.provider_type == "aws" ? (
    "gcloud iam workload-identity-pools create-cred-config ${google_iam_workload_identity_pool_provider.cudly.name} --service-account=${var.service_account_email} --aws --output-file=cudly-wif-config.json"
    ) : (
    "gcloud iam workload-identity-pools create-cred-config ${google_iam_workload_identity_pool_provider.cudly.name} --service-account=${var.service_account_email} --output-file=cudly-wif-config.json"
  )
}

output "cudly_account_registration" {
  description = "Values to use when registering this account in CUDly (auto-sent when cudly_api_url is set)"
  value       = <<-EOT
    provider                        : gcp
    gcp_auth_mode                   : workload_identity_federation
    gcp_project_id                  : ${local.project}
    gcp_client_email                : ${var.service_account_email}
    gcp_wif_audience                : ${local.wif_audience}
    If auto-registration is enabled, credential JSON + wif_audience are sent automatically.
  EOT
}

output "registration_response" {
  description = "CUDly registration API response (contains reference_token for status checks)"
  value       = local.do_register ? data.http.cudly_registration[0].response_body : "Skipped (cudly_api_url or contact_email not set)"
  sensitive   = false
}
