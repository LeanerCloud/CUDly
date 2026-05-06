variable "enable_archera" {
  description = <<-EOT
    When true, provision the Archera cross-account IAM role and read-only
    cost/commitment policy so Archera can underwrite commitment-overuse
    insurance. Leave false (default) unless you are enrolled with Archera.
    PROVISIONAL — confirm scope with Archera before enabling.
  EOT
  type        = bool
  default     = false
}

variable "archera_aws_account_id" {
  description = "Archera's AWS account ID. Obtain from your Archera onboarding documentation. Required when enable_archera = true."
  type        = string
  default     = ""

  validation {
    condition     = var.archera_aws_account_id == "" || can(regex("^[0-9]{12}$", var.archera_aws_account_id))
    error_message = "archera_aws_account_id must be a 12-digit AWS account ID."
  }
}

variable "archera_external_id" {
  description = "External ID for confused-deputy protection on the Archera cross-account role. Obtain from Archera during onboarding. Required when enable_archera = true."
  type        = string
  default     = ""
  sensitive   = true
}

variable "enable_archera_purchase_actions" {
  description = <<-EOT
    When true (and enable_archera = true), attach the Archera purchase policy
    that allows RI/SP writes. Only enable after confirming Archera requires
    customer approval before executing purchases. Default false.
  EOT
  type        = bool
  default     = false
}

variable "managed_by_tag" {
  description = "Value for the ManagedBy tag on all Archera resources."
  type        = string
  default     = "cudly-archera-module"
}
