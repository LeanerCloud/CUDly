# GCP Frontend Module Variables

variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "project_name" {
  description = "Project name for resource naming"
  type        = string
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
}

variable "region" {
  description = "GCP region for regional resources"
  type        = string
}

variable "location" {
  description = "GCP location for bucket (region or multi-region like US, EU)"
  type        = string
  default     = "US"
}

variable "bucket_name" {
  description = "Cloud Storage bucket name (must be globally unique)"
  type        = string
}

variable "cloud_run_service_name" {
  description = "Cloud Run service name for API backend"
  type        = string
}

variable "domain_names" {
  description = "Custom domain names for the load balancer"
  type        = list(string)
  default     = []
}

variable "subdomain_zone_name" {
  description = "Cloud DNS subdomain zone name to create (e.g., cudly.leanercloud.com). Leave empty to skip zone creation."
  type        = string
  default     = ""
}

variable "enable_cloud_armor" {
  description = "Enable Cloud Armor security policy"
  type        = bool
  default     = true
}

variable "api_log_sample_rate" {
  description = "Sample rate for API backend logs (0.0 to 1.0)"
  type        = number
  default     = 1.0
}

variable "enable_monitoring" {
  description = "Enable monitoring and alerting"
  type        = bool
  default     = true
}

variable "notification_channels" {
  description = "List of notification channel IDs for alerts"
  type        = list(string)
  default     = []
}

variable "labels" {
  description = "Additional labels for resources"
  type        = map(string)
  default     = {}
}

variable "enable_frontend_build" {
  description = "Enable frontend build and deployment (set to false to skip npm build and file uploads)"
  type        = bool
  default     = true
}

variable "frontend_path" {
  description = "Path to frontend directory relative to Terraform root (default assumes terraform/environments/<provider> structure)"
  type        = string
  default     = "../../../frontend"
}
