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
    Leaving this empty does not skip audience matching: both the trust policy
    condition and the OIDC provider client ID list fall back to the literal
    string sts.amazonaws.com.
  EOT
  type        = string
  default     = ""

  # Same IAM policy-variable expansion hazard as oidc_subject_claim: an
  # audience of ${accounts.google.com:aud} expands to the token's own aud
  # claim and matches every token. Audience is the only other control on this
  # trust policy, so it gets the same guard, including the whitespace
  # rejection that keeps it equivalent to the CloudFormation AllowedPattern
  # ^$|^[^\s*$]+$. An empty audience still passes (it contains none of the
  # rejected characters) and falls back to sts.amazonaws.com, matching that
  # pattern's ^$ branch without needing an explicit empty-string clause.
  validation {
    condition     = !can(regex("[\\s*$]", var.oidc_audience))
    error_message = "oidc_audience must not contain whitespace, '$' or '*'. IAM expands $${...} policy variables inside Condition values, so a value such as $${accounts.google.com:aud} would expand to the token's own aud claim and match every token. '*' is compared literally by StringEquals."
  }
}

variable "oidc_subject_claim" {
  description = <<-EOT
    Subject (sub) claim used to restrict OIDC trust to a specific identity.
    Azure AD managed identity: the object ID of the managed identity.
    GCP service account:       the service account's numeric unique ID
                               (typically 21 digits; Google documents it as a
                               numeric string without guaranteeing a length).
                               That is the sub claim accounts.google.com
                               actually issues; it is not the SA email. The
                               system:serviceaccount:<namespace>:<name> form
                               is the Kubernetes subject format and belongs to
                               a cluster OIDC issuer, not accounts.google.com.
    This variable is required and must not be empty. An empty value would
    allow any principal in the same OIDC provider tenant to assume this role.
  EOT
  type        = string

  validation {
    condition     = var.oidc_subject_claim != null && length(trimspace(var.oidc_subject_claim)) > 0
    error_message = "oidc_subject_claim must be set to a non-empty subject claim. Leaving it empty would allow any principal in the OIDC provider tenant to assume this role."
  }

  # assume_role_policy is an IAM policy document, and IAM expands ${...} policy
  # variables inside Condition values. A subject of ${accounts.google.com:sub}
  # expands to the presented token's own sub claim, making the condition a
  # tautology that matches every identity the issuer can mint. Reject '$'
  # outright rather than emit a trust policy that only looks restricted.
  # Whitespace is rejected in the same expression so this stays byte-for-byte
  # equivalent to the CloudFormation AllowedPattern ^[^\s*$]+$ that the
  # template's ConstraintDescription claims to mirror. No real OIDC subject
  # (GCP numeric IDs, Azure GUIDs, system:serviceaccount:, GitHub, GitLab,
  # SPIFFE, Auth0, Bitbucket) contains whitespace.
  validation {
    condition     = !can(regex("[\\s*$]", var.oidc_subject_claim))
    error_message = "oidc_subject_claim must not contain whitespace, '$' or '*'. IAM expands $${...} policy variables inside Condition values, so a value such as $${accounts.google.com:sub} would expand to the token's own sub claim and match every identity the issuer can mint. '*' is compared literally by StringEquals and would silently produce a role nobody can assume."
  }
}

variable "role_name" {
  description = "Name of the IAM role CUDly will assume."
  type        = string
  default     = "CUDly-WIF"
}

variable "thumbprint_list" {
  description = <<-EOT
    TLS root CA thumbprints for the OIDC provider (40-character hex SHA-1).
    AWS auto-validates well-known providers (Azure AD, Google); for those the
    all-zeros placeholder is intentional and accepted.
    For any other issuer you MUST supply the real root CA SHA-1 thumbprint.
    Supplying the all-zeros placeholder for a custom issuer is rejected by this
    module to prevent operators from silently bypassing CA-chain validation.
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

  # Guard against copy-paste of the all-zeros placeholder for custom OIDC
  # issuers. AWS natively validates Azure AD and Google endpoints, so
  # all-zeros is safe for those. Any other issuer URL requires a real CA
  # thumbprint; the all-zeros value bypasses the CA-chain check entirely.
  validation {
    condition = !(
      length(var.thumbprint_list) == 1 &&
      var.thumbprint_list[0] == "0000000000000000000000000000000000000000" &&
      !startswith(var.oidc_issuer_url, "https://login.microsoftonline.com/") &&
      !startswith(var.oidc_issuer_url, "https://accounts.google.com")
    )
    error_message = "thumbprint_list is the all-zeros placeholder, which is only safe for Azure AD (login.microsoftonline.com) and Google (accounts.google.com) issuers that AWS validates natively. For any other OIDC issuer you must supply the real root CA SHA-1 thumbprint."
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
