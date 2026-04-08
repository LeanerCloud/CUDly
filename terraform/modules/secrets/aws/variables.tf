variable "stack_name" {
  description = "Name of the stack"
  type        = string
}

variable "environment" {
  description = "Environment name (dev/staging/prod)"
  type        = string
}

variable "region" {
  description = "AWS region"
  type        = string
}

variable "database_username" {
  description = "Database master username (required for RDS Proxy secret format)"
  type        = string
  default     = "cudly"
}

variable "database_password" {
  description = "Database password (if null, a random password will be generated)"
  type        = string
  default     = null
  sensitive   = true
}

variable "recovery_window_days" {
  description = "Number of days to retain deleted secrets (0-30, 0 for immediate deletion)"
  type        = number
  default     = 7

  validation {
    condition     = var.recovery_window_days >= 0 && var.recovery_window_days <= 30
    error_message = "Recovery window must be between 0 and 30 days."
  }
}

variable "admin_password" {
  description = "Admin password (if null, a random password will be generated)"
  type        = string
  default     = null
  sensitive   = true
}

variable "create_admin_password_secret" {
  description = "Create admin password secret in Secrets Manager"
  type        = bool
  default     = true
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

variable "additional_secrets" {
  description = "Map of additional secrets to create (map keys are not sensitive, only values are)"
  type = map(object({
    description = string
    value       = string
  }))
  default = {}
}

variable "enable_secret_rotation" {
  description = "Enable automatic secret rotation for database password"
  type        = bool
  default     = false
}

variable "rotation_days" {
  description = "Number of days between automatic rotations"
  type        = number
  default     = 30

  validation {
    condition     = var.rotation_days >= 1 && var.rotation_days <= 365
    error_message = "Rotation days must be between 1 and 365."
  }
}

variable "rotation_lambda_vpc_config" {
  description = "VPC configuration for rotation Lambda function"
  type = object({
    subnet_ids         = list(string)
    security_group_ids = list(string)
  })
  default = null
}

variable "rds_cluster_id" {
  description = "RDS cluster ID for rotation (required if enable_secret_rotation is true)"
  type        = string
  default     = null
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}

variable "create_credential_encryption_key" {
  description = "Whether to create a Secrets Manager secret for the credential encryption key"
  type        = bool
  default     = false
}

variable "credential_encryption_key" {
  description = "AES-256-GCM key for account credential encryption (64-char hex = 32 bytes). Generate with: openssl rand -hex 32"
  type        = string
  sensitive   = true
  default     = ""
}
