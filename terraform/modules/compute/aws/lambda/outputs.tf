output "function_name" {
  description = "Lambda function name"
  value       = aws_lambda_function.main.function_name
}

output "function_arn" {
  description = "Lambda function ARN"
  value       = aws_lambda_function.main.arn
}

output "function_url" {
  description = "Lambda Function URL"
  value       = aws_lambda_function_url.main.function_url
}

output "function_invoke_arn" {
  description = "ARN to invoke Lambda function"
  value       = aws_lambda_function.main.invoke_arn
}

output "role_arn" {
  description = "IAM role ARN for Lambda"
  value       = aws_iam_role.lambda.arn
}

output "log_group_name" {
  description = "CloudWatch log group name"
  value       = aws_cloudwatch_log_group.lambda.name
}

output "signing_key_arn" {
  description = "KMS asymmetric key ARN used by the CUDly OIDC issuer"
  value       = aws_kms_key.signing.arn
}

output "signing_key_id" {
  description = "KMS asymmetric key ID used by the CUDly OIDC issuer"
  value       = aws_kms_key.signing.key_id
}

output "migration_failed_alarm_arn" {
  description = "ARN of the CloudWatch alarm that fires when a database migration fails on cold start"
  value       = aws_cloudwatch_metric_alarm.migration_failed.arn
}

output "migration_failed_metric_filter_name" {
  description = "Name of the log metric filter counting migration-failure log lines"
  value       = aws_cloudwatch_log_metric_filter.migration_failed.name
}
