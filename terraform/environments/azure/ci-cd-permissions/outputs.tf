output "role_definition_id" {
  description = "ID of the CUDly Terraform deploy custom role"
  value       = azurerm_role_definition.cudly_deploy.id
}

output "role_assignment_id" {
  description = "ID of the role assignment"
  value       = azurerm_role_assignment.cudly_deploy.id
}

output "client_id" {
  description = "Client ID (app ID) of the deploy service principal — use as AZURE_CLIENT_ID in GitHub Actions"
  value       = azuread_application.cudly_deploy.client_id
}

output "sp_object_id" {
  description = "Object ID of the deploy service principal"
  value       = azuread_service_principal.cudly_deploy.object_id
}

output "tenant_id" {
  description = "Azure AD tenant ID — use as AZURE_TENANT_ID in GitHub Actions"
  value       = azuread_service_principal.cudly_deploy.application_tenant_id
}
