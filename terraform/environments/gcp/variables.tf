# ==============================================
# Compute Platform Selection
# ==============================================

variable "compute_platform" {
  description = "Compute platform (cloud-run or gke)"
  type        = string
  default     = "cloud-run"

  validation {
    condition     = contains(["cloud-run", "gke"], var.compute_platform)
    error_message = "compute_platform must be either 'cloud-run' or 'gke'"
  }
}

# ==============================================
# General Configuration
# ==============================================

variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "project_name" {
  description = "Project name"
  type        = string
  default     = "cudly"
}

variable "environment" {
  description = "Environment name"
  type        = string
  default     = "dev"
}

variable "region" {
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

variable "image_uri" {
  description = "Container image URI from Artifact Registry (used when enable_docker_build is false)"
  type        = string
}

variable "enable_docker_build" {
  description = "Enable Docker build module (builds and pushes image during terraform apply). Set to false to use pre-built image_uri instead."
  type        = bool
  default     = false
}

# ==============================================
# Networking Configuration
# ==============================================

variable "subnet_cidr" {
  description = "CIDR range for private subnet"
  type        = string
  default     = "10.0.0.0/24"
}

variable "connector_subnet_cidr" {
  description = "CIDR range for VPC Access Connector (/28 required)"
  type        = string
  default     = "10.8.0.0/28"
}

variable "enable_nat_logging" {
  description = "Enable Cloud NAT logging"
  type        = bool
  default     = false
}

variable "connector_machine_type" {
  description = "VPC Connector machine type (e2-micro, e2-standard-4)"
  type        = string
  default     = "e2-micro"
}

variable "connector_min_instances" {
  description = "VPC Connector minimum instances"
  type        = number
  default     = 2
}

variable "connector_max_instances" {
  description = "VPC Connector maximum instances"
  type        = number
  default     = 3
}

# ==============================================
# Database Configuration
# ==============================================

variable "database_name" {
  description = "Database name"
  type        = string
  default     = "cudly"
}

variable "database_username" {
  description = "Database master username"
  type        = string
  default     = "cudly"
}

variable "database_version" {
  description = "PostgreSQL version"
  type        = string
  default     = "POSTGRES_16"
}

variable "database_tier" {
  description = "Cloud SQL tier (db-custom-[CPUs]-[RAM_MB])"
  type        = string
  default     = "db-custom-1-3840" # 1 vCPU, 3.75 GB RAM
}

variable "database_high_availability" {
  description = "Enable high availability (REGIONAL)"
  type        = bool
  default     = false
}

variable "database_disk_size" {
  description = "Disk size in GB"
  type        = number
  default     = 10
}

variable "database_disk_autoresize" {
  description = "Enable automatic disk resize"
  type        = bool
  default     = true
}

variable "database_backup_enabled" {
  description = "Enable automated backups"
  type        = bool
  default     = true
}

variable "database_point_in_time_recovery" {
  description = "Enable point-in-time recovery"
  type        = bool
  default     = true
}

variable "database_backup_retention_count" {
  description = "Number of backups to retain"
  type        = number
  default     = 7
}

variable "database_query_insights" {
  description = "Enable Query Insights"
  type        = bool
  default     = false
}

variable "database_deletion_protection" {
  description = "Enable deletion protection"
  type        = bool
  default     = false # False in dev for easy teardown
}

variable "database_enable_iam_auth" {
  description = "Enable IAM database authentication"
  type        = bool
  default     = false
}

variable "admin_email" {
  description = "Administrator email for password reset notifications"
  type        = string
}

variable "admin_password" {
  description = "Optional initial admin password (skips password reset requirement)"
  type        = string
  default     = ""
  sensitive   = true
}

# ==============================================
# Cloud Run Configuration
# ==============================================

variable "cloud_run_cpu" {
  description = "CPU allocation (1, 2, 4, 6, 8)"
  type        = string
  default     = "1"
}

variable "cloud_run_memory" {
  description = "Memory allocation (128Mi-32Gi)"
  type        = string
  default     = "512Mi"
}

variable "cloud_run_min_instances" {
  description = "Minimum instances"
  type        = number
  default     = 0
}

variable "cloud_run_max_instances" {
  description = "Maximum instances"
  type        = number
  default     = 10
}

variable "cloud_run_request_timeout" {
  description = "Request timeout in seconds"
  type        = number
  default     = 300
}

variable "cloud_run_allow_unauthenticated" {
  description = "Allow unauthenticated access"
  type        = bool
  default     = true
}

# ==============================================
# Scheduled Tasks (Cloud Scheduler)
# ==============================================

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

# ==============================================
# Database Migration
# ==============================================

variable "auto_migrate" {
  description = "Auto-run database migrations on startup"
  type        = bool
  default     = true
}

# ==============================================
# Additional Configuration
# ==============================================

variable "additional_env_vars" {
  description = "Additional environment variables"
  type        = map(string)
  default     = {}
}

# ==============================================
# GKE (Kubernetes) - Alternative Compute Platform
# ==============================================

variable "gke_kubernetes_version" {
  description = "Kubernetes version for GKE"
  type        = string
  default     = "1.30"
}

variable "gke_zones" {
  description = "GCP zones for GKE node pools (leave empty for regional)"
  type        = list(string)
  default     = []
}

variable "gke_node_count" {
  description = "Initial node count per zone for GKE"
  type        = number
  default     = 1
}

variable "gke_node_machine_type" {
  description = "Machine type for GKE nodes"
  type        = string
  default     = "e2-standard-2"
}

variable "gke_node_disk_size_gb" {
  description = "Disk size for GKE nodes in GB"
  type        = number
  default     = 50
}

variable "gke_min_node_count" {
  description = "Minimum node count for GKE auto-scaling (per zone)"
  type        = number
  default     = 1
}

variable "gke_max_node_count" {
  description = "Maximum node count for GKE auto-scaling (per zone)"
  type        = number
  default     = 3
}

variable "gke_enable_auto_scaling" {
  description = "Enable GKE cluster auto-scaling"
  type        = bool
  default     = true
}

variable "gke_enable_auto_repair" {
  description = "Enable automatic node repair"
  type        = bool
  default     = true
}

variable "gke_enable_auto_upgrade" {
  description = "Enable automatic node upgrades"
  type        = bool
  default     = true
}

variable "gke_enable_workload_identity" {
  description = "Enable Workload Identity for GKE"
  type        = bool
  default     = true
}

# ==============================================
# Frontend (Cloud CDN + Load Balancer) Configuration
# ==============================================

variable "enable_frontend" {
  description = "Enable frontend deployment (Cloud CDN + Load Balancer). Set to false for API-only deployments, when the frontend is hosted externally (e.g. Vercel/Netlify), or to reduce costs in dev/test environments."
  type        = bool
  default     = true
}

variable "enable_frontend_build" {
  description = "Enable frontend build and deployment (npm build and file uploads)"
  type        = bool
  default     = true
}

variable "frontend_bucket_name" {
  description = "Cloud Storage bucket name for frontend files (globally unique)"
  type        = string
  default     = ""
}

variable "subdomain_zone_name" {
  description = "Cloud DNS subdomain zone name to create (e.g., cudly.example.com). Leave empty to skip zone creation."
  type        = string
  default     = ""
}

variable "frontend_domain_names" {
  description = "Custom domain names for the frontend Load Balancer (e.g., [\"app.cudly.example.com\"])"
  type        = list(string)
  default     = []
}

variable "enable_cloud_armor" {
  description = "Enable Cloud Armor security policy (DDoS protection, rate limiting)"
  type        = bool
  default     = true
}
