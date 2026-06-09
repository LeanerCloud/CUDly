# ==============================================
# Migration-failure alarm
# ==============================================
#
# A dirty/failed schema migration is non-fatal at runtime (the app fail-opens
# and keeps serving requests that don't need the unapplied columns -- see
# internal/server/app.go ensureDB and specs/migration-resilience.md). That
# means a broken migration is INVISIBLE to the AWS/Lambda Errors metric: the
# function returns 200s while handlers needing the new schema 500 at query
# time. This metric filter + alarm makes that failure mode observable.
#
# The filter matches the literal the app logs on a failed migration attempt
# (internal/server/app.go: "Migration failed - app continuing with existing
# schema"). The quoted substring pattern matches the message regardless of the
# leading emoji / surrounding text, so a copy tweak to the log line that keeps
# the "Migration failed" phrase keeps the alarm working.
#
# Notification target is optional (var.alarm_sns_topic_arn). When no topic is
# supplied the alarm still exists and transitions to ALARM state -- visible in
# the console and queryable -- it just has no notification action. This mirrors
# the module's other optional, count/conditional-gated wiring and avoids
# inventing new SNS notification infrastructure here.

resource "aws_cloudwatch_log_metric_filter" "migration_failed" {
  name           = "${var.stack_name}-migration-failed"
  log_group_name = aws_cloudwatch_log_group.lambda.name

  # Quoted term = substring match anywhere in the log event. Matches the
  # app.go log line "⚠️ Migration failed — app continuing with existing schema".
  pattern = "\"Migration failed\""

  metric_transformation {
    name          = "MigrationFailed"
    namespace     = "CUDly"
    value         = "1"
    default_value = "0"
  }
}

resource "aws_cloudwatch_metric_alarm" "migration_failed" {
  alarm_name          = "${var.stack_name}-migration-failed"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = aws_cloudwatch_log_metric_filter.migration_failed.metric_transformation[0].name
  namespace           = aws_cloudwatch_log_metric_filter.migration_failed.metric_transformation[0].namespace
  period              = 300
  statistic           = "Sum"
  threshold           = 0
  alarm_description   = "A database migration failed on Lambda cold start (schema_migrations dirty or migration error/timeout). The app fail-opens, so AWS/Lambda Errors stays clean while schema-dependent handlers 500. See specs/migration-resilience.md for recovery."
  alarm_actions       = var.alarm_sns_topic_arn
  ok_actions          = var.alarm_sns_topic_arn
  treat_missing_data  = "notBreaching"

  tags = var.tags
}
