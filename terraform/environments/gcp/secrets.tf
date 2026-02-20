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

  create_jwt_secret     = true
  create_session_secret = true

  # IAM permissions for compute service account are handled in compute modules
  # Setting to null to avoid circular dependency
  cloud_run_service_account_email = null

  labels = local.common_labels
}
