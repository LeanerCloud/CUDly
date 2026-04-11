variable "source_account_id" {
  description = "AWS account ID where CUDly runs (the source account that will assume this role)."
  type        = string

  validation {
    condition     = can(regex("^[0-9]{12}$", var.source_account_id))
    error_message = "source_account_id must be exactly 12 digits."
  }
}

variable "external_id" {
  description = "External ID for confused deputy protection. Auto-generated if empty."
  type        = string
  default     = ""
  sensitive   = true
}

variable "role_name" {
  description = "Name of the IAM role created in this target account."
  type        = string
  default     = "CUDly-CrossAccount"
}

variable "cudly_execution_role_arn" {
  description = <<-EOT
    ARN of the IAM role that CUDly uses to execute (e.g. the Lambda execution role or
    the ECS task role in the source account). When provided, the trust policy is scoped
    to this specific principal instead of the entire source account (:root).
    Leave empty to allow any principal in source_account_id to assume this role.
  EOT
  type        = string
  default     = ""
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
