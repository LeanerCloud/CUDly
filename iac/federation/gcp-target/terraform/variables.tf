variable "project" {
  description = "GCP project ID where the Workload Identity Pool will be created."
  type        = string
}

variable "pool_id" {
  description = "ID for the Workload Identity Pool (must be unique within the project)."
  type        = string
  default     = "cudly-pool"
}

variable "provider_id" {
  description = "ID for the Workload Identity Pool Provider."
  type        = string
  default     = "cudly-provider"
}

variable "provider_type" {
  description = "Type of identity provider: 'aws' or 'oidc'."
  type        = string
  validation {
    condition     = contains(["aws", "oidc"], var.provider_type)
    error_message = "provider_type must be 'aws' or 'oidc'."
  }
}

variable "aws_account_id" {
  description = "AWS account ID. Required when provider_type is 'aws'."
  type        = string
  default     = ""
}

variable "oidc_issuer_uri" {
  description = "OIDC issuer URI. Required when provider_type is 'oidc'. Example: https://login.microsoftonline.com/<tenant_id>/v2.0"
  type        = string
  default     = ""
}

variable "oidc_attribute_mapping" {
  description = "CEL attribute mapping from the OIDC token to Google attributes."
  type        = map(string)
  default = {
    "google.subject" = "assertion.sub"
  }
}

variable "service_account_email" {
  description = "Email of the GCP service account CUDly will impersonate."
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
