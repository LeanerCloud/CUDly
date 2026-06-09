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

output "admin_password_secret_id" {
  description = "Admin password secret ID (if created)"
  value       = var.create_admin_password_secret ? azurerm_key_vault_secret.admin_password[0].id : null
}

output "admin_password_secret_name" {
  description = "Admin password secret name (if created)"
  value       = var.create_admin_password_secret ? azurerm_key_vault_secret.admin_password[0].name : null
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

output "scheduled_task_secret_value" {
  description = "Scheduled task secret value (raw password for env var)"
  value       = var.create_scheduled_task_secret ? random_password.scheduled_task_secret[0].result : null
  sensitive   = true
}

output "scheduled_task_secret_name" {
  description = "Scheduled task secret Key Vault name"
  value       = var.create_scheduled_task_secret ? azurerm_key_vault_secret.scheduled_task_secret[0].name : null
}

output "signing_key_name" {
  description = "Name of the OIDC issuer signing key in Key Vault"
  value       = azurerm_key_vault_key.signing.name
}

output "signing_key_id" {
  description = "Full Key Vault key ID of the OIDC issuer signing key"
  value       = azurerm_key_vault_key.signing.id
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
    var.create_admin_password_secret ? [azurerm_key_vault_secret.admin_password[0].name] : [],
    var.create_jwt_secret ? [azurerm_key_vault_secret.jwt_secret[0].name] : [],
    var.create_session_secret ? [azurerm_key_vault_secret.session_secret[0].name] : [],
    [for secret in azurerm_key_vault_secret.additional : secret.name]
  )
}

# Emitted when SMTP secrets are created with placeholder values (no
# smtp_username/smtp_password passed). Prints the exact helper-script
# invocation the operator should run next to get the portal flow +
# pre-filled `az keyvault secret set` commands. Empty string when SMTP
# creation is skipped or credentials were provided at apply time.
output "smtp_setup_instructions" {
  description = "Next-step command to generate Azure ACS SMTP credentials, with deployment-specific values pre-filled. Non-empty when SMTP secrets are created; operator should run the printed command if credentials weren't pre-generated elsewhere."
  # References only non-sensitive vars (resource_group_name, key_vault_name).
  # We deliberately DON'T condition on smtp_username/smtp_password being
  # null — those variables are marked sensitive, and checking them
  # (even for null) taints the output. If creds were passed at apply
  # time, the printed instructions are harmless no-ops; operators can
  # ignore them.
  value = var.create_smtp_secrets ? format(
    "Generate Azure ACS SMTP credentials if not pre-supplied. Run:\n  bash scripts/azure-smtp-setup.sh %s <acs-domain-name> %s\n(Replace <acs-domain-name> with the email domain you connected to Azure Communication Services; the script emits pre-filled portal + az CLI steps. Skip this step if you passed -var smtp_username=... -var smtp_password=... to terraform apply.)",
    var.resource_group_name,
    var.key_vault_name,
  ) : ""
}
