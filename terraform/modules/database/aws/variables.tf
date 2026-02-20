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

variable "engine_version" {
  description = "Aurora PostgreSQL engine version"
  type        = string
  default     = "16.4"
}

variable "min_capacity" {
  description = "Minimum Aurora Serverless v2 capacity (ACUs)"
  type        = number
  default     = 0.5
}

variable "max_capacity" {
  description = "Maximum Aurora Serverless v2 capacity (ACUs)"
  type        = number
  default     = 2.0
}

variable "instance_count" {
  description = "Number of Aurora instances (for high availability)"
  type        = number
  default     = 1
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
