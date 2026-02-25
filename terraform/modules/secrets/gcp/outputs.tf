output "database_password_secret_id" {
  description = "Secret Manager secret ID for database password"
  value       = google_secret_manager_secret.database_password.secret_id
}

output "database_password_secret_name" {
  description = "Full secret name for database password"
  value       = google_secret_manager_secret.database_password.name
}

output "database_password_value" {
  description = "Database password value (use with caution)"
  value       = google_secret_manager_secret_version.database_password.secret_data
  sensitive   = true
}

output "jwt_secret_id" {
  description = "Secret Manager secret ID for JWT (if created)"
  value       = var.create_jwt_secret ? google_secret_manager_secret.jwt_secret[0].secret_id : null
}

output "jwt_secret_name" {
  description = "Full secret name for JWT (if created)"
  value       = var.create_jwt_secret ? google_secret_manager_secret.jwt_secret[0].name : null
}

output "session_secret_id" {
  description = "Secret Manager secret ID for session (if created)"
  value       = var.create_session_secret ? google_secret_manager_secret.session_secret[0].secret_id : null
}

output "session_secret_name" {
  description = "Full secret name for session (if created)"
  value       = var.create_session_secret ? google_secret_manager_secret.session_secret[0].name : null
}

output "sendgrid_api_key_id" {
  description = "Secret Manager secret ID for SendGrid API key (if created)"
  value       = (var.sendgrid_api_key != null || var.create_sendgrid_secret) ? google_secret_manager_secret.sendgrid_api_key[0].secret_id : null
}

output "sendgrid_api_key_name" {
  description = "Full secret name for SendGrid API key (if created)"
  value       = (var.sendgrid_api_key != null || var.create_sendgrid_secret) ? google_secret_manager_secret.sendgrid_api_key[0].name : null
}

output "scheduled_task_secret_value" {
  description = "Scheduled task secret value (raw password for env var)"
  value       = var.create_scheduled_task_secret ? random_password.scheduled_task_secret[0].result : null
  sensitive   = true
}

output "scheduled_task_secret_id" {
  description = "Secret Manager secret ID for scheduled task secret"
  value       = var.create_scheduled_task_secret ? google_secret_manager_secret.scheduled_task_secret[0].secret_id : null
}

output "additional_secret_ids" {
  description = "Map of additional secret IDs"
  value       = { for k, v in google_secret_manager_secret.additional : k => v.secret_id }
}

output "additional_secret_names" {
  description = "Map of additional secret full names"
  value       = { for k, v in google_secret_manager_secret.additional : k => v.name }
}

# Convenience output with all secret IDs
output "all_secret_ids" {
  description = "List of all secret IDs created by this module"
  value = concat(
    [google_secret_manager_secret.database_password.secret_id],
    var.create_jwt_secret ? [google_secret_manager_secret.jwt_secret[0].secret_id] : [],
    var.create_session_secret ? [google_secret_manager_secret.session_secret[0].secret_id] : [],
    [for secret in google_secret_manager_secret.additional : secret.secret_id]
  )
}

# Convenience output for environment variables
output "secret_env_vars" {
  description = "Map of environment variable names to secret names"
  value = merge(
    {
      DB_PASSWORD_SECRET = google_secret_manager_secret.database_password.name
    },
    var.create_jwt_secret ? {
      JWT_SECRET_NAME = google_secret_manager_secret.jwt_secret[0].name
    } : {},
    var.create_session_secret ? {
      SESSION_SECRET_NAME = google_secret_manager_secret.session_secret[0].name
    } : {},
    { for k, v in google_secret_manager_secret.additional : "${upper(k)}_SECRET_NAME" => v.name }
  )
  sensitive = true
}
