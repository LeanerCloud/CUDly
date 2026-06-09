variable "api_base_url" {
  description = "Base URL of the deployed API (no trailing slash)"
  type        = string
}

variable "provider_name" {
  description = "Cloud provider name (aws, gcp, azure)"
  type        = string

  validation {
    condition     = contains(["aws", "gcp", "azure"], var.provider_name)
    error_message = "provider_name must be one of: aws, gcp, azure"
  }
}
