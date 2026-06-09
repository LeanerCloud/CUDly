# GCP Cloud Logging and Cloud Monitoring Module
#
# This module creates comprehensive monitoring for CUDly on GCP:
# - Cloud Logging log sinks and filters
# - Cloud Monitoring dashboards with key metrics
# - Alerting policies for critical issues
# - Notification channels (email, Slack)
# - Uptime checks for service availability
# - Log-based metrics for application insights

# Notification channel for email alerts
resource "google_monitoring_notification_channel" "email" {
  count = length(var.alert_email_addresses)

  display_name = "Email: ${var.alert_email_addresses[count.index]}"
  type         = "email"

  labels = {
    email_address = var.alert_email_addresses[count.index]
  }

  user_labels = var.labels
}

# Notification channel for Slack (optional)
resource "google_monitoring_notification_channel" "slack" {
  count = var.slack_webhook_url != "" ? 1 : 0

  display_name = "Slack: ${var.service_name} Alerts"
  type         = "slack"

  labels = {
    url = var.slack_webhook_url
  }

  sensitive_labels {
    auth_token = var.slack_webhook_url
  }

  user_labels = var.labels
}

# Log sink for errors to Pub/Sub
resource "google_logging_project_sink" "error_sink" {
  name        = "${var.service_name}-errors"
  destination = "pubsub.googleapis.com/projects/${var.project_id}/topics/${var.service_name}-errors"

  filter = <<-EOT
    resource.type="cloud_run_revision"
    severity >= ERROR
    resource.labels.service_name="${var.service_name}"
  EOT

  unique_writer_identity = true
}

# Pub/Sub topic for error logs
resource "google_pubsub_topic" "errors" {
  name = "${var.service_name}-errors"

  labels = var.labels
}

# Log-based metric for error count
resource "google_logging_metric" "error_count" {
  name   = "${var.service_name}_error_count"
  filter = <<-EOT
    resource.type="cloud_run_revision"
    severity >= ERROR
    resource.labels.service_name="${var.service_name}"
  EOT

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"
    unit        = "1"

    labels {
      key         = "severity"
      value_type  = "STRING"
      description = "Log severity level"
    }
  }

  label_extractors = {
    "severity" = "EXTRACT(severity)"
  }
}

# Log-based metric for recommendations fetched
resource "google_logging_metric" "recommendations_fetched" {
  name   = "${var.service_name}_recommendations_fetched"
  filter = <<-EOT
    resource.type="cloud_run_revision"
    resource.labels.service_name="${var.service_name}"
    jsonPayload.message="Recommendations fetched"
  EOT

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"
    unit        = "1"
  }

  value_extractor = "EXTRACT(jsonPayload.count)"
}

# Log-based metric for purchases executed
resource "google_logging_metric" "purchases_executed" {
  name   = "${var.service_name}_purchases_executed"
  filter = <<-EOT
    resource.type="cloud_run_revision"
    resource.labels.service_name="${var.service_name}"
    jsonPayload.message="Purchase executed"
  EOT

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"
    unit        = "1"
  }
}

# Cloud Monitoring Dashboard
resource "google_monitoring_dashboard" "main" {
  dashboard_json = jsonencode({
    displayName = "${var.service_name} - ${var.environment}"
    mosaicLayout = {
      columns = 12
      tiles = [
        # Cloud Run Performance
        {
          width  = 6
          height = 4
          widget = {
            title = "Cloud Run Performance"
            xyChart = {
              dataSets = [
                {
                  timeSeriesQuery = {
                    timeSeriesFilter = {
                      filter = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.service_name}\" metric.type=\"run.googleapis.com/request_count\""
                      aggregation = {
                        alignmentPeriod  = "60s"
                        perSeriesAligner = "ALIGN_RATE"
                      }
                    }
                  }
                  plotType   = "LINE"
                  targetAxis = "Y1"
                },
                {
                  timeSeriesQuery = {
                    timeSeriesFilter = {
                      filter = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.service_name}\" metric.type=\"run.googleapis.com/request_latencies\""
                      aggregation = {
                        alignmentPeriod    = "60s"
                        perSeriesAligner   = "ALIGN_PERCENTILE_95"
                        crossSeriesReducer = "REDUCE_MEAN"
                      }
                    }
                  }
                  plotType   = "LINE"
                  targetAxis = "Y2"
                }
              ]
              yAxis = {
                label = "Request Rate"
                scale = "LINEAR"
              }
              y2Axis = {
                label = "P95 Latency (ms)"
                scale = "LINEAR"
              }
            }
          }
        },
        # Error Rate
        {
          width  = 6
          height = 4
          widget = {
            title = "Error Rate"
            xyChart = {
              dataSets = [
                {
                  timeSeriesQuery = {
                    timeSeriesFilter = {
                      filter = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.service_name}\" metric.type=\"run.googleapis.com/request_count\" metric.labels.response_code_class=\"5xx\""
                      aggregation = {
                        alignmentPeriod  = "60s"
                        perSeriesAligner = "ALIGN_RATE"
                      }
                    }
                  }
                  plotType = "LINE"
                }
              ]
            }
          }
        },
        # Container Instances
        {
          width  = 6
          height = 4
          widget = {
            title = "Container Instances"
            xyChart = {
              dataSets = [
                {
                  timeSeriesQuery = {
                    timeSeriesFilter = {
                      filter = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.service_name}\" metric.type=\"run.googleapis.com/container/instance_count\""
                      aggregation = {
                        alignmentPeriod  = "60s"
                        perSeriesAligner = "ALIGN_MAX"
                      }
                    }
                  }
                  plotType = "LINE"
                }
              ]
            }
          }
        },
        # CPU and Memory Utilization
        {
          width  = 6
          height = 4
          widget = {
            title = "Resource Utilization"
            xyChart = {
              dataSets = [
                {
                  timeSeriesQuery = {
                    timeSeriesFilter = {
                      filter = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.service_name}\" metric.type=\"run.googleapis.com/container/cpu/utilizations\""
                      aggregation = {
                        alignmentPeriod  = "60s"
                        perSeriesAligner = "ALIGN_MEAN"
                      }
                    }
                  }
                  plotType   = "LINE"
                  targetAxis = "Y1"
                },
                {
                  timeSeriesQuery = {
                    timeSeriesFilter = {
                      filter = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.service_name}\" metric.type=\"run.googleapis.com/container/memory/utilizations\""
                      aggregation = {
                        alignmentPeriod  = "60s"
                        perSeriesAligner = "ALIGN_MEAN"
                      }
                    }
                  }
                  plotType   = "LINE"
                  targetAxis = "Y2"
                }
              ]
            }
          }
        },
        # Cloud SQL Database
        {
          width  = 6
          height = 4
          widget = {
            title = "Cloud SQL Performance"
            xyChart = {
              dataSets = [
                {
                  timeSeriesQuery = {
                    timeSeriesFilter = {
                      filter = "resource.type=\"cloudsql_database\" resource.labels.database_id=\"${var.project_id}:${var.db_instance_id}\" metric.type=\"cloudsql.googleapis.com/database/cpu/utilization\""
                      aggregation = {
                        alignmentPeriod  = "60s"
                        perSeriesAligner = "ALIGN_MEAN"
                      }
                    }
                  }
                  plotType   = "LINE"
                  targetAxis = "Y1"
                },
                {
                  timeSeriesQuery = {
                    timeSeriesFilter = {
                      filter = "resource.type=\"cloudsql_database\" resource.labels.database_id=\"${var.project_id}:${var.db_instance_id}\" metric.type=\"cloudsql.googleapis.com/database/memory/utilization\""
                      aggregation = {
                        alignmentPeriod  = "60s"
                        perSeriesAligner = "ALIGN_MEAN"
                      }
                    }
                  }
                  plotType   = "LINE"
                  targetAxis = "Y2"
                }
              ]
            }
          }
        },
        # Database Connections
        {
          width  = 6
          height = 4
          widget = {
            title = "Database Connections"
            xyChart = {
              dataSets = [
                {
                  timeSeriesQuery = {
                    timeSeriesFilter = {
                      filter = "resource.type=\"cloudsql_database\" resource.labels.database_id=\"${var.project_id}:${var.db_instance_id}\" metric.type=\"cloudsql.googleapis.com/database/postgresql/num_backends\""
                      aggregation = {
                        alignmentPeriod  = "60s"
                        perSeriesAligner = "ALIGN_MEAN"
                      }
                    }
                  }
                  plotType = "LINE"
                }
              ]
            }
          }
        },
        # Application Business Metrics
        {
          width  = 12
          height = 4
          widget = {
            title = "Business Metrics"
            xyChart = {
              dataSets = [
                {
                  timeSeriesQuery = {
                    timeSeriesFilter = {
                      filter = "metric.type=\"logging.googleapis.com/user/${var.service_name}_recommendations_fetched\""
                      aggregation = {
                        alignmentPeriod  = "3600s"
                        perSeriesAligner = "ALIGN_SUM"
                      }
                    }
                  }
                  plotType = "LINE"
                },
                {
                  timeSeriesQuery = {
                    timeSeriesFilter = {
                      filter = "metric.type=\"logging.googleapis.com/user/${var.service_name}_purchases_executed\""
                      aggregation = {
                        alignmentPeriod  = "3600s"
                        perSeriesAligner = "ALIGN_SUM"
                      }
                    }
                  }
                  plotType = "LINE"
                }
              ]
            }
          }
        }
      ]
    }
  })
}

# Alert: High error rate
resource "google_monitoring_alert_policy" "high_error_rate" {
  display_name = "${var.service_name} High Error Rate"
  combiner     = "OR"

  conditions {
    display_name = "Error rate > ${var.error_rate_threshold}%"

    condition_threshold {
      filter          = "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${var.service_name}\" AND metric.type=\"run.googleapis.com/request_count\" AND metric.labels.response_code_class=\"5xx\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = var.error_rate_threshold

      aggregations {
        alignment_period     = "60s"
        per_series_aligner   = "ALIGN_RATE"
        cross_series_reducer = "REDUCE_SUM"
      }
    }
  }

  notification_channels = concat(
    google_monitoring_notification_channel.email[*].id,
    var.slack_webhook_url != "" ? [google_monitoring_notification_channel.slack[0].id] : []
  )

  alert_strategy {
    auto_close = "1800s"
  }

  user_labels = var.labels
}

# Alert: High latency
resource "google_monitoring_alert_policy" "high_latency" {
  display_name = "${var.service_name} High Latency"
  combiner     = "OR"

  conditions {
    display_name = "P95 latency > ${var.latency_threshold}ms"

    condition_threshold {
      filter          = "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${var.service_name}\" AND metric.type=\"run.googleapis.com/request_latencies\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = var.latency_threshold

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_PERCENTILE_95"
      }
    }
  }

  notification_channels = concat(
    google_monitoring_notification_channel.email[*].id,
    var.slack_webhook_url != "" ? [google_monitoring_notification_channel.slack[0].id] : []
  )

  alert_strategy {
    auto_close = "1800s"
  }

  user_labels = var.labels
}

# Alert: High CPU utilization
resource "google_monitoring_alert_policy" "high_cpu" {
  display_name = "${var.service_name} High CPU Utilization"
  combiner     = "OR"

  conditions {
    display_name = "CPU utilization > ${var.cpu_threshold}%"

    condition_threshold {
      filter          = "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${var.service_name}\" AND metric.type=\"run.googleapis.com/container/cpu/utilizations\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = var.cpu_threshold / 100

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_MEAN"
      }
    }
  }

  notification_channels = concat(
    google_monitoring_notification_channel.email[*].id,
    var.slack_webhook_url != "" ? [google_monitoring_notification_channel.slack[0].id] : []
  )

  alert_strategy {
    auto_close = "1800s"
  }

  user_labels = var.labels
}

# Alert: High memory utilization
resource "google_monitoring_alert_policy" "high_memory" {
  display_name = "${var.service_name} High Memory Utilization"
  combiner     = "OR"

  conditions {
    display_name = "Memory utilization > ${var.memory_threshold}%"

    condition_threshold {
      filter          = "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${var.service_name}\" AND metric.type=\"run.googleapis.com/container/memory/utilizations\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = var.memory_threshold / 100

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_MEAN"
      }
    }
  }

  notification_channels = concat(
    google_monitoring_notification_channel.email[*].id,
    var.slack_webhook_url != "" ? [google_monitoring_notification_channel.slack[0].id] : []
  )

  alert_strategy {
    auto_close = "1800s"
  }

  user_labels = var.labels
}

# Alert: Database high CPU
resource "google_monitoring_alert_policy" "db_high_cpu" {
  display_name = "${var.service_name} Database High CPU"
  combiner     = "OR"

  conditions {
    display_name = "Database CPU > ${var.db_cpu_threshold}%"

    condition_threshold {
      filter          = "resource.type=\"cloudsql_database\" AND resource.labels.database_id=\"${var.project_id}:${var.db_instance_id}\" AND metric.type=\"cloudsql.googleapis.com/database/cpu/utilization\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = var.db_cpu_threshold / 100

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_MEAN"
      }
    }
  }

  notification_channels = concat(
    google_monitoring_notification_channel.email[*].id,
    var.slack_webhook_url != "" ? [google_monitoring_notification_channel.slack[0].id] : []
  )

  alert_strategy {
    auto_close = "1800s"
  }

  user_labels = var.labels
}

# Alert: Database high connections
resource "google_monitoring_alert_policy" "db_high_connections" {
  display_name = "${var.service_name} Database High Connections"
  combiner     = "OR"

  conditions {
    display_name = "Database connections > ${var.db_connection_threshold}"

    condition_threshold {
      filter          = "resource.type=\"cloudsql_database\" AND resource.labels.database_id=\"${var.project_id}:${var.db_instance_id}\" AND metric.type=\"cloudsql.googleapis.com/database/postgresql/num_backends\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = var.db_connection_threshold

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_MEAN"
      }
    }
  }

  notification_channels = concat(
    google_monitoring_notification_channel.email[*].id,
    var.slack_webhook_url != "" ? [google_monitoring_notification_channel.slack[0].id] : []
  )

  alert_strategy {
    auto_close = "1800s"
  }

  user_labels = var.labels
}

# Alert: Application errors (log-based)
resource "google_monitoring_alert_policy" "application_errors" {
  display_name = "${var.service_name} Application Errors"
  combiner     = "OR"

  conditions {
    display_name = "Application error count > ${var.app_error_threshold}"

    condition_threshold {
      filter          = "metric.type=\"logging.googleapis.com/user/${var.service_name}_error_count\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = var.app_error_threshold

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_RATE"
      }
    }
  }

  notification_channels = concat(
    google_monitoring_notification_channel.email[*].id,
    var.slack_webhook_url != "" ? [google_monitoring_notification_channel.slack[0].id] : []
  )

  alert_strategy {
    auto_close = "1800s"
  }

  user_labels = var.labels
}

# Uptime check for service availability
resource "google_monitoring_uptime_check_config" "service_health" {
  display_name = "${var.service_name} Health Check"
  timeout      = "10s"
  period       = "60s"

  http_check {
    path         = "/health"
    port         = "443"
    use_ssl      = true
    validate_ssl = true
  }

  monitored_resource {
    type = "uptime_url"
    labels = {
      project_id = var.project_id
      host       = var.service_url
    }
  }

  content_matchers {
    content = "healthy"
    matcher = "CONTAINS_STRING"
  }
}

# Alert: Service unavailable
resource "google_monitoring_alert_policy" "service_unavailable" {
  display_name = "${var.service_name} Service Unavailable"
  combiner     = "OR"

  conditions {
    display_name = "Health check failing"

    condition_threshold {
      filter          = "metric.type=\"monitoring.googleapis.com/uptime_check/check_passed\" AND resource.type=\"uptime_url\" AND metric.labels.check_id=\"${google_monitoring_uptime_check_config.service_health.uptime_check_id}\""
      duration        = "180s"
      comparison      = "COMPARISON_LT"
      threshold_value = 1

      aggregations {
        alignment_period     = "60s"
        per_series_aligner   = "ALIGN_NEXT_OLDER"
        cross_series_reducer = "REDUCE_COUNT_FALSE"
        group_by_fields      = ["resource.label.project_id"]
      }
    }
  }

  notification_channels = concat(
    google_monitoring_notification_channel.email[*].id,
    var.slack_webhook_url != "" ? [google_monitoring_notification_channel.slack[0].id] : []
  )

  alert_strategy {
    auto_close = "1800s"
  }

  user_labels = var.labels
}
