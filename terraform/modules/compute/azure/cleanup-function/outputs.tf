output "function_app_name" {
  description = "Name of the Function App"
  value       = azurerm_linux_function_app.cleanup.name
}

output "function_app_url" {
  description = "Default hostname of the Function App"
  value       = "https://${azurerm_linux_function_app.cleanup.default_hostname}"
}

output "identity_principal_id" {
  description = "Principal ID of the managed identity"
  value       = azurerm_user_assigned_identity.cleanup.principal_id
}
