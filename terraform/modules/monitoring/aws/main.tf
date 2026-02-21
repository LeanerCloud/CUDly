# AWS CloudWatch Monitoring Module
#
# This module creates comprehensive monitoring for CUDly on AWS:
# - CloudWatch Dashboards with key metrics
# - CloudWatch Alarms for critical issues
# - SNS topics for alert notifications
# - X-Ray tracing for distributed tracing
# - Log metric filters for application insights

# SNS Topic for alerts
resource "aws_sns_topic" "alerts" {
  name              = "${var.stack_name}-alerts"
  display_name      = "CUDly Alerts - ${var.environment}"
  kms_master_key_id = var.enable_sns_encryption ? aws_kms_key.sns[0].id : null

  tags = merge(
    var.tags,
    {
      Name        = "${var.stack_name}-alerts"
      Environment = var.environment
    }
  )
}

# KMS key for SNS encryption (optional)
resource "aws_kms_key" "sns" {
  count = var.enable_sns_encryption ? 1 : 0

  description             = "KMS key for SNS topic encryption"
  deletion_window_in_days = 7
  enable_key_rotation     = true

  tags = var.tags
}

resource "aws_kms_alias" "sns" {
  count = var.enable_sns_encryption ? 1 : 0

  name          = "alias/${var.stack_name}-sns"
  target_key_id = aws_kms_key.sns[0].key_id
}

# Email subscription for alerts
resource "aws_sns_topic_subscription" "email" {
  count = length(var.alert_email_addresses)

  topic_arn = aws_sns_topic.alerts.arn
  protocol  = "email"
  endpoint  = var.alert_email_addresses[count.index]
}

# Slack webhook subscription (optional)
resource "aws_sns_topic_subscription" "slack" {
  count = var.slack_webhook_url != "" ? 1 : 0

  topic_arn = aws_sns_topic.alerts.arn
  protocol  = "https"
  endpoint  = var.slack_webhook_url
}

# CloudWatch Dashboard
resource "aws_cloudwatch_dashboard" "main" {
  dashboard_name = "${var.stack_name}-${var.environment}"

  dashboard_body = jsonencode({
    widgets = concat(
      var.compute_platform == "lambda" ? [
        # Lambda metrics
        {
          type = "metric"
          properties = {
            metrics = [
              ["AWS/Lambda", "Invocations", { stat = "Sum", label = "Total Invocations" }],
              [".", "Errors", { stat = "Sum", label = "Errors", color = "#d62728" }],
              [".", "Throttles", { stat = "Sum", label = "Throttles", color = "#ff7f0e" }],
              [".", "Duration", { stat = "Average", label = "Avg Duration (ms)" }],
              [".", "ConcurrentExecutions", { stat = "Maximum", label = "Peak Concurrency" }]
            ]
            period = 300
            stat   = "Average"
            region = var.aws_region
            title  = "Lambda Performance"
            yAxis = {
              left = { min = 0 }
            }
          }
        },
        {
          type = "metric"
          properties = {
            metrics = [
              ["AWS/Lambda", "Errors", { stat = "Sum" }],
              [".", "Invocations", { stat = "Sum" }]
            ]
            period = 300
            stat   = "Sum"
            region = var.aws_region
            title  = "Lambda Error Rate"
            yAxis = {
              left = { min = 0, max = 100 }
            }
            view = "singleValue"
          }
        }
        ] : [
        # ECS Fargate metrics
        {
          type = "metric"
          properties = {
            metrics = [
              ["AWS/ECS", "CPUUtilization", { stat = "Average", label = "CPU %" }],
              [".", "MemoryUtilization", { stat = "Average", label = "Memory %" }]
            ]
            period = 300
            stat   = "Average"
            region = var.aws_region
            title  = "ECS Resource Utilization"
          }
        },
        {
          type = "metric"
          properties = {
            metrics = [
              ["AWS/ApplicationELB", "TargetResponseTime", { stat = "Average", label = "Response Time (s)" }],
              [".", "RequestCount", { stat = "Sum", label = "Request Count" }],
              [".", "HTTPCode_Target_5XX_Count", { stat = "Sum", label = "5xx Errors", color = "#d62728" }],
              [".", "HTTPCode_Target_4XX_Count", { stat = "Sum", label = "4xx Errors", color = "#ff7f0e" }]
            ]
            period = 300
            stat   = "Average"
            region = var.aws_region
            title  = "ALB Performance"
          }
        }
      ],
      [
        # Database metrics
        {
          type = "metric"
          properties = {
            metrics = [
              ["AWS/RDS", "DatabaseConnections", { stat = "Average", label = "DB Connections" }],
              [".", "CPUUtilization", { stat = "Average", label = "CPU %" }],
              [".", "FreeableMemory", { stat = "Average", label = "Free Memory (bytes)" }],
              [".", "ReadLatency", { stat = "Average", label = "Read Latency (s)" }],
              [".", "WriteLatency", { stat = "Average", label = "Write Latency (s)" }]
            ]
            period = 300
            stat   = "Average"
            region = var.aws_region
            title  = "Aurora Database Performance"
          }
        },
        {
          type = "metric"
          properties = {
            metrics = [
              ["AWS/RDS", "ServerlessDatabaseCapacity", { stat = "Average", label = "ACU Usage" }]
            ]
            period = 300
            stat   = "Average"
            region = var.aws_region
            title  = "Aurora Serverless Capacity"
            yAxis = {
              left = { min = 0, max = 2 }
            }
          }
        },
        # RDS Proxy metrics
        {
          type = "metric"
          properties = {
            metrics = [
              ["AWS/RDS", "DatabaseConnectionsCurrentlySessionPinned", { stat = "Average", label = "Pinned Connections" }],
              [".", "DatabaseConnectionsCurrentlyInTransaction", { stat = "Average", label = "Active Transactions" }],
              [".", "DatabaseConnectionsSetupSucceeded", { stat = "Sum", label = "Successful Setups" }],
              [".", "DatabaseConnectionsSetupFailed", { stat = "Sum", label = "Failed Setups", color = "#d62728" }]
            ]
            period = 300
            stat   = "Average"
            region = var.aws_region
            title  = "RDS Proxy Performance"
          }
        },
        # Application metrics (custom)
        {
          type = "metric"
          properties = {
            metrics = [
              ["CUDly", "RecommendationsFetched", { stat = "Sum", label = "Recommendations Fetched" }],
              [".", "PurchasesExecuted", { stat = "Sum", label = "Purchases Executed" }],
              [".", "SavingsGenerated", { stat = "Sum", label = "Savings Generated ($)" }]
            ]
            period = 3600
            stat   = "Sum"
            region = var.aws_region
            title  = "Business Metrics"
          }
        },
        # Error logs
        {
          type = "log"
          properties = {
            query   = "SOURCE '${var.log_group_name}' | fields @timestamp, @message | filter @message like /ERROR/ | sort @timestamp desc | limit 20"
            region  = var.aws_region
            title   = "Recent Errors"
            stacked = false
          }
        }
      ]
    )
  })
}

# Lambda alarms
resource "aws_cloudwatch_metric_alarm" "lambda_errors" {
  count = var.compute_platform == "lambda" ? 1 : 0

  alarm_name          = "${var.stack_name}-lambda-high-errors"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "Errors"
  namespace           = "AWS/Lambda"
  period              = 300
  statistic           = "Sum"
  threshold           = var.lambda_error_threshold
  alarm_description   = "Lambda function error rate is too high"
  alarm_actions       = [aws_sns_topic.alerts.arn]
  ok_actions          = [aws_sns_topic.alerts.arn]
  treat_missing_data  = "notBreaching"

  dimensions = {
    FunctionName = var.function_name
  }

  tags = var.tags
}

resource "aws_cloudwatch_metric_alarm" "lambda_throttles" {
  count = var.compute_platform == "lambda" ? 1 : 0

  alarm_name          = "${var.stack_name}-lambda-throttles"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "Throttles"
  namespace           = "AWS/Lambda"
  period              = 300
  statistic           = "Sum"
  threshold           = 10
  alarm_description   = "Lambda function is being throttled"
  alarm_actions       = [aws_sns_topic.alerts.arn]
  ok_actions          = [aws_sns_topic.alerts.arn]

  dimensions = {
    FunctionName = var.function_name
  }

  tags = var.tags
}

resource "aws_cloudwatch_metric_alarm" "lambda_duration" {
  count = var.compute_platform == "lambda" ? 1 : 0

  alarm_name          = "${var.stack_name}-lambda-high-duration"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "Duration"
  namespace           = "AWS/Lambda"
  period              = 300
  statistic           = "Average"
  threshold           = var.lambda_duration_threshold
  alarm_description   = "Lambda function duration is too high"
  alarm_actions       = [aws_sns_topic.alerts.arn]
  ok_actions          = [aws_sns_topic.alerts.arn]

  dimensions = {
    FunctionName = var.function_name
  }

  tags = var.tags
}

# Database alarms
resource "aws_cloudwatch_metric_alarm" "db_cpu" {
  alarm_name          = "${var.stack_name}-db-high-cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "CPUUtilization"
  namespace           = "AWS/RDS"
  period              = 300
  statistic           = "Average"
  threshold           = var.db_cpu_threshold
  alarm_description   = "Database CPU utilization is too high"
  alarm_actions       = [aws_sns_topic.alerts.arn]
  ok_actions          = [aws_sns_topic.alerts.arn]

  dimensions = {
    DBClusterIdentifier = var.db_cluster_id
  }

  tags = var.tags
}

resource "aws_cloudwatch_metric_alarm" "db_connections" {
  alarm_name          = "${var.stack_name}-db-high-connections"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "DatabaseConnections"
  namespace           = "AWS/RDS"
  period              = 300
  statistic           = "Average"
  threshold           = var.db_connection_threshold
  alarm_description   = "Database connection count is too high"
  alarm_actions       = [aws_sns_topic.alerts.arn]
  ok_actions          = [aws_sns_topic.alerts.arn]

  dimensions = {
    DBClusterIdentifier = var.db_cluster_id
  }

  tags = var.tags
}

resource "aws_cloudwatch_metric_alarm" "db_capacity" {
  alarm_name          = "${var.stack_name}-db-capacity-warning"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "ServerlessDatabaseCapacity"
  namespace           = "AWS/RDS"
  period              = 300
  statistic           = "Average"
  threshold           = 1.5 # 75% of max 2.0 ACU
  alarm_description   = "Aurora Serverless capacity approaching limit"
  alarm_actions       = [aws_sns_topic.alerts.arn]
  ok_actions          = [aws_sns_topic.alerts.arn]

  dimensions = {
    DBClusterIdentifier = var.db_cluster_id
  }

  tags = var.tags
}

# Log metric filters
resource "aws_cloudwatch_log_metric_filter" "errors" {
  name           = "${var.stack_name}-error-count"
  log_group_name = var.log_group_name
  pattern        = "[time, request_id, level = ERROR*, ...]"

  metric_transformation {
    name      = "ErrorCount"
    namespace = "CUDly"
    value     = "1"
  }
}

resource "aws_cloudwatch_log_metric_filter" "recommendations_fetched" {
  name           = "${var.stack_name}-recommendations-fetched"
  log_group_name = var.log_group_name
  pattern        = "[time, request_id, level, msg=\"Recommendations fetched\", count]"

  metric_transformation {
    name      = "RecommendationsFetched"
    namespace = "CUDly"
    value     = "$count"
  }
}

resource "aws_cloudwatch_log_metric_filter" "purchases_executed" {
  name           = "${var.stack_name}-purchases-executed"
  log_group_name = var.log_group_name
  pattern        = "[time, request_id, level, msg=\"Purchase executed\", ...]"

  metric_transformation {
    name      = "PurchasesExecuted"
    namespace = "CUDly"
    value     = "1"
  }
}

# Application error alarm based on log metrics
resource "aws_cloudwatch_metric_alarm" "application_errors" {
  alarm_name          = "${var.stack_name}-application-errors"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "ErrorCount"
  namespace           = "CUDly"
  period              = 300
  statistic           = "Sum"
  threshold           = var.app_error_threshold
  alarm_description   = "Application error rate is too high"
  alarm_actions       = [aws_sns_topic.alerts.arn]
  ok_actions          = [aws_sns_topic.alerts.arn]
  treat_missing_data  = "notBreaching"

  tags = var.tags
}

# X-Ray tracing (if enabled)
resource "aws_xray_sampling_rule" "cudly" {
  count = var.enable_xray ? 1 : 0

  rule_name      = "${var.stack_name}-sampling"
  priority       = 1000
  version        = 1
  reservoir_size = 1
  fixed_rate     = var.xray_sampling_rate
  url_path       = "*"
  host           = "*"
  http_method    = "*"
  service_type   = "*"
  service_name   = var.stack_name
  resource_arn   = "*"

  tags = var.tags
}

# CloudWatch Insights queries
resource "aws_cloudwatch_query_definition" "errors_by_type" {
  name = "${var.stack_name}/errors-by-type"

  log_group_names = [var.log_group_name]

  query_string = <<-QUERY
    fields @timestamp, @message
    | filter @message like /ERROR/
    | parse @message /ERROR: (?<error_type>.*?):/
    | stats count() by error_type
    | sort count desc
  QUERY
}

resource "aws_cloudwatch_query_definition" "slow_requests" {
  name = "${var.stack_name}/slow-requests"

  log_group_names = [var.log_group_name]

  query_string = <<-QUERY
    fields @timestamp, @message, @duration
    | filter @type = "REPORT"
    | filter @duration > ${var.slow_request_threshold}
    | sort @duration desc
    | limit 20
  QUERY
}

resource "aws_cloudwatch_query_definition" "request_volume" {
  name = "${var.stack_name}/request-volume"

  log_group_names = [var.log_group_name]

  query_string = <<-QUERY
    fields @timestamp
    | filter @type = "REPORT"
    | stats count() as request_count by bin(5m)
  QUERY
}
