# ==============================================
# Secrets Module (Key Vault)
# ==============================================

module "secrets" {
  source = "../../modules/secrets/azure"

  app_name            = local.app_name
  environment         = var.environment
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location

  key_vault_name             = var.key_vault_name
  sku_name                   = var.key_vault_sku
  soft_delete_retention_days = var.soft_delete_retention_days
  purge_protection_enabled   = var.purge_protection_enabled
  default_network_acl_action = var.key_vault_network_acl_action
  allowed_ip_addresses       = var.allowed_ip_addresses
  allowed_subnet_ids         = var.create_private_subnet ? [module.networking.private_subnet_id] : []
  database_password          = var.database_password
  create_jwt_secret          = true
  create_session_secret      = true
  additional_secrets         = var.additional_secrets
  log_analytics_workspace_id = module.networking.log_analytics_workspace_id

  tags = local.common_tags

  depends_on = [module.networking]
}
