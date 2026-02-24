# ==============================================
# Frontend (Azure CDN + Blob Storage)
# ==============================================

module "frontend" {
  source = "../../modules/frontend/azure"
  count  = var.enable_frontend ? 1 : 0

  project_name        = var.app_name
  environment         = var.environment
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location

  # Storage account for frontend files
  storage_account_name = var.frontend_storage_account_name != "" ? var.frontend_storage_account_name : "${replace(local.app_name, "-", "")}frontend"

  # API endpoint - gets from Container Apps
  api_hostname = var.compute_platform == "container-apps" ? (
    length(module.compute_container_apps) > 0 ? module.compute_container_apps[0].container_app_fqdn : ""
  ) : ""

  # CDN configuration
  cdn_sku = var.frontend_cdn_sku

  # Custom domain configuration
  domain_names        = var.frontend_domain_names
  subdomain_zone_name = var.subdomain_zone_name
  use_front_door      = var.use_front_door

  # Frontend build configuration
  enable_frontend_build = var.enable_frontend_build

  tags = local.common_tags

  depends_on = [module.compute_container_apps, azurerm_resource_group.main]
}
