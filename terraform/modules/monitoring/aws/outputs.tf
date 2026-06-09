# Outputs for AWS Monitoring Module

output "sns_topic_arn" {
  description = "ARN of the SNS topic for alerts"
  value       = aws_sns_topic.alerts.arn
}

output "sns_topic_name" {
  description = "Name of the SNS topic for alerts"
  value       = aws_sns_topic.alerts.name
}

output "kms_key_id" {
  description = "ID of the KMS key used for SNS encryption (if enabled)"
  value       = var.enable_sns_encryption ? aws_kms_key.sns[0].id : null
}

output "kms_key_arn" {
  description = "ARN of the KMS key used for SNS encryption (if enabled)"
  value       = var.enable_sns_encryption ? aws_kms_key.sns[0].arn : null
}

output "dashboard_name" {
  description = "Name of the CloudWatch dashboard"
  value       = aws_cloudwatch_dashboard.main.dashboard_name
}

output "dashboard_arn" {
  description = "ARN of the CloudWatch dashboard"
  value       = aws_cloudwatch_dashboard.main.dashboard_arn
}

# Lambda alarms (only created if compute_platform is lambda)
output "lambda_errors_alarm_arn" {
  description = "ARN of the Lambda errors alarm"
  value       = var.compute_platform == "lambda" ? aws_cloudwatch_metric_alarm.lambda_errors[0].arn : null
}

output "lambda_throttles_alarm_arn" {
  description = "ARN of the Lambda throttles alarm"
  value       = var.compute_platform == "lambda" ? aws_cloudwatch_metric_alarm.lambda_throttles[0].arn : null
}

output "lambda_duration_alarm_arn" {
  description = "ARN of the Lambda duration alarm"
  value       = var.compute_platform == "lambda" ? aws_cloudwatch_metric_alarm.lambda_duration[0].arn : null
}

# Database alarms
output "db_cpu_alarm_arn" {
  description = "ARN of the database CPU alarm"
  value       = aws_cloudwatch_metric_alarm.db_cpu.arn
}

output "db_connections_alarm_arn" {
  description = "ARN of the database connections alarm"
  value       = aws_cloudwatch_metric_alarm.db_connections.arn
}

output "db_capacity_alarm_arn" {
  description = "ARN of the database capacity alarm"
  value       = aws_cloudwatch_metric_alarm.db_capacity.arn
}

# Application alarms
output "application_errors_alarm_arn" {
  description = "ARN of the application errors alarm"
  value       = aws_cloudwatch_metric_alarm.application_errors.arn
}

# X-Ray sampling rule (only created if X-Ray is enabled)
output "xray_sampling_rule_arn" {
  description = "ARN of the X-Ray sampling rule (if enabled)"
  value       = var.enable_xray ? aws_xray_sampling_rule.cudly[0].arn : null
}

# Log metric filters
output "error_metric_filter_name" {
  description = "Name of the error count metric filter"
  value       = aws_cloudwatch_log_metric_filter.errors.name
}

output "recommendations_metric_filter_name" {
  description = "Name of the recommendations fetched metric filter"
  value       = aws_cloudwatch_log_metric_filter.recommendations_fetched.name
}

output "purchases_metric_filter_name" {
  description = "Name of the purchases executed metric filter"
  value       = aws_cloudwatch_log_metric_filter.purchases_executed.name
}

# CloudWatch Insights queries
output "errors_by_type_query_id" {
  description = "ID of the errors by type CloudWatch Insights query"
  value       = aws_cloudwatch_query_definition.errors_by_type.query_definition_id
}

output "slow_requests_query_id" {
  description = "ID of the slow requests CloudWatch Insights query"
  value       = aws_cloudwatch_query_definition.slow_requests.query_definition_id
}

output "request_volume_query_id" {
  description = "ID of the request volume CloudWatch Insights query"
  value       = aws_cloudwatch_query_definition.request_volume.query_definition_id
}

# Email subscriptions (list of ARNs)
output "email_subscription_arns" {
  description = "ARNs of email subscriptions to the SNS topic"
  value       = aws_sns_topic_subscription.email[*].arn
}

# Slack subscription (if configured)
output "slack_subscription_arn" {
  description = "ARN of Slack subscription to the SNS topic (if configured)"
  value       = var.slack_webhook_url != "" ? aws_sns_topic_subscription.slack[0].arn : null
  sensitive   = true
}
