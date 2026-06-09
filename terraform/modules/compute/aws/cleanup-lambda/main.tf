# IAM role for cleanup Lambda
resource "aws_iam_role" "cleanup" {
  name = "${var.stack_name}-cleanup-lambda"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "lambda.amazonaws.com"
      }
    }]
  })

  tags = var.tags
}

# Basic Lambda execution policy
resource "aws_iam_role_policy_attachment" "cleanup_basic" {
  role       = aws_iam_role.cleanup.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

# VPC execution policy
resource "aws_iam_role_policy_attachment" "cleanup_vpc" {
  role       = aws_iam_role.cleanup.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"
}

# Policy to read database password from Secrets Manager
resource "aws_iam_role_policy" "cleanup_secrets" {
  name = "${var.stack_name}-cleanup-secrets"
  role = aws_iam_role.cleanup.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "secretsmanager:GetSecretValue"
      ]
      Resource = var.db_password_secret_arn
    }]
  })
}

# CloudWatch log group
resource "aws_cloudwatch_log_group" "cleanup" {
  name              = "/aws/lambda/${var.stack_name}-cleanup"
  retention_in_days = 90
  tags              = var.tags
}

# Lambda function
resource "aws_lambda_function" "cleanup" {
  function_name = "${var.stack_name}-cleanup"
  role          = aws_iam_role.cleanup.arn
  timeout       = var.timeout
  memory_size   = var.memory_size

  # Use container image
  package_type = "Image"
  image_uri    = var.image_uri

  image_config {
    command = ["/app/cleanup-lambda"]
  }

  environment {
    variables = {
      DB_HOST            = var.db_host
      DB_PORT            = "5432"
      DB_NAME            = var.db_name
      DB_USER            = var.db_username
      DB_PASSWORD_SECRET = var.db_password_secret_arn
      DB_SSL_MODE        = "require"
      SECRET_PROVIDER    = "aws"
    }
  }

  vpc_config {
    subnet_ids         = var.subnet_ids
    security_group_ids = var.security_group_ids
  }

  tags = var.tags

  depends_on = [
    aws_cloudwatch_log_group.cleanup,
    aws_iam_role_policy_attachment.cleanup_basic,
    aws_iam_role_policy_attachment.cleanup_vpc,
    aws_iam_role_policy.cleanup_secrets
  ]
}

# EventBridge rule to run cleanup on schedule
resource "aws_cloudwatch_event_rule" "cleanup_schedule" {
  name                = "${var.stack_name}-cleanup"
  description         = "Trigger cleanup of expired sessions and executions"
  schedule_expression = var.schedule_expression
  tags                = var.tags
}

# EventBridge target
resource "aws_cloudwatch_event_target" "cleanup" {
  rule      = aws_cloudwatch_event_rule.cleanup_schedule.name
  target_id = "cleanup-lambda"
  arn       = aws_lambda_function.cleanup.arn

  # Pass empty event (dryRun defaults to false)
  input = jsonencode({
    dryRun = false
  })
}

# Permission for EventBridge to invoke Lambda
resource "aws_lambda_permission" "cleanup_eventbridge" {
  statement_id  = "AllowEventBridgeInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.cleanup.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.cleanup_schedule.arn
}

# CloudWatch alarm for cleanup failures
resource "aws_cloudwatch_metric_alarm" "cleanup_errors" {
  alarm_name          = "${var.stack_name}-cleanup-errors"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "Errors"
  namespace           = "AWS/Lambda"
  period              = 300
  statistic           = "Sum"
  threshold           = 0
  treat_missing_data  = "notBreaching"

  dimensions = {
    FunctionName = aws_lambda_function.cleanup.function_name
  }

  alarm_description = "Cleanup Lambda function errors"
  tags              = var.tags
}
