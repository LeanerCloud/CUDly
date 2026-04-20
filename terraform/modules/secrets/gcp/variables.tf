variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "service_name" {
  description = "Service name"
  type        = string
}

variable "environment" {
  description = "Environment name (dev/staging/prod)"
  type        = string
}

variable "database_password" {
  description = "Database password (if null, will be auto-generated)"
  type        = string
  default     = null
  sensitive   = true
}

variable "admin_password" {
  description = "Admin password (if null, a random password will be generated)"
  type        = string
  default     = null
  sensitive   = true
}

variable "create_admin_password_secret" {
  description = "Create admin password secret in Secret Manager"
  type        = bool
  default     = true
}

variable "create_jwt_secret" {
  description = "Create JWT signing secret"
  type        = bool
  default     = true
}

variable "create_session_secret" {
  description = "Create session encryption secret"
  type        = bool
  default     = true
}

variable "sendgrid_api_key" {
  description = "SendGrid API key for email sending (if null, secret created with placeholder)"
  type        = string
  default     = null
  sensitive   = true
}

variable "create_sendgrid_secret" {
  description = "Create SendGrid API key secret (even if key is null, for manual population later)"
  type        = bool
  default     = true
}

variable "create_scheduled_task_secret" {
  description = "Create scheduled task authentication secret"
  type        = bool
  default     = true
}

variable "additional_secrets" {
  description = <<-EOT
    Map of additional secret values to create (keys are not sensitive,
    values are). The map itself cannot be marked `sensitive = true`
    because Terraform `for_each` requires the key set to be non-sensitive,
    and unwrapping with `nonsensitive(keys(...))` only works cleanly when
    the values are uniformly typed strings (which is what we want here).

    Use this for low-risk auxiliary secrets only. Sensitive material that
    must never appear in plan output or unencrypted state — currently the
    credential encryption key — should be passed via the dedicated
    `credential_encryption_key` variable below, which is `sensitive = true`
    end-to-end.
  EOT
  type        = map(string)
  default     = {}
}

variable "credential_encryption_key" {
  description = <<-EOT
    Cleartext credential-encryption key (AES-256, hex-encoded — 64 hex
    chars or empty). When non-empty, the module creates a dedicated
    Secret Manager secret + version named `<service>-credential-
    encryption-key` and exposes its IDs via outputs. When empty, no
    secret is created — pass the key in via this variable rather than
    `additional_secrets` so it stays redacted in plan output and state.

    Marked `sensitive = true` so the value never leaks to plan diffs
    or `terraform show` output. Validation enforces 64-hex-char or empty.
  EOT
  type        = string
  default     = ""
  sensitive   = true

  validation {
    condition     = var.credential_encryption_key == "" || can(regex("^[0-9a-fA-F]{64}$", var.credential_encryption_key))
    error_message = "credential_encryption_key must be empty or a 64-character hex string (AES-256)."
  }
}

variable "cloud_run_service_account_email" {
  description = "Cloud Run service account email for IAM permissions"
  type        = string
  default     = null
}

variable "labels" {
  description = "Labels to apply to resources"
  type        = map(string)
  default     = {}
}
