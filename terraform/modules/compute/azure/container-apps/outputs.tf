output "container_app_id" {
  description = "Container App ID"
  value       = azurerm_container_app.main.id
}

output "container_app_name" {
  description = "Container App name"
  value       = azurerm_container_app.main.name
}

output "container_app_fqdn" {
  description = "Container App FQDN"
  value       = azurerm_container_app.main.latest_revision_fqdn
}

output "container_app_url" {
  description = "Container App URL"
  value       = "https://${azurerm_container_app.main.latest_revision_fqdn}"
}

output "container_app_environment_id" {
  description = "Container App Environment ID"
  value       = azurerm_container_app_environment.main.id
}

output "managed_identity_id" {
  description = "User-assigned managed identity ID"
  value       = azurerm_user_assigned_identity.container_app.id
}

output "managed_identity_principal_id" {
  description = "User-assigned managed identity principal ID"
  value       = azurerm_user_assigned_identity.container_app.principal_id
}

output "managed_identity_client_id" {
  description = "User-assigned managed identity client ID"
  value       = azurerm_user_assigned_identity.container_app.client_id
}

output "scheduled_job_id" {
  description = "Scheduled job ID (if enabled)"
  value       = var.enable_scheduled_jobs ? azurerm_container_app_job.recommendations[0].id : null
}

output "ingress_fqdn" {
  description = "Ingress FQDN for external access"
  value       = azurerm_container_app.main.latest_revision_fqdn
}
