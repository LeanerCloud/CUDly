output "server_id" {
  description = "PostgreSQL server ID"
  value       = azurerm_postgresql_flexible_server.main.id
}

output "server_name" {
  description = "PostgreSQL server name"
  value       = azurerm_postgresql_flexible_server.main.name
}

output "server_fqdn" {
  description = "PostgreSQL server FQDN"
  value       = azurerm_postgresql_flexible_server.main.fqdn
}

output "database_name" {
  description = "Database name"
  value       = azurerm_postgresql_flexible_server_database.main.name
}

output "administrator_login" {
  description = "Administrator login name"
  value       = azurerm_postgresql_flexible_server.main.administrator_login
  sensitive   = true
}

output "connection_details" {
  description = "Database connection details"
  value = {
    host     = azurerm_postgresql_flexible_server.main.fqdn
    port     = 5432
    database = azurerm_postgresql_flexible_server_database.main.name
    username = azurerm_postgresql_flexible_server.main.administrator_login
    ssl_mode = "require"
  }
  sensitive = true
}
