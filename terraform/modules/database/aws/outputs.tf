output "cluster_endpoint" {
  description = "Aurora cluster endpoint"
  value       = aws_rds_cluster.main.endpoint
}

output "cluster_reader_endpoint" {
  description = "Aurora cluster reader endpoint"
  value       = aws_rds_cluster.main.reader_endpoint
}

output "proxy_endpoint" {
  description = "RDS Proxy endpoint (use this for Lambda)"
  value       = var.enable_rds_proxy ? aws_db_proxy.main[0].endpoint : null
}

output "database_name" {
  description = "Name of the created database"
  value       = aws_rds_cluster.main.database_name
}

output "master_username" {
  description = "Master username"
  value       = aws_rds_cluster.main.master_username
  sensitive   = true
}

output "password_secret_arn" {
  description = "ARN of the Secrets Manager secret containing the database password"
  value       = local.db_password_secret_arn
}

output "password_secret_name" {
  description = "Name of the Secrets Manager secret containing the database password"
  value       = var.master_password_secret_arn != null ? data.aws_secretsmanager_secret.existing_password[0].name : aws_secretsmanager_secret.db_password[0].name
}

output "security_group_id" {
  description = "Security group ID for the Aurora cluster"
  value       = aws_security_group.aurora.id
}

output "connection_details" {
  description = "Database connection details"
  value = {
    host                = var.enable_rds_proxy ? aws_db_proxy.main[0].endpoint : aws_rds_cluster.main.endpoint
    port                = 5432
    database            = aws_rds_cluster.main.database_name
    username            = aws_rds_cluster.main.master_username
    password_secret_arn = local.db_password_secret_arn
    ssl_mode            = "require"
  }
  sensitive = true
}

output "admin_email" {
  description = "Email address of the admin user (created without password - must use password reset)"
  value       = var.admin_email
}
