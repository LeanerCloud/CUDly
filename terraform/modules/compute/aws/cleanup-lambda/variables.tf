variable "stack_name" {
  description = "Name prefix for all resources"
  type        = string
}

variable "permissions_boundary_arn" {
  description = <<-EOT
    ARN of the permissions boundary that every IAM role in this module must
    carry. Required, not optional: cudly-terraform-deploy's IAM grants are
    conditioned on iam:PermissionsBoundary, so a role created without this
    boundary cannot have its inline policies or managed-policy attachments
    written and the apply fails with AccessDenied. See
    terraform/environments/aws/ci-cd-permissions/policy_boundary.tf.
  EOT
  type        = string
}

variable "image_uri" {
  description = "Docker image URI containing the cleanup Lambda handler"
  type        = string
}

variable "db_host" {
  description = "Database host (RDS Proxy endpoint recommended)"
  type        = string
}

variable "db_name" {
  description = "PostgreSQL database name"
  type        = string
  default     = "cudly"
}

variable "db_username" {
  description = "PostgreSQL database username"
  type        = string
  default     = "cudly"
}

variable "db_password_secret_arn" {
  description = "ARN of the secret containing the database password"
  type        = string
}

variable "subnet_ids" {
  description = "VPC subnet IDs for Lambda function"
  type        = list(string)
}

variable "security_group_ids" {
  description = "Security group IDs for Lambda function"
  type        = list(string)
}

variable "schedule_expression" {
  description = "EventBridge schedule expression (default: daily at 2 AM UTC)"
  type        = string
  default     = "cron(0 2 * * ? *)"
}

variable "timeout" {
  description = "Lambda timeout in seconds"
  type        = number
  default     = 300
}

variable "memory_size" {
  description = "Lambda memory size in MB"
  type        = number
  default     = 256
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}
