# ==============================================
# Docker Build (before compute deployment)
# ==============================================

locals {
  # ACR names must be alphanumeric only (no hyphens), 5-50 chars
  acr_name = "${replace(local.app_name, "-", "")}acr"
}

# Build module is optional - set enable_docker_build=false to use var.image_uri instead
module "build" {
  source = "../../modules/build"
  count  = var.enable_docker_build ? 1 : 0

  # ACR registry configuration (Azure Container Registry)
  registry_url = azurerm_container_registry.main.login_server
  image_name   = "cudly"

  # Build configuration
  source_path = "${path.root}/../../.." # Root of the project (where Dockerfile is)
  platform    = "linux/amd64"           # Azure Container Apps and AKS use amd64

  # Registry login for ACR using admin credentials
  registry_login_command = "echo '${nonsensitive(azurerm_container_registry.main.admin_password)}' | docker login ${azurerm_container_registry.main.login_server} -u ${azurerm_container_registry.main.admin_username} --password-stdin"

  # Build options
  skip_docker_build  = false
  cleanup_old_images = true
}
