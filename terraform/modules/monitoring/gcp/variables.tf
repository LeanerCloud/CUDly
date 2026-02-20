# Variables for GCP Monitoring Module

variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "service_name" {
  description = "Name of the Cloud Run service"
  type        = string
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
}

variable "db_instance_id" {
  description = "Cloud SQL instance ID"
  type        = string
}

variable "service_url" {
  description = "Cloud Run service URL (for uptime checks)"
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

# Alert thresholds
variable "error_rate_threshold" {
  description = "Error rate threshold (requests per second)"
  type        = number
  default     = 5
}

variable "latency_threshold" {
  description = "P95 latency threshold in milliseconds"
  type        = number
  default     = 5000 # 5 seconds
}

variable "cpu_threshold" {
  description = "CPU utilization threshold (%)"
  type        = number
  default     = 80
}

variable "memory_threshold" {
  description = "Memory utilization threshold (%)"
  type        = number
  default     = 85
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
  description = "Application error count threshold per minute"
  type        = number
  default     = 10
}

variable "labels" {
  description = "Labels to apply to all resources"
  type        = map(string)
  default     = {}
}
