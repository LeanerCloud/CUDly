output "repository_url" {
  description = "URL of the Artifact Registry repository"
  value       = "${var.location}-docker.pkg.dev/${var.project_id}/${var.repository_id}"
}

output "repository_name" {
  description = "Full name of the repository"
  value       = google_artifact_registry_repository.main.name
}

output "repository_id" {
  description = "Repository ID"
  value       = google_artifact_registry_repository.main.repository_id
}
