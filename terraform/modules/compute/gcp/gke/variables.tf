# GCP GKE Module Variables

variable "project_name" {
  description = "Project name for resource naming"
  type        = string
}

variable "environment" {
  description = "Environment name (dev/staging/prod)"
  type        = string
}

variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
}

variable "zones" {
  description = "GCP zones for node pools"
  type        = list(string)
  default     = []
}

variable "network_name" {
  description = "VPC network name"
  type        = string
}

variable "subnetwork_name" {
  description = "VPC subnetwork name"
  type        = string
}

variable "image_name" {
  description = "Container image name (with registry)"
  type        = string
}

variable "image_tag" {
  description = "Container image tag"
  type        = string
  default     = "latest"
}

variable "kubernetes_version" {
  description = "Kubernetes version"
  type        = string
  default     = "1.30"
}

variable "node_count" {
  description = "Initial number of nodes per zone"
  type        = number
  default     = 1
}

variable "node_machine_type" {
  description = "Machine type for nodes"
  type        = string
  default     = "e2-standard-2"
}

variable "node_disk_size_gb" {
  description = "Disk size for nodes in GB"
  type        = number
  default     = 50
}

variable "min_node_count" {
  description = "Minimum node count for auto-scaling (per zone)"
  type        = number
  default     = 1
}

variable "max_node_count" {
  description = "Maximum node count for auto-scaling (per zone)"
  type        = number
  default     = 3
}

variable "database_host" {
  description = "Cloud SQL instance connection name"
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

variable "database_password_secret_name" {
  description = "Name of Secret Manager secret containing database password"
  type        = string
}

variable "secret_manager_project_id" {
  description = "GCP project ID for Secret Manager (defaults to main project)"
  type        = string
  default     = ""
}

variable "enable_workload_identity" {
  description = "Enable Workload Identity for GKE"
  type        = bool
  default     = true
}

variable "enable_auto_scaling" {
  description = "Enable cluster auto-scaling"
  type        = bool
  default     = true
}

variable "enable_auto_repair" {
  description = "Enable automatic node repair"
  type        = bool
  default     = true
}

variable "enable_auto_upgrade" {
  description = "Enable automatic node upgrades"
  type        = bool
  default     = true
}

variable "enable_http_load_balancing" {
  description = "Enable HTTP Load Balancing add-on"
  type        = bool
  default     = true
}

variable "enable_horizontal_pod_autoscaling" {
  description = "Enable Horizontal Pod Autoscaling add-on"
  type        = bool
  default     = true
}

variable "labels" {
  description = "Additional labels for resources"
  type        = map(string)
  default     = {}
}

variable "admin_email" {
  description = "Administrator email address"
  type        = string
  default     = ""
}

variable "admin_password" {
  description = "Optional initial admin password (skips password reset requirement)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "auto_migrate" {
  description = "Automatically run database migrations on startup"
  type        = bool
  default     = true
}

variable "allowed_origins" {
  description = "List of allowed CORS origins"
  type        = list(string)
  default     = ["*"]
}

variable "additional_env_vars" {
  description = "Additional environment variables"
  type        = map(string)
  default     = {}
}

variable "deploy_kubernetes_resources" {
  description = "Deploy kubernetes resources (namespace, deployment, service, etc). Requires kubernetes/helm providers to be configured at root level. Set to false to only create the GKE cluster."
  type        = bool
  default     = false
}

# ==============================================
# Health Check Configuration
# ==============================================

variable "health_check_path" {
  description = "HTTP path for health checks"
  type        = string
  default     = "/health"
}

variable "health_check_port" {
  description = "Port for health checks"
  type        = number
  default     = 8080
}

variable "liveness_probe_initial_delay" {
  description = "Liveness probe initial delay in seconds"
  type        = number
  default     = 30
}

variable "liveness_probe_period" {
  description = "Liveness probe period in seconds"
  type        = number
  default     = 10
}

variable "readiness_probe_initial_delay" {
  description = "Readiness probe initial delay in seconds"
  type        = number
  default     = 10
}

variable "readiness_probe_period" {
  description = "Readiness probe period in seconds"
  type        = number
  default     = 5
}

# ==============================================
# Scheduled Tasks Configuration
# ==============================================

variable "enable_scheduled_tasks" {
  description = "Enable Cloud Scheduler for scheduled tasks"
  type        = bool
  default     = false
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

variable "app_url" {
  description = "Application URL for scheduled task HTTP triggers (e.g., https://app.example.com). Required when enable_scheduled_tasks is true."
  type        = string
  default     = ""
}
