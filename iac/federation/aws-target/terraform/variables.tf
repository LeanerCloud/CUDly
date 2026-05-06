variable "oidc_issuer_url" {
  description = <<-EOT
    OIDC issuer URL of the source identity provider. Must be https:// and must
    not contain a trailing slash — AWS IAM condition keys are case-sensitive
    and the trust-policy host string must match the issuer URL exactly.
    Azure AD: https://login.microsoftonline.com/<tenant_id>/v2.0
    GCP:      https://accounts.google.com
  EOT
  type        = string

  validation {
    condition     = can(regex("^https://[^/]+(/[^/].*[^/]|/[^/]+)?$", var.oidc_issuer_url)) && !endswith(var.oidc_issuer_url, "/")
    error_message = "oidc_issuer_url must start with https:// and must not end with a trailing slash."
  }
}

variable "oidc_audience" {
  description = <<-EOT
    Expected audience (aud) in the OIDC token.
    Azure: api://<client_id>  or  <client_id>
    GCP:   https://iam.googleapis.com/projects/.../providers/...
    Defaults to sts.amazonaws.com when empty.
  EOT
  type        = string
  default     = ""
}

variable "oidc_subject_claim" {
  description = "Optional subject (sub) claim to restrict trust. Leave empty to allow any subject."
  type        = string
  default     = ""
}

variable "role_name" {
  description = "Name of the IAM role CUDly will assume."
  type        = string
  default     = "CUDly-WIF"
}

variable "thumbprint_list" {
  description = <<-EOT
    TLS root CA thumbprints for the OIDC provider (40-character lowercase hex SHA-1).
    AWS auto-validates well-known providers (Azure AD, Google); for those the all-zeros
    placeholder works. For any other issuer you MUST supply the real root CA SHA-1
    thumbprint — the module rejects all-zeros for custom issuers via the second
    validation block.
  EOT
  type        = list(string)
  default     = ["0000000000000000000000000000000000000000"]

  validation {
    condition     = length(var.thumbprint_list) > 0
    error_message = "thumbprint_list must contain at least one thumbprint."
  }

  validation {
    condition = alltrue([
      for t in var.thumbprint_list : can(regex("^[0-9a-fA-F]{40}$", t))
    ])
    error_message = "Each thumbprint in thumbprint_list must be a 40-character SHA-1 hex string."
  }
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
