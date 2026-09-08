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
  # platform not set — auto-detected from builder host (Container Apps and AKS support arm64 and amd64)

  # Registry login with the caller's Entra identity (deploy SP in CI, az login
  # locally); the principal needs AcrPush on the registry.
  registry_login_command = "az acr login --name ${azurerm_container_registry.main.name}"

  # Build options
  skip_docker_build  = false
  cleanup_old_images = true
}
