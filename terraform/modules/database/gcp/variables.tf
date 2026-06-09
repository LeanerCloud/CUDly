variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "service_name" {
  description = "Service name"
  type        = string
}

variable "environment" {
  description = "Environment name (dev/staging/prod)"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
}

variable "database_version" {
  description = "PostgreSQL version"
  type        = string
  default     = "POSTGRES_16"
}

variable "database_name" {
  description = "Database name"
  type        = string
  default     = "cudly"
}

variable "master_username" {
  description = "Master database username"
  type        = string
  default     = "cudly"
}

variable "master_password" {
  description = "Master database password (if null, will be auto-generated)"
  type        = string
  default     = null
  sensitive   = true
}

variable "generate_password" {
  description = "Auto-generate a random database password. Set to false when master_password is provided externally (e.g. from a secrets module output) to avoid the sensitive-value-in-count Terraform limitation."
  type        = bool
  default     = true
}

variable "tier" {
  description = "Machine type tier (db-custom-[CPUs]-[RAM_MB])"
  type        = string
  default     = "db-custom-1-3840" # 1 vCPU, 3.75 GB RAM
}

variable "high_availability" {
  description = "Enable high availability (REGIONAL vs ZONAL)"
  type        = bool
  default     = false
}

variable "disk_type" {
  description = "Disk type (PD_SSD or PD_HDD)"
  type        = string
  default     = "PD_SSD"
}

variable "disk_size" {
  description = "Disk size in GB"
  type        = number
  default     = 10
}

variable "disk_autoresize" {
  description = "Enable automatic disk resize"
  type        = bool
  default     = true
}

variable "disk_autoresize_limit" {
  description = "Maximum disk size in GB (0 = no limit)"
  type        = number
  default     = 100
}

variable "vpc_network_id" {
  description = "VPC network ID for private IP"
  type        = string
}

variable "enable_public_ip" {
  description = "Enable public IP address"
  type        = bool
  default     = false
}

variable "authorized_networks" {
  description = "List of authorized networks (if public IP is enabled)"
  type = list(object({
    name = string
    cidr = string
  }))
  default = []
}

variable "backup_enabled" {
  description = "Enable automated backups"
  type        = bool
  default     = true
}

variable "backup_start_time" {
  description = "Backup start time (HH:MM format, UTC)"
  type        = string
  default     = "03:00"
}

variable "point_in_time_recovery" {
  description = "Enable point-in-time recovery"
  type        = bool
  default     = true
}

variable "transaction_log_retention_days" {
  description = "Transaction log retention in days (1-7)"
  type        = number
  default     = 7

  validation {
    condition     = var.transaction_log_retention_days >= 1 && var.transaction_log_retention_days <= 7
    error_message = "Transaction log retention must be between 1 and 7 days."
  }
}

variable "backup_retention_count" {
  description = "Number of backups to retain"
  type        = number
  default     = 7
}

variable "maintenance_window_day" {
  description = "Maintenance window day (1-7, 1=Monday)"
  type        = number
  default     = 7
}

variable "maintenance_window_hour" {
  description = "Maintenance window hour (0-23, UTC)"
  type        = number
  default     = 4
}

variable "maintenance_update_track" {
  description = "Maintenance update track (stable or canary)"
  type        = string
  default     = "stable"
}

variable "database_flags" {
  description = "Database flags to set"
  type = list(object({
    name  = string
    value = string
  }))
  default = [
    {
      name  = "max_connections"
      value = "100"
    }
  ]
}

variable "query_insights_enabled" {
  description = "Enable Query Insights"
  type        = bool
  default     = false
}

variable "query_string_length" {
  description = "Query string length for insights (256-4500)"
  type        = number
  default     = 1024
}

variable "record_application_tags" {
  description = "Record application tags in Query Insights"
  type        = bool
  default     = false
}

variable "record_client_address" {
  description = "Record client address in Query Insights"
  type        = bool
  default     = false
}

variable "deletion_protection" {
  description = "Enable deletion protection"
  type        = bool
  default     = false
}

variable "enable_iam_authentication" {
  description = "Enable IAM database authentication"
  type        = bool
  default     = false
}

variable "cloud_run_service_account_email" {
  description = "Cloud Run service account email for IAM auth"
  type        = string
  default     = null
}

variable "enable_read_replica" {
  description = "Enable read replica"
  type        = bool
  default     = false
}

variable "replica_region" {
  description = "Region for read replica (if different from main)"
  type        = string
  default     = null
}

variable "replica_tier" {
  description = "Machine tier for replica (if different from main)"
  type        = string
  default     = null
}
