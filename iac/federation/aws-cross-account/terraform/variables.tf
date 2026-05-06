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

variable "enable_org_discovery" {
  description = <<-EOT
    Grant organizations:ListAccounts and organizations:DescribeOrganization so
    CUDly can enumerate all accounts in the AWS Organization through this role.
    Set this only when this role is deployed in an Organizations management or
    delegated-administrator account. Leave false in member-account deployments.
  EOT
  type        = bool
  default     = false
}

# ---------------------------------------------------------------------------
# Archera Insurance integration (opt-in, provisional)
# ---------------------------------------------------------------------------

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
