# ==============================================
# Secrets Management
# ==============================================

module "secrets" {
  source = "../../modules/secrets/aws"

  stack_name  = local.stack_name
  environment = var.environment
  region      = var.region

  # Generate random password for dev (in prod, you'd provide this via tfvars)
  database_password = null # Will be auto-generated

  recovery_window_days  = var.secret_recovery_window_days
  create_jwt_secret     = true
  create_session_secret = true

  # Optional: Add additional secrets
  additional_secrets = var.additional_secrets

  # Secret rotation disabled in dev (enable in prod)
  enable_secret_rotation = false

  tags = local.common_tags
}
