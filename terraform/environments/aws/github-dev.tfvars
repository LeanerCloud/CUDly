# AWS Development Environment - GitHub Actions
# Used by CI/CD pipelines for automated deployments
# Sensitive values are provided via GitHub secrets

# ==============================================
# Project Settings
# ==============================================

project_name = "cudly"
environment  = "dev"
region       = "us-east-1"

# ==============================================
# Compute Platform
# ==============================================

compute_platform    = "lambda"
enable_docker_build = false # Use pre-built image from CI/CD pipeline

# Lambda Configuration
lambda_architecture           = "arm64"
lambda_memory_size            = 512
lambda_timeout                = 60
lambda_reserved_concurrency   = -1
lambda_log_retention_days     = 7
lambda_enable_function_url    = true
lambda_function_url_auth_type = "NONE"
lambda_allowed_origins        = ["*"]

# Fargate Configuration (when compute_platform = "fargate")
fargate_cpu           = 256
fargate_memory        = 512
fargate_desired_count = 1
fargate_min_capacity  = 1
fargate_max_capacity  = 5

# ==============================================
# Database (Aurora Serverless v2)
# ==============================================

database_name                  = "cudly"
database_username              = "cudly"
database_engine_version        = "16.6"
database_backup_retention_days = 7
database_deletion_protection   = false
database_skip_final_snapshot   = true
database_performance_insights  = false
database_auto_migrate          = true

# ==============================================
# Networking
# ==============================================

vpc_cidr         = "10.0.0.0/16"
az_count         = 2
enable_flow_logs = false

# ==============================================
# Secrets
# ==============================================

secret_recovery_window_days = 7

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
#   TF_VAR_subdomain_zone_name    = cudly.leanercloud.com
#   TF_VAR_frontend_domain_names  = ["lambda-dev.cudly.leanercloud.com"]
# ==============================================
