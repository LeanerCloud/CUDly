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

    # Restrict to internal traffic only; external triggers go via Cloud Scheduler with OIDC
    ingress_settings = "ALLOW_INTERNAL_ONLY"

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
    vpc_connector                 = var.vpc_connector != "" ? var.vpc_connector : null
    vpc_connector_egress_settings = var.vpc_connector != "" ? "ALL_TRAFFIC" : null
  }

  labels = var.labels
}

# Storage bucket for function source (required for Cloud Functions)
resource "google_storage_bucket" "function_source" {
  project       = var.project_id
  name          = "${var.project_id}-${var.function_name}-source"
  location      = var.region
  force_destroy = false # Prevent accidental data loss; delete bucket contents manually before destroying

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
