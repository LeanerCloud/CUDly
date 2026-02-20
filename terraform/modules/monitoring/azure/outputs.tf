# Outputs for Azure Monitoring Module

# Log Analytics Workspace
output "log_analytics_workspace_id" {
  description = "ID of the Log Analytics workspace"
  value       = azurerm_log_analytics_workspace.main.id
}

output "log_analytics_workspace_name" {
  description = "Name of the Log Analytics workspace"
  value       = azurerm_log_analytics_workspace.main.name
}

output "log_analytics_workspace_primary_key" {
  description = "Primary key of the Log Analytics workspace"
  value       = azurerm_log_analytics_workspace.main.primary_shared_key
  sensitive   = true
}

# Application Insights
output "application_insights_id" {
  description = "ID of the Application Insights instance"
  value       = azurerm_application_insights.main.id
}

output "application_insights_name" {
  description = "Name of the Application Insights instance"
  value       = azurerm_application_insights.main.name
}

output "application_insights_instrumentation_key" {
  description = "Instrumentation key for Application Insights"
  value       = azurerm_application_insights.main.instrumentation_key
  sensitive   = true
}

output "application_insights_connection_string" {
  description = "Connection string for Application Insights"
  value       = azurerm_application_insights.main.connection_string
  sensitive   = true
}

output "application_insights_app_id" {
  description = "Application ID of Application Insights"
  value       = azurerm_application_insights.main.app_id
}

# Action Groups
output "email_action_group_id" {
  description = "ID of the email action group"
  value       = azurerm_monitor_action_group.email.id
}

output "slack_action_group_id" {
  description = "ID of the Slack action group (if configured)"
  value       = var.slack_webhook_url != "" ? azurerm_monitor_action_group.slack[0].id : null
}

# Metric Alerts
output "high_error_rate_alert_id" {
  description = "ID of the high error rate alert"
  value       = azurerm_monitor_metric_alert.high_error_rate.id
}

output "high_response_time_alert_id" {
  description = "ID of the high response time alert"
  value       = azurerm_monitor_metric_alert.high_response_time.id
}

output "high_cpu_alert_id" {
  description = "ID of the high CPU alert"
  value       = azurerm_monitor_metric_alert.high_cpu.id
}

output "high_memory_alert_id" {
  description = "ID of the high memory alert"
  value       = azurerm_monitor_metric_alert.high_memory.id
}

output "db_high_cpu_alert_id" {
  description = "ID of the database high CPU alert"
  value       = azurerm_monitor_metric_alert.db_high_cpu.id
}

output "db_high_memory_alert_id" {
  description = "ID of the database high memory alert"
  value       = azurerm_monitor_metric_alert.db_high_memory.id
}

output "db_high_connections_alert_id" {
  description = "ID of the database high connections alert"
  value       = azurerm_monitor_metric_alert.db_high_connections.id
}

output "service_unavailable_alert_id" {
  description = "ID of the service unavailable alert"
  value       = azurerm_monitor_metric_alert.service_unavailable.id
}

# Query Alerts
output "application_errors_alert_id" {
  description = "ID of the application errors alert"
  value       = azurerm_monitor_scheduled_query_rules_alert_v2.application_errors.id
}

# Availability Test
output "health_check_id" {
  description = "ID of the availability test"
  value       = azurerm_application_insights_standard_web_test.health_check.id
}

output "health_check_name" {
  description = "Name of the availability test"
  value       = azurerm_application_insights_standard_web_test.health_check.name
}

# Workbook
output "workbook_id" {
  description = "ID of the monitoring workbook"
  value       = azurerm_application_insights_workbook.main.id
}

output "workbook_name" {
  description = "Name of the monitoring workbook"
  value       = azurerm_application_insights_workbook.main.display_name
}
