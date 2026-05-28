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
enable_docker_build = true # Build image via Terraform build module (no separate CI build step)

# Lambda Configuration
lambda_memory_size          = 1024
lambda_timeout              = 300
lambda_reserved_concurrency = -1
lambda_log_retention_days   = 30
# Function URL auth_type is derived from enable_cdn (local in compute.tf):
#   enable_cdn = false -> NONE  (direct browser hits, app-layer auth)
#   enable_cdn = true  -> AWS_IAM (CloudFront OAC signs every request)
# Prod is not yet provisioned. Before the first terraform apply:
#   1. Replace the .invalid placeholder with the actual prod CloudFront domain
#      (from `terraform output cloudfront_domain_name` after apply), or the
#      customer-facing custom domain set in TF_VAR_frontend_domain_names.
#   2. Set TF_VAR_frontend_domain_names to the customer-facing domain (e.g.
#      "app.cudly.io") so the CloudFront distribution aliases are correct.
# The .invalid TLD ensures any accidental apply fails fast on hostname resolution.
lambda_allowed_origins = ["https://prod-not-yet-deployed.invalid"]

# Fargate Configuration (when compute_platform = "fargate")
fargate_cpu           = 1024
fargate_memory        = 2048
fargate_desired_count = 3
fargate_min_capacity  = 2
fargate_max_capacity  = 20

# ==============================================
# Database (RDS PostgreSQL)
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

enable_cdn            = true
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
