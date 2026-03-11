# Azure Production Environment - GitHub Actions
# Used by CI/CD pipelines for automated deployments
# Sensitive values are provided via GitHub secrets

# ==============================================
# Project Settings
# ==============================================

project_name = "cudly"
environment  = "prod"
location     = "eastus"
cost_center  = "engineering"

# ==============================================
# Compute Platform
# ==============================================

compute_platform    = "container-apps"
enable_docker_build = false # Use pre-built image from CI/CD pipeline

# Container Apps Configuration
container_cpu            = 2.0
container_memory         = "4.0Gi"
min_replicas             = 2
max_replicas             = 20
external_ingress_enabled = true

# ==============================================
# Database (Azure Flexible Server PostgreSQL)
# ==============================================

postgres_version                = "16"
database_sku_name               = "GP_Standard_D2s_v3"
database_storage_mb             = 131072
database_backup_retention_days  = 35
database_geo_redundant_backup   = true
database_high_availability_mode = "ZoneRedundant"
auto_migrate                    = false # Manual migrations in production

# ==============================================
# Networking
# ==============================================

vnet_cidr                  = "10.2.0.0/16"
container_apps_subnet_cidr = "10.2.1.0/24"
database_subnet_cidr       = "10.2.2.0/24"

# ==============================================
# Key Vault
# ==============================================

key_vault_sku              = "standard"
soft_delete_retention_days = 90
purge_protection_enabled   = true

# ==============================================
# Scheduled Tasks
# ==============================================

enable_scheduled_tasks  = true
recommendation_schedule = "0 2 * * *"

# ==============================================
# Logging
# ==============================================

log_retention_days = 90

# ==============================================
# Frontend (Azure CDN)
# ==============================================

enable_cdn = false

# ==============================================
# Email Service
# ==============================================

enable_email_service           = true
email_use_azure_managed_domain = false # Use custom domain in production

# ==============================================
# Tags
# ==============================================

tags = {
  "ManagedBy"   = "Terraform"
  "Project"     = "CUDly"
  "Environment" = "production"
}

# ==============================================
# Variables provided by GitHub Actions:
#   TF_VAR_subscription_id        = ${{ secrets.AZURE_SUBSCRIPTION_ID }}
#   TF_VAR_key_vault_name         = ${{ vars.KEY_VAULT_NAME }}
#   TF_VAR_admin_email            = ${{ secrets.ADMIN_EMAIL }}
#   TF_VAR_image_uri              = (from build step)
#   TF_VAR_email_custom_domain_name = (for production email)
# ==============================================
