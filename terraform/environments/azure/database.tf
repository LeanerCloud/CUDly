# ==============================================
# Database Module (PostgreSQL Flexible Server)
# ==============================================

module "database" {
  source = "../../modules/database/azure"

  app_name            = local.app_name
  environment         = var.environment
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location

  postgres_version             = var.postgres_version
  sku_name                     = var.database_sku_name
  storage_mb                   = var.database_storage_mb
  administrator_login          = var.database_administrator_login
  administrator_password       = module.secrets.database_password_value
  backup_retention_days        = var.database_backup_retention_days
  geo_redundant_backup_enabled = var.database_geo_redundant_backup
  high_availability_mode       = var.database_high_availability_mode
  standby_availability_zone    = var.database_standby_availability_zone
  delegated_subnet_id          = module.networking.database_subnet_id
  private_dns_zone_id          = module.networking.postgres_private_dns_zone_id
  enable_diagnostics           = true
  log_analytics_workspace_id   = module.networking.log_analytics_workspace_id

  server_parameters = {
    "azure.extensions" = "UUID-OSSP,PG_TRGM"
  }

  tags = local.common_tags

  depends_on = [module.networking, module.secrets]
}
