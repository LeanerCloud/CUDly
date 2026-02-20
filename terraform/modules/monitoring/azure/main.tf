# Azure Application Insights and Azure Monitor Module
#
# This module creates comprehensive monitoring for CUDly on Azure:
# - Application Insights for application monitoring
# - Log Analytics workspace for log aggregation
# - Azure Monitor alerts for critical issues
# - Action groups for email and Slack notifications
# - Availability tests for service health
# - Custom metrics and queries

# Log Analytics Workspace
resource "azurerm_log_analytics_workspace" "main" {
  name                = "${var.app_name}-logs"
  location            = var.location
  resource_group_name = var.resource_group_name
  sku                 = "PerGB2018"
  retention_in_days   = var.log_retention_days

  tags = var.tags
}

# Application Insights
resource "azurerm_application_insights" "main" {
  name                = "${var.app_name}-insights"
  location            = var.location
  resource_group_name = var.resource_group_name
  workspace_id        = azurerm_log_analytics_workspace.main.id
  application_type    = "web"

  retention_in_days = var.log_retention_days

  tags = var.tags
}

# Action Group for email notifications
resource "azurerm_monitor_action_group" "email" {
  name                = "${var.app_name}-email-alerts"
  resource_group_name = var.resource_group_name
  short_name          = "email"

  dynamic "email_receiver" {
    for_each = var.alert_email_addresses
    content {
      name          = "email-${email_receiver.key}"
      email_address = email_receiver.value
    }
  }

  tags = var.tags
}

# Action Group for Slack notifications (optional)
resource "azurerm_monitor_action_group" "slack" {
  count = var.slack_webhook_url != "" ? 1 : 0

  name                = "${var.app_name}-slack-alerts"
  resource_group_name = var.resource_group_name
  short_name          = "slack"

  webhook_receiver {
    name        = "slack-webhook"
    service_uri = var.slack_webhook_url
  }

  tags = var.tags
}

# Alert: High error rate
resource "azurerm_monitor_metric_alert" "high_error_rate" {
  name                = "${var.app_name}-high-error-rate"
  resource_group_name = var.resource_group_name
  scopes              = [azurerm_application_insights.main.id]
  description         = "Alert when error rate exceeds threshold"
  severity            = 2
  frequency           = "PT5M"
  window_size         = "PT5M"

  criteria {
    metric_namespace = "Microsoft.Insights/components"
    metric_name      = "exceptions/count"
    aggregation      = "Count"
    operator         = "GreaterThan"
    threshold        = var.error_rate_threshold
  }

  action {
    action_group_id = azurerm_monitor_action_group.email.id
  }

  dynamic "action" {
    for_each = var.slack_webhook_url != "" ? [1] : []
    content {
      action_group_id = azurerm_monitor_action_group.slack[0].id
    }
  }

  tags = var.tags
}

# Alert: High response time
resource "azurerm_monitor_metric_alert" "high_response_time" {
  name                = "${var.app_name}-high-response-time"
  resource_group_name = var.resource_group_name
  scopes              = [azurerm_application_insights.main.id]
  description         = "Alert when response time exceeds threshold"
  severity            = 2
  frequency           = "PT5M"
  window_size         = "PT5M"

  criteria {
    metric_namespace = "Microsoft.Insights/components"
    metric_name      = "requests/duration"
    aggregation      = "Average"
    operator         = "GreaterThan"
    threshold        = var.latency_threshold
  }

  action {
    action_group_id = azurerm_monitor_action_group.email.id
  }

  dynamic "action" {
    for_each = var.slack_webhook_url != "" ? [1] : []
    content {
      action_group_id = azurerm_monitor_action_group.slack[0].id
    }
  }

  tags = var.tags
}

# Alert: Container App high CPU
resource "azurerm_monitor_metric_alert" "high_cpu" {
  name                = "${var.app_name}-high-cpu"
  resource_group_name = var.resource_group_name
  scopes              = [var.container_app_id]
  description         = "Alert when CPU utilization is high"
  severity            = 2
  frequency           = "PT5M"
  window_size         = "PT5M"

  criteria {
    metric_namespace = "Microsoft.App/containerApps"
    metric_name      = "UsageNanoCores"
    aggregation      = "Average"
    operator         = "GreaterThan"
    threshold        = var.cpu_threshold * 10000000 # Convert to nanocores
  }

  action {
    action_group_id = azurerm_monitor_action_group.email.id
  }

  dynamic "action" {
    for_each = var.slack_webhook_url != "" ? [1] : []
    content {
      action_group_id = azurerm_monitor_action_group.slack[0].id
    }
  }

  tags = var.tags
}

# Alert: Container App high memory
resource "azurerm_monitor_metric_alert" "high_memory" {
  name                = "${var.app_name}-high-memory"
  resource_group_name = var.resource_group_name
  scopes              = [var.container_app_id]
  description         = "Alert when memory utilization is high"
  severity            = 2
  frequency           = "PT5M"
  window_size         = "PT5M"

  criteria {
    metric_namespace = "Microsoft.App/containerApps"
    metric_name      = "WorkingSetBytes"
    aggregation      = "Average"
    operator         = "GreaterThan"
    threshold        = var.memory_threshold * 1024 * 1024 # Convert to bytes
  }

  action {
    action_group_id = azurerm_monitor_action_group.email.id
  }

  dynamic "action" {
    for_each = var.slack_webhook_url != "" ? [1] : []
    content {
      action_group_id = azurerm_monitor_action_group.slack[0].id
    }
  }

  tags = var.tags
}

# Alert: Database high CPU
resource "azurerm_monitor_metric_alert" "db_high_cpu" {
  name                = "${var.app_name}-db-high-cpu"
  resource_group_name = var.resource_group_name
  scopes              = [var.db_server_id]
  description         = "Alert when database CPU is high"
  severity            = 2
  frequency           = "PT5M"
  window_size         = "PT5M"

  criteria {
    metric_namespace = "Microsoft.DBforPostgreSQL/flexibleServers"
    metric_name      = "cpu_percent"
    aggregation      = "Average"
    operator         = "GreaterThan"
    threshold        = var.db_cpu_threshold
  }

  action {
    action_group_id = azurerm_monitor_action_group.email.id
  }

  dynamic "action" {
    for_each = var.slack_webhook_url != "" ? [1] : []
    content {
      action_group_id = azurerm_monitor_action_group.slack[0].id
    }
  }

  tags = var.tags
}

# Alert: Database high memory
resource "azurerm_monitor_metric_alert" "db_high_memory" {
  name                = "${var.app_name}-db-high-memory"
  resource_group_name = var.resource_group_name
  scopes              = [var.db_server_id]
  description         = "Alert when database memory is high"
  severity            = 2
  frequency           = "PT5M"
  window_size         = "PT5M"

  criteria {
    metric_namespace = "Microsoft.DBforPostgreSQL/flexibleServers"
    metric_name      = "memory_percent"
    aggregation      = "Average"
    operator         = "GreaterThan"
    threshold        = var.db_memory_threshold
  }

  action {
    action_group_id = azurerm_monitor_action_group.email.id
  }

  dynamic "action" {
    for_each = var.slack_webhook_url != "" ? [1] : []
    content {
      action_group_id = azurerm_monitor_action_group.slack[0].id
    }
  }

  tags = var.tags
}

# Alert: Database high connections
resource "azurerm_monitor_metric_alert" "db_high_connections" {
  name                = "${var.app_name}-db-high-connections"
  resource_group_name = var.resource_group_name
  scopes              = [var.db_server_id]
  description         = "Alert when database connection count is high"
  severity            = 2
  frequency           = "PT5M"
  window_size         = "PT5M"

  criteria {
    metric_namespace = "Microsoft.DBforPostgreSQL/flexibleServers"
    metric_name      = "active_connections"
    aggregation      = "Average"
    operator         = "GreaterThan"
    threshold        = var.db_connection_threshold
  }

  action {
    action_group_id = azurerm_monitor_action_group.email.id
  }

  dynamic "action" {
    for_each = var.slack_webhook_url != "" ? [1] : []
    content {
      action_group_id = azurerm_monitor_action_group.slack[0].id
    }
  }

  tags = var.tags
}

# Alert: Application errors (log-based)
resource "azurerm_monitor_scheduled_query_rules_alert_v2" "application_errors" {
  name                = "${var.app_name}-application-errors"
  resource_group_name = var.resource_group_name
  location            = var.location

  evaluation_frequency = "PT5M"
  window_duration      = "PT5M"
  scopes               = [azurerm_application_insights.main.id]
  severity             = 2
  description          = "Alert when application error count is high"

  criteria {
    query                   = <<-QUERY
      traces
      | where severityLevel >= 3
      | summarize count() by bin(timestamp, 5m)
      | where count_ > ${var.app_error_threshold}
    QUERY
    time_aggregation_method = "Maximum"
    threshold               = var.app_error_threshold
    operator                = "GreaterThan"

    failing_periods {
      minimum_failing_periods_to_trigger_alert = 1
      number_of_evaluation_periods             = 1
    }
  }

  action {
    action_groups = concat(
      [azurerm_monitor_action_group.email.id],
      var.slack_webhook_url != "" ? [azurerm_monitor_action_group.slack[0].id] : []
    )
  }

  tags = var.tags
}

# Availability test for service health
resource "azurerm_application_insights_standard_web_test" "health_check" {
  name                    = "${var.app_name}-health-check"
  resource_group_name     = var.resource_group_name
  location                = var.location
  application_insights_id = azurerm_application_insights.main.id
  geo_locations           = ["us-tx-sn1-azr", "us-il-ch1-azr", "us-ca-sjc-azr"]

  frequency = 300
  timeout   = 30
  enabled   = true

  request {
    url = "${var.app_url}/health"
  }

  validation_rules {
    expected_status_code = 200
    content {
      content_match      = "healthy"
      pass_if_text_found = true
    }
  }

  tags = var.tags
}

# Alert: Service unavailable
resource "azurerm_monitor_metric_alert" "service_unavailable" {
  name                = "${var.app_name}-service-unavailable"
  resource_group_name = var.resource_group_name
  scopes              = [azurerm_application_insights.main.id, azurerm_application_insights_standard_web_test.health_check.id]
  description         = "Alert when health check fails"
  severity            = 1
  frequency           = "PT1M"
  window_size         = "PT5M"

  application_insights_web_test_location_availability_criteria {
    web_test_id           = azurerm_application_insights_standard_web_test.health_check.id
    component_id          = azurerm_application_insights.main.id
    failed_location_count = 2
  }

  action {
    action_group_id = azurerm_monitor_action_group.email.id
  }

  dynamic "action" {
    for_each = var.slack_webhook_url != "" ? [1] : []
    content {
      action_group_id = azurerm_monitor_action_group.slack[0].id
    }
  }

  tags = var.tags
}

# Workbook for dashboard
resource "azurerm_application_insights_workbook" "main" {
  name                = "${var.app_name}-dashboard"
  resource_group_name = var.resource_group_name
  location            = var.location
  display_name        = "${var.app_name} - ${var.environment}"
  source_id           = azurerm_application_insights.main.id

  data_json = jsonencode({
    version = "Notebook/1.0"
    items = [
      {
        type = 10
        content = {
          chartId          = "workbookRequestsChart"
          version          = "MetricsItem/2.0"
          size             = 0
          chartType        = 2
          resourceType     = "microsoft.insights/components"
          metricScope      = 0
          resourceIds      = [azurerm_application_insights.main.id]
          timeContext = {
            durationMs = 3600000
          }
          metrics = [
            {
              namespace = "microsoft.insights/components"
              metric    = "requests/count"
              aggregation = 7
            },
            {
              namespace = "microsoft.insights/components"
              metric    = "requests/failed"
              aggregation = 7
            }
          ]
          title = "Request Rate and Failures"
        }
      },
      {
        type = 10
        content = {
          chartId          = "workbookLatencyChart"
          version          = "MetricsItem/2.0"
          size             = 0
          chartType        = 2
          resourceType     = "microsoft.insights/components"
          metricScope      = 0
          resourceIds      = [azurerm_application_insights.main.id]
          timeContext = {
            durationMs = 3600000
          }
          metrics = [
            {
              namespace = "microsoft.insights/components"
              metric    = "requests/duration"
              aggregation = 4
            }
          ]
          title = "Response Time"
        }
      },
      {
        type = 10
        content = {
          chartId          = "workbookDatabaseChart"
          version          = "MetricsItem/2.0"
          size             = 0
          chartType        = 2
          resourceType     = "microsoft.dbforpostgresql/flexibleservers"
          metricScope      = 0
          resourceIds      = [var.db_server_id]
          timeContext = {
            durationMs = 3600000
          }
          metrics = [
            {
              namespace = "microsoft.dbforpostgresql/flexibleservers"
              metric    = "cpu_percent"
              aggregation = 4
            },
            {
              namespace = "microsoft.dbforpostgresql/flexibleservers"
              metric    = "memory_percent"
              aggregation = 4
            },
            {
              namespace = "microsoft.dbforpostgresql/flexibleservers"
              metric    = "active_connections"
              aggregation = 4
            }
          ]
          title = "Database Performance"
        }
      },
      {
        type = 3
        content = {
          version = "KqlItem/1.0"
          query = <<-QUERY
            traces
            | where severityLevel >= 3
            | summarize count() by bin(timestamp, 5m), severityLevel
            | order by timestamp desc
          QUERY
          size = 0
          title = "Error Trend"
          timeContext = {
            durationMs = 3600000
          }
          queryType      = 0
          resourceType   = "microsoft.insights/components"
          visualization  = "timechart"
        }
      }
    ]
  })

  tags = var.tags
}
