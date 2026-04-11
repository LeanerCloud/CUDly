output "client_id" {
  description = "Azure AD application (client) ID — use as azure_client_id in CUDly account registration"
  value       = azuread_application.cudly.client_id
}

output "tenant_id" {
  description = "Azure AD tenant ID — use as azure_tenant_id in CUDly account registration"
  value       = var.tenant_id
}

output "cudly_account_registration" {
  description = "Values to use when registering this account in CUDly"
  value       = <<-EOT
    provider               : azure
    azure_auth_mode        : workload_identity_federation
    azure_subscription_id  : ${var.subscription_id}
    azure_tenant_id        : ${var.tenant_id}
    azure_client_id        : ${azuread_application.cudly.client_id}
    azure_wif_private_key  : <store the private key PEM in CUDly — it was NOT managed by Terraform>
  EOT
}

output "registration_response" {
  description = "CUDly registration API response (contains reference_token for status checks)"
  value       = local.do_register ? data.http.cudly_registration[0].response_body : "Skipped (cudly_api_url or contact_email not set)"
  sensitive   = false
}
