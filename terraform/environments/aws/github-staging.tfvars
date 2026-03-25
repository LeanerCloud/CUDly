# AWS Staging Environment - GitHub Actions
# Used by CI/CD pipelines for automated deployments
# Sensitive values are provided via GitHub secrets

# ==============================================
# Project Settings
# ==============================================

project_name = "cudly"
environment  = "staging"
region       = "us-east-1"

# ==============================================
# Compute Platform
# ==============================================

compute_platform    = "lambda"
enable_docker_build = true # Build image via Terraform build module (no separate CI build step)

# Lambda Configuration
lambda_architecture           = "arm64"
lambda_memory_size            = 1024
lambda_timeout                = 60
lambda_reserved_concurrency   = -1
lambda_log_retention_days     = 14
lambda_enable_function_url    = true
lambda_function_url_auth_type = "NONE"
lambda_allowed_origins        = ["*"]

# Fargate Configuration (when compute_platform = "fargate")
fargate_cpu           = 1024
fargate_memory        = 2048
fargate_desired_count = 2
fargate_min_capacity  = 2
fargate_max_capacity  = 10

# ==============================================
# Database (RDS PostgreSQL)
# ==============================================

database_name                  = "cudly"
database_username              = "cudly"
database_engine_version        = "16.6"
database_backup_retention_days = 14
database_deletion_protection   = false
database_skip_final_snapshot   = true
database_performance_insights  = true
database_auto_migrate          = true

# ==============================================
# Networking
# ==============================================

vpc_cidr                 = "10.1.0.0/16"
az_count                 = 2
enable_flow_logs         = true
flow_logs_retention_days = 14

# ==============================================
# Secrets
# ==============================================

secret_recovery_window_days = 14

# ==============================================
# Frontend / CDN
# ==============================================

enable_cdn            = false
frontend_price_class  = "PriceClass_100"
create_subdomain_zone = false

# ==============================================
# Scheduled Tasks
# ==============================================

enable_scheduled_tasks  = true
recommendation_schedule = "rate(1 day)"

# ==============================================
# Variables provided by GitHub Actions:
#   TF_VAR_admin_email    = ${{ secrets.ADMIN_EMAIL }}
#   TF_VAR_image_uri      = (from build step)
# ==============================================
