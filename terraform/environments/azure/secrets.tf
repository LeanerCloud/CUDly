# ==============================================
# Credential Encryption Key (auto-generate if not provided)
# ==============================================

resource "random_bytes" "credential_encryption_key" {
  count  = var.credential_encryption_key == "" ? 1 : 0
  length = 32
}

locals {
  credential_encryption_key = var.credential_encryption_key != "" ? var.credential_encryption_key : (
    length(random_bytes.credential_encryption_key) > 0 ? random_bytes.credential_encryption_key[0].hex : ""
  )
}

# ==============================================
# Secrets Module (Key Vault)
# ==============================================

module "secrets" {
  source = "../../modules/secrets/azure"

  app_name            = local.app_name
  environment         = var.environment
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location

  key_vault_name             = "${local.app_name}-kv"
  sku_name                   = var.key_vault_sku
  soft_delete_retention_days = var.soft_delete_retention_days
  purge_protection_enabled   = var.purge_protection_enabled
  default_network_acl_action = "Allow" # Allow access from all networks for dev
  allowed_ip_addresses       = var.allowed_ip_addresses
  allowed_subnet_ids         = var.create_private_subnet ? [module.networking.private_subnet_id] : []
  database_password          = var.database_password
  admin_password             = var.admin_password
  create_jwt_secret          = true
  create_session_secret      = true
  additional_secrets = merge(nonsensitive(var.additional_secrets), {
    "credential-encryption-key" = local.credential_encryption_key
  })
  log_analytics_workspace_id = null # Diagnostics configured separately after workspace creation

  tags = local.common_tags

  depends_on = [module.networking]
}
