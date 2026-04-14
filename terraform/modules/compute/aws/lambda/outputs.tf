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
  value       = var.enable_function_url ? aws_lambda_function_url.main[0].function_url : null
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
