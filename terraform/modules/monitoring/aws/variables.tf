# Variables for AWS Monitoring Module

variable "stack_name" {
  description = "Name of the stack"
  type        = string
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "aws_region" {
  description = "AWS region"
  type        = string
}

variable "compute_platform" {
  description = "Compute platform (lambda or fargate)"
  type        = string
  default     = "lambda"

  validation {
    condition     = contains(["lambda", "fargate"], var.compute_platform)
    error_message = "compute_platform must be either 'lambda' or 'fargate'"
  }
}

variable "function_name" {
  description = "Lambda function name (required if compute_platform is lambda)"
  type        = string
  default     = ""
}

variable "db_cluster_id" {
  description = "RDS cluster identifier"
  type        = string
}

variable "log_group_name" {
  description = "CloudWatch log group name"
  type        = string
}

variable "alert_email_addresses" {
  description = "List of email addresses to receive alerts"
  type        = list(string)
  default     = []
}

variable "slack_webhook_url" {
  description = "Slack webhook URL for notifications (optional)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "enable_sns_encryption" {
  description = "Enable KMS encryption for SNS topic"
  type        = bool
  default     = true
}

variable "enable_xray" {
  description = "Enable AWS X-Ray tracing"
  type        = bool
  default     = true
}

variable "xray_sampling_rate" {
  description = "X-Ray sampling rate (0.0 to 1.0)"
  type        = number
  default     = 0.1

  validation {
    condition     = var.xray_sampling_rate >= 0 && var.xray_sampling_rate <= 1
    error_message = "xray_sampling_rate must be between 0.0 and 1.0"
  }
}

# Alarm thresholds
variable "lambda_error_threshold" {
  description = "Lambda error count threshold for alarm"
  type        = number
  default     = 10
}

variable "lambda_duration_threshold" {
  description = "Lambda duration threshold in milliseconds"
  type        = number
  default     = 10000 # 10 seconds
}

variable "db_cpu_threshold" {
  description = "Database CPU utilization threshold (%)"
  type        = number
  default     = 80
}

variable "db_connection_threshold" {
  description = "Database connection count threshold"
  type        = number
  default     = 80
}

variable "app_error_threshold" {
  description = "Application error count threshold per 5 minutes"
  type        = number
  default     = 20
}

variable "slow_request_threshold" {
  description = "Slow request threshold in milliseconds"
  type        = number
  default     = 5000 # 5 seconds
}

variable "enable_guardduty" {
  description = "Enable GuardDuty threat detection. Has per-GB cost implications (CloudTrail/VPC Flow Logs/DNS logs analyzed). Recommended for production."
  type        = bool
  default     = false
}

variable "enable_security_hub" {
  description = "Enable Security Hub with CIS and AFSBP standards. Has per-check cost implications (~$0.001/check/resource/month). Recommended for production."
  type        = bool
  default     = false
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}
