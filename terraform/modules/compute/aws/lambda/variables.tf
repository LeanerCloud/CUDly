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
  description = "Allowed origins for CORS"
  type        = list(string)
  default     = ["*"]
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

variable "additional_env_vars" {
  description = "Additional environment variables"
  type        = map(string)
  default     = {}
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}
