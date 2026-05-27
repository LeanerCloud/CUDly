# GCP Development Environment - GitHub Actions
# Used by CI/CD pipelines for automated deployments
# Sensitive values are provided via GitHub secrets

# ==============================================
# Project Settings
# ==============================================

project_name = "cudly"
environment  = "dev"
region       = "us-central1"

# ==============================================
# Compute Platform
# ==============================================

compute_platform    = "cloud-run"
enable_docker_build = true # Build and push image via terraform apply on the runner

# Cloud Run Configuration
cloud_run_cpu             = "1"
cloud_run_memory          = "512Mi"
cloud_run_min_instances   = 0
cloud_run_max_instances   = 10
cloud_run_request_timeout = 300
# github-dev: enable_cdn = false means no external HTTPS LB is provisioned
# yet, so direct *.run.app traffic must still be accepted — override the
# secure ingress default until the LB stack (enable_cdn = true + DNS + cert)
# lands. Once enable_cdn flips to true, remove this line so
# INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER takes effect. See issues #78 + #384.
# (allow_unauthenticated is no longer an operator-facing tfvar — it is derived
# from enable_cdn in compute.tf so the IAM gate and ingress door flip in lock-
# step with the LB stack landing.)
cloud_run_ingress = "INGRESS_TRAFFIC_ALL"

# ==============================================
# Database (Cloud SQL PostgreSQL)
# ==============================================

database_name                   = "cudly"
database_username               = "cudly"
database_version                = "POSTGRES_16"
database_tier                   = "db-custom-1-3840"
database_disk_size              = 10
database_disk_autoresize        = true
database_backup_enabled         = true
database_point_in_time_recovery = false
database_backup_retention_count = 7
database_query_insights         = false
database_deletion_protection    = false

# ==============================================
# Networking
# ==============================================

subnet_cidr           = "10.0.0.0/24"
connector_subnet_cidr = "10.8.0.0/28"
enable_nat_logging    = false

# ==============================================
# Scheduled Tasks (Cloud Scheduler)
# ==============================================

enable_scheduled_tasks  = true
recommendation_schedule = "0 2 * * *"

# ==============================================
# Database Migration
# ==============================================

auto_migrate = true

# ==============================================
# Frontend (Load Balancer)
# ==============================================

enable_cdn         = false
enable_cloud_armor = false

# ==============================================
# Variables provided by GitHub Actions:
#   TF_VAR_project_id   = ${{ secrets.GCP_PROJECT_ID }}
#   TF_VAR_admin_email  = ${{ secrets.ADMIN_EMAIL }}
#   TF_VAR_image_uri    = (from build step)
# ==============================================
