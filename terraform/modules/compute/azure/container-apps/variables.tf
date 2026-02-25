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

variable "database_password_secret_id" {
  description = "Key Vault secret ID for database password"
  type        = string
}

variable "key_vault_uri" {
  description = "Key Vault URI"
  type        = string
}

variable "auto_migrate" {
  description = "Auto-run database migrations on startup"
  type        = string
  default     = "true"
}

variable "admin_email" {
  description = "Administrator email address"
  type        = string
}

variable "admin_password" {
  description = "Optional initial admin password (skips password reset requirement)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "allowed_origins" {
  description = "List of allowed CORS origins"
  type        = list(string)
  default     = ["*"]
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

variable "enable_scheduled_jobs" {
  description = "Enable scheduled jobs (recommendations, etc.)"
  type        = bool
  default     = true
}

variable "recommendation_schedule" {
  description = "Cron schedule for recommendations job"
  type        = string
  default     = "0 2 * * *" # 2 AM daily
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

variable "recommendations_schedule" {
  description = "Cron schedule for recommendations refresh (default: daily at 2 AM UTC)"
  type        = string
  default     = "0 2 * * *"
}
