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

variable "cudly_api_url" {
  description = "CUDly API base URL for automatic account registration. Leave empty to skip registration."
  type        = string
  default     = ""
}

variable "account_name" {
  description = "Human-readable name for this account in CUDly."
  type        = string
  default     = ""
}

variable "contact_email" {
  description = "Contact email for registration notifications."
  type        = string
  default     = ""
}
