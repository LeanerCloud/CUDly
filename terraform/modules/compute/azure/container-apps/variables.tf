variable "app_name" {
  description = "Application name"
  type        = string
}

variable "environment" {
  description = "Environment name (dev/staging/prod)"
  type        = string
}

variable "resource_group_name" {
  description = "Resource group name"
  type        = string
}

variable "location" {
  description = "Azure region"
  type        = string
}

variable "image_uri" {
  description = "Container image URI"
  type        = string
}

variable "cpu" {
  description = "CPU allocation (0.25, 0.5, 0.75, 1.0, 1.25, 1.5, 1.75, 2.0)"
  type        = number
  default     = 0.5

  validation {
    condition     = contains([0.25, 0.5, 0.75, 1.0, 1.25, 1.5, 1.75, 2.0], var.cpu)
    error_message = "CPU must be one of: 0.25, 0.5, 0.75, 1.0, 1.25, 1.5, 1.75, 2.0"
  }
}

variable "memory" {
  description = "Memory allocation (0.5Gi, 1.0Gi, 1.5Gi, 2.0Gi, 3.0Gi, 4.0Gi)"
  type        = string
  default     = "1.0Gi"

  validation {
    condition     = contains(["0.5Gi", "1.0Gi", "1.5Gi", "2.0Gi", "3.0Gi", "4.0Gi"], var.memory)
    error_message = "Memory must be one of: 0.5Gi, 1.0Gi, 1.5Gi, 2.0Gi, 3.0Gi, 4.0Gi"
  }
}

variable "min_replicas" {
  description = "Minimum number of replicas"
  type        = number
  default     = 0
}

variable "max_replicas" {
  description = "Maximum number of replicas"
  type        = number
  default     = 10
}

variable "external_ingress_enabled" {
  description = "Enable external ingress (public access)"
  type        = bool
  default     = true
}

variable "infrastructure_subnet_id" {
  description = "Subnet ID for Container App Environment infrastructure"
  type        = string
  default     = null
}

variable "internal_load_balancer_enabled" {
  description = "Enable internal load balancer"
  type        = bool
  default     = false
}

variable "log_analytics_workspace_id" {
  description = "Log Analytics workspace ID for Container App Environment"
  type        = string
  default     = null
}

variable "enable_diagnostics" {
  description = "Enable diagnostic settings for Container App"
  type        = bool
  default     = false
}

variable "database_host" {
  description = "Database host (FQDN)"
  type        = string
}

variable "database_name" {
  description = "Database name"
  type        = string
}

variable "database_username" {
  description = "Database username"
  type        = string
}

variable "database_password_secret_name" {
  description = "Key Vault secret name for database password"
  type        = string
}

variable "key_vault_uri" {
  description = "Key Vault URI"
  type        = string
}

variable "signing_key_id" {
  description = "Full Key Vault key ID of the OIDC issuer signing key (Microsoft.KeyVault/vaults/.../keys/<name>)"
  type        = string
}

variable "signing_key_name" {
  description = "Name of the OIDC issuer signing key in Key Vault"
  type        = string
}

variable "auto_migrate" {
  description = "Auto-run database migrations on startup"
  type        = bool
  default     = true
}

variable "admin_email" {
  description = "Administrator email address"
  type        = string
}

variable "admin_password_secret_name" {
  description = "Key Vault secret name containing admin password"
  type        = string
  default     = ""
}

variable "allowed_origins" {
  description = "List of allowed CORS origins"
  type        = list(string)
  default     = []
}

variable "additional_env_vars" {
  description = "Additional environment variables"
  type        = map(string)
  default     = {}
}

variable "secrets" {
  description = "List of secrets to mount from Key Vault"
  type = list(object({
    name  = string
    value = string
  }))
  default = []
}

variable "custom_domains" {
  description = "List of custom domains"
  type = list(object({
    name           = string
    certificate_id = string
  }))
  default = []
}

variable "registry_server" {
  description = "Container registry server URL (e.g. myacr.azurecr.io)"
  type        = string
  default     = ""
}

variable "registry_username" {
  description = "Container registry username (for admin auth)"
  type        = string
  default     = ""
}

variable "registry_password" {
  description = "Container registry password (for admin auth)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}

# ==============================================
# Scheduled Tasks (Logic Apps) Configuration
# ==============================================

variable "enable_scheduled_tasks" {
  description = "Enable scheduled tasks via Logic Apps"
  type        = bool
  default     = true
}

variable "scheduled_task_secret" {
  description = "Shared secret for authenticating scheduled task HTTP calls"
  type        = string
  default     = ""
  sensitive   = true
}

variable "recommendation_schedule" {
  description = "Cron schedule for recommendations refresh (default: daily at 2 AM UTC)"
  type        = string
  default     = "0 2 * * *"
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
