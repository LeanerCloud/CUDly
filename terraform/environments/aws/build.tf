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
  platform    = "linux/arm64"           # Always use ARM64 for cost savings (Fargate Graviton + Lambda ARM64)

  # Registry login for ECR
  registry_login_command = "aws ecr get-login-password --region ${var.region} --profile ${var.aws_profile != null ? var.aws_profile : "default"} | docker login --username AWS --password-stdin ${data.aws_caller_identity.current.account_id}.dkr.ecr.${var.region}.amazonaws.com"

  # Build options
  skip_docker_build  = false
  skip_docker_push   = false
  cleanup_old_images = true
  load_image         = false
}
