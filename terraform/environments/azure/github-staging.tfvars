# Azure Staging Environment - GitHub Actions
# Used by CI/CD pipelines for automated deployments
# Sensitive values are provided via GitHub secrets

# ==============================================
# Project Settings
# ==============================================

project_name = "cudly"
environment  = "staging"
location     = "eastus"
cost_center  = "engineering"

# ==============================================
# Compute Platform
# ==============================================

compute_platform    = "container-apps"
enable_docker_build = true # Build image via Terraform build module (no separate CI build step)

# Container Apps Configuration
container_cpu            = 1.0
container_memory         = "2.0Gi"
min_replicas             = 1
max_replicas             = 10
external_ingress_enabled = true

# ==============================================
# Database (Azure Flexible Server PostgreSQL)
# ==============================================

postgres_version                = "16"
database_sku_name               = "B_Standard_B2s"
database_storage_mb             = 65536
database_backup_retention_days  = 14
database_geo_redundant_backup   = false
database_high_availability_mode = "Disabled"
auto_migrate                    = true

# ==============================================
# Networking
# ==============================================

vnet_cidr                  = "10.1.0.0/16"
container_apps_subnet_cidr = "10.1.1.0/24"
database_subnet_cidr       = "10.1.2.0/24"

# ==============================================
# Key Vault
# ==============================================

key_vault_sku              = "standard"
soft_delete_retention_days = 14
purge_protection_enabled   = false

# CI staging runs on GitHub Actions with non-fixed runner IPs. Production
# tfvars should keep the default "Deny" and supply allowed_ip_addresses.
key_vault_default_network_acl_action = "Allow"

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
# Frontend (Azure CDN)
# ==============================================

enable_cdn = false

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
  "Environment" = "staging"
}

# ==============================================
# Variables provided by GitHub Actions:
#   TF_VAR_subscription_id = ${{ secrets.AZURE_SUBSCRIPTION_ID }}
#   TF_VAR_admin_email     = ${{ secrets.ADMIN_EMAIL }}
#   TF_VAR_image_uri       = (from build step)
# ==============================================
