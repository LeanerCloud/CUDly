# Azure Development Environment - GitHub Actions
# Used by CI/CD pipelines for automated deployments
# Sensitive values are provided via GitHub secrets

# ==============================================
# Project Settings
# ==============================================

project_name = "cudly"
environment  = "dev"
location     = "westus2"
cost_center  = "engineering"

# ==============================================
# Compute Platform
# ==============================================

compute_platform    = "container-apps"
enable_docker_build = true # Build and push image via terraform apply on the runner

# Container Apps Configuration
container_cpu            = 0.25
container_memory         = "0.5Gi"
min_replicas             = 0
max_replicas             = 10
external_ingress_enabled = true

# ==============================================
# Database (Azure Flexible Server PostgreSQL)
# ==============================================

postgres_version                = "16"
database_sku_name               = "B_Standard_B1ms"
database_storage_mb             = 32768
database_backup_retention_days  = 7
database_geo_redundant_backup   = false
database_high_availability_mode = "Disabled"
auto_migrate                    = true

# ==============================================
# Networking
# ==============================================

vnet_cidr                  = "10.0.0.0/16"
container_apps_subnet_cidr = "10.0.1.0/24"
database_subnet_cidr       = "10.0.2.0/24"

# ==============================================
# Key Vault
# ==============================================

key_vault_name             = "cudly-dev-kv"
key_vault_sku              = "standard"
soft_delete_retention_days = 7
purge_protection_enabled   = false

# ==============================================
# Scheduled Tasks
# ==============================================

enable_scheduled_tasks  = true
recommendation_schedule = "0 2 * * *"

# ==============================================
# Logging
# ==============================================

log_retention_days = 30

# ==============================================
# Frontend (Azure Front Door)
# ==============================================

enable_cdn     = false
use_front_door = true

# ==============================================
# Email Service
# ==============================================

enable_email_service           = true
email_use_azure_managed_domain = true

# ==============================================
# Tags
# ==============================================

tags = {
  "ManagedBy"   = "Terraform"
  "Project"     = "CUDly"
  "Environment" = "dev"
}

# ==============================================
# Variables provided by GitHub Actions:
#   TF_VAR_subscription_id = ${{ secrets.AZURE_SUBSCRIPTION_ID }}
#   TF_VAR_key_vault_name  = ${{ vars.KEY_VAULT_NAME }}
#   TF_VAR_admin_email     = ${{ secrets.ADMIN_EMAIL }}
#   TF_VAR_image_uri       = (from build step)
# ==============================================
