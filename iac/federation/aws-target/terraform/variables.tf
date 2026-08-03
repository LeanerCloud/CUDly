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
    OPTIONAL. TLS thumbprints (40-character hex SHA-1) of the top intermediate
    CA that signed the certificate of the issuer's JWKS endpoint.

    LEAVE THIS EMPTY unless that certificate does not chain to a publicly
    trusted CA. An empty list omits the argument entirely, and IAM then
    retrieves the correct thumbprint itself.

    AWS verifies the JWKS endpoint's TLS certificate against its own library of
    trusted root CAs, and falls back to these thumbprints only when that
    certificate does not chain to one of them, when AWS cannot retrieve the
    certificate, or when the endpoint requires TLS 1.3. Both documented
    issuers (login.microsoftonline.com and accounts.google.com) present
    publicly trusted certificates, so for them the value is read only if one of
    those fallback conditions applies, whatever it is set to.

    When the issuer's discovery endpoint and jwks_uri are on different hosts,
    AWS requires the thumbprints of BOTH; supply both entries in that case.
  EOT
  type        = list(string)
  default     = []

  validation {
    condition = alltrue([
      for t in var.thumbprint_list : can(regex("^[0-9a-fA-F]{40}$", t))
    ])
    error_message = "Each thumbprint in thumbprint_list must be a 40-character SHA-1 hex string."
  }

  # The all-zeros placeholder used to be this variable's default, guarded by an
  # issuer allowlist justified as "the all-zeros value bypasses the CA-chain
  # check entirely". That justification was wrong in both directions, so the
  # guard is now unconditional and the default is empty.
  #
  # All-zeros bypasses nothing: it is simply not the SHA-1 of any certificate.
  # On the primary path AWS does not read it at all, and on the fallback path it
  # matches nothing, so role assumption fails outright. Restricting it to Azure AD and
  # Google was equally beside the point -- the thumbprint is unread for every
  # publicly-trusted issuer, not only those two, and setting it for a
  # private-CA issuer breaks that issuer no matter which one it is.
  validation {
    condition = alltrue([
      for t in var.thumbprint_list : t != "0000000000000000000000000000000000000000"
    ])
    error_message = "thumbprint_list must not contain the all-zeros placeholder. It is not the fingerprint of any certificate, so whenever AWS actually falls back to thumbprint verification it matches nothing and role assumption fails. Leave thumbprint_list empty to have IAM retrieve the issuer's real thumbprint, or supply that thumbprint."
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
