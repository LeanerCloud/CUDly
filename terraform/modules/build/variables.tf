# Docker Build Module Variables

variable "registry_url" {
  description = "Docker registry URL (e.g., 123456789012.dkr.ecr.us-east-1.amazonaws.com)"
  type        = string
}

variable "image_name" {
  description = "Docker image name (e.g., cudly-lambda-dev)"
  type        = string
}

variable "source_path" {
  description = "Path to source code directory containing Dockerfile"
  type        = string
  default     = "../../../.."
}

variable "platform" {
  description = "Target platform for Docker build (e.g. linux/amd64, linux/arm64). Empty string uses native platform."
  type        = string
  default     = ""
}

variable "custom_image_tag" {
  description = "Custom image tag (defaults to git-commit-timestamp)"
  type        = string
  default     = ""
}

variable "skip_docker_build" {
  description = "Skip Docker build (useful for infrastructure-only changes)"
  type        = bool
  default     = false
}

variable "extra_build_args" {
  description = "Extra arguments to pass to docker build"
  type        = string
  default     = ""
}

variable "registry_login_command" {
  description = "Command to authenticate with registry (e.g., aws ecr get-login-password | docker login...)"
  type        = string
}

variable "cleanup_old_images" {
  description = "Clean up old Docker images after push"
  type        = bool
  default     = true
}
