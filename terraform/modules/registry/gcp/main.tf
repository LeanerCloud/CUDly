# Artifact Registry Repository
resource "google_artifact_registry_repository" "main" {
  project       = var.project_id
  location      = var.location
  repository_id = var.repository_id
  description   = "Docker container registry for CUDly application"
  format        = "DOCKER"

  labels = var.labels

  # Keep the N most recent tagged versions (takes precedence over delete policies)
  cleanup_policies {
    id     = "keep-recent-versions"
    action = "KEEP"

    most_recent_versions {
      keep_count = var.keep_image_count
    }
  }

  # Delete old tagged images (keep-recent-versions takes precedence)
  cleanup_policies {
    id     = "delete-old-tagged"
    action = "DELETE"

    condition {
      tag_state  = "TAGGED"
      older_than = "${var.tagged_expiry_days * 86400}s"
    }
  }

  # Delete untagged images after a short period
  cleanup_policies {
    id     = "delete-untagged"
    action = "DELETE"

    condition {
      tag_state  = "UNTAGGED"
      older_than = "${var.untagged_expiry_days * 86400}s"
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
