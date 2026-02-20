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

variable "postgres_version" {
  description = "PostgreSQL version (11, 12, 13, 14, 15, 16)"
  type        = string
  default     = "16"
}

variable "database_name" {
  description = "Database name"
  type        = string
  default     = "cudly"
}

variable "administrator_login" {
  description = "Administrator login name"
  type        = string
  default     = "cudly"
}

variable "administrator_password" {
  description = "Administrator password (if null, will be auto-generated)"
  type        = string
  default     = null
  sensitive   = true
}

variable "sku_name" {
  description = "SKU name (B_Standard_B1ms, GP_Standard_D2s_v3, etc.)"
  type        = string
  default     = "B_Standard_B1ms" # Burstable, 1 vCore, 2 GiB RAM
}

variable "storage_mb" {
  description = "Storage size in MB (32768-16777216)"
  type        = number
  default     = 32768 # 32 GB
}

variable "backup_retention_days" {
  description = "Backup retention in days (7-35)"
  type        = number
  default     = 7

  validation {
    condition     = var.backup_retention_days >= 7 && var.backup_retention_days <= 35
    error_message = "Backup retention must be between 7 and 35 days."
  }
}

variable "geo_redundant_backup_enabled" {
  description = "Enable geo-redundant backups"
  type        = bool
  default     = false
}

variable "high_availability_mode" {
  description = "High availability mode (Disabled, SameZone, ZoneRedundant)"
  type        = string
  default     = "Disabled"

  validation {
    condition     = contains(["Disabled", "SameZone", "ZoneRedundant"], var.high_availability_mode)
    error_message = "HA mode must be Disabled, SameZone, or ZoneRedundant."
  }
}

variable "standby_availability_zone" {
  description = "Standby availability zone (1, 2, or 3)"
  type        = string
  default     = null
}

variable "availability_zone" {
  description = "Preferred availability zone (1, 2, or 3)"
  type        = string
  default     = null
}

variable "maintenance_window" {
  description = "Maintenance window configuration"
  type = object({
    day_of_week  = number
    start_hour   = number
    start_minute = number
  })
  default = null
}

variable "delegated_subnet_id" {
  description = "Delegated subnet ID for private access"
  type        = string
}

variable "private_dns_zone_id" {
  description = "Private DNS zone ID"
  type        = string
}

variable "public_network_access_enabled" {
  description = "Enable public network access"
  type        = bool
  default     = false
}

variable "allowed_ip_ranges" {
  description = "Allowed IP ranges (if public access enabled)"
  type = map(object({
    start_ip = string
    end_ip   = string
  }))
  default = {}
}

variable "server_parameters" {
  description = "PostgreSQL server parameters"
  type        = map(string)
  default     = {}
}

variable "key_vault_id" {
  description = "Key Vault ID for storing password"
  type        = string
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
