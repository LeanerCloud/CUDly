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

variable "region" {
  description = "GCP region"
  type        = string
}

variable "image_uri" {
  description = "Container image URI from Artifact Registry"
  type        = string
}

variable "cpu" {
  description = "CPU allocation (0.08-8.0, in increments based on memory)"
  type        = string
  default     = "1"
}

variable "memory" {
  description = "Memory allocation (128Mi-32Gi)"
  type        = string
  default     = "512Mi"
}

variable "min_instances" {
  description = "Minimum number of instances"
  type        = number
  default     = 0
}

variable "max_instances" {
  description = "Maximum number of instances"
  type        = number
  default     = 10
}

variable "cpu_throttling" {
  description = "CPU throttling when idle"
  type        = bool
  default     = true
}

variable "startup_cpu_boost" {
  description = "Enable CPU boost during startup"
  type        = bool
  default     = false
}

variable "request_timeout" {
  description = "Request timeout in seconds (1-3600)"
  type        = number
  default     = 300

  validation {
    condition     = var.request_timeout >= 1 && var.request_timeout <= 3600
    error_message = "Request timeout must be between 1 and 3600 seconds."
  }
}

variable "execution_environment" {
  description = "Execution environment (EXECUTION_ENVIRONMENT_GEN1 or EXECUTION_ENVIRONMENT_GEN2)"
  type        = string
  default     = "EXECUTION_ENVIRONMENT_GEN2"
}

variable "ingress" {
  description = "Ingress settings (INGRESS_TRAFFIC_ALL, INGRESS_TRAFFIC_INTERNAL_ONLY, or INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER)"
  type        = string
  default     = "INGRESS_TRAFFIC_ALL"
}

variable "allow_unauthenticated" {
  description = "Allow unauthenticated access"
  type        = bool
  default     = true
}

variable "database_host" {
  description = "Database host (Cloud SQL private IP)"
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

variable "database_password_secret_id" {
  description = "Secret Manager secret ID containing database password"
  type        = string
}

variable "auto_migrate" {
  description = "Automatically run database migrations on startup"
  type        = bool
  default     = true
}

variable "admin_email" {
  description = "Administrator email address"
  type        = string
}

variable "admin_password_secret_name" {
  description = "Full Secret Manager secret name containing admin password"
  type        = string
  default     = ""
}

variable "enable_admin_password_writer" {
  description = "Grant the Cloud Run SA write access to the admin password secret. Set to true when admin_password_secret_name comes from a resource attribute (known after apply) to avoid sensitive-value-in-count Terraform limitation."
  type        = bool
  default     = false
}

variable "allowed_origins" {
  description = "List of allowed CORS origins"
  type        = list(string)
  default     = []
}

variable "vpc_connector_id" {
  description = "Serverless VPC Access connector ID for Cloud SQL"
  type        = string
  default     = null
}

variable "vpc_egress_mode" {
  description = "VPC egress mode (ALL_TRAFFIC or PRIVATE_RANGES_ONLY)"
  type        = string
  default     = "PRIVATE_RANGES_ONLY"
}

variable "enable_scheduled_tasks" {
  description = "Enable Cloud Scheduler for scheduled tasks"
  type        = bool
  default     = true
}

variable "recommendation_schedule" {
  description = "Cron schedule for recommendations (e.g., '0 2 * * *' for 2 AM daily)"
  type        = string
  default     = "0 2 * * *"
}

variable "scheduled_task_secret" {
  description = "Shared secret for authenticating scheduled task HTTP calls"
  type        = string
  default     = ""
  sensitive   = true
}

variable "log_retention_days" {
  description = "Log retention in days (null for default retention)"
  type        = number
  default     = null
}

variable "manage_project_log_retention" {
  description = "Whether to manage the project-level _Default log bucket retention. WARNING: This modifies the project-wide log bucket, affecting all services in the project. Only enable if this module should own project-level log retention."
  type        = bool
  default     = false
}

variable "enable_ri_exchange_schedule" {
  description = "Enable scheduled RI exchange automation"
  type        = bool
  default     = false
}

variable "ri_exchange_schedule" {
  # Standard 5-field cron: 0 */6 * * * runs at 00:00, 06:00, 12:00, 18:00 UTC.
  description = "Cron schedule for RI exchange automation (e.g., '0 */6 * * *' for every 6 hours)"
  type        = string
  default     = "0 */6 * * *"
}

variable "billing_account_id" {
  description = "GCP billing account ID (e.g. '017217-CF7D29-F5AAE4'). Required to grant billing.viewer on the billing account so the app can read SKU prices. Leave empty to skip the billing IAM binding."
  type        = string
  default     = ""
}

variable "additional_env_vars" {
  description = "Additional environment variables"
  type        = map(string)
  default     = {}
}

variable "labels" {
  description = "Labels to apply to resources"
  type        = map(string)
  default     = {}
}
