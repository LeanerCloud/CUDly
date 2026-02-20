# ==============================================
# Docker Build (before compute deployment)
# ==============================================

# Build module is optional - set enable_docker_build=false to use var.image_uri instead
module "build" {
  source = "../../modules/build"
  count  = var.enable_docker_build ? 1 : 0

  # ACR registry configuration (Azure Container Registry)
  registry_url = "${local.app_name}acr.azurecr.io"
  image_name   = "cudly"

  # Build configuration
  source_path = "${path.root}/../../.." # Root of the project (where Dockerfile is)
  platform    = "linux/amd64"           # Azure Container Apps and AKS use amd64

  # Registry login for ACR
  registry_login_command = "az acr login --name ${local.app_name}acr"

  # Build options
  skip_docker_build  = false
  skip_docker_push   = false
  cleanup_old_images = true
  load_image         = false
}
