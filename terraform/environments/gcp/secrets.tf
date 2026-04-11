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

  additional_secrets = {
    "credential-encryption-key" = local.credential_encryption_key
  }

  # IAM permissions for compute service account are handled in compute modules
  # Setting to null to avoid circular dependency
  cloud_run_service_account_email = null

  labels = local.common_labels
}
