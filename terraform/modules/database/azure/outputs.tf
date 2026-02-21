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

output "password_secret_id" {
  description = "Key Vault secret ID for database password"
  value       = azurerm_key_vault_secret.db_password.id
}

output "password_secret_name" {
  description = "Key Vault secret name for database password"
  value       = azurerm_key_vault_secret.db_password.name
}

output "connection_details" {
  description = "Database connection details"
  value = {
    host               = azurerm_postgresql_flexible_server.main.fqdn
    port               = 5432
    database           = azurerm_postgresql_flexible_server_database.main.name
    username           = azurerm_postgresql_flexible_server.main.administrator_login
    password_secret_id = azurerm_key_vault_secret.db_password.id
    ssl_mode           = "require"
  }
  sensitive = true
}
