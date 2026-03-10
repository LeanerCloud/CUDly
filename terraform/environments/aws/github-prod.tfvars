# AWS Production Environment - GitHub Actions
# Used by CI/CD pipelines for automated deployments
# Sensitive values are provided via GitHub secrets

# ==============================================
# Project Settings
# ==============================================

project_name = "cudly"
environment  = "prod"
region       = "us-east-1"

# ==============================================
# Compute Platform
# ==============================================

compute_platform    = "lambda"
enable_docker_build = false # Use pre-built image from CI/CD pipeline

# Lambda Configuration
lambda_architecture           = "arm64"
lambda_memory_size            = 1024
lambda_timeout                = 60
lambda_reserved_concurrency   = -1
lambda_log_retention_days     = 30
lambda_enable_function_url    = true
lambda_function_url_auth_type = "NONE"
# TODO: restrict to actual production domain, e.g. ["https://cudly.example.com"]
lambda_allowed_origins = ["*"]

# Fargate Configuration (when compute_platform = "fargate")
fargate_cpu           = 1024
fargate_memory        = 2048
fargate_desired_count = 3
fargate_min_capacity  = 2
fargate_max_capacity  = 20

# ==============================================
# Database (Aurora Serverless v2)
# ==============================================

database_name                  = "cudly"
database_username              = "cudly"
database_engine_version        = "16.6"
database_backup_retention_days = 30
database_deletion_protection   = true
database_skip_final_snapshot   = false
database_performance_insights  = true
database_auto_migrate          = false # Manual migrations in production

# ==============================================
# Networking
# ==============================================

vpc_cidr                 = "10.2.0.0/16"
az_count                 = 3
enable_flow_logs         = true
flow_logs_retention_days = 30

# ==============================================
# Secrets
# ==============================================

secret_recovery_window_days = 30

# ==============================================
# Frontend / CDN
# ==============================================

enable_cdn            = false
frontend_price_class  = "PriceClass_200"
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
