# GCP Staging Environment - GitHub Actions
# Used by CI/CD pipelines for automated deployments
# Sensitive values are provided via GitHub secrets

# ==============================================
# Project Settings
# ==============================================

project_name = "cudly"
environment  = "staging"
region       = "us-central1"

# ==============================================
# Compute Platform
# ==============================================

compute_platform    = "cloud-run"
enable_docker_build = false # Use pre-built image from CI/CD pipeline

# Cloud Run Configuration
cloud_run_cpu                   = "1"
cloud_run_memory                = "1Gi"
cloud_run_min_instances         = 1
cloud_run_max_instances         = 10
cloud_run_request_timeout       = 300
cloud_run_allow_unauthenticated = true
cloud_run_startup_cpu_boost     = true
cloud_run_cpu_throttling        = true

# ==============================================
# Database (Cloud SQL PostgreSQL)
# ==============================================

database_name                   = "cudly"
database_username               = "cudly"
database_version                = "POSTGRES_16"
database_tier                   = "db-custom-1-3840"
database_disk_size              = 20
database_disk_autoresize        = true
database_backup_enabled         = true
database_point_in_time_recovery = true
database_backup_retention_count = 14
database_query_insights         = true
database_deletion_protection    = true
database_auto_migrate           = true

# ==============================================
# Networking
# ==============================================

subnet_cidr           = "10.1.0.0/24"
connector_subnet_cidr = "10.9.0.0/28"
enable_nat_logging    = false

# ==============================================
# Frontend (Cloud CDN + Load Balancer)
# ==============================================

enable_frontend       = true
enable_frontend_build = true
enable_cloud_armor    = true

# ==============================================
# Scheduled Tasks
# ==============================================

enable_scheduled_tasks  = true
recommendation_schedule = "0 2 * * *"

# ==============================================
# Variables provided by GitHub Actions:
#   TF_VAR_project_id   = ${{ secrets.GCP_PROJECT_ID }}
#   TF_VAR_admin_email  = ${{ secrets.ADMIN_EMAIL }}
#   TF_VAR_image_uri    = (from build step)
# ==============================================
