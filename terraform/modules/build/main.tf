# Docker Build and Push Automation Module
# Handles building and pushing Docker images before infrastructure deployment

terraform {
  required_version = ">= 1.5"

  required_providers {
    external = {
      source  = "hashicorp/external"
      version = "~> 2.0"
    }
  }
}

# Detect builder architecture at plan time.
# Used to auto-select the target platform when var.platform is not set explicitly,
# so native builds are used on platforms that support multiple architectures.
data "external" "host_arch" {
  program = ["${path.module}/scripts/detect-arch.sh"]
}

# Generate image tag from git commit + timestamp
resource "terraform_data" "image_tag" {
  input = var.custom_image_tag != "" ? var.custom_image_tag : "${local.git_commit}-${local.timestamp}"
}

locals {
  # Get git commit hash (use source_path which points to the repo root, not path.root which is the terraform environment dir)
  git_commit = var.skip_docker_build ? "skip" : trimspace(try(
    file("${var.source_path}/.git/HEAD") != "" ? (
      can(regex("^ref:", file("${var.source_path}/.git/HEAD"))) ?
      substr(file("${var.source_path}/.git/${trimspace(replace(file("${var.source_path}/.git/HEAD"), "ref: ", ""))}"), 0, 7) :
      substr(file("${var.source_path}/.git/HEAD"), 0, 7)
    ) : "unknown",
    "unknown"
  ))

  # Generate timestamp
  timestamp = var.skip_docker_build ? "skip" : formatdate("YYYYMMDDhhmmss", timestamp())

  # Final image tag
  image_tag = terraform_data.image_tag.output

  # Full image URI
  image_uri = "${var.registry_url}/${var.image_name}:${local.image_tag}"

  # Effective platform: use explicit var.platform when set, otherwise match the builder host
  effective_platform = var.platform != "" ? var.platform : data.external.host_arch.result.platform
}

# Docker build and push (single step using buildx --push)
resource "terraform_data" "docker_build" {
  count = var.skip_docker_build ? 0 : 1

  triggers_replace = {
    # Rebuild when source code changes
    go_mod     = fileexists("${var.source_path}/go.mod") ? filemd5("${var.source_path}/go.mod") : "none"
    go_sum     = fileexists("${var.source_path}/go.sum") ? filemd5("${var.source_path}/go.sum") : "none"
    dockerfile = fileexists("${var.source_path}/Dockerfile") ? filemd5("${var.source_path}/Dockerfile") : "none"
    # Hash all Go files in cmd and pkg directories
    cmd_files = try(sha256(join("", [for f in fileset("${var.source_path}/cmd", "**/*.go") : filemd5("${var.source_path}/cmd/${f}")])), "none")
    pkg_files = try(sha256(join("", [for f in fileset("${var.source_path}/pkg", "**/*.go") : filemd5("${var.source_path}/pkg/${f}")])), "none")

    # Force rebuild when image tag or target platform changes
    image_tag = local.image_tag
    platform  = local.effective_platform
  }

  provisioner "local-exec" {
    working_dir = var.source_path
    command     = <<-EOT
      set -e
      echo "Logging in to registry..."
      ${var.registry_login_command}

      # Detect host architecture to confirm native vs cross-compile
      HOST_ARCH=$(uname -m)
      case "$HOST_ARCH" in
        x86_64)        HOST_PLATFORM="linux/amd64" ;;
        arm64|aarch64) HOST_PLATFORM="linux/arm64" ;;
        *)             HOST_PLATFORM="unknown" ;;
      esac

      TARGET_PLATFORM="${local.effective_platform}"

      # Skip --platform when host matches target (native build)
      if [ "$TARGET_PLATFORM" = "$HOST_PLATFORM" ]; then
        PLATFORM_ARG=""
        BUILD_MODE="native"
      else
        PLATFORM_ARG="--platform $TARGET_PLATFORM"
        BUILD_MODE="cross-compiling from $HOST_PLATFORM"
      fi

      echo "Building and pushing Docker image..."
      echo "Image: ${local.image_uri}"
      echo "Platform: $TARGET_PLATFORM ($BUILD_MODE)"
      echo "Git commit: ${local.git_commit}"

      docker buildx build \
        $PLATFORM_ARG \
        --network=host \
        --tag ${local.image_uri} \
        --build-arg GIT_COMMIT=${local.git_commit} \
        --build-arg BUILD_DATE=${local.timestamp} \
        --push \
        ${var.extra_build_args} \
        .

      echo "Docker image built and pushed successfully"
      echo "Image URI: ${local.image_uri}"
    EOT
  }
}

# Cleanup old images (optional)
resource "terraform_data" "docker_cleanup" {
  count = var.cleanup_old_images && !var.skip_docker_build ? 1 : 0

  triggers_replace = {
    build_id = terraform_data.docker_build[0].id
  }

  provisioner "local-exec" {
    command = <<-EOT
      set -e
      echo "🧹 Cleaning up old Docker images..."
      docker image prune -f --filter "until=24h"
      echo "✅ Cleanup complete"
    EOT
  }

  depends_on = [terraform_data.docker_build]
}
