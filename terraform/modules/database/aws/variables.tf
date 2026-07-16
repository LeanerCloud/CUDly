variable "stack_name" {
  description = "Name of the stack (used for resource naming)"
  type        = string
}

variable "vpc_id" {
  description = "VPC ID where database will be deployed"
  type        = string
}

variable "vpc_cidr" {
  description = "CIDR block of the VPC"
  type        = string
}

variable "private_subnet_ids" {
  description = "List of private subnet IDs for database"
  type        = list(string)
}

variable "database_name" {
  description = "Name of the database to create"
  type        = string
  default     = "cudly"
}

variable "master_username" {
  description = "Master username for the database"
  type        = string
  default     = "cudly"
}

variable "master_password_secret_arn" {
  description = "ARN of existing Secrets Manager secret containing database password (if null, creates new one)"
  type        = string
  default     = null
}

variable "create_password" {
  description = "Whether to create a new database password. Set to false when master_password_secret_arn is provided externally."
  type        = bool
  default     = true
}

variable "engine_version" {
  description = "PostgreSQL major engine version (major-only pin, e.g. \"16\"). The AWS provider resolves to the latest minor release and stores it in engine_version_actual, suppressing drift when RDS auto-upgrades the minor version. Pinning a full minor version (e.g. \"16.6\") causes downgrade failures if RDS auto-upgrades beyond it (incident: 2026-07-16, #1372)."
  type        = string
  default     = "16"
}

variable "instance_class" {
  description = "RDS instance class (e.g., db.t4g.micro, db.t4g.small)"
  type        = string
  default     = "db.t4g.micro"
}

variable "allocated_storage" {
  description = "Initial allocated storage in GB"
  type        = number
  default     = 20
}

variable "max_allocated_storage" {
  description = "Maximum allocated storage in GB for autoscaling (0 to disable)"
  type        = number
  default     = 100
}

variable "backup_retention_days" {
  description = "Number of days to retain backups"
  type        = number
  default     = 7
}

variable "deletion_protection" {
  description = "Enable deletion protection"
  type        = bool
  default     = true
}

variable "skip_final_snapshot" {
  description = "Skip final snapshot on deletion"
  type        = bool
  default     = false
}

variable "performance_insights_enabled" {
  description = "Enable Performance Insights"
  type        = bool
  default     = true
}

variable "enable_rds_proxy" {
  description = "Enable RDS Proxy for Lambda connection pooling"
  type        = bool
  default     = true
}

variable "kms_key_id" {
  description = "KMS key ID for encryption (optional)"
  type        = string
  default     = null
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}

variable "admin_email" {
  description = "Email address for the default admin user"
  type        = string
}
