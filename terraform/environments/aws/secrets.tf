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
  admin_password    = var.admin_password

  recovery_window_days  = var.secret_recovery_window_days
  create_jwt_secret     = true
  create_session_secret = true

  # Optional: Add additional secrets
  additional_secrets = var.additional_secrets

  # Credential encryption key for multi-account support
  create_credential_encryption_key = true
  credential_encryption_key        = var.credential_encryption_key

  # Secret rotation defaults to false to keep dev ergonomic, but can now be
  # flipped per environment via var.enable_secret_rotation. Production tfvars
  # should set this true and supply rds_cluster_id for the database-password
  # rotation Lambda to target. See variables.tf for the rotation prerequisites.
  enable_secret_rotation = var.enable_secret_rotation
  rds_cluster_id         = var.rds_cluster_id_for_rotation

  tags = local.common_tags
}
