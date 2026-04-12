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

variable "image_uri" {
  description = "ECR image URI for Lambda container"
  type        = string
}

variable "architecture" {
  description = "Lambda architecture (x86_64 or arm64)"
  type        = string
  default     = "arm64"
}

variable "memory_size" {
  description = "Lambda memory size in MB"
  type        = number
  default     = 512
}

variable "timeout" {
  description = "Lambda timeout in seconds"
  type        = number
  default     = 30
}

variable "database_host" {
  description = "Database endpoint (RDS Proxy endpoint recommended)"
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

variable "database_password_secret_arn" {
  description = "ARN of Secrets Manager secret containing database password"
  type        = string
}

variable "admin_email" {
  description = "Email address for the default admin user (created without password - must use password reset)"
  type        = string
}

variable "admin_password_secret_arn" {
  description = "ARN of Secrets Manager secret containing admin password"
  type        = string
  default     = ""
}

variable "auto_migrate" {
  description = "Automatically run database migrations on startup"
  type        = bool
  default     = true
}

variable "vpc_config" {
  description = "VPC configuration for Lambda"
  type = object({
    vpc_id                        = string
    subnet_ids                    = list(string)
    additional_security_group_ids = list(string)
  })
  default = null
}

variable "enable_function_url" {
  description = "Enable Lambda Function URL"
  type        = bool
  default     = true
}

variable "function_url_auth_type" {
  description = "Function URL authorization type (NONE or AWS_IAM)"
  type        = string
  default     = "NONE"
}

variable "allowed_origins" {
  description = "Allowed origins for CORS. Must be non-empty — Lambda Function URL CORS is infrastructure-enforced and rejects all requests with empty allow_origins."
  type        = list(string)
  default     = []

  validation {
    condition     = length(var.allowed_origins) > 0
    error_message = "allowed_origins must not be empty for Lambda Function URL CORS — set an explicit origin list in tfvars."
  }
}

variable "reserved_concurrent_executions" {
  description = "Reserved concurrent executions (-1 for unreserved)"
  type        = number
  default     = -1
}

variable "log_retention_days" {
  description = "CloudWatch log retention in days"
  type        = number
  default     = 7
}

variable "enable_scheduled_tasks" {
  description = "Enable scheduled EventBridge tasks"
  type        = bool
  default     = true
}

variable "recommendation_schedule" {
  description = "EventBridge schedule expression for recommendations"
  type        = string
  default     = "rate(1 day)"
}

variable "enable_ri_exchange_schedule" {
  description = "Enable scheduled RI exchange automation"
  type        = bool
  default     = false
}

variable "ri_exchange_schedule" {
  description = "Schedule for RI exchange automation. rate() starts from deployment time; cron(0 0/6 * * ? *) runs at fixed clock hours (EventBridge cron syntax). For predictable approval windows, prefer cron."
  type        = string
  default     = "rate(6 hours)"
}

variable "additional_env_vars" {
  description = "Additional environment variables"
  type        = map(string)
  default     = {}
}

variable "credential_encryption_key_secret_arn" {
  description = "ARN of Secrets Manager secret holding the AES-256-GCM credential encryption key"
  type        = string
  default     = ""
}

variable "enable_cross_account_sts" {
  description = "Allow Lambda to assume roles in remote AWS accounts (required for multi-account support)"
  type        = bool
  default     = false
}

variable "enable_org_discovery" {
  description = "Allow Lambda to call AWS Organizations ListAccounts for member account discovery"
  type        = bool
  default     = false
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}
