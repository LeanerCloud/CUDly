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

variable "key_vault_name" {
  description = "Key Vault name (must be globally unique, 3-24 chars)"
  type        = string
}

variable "sku_name" {
  description = "Key Vault SKU (standard or premium)"
  type        = string
  default     = "standard"

  validation {
    condition     = contains(["standard", "premium"], var.sku_name)
    error_message = "SKU must be either 'standard' or 'premium'."
  }
}

variable "soft_delete_retention_days" {
  description = "Soft delete retention in days (7-90)"
  type        = number
  default     = 7

  validation {
    condition     = var.soft_delete_retention_days >= 7 && var.soft_delete_retention_days <= 90
    error_message = "Soft delete retention must be between 7 and 90 days."
  }
}

variable "purge_protection_enabled" {
  description = "Enable purge protection"
  type        = bool
  default     = false
}

variable "default_network_acl_action" {
  description = "Default network ACL action (Allow or Deny)"
  type        = string
  default     = "Deny"
}

variable "allowed_ip_addresses" {
  description = "List of allowed IP addresses"
  type        = list(string)
  default     = []
}

variable "allowed_subnet_ids" {
  description = "List of allowed subnet IDs"
  type        = list(string)
  default     = []
}

variable "database_password" {
  description = "Database password (if null, will be auto-generated)"
  type        = string
  default     = null
  sensitive   = true
}

variable "create_jwt_secret" {
  description = "Create JWT signing secret"
  type        = bool
  default     = true
}

variable "create_session_secret" {
  description = "Create session encryption secret"
  type        = bool
  default     = true
}

variable "create_smtp_secrets" {
  description = "Create Azure Communication Services SMTP credential secrets"
  type        = bool
  default     = true
}

variable "smtp_username" {
  description = "Azure Communication Services SMTP username (if null, secret created with placeholder)"
  type        = string
  default     = null
  sensitive   = true
}

variable "smtp_password" {
  description = "Azure Communication Services SMTP password (if null, secret created with placeholder)"
  type        = string
  default     = null
  sensitive   = true
}

variable "create_scheduled_task_secret" {
  description = "Create scheduled task authentication secret"
  type        = bool
  default     = true
}

variable "additional_secrets" {
  description = "Map of additional secret values to create (keys are not sensitive, values are)"
  type        = map(string)
  default     = {}
}

variable "container_app_identity_principal_id" {
  description = "Container App managed identity principal ID for RBAC"
  type        = string
  default     = null
}

variable "log_analytics_workspace_id" {
  description = "Log Analytics workspace ID for diagnostics"
  type        = string
  default     = null
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}
