# Azure Communication Services Email Module
# Provides email sending capability for Azure deployments

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
  }
}

# ==============================================
# Communication Services Resource
# ==============================================

resource "azurerm_communication_service" "main" {
  name                = "${var.app_name}-communication"
  resource_group_name = var.resource_group_name
  data_location       = var.data_location

  tags = var.tags
}

# ==============================================
# Email Communication Service
# ==============================================

resource "azurerm_email_communication_service" "main" {
  name                = "${var.app_name}-email"
  resource_group_name = var.resource_group_name
  data_location       = var.data_location

  tags = var.tags
}

# ==============================================
# Email Domain (Azure-Managed for Dev/Test)
# ==============================================

resource "azurerm_email_communication_service_domain" "managed" {
  count = var.use_azure_managed_domain ? 1 : 0

  name             = "AzureManagedDomain"
  email_service_id = azurerm_email_communication_service.main.id

  domain_management = "AzureManaged"

  tags = var.tags
}

# ==============================================
# Custom Domain (for Production)
# ==============================================

resource "azurerm_email_communication_service_domain" "custom" {
  count = var.custom_domain_name != "" ? 1 : 0

  name             = var.custom_domain_name
  email_service_id = azurerm_email_communication_service.main.id

  domain_management = "CustomerManaged"

  tags = var.tags
}

# ==============================================
# Link Email Service to Communication Service
# ==============================================

# Note: Connection is created automatically when domain is provisioned
# SMTP credentials must be generated manually via Azure Portal or CLI
# as there's no Terraform resource for SMTP credential generation yet

# ==============================================
# Auto-Generate SMTP Credentials (Optional)
# ==============================================

# Attempt to retrieve SMTP configuration via Azure CLI
resource "null_resource" "get_smtp_credentials" {
  count = var.auto_generate_smtp_credentials ? 1 : 0

  provisioner "local-exec" {
    command = <<-EOT
      #!/bin/bash
      set -e

      echo "Attempting to retrieve Azure Communication Services SMTP configuration..."

      # Get the domain name
      DOMAIN_NAME="${var.use_azure_managed_domain ? "AzureManagedDomain" : var.custom_domain_name}"

      # Check if domain is provisioned and ready
      DOMAIN_STATUS=$(az communication email domain show \
        --email-service-name ${azurerm_email_communication_service.main.name} \
        --domain-name "$DOMAIN_NAME" \
        --resource-group ${var.resource_group_name} \
        --query "domainManagement" -o tsv 2>/dev/null || echo "NotFound")

      if [ "$DOMAIN_STATUS" = "NotFound" ]; then
        echo "⚠️  Domain not yet fully provisioned. Please wait a few minutes and run terraform apply again."
        exit 0
      fi

      # Get the Mail From sender domain (this is the SMTP endpoint identifier)
      MAIL_FROM=$(az communication email domain show \
        --email-service-name ${azurerm_email_communication_service.main.name} \
        --domain-name "$DOMAIN_NAME" \
        --resource-group ${var.resource_group_name} \
        --query "mailFromSenderDomain" -o tsv)

      echo "✅ Mail From Domain: $MAIL_FROM"
      echo ""
      echo "⚠️  NOTE: Azure Communication Services SMTP credentials must be generated manually:"
      echo "   1. Go to Azure Portal → Communication Services → ${azurerm_email_communication_service.main.name}"
      echo "   2. Navigate to 'Domains' → Select '$DOMAIN_NAME'"
      echo "   3. Click 'SMTP' tab → 'Generate credentials'"
      echo "   4. Copy the username and password"
      echo ""
      echo "   Then store in Key Vault:"
      echo "   az keyvault secret set --vault-name ${var.key_vault_name} --name azure-smtp-username --value '<username>'"
      echo "   az keyvault secret set --vault-name ${var.key_vault_name} --name azure-smtp-password --value '<password>'"
      echo ""
      echo "   Sender address will be: DoNotReply@$MAIL_FROM"
    EOT
  }

  depends_on = [
    azurerm_email_communication_service_domain.managed,
    azurerm_email_communication_service_domain.custom
  ]

  triggers = {
    domain_id = var.use_azure_managed_domain ? (
      length(azurerm_email_communication_service_domain.managed) > 0 ? azurerm_email_communication_service_domain.managed[0].id : ""
    ) : (
      length(azurerm_email_communication_service_domain.custom) > 0 ? azurerm_email_communication_service_domain.custom[0].id : ""
    )
  }
}
