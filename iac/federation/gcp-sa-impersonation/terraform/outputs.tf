output "target_service_account" {
  description = "Target SA email. Set as gcp_client_email in CUDly (optional)."
  value       = var.service_account_email
}

output "cudly_account_registration" {
  description = "Values to use when registering this account in CUDly"
  value       = <<-EOT
    provider          : gcp
    gcp_auth_mode     : application_default
    gcp_project_id    : ${var.project_id}
    gcp_client_email  : ${var.service_account_email}
  EOT
}
