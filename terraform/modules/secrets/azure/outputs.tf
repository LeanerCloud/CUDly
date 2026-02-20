output "key_vault_id" {
  description = "Key Vault ID"
  value       = azurerm_key_vault.main.id
}

output "key_vault_name" {
  description = "Key Vault name"
  value       = azurerm_key_vault.main.name
}

output "key_vault_uri" {
  description = "Key Vault URI"
  value       = azurerm_key_vault.main.vault_uri
}

output "database_password_secret_id" {
  description = "Database password secret ID"
  value       = azurerm_key_vault_secret.database_password.id
}

output "database_password_secret_name" {
  description = "Database password secret name"
  value       = azurerm_key_vault_secret.database_password.name
}

output "database_password_value" {
  description = "Database password value (use with caution)"
  value       = azurerm_key_vault_secret.database_password.value
  sensitive   = true
}

output "jwt_secret_id" {
  description = "JWT secret ID (if created)"
  value       = var.create_jwt_secret ? azurerm_key_vault_secret.jwt_secret[0].id : null
}

output "jwt_secret_name" {
  description = "JWT secret name (if created)"
  value       = var.create_jwt_secret ? azurerm_key_vault_secret.jwt_secret[0].name : null
}

output "session_secret_id" {
  description = "Session secret ID (if created)"
  value       = var.create_session_secret ? azurerm_key_vault_secret.session_secret[0].id : null
}

output "session_secret_name" {
  description = "Session secret name (if created)"
  value       = var.create_session_secret ? azurerm_key_vault_secret.session_secret[0].name : null
}

output "smtp_username_id" {
  description = "SMTP username secret ID (if created)"
  value       = var.create_smtp_secrets ? azurerm_key_vault_secret.smtp_username[0].id : null
}

output "smtp_username_name" {
  description = "SMTP username secret name (if created)"
  value       = var.create_smtp_secrets ? azurerm_key_vault_secret.smtp_username[0].name : null
}

output "smtp_password_id" {
  description = "SMTP password secret ID (if created)"
  value       = var.create_smtp_secrets ? azurerm_key_vault_secret.smtp_password[0].id : null
}

output "smtp_password_name" {
  description = "SMTP password secret name (if created)"
  value       = var.create_smtp_secrets ? azurerm_key_vault_secret.smtp_password[0].name : null
}

output "additional_secret_ids" {
  description = "Map of additional secret IDs"
  value       = { for k, v in azurerm_key_vault_secret.additional : k => v.id }
}

output "additional_secret_names" {
  description = "Map of additional secret names"
  value       = { for k, v in azurerm_key_vault_secret.additional : k => v.name }
}

# Convenience output with all secret names
output "all_secret_names" {
  description = "List of all secret names"
  value = concat(
    [azurerm_key_vault_secret.database_password.name],
    var.create_jwt_secret ? [azurerm_key_vault_secret.jwt_secret[0].name] : [],
    var.create_session_secret ? [azurerm_key_vault_secret.session_secret[0].name] : [],
    [for secret in azurerm_key_vault_secret.additional : secret.name]
  )
}
