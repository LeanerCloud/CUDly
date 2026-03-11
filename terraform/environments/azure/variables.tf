# ==============================================
# Azure Subscription & General
# ==============================================

variable "subscription_id" {
  description = "Azure subscription ID"
  type        = string
}

variable "project_name" {
  description = "Project name (will be prefixed with environment)"
  type        = string
  default     = "cudly"
}

variable "environment" {
  description = "Environment name"
  type        = string
  default     = "dev"
}

variable "location" {
  description = "Azure region"
  type        = string
  default     = "eastus"
}

variable "cost_center" {
  description = "Cost center for billing"
  type        = string
  default     = "engineering"
}

variable "tags" {
  description = "Additional tags to apply to all resources"
  type        = map(string)
  default     = {}
}

# ==============================================
# Compute Platform Selection
# ==============================================

variable "compute_platform" {
  description = "Compute platform (container-apps or aks)"
  type        = string
  default     = "container-apps"

  validation {
    condition     = contains(["container-apps", "aks"], var.compute_platform)
    error_message = "compute_platform must be either 'container-apps' or 'aks'"
  }
}

# ==============================================
# Networking
# ==============================================

variable "vnet_cidr" {
  description = "VNet CIDR block"
  type        = string
  default     = "10.0.0.0/16"
}

variable "container_apps_subnet_cidr" {
  description = "Container Apps subnet CIDR block"
  type        = string
  default     = "10.0.1.0/24"
}

variable "database_subnet_cidr" {
  description = "Database subnet CIDR block"
  type        = string
  default     = "10.0.2.0/24"
}

variable "private_subnet_cidr" {
  description = "Private subnet CIDR block"
  type        = string
  default     = "10.0.3.0/24"
}

variable "create_private_subnet" {
  description = "Create additional private subnet"
  type        = bool
  default     = false
}

variable "allow_inbound_from_internet" {
  description = "Allow inbound HTTPS traffic from internet"
  type        = bool
  default     = true
}

variable "log_retention_days" {
  description = "Log Analytics retention in days"
  type        = number
  default     = 30
}

# ==============================================
# Key Vault (Secrets)
# ==============================================

variable "key_vault_name" {
  description = "Key Vault name (must be globally unique, 3-24 chars)"
  type        = string
}

variable "key_vault_sku" {
  description = "Key Vault SKU (standard or premium)"
  type        = string
  default     = "standard"

  validation {
    condition     = contains(["standard", "premium"], var.key_vault_sku)
    error_message = "Key Vault SKU must be 'standard' or 'premium'."
  }
}

variable "soft_delete_retention_days" {
  description = "Key Vault soft delete retention in days"
  type        = number
  default     = 7
}

variable "purge_protection_enabled" {
  description = "Enable Key Vault purge protection"
  type        = bool
  default     = false
}


variable "allowed_ip_addresses" {
  description = "List of allowed IP addresses for Key Vault"
  type        = list(string)
  default     = []
}

variable "database_password" {
  description = "Database password (if null, auto-generated)"
  type        = string
  default     = null
  sensitive   = true
}

variable "additional_secrets" {
  description = "Map of additional secrets to create"
  type        = map(string)
  default     = {}
  sensitive   = true
}

# ==============================================
# Database (PostgreSQL Flexible Server)
# ==============================================

variable "postgres_version" {
  description = "PostgreSQL version"
  type        = string
  default     = "16"

  validation {
    condition     = contains(["11", "12", "13", "14", "15", "16"], var.postgres_version)
    error_message = "PostgreSQL version must be 11, 12, 13, 14, 15, or 16."
  }
}

variable "database_sku_name" {
  description = "Database SKU name"
  type        = string
  default     = "B_Standard_B1ms" # Burstable, 1 vCore, 2 GB RAM

  # Common SKUs:
  # - B_Standard_B1ms: Burstable, 1 vCore, 2 GB RAM (~$12/month)
  # - B_Standard_B2s: Burstable, 2 vCore, 4 GB RAM (~$30/month)
  # - GP_Standard_D2s_v3: General Purpose, 2 vCore, 8 GB RAM (~$140/month)
  # - MO_Standard_E4s_v3: Memory Optimized, 4 vCore, 32 GB RAM (~$380/month)
}

variable "database_storage_mb" {
  description = "Database storage in MB"
  type        = number
  default     = 32768 # 32 GB

  validation {
    condition     = var.database_storage_mb >= 32768 && var.database_storage_mb <= 16777216
    error_message = "Database storage must be between 32 GB and 16 TB."
  }
}

variable "database_administrator_login" {
  description = "Database administrator username"
  type        = string
  default     = "cudlyadmin"
}

variable "database_backup_retention_days" {
  description = "Database backup retention in days"
  type        = number
  default     = 7

  validation {
    condition     = var.database_backup_retention_days >= 7 && var.database_backup_retention_days <= 35
    error_message = "Backup retention must be between 7 and 35 days."
  }
}

variable "database_geo_redundant_backup" {
  description = "Enable geo-redundant backups"
  type        = bool
  default     = false
}

variable "database_high_availability_mode" {
  description = "High availability mode (Disabled, ZoneRedundant, SameZone)"
  type        = string
  default     = "Disabled"

  validation {
    condition     = contains(["Disabled", "ZoneRedundant", "SameZone"], var.database_high_availability_mode)
    error_message = "HA mode must be Disabled, ZoneRedundant, or SameZone."
  }
}

variable "database_standby_availability_zone" {
  description = "Standby availability zone (for ZoneRedundant HA)"
  type        = string
  default     = null
}

# ==============================================
# Container Apps (Compute)
# ==============================================

variable "image_uri" {
  description = "Container image URI (used when enable_docker_build is false)"
  type        = string
}

variable "enable_docker_build" {
  description = "Enable Docker build module (builds and pushes image during terraform apply). Set to false to use pre-built image_uri instead."
  type        = bool
  default     = false
}

variable "container_cpu" {
  description = "CPU allocation per container"
  type        = number
  default     = 0.5

  validation {
    condition     = contains([0.25, 0.5, 0.75, 1.0, 1.25, 1.5, 1.75, 2.0], var.container_cpu)
    error_message = "CPU must be 0.25, 0.5, 0.75, 1.0, 1.25, 1.5, 1.75, or 2.0."
  }
}

variable "container_memory" {
  description = "Memory allocation per container"
  type        = string
  default     = "1.0Gi"

  validation {
    condition     = contains(["0.5Gi", "1.0Gi", "1.5Gi", "2.0Gi", "3.0Gi", "4.0Gi"], var.container_memory)
    error_message = "Memory must be 0.5Gi, 1.0Gi, 1.5Gi, 2.0Gi, 3.0Gi, or 4.0Gi."
  }
}

variable "min_replicas" {
  description = "Minimum number of container replicas"
  type        = number
  default     = 0
}

variable "max_replicas" {
  description = "Maximum number of container replicas"
  type        = number
  default     = 10
}

variable "external_ingress_enabled" {
  description = "Enable external ingress (public access)"
  type        = bool
  default     = true
}

variable "internal_load_balancer_enabled" {
  description = "Enable internal load balancer"
  type        = bool
  default     = false
}

variable "auto_migrate" {
  description = "Auto-run database migrations on startup"
  type        = bool
  default     = true
}

variable "admin_email" {
  description = "Administrator email for password reset notifications"
  type        = string
}

variable "admin_password" {
  description = "Optional initial admin password (skips password reset requirement)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "additional_env_vars" {
  description = "Additional environment variables for containers"
  type        = map(string)
  default     = {}
}

variable "enable_scheduled_tasks" {
  description = "Enable scheduled tasks via Logic Apps"
  type        = bool
  default     = true
}

variable "recommendation_schedule" {
  description = "Cron schedule for recommendations task"
  type        = string
  default     = "0 2 * * *" # 2 AM daily
}

variable "enable_ri_exchange_schedule" {
  description = "Enable scheduled RI exchange automation via Logic Apps"
  type        = bool
  default     = false
}

variable "ri_exchange_schedule" {
  # Standard 5-field cron: 0 */6 * * * runs at 00:00, 06:00, 12:00, 18:00 UTC.
  description = "Cron schedule for RI exchange automation (e.g., '0 */6 * * *' for every 6 hours)"
  type        = string
  default     = "0 */6 * * *"
}

# ==============================================
# AKS (Kubernetes) - Alternative Compute Platform
# ==============================================

variable "aks_kubernetes_version" {
  description = "Kubernetes version for AKS"
  type        = string
  default     = "1.28"
}

variable "aks_node_count" {
  description = "Initial node count per zone for AKS"
  type        = number
  default     = 2
}

variable "aks_node_vm_size" {
  description = "VM size for AKS nodes"
  type        = string
  default     = "Standard_D2s_v3"
}

variable "aks_min_node_count" {
  description = "Minimum node count for AKS auto-scaling"
  type        = number
  default     = 1
}

variable "aks_max_node_count" {
  description = "Maximum node count for AKS auto-scaling"
  type        = number
  default     = 10
}

variable "aks_enable_auto_scaling" {
  description = "Enable AKS cluster auto-scaling"
  type        = bool
  default     = true
}

variable "aks_enable_azure_policy" {
  description = "Enable Azure Policy add-on for AKS"
  type        = bool
  default     = false
}

variable "aks_enable_log_analytics" {
  description = "Enable Log Analytics for AKS"
  type        = bool
  default     = true
}

# ==============================================
# Frontend (CDN) Configuration
# ==============================================

variable "enable_cdn" {
  description = "Enable CDN (Azure CDN / Front Door) for custom domain and edge caching. When false, the Container Apps URL serves the frontend directly. Only needed when using custom domains."
  type        = bool
  default     = false
}

variable "subdomain_zone_name" {
  description = "Azure DNS subdomain zone name to create (e.g., cudly.example.com). Leave empty to skip zone creation."
  type        = string
  default     = ""
}

variable "frontend_domain_names" {
  description = "Custom domain names for the frontend CDN (e.g., [\"app.cudly.example.com\"])"
  type        = list(string)
  default     = []
}

variable "frontend_cdn_sku" {
  description = "CDN SKU (Standard_Microsoft, Standard_Akamai, Standard_Verizon, Premium_Verizon)"
  type        = string
  default     = "Standard_Microsoft"
}

variable "use_front_door" {
  description = "Use Azure Front Door instead of classic CDN (classic CDN no longer accepts new profiles)"
  type        = bool
  default     = true
}

# ==============================================
# Email Service (Azure Communication Services)
# ==============================================

variable "enable_email_service" {
  description = "Enable Azure Communication Services for email"
  type        = bool
  default     = true
}

variable "email_data_location" {
  description = "Data residency location for Azure Communication Services"
  type        = string
  default     = "United States"
}

variable "email_use_azure_managed_domain" {
  description = "Use Azure-managed domain (*.azurecomm.net) - recommended for dev/test"
  type        = bool
  default     = true
}

variable "email_custom_domain_name" {
  description = "Custom domain name for email (e.g., cudly.leanercloud.com) - for production"
  type        = string
  default     = ""
}
