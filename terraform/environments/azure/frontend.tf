# ==============================================
# Frontend (Azure CDN -> Container Apps)
# ==============================================

module "frontend" {
  source = "../../modules/frontend/azure"
  count  = var.enable_cdn ? 1 : 0

  project_name        = var.project_name
  environment         = var.environment
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location

  # All traffic routes to Container Apps
  api_hostname = var.compute_platform == "container-apps" ? (
    length(module.compute_container_apps) > 0 ? module.compute_container_apps[0].container_app_fqdn : ""
  ) : ""

  # CDN configuration
  cdn_sku = var.frontend_cdn_sku

  # Custom domain configuration
  domain_names        = var.frontend_domain_names
  subdomain_zone_name = var.subdomain_zone_name
  use_front_door      = var.use_front_door

  tags = local.common_tags

  depends_on = [module.compute_container_apps, azurerm_resource_group.main]
}
