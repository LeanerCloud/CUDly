# ==============================================
# Docker Build (before compute deployment)
# ==============================================

# Detect builder architecture at plan time so Lambda and Fargate use the same
# architecture as the host, avoiding cross-compilation on matching platforms.
data "external" "host_arch" {
  program = ["${path.module}/../../modules/build/scripts/detect-arch.sh"]
}

locals {
  # Use explicit lambda_architecture when set; otherwise match the builder host.
  # Lambda and Fargate both support arm64 (Graviton, 20% cheaper) and x86_64.
  effective_lambda_arch = var.lambda_architecture != "" ? var.lambda_architecture : data.external.host_arch.result.arch
}

# Build module is optional - set enable_docker_build=false to use var.image_uri instead
module "build" {
  source = "../../modules/build"
  count  = var.enable_docker_build ? 1 : 0

  # ECR registry configuration
  registry_url = "${data.aws_caller_identity.current.account_id}.dkr.ecr.${var.region}.amazonaws.com"
  image_name   = module.registry.repository_name

  # Build configuration
  source_path = "${path.root}/../../.." # Root of the project (where Dockerfile is)
  platform    = local.effective_lambda_arch == "arm64" ? "linux/arm64" : "linux/amd64"

  # Registry login for ECR
  registry_login_command = "aws ecr get-login-password --region ${var.region}${var.aws_profile != null ? " --profile ${var.aws_profile}" : ""} | docker login --username AWS --password-stdin ${data.aws_caller_identity.current.account_id}.dkr.ecr.${var.region}.amazonaws.com"

  # Build options
  skip_docker_build  = false
  cleanup_old_images = true

  depends_on = [module.registry]
}
