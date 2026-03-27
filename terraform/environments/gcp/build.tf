# ==============================================
# Docker Build (before compute deployment)
# ==============================================

# Build module is optional - set enable_docker_build=false to use var.image_uri instead
module "build" {
  source = "../../modules/build"
  count  = var.enable_docker_build ? 1 : 0

  # Artifact Registry configuration
  registry_url = module.registry.repository_url
  image_name   = "cudly"

  # Build configuration
  source_path = "${path.root}/../../.." # Root of the project (where Dockerfile is)
  # platform not set — auto-detected from builder host (Cloud Run and GKE support arm64 and amd64)

  # Registry login for Artifact Registry
  registry_login_command = "gcloud auth configure-docker ${var.region}-docker.pkg.dev"

  # Build options
  skip_docker_build  = false
  cleanup_old_images = true
}
