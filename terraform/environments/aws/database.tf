# ==============================================
# Database
# ==============================================

module "database" {
  source = "../../modules/database/aws"

  stack_name = local.stack_name

  # Database configuration
  engine_version             = var.database_engine_version
  database_name              = var.database_name
  master_username            = var.database_username
  master_password_secret_arn = module.secrets.database_password_secret_arn # Use secrets module password

  # Aurora Serverless v2 scaling
  min_capacity = var.database_min_capacity
  max_capacity = var.database_max_capacity

  # Networking
  vpc_id             = module.networking.vpc_id
  vpc_cidr           = var.vpc_cidr
  private_subnet_ids = module.networking.private_subnet_ids

  # RDS Proxy (critical for Lambda)
  enable_rds_proxy = var.compute_platform == "lambda"

  # Backups
  backup_retention_days = var.database_backup_retention_days

  # Monitoring
  performance_insights_enabled = var.database_performance_insights

  # Protection
  deletion_protection = var.database_deletion_protection
  skip_final_snapshot = var.database_skip_final_snapshot

  # Admin user configuration
  admin_email = var.admin_email

  tags = local.common_tags

  depends_on = [module.networking, module.secrets]
}
