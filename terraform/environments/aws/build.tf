# ==============================================
# Docker Build (before compute deployment)
# ==============================================

# Build module is optional - set enable_docker_build=false to use var.image_uri instead
module "build" {
  source = "../../modules/build"
  count  = var.enable_docker_build ? 1 : 0

  # ECR registry configuration
  registry_url = "${data.aws_caller_identity.current.account_id}.dkr.ecr.${var.region}.amazonaws.com"
  image_name   = "cudly"

  # Build configuration
  source_path = "${path.root}/../../.." # Root of the project (where Dockerfile is)
  # platform defaults to native (arm64 on Apple Silicon, amd64 on x86). Set explicitly for cross-compilation in CI/CD.

  # Registry login for ECR
  registry_login_command = "aws ecr get-login-password --region ${var.region}${var.aws_profile != null ? " --profile ${var.aws_profile}" : ""} | docker login --username AWS --password-stdin ${data.aws_caller_identity.current.account_id}.dkr.ecr.${var.region}.amazonaws.com"

  # Build options
  skip_docker_build  = false
  skip_docker_push   = false
  cleanup_old_images = true
  load_image         = false
}
