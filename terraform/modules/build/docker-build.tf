# Docker Build and Push Automation Module
# Handles building and pushing Docker images before infrastructure deployment

terraform {
  required_version = ">= 1.5"
}

# Generate image tag from git commit + timestamp
resource "terraform_data" "image_tag" {
  input = var.custom_image_tag != "" ? var.custom_image_tag : "${local.git_commit}-${local.timestamp}"
}

locals {
  # Get git commit hash
  git_commit = var.skip_docker_build ? "skip" : trimspace(try(
    file("${path.root}/.git/HEAD") != "" ? (
      can(regex("^ref:", file("${path.root}/.git/HEAD"))) ?
        substr(file("${path.root}/.git/${trimspace(replace(file("${path.root}/.git/HEAD"), "ref: ", ""))}"), 0, 7) :
        substr(file("${path.root}/.git/HEAD"), 0, 7)
    ) : "unknown",
    "unknown"
  ))

  # Generate timestamp
  timestamp = var.skip_docker_build ? "skip" : formatdate("YYYYMMDDhhmmss", timestamp())

  # Final image tag
  image_tag = terraform_data.image_tag.output

  # Full image URI
  image_uri = "${var.registry_url}/${var.image_name}:${local.image_tag}"
}

# Docker build
resource "terraform_data" "docker_build" {
  count = var.skip_docker_build ? 0 : 1

  triggers_replace = {
    # Rebuild when source code changes
    go_mod      = fileexists("${var.source_path}/go.mod") ? filemd5("${var.source_path}/go.mod") : "none"
    go_sum      = fileexists("${var.source_path}/go.sum") ? filemd5("${var.source_path}/go.sum") : "none"
    dockerfile  = fileexists("${var.source_path}/Dockerfile") ? filemd5("${var.source_path}/Dockerfile") : "none"
    # Hash all Go files in cmd and pkg directories
    cmd_files   = try(sha256(join("", [for f in fileset("${var.source_path}/cmd", "**/*.go") : filemd5("${var.source_path}/cmd/${f}")])), "none")
    pkg_files   = try(sha256(join("", [for f in fileset("${var.source_path}/pkg", "**/*.go") : filemd5("${var.source_path}/pkg/${f}")])), "none")

    # Force rebuild with image tag
    image_tag   = local.image_tag
  }

  provisioner "local-exec" {
    working_dir = var.source_path
    command     = <<-EOT
      echo "🔨 Building Docker image..."
      echo "Image: ${local.image_uri}"
      echo "Platform: ${var.platform}"
      echo "Git commit: ${local.git_commit}"

      docker buildx build \
        --platform ${var.platform} \
        --tag ${local.image_uri} \
        --build-arg GIT_COMMIT=${local.git_commit} \
        --build-arg BUILD_DATE=${local.timestamp} \
        ${var.load_image ? "--load" : ""} \
        ${var.extra_build_args} \
        .

      echo "✅ Docker image built successfully"
    EOT
  }
}

# Registry login
# IMPORTANT: Re-login before EVERY push since ECR tokens expire after 12 hours
resource "terraform_data" "registry_login" {
  count = var.skip_docker_build || var.skip_docker_push ? 0 : 1

  triggers_replace = {
    # Re-login whenever we're about to push (build ID changes)
    build_id = terraform_data.docker_build[0].id
    registry = var.registry_url
  }

  provisioner "local-exec" {
    command = var.registry_login_command
  }

  depends_on = [terraform_data.docker_build]
}

# Docker push
resource "terraform_data" "docker_push" {
  count = var.skip_docker_build || var.skip_docker_push ? 0 : 1

  triggers_replace = {
    # Push when build changes
    build_id = terraform_data.docker_build[0].id
  }

  provisioner "local-exec" {
    command = <<-EOT
      echo "📤 Pushing Docker image to registry..."
      echo "Image: ${local.image_uri}"

      docker push ${local.image_uri}

      echo "✅ Docker image pushed successfully"
      echo "Image URI: ${local.image_uri}"
    EOT
  }

  depends_on = [
    terraform_data.docker_build,
    terraform_data.registry_login
  ]
}

# Cleanup old images (optional)
resource "terraform_data" "docker_cleanup" {
  count = var.cleanup_old_images && !var.skip_docker_build ? 1 : 0

  triggers_replace = {
    # Run after push
    push_id = var.skip_docker_push ? "skip" : terraform_data.docker_push[0].id
  }

  provisioner "local-exec" {
    command = <<-EOT
      echo "🧹 Cleaning up old Docker images..."
      docker image prune -f --filter "until=24h"
      echo "✅ Cleanup complete"
    EOT
  }

  depends_on = [terraform_data.docker_push]
}
