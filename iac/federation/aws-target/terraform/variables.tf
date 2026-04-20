variable "oidc_issuer_url" {
  description = <<-EOT
    OIDC issuer URL of the source identity provider.
    Azure AD: https://login.microsoftonline.com/<tenant_id>/v2.0
    GCP:      https://accounts.google.com
  EOT
  type        = string

  validation {
    condition     = startswith(var.oidc_issuer_url, "https://")
    error_message = "oidc_issuer_url must start with https://"
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
