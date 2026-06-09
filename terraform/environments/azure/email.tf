# ==============================================
# Email Service (Azure Communication Services)
# ==============================================

module "email" {
  source = "../../modules/email/azure"
  count  = var.enable_email_service ? 1 : 0

  app_name            = local.app_name
  resource_group_name = azurerm_resource_group.main.name
  data_location       = var.email_data_location
  key_vault_name      = module.secrets.key_vault_name

  # Use Azure-managed domain for dev (*.azurecomm.net)
  # For production, set to false and specify custom_domain_name
  use_azure_managed_domain = var.email_use_azure_managed_domain
  custom_domain_name       = var.email_custom_domain_name

  tags = local.common_tags

  depends_on = [azurerm_resource_group.main]
}
