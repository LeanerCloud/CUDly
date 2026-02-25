# GCP Secret Manager Module
# Manages application secrets with automatic replication

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }
}

# ==============================================
# Database Password Secret
# ==============================================

resource "random_password" "database" {
  count = var.database_password == null ? 1 : 0

  length  = 32
  special = true
}

resource "google_secret_manager_secret" "database_password" {
  secret_id = "${var.service_name}-db-password"
  project   = var.project_id

  replication {
    auto {}
  }

  labels = merge(var.labels, {
    environment = var.environment
    managed_by  = "terraform"
  })
}

resource "google_secret_manager_secret_version" "database_password" {
  secret      = google_secret_manager_secret.database_password.id
  secret_data = var.database_password != null ? var.database_password : random_password.database[0].result
}

# ==============================================
# Application Secrets
# ==============================================

# JWT signing secret
resource "random_password" "jwt_secret" {
  count = var.create_jwt_secret ? 1 : 0

  length  = 64
  special = false # Base64-friendly
}

resource "google_secret_manager_secret" "jwt_secret" {
  count = var.create_jwt_secret ? 1 : 0

  secret_id = "${var.service_name}-jwt-secret"
  project   = var.project_id

  replication {
    auto {}
  }

  labels = merge(var.labels, {
    environment = var.environment
    managed_by  = "terraform"
  })
}

resource "google_secret_manager_secret_version" "jwt_secret" {
  count = var.create_jwt_secret ? 1 : 0

  secret      = google_secret_manager_secret.jwt_secret[0].id
  secret_data = random_password.jwt_secret[0].result
}

# Session encryption secret
resource "random_password" "session_secret" {
  count = var.create_session_secret ? 1 : 0

  length  = 64
  special = false # Base64-friendly
}

resource "google_secret_manager_secret" "session_secret" {
  count = var.create_session_secret ? 1 : 0

  secret_id = "${var.service_name}-session-secret"
  project   = var.project_id

  replication {
    auto {}
  }

  labels = merge(var.labels, {
    environment = var.environment
    managed_by  = "terraform"
  })
}

resource "google_secret_manager_secret_version" "session_secret" {
  count = var.create_session_secret ? 1 : 0

  secret      = google_secret_manager_secret.session_secret[0].id
  secret_data = random_password.session_secret[0].result
}

# SendGrid API Key (for email)
resource "google_secret_manager_secret" "sendgrid_api_key" {
  count = var.sendgrid_api_key != null || var.create_sendgrid_secret ? 1 : 0

  secret_id = "${var.service_name}-sendgrid-api-key"
  project   = var.project_id

  replication {
    auto {}
  }

  labels = merge(var.labels, {
    environment = var.environment
    managed_by  = "terraform"
  })
}

resource "google_secret_manager_secret_version" "sendgrid_api_key" {
  count = var.sendgrid_api_key != null || var.create_sendgrid_secret ? 1 : 0

  secret      = google_secret_manager_secret.sendgrid_api_key[0].id
  secret_data = var.sendgrid_api_key != null ? var.sendgrid_api_key : "PLACEHOLDER_REPLACE_ME"
}

# ==============================================
# Scheduled Task Secret
# ==============================================

resource "random_password" "scheduled_task_secret" {
  count = var.create_scheduled_task_secret ? 1 : 0

  length  = 64
  special = false
}

resource "google_secret_manager_secret" "scheduled_task_secret" {
  count = var.create_scheduled_task_secret ? 1 : 0

  secret_id = "${var.service_name}-scheduled-task-secret"
  project   = var.project_id

  replication {
    auto {}
  }

  labels = merge(var.labels, {
    environment = var.environment
    managed_by  = "terraform"
  })
}

resource "google_secret_manager_secret_version" "scheduled_task_secret" {
  count = var.create_scheduled_task_secret ? 1 : 0

  secret      = google_secret_manager_secret.scheduled_task_secret[0].id
  secret_data = random_password.scheduled_task_secret[0].result
}

resource "google_secret_manager_secret_iam_member" "cloud_run_scheduled_task" {
  count = var.create_scheduled_task_secret && var.cloud_run_service_account_email != null ? 1 : 0

  project   = var.project_id
  secret_id = google_secret_manager_secret.scheduled_task_secret[0].id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${var.cloud_run_service_account_email}"
}

# ==============================================
# Additional Custom Secrets
# ==============================================

resource "google_secret_manager_secret" "additional" {
  for_each = var.additional_secrets

  secret_id = "${var.service_name}-${each.key}"
  project   = var.project_id

  replication {
    auto {}
  }

  labels = merge(var.labels, {
    environment = var.environment
    managed_by  = "terraform"
  })
}

resource "google_secret_manager_secret_version" "additional" {
  for_each = var.additional_secrets

  secret      = google_secret_manager_secret.additional[each.key].id
  secret_data = each.value
}

# ==============================================
# IAM Permissions for Cloud Run Service Account
# ==============================================

# Grant Cloud Run service account access to read secrets
resource "google_secret_manager_secret_iam_member" "cloud_run_db_password" {
  count = var.cloud_run_service_account_email != null ? 1 : 0

  project   = var.project_id
  secret_id = google_secret_manager_secret.database_password.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${var.cloud_run_service_account_email}"
}

resource "google_secret_manager_secret_iam_member" "cloud_run_jwt" {
  count = var.create_jwt_secret && var.cloud_run_service_account_email != null ? 1 : 0

  project   = var.project_id
  secret_id = google_secret_manager_secret.jwt_secret[0].id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${var.cloud_run_service_account_email}"
}

resource "google_secret_manager_secret_iam_member" "cloud_run_session" {
  count = var.create_session_secret && var.cloud_run_service_account_email != null ? 1 : 0

  project   = var.project_id
  secret_id = google_secret_manager_secret.session_secret[0].id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${var.cloud_run_service_account_email}"
}

resource "google_secret_manager_secret_iam_member" "cloud_run_sendgrid" {
  count = (var.sendgrid_api_key != null || var.create_sendgrid_secret) && var.cloud_run_service_account_email != null ? 1 : 0

  project   = var.project_id
  secret_id = google_secret_manager_secret.sendgrid_api_key[0].id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${var.cloud_run_service_account_email}"
}

resource "google_secret_manager_secret_iam_member" "cloud_run_additional" {
  for_each = var.cloud_run_service_account_email != null ? var.additional_secrets : {}

  project   = var.project_id
  secret_id = google_secret_manager_secret.additional[each.key].id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${var.cloud_run_service_account_email}"
}
