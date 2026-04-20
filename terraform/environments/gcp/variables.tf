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
  default     = ""
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
  description = <<-EOT
    Enable Cloud SQL deletion protection. Default is true so production
    cannot be wiped by an accidental destroy. Disabling requires two
    applies (one to flip the flag, one to destroy) by design of the
    Cloud SQL provider — dev tfvars override to false to keep ephemeral
    teardown ergonomic.
  EOT
  type        = bool
  default     = true
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
  description = <<-EOT
    Whether to expose the Cloud Run service publicly without IAM-level
    authentication. The default is `false` (least-privilege): only callers
    with the `roles/run.invoker` IAM binding can hit the URL. Application-
    layer auth (sessions, RBAC, OIDC federation) still runs on top.

    Set to `true` to allow direct browser access on the *.run.app URL —
    useful for dev/preview environments where the only consumer is the
    bundled frontend hitting the Cloud Run URL directly without an HTTPS
    load balancer in front.

    NOTE: For production, prefer `false` plus an external HTTPS load
    balancer with Cloud Armor (see `cloud_run_ingress` semantics in the
    cloud-run module). The current default does not flip the module's
    `ingress` default to `INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER` because
    the supporting LB is not provisioned by this environment yet — when
    that lands, this default and the ingress default should move together.
  EOT
  type        = bool
  default     = false
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

variable "credential_encryption_key" {
  description = "AES-256-GCM key for account credential encryption (64-char hex = 32 bytes). Auto-generated when empty."
  type        = string
  sensitive   = true
  default     = ""
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
# Frontend (Load Balancer) Configuration
# ==============================================

variable "enable_cdn" {
  description = "Enable CDN (Global Load Balancer) for custom domain and edge caching. When false, the Cloud Run service URL serves the frontend directly. Only needed when using custom domains."
  type        = bool
  default     = false
}

variable "subdomain_zone_name" {
  description = "Cloud DNS subdomain zone name to create (e.g., cudly.example.com). Leave empty to skip zone creation."
  type        = string
  default     = ""
}

variable "frontend_domain_names" {
  description = <<-EOT
    Custom domain names for the frontend Load Balancer (e.g., ["app.cudly.example.com"]).
    The first entry is also used to build the CORS_ALLOWED_ORIGIN env var exposed to
    the backend. Must not contain a bare "*" — wildcard CORS is never acceptable for
    an authenticated API surface. When this list is empty, CORS falls back to
    http://localhost:3000 (safe for local dev).
  EOT
  type        = list(string)
  default     = []

  validation {
    condition     = !contains(var.frontend_domain_names, "*")
    error_message = "frontend_domain_names must not contain \"*\" — bare wildcard CORS is never safe. Supply explicit origins per environment."
  }

  validation {
    condition = alltrue([
      for d in var.frontend_domain_names : !strcontains(d, " ")
    ])
    error_message = "Entries in frontend_domain_names must not contain whitespace."
  }
}

variable "enable_cloud_armor" {
  description = "Enable Cloud Armor security policy (DDoS protection, rate limiting)"
  type        = bool
  default     = true
}

variable "billing_account_id" {
  description = "GCP billing account ID. Required to grant billing.viewer so the app can read SKU prices. Leave empty to skip the IAM binding."
  type        = string
  default     = ""
}

variable "max_account_parallelism" {
  description = "Maximum number of cloud accounts to process in parallel during plan fan-out"
  type        = number
  default     = 10
}
