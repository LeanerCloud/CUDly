variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
}

variable "function_name" {
  description = "Name of the Cloud Function"
  type        = string
  default     = "cudly-cleanup"
}

variable "image_uri" {
  description = "Container image URI for the cleanup function"
  type        = string
}

variable "db_host" {
  description = "Database host (Cloud SQL connection name or private IP)"
  type        = string
}

variable "db_password_secret_id" {
  description = "Secret Manager secret ID containing the database password"
  type        = string
}

variable "vpc_connector" {
  description = "VPC connector for private Cloud SQL access"
  type        = string
  default     = ""
}

variable "schedule" {
  description = "Cloud Scheduler schedule (cron format)"
  type        = string
  default     = "0 2 * * *"
}

variable "labels" {
  description = "Labels to apply to all resources"
  type        = map(string)
  default     = {}
}

# Service account for Cloud Function
resource "google_service_account" "cleanup" {
  project      = var.project_id
  account_id   = "${var.function_name}-sa"
  display_name = "Service account for ${var.function_name}"
}

# Grant access to Secret Manager
resource "google_project_iam_member" "cleanup_secrets" {
  project = var.project_id
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:${google_service_account.cleanup.email}"
}

# Cloud Function (2nd gen)
resource "google_cloudfunctions2_function" "cleanup" {
  name     = var.function_name
  location = var.region
  project  = var.project_id

  build_config {
    runtime     = "go121"
    entry_point = "cleanupExpiredRecords"

    # Use pre-built container image
    source {
      storage_source {
        bucket = google_storage_bucket.function_source.name
        object = google_storage_bucket_object.function_source.name
      }
    }
  }

  service_config {
    max_instance_count    = 1
    timeout_seconds       = 300
    service_account_email = google_service_account.cleanup.email

    environment_variables = {
      DB_HOST            = var.db_host
      DB_PORT            = "5432"
      DB_NAME            = "cudly"
      DB_USER            = "cudly"
      DB_PASSWORD_SECRET = var.db_password_secret_id
      DB_SSL_MODE        = "require"
      SECRET_PROVIDER    = "gcp"
      GCP_PROJECT_ID     = var.project_id
    }

    # VPC connector for private Cloud SQL access
    dynamic "vpc_connector" {
      for_each = var.vpc_connector != "" ? [1] : []
      content {
        name = var.vpc_connector
      }
    }
  }

  labels = var.labels
}

# Storage bucket for function source (required for Cloud Functions)
resource "google_storage_bucket" "function_source" {
  project       = var.project_id
  name          = "${var.project_id}-${var.function_name}-source"
  location      = var.region
  force_destroy = true

  uniform_bucket_level_access = true
}

# Placeholder source object (will be replaced by actual deployment)
resource "google_storage_bucket_object" "function_source" {
  name   = "cleanup-${formatdate("YYYYMMDDhhmmss", timestamp())}.zip"
  bucket = google_storage_bucket.function_source.name
  source = "${path.module}/placeholder.zip"
}

# Cloud Scheduler job to trigger the function
resource "google_cloud_scheduler_job" "cleanup" {
  project          = var.project_id
  region           = var.region
  name             = "${var.function_name}-schedule"
  description      = "Trigger cleanup of expired sessions and executions"
  schedule         = var.schedule
  time_zone        = "UTC"
  attempt_deadline = "320s"

  http_target {
    http_method = "POST"
    uri         = google_cloudfunctions2_function.cleanup.service_config[0].uri

    body = base64encode(jsonencode({
      dryRun = false
    }))

    headers = {
      "Content-Type" = "application/json"
    }

    oidc_token {
      service_account_email = google_service_account.cleanup.email
    }
  }

  retry_config {
    retry_count = 1
  }
}

# Cloud Function invoker permission for Cloud Scheduler
resource "google_cloudfunctions2_function_iam_member" "cleanup_invoker" {
  project        = google_cloudfunctions2_function.cleanup.project
  location       = google_cloudfunctions2_function.cleanup.location
  cloud_function = google_cloudfunctions2_function.cleanup.name
  role           = "roles/cloudfunctions.invoker"
  member         = "serviceAccount:${google_service_account.cleanup.email}"
}

# Outputs
output "function_uri" {
  description = "URI of the Cloud Function"
  value       = google_cloudfunctions2_function.cleanup.service_config[0].uri
}

output "function_name" {
  description = "Name of the Cloud Function"
  value       = google_cloudfunctions2_function.cleanup.name
}

output "schedule" {
  description = "Cloud Scheduler schedule"
  value       = var.schedule
}

output "service_account_email" {
  description = "Service account email for the cleanup function"
  value       = google_service_account.cleanup.email
}
