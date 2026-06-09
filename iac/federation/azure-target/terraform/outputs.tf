output "client_id" {
  description = "Azure AD application (client) ID — use as azure_client_id in CUDly account registration"
  value       = azuread_application.cudly.client_id
}

output "tenant_id" {
  description = "Azure AD tenant ID — use as azure_tenant_id in CUDly account registration"
  value       = local.tenant_id
}

output "cudly_account_registration" {
  description = "Values to use when registering this account in CUDly. Marked sensitive so subscription/tenant IDs do not leak into CI/CD logs; retrieve via `terraform output -raw cudly_account_registration` when needed."
  value       = <<-EOT
    provider               : azure
    azure_auth_mode        : workload_identity_federation
    azure_subscription_id  : ${local.subscription_id}
    azure_tenant_id        : ${local.tenant_id}
    azure_client_id        : ${azuread_application.cudly.client_id}
    No secret or key needed — CUDly signs JWTs via its OIDC issuer.
  EOT
  sensitive   = true
}

output "registration_response" {
  description = "CUDly registration API response (contains reference_token for status checks)"
  value       = local.do_register ? data.http.cudly_registration[0].response_body : "Skipped (cudly_api_url or contact_email not set)"
  sensitive   = false
}
