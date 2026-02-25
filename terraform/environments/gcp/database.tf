# ==============================================
# Database
# ==============================================

module "database" {
  source = "../../modules/database/gcp"

  project_id   = var.project_id
  service_name = local.service_name
  environment  = var.environment
  region       = var.region

  # Database configuration
  database_version = var.database_version
  database_name    = var.database_name
  master_username  = var.database_username
  # TODO: revert to module.secrets.database_password_value after state stabilizes
  master_password  = "IMPORT_PLACEHOLDER"

  # Cloud SQL configuration
  tier              = var.database_tier
  high_availability = var.database_high_availability
  disk_size         = var.database_disk_size
  disk_autoresize   = var.database_disk_autoresize

  # Networking (private IP via VPC peering)
  vpc_network_id   = module.networking.network_id
  enable_public_ip = false

  # Backups
  backup_enabled         = var.database_backup_enabled
  point_in_time_recovery = var.database_point_in_time_recovery
  backup_retention_count = var.database_backup_retention_count

  # Monitoring
  query_insights_enabled = var.database_query_insights

  # Protection
  deletion_protection = var.database_deletion_protection

  # IAM authentication
  enable_iam_authentication       = var.database_enable_iam_auth
  cloud_run_service_account_email = null # Avoid circular dependency

  labels = local.common_labels

  depends_on = [module.networking, module.secrets]
}
