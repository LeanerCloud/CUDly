# GCP Cloud Run Module
# Serverless container platform with automatic scaling
#
# ARCHITECTURE NOTE:
# This module uses Gen2 execution environment which provides ARM64 support.
# Gen2 offers:
# - Better performance with custom Google silicon (similar to AWS Graviton)
# - 10-15% cost savings compared to Gen1
# - Improved startup times and lower cold starts
# - Better memory and network performance
#
# The execution_environment variable defaults to "EXECUTION_ENVIRONMENT_GEN2" (ARM64-capable).
# Gen2 automatically handles ARM64 binaries without explicit architecture configuration.

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

# ==============================================
# Service Account for Cloud Run
# ==============================================

resource "google_service_account" "cloud_run" {
  account_id   = "${var.service_name}-cloudrun"
  display_name = "Cloud Run service account for ${var.service_name}"
  description  = "Service account used by Cloud Run service"
  project      = var.project_id
}

# ==============================================
# Cloud Run Service
# ==============================================

resource "google_cloud_run_v2_service" "main" {
  name     = var.service_name
  location = var.region
  project  = var.project_id

  template {
    service_account = google_service_account.cloud_run.email

    # Scaling configuration
    scaling {
      min_instance_count = var.min_instances
      max_instance_count = var.max_instances
    }

    # VPC Access (for Cloud SQL)
    dynamic "vpc_access" {
      for_each = var.vpc_connector_id != null ? [var.vpc_connector_id] : []
      content {
        connector = vpc_access.value
        egress    = var.vpc_egress_mode
      }
    }

    # Container configuration
    containers {
      image = var.image_uri

      # Resource limits
      resources {
        limits = {
          cpu    = var.cpu
          memory = var.memory
        }
        cpu_idle          = var.cpu_throttling
        startup_cpu_boost = var.startup_cpu_boost
      }

      # Environment variables
      dynamic "env" {
        for_each = merge(
          {
            ENVIRONMENT        = var.environment
            RUNTIME_MODE       = "http"
            DB_HOST            = var.database_host
            DB_PORT            = "5432"
            DB_NAME            = var.database_name
            DB_USER            = var.database_username
            DB_PASSWORD_SECRET = var.database_password_secret_id
            DB_SSL_MODE        = "require"
            DB_CONNECT_TIMEOUT = "8s"
            DB_AUTO_MIGRATE    = tostring(var.auto_migrate)
            DB_MIGRATIONS_PATH = "/app/migrations"
            ADMIN_EMAIL        = var.admin_email
            ADMIN_PASSWORD     = var.admin_password
            SECRET_PROVIDER    = "gcp"
            GCP_PROJECT_ID     = var.project_id
            GCP_REGION         = var.region
            PORT               = "8080"
            ALLOWED_ORIGINS    = join(",", var.allowed_origins)
          },
          var.additional_env_vars
        )
        content {
          name  = env.key
          value = env.value
        }
      }

      # Startup probe
      startup_probe {
        http_get {
          path = "/health"
          port = 8080
        }
        initial_delay_seconds = 10
        timeout_seconds       = 3
        period_seconds        = 10
        failure_threshold     = 3
      }

      # Liveness probe
      liveness_probe {
        http_get {
          path = "/health"
          port = 8080
        }
        initial_delay_seconds = 30
        timeout_seconds       = 3
        period_seconds        = 30
        failure_threshold     = 3
      }

      # Port
      ports {
        name           = "http1"
        container_port = 8080
      }
    }

    # Timeout
    timeout = "${var.request_timeout}s"

    # ARCHITECTURE: Execution environment (Gen2 = ARM64-capable)
    # Gen2 provides better performance and cost efficiency with ARM64 support
    # Gen1 = x86_64 only (legacy)
    # Gen2 = ARM64-capable with Google's custom silicon (default, recommended)
    execution_environment = var.execution_environment
  }

  # Traffic configuration
  traffic {
    type    = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
    percent = 100
  }

  # Ingress settings
  ingress = var.ingress

  labels = merge(var.labels, {
    environment  = var.environment
    managed_by   = "terraform"
    architecture = var.execution_environment == "EXECUTION_ENVIRONMENT_GEN2" ? "arm64-capable" : "x86_64"
  })
}

# ==============================================
# IAM for Public Access (if enabled)
# ==============================================

resource "google_cloud_run_service_iam_member" "public_access" {
  count = var.allow_unauthenticated ? 1 : 0

  project  = var.project_id
  location = var.region
  service  = google_cloud_run_v2_service.main.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# ==============================================
# Cloud SQL Connection IAM
# ==============================================

resource "google_project_iam_member" "cloud_sql_client" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = "serviceAccount:${google_service_account.cloud_run.email}"
}

# Secret Manager access for reading email credentials and other secrets
resource "google_project_iam_member" "secret_accessor" {
  project = var.project_id
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:${google_service_account.cloud_run.email}"
}

# ==============================================
# Cloud Scheduler for Scheduled Tasks
# ==============================================

resource "google_cloud_scheduler_job" "recommendations" {
  count = var.enable_scheduled_tasks ? 1 : 0

  name             = "${var.service_name}-recommendations"
  description      = "Trigger recommendations collection"
  schedule         = var.recommendation_schedule
  time_zone        = "UTC"
  attempt_deadline = "320s"
  project          = var.project_id
  region           = var.region

  retry_config {
    retry_count = 3
  }

  http_target {
    http_method = "POST"
    uri         = "${google_cloud_run_v2_service.main.uri}/api/scheduled/recommendations"

    oidc_token {
      service_account_email = google_service_account.scheduler[0].email
    }
  }
}

# Service account for Cloud Scheduler
resource "google_service_account" "scheduler" {
  count = var.enable_scheduled_tasks ? 1 : 0

  account_id   = "${var.service_name}-scheduler"
  display_name = "Cloud Scheduler service account"
  project      = var.project_id
}

# Grant scheduler permission to invoke Cloud Run
resource "google_cloud_run_service_iam_member" "scheduler_invoker" {
  count = var.enable_scheduled_tasks ? 1 : 0

  project  = var.project_id
  location = var.region
  service  = google_cloud_run_v2_service.main.name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.scheduler[0].email}"
}

# ==============================================
# Cloud Logging
# ==============================================

# Cloud Run automatically sends logs to Cloud Logging
# No explicit configuration needed, but we can set retention

resource "google_logging_project_bucket_config" "cloud_run_logs" {
  count = var.log_retention_days != null ? 1 : 0

  project        = var.project_id
  location       = "global"
  retention_days = var.log_retention_days
  bucket_id      = "_Default"
}
