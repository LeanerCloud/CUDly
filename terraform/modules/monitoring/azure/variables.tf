# Variables for Azure Monitoring Module

variable "app_name" {
  description = "Name of the application"
  type        = string
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "location" {
  description = "Azure region"
  type        = string
}

variable "resource_group_name" {
  description = "Name of the resource group"
  type        = string
}

variable "container_app_id" {
  description = "Resource ID of the Container App"
  type        = string
}

variable "db_server_id" {
  description = "Resource ID of the PostgreSQL Flexible Server"
  type        = string
}

variable "app_url" {
  description = "Container App URL (for availability tests)"
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

variable "log_retention_days" {
  description = "Number of days to retain logs"
  type        = number
  default     = 30

  validation {
    condition     = contains([30, 60, 90, 120, 180, 270, 365, 550, 730], var.log_retention_days)
    error_message = "log_retention_days must be one of: 30, 60, 90, 120, 180, 270, 365, 550, 730"
  }
}

# Alert thresholds
variable "error_rate_threshold" {
  description = "Error count threshold for alarm"
  type        = number
  default     = 10
}

variable "latency_threshold" {
  description = "Response time threshold in milliseconds"
  type        = number
  default     = 5000 # 5 seconds
}

variable "cpu_threshold" {
  description = "CPU cores threshold"
  type        = number
  default     = 0.8 # 0.8 cores
}

variable "memory_threshold" {
  description = "Memory threshold in MB"
  type        = number
  default     = 400 # 400 MB
}

variable "db_cpu_threshold" {
  description = "Database CPU utilization threshold (%)"
  type        = number
  default     = 80
}

variable "db_memory_threshold" {
  description = "Database memory utilization threshold (%)"
  type        = number
  default     = 85
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

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}
