# Docker Build Module Outputs

output "image_uri" {
  description = "Full Docker image URI"
  value       = local.image_uri
}

output "image_tag" {
  description = "Docker image tag"
  value       = local.image_tag
}

output "git_commit" {
  description = "Git commit hash used for tagging"
  value       = local.git_commit
}

output "registry_url" {
  description = "Docker registry URL"
  value       = var.registry_url
}

output "image_name" {
  description = "Docker image name"
  value       = var.image_name
}
