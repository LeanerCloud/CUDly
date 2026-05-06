variable "enable_archera" {
  description = <<-EOT
    When true, provision the Archera custom IAM role and role binding so
    Archera can underwrite commitment-overuse insurance. Leave false (default)
    unless you are enrolled with Archera.
    PROVISIONAL — confirm scope with Archera before enabling.
  EOT
  type        = bool
  default     = false
}

variable "project_id" {
  description = "GCP project ID to provision the Archera custom role in. Required when enable_archera = true."
  type        = string
}

variable "archera_gcp_service_account" {
  description = "Full service account email of the SA Archera provides during onboarding, e.g. archera-integration@archera-prod.iam.gserviceaccount.com. Required when enable_archera = true."
  type        = string
  default     = ""

  validation {
    condition     = !var.enable_archera || can(regex("^[a-z][a-z0-9-]*@[a-z][a-z0-9-]*\\.iam\\.gserviceaccount\\.com$", var.archera_gcp_service_account))
    error_message = "archera_gcp_service_account must be a valid GCP service account email (e.g. name@project.iam.gserviceaccount.com) when enable_archera = true."
  }
}

variable "enable_archera_purchase_actions" {
  description = <<-EOT
    When true (and enable_archera = true), include CUD purchase permissions in
    the Archera custom role. Only enable after confirming Archera requires
    customer approval before executing purchases. Default false.
  EOT
  type        = bool
  default     = false
}
