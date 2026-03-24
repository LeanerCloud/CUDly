# ==============================================
# Docker Build (before compute deployment)
# ==============================================

# Build module is optional - set enable_docker_build=false to use var.image_uri instead
module "build" {
  source = "../../modules/build"
  count  = var.enable_docker_build ? 1 : 0

  # GCR/Artifact Registry configuration
  registry_url = "gcr.io/${var.project_id}"
  image_name   = "cudly"

  # Build configuration
  source_path = "${path.root}/../../.." # Root of the project (where Dockerfile is)
  platform    = "linux/amd64"           # Cloud Run and GKE use amd64

  # Registry login for GCR
  registry_login_command = "gcloud auth configure-docker gcr.io"

  # Build options
  skip_docker_build  = false
  cleanup_old_images = true
}
