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
  # Default-deny network ACL: only ip_addresses in var.allowed_ip_addresses and
  # the Container App subnet reach the Key Vault data plane. Dev environments
  # that need cross-network access from an operator laptop should add the IP
  # explicitly; they should not widen this to "Allow".
  default_network_acl_action = var.key_vault_default_network_acl_action
  allowed_ip_addresses       = var.allowed_ip_addresses
  allowed_subnet_ids         = var.create_private_subnet ? [module.networking.private_subnet_id] : []
  database_password          = var.database_password
  admin_password             = var.admin_password
  create_jwt_secret          = true
  create_session_secret      = true
  # IMPORTANT: do NOT wrap this merge in nonsensitive(). The values flowing
  # through `additional_secrets` are real secrets that must stay redacted in
  # `terraform plan/apply` output and CI logs.
  additional_secrets = merge(var.additional_secrets, {
    "credential-encryption-key" = local.credential_encryption_key
  })
  log_analytics_workspace_id = null # Diagnostics configured separately after workspace creation

  tags = local.common_tags

  depends_on = [module.networking]
}
