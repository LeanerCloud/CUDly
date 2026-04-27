output "registry_url" {
  description = "Login server URL for the ACR"
  value       = azurerm_container_registry.main.login_server
}

output "registry_id" {
  description = "ID of the container registry"
  value       = azurerm_container_registry.main.id
}

output "registry_name" {
  description = "Name of the container registry"
  value       = azurerm_container_registry.main.name
}

output "admin_username" {
  description = "Admin username (only if admin_enabled is true)"
  value       = var.enable_admin_user ? azurerm_container_registry.main.admin_username : null
  sensitive   = true
}

output "admin_password" {
  description = "Admin password (only if admin_enabled is true)"
  value       = var.enable_admin_user ? azurerm_container_registry.main.admin_password : null
  sensitive   = true
}
