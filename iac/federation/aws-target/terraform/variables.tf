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
  description = <<-EOT
    Subject (sub) claim used to restrict OIDC trust to a specific identity.
    Azure AD managed identity: the object ID of the managed identity.
    GCP service account:       the service account email in the form
                               system:serviceaccount:<project>:<sa-email>.
    This variable is required and must not be empty. An empty value would
    allow any principal in the same OIDC provider tenant to assume this role.
  EOT
  type        = string

  validation {
    condition     = var.oidc_subject_claim != null && length(trimspace(var.oidc_subject_claim)) > 0
    error_message = "oidc_subject_claim must be set to a non-empty subject claim. Leaving it empty would allow any principal in the OIDC provider tenant to assume this role."
  }
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
