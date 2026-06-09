output "container_app_id" {
  description = "Container App ID"
  value       = azurerm_container_app.main.id
}

output "container_app_name" {
  description = "Container App name"
  value       = azurerm_container_app.main.name
}

output "container_app_fqdn" {
  description = "Container App FQDN (stable, not revision-specific)"
  value       = azurerm_container_app.main.ingress[0].fqdn
}

output "container_app_url" {
  description = "Container App URL"
  value       = "https://${azurerm_container_app.main.ingress[0].fqdn}"
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

output "ingress_fqdn" {
  description = "Ingress FQDN for external access (stable, not revision-specific)"
  value       = azurerm_container_app.main.ingress[0].fqdn
}

# ==============================================
# Scheduled Tasks (Logic Apps) Outputs
# ==============================================

output "recommendations_workflow_id" {
  description = "ID of the recommendations Logic App workflow"
  value       = var.enable_scheduled_tasks ? azurerm_logic_app_workflow.recommendations[0].id : null
}

output "cleanup_workflow_id" {
  description = "ID of the cleanup Logic App workflow"
  value       = var.enable_scheduled_tasks ? azurerm_logic_app_workflow.cleanup[0].id : null
}

output "recommendation_schedule" {
  description = "Schedule for recommendations workflow"
  value       = var.enable_scheduled_tasks ? "Daily at ${local.schedule_hour}:00 UTC" : null
}
