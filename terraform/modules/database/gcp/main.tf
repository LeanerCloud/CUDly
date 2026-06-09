# GCP Cloud SQL PostgreSQL Module
# Serverless-compatible PostgreSQL database with high availability

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
# Database Password Generation
# ==============================================

resource "random_password" "database" {
  count = var.generate_password ? 1 : 0

  length           = 32
  special          = true
  override_special = "!#$%&*()-_=+[]{}<>:?"
}

# ==============================================
# Cloud SQL Instance
# ==============================================

resource "google_sql_database_instance" "main" {
  name             = "${var.service_name}-postgres"
  database_version = var.database_version
  region           = var.region
  project          = var.project_id

  # Deletion protection
  deletion_protection = var.deletion_protection

  settings {
    # Tier determines CPU/RAM (db-custom-[CPUs]-[RAM_MB])
    # For dev: db-custom-1-3840 (1 vCPU, 3.75 GB)
    # For prod: db-custom-2-7680 or higher
    tier              = var.tier
    availability_type = var.high_availability ? "REGIONAL" : "ZONAL"

    user_labels = {
      environment = var.environment
    }
    disk_type       = var.disk_type
    disk_size       = var.disk_size
    disk_autoresize = var.disk_autoresize

    # Automatic storage increase limit
    disk_autoresize_limit = var.disk_autoresize_limit

    # Backup configuration
    backup_configuration {
      enabled                        = var.backup_enabled
      start_time                     = var.backup_start_time
      point_in_time_recovery_enabled = var.point_in_time_recovery
      transaction_log_retention_days = var.transaction_log_retention_days
      backup_retention_settings {
        retained_backups = var.backup_retention_count
        retention_unit   = "COUNT"
      }
    }

    # Maintenance window
    maintenance_window {
      day          = var.maintenance_window_day
      hour         = var.maintenance_window_hour
      update_track = var.maintenance_update_track
    }

    # IP configuration
    ip_configuration {
      ipv4_enabled    = var.enable_public_ip
      private_network = var.vpc_network_id
      ssl_mode        = "ENCRYPTED_ONLY"

      # Authorized networks (if public IP is enabled)
      dynamic "authorized_networks" {
        for_each = var.authorized_networks
        content {
          name  = authorized_networks.value.name
          value = authorized_networks.value.cidr
        }
      }
    }

    # Database flags
    dynamic "database_flags" {
      for_each = var.database_flags
      content {
        name  = database_flags.value.name
        value = database_flags.value.value
      }
    }

    # Insights configuration
    insights_config {
      query_insights_enabled  = var.query_insights_enabled
      query_string_length     = var.query_string_length
      record_application_tags = var.record_application_tags
      record_client_address   = var.record_client_address
    }
  }

  # Lifecycle
  lifecycle {
    ignore_changes = [
      settings[0].disk_size, # Allow auto-resize
    ]
  }
}

# ==============================================
# Cloud SQL Database
# ==============================================

resource "google_sql_database" "main" {
  name     = var.database_name
  instance = google_sql_database_instance.main.name
  project  = var.project_id
}

# ==============================================
# Cloud SQL User
# ==============================================

resource "google_sql_user" "main" {
  name     = var.master_username
  instance = google_sql_database_instance.main.name
  password = var.master_password != null ? var.master_password : random_password.database[0].result
  project  = var.project_id
}

# ==============================================
# Cloud SQL IAM User (for Cloud Run service account)
# ==============================================

resource "google_sql_user" "iam_service_account" {
  count = var.enable_iam_authentication ? 1 : 0

  name     = var.cloud_run_service_account_email
  instance = google_sql_database_instance.main.name
  type     = "CLOUD_IAM_SERVICE_ACCOUNT"
  project  = var.project_id
}

# ==============================================
# Read Replica (Optional)
# ==============================================

resource "google_sql_database_instance" "read_replica" {
  count = var.enable_read_replica ? 1 : 0

  name                 = "${var.service_name}-postgres-replica"
  master_instance_name = google_sql_database_instance.main.name
  database_version     = var.database_version
  region               = var.replica_region != null ? var.replica_region : var.region
  project              = var.project_id

  replica_configuration {
    failover_target = false
  }

  settings {
    tier              = var.replica_tier != null ? var.replica_tier : var.tier
    availability_type = "ZONAL"
    disk_type         = var.disk_type
    disk_size         = var.disk_size
    disk_autoresize   = var.disk_autoresize

    ip_configuration {
      ipv4_enabled    = var.enable_public_ip
      private_network = var.vpc_network_id
      ssl_mode        = "ENCRYPTED_ONLY"
    }

    insights_config {
      query_insights_enabled = var.query_insights_enabled
    }
  }
}
