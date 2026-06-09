output "instance_name" {
  description = "Cloud SQL instance name"
  value       = google_sql_database_instance.main.name
}

output "instance_connection_name" {
  description = "Cloud SQL instance connection name (project:region:instance)"
  value       = google_sql_database_instance.main.connection_name
}

output "instance_self_link" {
  description = "Cloud SQL instance self link"
  value       = google_sql_database_instance.main.self_link
}

output "database_name" {
  description = "Database name"
  value       = google_sql_database.main.name
}

output "master_username" {
  description = "Master username"
  value       = google_sql_user.main.name
  sensitive   = true
}

output "private_ip_address" {
  description = "Private IP address of the instance"
  value       = google_sql_database_instance.main.private_ip_address
}

output "public_ip_address" {
  description = "Public IP address of the instance (if enabled)"
  value       = var.enable_public_ip ? google_sql_database_instance.main.public_ip_address : null
}

output "read_replica_connection_name" {
  description = "Read replica connection name (if enabled)"
  value       = var.enable_read_replica ? google_sql_database_instance.read_replica[0].connection_name : null
}

output "read_replica_private_ip" {
  description = "Read replica private IP (if enabled)"
  value       = var.enable_read_replica ? google_sql_database_instance.read_replica[0].private_ip_address : null
}

output "connection_details" {
  description = "Database connection details"
  value = {
    host            = google_sql_database_instance.main.private_ip_address
    connection_name = google_sql_database_instance.main.connection_name
    port            = 5432
    database        = google_sql_database.main.name
    username        = google_sql_user.main.name
    ssl_mode        = "require"
  }
  sensitive = true
}
