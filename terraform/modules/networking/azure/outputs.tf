output "vnet_id" {
  description = "Virtual Network ID"
  value       = azurerm_virtual_network.main.id
}

output "vnet_name" {
  description = "Virtual Network name"
  value       = azurerm_virtual_network.main.name
}

output "vnet_address_space" {
  description = "Virtual Network address space"
  value       = azurerm_virtual_network.main.address_space
}

output "container_apps_subnet_id" {
  description = "Container Apps subnet ID"
  value       = azurerm_subnet.container_apps.id
}

output "container_apps_subnet_name" {
  description = "Container Apps subnet name"
  value       = azurerm_subnet.container_apps.name
}

output "database_subnet_id" {
  description = "Database subnet ID"
  value       = azurerm_subnet.database.id
}

output "database_subnet_name" {
  description = "Database subnet name"
  value       = azurerm_subnet.database.name
}

output "private_subnet_id" {
  description = "Private subnet ID (if created)"
  value       = var.create_private_subnet ? azurerm_subnet.private[0].id : null
}

output "private_subnet_name" {
  description = "Private subnet name (if created)"
  value       = var.create_private_subnet ? azurerm_subnet.private[0].name : null
}

output "postgres_private_dns_zone_id" {
  description = "PostgreSQL Private DNS zone ID"
  value       = azurerm_private_dns_zone.postgres.id
}

output "postgres_private_dns_zone_name" {
  description = "PostgreSQL Private DNS zone name"
  value       = azurerm_private_dns_zone.postgres.name
}

output "container_apps_nsg_id" {
  description = "Container Apps NSG ID"
  value       = azurerm_network_security_group.container_apps.id
}

output "database_nsg_id" {
  description = "Database NSG ID"
  value       = azurerm_network_security_group.database.id
}

output "log_analytics_workspace_id" {
  description = "Log Analytics workspace ID (if created)"
  value       = var.create_log_analytics ? azurerm_log_analytics_workspace.main[0].id : null
}

output "log_analytics_workspace_name" {
  description = "Log Analytics workspace name (if created)"
  value       = var.create_log_analytics ? azurerm_log_analytics_workspace.main[0].name : null
}

output "network_watcher_id" {
  description = "Network Watcher ID (if created)"
  value       = var.create_network_watcher ? azurerm_network_watcher.main[0].id : null
}
