variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
}

variable "function_name" {
  description = "Name of the Cloud Function"
  type        = string
  default     = "cudly-cleanup"
}

variable "db_host" {
  description = "Database host (Cloud SQL connection name or private IP)"
  type        = string
}

variable "db_password_secret_id" {
  description = "Secret Manager secret ID containing the database password"
  type        = string
}

variable "vpc_connector" {
  description = "VPC connector for private Cloud SQL access"
  type        = string
  default     = ""
}

variable "schedule" {
  description = "Cloud Scheduler schedule (cron format)"
  type        = string
  default     = "0 2 * * *"
}

variable "labels" {
  description = "Labels to apply to all resources"
  type        = map(string)
  default     = {}
}
