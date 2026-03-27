variable "stack_name" {
  description = "Name prefix for all resources"
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
