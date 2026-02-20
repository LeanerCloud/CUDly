output "communication_service_id" {
  description = "Azure Communication Services resource ID"
  value       = azurerm_communication_service.main.id
}

output "communication_service_name" {
  description = "Azure Communication Services resource name"
  value       = azurerm_communication_service.main.name
}

output "email_service_id" {
  description = "Email Communication Service resource ID"
  value       = azurerm_email_communication_service.main.id
}

output "email_service_name" {
  description = "Email Communication Service resource name"
  value       = azurerm_email_communication_service.main.name
}

output "domain_name" {
  description = "Email domain name (Azure-managed or custom)"
  value = var.use_azure_managed_domain ? (
    length(azurerm_email_communication_service_domain.managed) > 0 ? azurerm_email_communication_service_domain.managed[0].name : ""
    ) : (
    length(azurerm_email_communication_service_domain.custom) > 0 ? azurerm_email_communication_service_domain.custom[0].name : ""
  )
}

output "sender_address" {
  description = "Default sender email address"
  value = var.use_azure_managed_domain ? (
    length(azurerm_email_communication_service_domain.managed) > 0 ? "DoNotReply@${azurerm_email_communication_service_domain.managed[0].mail_from_sender_domain}" : ""
    ) : (
    length(azurerm_email_communication_service_domain.custom) > 0 ? "DoNotReply@${azurerm_email_communication_service_domain.custom[0].mail_from_sender_domain}" : ""
  )
}

output "smtp_endpoint" {
  description = "SMTP endpoint for Azure Communication Services"
  value       = "smtp.azurecomm.net"
}

output "smtp_port" {
  description = "SMTP port (TLS)"
  value       = 587
}

output "smtp_credentials_instructions" {
  description = "Instructions for generating SMTP credentials"
  value       = <<-EOT
    Generate SMTP credentials via Azure Portal or CLI:

    1. Portal: Communication Services → ${azurerm_email_communication_service.main.name} → Domains → Select domain → SMTP → Generate credentials

    2. CLI: See terraform output for detailed commands

    3. Store in Key Vault secrets:
       - azure-smtp-username
       - azure-smtp-password
  EOT
}
