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
  default     = "1.28"
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

variable "deploy_kubernetes_resources" {
  description = "Deploy kubernetes resources (namespace, deployment, service, etc). Requires kubernetes/helm providers to be configured at root level. Set to false to only create the GKE cluster."
  type        = bool
  default     = false
}
