# Outputs for GCP Monitoring Module

# Notification channels
output "email_notification_channel_ids" {
  description = "IDs of email notification channels"
  value       = google_monitoring_notification_channel.email[*].id
}

output "slack_notification_channel_id" {
  description = "ID of Slack notification channel (if configured)"
  value       = var.slack_webhook_url != "" ? google_monitoring_notification_channel.slack[0].id : null
}

# Log sink
output "error_sink_id" {
  description = "ID of the error log sink"
  value       = google_logging_project_sink.error_sink.id
}

output "error_sink_writer_identity" {
  description = "Writer identity of the error log sink"
  value       = google_logging_project_sink.error_sink.writer_identity
}

# Pub/Sub topic
output "error_topic_id" {
  description = "ID of the error Pub/Sub topic"
  value       = google_pubsub_topic.errors.id
}

output "error_topic_name" {
  description = "Name of the error Pub/Sub topic"
  value       = google_pubsub_topic.errors.name
}

# Log-based metrics
output "error_count_metric_name" {
  description = "Name of the error count log-based metric"
  value       = google_logging_metric.error_count.name
}

output "recommendations_metric_name" {
  description = "Name of the recommendations fetched log-based metric"
  value       = google_logging_metric.recommendations_fetched.name
}

output "purchases_metric_name" {
  description = "Name of the purchases executed log-based metric"
  value       = google_logging_metric.purchases_executed.name
}

# Dashboard
output "dashboard_id" {
  description = "ID of the Cloud Monitoring dashboard"
  value       = google_monitoring_dashboard.main.id
}

# Alert policies
output "high_error_rate_policy_id" {
  description = "ID of the high error rate alert policy"
  value       = google_monitoring_alert_policy.high_error_rate.id
}

output "high_latency_policy_id" {
  description = "ID of the high latency alert policy"
  value       = google_monitoring_alert_policy.high_latency.id
}

output "high_cpu_policy_id" {
  description = "ID of the high CPU alert policy"
  value       = google_monitoring_alert_policy.high_cpu.id
}

output "high_memory_policy_id" {
  description = "ID of the high memory alert policy"
  value       = google_monitoring_alert_policy.high_memory.id
}

output "db_high_cpu_policy_id" {
  description = "ID of the database high CPU alert policy"
  value       = google_monitoring_alert_policy.db_high_cpu.id
}

output "db_high_connections_policy_id" {
  description = "ID of the database high connections alert policy"
  value       = google_monitoring_alert_policy.db_high_connections.id
}

output "application_errors_policy_id" {
  description = "ID of the application errors alert policy"
  value       = google_monitoring_alert_policy.application_errors.id
}

output "service_unavailable_policy_id" {
  description = "ID of the service unavailable alert policy"
  value       = google_monitoring_alert_policy.service_unavailable.id
}

# Uptime check
output "uptime_check_id" {
  description = "ID of the uptime check"
  value       = google_monitoring_uptime_check_config.service_health.uptime_check_id
}

output "uptime_check_name" {
  description = "Name of the uptime check"
  value       = google_monitoring_uptime_check_config.service_health.name
}
