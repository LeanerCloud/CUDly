variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "location" {
  description = "Repository location (e.g., us-central1)"
  type        = string
  default     = "us-central1"
}

variable "repository_id" {
  description = "Repository ID"
  type        = string
}

variable "keep_image_count" {
  description = "Number of recent versions to keep"
  type        = number
  default     = 10
}

variable "tagged_expiry_days" {
  description = "Days after which old tagged images are deleted"
  type        = number
  default     = 30
}

variable "untagged_expiry_days" {
  description = "Days after which untagged images are deleted"
  type        = number
  default     = 7
}

variable "labels" {
  description = "Labels to apply to all resources"
  type        = map(string)
  default     = {}
}

# Artifact Registry Repository
resource "google_artifact_registry_repository" "main" {
  project       = var.project_id
  location      = var.location
  repository_id = var.repository_id
  description   = "Docker container registry for CUDly application"
  format        = "DOCKER"

  labels = var.labels

  # Cleanup policy for tagged images
  cleanup_policies {
    id     = "keep-recent-versions"
    action = "DELETE"

    condition {
      tag_state  = "TAGGED"
      older_than = "${var.tagged_expiry_days * 86400}s" # Convert days to seconds
    }

    most_recent_versions {
      keep_count = var.keep_image_count
    }
  }

  # Cleanup policy for untagged images
  cleanup_policies {
    id     = "delete-untagged"
    action = "DELETE"

    condition {
      tag_state  = "UNTAGGED"
      older_than = "${var.untagged_expiry_days * 86400}s" # Convert days to seconds
    }
  }

  # Cleanup policy to prevent unbounded growth
  cleanup_policies {
    id     = "delete-very-old"
    action = "DELETE"

    condition {
      tag_state  = "ANY"
      older_than = "${90 * 86400}s" # Delete anything older than 90 days
    }
  }
}

# IAM binding to allow Cloud Run to pull images
resource "google_artifact_registry_repository_iam_member" "cloud_run_pull" {
  project    = google_artifact_registry_repository.main.project
  location   = google_artifact_registry_repository.main.location
  repository = google_artifact_registry_repository.main.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${var.project_id}@appspot.gserviceaccount.com"
}

# Outputs
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
