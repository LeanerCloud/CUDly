# ==============================================
# Docker Build (before compute deployment)
# ==============================================

# Build module is optional - set enable_docker_build=false to use var.image_uri instead
module "build" {
  source = "../../modules/build"
  count  = var.enable_docker_build ? 1 : 0

  # ECR registry configuration
  registry_url = "${data.aws_caller_identity.current.account_id}.dkr.ecr.${var.region}.amazonaws.com"
  image_name   = module.registry.repository_name

  # Build configuration
  source_path = "${path.root}/../../.." # Root of the project (where Dockerfile is)
  platform    = "linux/arm64"           # Lambda and Fargate use ARM64 (Graviton2, 20% cost savings)

  # Registry login for ECR
  registry_login_command = "aws ecr get-login-password --region ${var.region}${var.aws_profile != null ? " --profile ${var.aws_profile}" : ""} | docker login --username AWS --password-stdin ${data.aws_caller_identity.current.account_id}.dkr.ecr.${var.region}.amazonaws.com"

  # Build options
  skip_docker_build  = false
  cleanup_old_images = true

  depends_on = [module.registry]
}
