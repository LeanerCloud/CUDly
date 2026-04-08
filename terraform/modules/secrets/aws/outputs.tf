output "database_password_secret_arn" {
  description = "ARN of the database password secret"
  value       = aws_secretsmanager_secret.database_password.arn
}

output "database_password_secret_name" {
  description = "Name of the database password secret"
  value       = aws_secretsmanager_secret.database_password.name
}

output "database_password_value" {
  description = "Database password value (use with caution in outputs)"
  value       = aws_secretsmanager_secret_version.database_password.secret_string
  sensitive   = true
}

output "admin_password_secret_arn" {
  description = "ARN of the admin password secret (if created)"
  value       = var.create_admin_password_secret ? aws_secretsmanager_secret.admin_password[0].arn : null
}

output "admin_password_secret_name" {
  description = "Name of the admin password secret (if created)"
  value       = var.create_admin_password_secret ? aws_secretsmanager_secret.admin_password[0].name : null
}

output "jwt_secret_arn" {
  description = "ARN of the JWT signing secret (if created)"
  value       = var.create_jwt_secret ? aws_secretsmanager_secret.jwt_secret[0].arn : null
}

output "jwt_secret_name" {
  description = "Name of the JWT signing secret (if created)"
  value       = var.create_jwt_secret ? aws_secretsmanager_secret.jwt_secret[0].name : null
}

output "session_secret_arn" {
  description = "ARN of the session encryption secret (if created)"
  value       = var.create_session_secret ? aws_secretsmanager_secret.session_secret[0].arn : null
}

output "session_secret_name" {
  description = "Name of the session encryption secret (if created)"
  value       = var.create_session_secret ? aws_secretsmanager_secret.session_secret[0].name : null
}

output "additional_secret_arns" {
  description = "Map of additional secret ARNs"
  value       = { for k, v in aws_secretsmanager_secret.additional : k => v.arn }
}

output "additional_secret_names" {
  description = "Map of additional secret names"
  value       = { for k, v in aws_secretsmanager_secret.additional : k => v.name }
}

output "secret_read_policy_arn" {
  description = "ARN of IAM policy for reading secrets"
  value       = aws_iam_policy.secret_read.arn
}

output "secret_read_policy_name" {
  description = "Name of IAM policy for reading secrets"
  value       = aws_iam_policy.secret_read.name
}

output "credential_encryption_key_secret_arn" {
  description = "ARN of the credential encryption key secret; empty string if not created"
  value       = var.create_credential_encryption_key ? aws_secretsmanager_secret.credential_encryption_key[0].arn : ""
}

output "rotation_lambda_arn" {
  description = "ARN of rotation Lambda function (if created)"
  value       = var.enable_secret_rotation ? aws_lambda_function.rotation[0].arn : null
}

output "rotation_lambda_name" {
  description = "Name of rotation Lambda function (if created)"
  value       = var.enable_secret_rotation ? aws_lambda_function.rotation[0].function_name : null
}

# Convenience output with all secret ARNs
output "all_secret_arns" {
  description = "List of all secret ARNs created by this module"
  value = concat(
    [aws_secretsmanager_secret.database_password.arn],
    var.create_admin_password_secret ? [aws_secretsmanager_secret.admin_password[0].arn] : [],
    var.create_jwt_secret ? [aws_secretsmanager_secret.jwt_secret[0].arn] : [],
    var.create_session_secret ? [aws_secretsmanager_secret.session_secret[0].arn] : [],
    [for secret in aws_secretsmanager_secret.additional : secret.arn]
  )
}

# Convenience output for environment variables
output "secret_env_vars" {
  description = "Map of environment variable names to secret ARNs"
  value = merge(
    {
      DB_PASSWORD_SECRET = aws_secretsmanager_secret.database_password.arn
    },
    var.create_admin_password_secret ? {
      ADMIN_PASSWORD_SECRET = aws_secretsmanager_secret.admin_password[0].arn
    } : {},
    var.create_jwt_secret ? {
      JWT_SECRET_ARN = aws_secretsmanager_secret.jwt_secret[0].arn
    } : {},
    var.create_session_secret ? {
      SESSION_SECRET_ARN = aws_secretsmanager_secret.session_secret[0].arn
    } : {},
    { for k, v in aws_secretsmanager_secret.additional : "${upper(k)}_SECRET_ARN" => v.arn }
  )
  sensitive = true
}
