variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "service_name" {
  description = "Service name"
  type        = string
}

variable "environment" {
  description = "Environment name (dev/staging/prod)"
  type        = string
}

variable "database_password" {
  description = "Database password (if null, will be auto-generated)"
  type        = string
  default     = null
  sensitive   = true
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

variable "sendgrid_api_key" {
  description = "SendGrid API key for email sending (if null, secret created with placeholder)"
  type        = string
  default     = null
  sensitive   = true
}

variable "create_sendgrid_secret" {
  description = "Create SendGrid API key secret (even if key is null, for manual population later)"
  type        = bool
  default     = true
}

variable "create_scheduled_task_secret" {
  description = "Create scheduled task authentication secret"
  type        = bool
  default     = true
}

variable "additional_secrets" {
  description = "Map of additional secret values to create (keys are not sensitive, values are)"
  type        = map(string)
  default     = {}
  # Removed sensitive flag - keys used in for_each cannot be sensitive
}

variable "cloud_run_service_account_email" {
  description = "Cloud Run service account email for IAM permissions"
  type        = string
  default     = null
}

variable "labels" {
  description = "Labels to apply to resources"
  type        = map(string)
  default     = {}
}
