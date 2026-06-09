# ==============================================
# Networking Module
# ==============================================

module "networking" {
  source = "../../modules/networking/azure"

  app_name            = local.app_name
  environment         = var.environment
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location

  vnet_cidr                   = var.vnet_cidr
  container_apps_subnet_cidr  = var.container_apps_subnet_cidr
  database_subnet_cidr        = var.database_subnet_cidr
  private_subnet_cidr         = var.private_subnet_cidr
  create_private_subnet       = var.create_private_subnet
  allow_inbound_from_internet = var.allow_inbound_from_internet
  create_log_analytics        = true
  log_retention_days          = var.log_retention_days

  tags = local.common_tags
}
