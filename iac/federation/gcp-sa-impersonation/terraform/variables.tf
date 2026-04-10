variable "project_id" {
  description = "GCP project ID of the target account."
  type        = string
}

variable "service_account_email" {
  description = "Email of the service account in the target project that CUDly will use."
  type        = string
}

variable "source_service_account" {
  description = "Full email of the service account that CUDly runs as on the source GCP project."
  type        = string
}
