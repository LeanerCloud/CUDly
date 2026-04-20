# AWS Fargate Module Variables

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
  description = "ECR image URI for Fargate container"
  type        = string
}

variable "cpu_architecture" {
  description = "ECS task CPU architecture (arm64 or x86_64)"
  type        = string
  default     = "x86_64"

  validation {
    condition     = contains(["arm64", "x86_64"], var.cpu_architecture)
    error_message = "cpu_architecture must be 'arm64' or 'x86_64'."
  }
}

variable "cpu" {
  description = "Fargate CPU units (256, 512, 1024, 2048, 4096)"
  type        = number
  default     = 512
}

variable "memory" {
  description = "Fargate memory in MB (512, 1024, 2048, 4096, 8192, 16384, 30720)"
  type        = number
  default     = 1024
}

variable "desired_count" {
  description = "Desired number of tasks"
  type        = number
  default     = 2
}

variable "min_capacity" {
  description = "Minimum number of tasks for auto-scaling"
  type        = number
  default     = 1
}

variable "max_capacity" {
  description = "Maximum number of tasks for auto-scaling"
  type        = number
  default     = 10
}

variable "database_host" {
  description = "Database endpoint"
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

variable "vpc_id" {
  description = "VPC ID"
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnet IDs for ECS tasks"
  type        = list(string)
}

variable "public_subnet_ids" {
  description = "Public subnet IDs for Application Load Balancer"
  type        = list(string)
}

variable "alb_security_group_id" {
  description = "Security group ID for ALB"
  type        = string
}

variable "enable_https" {
  description = "Enable HTTPS listener on ALB"
  type        = bool
  default     = true
}

variable "certificate_arn" {
  description = "ARN of ACM certificate for HTTPS"
  type        = string
  default     = ""
}

variable "health_check_path" {
  description = "Health check path"
  type        = string
  default     = "/health"
}

variable "health_check_interval" {
  description = "Health check interval in seconds"
  type        = number
  default     = 30
}

variable "health_check_timeout" {
  description = "Health check timeout in seconds"
  type        = number
  default     = 5
}

variable "healthy_threshold" {
  description = "Number of consecutive successful health checks"
  type        = number
  default     = 2
}

variable "unhealthy_threshold" {
  description = "Number of consecutive failed health checks"
  type        = number
  default     = 3
}

variable "log_retention_days" {
  description = "CloudWatch log retention in days"
  type        = number
  default     = 90
}

variable "enable_scheduled_tasks" {
  description = "Enable scheduled EventBridge tasks"
  type        = bool
  default     = true
}

variable "recommendation_schedule" {
  description = "Schedule expression for recommendation collection"
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

variable "task_timeout" {
  description = "Timeout in seconds for one-off scheduled tasks"
  type        = number
  default     = 900
}

variable "additional_env_vars" {
  description = "Additional environment variables"
  type        = map(string)
  default     = {}
}

variable "allowed_origins" {
  description = "CORS allowed origins"
  type        = list(string)
  default     = []
}

variable "enable_execute_command" {
  description = "Enable ECS Exec for debugging"
  type        = bool
  default     = false
}

variable "enable_container_insights" {
  description = "Enable ECS Container Insights (CloudWatch custom metrics per container). Has per-metric cost implications (~$0.30/metric/month). Recommended for production."
  type        = bool
  default     = false
}

variable "enable_alb_deletion_protection" {
  description = "Enable ALB deletion protection (recommended for production)"
  type        = bool
  default     = false
}

variable "tags" {
  description = "Additional tags for resources"
  type        = map(string)
  default     = {}
}

# ==============================================
# Multi-account credentials + discovery
# ==============================================
# These options mirror the Lambda module's 5-field shape so the Fargate
# path is at feature parity. See terraform/modules/compute/aws/lambda/variables.tf
# for the same set.

variable "credential_encryption_key_secret_arn" {
  description = "ARN of the Secrets Manager secret holding the credential encryption key. Empty string disables the GetSecretValue grant on this resource."
  type        = string
  default     = ""
}

variable "enable_cross_account_sts" {
  description = "Grant the task role sts:AssumeRole on IAM roles whose name starts with cross_account_role_name_prefix. Required for multi-account plan execution."
  type        = bool
  default     = true
}

variable "cross_account_role_name_prefix" {
  description = "Prefix that scopes the cross_account_sts grant. The shipped federation templates (iac/federation/aws-*) create roles starting with this prefix."
  type        = string
  default     = "CUDly"
}

variable "enable_org_discovery" {
  description = "Grant the task role organizations:ListAccounts / DescribeOrganization so CUDly can enumerate accounts in an AWS Organizations management / delegated account."
  type        = bool
  default     = false
}

variable "email_from_domain" {
  description = "Verified SES From domain. When set, the task role gets SES send permissions scoped to this domain plus stack-specific configuration sets. Leave empty to disable SES entirely."
  type        = string
  default     = ""
}
