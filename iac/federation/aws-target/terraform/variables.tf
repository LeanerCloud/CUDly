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
    TLS root CA thumbprints for the OIDC provider.
    AWS auto-validates well-known providers (Azure AD, Google); for those a placeholder works.
    For custom issuers, provide the real root CA SHA-1 thumbprint.
  EOT
  type        = list(string)
  default     = ["0000000000000000000000000000000000000000"]
}
