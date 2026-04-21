# ==============================================
# Credential Encryption Key (auto-generate if not provided)
# ==============================================

resource "random_bytes" "credential_encryption_key" {
  count  = var.credential_encryption_key == "" ? 1 : 0
  length = 32
}

locals {
  credential_encryption_key = var.credential_encryption_key != "" ? var.credential_encryption_key : (
    length(random_bytes.credential_encryption_key) > 0 ? random_bytes.credential_encryption_key[0].hex : ""
  )
}

# ==============================================
# Secrets Management
# ==============================================

module "secrets" {
  source = "../../modules/secrets/gcp"

  project_id   = var.project_id
  service_name = local.service_name
  environment  = var.environment

  # Generate random password for dev (in prod, provide this via tfvars)
  database_password = null # Will be auto-generated
  admin_password    = var.admin_password

  create_jwt_secret     = true
  create_session_secret = true

  # Pass the credential-encryption key through the dedicated `sensitive`
  # variable so it never leaks into plan output or unencrypted state. The
  # `additional_secrets` path was previously used here but couldn't be
  # marked sensitive (it needs `for_each` over its keys), so the value was
  # rendered in plan diffs.
  credential_encryption_key = local.credential_encryption_key

  # IAM permissions for compute service account are handled in compute modules
  # via per-secret bindings (see `additional_secret_accessor_ids` in
  # compute.tf). Setting to null avoids the circular module dependency.
  cloud_run_service_account_email = null

  labels = local.common_labels
}

# ==============================================
# Import recovery: credential-encryption-key secret
# ==============================================
#
# A prior failed apply created the Secret Manager secret
# `<service_name>-credential-encryption-key` outside Terraform state. The
# next apply then hit `googleapi: Error 409: Secret already exists.`
# because the resource was present in GCP but not tracked.
#
# This import block pulls the existing secret into state on the next
# apply. It is idempotent — Terraform skips the import when the resource
# is already in state. Remove this block after a successful apply that
# reconciles the imported resource.
#
# The secret version is NOT imported: the existing version may not match
# `local.credential_encryption_key`, so Terraform will create a new
# version alongside the imported resource, which is the desired
# behaviour (secret versions are immutable and append-only).
import {
  to = module.secrets.google_secret_manager_secret.credential_encryption_key[0]
  id = "projects/${var.project_id}/secrets/${local.service_name}-credential-encryption-key"
}
